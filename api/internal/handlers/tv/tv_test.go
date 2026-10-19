package tv

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		tuner_count   INTEGER NOT NULL DEFAULT 20,
		enabled       INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		t.Fatal(err)
	}
	// slot 0081 channels (the columns read here) + slot 0083/0117 channel_runtime and streaming_hosts
	if _, err := db.Exec(`CREATE TABLE channels (id TEXT PRIMARY KEY, number INTEGER, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE channel_runtime (
		channel_id      TEXT    PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
		host            TEXT    NOT NULL,
		pid             INTEGER,
		state           TEXT    NOT NULL DEFAULT 'idle',
		viewer_count    INTEGER NOT NULL DEFAULT 0,
		started_at      TEXT,
		last_segment_at TEXT,
		host_id         TEXT,
		stream_base_url TEXT,
		encoder         TEXT,
		restarts        INTEGER NOT NULL DEFAULT 0,
		nvenc_fallbacks INTEGER NOT NULL DEFAULT 0,
		heartbeat_at    TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE streaming_hosts (
		host_id TEXT PRIMARY KEY, host TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'home',
		stream_base_url TEXT, encoder TEXT, max_active INTEGER NOT NULL DEFAULT 0,
		active_channels INTEGER NOT NULL DEFAULT 0, nvenc_free INTEGER NOT NULL DEFAULT 0,
		vt_free INTEGER NOT NULL DEFAULT 0, started_at TEXT, heartbeat_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	(&Handler{DB: db, PublicBaseURL: base, DefaultStreamPort: "8782", Now: testNow}).Mount(r)
	return db, r
}

func testNow() time.Time { return time.Date(2026, 10, 18, 20, 6, 10, 0, time.UTC) }

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
	if len(id) != 8 || out["enabled"] != false || out["tuner_count"] != float64(20) || out["max_tuners"] != float64(32) || out["friendly_name"] != "Maktaba" {
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
	for _, body := range []string{`{"tuner_count":0}`, `{"tuner_count":33}`, `{"friendly_name":"  "}`} {
		if code, _ := do(t, srv, "PATCH", "/api/tv/hdhr", body, admin); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: code %d, want 422", body, code)
		}
	}
}

func TestOnAirListsRuntimeRowsAndHosts(t *testing.T) {
	db, srv := newServer(t, "")
	if code, _ := do(t, srv, "GET", "/api/tv/onair", "", user); code != http.StatusForbidden {
		t.Fatalf("user GET: %d", code)
	}
	code, out := do(t, srv, "GET", "/api/tv/onair", "", admin)
	if code != 200 || len(out["channels"].([]any)) != 0 || len(out["hosts"].([]any)) != 0 {
		t.Fatalf("empty: %d %v", code, out)
	}
	if _, err := db.Exec(`INSERT INTO channels VALUES ('c1', 101, 'Quran'), ('c2', 102, 'Kids')`); err != nil {
		t.Fatal(err)
	}
	// testNow is 2026-10-18T20:06:10Z: c2's heartbeat is 10 s old, c1's an hour
	if _, err := db.Exec(`INSERT INTO channel_runtime
		(channel_id, host, pid, state, viewer_count, started_at, last_segment_at, host_id, stream_base_url,
		 encoder, restarts, nvenc_fallbacks, heartbeat_at)
		VALUES ('c2', 'studio', 42, 'live', 3, '2026-10-18T20:00:00Z', '2026-10-18T20:05:00Z', 'home-1',
		        'http://studio:8781', 'videotoolbox', 1, 2, '2026-10-18T20:06:00Z'),
		       ('c1', 'pc', NULL, 'live', 0, '2026-10-18T19:00:00Z', '2026-10-18T19:06:00Z', NULL,
		        NULL, NULL, 0, 0, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO streaming_hosts
		(host_id, host, role, stream_base_url, encoder, max_active, active_channels, nvenc_free, vt_free, heartbeat_at)
		VALUES ('home-1', 'studio', 'home', 'http://studio:8781', 'videotoolbox', 16, 1, -1, -1, '2026-10-18T20:06:00Z')`); err != nil {
		t.Fatal(err)
	}
	code, out = do(t, srv, "GET", "/api/tv/onair", "", admin)
	chans := out["channels"].([]any)
	if code != 200 || len(chans) != 2 {
		t.Fatalf("onair: %d %v", code, out)
	}
	first, second := chans[0].(map[string]any), chans[1].(map[string]any)
	if first["number"] != float64(101) || first["host"] != "pc" || first["pid"] != nil ||
		first["heartbeat_at"] != "2026-10-18T19:06:00Z" || first["stale"] != true {
		t.Errorf("first: %v", first)
	}
	if second["name"] != "Kids" || second["encoder"] != "videotoolbox" || second["viewers"] != float64(3) ||
		second["restarts"] != float64(1) || second["nvenc_fallbacks"] != float64(2) || second["pid"] != float64(42) ||
		second["started_at"] != "2026-10-18T20:00:00Z" || second["host_id"] != "home-1" ||
		second["stream_base_url"] != "http://studio:8781" || second["stale"] != false {
		t.Errorf("second: %v", second)
	}
	hosts := out["hosts"].([]any)
	if len(hosts) != 1 {
		t.Fatalf("hosts: %v", hosts)
	}
	if h := hosts[0].(map[string]any); h["role"] != "home" || h["max_active"] != float64(16) ||
		h["vt_free"] != float64(-1) || h["stale"] != false {
		t.Errorf("host: %v", h)
	}
}
