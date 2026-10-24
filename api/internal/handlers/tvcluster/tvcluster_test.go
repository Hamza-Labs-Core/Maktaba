package tvcluster

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const schema = `
CREATE TABLE channels (id TEXT PRIMARY KEY, number INTEGER, name TEXT, slug TEXT);
CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT, updated_at TEXT);
CREATE TABLE channel_runtime (
	channel_id TEXT PRIMARY KEY, host TEXT NOT NULL, pid INTEGER, state TEXT NOT NULL DEFAULT 'idle',
	viewer_count INTEGER NOT NULL DEFAULT 0, started_at TEXT, last_segment_at TEXT, host_id TEXT,
	stream_base_url TEXT, encoder TEXT, restarts INTEGER NOT NULL DEFAULT 0,
	nvenc_fallbacks INTEGER NOT NULL DEFAULT 0, heartbeat_at TEXT);
CREATE TABLE streaming_hosts (
	host_id TEXT PRIMARY KEY, host TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'home', stream_base_url TEXT,
	encoder TEXT, max_active INTEGER NOT NULL DEFAULT 0, active_channels INTEGER NOT NULL DEFAULT 0,
	nvenc_free INTEGER NOT NULL DEFAULT 0, vt_free INTEGER NOT NULL DEFAULT 0, started_at TEXT, heartbeat_at TEXT,
	enabled INTEGER NOT NULL DEFAULT 0, draining INTEGER NOT NULL DEFAULT 0,
	stream_base_url_source TEXT NOT NULL DEFAULT 'reported', encoders TEXT NOT NULL DEFAULT '[]',
	capacity_override INTEGER, weight INTEGER NOT NULL DEFAULT 1, active TEXT NOT NULL DEFAULT '[]',
	load TEXT NOT NULL DEFAULT '{}', can_push TEXT NOT NULL DEFAULT '{}', can_live INTEGER NOT NULL DEFAULT 0,
	unfit TEXT, version TEXT);
CREATE TABLE channel_placements (
	channel_id TEXT PRIMARY KEY, host_id TEXT NOT NULL, generation INTEGER NOT NULL, state TEXT NOT NULL,
	reason TEXT NOT NULL, placed_at TEXT NOT NULL, acked_at TEXT, released_at TEXT);
CREATE TABLE tv_cluster_secret (id INTEGER PRIMARY KEY, stream_key BLOB NOT NULL, previous BLOB, rotated_at TEXT);
`

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range strings.Split(schema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func admin(r *http.Request) *http.Request {
	return r.WithContext(principal.WithPrincipal(r.Context(), &principal.Principal{UserID: "u1", IsAdmin: true}))
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func seed(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO channels VALUES ('c1', 101, 'One', 'one'), ('c2', 102, 'Two', 'two'), ('c3', 103, 'Three', 'three')`)
	exec(`INSERT INTO app_settings VALUES ('rules.tv_cluster', '{"nodes_stream": true, "pins": {"two": ["pc"]}}', '')`)
	exec(`INSERT INTO streaming_hosts (host_id, host, role, stream_base_url, enabled, encoders, max_active, load, can_push, can_live, version, heartbeat_at)
		VALUES ('h', 'mac', 'home', 'http://10.0.0.1:8081', 1, '["videotoolbox","software"]', 16,
		        '{"cpu":0.2,"protocol":2,"viewers":{"c1":{"hls":2,"tuners":1}},"no_room":{"channel_id":"c3","reason":"all_full","at":"`+ts(now)+`"}}',
		        '{"rtmps":true,"srt":false}', 1, 'v1', ?),
		       ('n', 'pc', 'node', 'http://10.0.0.2:8081', 1, '["nvenc","software"]', 3, '{"protocol":2}', '{}', 0, 'v1', ?),
		       ('old', 'laptop', 'node', '', 0, '[]', 8, '{}', '{}', 0, 'v1', ?)`,
		ts(now.Add(-2*time.Second)), ts(now.Add(-3*time.Second)), ts(now.Add(-time.Hour)))
	exec(`INSERT INTO channel_placements VALUES ('c1', 'h', 7, 'live', 'first_request', ?, ?, NULL),
		('c2', 'n', 8, 'placing', 'pin', ?, NULL, NULL)`, ts(now.Add(-time.Minute)), ts(now.Add(-time.Minute)), ts(now))
	exec(`INSERT INTO channel_runtime (channel_id, host, state, host_id, encoder, restarts, nvenc_fallbacks, started_at)
		VALUES ('c1', 'mac', 'live', 'h', 'videotoolbox', 1, 0, ?), ('c3', 'laptop', 'live', 'old', 'software', 0, 0, ?)`, ts(now), ts(now))
}

// TC1 of 32.5: the page's one read.
func TestClusterGetShape(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	seed(t, db, now)
	h := &Handler{DB: db, Now: func() time.Time { return now }}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/tv/cluster", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("GET", "/api/tv/cluster", nil)))
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Settings.NodesStream || resp.Settings.Policy != "home_first" || resp.Settings.Pins["two"][0] != "pc" {
		t.Errorf("settings %+v", resp.Settings)
	}
	byID := map[string]Host{}
	for _, x := range resp.Hosts {
		byID[x.HostID] = x
	}
	home, node, old := byID["h"], byID["n"], byID["old"]
	if home.State != "online" || home.Used != 1 || home.Capacity != 16 || !home.CanPush["rtmps"] || len(home.Encoders) != 2 {
		t.Errorf("home %+v", home)
	}
	if node.State != "online" || node.Used != 1 || len(node.Channels) != 1 || node.Channels[0] != "c2" {
		t.Errorf("node %+v", node)
	}
	if old.State != "down" || old.Reason == nil {
		t.Errorf("old %+v", old)
	}
	if len(resp.OnAir) != 3 {
		t.Fatalf("on air %+v", resp.OnAir)
	}
	c1 := resp.OnAir[0]
	if c1.ChannelID != "c1" || c1.HostName != "mac" || c1.Encoder != "videotoolbox" || c1.Viewers.HLS != 2 ||
		c1.Viewers.Tuners != 1 || c1.Generation != 7 || c1.Restarts != 1 || c1.Stale {
		t.Errorf("c1 %+v", c1)
	}
	var c3 *OnAir
	for i := range resp.OnAir {
		if resp.OnAir[i].ChannelID == "c3" {
			c3 = &resp.OnAir[i]
		}
	}
	if c3 == nil || !c3.Stale || c3.State != "runtime" {
		t.Errorf("a runtime row without a placement is stale: %+v", c3)
	}
	var noRoom bool
	for _, n := range resp.Notices {
		if n.Kind == "no_room" && n.ChannelID == "c3" {
			noRoom = true
		}
	}
	if !noRoom {
		t.Errorf("notices %+v", resp.Notices)
	}
	// two homes → a notice naming both
	if _, err := db.Exec(`UPDATE streaming_hosts SET role = 'home' WHERE host_id = 'n'`); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("GET", "/api/tv/cluster", nil)))
	if !strings.Contains(rec.Body.String(), `"kind":"two_homes"`) || !strings.Contains(rec.Body.String(), `"pc"`) {
		t.Errorf("two homes not flagged: %s", rec.Body)
	}
}

// TC1 of 32.5: the home cannot be disabled; override 0-64; weight; URL must be http(s).
func TestPutHostValidation(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	seed(t, db, now)
	h := &Handler{DB: db, Now: func() time.Time { return now }}
	r := chi.NewRouter()
	h.Mount(r)
	put := func(id, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, admin(httptest.NewRequest("PUT", "/api/tv/cluster/hosts/"+id, strings.NewReader(body))))
		return rec
	}
	for _, c := range []struct {
		id, body string
		want     int
	}{
		{"h", `{"enabled": false}`, http.StatusConflict},
		{"n", `{"capacity_override": 65}`, http.StatusBadRequest},
		{"n", `{"capacity_override": -1}`, http.StatusBadRequest},
		{"n", `{"weight": 0}`, http.StatusBadRequest},
		{"n", `{"stream_base_url": "ftp://x"}`, http.StatusBadRequest},
		{"h", `{"stream_base_url": "http://1.2.3.4:8081"}`, http.StatusConflict},
		{"zz", `{"enabled": true}`, http.StatusNotFound},
	} {
		if rec := put(c.id, c.body); rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d (%s)", c.id, c.body, rec.Code, c.want, rec.Body)
		}
	}
	rec := put("n", `{"enabled": false, "drain": true, "capacity_override": 2, "weight": 3, "stream_base_url": "http://192.168.1.9:8081/"}`)
	if rec.Code != 200 {
		t.Fatalf("good put: %d %s", rec.Code, rec.Body)
	}
	var x Host
	_ = json.Unmarshal(rec.Body.Bytes(), &x)
	if x.Enabled || !x.Draining || x.CapacityOverride == nil || *x.CapacityOverride != 2 || x.Capacity != 2 ||
		x.Weight != 3 || x.StreamBaseURL != "http://192.168.1.9:8081" || x.StreamBaseURLSource != "admin" || x.State != "disabled" {
		t.Errorf("after put: %+v", x)
	}
	rec = put("n", `{"capacity_override": null, "stream_base_url": null}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &x)
	if x.CapacityOverride != nil || x.StreamBaseURLSource != "reported" {
		t.Errorf("cleared: %+v", x)
	}
}

// Move and stop go to the home streaming service, signed with the cluster key.
func TestMoveAndStopForwarded(t *testing.T) {
	db := newDB(t)
	seed(t, db, time.Now())
	key := []byte("0123456789abcdef0123456789abcdef")
	if _, err := db.Exec(`INSERT INTO tv_cluster_secret (id, stream_key) VALUES (1, ?)`, key); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var lastBody, lastSig, lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		lastBody, lastSig, lastPath = string(b), r.Header.Get("X-Maktaba-Front"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"channel_id":"c","host_id":"n","generation":9}`)
	}))
	defer srv.Close()
	now := time.Now()
	h := &Handler{DB: db, StreamingURL: srv.URL, Now: func() time.Time { return now }}
	r := chi.NewRouter()
	h.Mount(r)
	ch := "6f1c9a2e-0000-4000-8000-000000000001"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("POST", "/api/tv/cluster/channels/"+ch+"/move", strings.NewReader(`{"host_id":"n"}`))))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"generation":9`) {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	if lastPath != "/internal/tv/place/"+ch || !strings.Contains(lastBody, `"action":"move"`) || !strings.Contains(lastBody, `"host_id":"n"`) {
		t.Errorf("forwarded %s %s", lastPath, lastBody)
	}
	exp, _, _ := strings.Cut(lastSig, ".")
	if lastSig == "" || Sign(key, "POST", lastPath, now.Add(60*time.Second)) != lastSig || exp == "" {
		t.Errorf("signature %q", lastSig)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("DELETE", "/api/tv/cluster/channels/"+ch+"/placement", nil)))
	if rec.Code != http.StatusNoContent || !strings.Contains(lastBody, `"stop"`) {
		t.Errorf("stop: %d %s", rec.Code, lastBody)
	}
}

func jsonBody(s string) io.Reader { return strings.NewReader(s) }

// Story 32.11 AC5: failovers in the last 24 h for the System Health TV line.
func TestClusterFailovers24h(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	seed(t, db, now)
	if _, err := db.Exec(`INSERT INTO channel_placements VALUES ('c3', 'h', 9, 'live', 'failover', ?, NULL, NULL),
		('c4', 'h', 3, 'released', 'failover', ?, NULL, NULL)`, ts(now.Add(-time.Hour)), ts(now.Add(-48*time.Hour))); err != nil {
		t.Fatal(err)
	}
	h := &Handler{DB: db, Now: func() time.Time { return now }}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("GET", "/api/tv/cluster", nil)))
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Failovers24h != 1 {
		t.Fatalf("failovers_24h = %d, want 1", resp.Failovers24h)
	}
}

// A home with no streaming host yet answers empty lists, never null: the admin
// page reads view.hosts.filter(...) and went blank on "hosts": null.
func TestClusterGetEmptyListsAreArrays(t *testing.T) {
	db := newDB(t)
	h := &Handler{DB: db, Now: time.Now}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("GET", "/api/tv/cluster", nil)))
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	for _, want := range []string{`"hosts":[]`, `"on_air":[]`, `"notices":[]`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("want %s in %s", want, rec.Body)
		}
	}
}
