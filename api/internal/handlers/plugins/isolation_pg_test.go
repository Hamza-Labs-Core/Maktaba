package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// Epic 59 story 59.07 T05: Allow plugins with container-only isolation is the admin's
// alone: a viewer and an editor (library write, not an admin) 403 and nothing changes; an
// admin 200 with the password typed again, the switch is on, the audit log has plugin.isolation, and the unreviewed
// policy's own route keeps it.
func TestContainerIsolationSwitchIsAdminOnlyAndAudited(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	adminID := "00000000-0000-0000-0000-0000000000a1"
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'owner', 'x', true)`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, '{"home": {"level": "container", "missing": ["landlock", "namespaces"], "landlock": 0}}')`, pl.IsolationKey); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	verify := func(_ context.Context, userID, password string) error {
		if userID == adminID && password == "right" {
			return nil
		}
		return errors.New("wrong password")
	}
	(&Admin{L: &pl.Lifecycle{DB: db}, VerifyPassword: verify}).Mount(r)
	call := func(p *principal.Principal, method, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/plugins/isolation", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	viewer := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}
	editor := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c2", Libraries: []string{"00000000-0000-0000-0000-0000000000d1"}}
	admin := &principal.Principal{UserID: adminID, IsAdmin: true, AccessAllLibraries: true}
	for _, who := range []struct {
		name string
		p    *principal.Principal
	}{{"viewer", viewer}, {"editor", editor}} {
		if code, _ := call(who.p, http.MethodPut, `{"allow": true}`); code != http.StatusForbidden {
			t.Fatalf("%s: %d, want 403", who.name, code)
		}
	}
	if code, got := call(admin, http.MethodGet, ""); code != http.StatusOK || got["level"] != "container" || got["held"] != true ||
		got["allow_container_isolation"] != false {
		t.Fatalf("GET as the admin: %d %v", code, got)
	}
	if code, _ := call(admin, http.MethodPut, `{}`); code != http.StatusBadRequest {
		t.Fatalf("PUT without allow: %d, want 400", code)
	}
	// turning it on asks for the password again
	for _, body := range []string{`{"allow": true}`, `{"allow": true, "password": "wrong"}`} {
		if code, got := call(admin, http.MethodPut, body); code != http.StatusUnauthorized || got["code"] != "reauth_required" {
			t.Fatalf("PUT %s: %d %v, want 401 reauth_required", body, code, got)
		}
	}
	code, got := call(admin, http.MethodPut, `{"allow": true, "password": "right"}`)
	if code != http.StatusOK || got["allow_container_isolation"] != true || got["held"] != false {
		t.Fatalf("PUT as the admin: %d %v", code, got)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'plugin.isolation' AND actor_user_id::text = $1`, adminID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows %d (%v), want 1", n, err)
	}
	// story 59.16 T03: who, when and the level
	var level string
	var at sql.NullTime
	if err := db.QueryRow(`SELECT COALESCE(payload::jsonb->>'level', ''), ts FROM audit_log WHERE action = 'plugin.isolation'`).
		Scan(&level, &at); err != nil || level == "" || !at.Valid {
		t.Fatalf("audit level %q at %v (%v)", level, at, err)
	}
	// the unreviewed policy's route does not reset it
	req := httptest.NewRequest(http.MethodPut, "/api/plugins/policy", strings.NewReader(`{"allow_unreviewed": false}`))
	req = req.WithContext(principal.WithPrincipal(req.Context(), admin))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT policy: %d %s", rec.Code, rec.Body)
	}
	if p, err := pl.LoadPolicy(req.Context(), db); err != nil || !p.AllowContainerIsolation {
		t.Fatalf("policy %+v (%v): the switch was reset", p, err)
	}
}
