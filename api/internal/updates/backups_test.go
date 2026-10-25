package updates

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// supervisorFake is the home's supervisor's restore endpoints (serve_restore.go): one
// backup, a refusal for any other name, what it was asked to restore.
type supervisorFake struct {
	mu       sync.Mutex
	restored []map[string]any
}

func (s *supervisorFake) server(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("tok\n"), 0o600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		plan := map[string]any{"backup": "pre-v0.2.0-to-v0.3.0.dump", "schema": 140, "release": "0.2.0",
			"running": "0.3.0", "at": "2026-10-20T03:00:00Z"}
		switch {
		case r.URL.Path == "/update/backups":
			_ = json.NewEncoder(w).Encode(map[string]any{"backups": map[string]any{"running": "0.3.0",
				"backups": []map[string]any{{"name": "pre-v0.2.0-to-v0.3.0.dump", "bytes": 4, "kind": "update",
					"from": "0.2.0", "to": "0.3.0", "at": "2026-10-20T03:00:00Z"}},
				"kept": []map[string]any{{"version": "0.2.0", "schema": 140}, {"version": "0.3.0", "schema": 149, "running": true}}}})
		case r.URL.Path == "/update/restore" && r.URL.Query().Get("backup") != "pre-v0.2.0-to-v0.3.0.dump" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "restore_no_backup", "error": "there is no backup x"})
		case r.URL.Path == "/update/restore" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"plan": plan})
		case r.URL.Path == "/update/restore" && r.Method == http.MethodPost:
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			s.mu.Lock()
			s.restored = append(s.restored, body)
			s.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"plan": plan})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, tok
}

// disconnectFake is the cluster channel: the machine agents, the commands sent to them.
type disconnectFake struct {
	mu   sync.Mutex
	sent []string
}

func (d *disconnectFake) MachineAgents() map[string]string          { return map[string]string{"n1": "s-n1"} }
func (d *disconnectFake) MachineStates() map[string]json.RawMessage { return nil }
func (d *disconnectFake) HomeProcesses() []string                   { return nil }
func (d *disconnectFake) Command(_ context.Context, sid, name string, _ any, _ time.Duration) (bool, json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, sid+" "+name)
	return true, nil, nil
}

func (d *disconnectFake) commands() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sent...)
}

// Story 35.5 AC8 on the Updates page: the home's backups listed, a restore's plan shown,
// the restore itself only with the backup's name typed (admin only, audited), the
// machines told the home goes away; a refusal keeps its code.
func TestBackupsAndRestore(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqliteDB(t))
	sup := &supervisorFake{}
	srv, tok := sup.server(t)
	cl := &disconnectFake{}
	h := &Handler{Svc: f.svc, Driver: &Driver{Store: f.store, Svc: f.svc,
		Home: &SupervisorClient{BaseURL: srv.URL, TokenFile: tok}, Cluster: cl}}
	r := chi.NewRouter()
	h.Mount(r)

	code, out := do(t, r, "GET", "/api/cluster/updates/backups", nil, true)
	if code != http.StatusOK || out["running"] != "0.3.0" || len(out["backups"].([]any)) != 1 || len(out["kept"].([]any)) != 2 {
		t.Fatalf("list %d %v", code, out)
	}
	code, out = do(t, r, "GET", "/api/cluster/updates/backups/pre-v0.2.0-to-v0.3.0.dump/restore", nil, true)
	if code != http.StatusOK || out["release"] != "0.2.0" {
		t.Fatalf("plan %d %v", code, out)
	}
	code, out = do(t, r, "GET", "/api/cluster/updates/backups/nope.dump/restore", nil, true)
	if code != http.StatusConflict || out["code"] != "restore_no_backup" {
		t.Fatalf("a refused plan %d %v", code, out)
	}
	for _, c := range []struct {
		body  any
		admin bool
		want  int
	}{
		{map[string]any{"confirm": "pre-v0.2.0-to-v0.3.0.dump"}, false, http.StatusForbidden},
		{map[string]any{}, true, http.StatusBadRequest},
		{map[string]any{"confirm": "yes"}, true, http.StatusBadRequest},
	} {
		if code, out := do(t, r, "POST", "/api/cluster/updates/backups/pre-v0.2.0-to-v0.3.0.dump/restore", c.body, c.admin); code != c.want {
			t.Fatalf("%v (admin %v): %d %v, want %d", c.body, c.admin, code, out, c.want)
		}
	}
	if len(sup.restored) != 0 {
		t.Fatalf("restored without the confirmation: %v", sup.restored)
	}
	code, out = do(t, r, "POST", "/api/cluster/updates/backups/pre-v0.2.0-to-v0.3.0.dump/restore",
		map[string]any{"confirm": "pre-v0.2.0-to-v0.3.0.dump"}, true)
	if code != http.StatusAccepted || out["release"] != "0.2.0" {
		t.Fatalf("restore %d %v", code, out)
	}
	if len(sup.restored) != 1 || sup.restored[0]["backup"] != "pre-v0.2.0-to-v0.3.0.dump" || sup.restored[0]["confirm"] != true ||
		sup.restored[0]["page"] != true ||
		sup.restored[0]["by"] == "" {
		t.Fatalf("asked %v", sup.restored)
	}
	if a := auditActions(t, f); len(a) != 1 || a[0] != "update.restore" {
		t.Fatalf("audit %v", a)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(cl.commands()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := cl.commands(); len(got) != 1 || got[0] != "s-n1 disconnect" {
		t.Fatalf("machines told %v", got)
	}
	for _, p := range []string{"/api/cluster/updates/backups", "/api/cluster/updates/backups/x.dump/restore"} {
		if code, _ := do(t, r, "GET", p, nil, false); code != http.StatusForbidden {
			t.Fatalf("%s for a user: %d", p, code)
		}
	}
	// a home whose server cannot restore (no supervisor): said so
	h2 := &Handler{Svc: f.svc, Driver: &Driver{Home: &SupervisorClient{}}}
	r2 := chi.NewRouter()
	h2.Mount(r2)
	if code, out := do(t, r2, "GET", "/api/cluster/updates/backups", nil, true); code != http.StatusServiceUnavailable ||
		!strings.Contains(out["code"].(string), "orchestrator_unavailable") {
		t.Fatalf("no supervisor: %d %v", code, out)
	}
}

// Review L3: a restore the command line asked for (through the supervisor) is recorded by
// the API as the page's is: audited, the machines told; only this machine's supervisor may
// say so.
func TestRestoredNoticeFromTheSupervisor(t *testing.T) {
	f := newFixture(t, sqliteDB(t))
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("tok\n"), 0o600)
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tok)
	cl := &disconnectFake{}
	h := &Handler{Svc: f.svc, Driver: &Driver{Store: f.store, Svc: f.svc, Cluster: cl}}
	r := chi.NewRouter()
	h.Mount(r)
	post := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/internal/updates/restored",
			strings.NewReader(`{"backup":"pre-v0.2.0-to-v0.3.0.dump","release":"0.2.0","schema":140,"by":"the command line"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Maktaba-Local-Token", token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post(""); code != http.StatusForbidden {
		t.Fatalf("without the token: %d", code)
	}
	if code := post("nope"); code != http.StatusForbidden {
		t.Fatalf("another token: %d", code)
	}
	if code := post("tok"); code != http.StatusNoContent {
		t.Fatalf("the supervisor: %d", code)
	}
	if a := auditActions(t, f); len(a) != 1 || a[0] != "update.restore" {
		t.Fatalf("audit %v", a)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(cl.commands()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := cl.commands(); len(got) != 1 || got[0] != "s-n1 disconnect" {
		t.Fatalf("machines told %v", got)
	}
}
