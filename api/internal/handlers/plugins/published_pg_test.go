package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

type pubFixture struct {
	t     *testing.T
	r     chi.Router
	s     *svc.Service
	admin *principal.Principal
}

func newPub(t *testing.T) *pubFixture {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	adminID := "00000000-0000-0000-0000-0000000000a1"
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'owner', 'x', true)`, adminID); err != nil {
		t.Fatal(err)
	}
	s := svc.NewService(db)
	l := &pl.Lifecycle{DB: db, Components: s, Version: "0.3.90"}
	s.Plugins = l
	r := chi.NewRouter()
	(&Admin{L: l}).Mount(r)
	return &pubFixture{t: t, r: r, s: s, admin: &principal.Principal{UserID: adminID, IsAdmin: true, AccessAllLibraries: true}}
}

func (f *pubFixture) call(p *principal.Principal, method, path string) (int, map[string]any) {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func byID(list any, id string) map[string]any {
	l, _ := list.([]any)
	for _, x := range l {
		if m, _ := x.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

// Story 54.44 T01: GET /api/plugins lists the plugins Maktaba publishes in the same list
// and shape as any plugin, each with its publisher, where it came from and its own state;
// what must always run is core, not a plugin: it is not in the list and has no page.
func TestListHasMaktabaPluginsAndNoCore(t *testing.T) {
	f := newPub(t)
	code, got := f.call(f.admin, http.MethodGet, "/api/plugins")
	if code != http.StatusOK {
		t.Fatalf("list: %d %v", code, got)
	}
	if _, old := got["built_in"]; old {
		t.Fatal("the list still has a built_in class")
	}
	sub := byID(got["plugins"], "tv.maktaba.subtitles")
	if sub == nil {
		t.Fatalf("tv.maktaba.subtitles is not listed: %v", got["plugins"])
	}
	pub, _ := sub["publisher"].(map[string]any)
	names, _ := sub["names"].(map[string]any)
	name, _ := names["name"].(map[string]any)
	if pub["id"] != "maktaba" || pub["verified"] != true || sub["source"] != "release" || sub["component"] != "subtitles" ||
		sub["version"] != "0.3.90" || sub["want"] != "enabled" || name["en"] == "" || name["ar"] == "" {
		t.Fatalf("subtitles: %v", sub)
	}
	if byID(got["plugins"], "tv.maktaba.core-media") != nil {
		t.Fatal("core is listed as a plugin")
	}
	core, _ := got["core"].([]any)
	if len(core) != 1 || core[0].(map[string]any)["component"] != "core.pipeline" {
		t.Fatalf("core: %v", got["core"])
	}
	if code, _ := f.call(f.admin, http.MethodGet, "/api/plugins/tv.maktaba.core-media"); code != http.StatusNotFound {
		t.Fatalf("core has a plugin page: %d", code)
	}
	if code, _ := f.call(f.admin, http.MethodPost, "/api/plugins/tv.maktaba.core-media/disable"); code != http.StatusNotFound {
		t.Fatalf("core has a plugin switch: %d", code)
	}
	// a viewer and no one are refused before anything is read
	if code, _ := f.call(&principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}, http.MethodGet, "/api/plugins/tv.maktaba.subtitles"); code != http.StatusForbidden {
		t.Fatalf("a viewer reads a plugin's page: %d", code)
	}
	if code, _ := f.call(nil, http.MethodPost, "/api/plugins/tv.maktaba.subtitles/disable"); code != http.StatusUnauthorized {
		t.Fatalf("no one switches a plugin: %d", code)
	}
}

// Story 54.44 T02: a Maktaba plugin's switch on the plugin routes writes its component's
// desired row (one source of truth: the Components page shows the same), and only an
// admin's; a switch something on still needs is refused with what needs it.
func TestAMaktabaPluginIsSwitchedOnItsOwnRoutes(t *testing.T) {
	f := newPub(t)
	ctx := context.Background()
	viewer := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}
	if code, _ := f.call(viewer, http.MethodPost, "/api/plugins/tv.maktaba.subtitles/disable"); code != http.StatusForbidden {
		t.Fatalf("a viewer switches a plugin: %d", code)
	}
	if d, _ := f.s.Show(ctx, "subtitles"); d.Want != "enabled" {
		t.Fatalf("the viewer's refused switch changed the row: %q", d.Want)
	}
	code, got := f.call(f.admin, http.MethodPost, "/api/plugins/tv.maktaba.subtitles/disable")
	if code != http.StatusOK || got["want"] != "installed" || got["id"] != "tv.maktaba.subtitles" {
		t.Fatalf("disable: %d %v", code, got)
	}
	d, err := f.s.Show(ctx, "subtitles")
	if err != nil || d.Want != "installed" || d.Plugin != "tv.maktaba.subtitles" {
		t.Fatalf("the component's row after the plugin's switch: %+v %v", d.Want, err)
	}
	if code, got = f.call(f.admin, http.MethodPost, "/api/plugins/tv.maktaba.subtitles/enable"); code != http.StatusOK || got["want"] != "enabled" {
		t.Fatalf("enable: %d %v", code, got)
	}
	// stop and start are the Epic 51 hold; a stopped plugin's switch is locked, with why
	if code, got = f.call(f.admin, http.MethodPost, "/api/plugins/tv.maktaba.subtitles/stop"); code != http.StatusOK ||
		got["held_by"] != "owner" || got["switch_locked"] != "held" {
		t.Fatalf("stop: %d %v", code, got)
	}
	if code, got = f.call(f.admin, http.MethodPost, "/api/plugins/tv.maktaba.subtitles/start"); code != http.StatusOK || got["switch_locked"] != nil {
		t.Fatalf("start: %d %v", code, got)
	}
	// turning off a plugin others need is allowed (Epic 51: they wait); the dialog named them
	if code, got = f.call(f.admin, http.MethodPost, "/api/plugins/tv.maktaba.transcription/disable"); code != http.StatusOK || got["want"] != "installed" {
		t.Fatalf("disable transcription: %d %v", code, got)
	}
	// a core component is not a plugin on the Components list either
	if d, _ := f.s.Show(ctx, "core.pipeline"); d.Plugin != "" {
		t.Fatalf("core.pipeline names a plugin: %q", d.Plugin)
	}
}

// Story 54.44 T03: what turning a plugin off does is named before it is done: the
// components that need it (with the plugin each belongs to), and its component's own
// loops and processes that stop with it.
func TestSwitchEffectNamesWhatElseTurnsOff(t *testing.T) {
	f := newPub(t)
	code, got := f.call(f.admin, http.MethodGet, "/api/plugins/tv.maktaba.transcription/downstream")
	if code != http.StatusOK {
		t.Fatalf("effect: %d %v", code, got)
	}
	sub := byComponent(got["also"], "subtitles")
	if sub == nil || sub["plugin"] != "tv.maktaba.subtitles" || sub["title_key"] == "" || sub["to"] != "waiting" {
		t.Fatalf("transcription's switch does not name subtitles: %v", got)
	}
	// a part of Maktaba that runs with less is named as a component, not as a plugin
	if pd := byComponent(got["also"], "parental.detect"); pd == nil || pd["plugin"] != nil || pd["to"] != "degraded" {
		t.Fatalf("transcription's switch does not name parental.detect: %v", got)
	}
	procs, _ := got["processes"].([]any)
	if len(procs) == 0 {
		t.Fatalf("transcription's processes are not named: %v", got)
	}
	_, got = f.call(f.admin, http.MethodGet, "/api/plugins/tv.maktaba.livetv/downstream")
	loops, _ := got["loops"].([]any)
	feats, _ := got["features"].([]any)
	if len(loops) == 0 || len(feats) != 1 || feats[0] != "live_tv" || byComponent(got["also"], "livetv.slots") == nil {
		t.Fatalf("livetv's switch: %v", got)
	}
	// a leaf plugin touches nothing else
	_, got = f.call(f.admin, http.MethodGet, "/api/plugins/tv.maktaba.subtitles/downstream")
	if also, _ := got["also"].([]any); len(also) != 0 {
		t.Fatalf("subtitles' switch touches others: %v", got)
	}
	if code, _ := f.call(f.admin, http.MethodGet, "/api/plugins/dev.example.none/downstream"); code != http.StatusNotFound {
		t.Fatalf("an unknown plugin's effect: %d", code)
	}
}

func byComponent(list any, id string) map[string]any {
	l, _ := list.([]any)
	for _, x := range l {
		if m, _ := x.(map[string]any); m["component"] == id {
			return m
		}
	}
	return nil
}

// Story 54.44 T04: a plugin's jobs and what it uses. The jobs are its stages' (waiting,
// running, done and failed in the last day, the failed ones open now, the mean and p95
// time). A stage runs in its runner group's process once it is flipped, else in the
// pipeline worker; either is shared, so the process's own memory is shown with the other
// plugins it runs, never a share of it.
func TestActivityAndSharedProcesses(t *testing.T) {
	f := newPub(t)
	db := f.s.Store.DB
	var vid string
	if err := db.QueryRow(`WITH l AS (INSERT INTO libraries (name, roots) VALUES ('Films', '{}') RETURNING id)
		INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec)
		SELECT id, repeat('a', 64), '/x/a.mp4', 'a.mp4', 1, now(), 'A', 60 FROM l RETURNING id::text`).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	job := func(stage, state, timing string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO processing_jobs (video_id, subject_kind, subject_id, stage, state, started_at, finished_at)
			SELECT $1::uuid, 'video', $1::uuid, $2, $3, t.s, t.f FROM (SELECT `+timing+`) t`, vid, stage, state); err != nil {
			t.Fatalf("%s %s: %v", stage, state, err)
		}
	}
	job("subtitle_gen", "done", "now() - interval '10 minutes' AS s, now() - interval '10 minutes' + interval '2 seconds' AS f")
	job("subtitle_gen", "done", "now() - interval '9 minutes' AS s, now() - interval '9 minutes' + interval '4 seconds' AS f")
	job("subtitle_gen", "done", "now() - interval '3 days' AS s, now() - interval '3 days' + interval '90 seconds' AS f")
	job("subtitle_gen", "failed", "now() - interval '5 minutes' AS s, now() - interval '4 minutes' AS f")
	// the newest job of the subject failed: one open problem, though two jobs failed
	job("subtitle_gen", "failed", "now() - interval '4 days' AS s, now() - interval '4 days' AS f")
	// subtitles is flipped to its runner; thumbnails (the same group) too
	if _, err := db.Exec(`UPDATE job_stages SET impl = 'plugin' WHERE stage IN ('subtitle_gen', 'thumbnail', 'thumb_small', 'chapter_thumbs')`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Store.ReportStatus(context.Background(), store.Home, "supervisor", []store.Status{{ID: "core.plugin_runners", Phase: "running",
		Detail: map[string]any{"rss_mb": 900, "rss_parts": map[string]any{"plugin-runner/light": 310, "plugin-runner/files": 590}}}}); err != nil {
		t.Fatal(err)
	}
	code, got := f.call(f.admin, http.MethodGet, "/api/plugins/tv.maktaba.subtitles")
	if code != http.StatusOK {
		t.Fatalf("show: %d %v", code, got)
	}
	act, _ := got["activity"].(map[string]any)
	if act["done_24h"] != float64(2) || act["failed_24h"] != float64(1) || act["problems"] != float64(1) ||
		act["avg_ms"] != float64(3000) || act["p95_ms"] == nil {
		t.Fatalf("activity: %v", act)
	}
	stages, _ := got["stages"].([]any)
	if len(stages) != 1 || stages[0].(map[string]any)["runs_in"] != "light" || stages[0].(map[string]any)["queue"] != "subtitle_gen" {
		t.Fatalf("stages: %v", stages)
	}
	res, _ := got["resources"].(map[string]any)
	procs, _ := res["processes"].([]any)
	if len(procs) != 1 || res["shared"] != true {
		t.Fatalf("resources: %v", res)
	}
	p := procs[0].(map[string]any)
	shared, _ := json.Marshal(p["shared_with"])
	if p["process"] != "plugin-runner/light" || p["rss_mb"] != float64(310) || p["scope"] != "process" ||
		p["component"] != "core.plugin_runners" || !strings.Contains(string(shared), "tv.maktaba.thumbnails") ||
		strings.Contains(string(shared), "core-media") {
		t.Fatalf("the runner group's process: %v", p)
	}
	// a plugin whose stages are not flipped runs in the pipeline worker, with Maktaba itself
	_, got = f.call(f.admin, http.MethodGet, "/api/plugins/tv.maktaba.dedup")
	res, _ = got["resources"].(map[string]any)
	procs, _ = res["processes"].([]any)
	if len(procs) != 1 || procs[0].(map[string]any)["process"] != "pipeline/" || procs[0].(map[string]any)["shared_core"] != true {
		t.Fatalf("dedup's process: %v", res)
	}
	// the list carries the same numbers for the cards
	_, list := f.call(f.admin, http.MethodGet, "/api/plugins")
	card := byID(list["plugins"], "tv.maktaba.subtitles")
	if a, _ := card["activity"].(map[string]any); a["problems"] != float64(1) {
		t.Fatalf("the card's activity: %v", card)
	}
}

// Story 54.44 T05: what a plugin that comes with the release cannot do yet is refused in
// words, not offered: it is removed, updated and rolled back with Maktaba only.
func TestWithReleaseRefusals(t *testing.T) {
	f := newPub(t)
	for _, rt := range []string{
		"DELETE /api/plugins/tv.maktaba.subtitles",
		"POST /api/plugins/tv.maktaba.subtitles/rollback",
		"POST /api/plugins/tv.maktaba.subtitles/update",
		"PUT /api/plugins/tv.maktaba.subtitles/grants",
		"PUT /api/plugins/tv.maktaba.subtitles/settings",
		"POST /api/plugins/tv.maktaba.subtitles/writes/revert",
		"PUT /api/plugins/tv.maktaba.subtitles/auto-update",
	} {
		method, path, _ := strings.Cut(rt, " ")
		if code, got := f.call(f.admin, method, path); code != http.StatusConflict || got["code"] != "plugin_with_release" {
			t.Errorf("%s: %d %v", rt, code, got)
		}
	}
	if d, _ := f.s.Show(context.Background(), "subtitles"); d.Want != "enabled" {
		t.Fatalf("a refused removal changed the row: %q", d.Want)
	}
	// its flow is its stages and the ones they hand work to, core drawn as core
	code, got := f.call(f.admin, http.MethodGet, "/api/plugins/tv.maktaba.thumbnails/flow")
	g, _ := got["graph"].(map[string]any)
	nodes, _ := json.Marshal(g["nodes"])
	if code != http.StatusOK || !strings.Contains(string(nodes), `"stage:tv.maktaba.thumbnails/thumb_small"`) ||
		strings.Contains(string(nodes), "tv.maktaba.core-media") {
		t.Fatalf("flow: %d %s", code, nodes)
	}
}
