//go:build unix

package main

// Update-rehearsal scenario 15 (Epic 54, story 54.07 §8, T10, T14; docs/update-rehearsal.md):
// a home with plugins updates and rolls back, and its plugins follow.
//
//	A  dev.example.rehearsal-a, any server version, enabled: Running after every step
//	B  dev.example.rehearsal-b, a range that excludes next and after, enabled, its queue
//	   full: Waiting incompatible (never Failed) on a release it does not take, its queued
//	   jobs kept and not run; Running again after the rollback
//	C  dev.example.rehearsal-c, turned off by the admin (kept off): installed and kept_off
//	   after every step, as only an admin's enable turns it on
//
// A plugin's state is the home's decision, which the supervisor's plugins runner reports
// even with no pipeline (story 54.07 §8): the rehearsal's pipeline is a stand-in that runs
// no job, so the scenario reads that state; the plugins' own jobs run in the components CI
// (components-chaos, plugins-live). While the release that fails its watch runs, its API
// crash-loops by design, so B's state then is read from its status row in the database.
//
// The update to next fails its watch and rolls back to cur; the update to after passes.
// The plugins are the host's well-behaved test guest (plugind/guests/hello), packed here and
// installed through the API as an admin does; the kit's maktaba-plugind (tools/rehearsal/
// build.sh: plugins/) is the host for cur, next and after.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

const (
	rehearsalPluginA = "dev.example.rehearsal-a"
	rehearsalPluginB = "dev.example.rehearsal-b"
	rehearsalPluginC = "dev.example.rehearsal-c"
)

// pluginsKit is the kit's plugin host and test guest, or the scenario is skipped (a kit of
// a tree without the plugin host, or built where cargo was missing).
func (r *rehearsal) pluginsKit() (plugind, guest string) {
	r.t.Helper()
	dir := filepath.Join(r.kit.dir, "plugins")
	plugind, guest = filepath.Join(dir, "maktaba-plugind"), filepath.Join(dir, "hello.wasm")
	for _, f := range []string{plugind, guest} {
		if _, err := os.Stat(f); err != nil {
			r.skipUnlessRequired(fmt.Sprintf("the kit has no plugin host (tools/rehearsal/build.sh without cargo): %v", err))
		}
	}
	return plugind, guest
}

// rehearsalPluginPackage is the test guest packed as plugin id: one stage of subject on
// the server range rng.
func rehearsalPluginPackage(t *testing.T, guest, id, rng, subject string) ([]byte, string) {
	t.Helper()
	wasm, err := os.ReadFile(guest)
	if err != nil {
		t.Fatal(err)
	}
	name := "Rehearsal " + strings.ToUpper(id[len(id)-1:])
	manifest := fmt.Sprintf(`schema = 1
id = %q
version = "1.0.0"
plugin_api = "1.0"
maktaba = %q
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[[stage]]
name = "greet"
subject = %q
export = "run-greet"
version = 1
`, id, rng, subject)
	files := map[string][]byte{
		componentpack.PluginManifestName: []byte(manifest),
		"i18n/en.json":                   []byte(fmt.Sprintf(`{"plugin.name":%q,"plugin.summary":"The update rehearsal's plugin."}`, name)),
		"i18n/ar.json":                   []byte(fmt.Sprintf(`{"plugin.name":%q,"plugin.summary":"إضافة تمرين التحديث."}`, name)),
		"plugin.wasm":                    wasm,
	}
	var buf bytes.Buffer
	if _, err := componentpack.BuildPlugin(&buf, files, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), name
}

// adminJSON is an admin's JSON call on the home's web (signed in again across restarts).
func (a *ownerSession) adminJSON(method, path string, body any) (int, []byte) {
	code, out := a.b.try(method, a.m.webURL()+path, body, a.csrf)
	if code == http.StatusUnauthorized {
		a.login()
		code, out = a.b.try(method, a.m.webURL()+path, body, a.csrf)
	}
	return code, out
}

// installPlugin installs a package as the admin's dialog does, turned on.
func (a *ownerSession) installPlugin(pkg []byte, name string) {
	t := a.m.r.t
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range map[string]string{"grants": "[]", "libraries": "[]", "all_libraries": "true", "enable": "true",
		"confirm_name": name} {
		_ = w.WriteField(k, v)
	}
	part, _ := w.CreateFormFile("file", "plugin.mkp")
	_, _ = part.Write(pkg)
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, a.m.webURL()+"/api/plugins/install", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for k, v := range a.csrf {
		req.Header.Set(k, v)
	}
	resp, err := a.b.c.Do(req)
	if err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("install %s: %d %s", name, resp.StatusCode, out.String())
	}
}

// pluginView is what GET /api/plugins/{id} says of a plugin.
type pluginView struct {
	Want    string  `json:"want"`
	Phase   string  `json:"phase"`
	Reason  string  `json:"reason"`
	KeptOff *string `json:"kept_off"`
	Version string  `json:"version"`
}

func (a *ownerSession) plugin(id string) (pluginView, bool) {
	code, body := a.get("/api/plugins/" + id)
	var v pluginView
	if code != http.StatusOK || json.Unmarshal(body, &v) != nil {
		return v, false
	}
	return v, true
}

// waitPlugin waits until id's view passes ok; the last view.
func (a *ownerSession) waitPlugin(id, what string, d time.Duration, ok func(pluginView) bool) pluginView {
	a.m.r.t.Helper()
	var last pluginView
	waitFor(a.m.r.t, id+" "+what, d, func() bool {
		v, read := a.plugin(id)
		if read {
			last = v
		}
		if read && ok(v) {
			return true
		}
		time.Sleep(time.Second)
		return false
	})
	return last
}

// pluginWatch records the phases and reasons a plugin goes through (each once, in order).
type pluginWatch struct {
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	seen []string
}

// watchPlugin records the phases of id's status row on the home that the supervisor's
// plugins runner writes (part plugins), read from the database: it is there while the API
// is down, as it is while a release that fails its watch runs.
func watchPlugin(home *appMachine, id string) *pluginWatch {
	w := &pluginWatch{stop: make(chan struct{}), done: make(chan struct{})}
	db := home.superDB("maktaba")
	go func() {
		defer close(w.done)
		for {
			select {
			case <-w.stop:
				return
			case <-time.After(time.Second):
			}
			var phase string
			var raw []byte
			if db.QueryRow(`SELECT phase, conditions FROM component_status WHERE machine = 'home' AND id = $1
				AND part = 'plugins'`, "plugin."+id).Scan(&phase, &raw) == nil {
				var conds []struct{ Status, Reason string }
				_ = json.Unmarshal(raw, &conds)
				reason := ""
				for _, c := range conds {
					if c.Status == "false" && c.Reason != "" {
						reason = c.Reason
						break
					}
				}
				s := phase + "/" + reason
				w.mu.Lock()
				if len(w.seen) == 0 || w.seen[len(w.seen)-1] != s {
					w.seen = append(w.seen, s)
				}
				w.mu.Unlock()
			}
		}
	}()
	return w
}

func (w *pluginWatch) end() []string {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.seen)
}

// pluginJobs is the count of a plugin's stage's jobs by state.
func pluginJobs(t *testing.T, home *appMachine, id string) map[string]int64 {
	t.Helper()
	db := home.superDB("maktaba")
	defer db.Close()
	rows, err := db.Query(`SELECT j.state, count(*) FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
		WHERE s.extension = $1 GROUP BY j.state`, "plugin:"+id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			t.Fatal(err)
		}
		out[st] = n
	}
	return out
}

func total(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}

// queuePluginJobs enqueues id's stage for n of the seed's videos; how many it queued.
func queuePluginJobs(t *testing.T, home *appMachine, id string, n int) int {
	t.Helper()
	db := home.superDB("maktaba")
	defer db.Close()
	var stage string
	if err := db.QueryRow(`SELECT stage FROM job_stages WHERE extension = $1`, "plugin:"+id).Scan(&stage); err != nil {
		t.Fatalf("%s's stage: %v", id, err)
	}
	rows, err := db.Query(`SELECT id::text FROM videos ORDER BY id LIMIT $1`, n)
	if err != nil {
		t.Fatal(err)
	}
	var vids []string
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		vids = append(vids, v)
	}
	rows.Close()
	for _, v := range vids {
		if _, err := db.Exec(`SELECT processing_jobs_enqueue('video', $1::uuid, $2, 100, true, false,
			'{"why":"rehearsal","by":"system"}'::jsonb, NULL, NULL, NULL)`, v, stage); err != nil {
			t.Fatalf("enqueue %s: %v", stage, err)
		}
	}
	return len(vids)
}

// homeWithPlugins is the small seed's home on cur with plugins A, B and C as the scenario
// describes them.
func (r *rehearsal) homeWithPlugins() (*appMachine, *ownerSession) {
	r.t.Helper()
	k := r.kit
	plugind, guest := r.pluginsKit()
	home, _ := r.homeOnPrev(profile("small"))
	home.env = append(home.env, "MAKTABA_PLUGIND_BIN="+plugind)
	home.installByHand(k.curSpec())
	home.waitServing(k.cur, 10*time.Minute)
	admin := home.signIn()
	// the test guest is unsigned: the admin allows unreviewed plugins, with the password
	if code, body := admin.adminJSON("PUT", "/api/plugins/policy", map[string]any{"allow_unreviewed": true,
		"password": ownerPass}); code != http.StatusOK {
		r.t.Fatalf("allow unreviewed plugins: %d %s", code, body)
	}
	// a runner without user namespaces for the plugin host (Ubuntu's AppArmor restriction)
	// is a Container-level machine (59.07): the admin allows container-only isolation, so
	// the rehearsal follows the plugins through the update, not the hold
	if code, body := admin.adminJSON("PUT", "/api/plugins/isolation", map[string]any{"allow": true,
		"password": ownerPass}); code != http.StatusOK {
		r.t.Fatalf("allow container-only isolation: %d %s", code, body)
	}
	for _, p := range []struct{ id, rng, subject string }{
		{rehearsalPluginA, ">=0.0.1", "catalog"},
		{rehearsalPluginB, ">=0.0.1, <" + k.next, "video"},
		{rehearsalPluginC, ">=0.0.1", "catalog"},
	} {
		pkg, name := rehearsalPluginPackage(r.t, guest, p.id, p.rng, p.subject)
		admin.installPlugin(pkg, name)
	}
	// the stand-in pipeline registers no stage: their rows as the worker's graph sync writes
	// them (story 54.06), so the queue holds their jobs as a home's does
	db := home.superDB("maktaba")
	if _, err := db.Exec(`INSERT INTO worker_pools (pool, label, scope, size, default_size)
		VALUES ('plugins', 'Plugins', 'global', 1, 1) ON CONFLICT DO NOTHING`); err != nil {
		r.t.Fatal(err)
	}
	for _, p := range []struct{ id, subject string }{{rehearsalPluginA, "catalog"}, {rehearsalPluginB, "video"},
		{rehearsalPluginC, "catalog"}} {
		q := plugins.QueueName(p.id, "greet")
		if _, err := db.Exec(`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane, extension)
			VALUES ($1, $1, ARRAY[$2], 'plugins', 'media', $3) ON CONFLICT (stage) DO NOTHING`, q, p.subject,
			"plugin:"+p.id); err != nil {
			r.t.Fatalf("%s's stage row: %v", p.id, err)
		}
	}
	if code, body := admin.adminJSON("POST", "/api/plugins/"+rehearsalPluginC+"/disable", map[string]any{}); code != http.StatusOK {
		r.t.Fatalf("turn %s off: %d %s", rehearsalPluginC, code, body)
	}
	for _, id := range []string{rehearsalPluginA, rehearsalPluginB} {
		admin.waitPlugin(id, "its gate passing on "+k.cur, 5*time.Minute, gatePasses)
	}
	r.logf("plugins A and B pass their gates on %s; C is kept off", k.cur)
	return home, admin
}

// gatePasses: the plugin is on and Running, nothing gating it.
func gatePasses(v pluginView) bool {
	return v.Want == "enabled" && v.Reason == "" && v.Phase == "running"
}

// incompatible: the plugin waits incompatible, still the admin's choice.
func incompatible(v pluginView) bool {
	return v.Want == "enabled" && v.Phase == "waiting" && v.Reason == "incompatible"
}

// keptOff fails unless C is installed and kept off.
func (r *rehearsal) keptOff(admin *ownerSession, where string) {
	r.t.Helper()
	v := admin.waitPlugin(rehearsalPluginC, "kept off "+where, 2*time.Minute, func(v pluginView) bool { return v.Want != "" })
	if v.Want != "installed" || v.KeptOff == nil {
		r.t.Errorf("%s %s: want %q, kept_off %v; it must stay off until an admin turns it on", rehearsalPluginC, where, v.Want, v.KeptOff)
	}
}

// =====================================================================================
// 15 A home with plugins updates and rolls back.
// =====================================================================================

func TestUpdateRehearsal15Plugins(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, admin := r.homeWithPlugins()
	queued := queuePluginJobs(t, home, rehearsalPluginB, 20)
	jobsBefore := pluginJobs(t, home, rehearsalPluginB)
	r.logf("B's jobs before the update: %v (%d queued now)", jobsBefore, queued)

	// the update to next, which takes no B: it fails its watch and rolls back to cur
	watchB := watchPlugin(home, rehearsalPluginB)
	run, mon, states := r.update(home, k.nextAdditiveFailing(), 25*time.Minute)
	seenB := watchB.end()
	r.logf("B on the way: %v", seenB)
	if run.State != "rolled_back" {
		t.Fatalf("the update to %s ended %s, want rolled_back: %s (steps %+v)", k.next, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 5*time.Minute)
	r.budget(mon, "a rollback after additive migrations, with plugins installed", 4*time.Minute)
	home.assertServing(k.cur)
	r.noRestore(home, states)
	if !slices.Contains(seenB, "waiting/incompatible") {
		t.Errorf("B never waited incompatible while %s ran: %v", k.next, seenB)
	}
	for _, s := range seenB {
		if strings.HasPrefix(s, "failed/") {
			t.Errorf("B failed on the way (%s): an incompatible plugin waits", s)
		}
	}
	for _, id := range []string{rehearsalPluginA, rehearsalPluginB} {
		admin.waitPlugin(id, "its gate passing after the rollback", 5*time.Minute, gatePasses)
	}
	r.keptOff(admin, "after the rollback")
	jobs := pluginJobs(t, home, rehearsalPluginB)
	if total(jobs) != total(jobsBefore) || jobs["failed"] > jobsBefore["failed"] || jobs["cancelled"] > jobsBefore["cancelled"] {
		t.Errorf("B's jobs after the rollback %v, before %v: none lost, failed or cancelled", jobs, jobsBefore)
	}

	// the update to after, which takes no B either: it passes; B waits, its queue kept
	run, mon, _ = r.update(home, k.afterSpec(), 20*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.after, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.after, 3*time.Minute)
	r.budget(mon, "an update with plugins installed", 2*time.Minute)
	home.assertServing(k.after)
	admin.waitPlugin(rehearsalPluginA, "its gate passing on "+k.after, 5*time.Minute, gatePasses)
	admin.waitPlugin(rehearsalPluginB, "waiting incompatible on "+k.after, 5*time.Minute, incompatible)
	r.keptOff(admin, "on "+k.after)
	before := pluginJobs(t, home, rehearsalPluginB)
	more := queuePluginJobs(t, home, rehearsalPluginB, 30)
	time.Sleep(30 * time.Second)
	after := pluginJobs(t, home, rehearsalPluginB)
	if after["done"] != before["done"] || after["failed"] != before["failed"] || total(after) < total(before) {
		t.Errorf("B ran or lost jobs while incompatible: before %v, after %v (%d queued)", before, after, more)
	}
	r.summary(fmt.Sprintf("15: plugins follow the update and the rollback (B on the way: %s)", strings.Join(seenB, " → ")))
}
