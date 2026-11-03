package queues

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	comps "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.07: the built-in extensions are components. The routes of today keep their
// answers, and enabled goes through the component's desired state.

const extAdminID = "00000000-0000-0000-0000-0000000000a1"

func extRouter(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	for _, name := range []string{"plex", "dizquetv", "cloudflare_stream"} {
		if _, err := db.Exec(`INSERT INTO extensions (name, enabled, settings, updated_at)
			VALUES ($1, true, '{"url":"http://plex:32400"}'::jsonb, now()) ON CONFLICT (name) DO NOTHING`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin)
		VALUES ($1, 'ext-admin', 'x', true)`, extAdminID); err != nil {
		t.Fatal(err)
	}
	h := &Handler{DB: db}
	r := chi.NewRouter()
	r.Use(authmw.RequireAuthExcept(nil)) // the server's gate: no one gets 401
	h.Mount(r)
	return r, db
}

func extCall(t *testing.T, r http.Handler, p *principal.Principal, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func extAdmin() *principal.Principal {
	return &principal.Principal{UserID: extAdminID, IsAdmin: true, AccessAllLibraries: true}
}

// T01: switching Plex off through today's route writes both sides and one audit row.
func TestPutExtensionWritesTheComponent(t *testing.T) {
	r, db := extRouter(t)
	rec := extCall(t, r, extAdmin(), "PUT", "/api/extensions/plex", map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var on bool
	if err := db.QueryRow(`SELECT enabled FROM extensions WHERE name = 'plex'`).Scan(&on); err != nil || on {
		t.Fatalf("extensions.enabled %v %v", on, err)
	}
	d, err := components.New(db).DesiredOf(context.Background(), "ext.plex")
	if err != nil || d.Want != comps.WantInstalled {
		t.Fatalf("desired %+v %v", d, err)
	}
	var n int
	var actor sql.NullString
	if err := db.QueryRow(`SELECT count(*), max(actor_user_id::text) FROM audit_log
		WHERE action = 'component.disable' AND target_id = 'ext.plex'`).Scan(&n, &actor); err != nil || n != 1 {
		t.Fatalf("%d audit rows, %v", n, err)
	}
	if actor.String != extAdminID {
		t.Fatalf("the audit row names %q, not the admin", actor.String)
	}
	var got Extension
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Enabled || got.Component != "ext.plex" {
		t.Fatalf("answer %+v", got)
	}
	// settings alone leave the component as it is
	rec = extCall(t, r, extAdmin(), "PUT", "/api/extensions/plex", map[string]any{"settings": map[string]any{"url": "http://other:32400"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE target_id = 'ext.plex'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d audit rows after a settings change", n)
	}
}

// T04: GET answers today's fields plus component and held; enabled is the desired state.
func TestGetExtensionsAddsComponentAndHeld(t *testing.T) {
	r, db := extRouter(t)
	held := store.Desired{ID: "ext.dizquetv", Want: comps.WantEnabled, Hold: &store.Hold{By: "admin"}}
	if _, err := components.SetDesired(context.Background(), components.New(db), held,
		store.Audit{By: "admin", Action: "component.hold"}); err != nil {
		t.Fatal(err)
	}
	rec := extCall(t, r, extAdmin(), "GET", "/api/extensions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Extensions []map[string]any `json:"extensions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	today := []string{"configured", "description", "enabled", "label", "name", "secrets_set",
		"settings", "settings_schema", "updated_at", "version"}
	for _, e := range body.Extensions {
		keys := make([]string, 0, len(e))
		for k := range e {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := append(append([]string{}, today...), "component", "held")
		sort.Strings(want)
		if e["component"] == nil {
			want = append(append([]string{}, today...), "held")
			sort.Strings(want)
		}
		if len(keys) != len(want) {
			t.Fatalf("%v: keys %v, want %v", e["name"], keys, want)
		}
		for i := range keys {
			if keys[i] != want[i] {
				t.Fatalf("%v: keys %v, want %v", e["name"], keys, want)
			}
		}
		if e["name"] == "dizquetv" && (e["component"] != "ext.dizquetv" || e["held"] != true || e["enabled"] != true) {
			t.Fatalf("dizquetv %v", e)
		}
		if e["name"] == "plex" && (e["component"] != "ext.plex" || e["held"] != false) {
			t.Fatalf("plex %v", e)
		}
	}
}

// T12: the test route runs whatever the component's state (an admin sets up and tests an
// extension before turning it on).
func TestTestExtensionRunsWhenTheComponentIsOff(t *testing.T) {
	r, db := extRouter(t)
	if _, err := components.SetDesired(context.Background(), components.New(db),
		store.Desired{ID: "ext.plex", Want: comps.WantInstalled},
		store.Audit{By: "admin", Action: "component.disable"}); err != nil {
		t.Fatal(err)
	}
	rec := extCall(t, r, extAdmin(), "POST", "/api/extensions/plex/test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// T09: each route as no one, a viewer, a cloud-linked play sign-in and an admin.
func TestExtensionRoutesRoles(t *testing.T) {
	r, db := extRouter(t)
	play := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000b1", IsAdmin: true, AccessAllLibraries: true}
	play.ApplyCloudScope(principal.CloudScopePlay, nil)
	viewer := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}
	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/extensions", nil},
		{"PUT", "/api/extensions/plex", map[string]any{"enabled": false}},
		{"POST", "/api/extensions/plex/test", nil},
	}
	for _, rt := range routes {
		for _, c := range []struct {
			who  string
			p    *principal.Principal
			want int
		}{
			{"no one", nil, http.StatusUnauthorized},
			{"viewer", viewer, http.StatusForbidden},
			{"cloud play", play, http.StatusForbidden},
		} {
			if rec := extCall(t, r, c.p, rt.method, rt.path, rt.body); rec.Code != c.want {
				t.Errorf("%s %s as %s: %d, want %d", rt.method, rt.path, c.who, rec.Code, c.want)
			}
		}
	}
	// nothing changed for any of them
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM components`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d desired rows after refused calls", n)
	}
	for _, rt := range routes {
		if rec := extCall(t, r, extAdmin(), rt.method, rt.path, rt.body); rec.Code != http.StatusOK {
			t.Errorf("%s %s as admin: %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
}
