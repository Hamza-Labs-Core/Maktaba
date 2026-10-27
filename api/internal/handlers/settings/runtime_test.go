package settings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg/runtimecfgtest"
)

func patch(t *testing.T, r chi.Router, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// PATCH checks each runtime value, refuses keys nothing reads, and the
// shared reader sees a stored value at once.
func TestPatchRuntimeSettings(t *testing.T) {
	db := runtimecfgtest.DB(t)
	reader := runtimecfg.New(db)
	h := &Handler{DB: db, Settings: reader}
	r := chi.NewRouter()
	h.Mount(r)
	ctx := context.Background()
	if _, ok := reader.Float(ctx, runtimecfg.SearchFTSWeight); ok { // cache the miss
		t.Fatal("set before the patch")
	}
	if rec := patch(t, r, `{"search.fts_weight": 2.5, "ws.heartbeat_interval_sec": 15, "auth.open_registration": true}`); rec.Code != 200 {
		t.Fatalf("patch %d %s", rec.Code, rec.Body.String())
	}
	if f, ok := reader.Float(ctx, runtimecfg.SearchFTSWeight); !ok || f != 2.5 {
		t.Fatalf("the reader did not see the patch: %v %v", f, ok)
	}
	for _, bad := range []string{
		`{"search.semantic_weight": -1}`,
		`{"session.url_ttl_sec": 10}`,
		`{"ws.heartbeat_interval_sec": "fast"}`,
		`{"auth.open_registration": "yes"}`,
	} {
		if rec := patch(t, r, bad); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	// subtitle.url_ttl_sec is not a runtime setting (subtitles ride the session's signed URL)
	if rec := patch(t, r, `{"subtitle.url_ttl_sec": 600}`); rec.Code != http.StatusForbidden {
		t.Errorf("subtitle.url_ttl_sec: %d", rec.Code)
	}
}

// An admin renames the home (home.name): a name of up to 64 characters, empty for the
// computer's name again; anything else is refused and nothing is stored.
func TestPatchHomeName(t *testing.T) {
	db := runtimecfgtest.DB(t)
	reader := runtimecfg.New(db)
	h := &Handler{DB: db, Settings: reader}
	r := chi.NewRouter()
	h.Mount(r)
	ctx := context.Background()
	if rec := patch(t, r, `{"home.name": "  بيت سارة  "}`); rec.Code != 200 {
		t.Fatalf("patch %d %s", rec.Code, rec.Body.String())
	}
	if n, ok := reader.Text(ctx, runtimecfg.HomeName); !ok || n != "بيت سارة" {
		t.Fatalf("home name = %q %v", n, ok)
	}
	for _, bad := range []string{
		`{"home.name": 7}`,
		`{"home.name": "` + strings.Repeat("x", MaxHomeName+1) + `"}`,
		`{"home.name": "a\nb"}`,
	} {
		if rec := patch(t, r, bad); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%.40s: %d", bad, rec.Code)
		}
	}
	if rec := patch(t, r, `{"home.name": "`+strings.Repeat("é", MaxHomeName)+`"}`); rec.Code != 200 {
		t.Errorf("64 characters: %d", rec.Code)
	}
	if rec := patch(t, r, `{"home.name": ""}`); rec.Code != 200 {
		t.Fatalf("clear %d", rec.Code)
	}
	if n, ok := reader.Text(ctx, runtimecfg.HomeName); ok {
		t.Fatalf("a cleared name reads as set: %q", n)
	}
}

// The home's usage data to Maktaba Cloud is the admin's switch (Epic 16 story
// 16.5): off unless set, an admin turns it on and off, and nobody else may
// (TestEveryRouteIsAdminOnly holds the PATCH for everyone else). The sender's own
// keys (the home's id, the last report's day) are not runtime settings.
func TestShareServerDataSwitch(t *testing.T) {
	db := runtimecfgtest.DB(t)
	reader := runtimecfg.New(db)
	h := &Handler{DB: db, Settings: reader}
	r := chi.NewRouter()
	h.Mount(r)
	ctx := context.Background()
	if on, _ := reader.Bool(ctx, runtimecfg.ShareServerData); on {
		t.Fatal("on before the admin turned it on")
	}
	for _, v := range []string{"true", "false"} {
		if rec := patch(t, r, `{"telemetry.share_server_data": `+v+`}`); rec.Code != 200 {
			t.Fatalf("patch %s: %d %s", v, rec.Code, rec.Body.String())
		}
		if on, ok := reader.Bool(ctx, runtimecfg.ShareServerData); !ok || on != (v == "true") {
			t.Fatalf("after %s the switch reads %v %v", v, on, ok)
		}
	}
	if rec := patch(t, r, `{"telemetry.share_server_data": "on"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a non-bool: %d", rec.Code)
	}
	for _, k := range []string{"telemetry.server_install_id", "telemetry.server_last_report_day"} {
		if rec := patch(t, r, `{"`+k+`": "x"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", k, rec.Code)
		}
	}
}
