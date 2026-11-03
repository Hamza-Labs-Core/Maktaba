package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// newVersionedServer serves the embedded schema with section at major (story 51.13).
func newVersionedServer(t *testing.T, section string, major int) (*sql.DB, http.Handler) {
	t.Helper()
	s, err := ParseSchema(schemaJSON)
	if err != nil {
		t.Fatal(err)
	}
	s.section(section).Version = []int{major, 0}
	db := newTestDB(t)
	h := &Handler{DB: db, Schema: s}
	r := chi.NewRouter()
	h.Mount(r)
	return db, r
}

func storedVersionOf(t *testing.T, db *sql.DB, section string) (map[string]any, bool) {
	t.Helper()
	var v string
	err := db.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, VersionPrefix+section).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatal(err)
	}
	return m, true
}

func putVersion(t *testing.T, db *sql.DB, section, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2)`, VersionPrefix+section, value); err != nil {
		t.Fatal(err)
	}
}

// T09: a save writes the section's version with the value.
func TestPatch_WritesTheVersionWithTheValue(t *testing.T) {
	db, srv := newServer(t)
	if code, resp := do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11}}`, admin); code != http.StatusOK {
		t.Fatalf("%d %v", code, resp)
	}
	v, ok := storedVersionOf(t, db, "groups")
	if !ok || v["major"] != float64(1) || v["minor"] != float64(0) {
		t.Fatalf("rules_version.groups = %v (%v)", v, ok)
	}
	if _, failed := v["failed"]; failed {
		t.Errorf("a save marked the version failed: %v", v)
	}
}

// T07: while the stored value is at an older major the home has not migrated, a save
// is refused and writes nothing; a section with nothing stored saves at once.
func TestPatch_RefusedUntilTheHomeMigrates(t *testing.T) {
	db, srv := newVersionedServer(t, "groups", 2)
	putRow(t, db, "groups", `{"min_topic":9}`)
	code, resp := do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11}}`, admin)
	if code != http.StatusConflict || resp["code"] != "settings_need_migration" {
		t.Fatalf("want 409 settings_need_migration, got %d %v", code, resp)
	}
	if row, _ := storedRow(t, db, "groups"); row["min_topic"] != float64(9) {
		t.Errorf("the refused save wrote: %v", row)
	}
	if _, ok := storedVersionOf(t, db, "groups"); ok {
		t.Error("the refused save wrote a version")
	}

	db2, srv2 := newVersionedServer(t, "groups", 2)
	if code, resp := do(t, srv2, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11}}`, admin); code != http.StatusOK {
		t.Fatalf("nothing stored: want 200, got %d %v", code, resp)
	}
	if v, _ := storedVersionOf(t, db2, "groups"); v["major"] != float64(2) {
		t.Errorf("version %v, want major 2", v)
	}
}

// T08: a section the home could not migrate takes only a whole-section save
// (replace), which clears failed and keeps the previous value for support.
func TestPatch_AFailedSectionTakesAReplace(t *testing.T) {
	db, srv := newServer(t)
	putRow(t, db, "groups", `{"min_topic":9,"min_playlist":4}`)
	putVersion(t, db, "groups", `{"major":1,"minor":0,"failed":"migrate_v1_to_v2: boom","previous":{"major":1,"value":{}}}`)

	code, resp := do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11}}`, admin)
	if code != http.StatusConflict || resp["code"] != "settings_replace_required" {
		t.Fatalf("want 409 settings_replace_required, got %d %v", code, resp)
	}
	code, resp = do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11},"replace":true,"version":[1,0]}`, admin)
	if code != http.StatusOK {
		t.Fatalf("replace: %d %v", code, resp)
	}
	row, _ := storedRow(t, db, "groups")
	if len(row) != 1 || row["min_topic"] != float64(11) {
		t.Errorf("replace must store only what it sent: %v", row)
	}
	v, _ := storedVersionOf(t, db, "groups")
	if _, failed := v["failed"]; failed || v["previous"] == nil {
		t.Errorf("version after replace: %v", v)
	}
	// and a plain save works again
	if code, resp := do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_playlist":5}}`, admin); code != http.StatusOK {
		t.Fatalf("after replace: %d %v", code, resp)
	}
}

// A value written by a newer release (a rollback) and a form built from another major
// are refused: neither is read as this release's shape.
func TestPatch_RefusesNewerSchemaValues(t *testing.T) {
	db, srv := newServer(t)
	putRow(t, db, "groups", `{"min_topic":9}`)
	putVersion(t, db, "groups", `{"major":3,"minor":0}`)
	code, resp := do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11}}`, admin)
	if code != http.StatusConflict || resp["code"] != "settings_newer_schema" {
		t.Fatalf("want 409 settings_newer_schema, got %d %v", code, resp)
	}
	code, resp = do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11},"replace":true}`, admin)
	if code != http.StatusOK {
		t.Fatalf("replace: %d %v", code, resp)
	}
	if v, _ := storedVersionOf(t, db, "groups"); v["major"] != float64(1) {
		t.Errorf("version after replace: %v", v)
	}

	_, srv2 := newServer(t)
	code, resp = do(t, srv2, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11},"version":[2,0]}`, admin)
	if code != http.StatusConflict || resp["code"] != "settings_schema_changed" {
		t.Fatalf("want 409 settings_schema_changed, got %d %v", code, resp)
	}
}

// GET names each section's component (its state) and version, and what the home's
// migration said.
func TestGet_ComponentAndVersion(t *testing.T) {
	db, srv := newServer(t)
	if _, err := db.Exec(`CREATE TABLE components (id TEXT PRIMARY KEY, want TEXT NOT NULL, hold_by TEXT, hold_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO components (id, want) VALUES ('transcription', 'installed')`); err != nil {
		t.Fatal(err)
	}
	putRow(t, db, "groups", `{"min_topic":9}`)
	putVersion(t, db, "groups", `{"major":1,"minor":0,"failed":"boom"}`)
	code, resp := do(t, srv, http.MethodGet, "/api/rules", "", admin)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, resp)
	}
	tr := sectionOf(t, resp, "transcription")
	if tr["component"] != "transcription" || tr["component_state"] != "off" {
		t.Errorf("transcription: component %v (%v)", tr["component"], tr["component_state"])
	}
	if v := tr["version"].([]any); len(v) != 2 || v[0] != float64(1) || v[1] != float64(0) {
		t.Errorf("version %v", tr["version"])
	}
	g := sectionOf(t, resp, "groups")
	if g["component"] != Owner("groups") || g["component_state"] != "enabled" || g["failed"] != "boom" {
		t.Errorf("groups: %v %v %v", g["component"], g["component_state"], g["failed"])
	}
	for _, s := range resp["sections"].([]any) {
		sm := s.(map[string]any)
		if _, ok := sm["component"]; !ok {
			t.Errorf("%s has no component key", sm["key"])
		}
	}
}

func TestGet_NeedsMigration(t *testing.T) {
	db, srv := newVersionedServer(t, "groups", 2)
	putRow(t, db, "groups", `{"min_topic":9}`)
	_, resp := do(t, srv, http.MethodGet, "/api/rules", "", admin)
	if g := sectionOf(t, resp, "groups"); g["needs_migration"] != true {
		t.Errorf("groups at 1.0 stored, 2.0 code: %v", g)
	}
}

// T10: the save and its new body fields are a home admin's: no sign-in 401, a viewer and
// an admin's cloud play sign-in 403, nothing written.
func TestPatch_RolesWithReplace(t *testing.T) {
	play := &principal.Principal{UserID: "u-admin", IsAdmin: true}
	play.ApplyCloudScope(principal.CloudScopePlay, nil)
	for _, c := range []struct {
		name string
		p    *principal.Principal
		want int
	}{
		{"no sign-in", nil, http.StatusUnauthorized},
		{"viewer", user, http.StatusForbidden},
		{"cloud play sign-in", play, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, srv := newServer(t)
			putVersion(t, db, "groups", `{"major":1,"minor":0,"failed":"boom"}`)
			code, _ := do(t, srv, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11},"replace":true}`, c.p)
			if code != c.want {
				t.Fatalf("status %d, want %d", code, c.want)
			}
			if _, ok := storedRow(t, db, "groups"); ok {
				t.Error("written")
			}
			if v, _ := storedVersionOf(t, db, "groups"); v["failed"] != "boom" {
				t.Errorf("version changed: %v", v)
			}
			if code, _ := do(t, srv, http.MethodGet, "/api/rules", "", c.p); code != c.want {
				t.Errorf("GET: %d, want %d", code, c.want)
			}
		})
	}
}

// The API's own readers of a section the home could not migrate use the defaults.
func TestEffective_AFailedSectionIsTheDefaults(t *testing.T) {
	db := newTestDB(t)
	putRow(t, db, "relay", `{"max_in_flight":9}`)
	got, err := Effective(context.Background(), db, "relay")
	if err != nil || got["max_in_flight"] == json.Number("4") {
		t.Fatalf("before: %v %v", got, err)
	}
	putVersion(t, db, "relay", `{"major":1,"minor":0,"failed":"boom"}`)
	got, err = Effective(context.Background(), db, "relay")
	if err != nil || got["max_in_flight"] != json.Number("4") {
		t.Fatalf("failed section: %v %v, want the default 4", got, err)
	}
}
