package telemetry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const installID = "ABCDEFGHJKMNPQRSTUVW"

// memStore is the outbox in memory.
type memStore struct {
	mu   sync.Mutex
	next int64
	rows []memRow
}

type memRow struct {
	Row
	nextAt time.Time
	reason string
}

func (s *memStore) Add(_ context.Context, kind, id string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.rows = append(s.rows, memRow{Row: Row{ID: s.next, Kind: kind, InstallID: id, Body: raw, CreatedAt: time.Now().UTC()}})
	return nil
}

func (s *memStore) Due(_ context.Context, now time.Time, limit int) ([]Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Row
	for _, r := range s.rows {
		if !r.nextAt.After(now) && len(out) < limit {
			out = append(out, r.Row)
		}
	}
	return out, nil
}

func (s *memStore) filter(keep func(memRow) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []memRow
	for _, r := range s.rows {
		if keep(r) {
			out = append(out, r)
		}
	}
	s.rows = out
}

func (s *memStore) Sent(_ context.Context, ids []int64) error {
	gone := map[int64]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	s.filter(func(r memRow) bool { return !gone[r.ID] })
	return nil
}

func (s *memStore) Retry(_ context.Context, id int64, next time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Attempts++
			s.rows[i].nextAt, s.rows[i].reason = next, reason
		}
	}
	return nil
}

func (s *memStore) Prune(_ context.Context, before time.Time) (int64, error) {
	n := len(s.rows)
	s.filter(func(r memRow) bool { return !r.CreatedAt.Before(before) })
	return int64(n - len(s.rows)), nil
}

func (s *memStore) DropKind(_ context.Context, kind string) error {
	s.filter(func(r memRow) bool { return r.Kind != kind })
	return nil
}

func (s *memStore) DropInstall(_ context.Context, id string) error {
	s.filter(func(r memRow) bool { return r.InstallID != id || r.Kind == KindForget })
	return nil
}

func (s *memStore) Pending(_ context.Context, kind string) (int, *time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	var oldest *time.Time
	for _, r := range s.rows {
		if r.Kind == kind {
			n++
			if oldest == nil {
				t := r.CreatedAt
				oldest = &t
			}
		}
	}
	return n, oldest, nil
}

func (s *memStore) kinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.rows {
		out = append(out, r.Kind)
	}
	return out
}

// memState is the switch and the sender's state in memory.
type memState struct {
	mu      sync.Mutex
	sharing bool
	id      string
	day     string
}

func (s *memState) Sharing(context.Context) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.sharing }
func (s *memState) InstallID(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id == "" {
		s.id = NewInstallID()
	}
	return s.id, nil
}
func (s *memState) NewInstallID(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = NewInstallID()
	return s.id, nil
}
func (s *memState) LastReportDay(context.Context) string { return s.day }
func (s *memState) SetLastReportDay(_ context.Context, d string) error {
	s.day = d
	return nil
}

// reportDB is a SQLite database with the tables the report counts.
func reportDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE videos (id TEXT)`, `CREATE TABLE libraries (id TEXT)`, `CREATE TABLE users (id TEXT)`,
		`CREATE TABLE library_roots (path TEXT, path_canonical TEXT)`,
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE api_usage_daily (day TEXT, client_app TEXT, platform TEXT, requests INTEGER, errors INTEGER)`,
		`CREATE TABLE api_usage_people_daily (day TEXT, client_app TEXT, user_id TEXT)`,
		`INSERT INTO library_roots VALUES ('/srv/Lectures', '/srv/Lectures')`,
		`INSERT INTO videos VALUES ('a'), ('b'), ('c')`, `INSERT INTO libraries VALUES ('l')`,
		`INSERT INTO users VALUES ('u1'), ('u2')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v: %s", err, s)
		}
	}
	return db
}

type pipe struct {
	h     *Handler
	r     chi.Router
	store *memStore
	state *memState
}

func newPipe(t *testing.T, enabled bool) *pipe {
	t.Helper()
	db := reportDB(t)
	p := &pipe{store: &memStore{}, state: &memState{}}
	p.h = &Handler{DB: db, Enabled: enabled, Store: p.store, State: p.state,
		Reporter: &Reporter{DB: db, Version: "1.2.3", Errors: &ErrorCounts{}}}
	p.r = chi.NewRouter()
	p.h.Mount(p.r)
	return p
}

var (
	viewer = &principal.Principal{UserID: "00000000-0000-0000-0000-000000000002"}
	admin  = &principal.Principal{UserID: "00000000-0000-0000-0000-000000000001", IsAdmin: true}
)

func (p *pipe) do(t *testing.T, method, path string, body any, who *principal.Principal) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = "192.0.2.1:1234"
	if who != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), who))
	}
	rec := httptest.NewRecorder()
	p.r.ServeHTTP(rec, req)
	return rec
}

func batch(consent any, events ...map[string]any) map[string]any {
	ev := make([]any, len(events))
	for i, e := range events {
		ev[i] = e
	}
	b := map[string]any{"install_id": installID, "app": "web", "platform": "macos", "form_factor": "desktop",
		"app_version": "0.3.27", "os_version": "", "locale": "ar", "events": ev}
	if consent != nil {
		b["consent"] = consent
	}
	return b
}

func ev(kind string, payload map[string]any) map[string]any {
	return map[string]any{"kind": kind, "payload": payload, "ts": "2026-10-26T10:00:00Z"}
}

// An app's batch is forwarded only with its user's consent in it, whatever the
// admin's server switch says; scrubbed, its unknown fields dropped, the user
// never in it.
func TestPostAppConsent(t *testing.T) {
	p := newPipe(t, true)
	open := ev("app.open", map[string]any{"cold_start": true})

	for _, consent := range []any{nil, false, "yes"} {
		rec := p.do(t, http.MethodPost, "/api/telemetry/app", batch(consent, open), viewer)
		if consent == "yes" {
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("consent %v: %d", consent, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusNoContent {
			t.Fatalf("consent %v: %d %s", consent, rec.Code, rec.Body)
		}
	}
	if n := len(p.store.kinds()); n != 0 {
		t.Fatalf("%d batches kept without consent", n)
	}

	// the server switch is off: the app's own consent is what counts
	rec := p.do(t, http.MethodPost, "/api/telemetry/app", batch(true, open,
		ev("error.uncaught", map[string]any{"error_message": "Error: cannot open /srv/Lectures/Name/الدرس.mp4: x sara secret",
			"stack_first_5": "at load (http://192.168.1.2:8088/assets/a.js:1:2)"}),
		ev("feature.used", map[string]any{"feature": "library", "count": 3.0}),
	), viewer)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if k := p.store.kinds(); len(k) != 1 || k[0] != KindApp {
		t.Fatalf("outbox = %v", k)
	}
	body := string(p.store.rows[0].Body)
	for _, bad := range []string{"sara", "secret", "Lectures", "الدرس", "192.168", viewer.UserID, `"consent"`} {
		if strings.Contains(body, bad) {
			t.Errorf("the forwarded batch carries %q: %s", bad, body)
		}
	}
	var out AppBatch
	if err := json.Unmarshal(p.store.rows[0].Body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != KindApp || out.InstallID != installID || out.App != "web" || out.Locale != "ar" || len(out.Events) != 3 {
		t.Fatalf("batch = %+v", out)
	}
	if got := out.Events[1].Payload["error_message"]; got != "Error" {
		t.Fatalf("error_message = %q, want the error's kind only", got)
	}
	if got := out.Events[1].Payload["stack_first_5"]; got != "load (a.js:1:2)" {
		t.Fatalf("stack_first_5 = %q", got)
	}
}

// server.toml's [telemetry] off: nothing is kept, consent or not.
func TestPostAppTelemetryOff(t *testing.T) {
	p := newPipe(t, false)
	rec := p.do(t, http.MethodPost, "/api/telemetry/app", batch(true, ev("app.open", nil)), viewer)
	if rec.Code != http.StatusNoContent || len(p.store.kinds()) != 0 {
		t.Fatalf("status %d, outbox %v", rec.Code, p.store.kinds())
	}
}

func TestPostAppValidation(t *testing.T) {
	p := newPipe(t, true)
	open := ev("app.open", nil)
	bad := func(name string, b map[string]any, want int) {
		t.Helper()
		if rec := p.do(t, http.MethodPost, "/api/telemetry/app", b, viewer); rec.Code != want {
			t.Errorf("%s: %d, want %d (%s)", name, rec.Code, want, rec.Body)
		}
	}
	b := batch(true, open)
	b["install_id"] = "short"
	bad("install id", b, http.StatusBadRequest)
	b = batch(true, open)
	b["app"] = "Web App!"
	bad("app", b, http.StatusBadRequest)
	bad("kind", batch(true, ev("search.run", nil)), http.StatusBadRequest)
	// the hard rule: a field the allow-list does not name is refused, not dropped
	for _, payload := range []map[string]any{
		{"feature": "library", "count": 1.0, "title": "Lecture 1"},
		{"feature": "library", "count": 1.0, "user": "sara"},
		{"feature": "library", "count": 1.0, "query": "fiqh"},
		{"feature": "library", "count": map[string]any{"n": 1.0}},
		{"feature": []any{"library"}, "count": 1.0},
	} {
		bad("field", batch(true, ev("feature.used", payload)), http.StatusBadRequest)
	}
	// and so is any field of the batch itself the contract does not name
	for _, extra := range []string{"user", "user_id", "email", "home_name", "device_name", "ip", "account"} {
		b := batch(true, open)
		b[extra] = "x"
		bad("batch field "+extra, b, http.StatusBadRequest)
	}
	b = batch(true)
	b["events"] = []any{map[string]any{"kind": "app.open", "payload": map[string]any{}, "ts": "2026-10-26T10:00:00Z", "title": "x"}}
	bad("event field", b, http.StatusBadRequest)
	many := make([]map[string]any, MaxEvents+1)
	for i := range many {
		many[i] = open
	}
	bad("too many", batch(true, many...), http.StatusRequestEntityTooLarge)
	b = batch(true)
	b["vitals"] = []any{map[string]any{"metric": "XYZ", "value": 1.0}}
	bad("metric", b, http.StatusBadRequest)
	b["vitals"] = []any{map[string]any{"metric": "LCP", "value": -1.0}}
	bad("value", b, http.StatusBadRequest)
	if n := len(p.store.kinds()); n != 0 {
		t.Fatalf("%d batches kept from refused requests", n)
	}
	// a vital's route is kept as its template
	b["vitals"] = []any{map[string]any{"metric": "LCP", "value": 1200.0, "route": "/videos/3f2a9c/watch?t=3"}}
	bad("vitals", b, http.StatusNoContent)
	if body := string(p.store.rows[0].Body); !strings.Contains(body, `"/videos/:id/watch"`) {
		t.Fatalf("route not a template: %s", body)
	}
}

// The API enforces roles: a signed-out caller gets 401 on every route; the
// server's status and "delete what this server sent" are an admin's.
func TestRoles(t *testing.T) {
	p := newPipe(t, true)
	for _, c := range []struct {
		method, path string
		who          *principal.Principal
		want         int
	}{
		{http.MethodPost, "/api/telemetry/app", nil, http.StatusUnauthorized},
		{http.MethodDelete, "/api/telemetry/app/" + installID, nil, http.StatusUnauthorized},
		{http.MethodGet, "/api/telemetry/server", nil, http.StatusUnauthorized},
		{http.MethodGet, "/api/telemetry/server", viewer, http.StatusForbidden},
		{http.MethodPost, "/api/telemetry/server/forget", viewer, http.StatusForbidden},
		{http.MethodGet, "/api/telemetry/server", admin, http.StatusOK},
		{http.MethodPost, "/api/telemetry/server/forget", admin, http.StatusNoContent},
		{http.MethodDelete, "/api/telemetry/app/" + installID, viewer, http.StatusNoContent},
	} {
		var body any
		if c.method == http.MethodPost && strings.HasSuffix(c.path, "/app") {
			body = batch(true, ev("app.open", nil))
		}
		if rec := p.do(t, c.method, c.path, body, c.who); rec.Code != c.want {
			t.Errorf("%s %s as %v: %d, want %d", c.method, c.path, c.who, rec.Code, c.want)
		}
	}
}

// The admin sees exactly what the day's report holds, before agreeing; a
// preview leaves the errors counted for the real report.
func TestServerStatusPreview(t *testing.T) {
	p := newPipe(t, true)
	p.h.Reporter.Errors.add("scan_failed")
	rec := p.do(t, http.MethodGet, "/api/telemetry/server", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var v serverView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Sharing || !v.Enabled || v.Preview.LibrarySize != "2-5" || v.Preview.Users != "2-5" ||
		v.Preview.Libraries != "1" || v.Preview.Version != "1.2.3" || len(v.Preview.Errors) != 1 ||
		v.Preview.Errors[0].Message != "scan_failed" {
		t.Fatalf("status = %+v", v)
	}
	if len(p.h.Reporter.Errors.Take()) != 1 {
		t.Fatal("the preview took the errors")
	}
}

// "Delete what this app sent" drops what waits and asks the cloud to delete;
// "delete what this server sent" also gives the home a new id.
func TestForget(t *testing.T) {
	p := newPipe(t, true)
	if rec := p.do(t, http.MethodPost, "/api/telemetry/app", batch(true, ev("app.open", nil)), viewer); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	if rec := p.do(t, http.MethodDelete, "/api/telemetry/app/"+installID, nil, viewer); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	if k := p.store.kinds(); len(k) != 1 || k[0] != KindForget {
		t.Fatalf("outbox = %v", k)
	}

	old, _ := p.state.InstallID(context.Background())
	_ = p.store.Add(context.Background(), KindServer, old, map[string]string{"type": "server"})
	if rec := p.do(t, http.MethodPost, "/api/telemetry/server/forget", nil, admin); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	if now, _ := p.state.InstallID(context.Background()); now == old {
		t.Fatal("the home kept its id")
	}
	if k := p.store.kinds(); len(k) != 2 || k[0] != KindForget || k[1] != KindForget {
		t.Fatalf("outbox = %v", k)
	}
	if !strings.Contains(string(p.store.rows[1].Body), old) {
		t.Fatalf("the forget names %s", p.store.rows[1].Body)
	}
}
