package serversettings

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func backupsFixture(t *testing.T) (http.Handler, *atomic.Int32, *Handler) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		doc := `{"saved":{"dir":"/b","keep":14,"update_keep":4},"dumps":[]}`
		switch r.Method + " " + r.URL.Path {
		case "GET /config/backups":
			_, _ = io.WriteString(w, doc)
		case "PUT /config/backups":
			var in map[string]any
			_ = json.Unmarshal(b, &in)
			if in["hour"] != nil {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"errors":[{"field":"backups.hour","message":"0 to 23"}]}`)
				return
			}
			_, _ = io.WriteString(w, doc)
		case "DELETE /config/backups/dumps/maktaba-1.dump":
			_, _ = io.WriteString(w, doc)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no dump"}`)
		}
	}))
	t.Cleanup(srv.Close)
	tf := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tf, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := testDB(t)
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Supervisor: &Supervisor{URL: srv.URL, TokenFile: tf}, DB: db}
	r := chi.NewRouter()
	h.Mount(r)
	return r, calls, h
}

// TestBackupsRefuseNonAdmins: the API enforces the role on every backups route,
// before the supervisor is called.
func TestBackupsRefuseNonAdmins(t *testing.T) {
	r, calls, _ := backupsFixture(t)
	viewer := &principal.Principal{UserID: "u2", Libraries: []string{"lib"}}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/server/backups", ""},
		{"PUT", "/api/server/backups", `{"keep":3}`},
		{"DELETE", "/api/server/backups/dumps/maktaba-1.dump", ""},
	} {
		for _, p := range []*principal.Principal{nil, viewer} {
			if rec := call(r, p, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v: %d, want 403", c.method, c.path, p, rec.Code)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the supervisor was called %d times", n)
	}
}

func TestBackupsForAnAdmin(t *testing.T) {
	r, _, h := backupsFixture(t)
	rec := call(r, admin, "GET", "/api/server/backups", "")
	var doc map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["available"] != true {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	if rec := call(r, admin, "PUT", "/api/server/backups", `{"hour":30}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("refused %d", rec.Code)
	}
	if rec := call(r, admin, "PUT", "/api/server/backups", `{"keep":14,"update_keep":4,"move_dumps":true}`); rec.Code != 200 {
		t.Fatalf("PUT %d %s", rec.Code, rec.Body)
	}
	// kept in app_settings like the other rules
	var kept, updates string
	if err := h.DB.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, BackupsSettingsKey).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err := h.DB.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, "rules.updates").Scan(&updates); err != nil {
		t.Fatal(err)
	}
	var u map[string]any
	_ = json.Unmarshal([]byte(updates), &u)
	if kept != `{"dir":"/b","keep":14,"update_keep":4}` || u["backups_keep"] != float64(4) {
		t.Errorf("app_settings %s / %s", kept, updates)
	}
	if rec := call(r, admin, "DELETE", "/api/server/backups/dumps/maktaba-1.dump", ""); rec.Code != 200 {
		t.Errorf("delete %d", rec.Code)
	}
	if rec := call(r, admin, "DELETE", "/api/server/backups/dumps/nope.dump", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete a missing one %d", rec.Code)
	}
	var n int
	if err := h.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN (?, ?)`, ActionBackupsChanged, ActionBackupDeleted).
		Scan(&n); err != nil || n != 2 {
		t.Errorf("audit rows %d %v", n, err)
	}
}
