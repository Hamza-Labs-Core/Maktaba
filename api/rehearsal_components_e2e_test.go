//go:build unix

package main

// Update-rehearsal scenario 12 (Epic 51 story 51.03; docs/update-rehearsal.md): the first
// release with the component model changes nothing a home runs. The small seed's home on
// the previous release, with the TV slots on, the relay off, one extension on, one stage
// turned off by an operator and the HDHomeRun device off: the supervisor's processes,
// job_stages, extensions and hdhr_device are the same before and after the update; the
// seed ran once (bundle full) with the wants story 51.03 §2 gives this home; the loops
// the previous release ran are running in component_status and no other switchable loop
// is; a restart seeds nothing again.

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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
		out = append(out, fmt.Sprintf("%s/%s wanted=%v running=%v", p.Role, p.Instance, p.Wanted, p.State == "running"))
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
	// the catch-up stage is new with this release (slot 0173, hidden): not a stage the
	// previous release had
	h.Stages = stringsOf(t, db, `SELECT stage || ' enabled=' || enabled || ' reason=' || coalesce(disabled_reason, '-') ||
		' paused=' || paused FROM job_stages WHERE stage <> 'component_catchup' ORDER BY stage`)
	h.Extensions = stringsOf(t, db, `SELECT name || ' enabled=' || enabled FROM extensions ORDER BY name`)
	if v := stringsOf(t, db, `SELECT enabled::text FROM hdhr_device`); len(v) > 0 {
		h.HDHR = v[0]
	}
	return h
}

// loopsFullHome reads tools/rehearsal/loops-full-home.txt.
func loopsFullHome(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "tools", "rehearsal", "loops-full-home.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func diffLists(t *testing.T, what string, before, after []string) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Errorf("%s changed with the update:\nbefore: %s\nafter:  %s", what, strings.Join(before, "; "), strings.Join(after, "; "))
	}
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
	home.tvSlots = "on"
	home.start()
	home.waitServing(k.prev, 8*time.Minute)
	db := home.superDB("maktaba")
	// the home's own choices, made on the previous release: Plex on, one stage turned off
	// on the Queues page, the HDHomeRun device off (the relay is off: no [cloud])
	for _, q := range []string{
		`INSERT INTO extensions (name, enabled) VALUES ('plex', true) ON CONFLICT (name) DO UPDATE SET enabled = true`,
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

	run, mon, _ := r.update(home, k.curSpec(), 20*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.cur, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 3*time.Minute)
	r.budget(mon, "an update that seeds the components (previous release -> this commit)", 2*time.Minute)
	home.assertServing(k.cur)

	after := home.componentsHome(db)
	diffLists(t, "the supervisor's processes", before.Processes, after.Processes)
	diffLists(t, "job_stages", before.Stages, after.Stages)
	diffLists(t, "extensions", before.Extensions, after.Extensions)
	if before.HDHR != after.HDHR {
		t.Errorf("hdhr_device.enabled %s -> %s", before.HDHR, after.HDHR)
	}

	// the seed: once, bundle full, the wants of §2 for this home
	var seededAt time.Time
	var bundle, release sql.NullString
	if err := db.QueryRow(`SELECT seeded_at, bundle, seeded_release FROM component_meta`).Scan(&seededAt, &bundle, &release); err != nil {
		t.Fatalf("component_meta: %v (not seeded?)", err)
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
		if by != "seed" {
			t.Errorf("%s was written by %q, not the seed", id, by)
		}
	}
	rows.Close()
	cat := components.Default()
	for _, c := range cat.Components() {
		w, ok := wants[c.ID]
		switch {
		case !ok:
			t.Errorf("%s has no desired row", c.ID)
		case !c.Switchable && w != "enabled":
			t.Errorf("%s = %s, want enabled", c.ID, w)
		case c.Kind == components.KindAsset && w == "enabled":
			t.Errorf("asset %s enabled", c.ID)
		case c.Kind != components.KindAsset && w == "absent":
			t.Errorf("%s seeded absent", c.ID)
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

	// the loops the previous release ran are running, and no other switchable loop
	expected := map[string]bool{}
	for _, l := range loopsFullHome(t) {
		host, name, _ := strings.Cut(l, ":")
		c, ok := cat.ByLoop(host, name)
		if !ok {
			t.Fatalf("loops-full-home.txt: %s is no loop of the catalog", l)
		}
		expected[host+" "+c.ID] = true
	}
	running := map[string]bool{}
	waitFor(t, "the loops' component_status", 2*time.Minute, func() bool {
		running = map[string]bool{}
		for _, s := range stringsOf(t, db, `SELECT part || ' ' || id FROM component_status
			WHERE machine = 'home' AND phase = 'running' AND part IN ('api', 'streaming')`) {
			running[s] = true
		}
		for k := range expected {
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
		for _, l := range c.Loops {
			host, _, _ := strings.Cut(l, ":")
			k := host + " " + c.ID
			if (host == "api" || host == "streaming") && running[k] && !expected[k] {
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
	if n := queryInt(t, db, `SELECT count(*) FROM audit_log WHERE action = 'component.seed'`); n != 1 {
		t.Errorf("%d seeds in the audit log, want 1", n)
	}
	if strings.Contains(home.log.String(), "toml_key_superseded") {
		t.Errorf("the rehearsal's server.toml was found superseded:\n%s", grepLines(home.log.String(), "toml_key_superseded"))
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
