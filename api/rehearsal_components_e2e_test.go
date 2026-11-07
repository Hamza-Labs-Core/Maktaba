//go:build unix

package main

// Update-rehearsal scenario 12 (Epic 51 story 51.03; docs/update-rehearsal.md): an update
// changes nothing a home runs. The small seed's home on the previous release, with the TV
// slots on, the relay off, one extension on, one stage turned off by an operator and the
// HDHomeRun device off: the supervisor's processes, job_stages, extensions and hdhr_device
// are the same before and after the update; the components the previous release seeded
// (v0.3.81 on: the seed ran when the seeded home was made) keep their want, generation and
// writer, with the wants story 51.03 §2 gives this home; the loops the previous release ran
// are running in component_status and no other switchable loop is; a restart seeds nothing
// again. (The first seed, from a release before the component model, was rehearsed from
// v0.3.80 when v0.3.81 was tagged; componentseed's tests keep it.)

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// componentsHome is what scenario 12 compares before and after the update.
type componentsHome struct {
	Processes  []string // role/instance wanted=.. running=..
	Stages     []string // stage enabled disabled_reason paused
	Extensions []string // name enabled
	HDHR       string
}

// supervisorProcesses are the supervisor's processes (GET /processes on its admin
// address): role/instance, wanted, running.
func (m *appMachine) supervisorProcesses() ([]string, bool) {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/processes", m.sup))
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	var snap struct {
		Processes []struct {
			Role     string `json:"role"`
			Instance string `json:"instance"`
			Wanted   bool   `json:"wanted"`
			State    string `json:"state"`
		} `json:"processes"`
	}
	if json.NewDecoder(resp.Body).Decode(&snap) != nil {
		return nil, false
	}
	var out []string
	settled := true
	for _, p := range snap.Processes {
		switch p.State {
		case "starting", "unhealthy", "backoff":
			settled = false // starting, waiting on a step, restarting
		}
		row := fmt.Sprintf("%s/%s wanted=%v running=%v", p.Role, p.Instance, p.Wanted, p.State == "running")
		if p.State == "held" {
			row += " held" // its component is off: it runs nothing
		}
		out = append(out, row)
	}
	sort.Strings(out)
	return out, settled && len(out) > 0
}

// settledProcesses waits until the supervisor's processes are settled and the same twice
// in a row (a TV slot's start, a restart after the update).
func (m *appMachine) settledProcesses(d time.Duration) []string {
	m.r.t.Helper()
	var last []string
	waitFor(m.r.t, m.name+"'s processes settled", d, func() bool {
		now, ok := m.supervisorProcesses()
		same := ok && reflect.DeepEqual(now, last)
		last = now
		if !same {
			time.Sleep(2 * time.Second)
		}
		return same
	})
	return last
}

func stringsOf(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (m *appMachine) componentsHome(db *sql.DB) componentsHome {
	t := m.r.t
	t.Helper()
	h := componentsHome{Processes: m.settledProcesses(3 * time.Minute)}
	// the catch-up stage is new with this release (slot 0178, hidden): not a stage the
	// previous release had
	h.Stages = stringsOf(t, db, `SELECT stage || ' enabled=' || enabled || ' reason=' || coalesce(disabled_reason, '-') ||
		' paused=' || paused FROM job_stages WHERE stage <> 'component_catchup' ORDER BY stage`)
	h.Extensions = stringsOf(t, db, `SELECT name || ' enabled=' || enabled FROM extensions ORDER BY name`)
	if v := stringsOf(t, db, `SELECT enabled::text FROM hdhr_device`); len(v) > 0 {
		h.HDHR = v[0]
	}
	return h
}

func diffLists(t *testing.T, what string, before, after []string) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Errorf("%s changed with the update:\nbefore: %s\nafter:  %s", what, strings.Join(before, "; "), strings.Join(after, "; "))
	}
}

// keptProcesses is after's rows for the processes before had. A process new with this
// release is no change to what runs today when it stays held and not wanted (the language
// model server built dormant for an llm that is off, story 51.04), or when its component is
// one the home's bundle has (story 52.17: a new component starts as the bundle says; the
// log index, 53.09, is on in Full).
func keptProcesses(t *testing.T, before, after []string, bundle string) []string {
	t.Helper()
	cat := components.Default()
	had := map[string]bool{}
	for _, row := range before {
		had[strings.Fields(row)[0]] = true
	}
	var out []string
	for _, row := range after {
		f := strings.Fields(row)
		role, instance, _ := strings.Cut(f[0], "/")
		inBundle := false
		if c, ok := cat.ByProcess(role, instance); ok {
			inBundle = slices.Contains(c.Bundles, bundle)
		}
		switch {
		case had[f[0]]:
			out = append(out, row)
		case inBundle && strings.HasPrefix(strings.TrimPrefix(row, f[0]+" "), "wanted=true"):
		case !strings.HasSuffix(row, " wanted=false running=false held"):
			t.Errorf("a process new with this release must be held and not wanted, unless the home's bundle has it: %s", row)
		}
	}
	return out
}

// keptStages is after's rows for the stages before had. A stage new with this release (a
// migration adds it: the second listen since slot 0174) is no change to what runs today,
// but it must come in on, not off or paused.
func keptStages(t *testing.T, before, after []string) []string {
	t.Helper()
	had := map[string]bool{}
	for _, row := range before {
		had[strings.Fields(row)[0]] = true
	}
	var out []string
	for _, row := range after {
		f := strings.Fields(row)
		if had[f[0]] {
			out = append(out, row)
		} else if row != f[0]+" enabled=true reason=- paused=false" {
			t.Errorf("job_stages: the new stage %s came in %s", f[0], row)
		}
	}
	return out
}

// =====================================================================================
// 12 No behaviour change: existing installs keep running what they run.
// =====================================================================================

func TestUpdateRehearsal12NoBehaviourChange(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	if !tvSlotPortsFree() {
		r.skipUnlessRequired("the TV slots' fixed ports (8791-8793, 9111-9113) are taken: another Maktaba airs TV on " +
			"this machine, and the rehearsal's TV would meet it")
	}
	home, _ := r.seeded("home", profile("small"))
	home.startWithTV(k.prev, 8*time.Minute)
	db := home.superDB("maktaba")
	// the home's own choices, made on the previous release: Plex on (in Settings: the
	// components' apply writes ext.plex and its extensions mirror together; a write to
	// extensions behind the API's back is repaired only at its next start, which here
	// would be the update's), one stage turned off on the Queues page, the HDHomeRun
	// device off (the relay is off: no [cloud])
	owner := home.signIn()
	if resp, body := owner.b.do("POST", home.webURL()+"/api/components/apply", map[string]any{
		"changes": []map[string]string{{"id": "ext.plex", "want": "enabled"}}}, owner.csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("Plex on (components apply): %d %s", resp.StatusCode, body)
	}
	for _, q := range []string{
		`UPDATE job_stages SET enabled = false, disabled_reason = 'operator' WHERE stage = 'youtube_backfill'`,
		`INSERT INTO hdhr_device (id, device_id, enabled) VALUES (1, 'rehearsal', false)
			ON CONFLICT (id) DO UPDATE SET enabled = false`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	before := home.componentsHome(db)
	r.logf("before the update: %d processes, %d stages, extensions %v, hdhr %s", len(before.Processes),
		len(before.Stages), before.Extensions, before.HDHR)

	var seeded string
	var seededBefore time.Time
	_ = db.QueryRow(`SELECT COALESCE(seeded_release, ''), seeded_at FROM component_meta`).Scan(&seeded, &seededBefore)
	compBefore := map[string]string{}
	for _, row := range stringsOf(t, db, `SELECT id || ' ' || want || ' ' || generation || ' ' || COALESCE(updated_by, '') FROM components`) {
		id, rest, _ := strings.Cut(row, " ")
		compBefore[id] = rest
	}
	// the loops the previous release runs here, as component_status reports them on the
	// API and streaming (the pipeline is the rehearsal's stand-in): read once every
	// switchable component it left enabled with a loop on those hosts reports one running,
	// so the snapshot is the previous release's own, with no list to keep per release
	cat := components.Default()
	var waitLoops []string
	for _, c := range cat.Components() {
		if !c.Switchable || strings.Fields(compBefore[c.ID]+" ")[0] != components.WantEnabled {
			continue
		}
		for _, l := range c.Loops {
			if host, _, _ := strings.Cut(l, ":"); host == "api" || host == "streaming" {
				waitLoops = append(waitLoops, host+" "+c.ID)
			}
		}
	}
	runningLoops := func() map[string]bool {
		out := map[string]bool{}
		for _, s := range stringsOf(t, db, `SELECT part || ' ' || id FROM component_status
			WHERE machine = 'home' AND phase = 'running' AND part IN ('api', 'streaming')`) {
			out[s] = true
		}
		return out
	}
	loopsBefore := map[string]bool{}
	waitFor(t, "the previous release's loops in component_status", 2*time.Minute, func() bool {
		loopsBefore = runningLoops()
		for _, k := range waitLoops {
			if !loopsBefore[k] {
				return false
			}
		}
		return true
	})
	r.logf("before the update: loops running %v", sortedKeysOf(loopsBefore))
	logFrom := len(home.log.String())

	run, mon, _ := r.update(home, k.curSpec(), 20*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.cur, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 3*time.Minute)
	r.budget(mon, "an update of a seeded home (previous release -> this commit)", 2*time.Minute)
	home.assertServing(k.cur)

	after := home.componentsHome(db)
	// the seeded home is a Full one (checked below)
	diffLists(t, "the supervisor's processes", before.Processes, keptProcesses(t, before.Processes, after.Processes, "full"))
	diffLists(t, "job_stages", before.Stages, keptStages(t, before.Stages, after.Stages))
	diffLists(t, "extensions", before.Extensions, after.Extensions)
	if before.HDHR != after.HDHR {
		t.Errorf("hdhr_device.enabled %s -> %s", before.HDHR, after.HDHR)
	}

	// the components as the previous release left them: the seed (once, bundle full) and
	// the owner's changes since (the TV slots on, Plex on). The update writes none of them.
	if seeded == "" {
		t.Fatalf("component_meta: the previous release %s did not seed the components", k.prev)
	}
	var seededAt time.Time
	var bundle sql.NullString
	if err := db.QueryRow(`SELECT seeded_at, bundle FROM component_meta`).Scan(&seededAt, &bundle); err != nil {
		t.Fatalf("component_meta: %v", err)
	}
	if !seededAt.Equal(seededBefore) {
		t.Errorf("seeded_at moved in the update: %s -> %s", seededBefore, seededAt)
	}
	if bundle.String != "full" {
		t.Errorf("bundle %q, want full", bundle.String)
	}
	wants := map[string]string{}
	gens := map[string]int64{}
	rows, err := db.Query(`SELECT id, want, generation, updated_by FROM components`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, want, by string
		var g int64
		if err := rows.Scan(&id, &want, &g, &by); err != nil {
			t.Fatal(err)
		}
		wants[id], gens[id] = want, g
		if was, ok := compBefore[id]; ok && was != fmt.Sprintf("%s %d %s", want, g, by) {
			t.Errorf("%s changed in the update: %s -> %s %d %s", id, was, want, g, by)
		}
	}
	rows.Close()
	for id := range compBefore {
		if _, ok := wants[id]; !ok {
			t.Errorf("%s's row went in the update (it was %s)", id, compBefore[id])
		}
	}
	for id, want := range map[string]string{
		"livetv.slots": "enabled", "livetv.inprocess": "installed", "livetv.hdhomerun": "installed",
		"ext.plex": "enabled", "ext.dizquetv": "installed", "ext.cloudflare_stream": "installed",
		"cloud.link": "installed", "cloud.relay": "installed",
		"youtube":        "enabled", // one of its four stages off: the others still run
		"discovery.mdns": "enabled", "backups": "enabled", "telemetry": "enabled", "cluster": "enabled",
		"queue_health": "enabled", "analytics": "enabled", "updates.auto": "enabled", "livetv": "enabled",
		"media.thumbnails": "enabled", "transcription": "enabled",
	} {
		if wants[id] != want {
			t.Errorf("%s = %q, want %q", id, wants[id], want)
		}
	}

	// the loops the previous release ran are running, and no other switchable loop of those
	// hosts, but a component's this release adds when the home's bundle has it (story 52.17's
	// rule): one the previous release had no row for (its seed writes every component of its
	// catalog that has a loop)
	running := map[string]bool{}
	waitFor(t, "the loops' component_status", 2*time.Minute, func() bool {
		running = runningLoops()
		for k := range loopsBefore {
			if !running[k] {
				return false
			}
		}
		return true
	})
	for _, c := range cat.Components() {
		if !c.Switchable {
			continue
		}
		_, had := compBefore[c.ID]
		added := !had && slices.Contains(c.Bundles, "full")
		for _, l := range c.Loops {
			host, _, _ := strings.Cut(l, ":")
			k := host + " " + c.ID
			if (host == "api" || host == "streaming") && running[k] && !loopsBefore[k] && !added {
				t.Errorf("%s runs on %s after the update; the previous release did not run it here", c.ID, host)
			}
		}
	}

	// a restart seeds nothing again: seeded_at and every generation stay
	home.stop()
	home.start()
	home.waitServing(k.cur, 4*time.Minute)
	var again time.Time
	if err := db.QueryRow(`SELECT seeded_at FROM component_meta`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if !again.Equal(seededAt) {
		t.Errorf("seeded_at moved on a restart: %s -> %s", seededAt, again)
	}
	for _, s := range stringsOf(t, db, `SELECT id || ' ' || generation FROM components`) {
		id, g, _ := strings.Cut(s, " ")
		if g != fmt.Sprint(gens[id]) {
			t.Errorf("%s's generation moved on a restart: %d -> %s", id, gens[id], g)
		}
	}
	if n := queryInt(t, db, `SELECT count(*) FROM audit_log WHERE action = 'component.seed'
		AND NOT COALESCE((payload::jsonb ->> 'new')::boolean, false)`); n != 1 {
		t.Errorf("%d seeds in the audit log, want 1", n)
	}
	// story 52.17: the components this release adds over the seeded one (those the previous
	// release had no row for) got one seed of their own, as the home's bundle says (on in
	// it, off outside it; an asset outside it is downloaded when a component that seed
	// turned on requires it), and a restart adds no other. A component the previous release
	// seeded keeps its row as it was (checked above), whatever the bundle says: its first
	// seed found an asset absent when it was not on disk.
	if n := queryInt(t, db, `SELECT count(*) FROM audit_log WHERE action = 'component.seed'
		AND COALESCE((payload::jsonb ->> 'new')::boolean, false)`); n > 1 {
		t.Errorf("%d seeds of new components, want at most 1", n)
	}
	newWants := map[string]string{}
	for _, s := range stringsOf(t, db, `SELECT id || ' ' || want FROM components WHERE updated_by = 'seed'`) {
		id, want, _ := strings.Cut(s, " ")
		if _, had := compBefore[id]; !had {
			newWants[id] = want
		}
	}
	r.logf("components this release added: %v", newWants)
	for id, want := range newWants {
		c, ok := cat.Get(id)
		if !ok {
			t.Errorf("the update seeded %s, no component of the catalog", id)
			continue
		}
		in := slices.Contains(c.Bundles, bundle.String)
		required := false
		for other, w := range newWants {
			if w != components.WantEnabled {
				continue
			}
			for _, req := range cat.Requires(other) {
				required = required || slices.Contains(req.AnyOf, id)
			}
		}
		switch {
		case !c.Switchable && want != components.WantEnabled,
			c.Switchable && c.Kind == components.KindAsset && (in || required) && want != components.WantInstalled,
			c.Switchable && c.Kind == components.KindAsset && !in && !required && want != components.WantAbsent,
			c.Switchable && c.Kind != components.KindAsset && in && want != components.WantEnabled,
			c.Switchable && c.Kind != components.KindAsset && !in && want != components.WantInstalled:
			t.Errorf("new component %s is %s on a %s home", id, want, bundle.String)
		}
	}
	// (the first start on the previous release found it superseded: the seeded home was
	// made with the TV slots off, and startWithTV turned them on in the components)
	if since := home.log.String()[logFrom:]; strings.Contains(since, "toml_key_superseded") {
		t.Errorf("the rehearsal's server.toml was found superseded after the update:\n%s", grepLines(since, "toml_key_superseded"))
	}
}

func grepLines(s, sub string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// keptStages: a stage new with this release is no change when it comes in on.
func TestKeptStages(t *testing.T) {
	before := []string{"extract enabled=true reason=- paused=false", "transcribe enabled=false reason=operator paused=false"}
	after := []string{"extract enabled=true reason=- paused=false", "second_listen enabled=true reason=- paused=false",
		"transcribe enabled=false reason=operator paused=false"}
	if got := keptStages(t, before, after); !reflect.DeepEqual(got, before) {
		t.Fatalf("kept %v", got)
	}
	ft := &testing.T{}
	keptStages(ft, before, append(after, "zz enabled=false reason=component_off paused=false"))
	if !ft.Failed() {
		t.Fatal("a new stage that came in off was not reported")
	}
}
