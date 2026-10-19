package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
)

const adminID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func newRouter(t *testing.T, f *fixture) http.Handler {
	t.Helper()
	rh := &rules.Handler{DB: f.db}
	h := &Handler{Svc: f.svc, RulesPatch: rh.Patch}
	r := chi.NewRouter()
	h.Mount(r)
	r.Method(http.MethodGet, "/api/system/updates", h.ExtendStatus(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"available": true, "latest_version": "v1.5.0", "current_version": "1.4.0"})
	})))
	return r
}

func do(t *testing.T, h http.Handler, method, path string, body any, admin bool) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	user := adminID
	if !admin {
		user = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	}
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: user, IsAdmin: admin}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func auditActions(t *testing.T, f *fixture) []string {
	t.Helper()
	rows, err := f.db.Query(`SELECT action FROM audit_log WHERE category = 'update' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		out = append(out, a)
	}
	return out
}

// TC6: every update endpoint is admin only.
func TestNonAdmin403(t *testing.T) {
	f := newFixture(t, sqliteDB(t))
	h := newRouter(t, f)
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/cluster/updates"}, {"PUT", "/api/cluster/updates/settings"},
		{"POST", "/api/cluster/updates/run"}, {"POST", "/api/cluster/updates/skip"},
		{"POST", "/api/cluster/updates/defer"}, {"POST", "/api/cluster/updates/check"},
		{"GET", "/api/cluster/updates/runs"},
	} {
		if code, _ := do(t, h, c.m, c.p, map[string]any{}, false); code != http.StatusForbidden {
			t.Errorf("%s %s as a user: %d", c.m, c.p, code)
		}
	}
}

// TC6 / AC8: a pin below the installed version is refused with pin_below_installed.
func TestPinBelowWatermark400(t *testing.T) {
	f := newFixture(t, sqliteDB(t))
	if err := f.store.RaiseWatermark(context.Background(), "1.5.0"); err != nil {
		t.Fatal(err)
	}
	h := newRouter(t, f)
	code, body := do(t, h, "PUT", "/api/cluster/updates/settings", map[string]any{"pinned_version": "1.4.9"}, true)
	if code != http.StatusBadRequest || body["code"] != "pin_below_installed" {
		t.Fatalf("pin below: %d %v", code, body)
	}
	code, body = do(t, h, "PUT", "/api/cluster/updates/settings", map[string]any{"window": "3am"}, true)
	if code != http.StatusBadRequest || body["code"] != "bad_window" {
		t.Fatalf("bad window: %d %v", code, body)
	}
	code, body = do(t, h, "PUT", "/api/cluster/updates/settings", map[string]any{"pinned_version": "1.5.9", "window": "23:00-01:00"}, true)
	if code != http.StatusOK {
		t.Fatalf("good settings: %d %v", code, body)
	}
	pol, _ := f.svc.Policy(context.Background())
	if pol.PinnedVersion != "1.5.9" || pol.Window != "23:00-01:00" {
		t.Fatalf("not stored: %+v", pol)
	}
	// a value the schema refuses (the rules handler's validation)
	code, _ = do(t, h, "PUT", "/api/cluster/updates/settings", map[string]any{"channel": "nightly"}, true)
	if code < 400 {
		t.Fatalf("channel nightly accepted: %d", code)
	}
	if a := auditActions(t, f); len(a) != 1 || a[0] != "settings_changed" {
		t.Fatalf("audit: %v", a)
	}
}

// Skip and defer are stored in rules.updates and audited (AC8); Update now returns
// 202 {run_id} (AC7); GET shows the plan.
func TestSkipDeferRunAndOverview(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, sqliteDB(t))
	seed(t, f.store, rel("1.5.0"), rel("1.6.0"))
	h := newRouter(t, f)

	code, body := do(t, h, "GET", "/api/cluster/updates", nil, true)
	plan, _ := body["plan"].(map[string]any)
	target, _ := plan["target"].(map[string]any)
	if code != 200 || target["version"] != "1.6.0" || body["auto_install_available"] != false {
		t.Fatalf("overview: %d %v", code, body)
	}
	if code, _ := do(t, h, "POST", "/api/cluster/updates/skip", map[string]any{"version": "v1.6.0"}, true); code != 200 {
		t.Fatalf("skip: %d", code)
	}
	if code, b := do(t, h, "POST", "/api/cluster/updates/defer", map[string]any{"until": "tomorrow"}, true); code != 400 || b["code"] != "bad_deferral" {
		t.Fatalf("bad defer: %d %v", code, b)
	}
	if code, _ := do(t, h, "POST", "/api/cluster/updates/defer", map[string]any{"until": "2026-10-30T03:00:00Z"}, true); code != 200 {
		t.Fatalf("defer: %d", code)
	}
	pol, _ := f.svc.Policy(ctx)
	if !pol.Skipped("1.6.0") || pol.DeferredUntil == nil {
		t.Fatalf("policy: %+v", pol)
	}
	_, body = do(t, h, "GET", "/api/cluster/updates", nil, true)
	plan, _ = body["plan"].(map[string]any)
	target, _ = plan["target"].(map[string]any)
	if target["version"] != "1.5.0" {
		t.Fatalf("skipped release still the target: %v", plan)
	}

	if code, b := do(t, h, "POST", "/api/cluster/updates/run", map[string]any{}, true); code != 400 || b["code"] != "confirm_required" {
		t.Fatalf("run without confirm: %d %v", code, b)
	}
	code, body = do(t, h, "POST", "/api/cluster/updates/run", map[string]any{"confirm": true}, true)
	if code != http.StatusAccepted || body["run_id"] == "" || body["version"] != "1.5.0" {
		t.Fatalf("update now: %d %v", code, body)
	}
	code, body = do(t, h, "POST", "/api/cluster/updates/run", map[string]any{"confirm": true}, true)
	if code != http.StatusConflict || body["code"] != BlockActiveRun {
		t.Fatalf("second update now: %d %v", code, body)
	}
	code, body = do(t, h, "GET", "/api/cluster/updates/runs", nil, true)
	if items, _ := body["items"].([]any); code != 200 || len(items) != 1 {
		t.Fatalf("runs: %d %v", code, body)
	}
	got := strings.Join(auditActions(t, f), ",")
	if !strings.Contains(got, "release_skipped") || !strings.Contains(got, "updates_deferred") || !strings.Contains(got, "run_planned") {
		t.Fatalf("audit: %s", got)
	}

	// AC10: GET /api/system/updates gains verified, policy and next_run_at
	_, st := do(t, h, "GET", "/api/system/updates", nil, false)
	if st["verified"] != true || st["policy"] == nil {
		t.Fatalf("system updates: %v", st)
	}
	if _, ok := st["next_run_at"]; !ok {
		t.Fatalf("no next_run_at: %v", st)
	}
}

// The orchestrator is story 35.4: a supervisor that does not know /update/start makes
// Update now 503 orchestrator_unavailable and the run failed.
func TestUpdateNowWithoutOrchestrator(t *testing.T) {
	sup := httptest.NewServer(http.NotFoundHandler())
	defer sup.Close()
	f := newFixture(t, sqliteDB(t))
	f.svc.Starter = &SupervisorClient{BaseURL: sup.URL}
	seed(t, f.store, rel("1.5.0"))
	code, body := do(t, newRouter(t, f), "POST", "/api/cluster/updates/run", map[string]any{"confirm": true}, true)
	if code != http.StatusServiceUnavailable || body["code"] != "orchestrator_unavailable" || body["run_id"] == "" {
		t.Fatalf("%d %v", code, body)
	}
}

// Slot 0132: the five tables exist on SQLite (applied above) and, on Postgres, are
// revoked from maktaba_worker (TestMigrationS1Revoked; plan 35.3 §3).
func TestMigrationS1Revoked(t *testing.T) {
	b, err := os.ReadFile(migrationsDir(t) + "/0132_update_runs.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"-- +goose NO TRANSACTION",
		"REVOKE ALL ON release_catalog, update_runs, update_run_steps, update_targets,\n            update_watermark FROM maktaba_worker",
		"'tv_change', 'tv_remote', 'update'",
		"CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS update_runs_one_active_idx",
	} {
		if !strings.Contains(strings.ReplaceAll(s, "\r\n", "\n"), want) {
			t.Errorf("0132_update_runs.sql: missing %q", want)
		}
	}
	for _, be := range backends(t) {
		if be.name != "postgres" {
			continue
		}
		for _, tbl := range []string{"release_catalog", "update_runs", "update_run_steps", "update_targets", "update_watermark"} {
			var can bool
			if err := be.db.QueryRow(`SELECT has_table_privilege('maktaba_worker', $1, 'SELECT')`, tbl).Scan(&can); err != nil {
				t.Fatal(err)
			}
			if can {
				t.Errorf("maktaba_worker can read %s", tbl)
			}
		}
		if _, err := be.db.Exec(`INSERT INTO audit_log (category, action) VALUES ('update', 'probe')`); err != nil {
			t.Errorf("audit category update refused: %v", err)
		}
	}
}
