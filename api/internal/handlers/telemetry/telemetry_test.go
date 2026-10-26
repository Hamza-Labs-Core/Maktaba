package telemetry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
)

const pseud = "ABCDEFGHJKMNPQRS"

// testDB is a SQLite database with slot 0156's tables, from the migration itself,
// and a library_roots table holding roots.
func testDB(t *testing.T, roots ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	stmts := migrationStatements(t, "0156_telemetry.sqlite.sql")
	stmts = append(stmts, `CREATE TABLE library_roots (id TEXT PRIMARY KEY, library_id TEXT, path TEXT, path_canonical TEXT)`)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v: %s", err, s)
		}
	}
	for i, r := range roots {
		if _, err := db.Exec(`INSERT INTO library_roots VALUES ($1, 'lib', $2, $2)`, fmt.Sprint(i), r); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// migrationStatements is a migration's Up statements, comments dropped.
func migrationStatements(t *testing.T, name string) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(b), "-- +goose Down")
	var out []string
	for _, s := range strings.Split(up, ";") {
		var keep []string
		for _, l := range strings.Split(s, "\n") {
			if x := strings.TrimSpace(l); x != "" && !strings.HasPrefix(x, "--") {
				keep = append(keep, l)
			}
		}
		if len(keep) > 0 {
			out = append(out, strings.Join(keep, "\n"))
		}
	}
	return out
}

type fixture struct {
	db *sql.DB
	h  *Handler
	r  chi.Router
}

func newFixture(t *testing.T, enabled bool, roots ...string) *fixture {
	t.Helper()
	db := testDB(t, roots...)
	h := New(db, enabled, nil)
	r := chi.NewRouter()
	h.Mount(r)
	return &fixture{db: db, h: h, r: r}
}

func (f *fixture) do(t *testing.T, method, path string, body any, ip string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if ip == "" {
		ip = "203.0.113.9"
	}
	req.RemoteAddr = ip + ":51234"
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func events(evs ...map[string]any) map[string]any {
	list := make([]any, len(evs))
	for i, e := range evs {
		list[i] = e
	}
	return map[string]any{
		"device_pseudonym": pseud, "app_version": "1.4.2", "os": "macOS", "os_version": "15.1",
		"locale": "ar", "events": list,
	}
}

func ev(kind string, payload map[string]any) map[string]any {
	return map[string]any{"event_kind": kind, "payload": payload}
}

func TestPostEventStored(t *testing.T) {
	f := newFixture(t, true)
	rec := f.do(t, http.MethodPost, "/api/telemetry", events(ev("app.open", map[string]any{
		"cold_start": true, "library_size_bucket": "100-999",
	})), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var kind, pseudo, app, osName, locale, payload string
	if err := f.db.QueryRow(`SELECT event_kind, device_pseudonym, app_version, os, locale, payload FROM telemetry_events`).
		Scan(&kind, &pseudo, &app, &osName, &locale, &payload); err != nil {
		t.Fatal(err)
	}
	if kind != "app.open" || pseudo != pseud || app != "1.4.2" || osName != "macOS" || locale != "ar" {
		t.Fatalf("row = %s %s %s %s %s", kind, pseudo, app, osName, locale)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	if p["cold_start"] != true || p["library_size_bucket"] != "100-999" {
		t.Fatalf("payload = %v", p)
	}
}

// The sender's IP is read for the rate limit only: no column of any row holds it.
func TestNoRemoteAddrInTelemetry(t *testing.T) {
	f := newFixture(t, true)
	const ip = "198.51.100.77"
	for i := 0; i < 5; i++ {
		if rec := f.do(t, http.MethodPost, "/api/telemetry", events(ev("search.run", map[string]any{"result_count": 3.0})), ip); rec.Code != http.StatusNoContent {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	}
	if rec := f.do(t, http.MethodPost, "/api/telemetry/web-vitals", map[string]any{
		"device_pseudonym": pseud, "metrics": []any{map[string]any{"metric": "LCP", "value": 1200.0, "route": "/library"}},
	}, ip); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	for _, q := range []string{
		`SELECT count(*) FROM telemetry_events WHERE (received_at || coalesce(occurred_at,'') || device_pseudonym || event_kind ||
			coalesce(app_version,'') || coalesce(os,'') || coalesce(os_version,'') || coalesce(locale,'') || payload) LIKE '%198.51.100.77%'`,
		`SELECT count(*) FROM telemetry_web_vitals WHERE (received_at || device_pseudonym || metric || value || coalesce(route,'')) LIKE '%198.51.100.77%'`,
	} {
		var n int
		if err := f.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%d rows hold the sender's IP", n)
		}
	}
}

func TestRedactsLibraryRootServerSide(t *testing.T) {
	f := newFixture(t, true, "/Users/me/Lectures")
	rec := f.do(t, http.MethodPost, "/api/telemetry", events(ev("error.uncaught", map[string]any{
		"error_message": "cannot open /Users/me/Lectures/foo.mp4",
		"stack_first_5": "at play (/Users/me/Lectures/foo.mp4:1)\nat x (/home/sam/app.js:2)",
	})), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var payload string
	if err := f.db.QueryRow(`SELECT payload FROM telemetry_events`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var p map[string]string
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	if p["error_message"] != "cannot open <library>/foo.mp4" {
		t.Fatalf("error_message = %q", p["error_message"])
	}
	if strings.Contains(p["stack_first_5"], "/Users/me") || strings.Contains(p["stack_first_5"], "sam") {
		t.Fatalf("stack keeps a path: %q", p["stack_first_5"])
	}
}

func TestRedactor(t *testing.T) {
	r := NewRedactor([]string{"/data[archive]/", "/mnt/media", "/mnt/media/arabic"})
	cases := map[string]string{
		"open /data[archive]/x.mkv":        "open <library>/x.mkv",
		"open /dataa/x.mkv":                "open /dataa/x.mkv", // the brackets are literal
		"/mnt/media/arabic/a.mp4":          "<library>/a.mp4",   // the longer root wins
		"/mnt/media/b.mp4":                 "<library>/b.mp4",
		"/srv/other/c.mp4":                 "/srv/other/c.mp4", // not a root: unchanged
		`C:\Users\Amira\Videos\d.mp4`:      `<home>\Videos\d.mp4`,
		"/home/sam/e.mp4 and /Users/lee/f": "<home>/e.mp4 and <home>/f",
		"TypeError: x is undefined":        "TypeError: x is undefined",
	}
	for in, want := range cases {
		if got := r.Apply(in); got != want {
			t.Errorf("Apply(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnknownKindRejectedAtomically(t *testing.T) {
	f := newFixture(t, true)
	rec := f.do(t, http.MethodPost, "/api/telemetry", events(ev("app.open", nil), ev("private.evil", nil)), "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown-event-kind") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if n := f.count(t, "telemetry_events"); n != 0 {
		t.Fatalf("%d rows stored from a refused batch", n)
	}
}

func TestUnknownFieldsStripped(t *testing.T) {
	f := newFixture(t, true)
	rec := f.do(t, http.MethodPost, "/api/telemetry", events(
		ev("player.start", map[string]any{"is_hdr": true, "secret": "x", "codec": map[string]any{"nested": "y"}}),
		ev("feature.used", map[string]any{"feature": "search", "count": 4.0, "query": "fiqh"}),
	), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	rows, err := f.db.Query(`SELECT payload FROM telemetry_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	want := []string{`{"is_hdr":true}`, `{"count":4,"feature":"search"}`}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("payloads = %v, want %v", got, want)
	}
}

func TestBatchCaps(t *testing.T) {
	f := newFixture(t, true)
	batch := func(n int) map[string]any {
		evs := make([]map[string]any, n)
		for i := range evs {
			evs[i] = ev("app.open", nil)
		}
		return events(evs...)
	}
	if rec := f.do(t, http.MethodPost, "/api/telemetry", batch(MaxEvents), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("100 events: status %d: %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, http.MethodPost, "/api/telemetry", batch(MaxEvents+1), ""); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("101 events: status %d: %s", rec.Code, rec.Body)
	}
	if n := f.count(t, "telemetry_events"); n != MaxEvents {
		t.Fatalf("%d rows, want the first batch's %d only", n, MaxEvents)
	}
	metrics := make([]any, MaxMetrics+1)
	for i := range metrics {
		metrics[i] = map[string]any{"metric": "CLS", "value": 0.1}
	}
	if rec := f.do(t, http.MethodPost, "/api/telemetry/web-vitals", map[string]any{"device_pseudonym": pseud, "metrics": metrics}, ""); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("51 metrics: status %d: %s", rec.Code, rec.Body)
	}
	if n := f.count(t, "telemetry_web_vitals"); n != 0 {
		t.Fatalf("%d metrics stored from a refused batch", n)
	}
}

func TestServerDisabledStoresNothing(t *testing.T) {
	f := newFixture(t, false)
	if rec := f.do(t, http.MethodPost, "/api/telemetry", events(ev("app.open", nil)), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, http.MethodPost, "/api/telemetry/web-vitals", map[string]any{
		"device_pseudonym": pseud, "metrics": []any{map[string]any{"metric": "LCP", "value": 1.0}},
	}, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if f.count(t, "telemetry_events")+f.count(t, "telemetry_web_vitals") != 0 {
		t.Fatal("a disabled sink stored rows")
	}
}

func TestBadPseudonymRefused(t *testing.T) {
	f := newFixture(t, true)
	b := events(ev("app.open", nil))
	b["device_pseudonym"] = "user-42"
	if rec := f.do(t, http.MethodPost, "/api/telemetry", b, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, http.MethodDelete, "/api/telemetry/devices/short", nil, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete: status %d", rec.Code)
	}
}

func TestWebVitals(t *testing.T) {
	f := newFixture(t, true)
	rec := f.do(t, http.MethodPost, "/api/telemetry/web-vitals", map[string]any{
		"device_pseudonym": pseud, "metrics": []any{
			map[string]any{"metric": "LCP", "value": 1830.5, "route": "/videos/3f2a9c1e-77b4-4a8e-9d55-0c1d2e3f4a5b/watch?t=30"},
			map[string]any{"metric": "INP", "value": 80.0, "route": "/watch/:id"},
			map[string]any{"metric": "TTFB", "value": 120.0, "route": "/live/12"},
		},
	}, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	rows, err := f.db.Query(`SELECT route FROM telemetry_web_vitals ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if want := "/videos/:id/watch /watch/:id /live/:id"; strings.Join(got, " ") != want {
		t.Fatalf("routes = %v, want %s", got, want)
	}
	rec = f.do(t, http.MethodPost, "/api/telemetry/web-vitals", map[string]any{
		"device_pseudonym": pseud, "metrics": []any{map[string]any{"metric": "FCP", "value": 1.0}},
	}, "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown-metric") {
		t.Fatalf("FCP: status %d: %s", rec.Code, rec.Body)
	}
}

func TestDeleteDevicePurges(t *testing.T) {
	f := newFixture(t, true)
	other := events(ev("app.open", nil))
	other["device_pseudonym"] = "ZZZZZZZZZZZZZZZZ"
	for _, b := range []map[string]any{events(ev("app.open", nil), ev("search.run", nil)), other} {
		if rec := f.do(t, http.MethodPost, "/api/telemetry", b, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	}
	f.do(t, http.MethodPost, "/api/telemetry/web-vitals", map[string]any{
		"device_pseudonym": pseud, "metrics": []any{map[string]any{"metric": "LCP", "value": 1.0}},
	}, "")
	if rec := f.do(t, http.MethodDelete, "/api/telemetry/devices/"+pseud, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d: %s", rec.Code, rec.Body)
	}
	var mine, vitals, theirs int
	_ = f.db.QueryRow(`SELECT count(*) FROM telemetry_events WHERE device_pseudonym = $1`, pseud).Scan(&mine)
	_ = f.db.QueryRow(`SELECT count(*) FROM telemetry_web_vitals WHERE device_pseudonym = $1`, pseud).Scan(&vitals)
	_ = f.db.QueryRow(`SELECT count(*) FROM telemetry_events WHERE device_pseudonym = 'ZZZZZZZZZZZZZZZZ'`).Scan(&theirs)
	if mine != 0 || vitals != 0 || theirs != 1 {
		t.Fatalf("after forget: mine %d, vitals %d, another device's %d", mine, vitals, theirs)
	}
}

// Forgetting works with the sink switched off: a device can always take its data back.
func TestDeleteWorksWhileDisabled(t *testing.T) {
	f := newFixture(t, false)
	if rec := f.do(t, http.MethodDelete, "/api/telemetry/devices/"+pseud, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestDeleteRateLimited(t *testing.T) {
	f := newFixture(t, true)
	limited := false
	for i := 0; i < 100; i++ {
		if rec := f.do(t, http.MethodDelete, "/api/telemetry/devices/"+pseud, nil, ""); rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("100 rapid deletes were never limited")
	}
}

// The budget counts events, not requests: 1,000 a minute per IP, then 429; another
// IP keeps its own.
func TestRateLimitPerIPCountsEvents(t *testing.T) {
	f := newFixture(t, true)
	batch := make([]map[string]any, MaxEvents)
	for i := range batch {
		batch[i] = ev("app.open", nil)
	}
	for i := 0; i < EventsPerMinute/MaxEvents; i++ {
		if rec := f.do(t, http.MethodPost, "/api/telemetry", events(batch...), "192.0.2.1"); rec.Code != http.StatusNoContent {
			t.Fatalf("batch %d: status %d: %s", i, rec.Code, rec.Body)
		}
	}
	// a whole batch over the budget: the bucket refills ~17 events a
	// second, so a slow runner may have a few back, never 100
	rec := f.do(t, http.MethodPost, "/api/telemetry", events(batch...), "192.0.2.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("events 1,001-1,100: status %d: %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, http.MethodPost, "/api/telemetry", events(ev("app.open", nil)), "192.0.2.2"); rec.Code != http.StatusNoContent {
		t.Fatalf("another IP: status %d", rec.Code)
	}
}

// A device clock 24 h ahead: the event's time is the server's.
func TestFutureTimestampTruncated(t *testing.T) {
	f := newFixture(t, true)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	f.h.NowFunc = func() time.Time { return now }
	past := now.Add(-time.Hour)
	b := events(
		map[string]any{"event_kind": "app.open", "ts": now.Add(24 * time.Hour).Format(time.RFC3339)},
		map[string]any{"event_kind": "app.open", "ts": past.Format(time.RFC3339)},
	)
	if rec := f.do(t, http.MethodPost, "/api/telemetry", b, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	rows, err := f.db.Query(`SELECT occurred_at FROM telemetry_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []time.Time
	for rows.Next() {
		// SQLite keeps the time as database/sql's text form of it
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		ts, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", raw)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ts)
	}
	if len(got) != 2 || !got[0].Equal(now) || !got[1].Equal(past) {
		t.Fatalf("occurred_at = %v, want [%v %v]", got, now, past)
	}
}

func TestRouteTemplate(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"/library":               "/library",
		"/series/42":             "/series/:id",
		"/admin/live-tv/changes": "/admin/live-tv/changes",
		"/videos/:videoId/watch": "/videos/:videoId/watch",
		"/search?q=fiqh#x":       "/search",
		"/videos/AbC_9/watch":    "/videos/:id/watch",
	}
	for in, want := range cases {
		if got := RouteTemplate(in); got != want {
			t.Errorf("RouteTemplate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRetentionSweep(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	ins := func(table string, age time.Duration) {
		t.Helper()
		var err error
		if table == "telemetry_events" {
			_, err = db.Exec(`INSERT INTO telemetry_events (received_at, device_pseudonym, event_kind, payload) VALUES ($1, $2, 'app.open', '{}')`, now.Add(-age), pseud)
		} else {
			_, err = db.Exec(`INSERT INTO telemetry_web_vitals (received_at, device_pseudonym, metric, value) VALUES ($1, $2, 'LCP', 1)`, now.Add(-age), pseud)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	ins("telemetry_events", 91*day)
	ins("telemetry_events", 89*day)
	ins("telemetry_web_vitals", 31*day)
	ins("telemetry_web_vitals", 29*day)
	s := &Sweeper{DB: db, now: func() time.Time { return now }}
	events, vitals, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if events != 1 || vitals != 1 {
		t.Fatalf("swept %d events, %d vitals; want 1 and 1", events, vitals)
	}
}

// The sink's routes pass the auth gate without a sign-in (the pseudonym is the
// device's only identity); a neighbouring path does not.
func TestRoutesArePublic(t *testing.T) {
	f := newFixture(t, true)
	gated := authmw.RequireAuthExcept(authmw.DefaultPublicAllowlist())(f.r)
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/telemetry", http.StatusNoContent},
		{http.MethodPost, "/api/telemetry/web-vitals", http.StatusNoContent},
		{http.MethodDelete, "/api/telemetry/devices/" + pseud, http.StatusNoContent},
		{http.MethodGet, "/api/telemetry/devices/" + pseud + "/x", http.StatusUnauthorized},
	} {
		body := `{"device_pseudonym":"` + pseud + `"}`
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		gated.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s: status %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body)
		}
	}
}
