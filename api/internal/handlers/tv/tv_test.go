package tv

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// newServer opens a SQLite file with the slot-0084 hdhr_device table
// (shared/db/migrations/0084_hdhomerun.sqlite.sql).
func newServer(t *testing.T, base string) (*sql.DB, http.Handler) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tv.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE hdhr_device (
		id            INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
		device_id     TEXT    NOT NULL,
		device_uuid   TEXT    NOT NULL,
		friendly_name TEXT    NOT NULL DEFAULT 'Maktaba',
		tuner_count   INTEGER NOT NULL DEFAULT 4,
		enabled       INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	(&Handler{DB: db, PublicBaseURL: base, DefaultStreamPort: "8782"}).Mount(r)
	return db, r
}

var (
	admin = &principal.Principal{UserID: "a", IsAdmin: true}
	user  = &principal.Principal{UserID: "u"}
)

func do(t *testing.T, srv http.Handler, method, path, body string, p *principal.Principal) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "tv.lan:8780"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestInfoFromConfig(t *testing.T) {
	_, srv := newServer(t, "http://192.168.1.20:8782/")
	code, out := do(t, srv, "GET", "/api/tv/info", "", user)
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	want := map[string]any{
		"stream_base_url":   "http://192.168.1.20:8782",
		"hdhr_address":      "192.168.1.20:8782",
		"xmltv_url":         "http://192.168.1.20:8782/xmltv.xml",
		"m3u_url":           "http://192.168.1.20:8782/lineup.m3u",
		"base_from_config":  true,
		"hls_path_template": "/stream/channel/{id}/index.m3u8",
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("%s = %v, want %v", k, out[k], v)
		}
	}
}

func TestInfoFallbackUsesRequestHost(t *testing.T) {
	_, srv := newServer(t, "")
	_, out := do(t, srv, "GET", "/api/tv/info", "", user)
	if out["stream_base_url"] != "http://tv.lan:8782" || out["base_from_config"] != false {
		t.Fatalf("fallback: %v", out)
	}
}

func TestInfoNeedsAuth(t *testing.T) {
	_, srv := newServer(t, "")
	if code, _ := do(t, srv, "GET", "/api/tv/info", "", nil); code != http.StatusForbidden {
		t.Fatalf("anon info: %d", code)
	}
}

func TestHDHRAdminOnly(t *testing.T) {
	_, srv := newServer(t, "")
	if code, _ := do(t, srv, "GET", "/api/tv/hdhr", "", user); code != http.StatusForbidden {
		t.Fatalf("user GET: %d", code)
	}
	if code, _ := do(t, srv, "PATCH", "/api/tv/hdhr", `{"enabled":true}`, user); code != http.StatusForbidden {
		t.Fatalf("user PATCH: %d", code)
	}
}

func TestHDHRProvisionAndPatch(t *testing.T) {
	db, srv := newServer(t, "")
	code, out := do(t, srv, "GET", "/api/tv/hdhr", "", admin)
	if code != 200 {
		t.Fatalf("GET %d %v", code, out)
	}
	id, _ := out["device_id"].(string)
	if len(id) != 8 || out["enabled"] != false || out["tuner_count"] != float64(4) || out["friendly_name"] != "Maktaba" {
		t.Fatalf("provisioned device: %v", out)
	}

	code, out = do(t, srv, "PATCH", "/api/tv/hdhr",
		`{"enabled":true,"friendly_name":"  Maktaba TV ","tuner_count":2}`, admin)
	if code != 200 {
		t.Fatalf("PATCH %d %v", code, out)
	}
	if out["enabled"] != true || out["friendly_name"] != "Maktaba TV" || out["tuner_count"] != float64(2) {
		t.Fatalf("patched: %v", out)
	}
	if out["device_id"] != id {
		t.Fatalf("device id must stay stable: %v vs %s", out["device_id"], id)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM hdhr_device`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("singleton rows = %d (%v)", n, err)
	}
}

func TestHDHRPatchValidation(t *testing.T) {
	_, srv := newServer(t, "")
	for _, body := range []string{`{"tuner_count":0}`, `{"tuner_count":17}`, `{"friendly_name":"  "}`} {
		if code, _ := do(t, srv, "PATCH", "/api/tv/hdhr", body, admin); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: code %d, want 422", body, code)
		}
	}
}
