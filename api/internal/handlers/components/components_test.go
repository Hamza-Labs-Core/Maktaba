package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite" // the SQLite driver

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// base are the older tables the routes read (the columns they use).
const base = `
CREATE TABLE cluster_nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, action TEXT NOT NULL,
	actor_user_id TEXT, target_id TEXT, payload TEXT NOT NULL DEFAULT '{}',
	ts TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), actor_source TEXT, target_kind TEXT);
CREATE TABLE job_stages (stage TEXT PRIMARY KEY, label TEXT NOT NULL, subject_kinds TEXT NOT NULL,
	pool TEXT NOT NULL, lane TEXT NOT NULL, lane_order INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1,
	debounce_sec INTEGER NOT NULL DEFAULT 0, debounce_max_sec INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3, edges TEXT NOT NULL DEFAULT '[]', may_request TEXT NOT NULL DEFAULT '[]',
	entry_for TEXT NOT NULL DEFAULT '[]', legacy INTEGER NOT NULL DEFAULT 0, hidden INTEGER NOT NULL DEFAULT 0,
	external_write INTEGER NOT NULL DEFAULT 0, write_mode TEXT NOT NULL DEFAULT 'auto',
	placement TEXT NOT NULL DEFAULT 'any', enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE processing_jobs (id INTEGER PRIMARY KEY, stage TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending');
CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT (datetime('now')), updated_by TEXT);
CREATE TABLE extensions (name TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0,
	settings TEXT NOT NULL DEFAULT '{}', updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));
CREATE TABLE hdhr_device (id INTEGER PRIMARY KEY DEFAULT 1, device_id TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 0);
`

func upStatements(sqlText string) []string {
	up := sqlText
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	var out []string
	for _, block := range strings.Split(up, "-- +goose StatementBegin") {
		if i := strings.Index(block, "-- +goose StatementEnd"); i >= 0 {
			if s := strings.TrimSpace(block[:i]); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// sqliteDB is a fresh SQLite file with slot 0178's twin.
// schema creates the base tables and 0178's components tables in db.
func schema(t testing.TB, db *sql.DB) {
	t.Helper()
	for _, s := range strings.Split(base, ";\n") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	twin, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "db", "migrations", "0178_components.sqlite.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range upStatements(string(twin)) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("0178: %v\n%s", err, s)
		}
	}
}

type fixture struct {
	db      *sql.DB
	st      *store.Store
	r       chi.Router
	licence bool
	// auditBase is the fixture's own audit rows (the seed), left out of audits
	auditBase int
}

// newFixture is a home with every switchable component enabled, except
// livetv.inprocess (off: it conflicts with livetv.slots) and cloud.relay (off).
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return fixtureFrom(t, fromTemplate(t, "home", seedHome))
}

// fixtureFrom is a fixture over db.
func fixtureFrom(t *testing.T, db *sql.DB) *fixture {
	t.Helper()
	f := &fixture{db: db, st: store.New(db), licence: true}
	f.auditBase = f.audits(t)
	f.r = f.router()
	return f
}

// seedHome builds the home newFixture copies: the schema, and every switchable
// component's desired row as the seed writes it.
func seedHome(t testing.TB, db *sql.DB) {
	t.Helper()
	schema(t, db)
	_, err := store.New(db).SetDesiredBatch(context.Background(), func(map[string]store.Desired, int64) (store.BatchPlan, error) {
		var p store.BatchPlan
		for _, c := range components.Default().Components() {
			if !c.Switchable {
				continue
			}
			w := components.WantEnabled
			if c.ID == "livetv.inprocess" || c.ID == "cloud.relay" {
				w = components.WantInstalled
			}
			p.Writes = append(p.Writes, store.BatchWrite{Desired: store.Desired{ID: c.ID, Want: w},
				Audit: store.Audit{By: store.SeedBy, ActorSource: "seed", Action: "component.seed"}})
		}
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) router() chi.Router {
	s := &svc.Service{Store: f.st, Catalog: components.Default(), Licensed: func(string) bool { return f.licence }}
	r := chi.NewRouter()
	(&Handler{Service: s}).Mount(r)
	return r
}

func (f *fixture) do(method, path, body string, p *principal.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) want(t *testing.T, id string) store.Desired {
	t.Helper()
	d, err := f.st.DesiredOf(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f *fixture) audits(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE category = 'components'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n - f.auditBase
}

func (f *fixture) revision(t *testing.T) int64 {
	t.Helper()
	rev, err := f.st.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func admin() *principal.Principal { return &principal.Principal{UserID: "u1", IsAdmin: true} }

func viewer() *principal.Principal { return &principal.Principal{UserID: "u2"} }

// playAdmin is an admin's cloud-linked play sign-in: ApplyCloudScope clears IsAdmin.
func playAdmin() *principal.Principal {
	p := admin()
	p.ApplyCloudScope(principal.CloudScopePlay, nil)
	return p
}

func manageLink() *principal.Principal {
	p := admin()
	p.ApplyCloudScope(principal.CloudScopeManage, nil)
	return p
}

// token is an API token without the admin scope; machine a cluster machine's
// certificate, which is no admin.
func token() *principal.Principal {
	return &principal.Principal{UserID: "u1", Source: principal.SourcePAT}
}

func machine() *principal.Principal { return &principal.Principal{UserID: "node:n1"} }

func decode(t *testing.T, rec *httptest.ResponseRecorder, status int, v any) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if v != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("%v: %s", err, rec.Body)
		}
	}
}

func code(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) map[string]any {
	t.Helper()
	var body map[string]any
	decode(t, rec, status, &body)
	if body["code"] != want {
		t.Fatalf("code %v, want %s: %s", body["code"], want, rec.Body)
	}
	return body
}

var routes = []struct{ method, path, body string }{
	{"GET", "/api/components", ""},
	{"GET", "/api/components/llm", ""},
	{"PUT", "/api/components/llm", `{"want":"installed"}`},
	{"DELETE", "/api/components/youtube", ""},
	{"POST", "/api/components/youtube/install", ""},
	{"POST", "/api/components/model.embedder/install", ""},
	{"POST", "/api/components/transcription/stop", ""},
	{"POST", "/api/components/transcription/start", ""},
	{"POST", "/api/components/preview", `{"changes":[{"id":"llm","want":"installed"}]}`},
	{"POST", "/api/components/apply", `{"changes":[{"id":"llm","want":"installed"}]}`},
	{"POST", "/api/components/apply", `{"bundle":"core"}`},
	// story 51.13
	{"POST", "/api/components/transcription/settings/reset", ""},
	{"DELETE", "/api/components/youtube?purge=true", ""},
}

// T07: no sign-in is 401; a viewer, an admin's play sign-in, a token without the admin
// scope and a machine are 403; nothing changes.
func TestAdminOnly(t *testing.T) {
	f := newFixture(t)
	rev := f.revision(t)
	for _, c := range routes {
		if rec := f.do(c.method, c.path, c.body, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s as no one: %d", c.method, c.path, rec.Code)
		}
		for name, p := range map[string]*principal.Principal{"viewer": viewer(), "play": playAdmin(), "token": token(), "machine": machine()} {
			if rec := f.do(c.method, c.path, c.body, p); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d", c.method, c.path, name, rec.Code)
			}
		}
	}
	if f.revision(t) != rev || f.audits(t) != 0 {
		t.Fatalf("refused calls changed rows: revision %d -> %d, %d audit rows", rev, f.revision(t), f.audits(t))
	}
}

// T08: an admin and an admin's manage link pass every route.
func TestAdminAndManageLinkPass(t *testing.T) {
	for name, p := range map[string]*principal.Principal{"admin": admin(), "manage": manageLink()} {
		f := newFixture(t)
		for _, c := range routes {
			if rec := f.do(c.method, c.path, c.body, p); rec.Code/100 != 2 {
				t.Errorf("%s %s as %s: %d %s", c.method, c.path, name, rec.Code, rec.Body)
			}
		}
	}
}

// Preview changes nothing and lists the effect.
func TestPreviewChangesNothing(t *testing.T) {
	f := newFixture(t)
	rev := f.revision(t)
	var p svc.Preview
	decode(t, f.do("POST", "/api/components/preview", `{"changes":[{"id":"llm","want":"installed"}]}`, admin()), 200, &p)
	found := map[string]svc.Effect{}
	for _, e := range p.Effects {
		found[e.ID] = e
	}
	if found["llm"].From != components.PhaseRunning || found["llm"].To != components.PhaseOff ||
		found["classify"].To != components.PhaseDegraded || found["classify"].Reason != "want_missing:llm" {
		t.Fatalf("effects %+v", p.Effects)
	}
	if f.revision(t) != rev || f.audits(t) != 0 {
		t.Fatal("a preview wrote")
	}
}

// PUT turns llm off: its generation moves by one, classify keeps its wish, one audit row.
func TestPutDisables(t *testing.T) {
	f := newFixture(t)
	gen := f.want(t, "llm").Generation
	var v svc.Detail
	decode(t, f.do("PUT", "/api/components/llm", `{"want":"installed","generation":`+itoa(gen)+`}`, admin()), 200, &v)
	if v.Want != components.WantInstalled || v.Generation != gen+1 || v.Phase != components.PhaseOff {
		t.Fatalf("answered %+v", v.View)
	}
	if d := f.want(t, "classify"); d.Want != components.WantEnabled {
		t.Fatalf("classify %+v", d)
	}
	var action, source, target string
	if err := f.db.QueryRow(`SELECT action, actor_source, target_id FROM audit_log WHERE category = 'components' AND action <> 'component.seed'`).Scan(&action, &source, &target); err != nil {
		t.Fatal(err)
	}
	if f.audits(t) != 1 || action != "component.disable" || source != "web" || target != "llm" {
		t.Fatalf("audit %s %s %s (%d rows)", action, source, target, f.audits(t))
	}
	if len(v.Audit) != 2 || v.Audit[0].Action != "component.disable" || v.Audit[1].Action != "component.seed" {
		t.Fatalf("show's audit %+v", v.Audit)
	}
	// T04: the same generation again is stale, with the current row
	body := code(t, f.do("PUT", "/api/components/llm", `{"want":"enabled","generation":`+itoa(gen)+`}`, admin()), 409, svc.CodeStale)
	if cur, _ := body["current"].(map[string]any); cur["generation"] != float64(gen+1) {
		t.Fatalf("current %v", body["current"])
	}
	// a cloud manage link is audited as remote
	decode(t, f.do("PUT", "/api/components/llm", `{"want":"enabled"}`, manageLink()), 200, nil)
	if err := f.db.QueryRow(`SELECT actor_source FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&source); err != nil || source != "remote" {
		t.Fatalf("manage link audited as %q %v", source, err)
	}
}

// Remove is refused while something on requires it.
func TestRemoveRequired(t *testing.T) {
	f := newFixture(t)
	body := code(t, f.do("PUT", "/api/components/runtime.ml", `{"want":"absent"}`, admin()), 409, svc.CodeRequiredBy)
	by, _ := body["required_by"].([]any)
	if !slices.Contains(by, any("transcription")) {
		t.Fatalf("required_by %v", by)
	}
	code(t, f.do("DELETE", "/api/components/runtime.ml", "", admin()), 409, svc.CodeRequiredBy)
	if f.want(t, "runtime.ml").Want != components.WantEnabled || f.audits(t) != 0 {
		t.Fatal("a refused remove wrote")
	}
	code(t, f.do("DELETE", "/api/components/runtime.ml?purge=true", "", admin()), 409, svc.CodeRequiredBy)
	decode(t, f.do("DELETE", "/api/components/youtube", "", admin()), 200, nil)
	if f.want(t, "youtube").Want != components.WantAbsent {
		t.Fatal("youtube not removed")
	}
	// install brings it back off, once
	decode(t, f.do("POST", "/api/components/youtube/install", "", admin()), 200, nil)
	if f.want(t, "youtube").Want != components.WantInstalled {
		t.Fatal("youtube not installed")
	}
	n := f.audits(t)
	decode(t, f.do("POST", "/api/components/youtube/install", "", admin()), 200, nil)
	if f.audits(t) != n {
		t.Fatal("a second install wrote")
	}
}

// Install of a wanted asset that is not on disk: with no desired row (a fresh home's seed
// leaves the search model so) it writes the row as the catalog default (enabled), audited
// component.install; asked again (Retry after a failure) it writes a new generation; once
// the home reports it installed, it writes nothing.
func TestInstallWantedAssetNotOnDisk(t *testing.T) {
	f := newFixture(t)
	const id = "model.embedder"
	if _, err := f.db.Exec(`DELETE FROM components WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var v svc.Detail
	decode(t, f.do("POST", "/api/components/"+id+"/install", "", admin()), 200, &v)
	d := f.want(t, id)
	if !v.HasRow || v.Want != components.WantEnabled || d.Want != components.WantEnabled {
		t.Fatalf("no row written: has_row %v want %q, row %+v", v.HasRow, v.Want, d)
	}
	if n := f.audits(t); n != 1 {
		t.Fatalf("%d audit rows", n)
	}
	var action, source string
	if err := f.db.QueryRow(`SELECT action, actor_source FROM audit_log WHERE target_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&action, &source); err != nil ||
		action != svc.ActionInstall || source != "web" {
		t.Fatalf("audited %q %q %v", action, source, err)
	}
	// Retry: the same want, a new generation the packs runner acts on
	decode(t, f.do("POST", "/api/components/"+id+"/install", "", admin()), 200, nil)
	if again := f.want(t, id); again.Generation <= d.Generation || again.Want != components.WantEnabled || f.audits(t) != 2 {
		t.Fatalf("retry: %+v after %+v, %d audit rows", again, d, f.audits(t))
	}
	// installed at home: nothing to ask
	if err := f.st.ReportStatus(context.Background(), store.Home, "packs", []store.Status{{ID: id, Phase: components.PhaseOff,
		Conditions: []store.Condition{{Type: components.CondInstalled, Status: "true"}}}}); err != nil {
		t.Fatal(err)
	}
	gen := f.want(t, id).Generation
	decode(t, f.do("POST", "/api/components/"+id+"/install", "", admin()), 200, nil)
	if f.want(t, id).Generation != gen || f.audits(t) != 2 {
		t.Fatal("an install of an asset on disk wrote")
	}
	// a viewer cannot ask
	if rec := f.do("POST", "/api/components/runtime.ml/install", "", viewer()); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d", rec.Code)
	}
	// a component that is not an asset and is on is answered as it is
	g := f.want(t, "youtube").Generation
	decode(t, f.do("POST", "/api/components/youtube/install", "", admin()), 200, nil)
	if f.want(t, "youtube").Generation != g {
		t.Fatal("install of an enabled process wrote")
	}
}

// Enabling in-process TV while slots are on is refused, unless with_conflicts: then both
// change in one batch.
func TestConflicts(t *testing.T) {
	f := newFixture(t)
	body := code(t, f.do("PUT", "/api/components/livetv.inprocess", `{"want":"enabled"}`, admin()), 409, svc.CodeConflicts)
	if c, _ := body["conflicts"].([]any); len(c) != 1 || c[0] != "livetv.slots" {
		t.Fatalf("conflicts %v", body["conflicts"])
	}
	decode(t, f.do("PUT", "/api/components/livetv.inprocess", `{"want":"enabled","with_conflicts":true}`, admin()), 200, nil)
	if f.want(t, "livetv.inprocess").Want != components.WantEnabled || f.want(t, "livetv.slots").Want != components.WantInstalled {
		t.Fatal("both did not change")
	}
	rows, err := f.db.Query(`SELECT payload FROM audit_log WHERE action <> 'component.seed' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var batches []string
	for rows.Next() {
		var raw string
		_ = rows.Scan(&raw)
		var p map[string]any
		_ = json.Unmarshal([]byte(raw), &p)
		b, _ := p["batch_id"].(string)
		batches = append(batches, b)
	}
	if len(batches) != 2 || batches[0] == "" || batches[0] != batches[1] {
		t.Fatalf("one batch id for both rows: %v", batches)
	}
}

// T05 and the hold: a stop with an until is refused; a stop lasts across a restart of
// the API until start.
func TestStopHasNoUntil(t *testing.T) {
	f := newFixture(t)
	code(t, f.do("POST", "/api/components/transcription/stop", `{"until":"2026-11-04T06:00:00Z"}`, admin()), 400, svc.CodeNoUntil)
	if f.want(t, "transcription").Hold != nil || f.audits(t) != 0 {
		t.Fatal("a hold with an until was written")
	}
	var v svc.Detail
	decode(t, f.do("POST", "/api/components/transcription/stop", "", admin()), 200, &v)
	if v.Hold == nil || v.Phase != components.PhaseStopped {
		t.Fatalf("stopped: %+v", v.View)
	}
	f.r = f.router() // the API restarts
	decode(t, f.do("GET", "/api/components/transcription", "", admin()), 200, &v)
	if v.Hold == nil || v.Phase != components.PhaseStopped {
		t.Fatalf("after a restart: %+v", v.View)
	}
	decode(t, f.do("POST", "/api/components/transcription/start", "", admin()), 200, &v)
	if v.Hold != nil || v.Phase != components.PhaseRunning {
		t.Fatalf("started: %+v", v.View)
	}
}

// T06: every write to a core component is 409 not_switchable; an unknown id is 404.
func TestCoreAndUnknown(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/components/core.api", `{"want":"installed"}`},
		{"DELETE", "/api/components/core.api", ""},
		{"POST", "/api/components/core.api/stop", ""},
		{"POST", "/api/components/core.api/start", ""},
		{"POST", "/api/components/apply", `{"changes":[{"id":"core.api","want":"installed"}]}`},
	} {
		code(t, f.do(c.method, c.path, c.body, admin()), 409, svc.CodeNotSwitchable)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/components/nope", ""},
		{"PUT", "/api/components/nope", `{"want":"installed"}`},
		{"POST", "/api/components/nope/stop", ""},
	} {
		code(t, f.do(c.method, c.path, c.body, admin()), 404, svc.CodeUnknown)
	}
	code(t, f.do("PUT", "/api/components/llm", `{"want":"on"}`, admin()), 400, svc.CodeBadWant)
	if f.audits(t) != 0 {
		t.Fatal("a refusal wrote")
	}
}

// T10: an apply with a bad third change writes none; the licence gate is 403.
func TestApplyAllOrNone(t *testing.T) {
	f := newFixture(t)
	rev := f.revision(t)
	code(t, f.do("POST", "/api/components/apply", `{"changes":[{"id":"llm","want":"installed"},
		{"id":"youtube","want":"installed"},{"id":"core.api","want":"installed"}]}`, admin()), 409, svc.CodeNotSwitchable)
	if f.revision(t) != rev || f.want(t, "llm").Want != components.WantEnabled {
		t.Fatal("a refused batch wrote")
	}
	code(t, f.do("POST", "/api/components/apply", `{"generation":`+itoa(rev-1)+`,"changes":[{"id":"llm","want":"installed"}]}`, admin()), 409, svc.CodeStale)
	var res svc.Result
	decode(t, f.do("POST", "/api/components/apply", `{"generation":`+itoa(rev)+`,"changes":[{"id":"livetv.slots","want":"installed"},
		{"id":"livetv.inprocess","want":"enabled"}]}`, admin()), 200, &res)
	if len(res.Changed) != 2 || res.BatchID == "" || res.Revision != rev+2 {
		t.Fatalf("result %+v", res)
	}
	f.licence = false
	f.r = f.router()
	body := code(t, f.do("PUT", "/api/components/cloud.relay", `{"want":"enabled"}`, admin()), 403, svc.CodeLicence)
	if body["licence"] != "cloud_relay" {
		t.Fatalf("licence %v", body["licence"])
	}
}

// The list carries the revision, the bundle line and every component.
func TestList(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(`UPDATE component_meta SET bundle = 'full'`); err != nil {
		t.Fatal(err)
	}
	decode(t, f.do("PUT", "/api/components/youtube", `{"want":"installed"}`, admin()), 200, nil)
	var l svc.ListView
	decode(t, f.do("GET", "/api/components", "", admin()), 200, &l)
	if len(l.Components) != len(components.Default().Components()) || l.Revision != f.revision(t) {
		t.Fatalf("%d components at %d", len(l.Components), l.Revision)
	}
	if l.Bundle.Base != "full" || !slices.Contains(l.Bundle.Minus, "youtube") {
		t.Fatalf("bundle line %+v", l.Bundle)
	}
	for _, c := range l.Components {
		if c.ID == "runtime.ml" && !slices.Contains(c.RequiredBy, "transcription") {
			t.Fatalf("runtime.ml required_by %v", c.RequiredBy)
		}
		if c.ID == "model.embedder" && c.InstallSizeMB <= 0 {
			t.Fatalf("embedder size %d", c.InstallSizeMB)
		}
	}
}

// Each component carries settings: the catalog's list, verbatim (the page's Settings
// link reads it); a component that owns none answers [], never null.
func TestListAndShowCarrySettings(t *testing.T) {
	f := newFixture(t)
	want := []any{"rules.transcription", "toml:transcription.workers"}
	var l struct {
		Components []map[string]any `json:"components"`
	}
	decode(t, f.do("GET", "/api/components", "", admin()), 200, &l)
	seen := 0
	for _, c := range l.Components {
		got, ok := c["settings"].([]any)
		if !ok {
			t.Fatalf("%v: settings %#v", c["id"], c["settings"])
		}
		cat, _ := components.Default().Get(c["id"].(string))
		if len(got) != len(cat.Settings) {
			t.Fatalf("%v: settings %v, catalog %v", c["id"], got, cat.Settings)
		}
		if c["id"] == "transcription" {
			seen++
			if !slices.Equal(got, want) {
				t.Fatalf("transcription settings %v", got)
			}
		}
	}
	var one map[string]any
	decode(t, f.do("GET", "/api/components/transcription", "", admin()), 200, &one)
	if got, _ := one["settings"].([]any); seen != 1 || !slices.Equal(got, want) {
		t.Fatalf("show: settings %v (listed %d)", one["settings"], seen)
	}
}

// The reason's params ride with it, as text, on the component and on each machine: the
// page fills components.reason.<reason> from them.
func TestReasonParams(t *testing.T) {
	f := newFixture(t)
	if err := f.st.ReportStatus(context.Background(), store.Home, "supervisor", []store.Status{{
		Machine: store.Home, ID: "transcription", Part: "supervisor", Phase: components.PhaseFailed,
		Conditions: []store.Condition{
			{Type: "Ready", Status: "true"},
			{Type: "Started", Status: "false", Reason: "settings_invalid", Params: map[string]any{"name": "release", "holder_pid": 4242}},
		}}}); err != nil {
		t.Fatal(err)
	}
	var one map[string]any
	decode(t, f.do("GET", "/api/components/transcription", "", admin()), 200, &one)
	want := map[string]any{"name": "release", "holder_pid": "4242"}
	got, _ := one["reason_params"].(map[string]any)
	if one["reason"] != "settings_invalid" || len(got) != 2 || got["name"] != want["name"] || got["holder_pid"] != want["holder_pid"] {
		t.Fatalf("component: reason %v, params %v", one["reason"], one["reason_params"])
	}
	machines, _ := one["machines"].([]any)
	if len(machines) != 1 {
		t.Fatalf("machines %v", one["machines"])
	}
	if mp, _ := machines[0].(map[string]any)["reason_params"].(map[string]any); mp["name"] != "release" {
		t.Fatalf("machine params %v", machines[0])
	}
	// no reason, no params
	var llm map[string]any
	decode(t, f.do("GET", "/api/components/llm", "", admin()), 200, &llm)
	if _, has := llm["reason_params"]; has {
		t.Fatalf("llm %v", llm["reason_params"])
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
