package rules

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Story 51.13 on Postgres (jsonb app_settings, the advisory lock): a save writes its
// version; a failed section takes only a replace, which clears failed.
func TestPGPatchVersions(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	h := &Handler{DB: db}
	r := chi.NewRouter()
	h.Mount(r)
	pgAdmin := &principal.Principal{IsAdmin: true}

	if code, resp := do(t, r, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":11}}`, pgAdmin); code != http.StatusOK {
		t.Fatalf("%d %v", code, resp)
	}
	if v, ok := storedVersionOf(t, db, "groups"); !ok || v["major"] != float64(1) {
		t.Fatalf("version %v %v", v, ok)
	}
	if _, err := db.Exec(`UPDATE app_settings SET value = '{"major":1,"minor":0,"failed":"boom"}'::jsonb
		WHERE key = 'rules_version.groups'`); err != nil {
		t.Fatal(err)
	}
	code, resp := do(t, r, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":12}}`, pgAdmin)
	if code != http.StatusConflict || resp["code"] != "settings_replace_required" {
		t.Fatalf("want 409, got %d %v", code, resp)
	}
	if code, resp := do(t, r, http.MethodPatch, "/api/rules/groups", `{"set":{"min_topic":12},"replace":true}`, pgAdmin); code != http.StatusOK {
		t.Fatalf("replace: %d %v", code, resp)
	}
	if v, _ := storedVersionOf(t, db, "groups"); v["failed"] != nil {
		t.Fatalf("failed kept: %v", v)
	}
	code, resp = do(t, r, http.MethodGet, "/api/rules", "", pgAdmin)
	if code != http.StatusOK || sectionOf(t, resp, "transcription")["component"] != "transcription" {
		t.Fatalf("GET %d", code)
	}
}
