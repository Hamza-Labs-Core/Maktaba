package watch

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// stubDriver is a database/sql driver with no database behind it: in "fail"
// mode every statement errors, in "empty" mode every query returns no rows and
// every exec affects none. Enough to drive the handlers' error, not-found and
// empty-result paths without Postgres.
type stubDriver struct{}

func (stubDriver) Open(name string) (driver.Conn, error) { return stubConn{fail: name == "fail"}, nil }

type stubConn struct{ fail bool }

func (c stubConn) Prepare(string) (driver.Stmt, error) { return stubStmt(c), nil }
func (stubConn) Close() error                          { return nil }
func (stubConn) Begin() (driver.Tx, error)             { return stubTx{}, nil }

type stubTx struct{}

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

type stubStmt struct{ fail bool }

var errStub = errors.New("stub database")

func (stubStmt) Close() error  { return nil }
func (stubStmt) NumInput() int { return -1 }
func (s stubStmt) Exec([]driver.Value) (driver.Result, error) {
	if s.fail {
		return nil, errStub
	}
	return driver.RowsAffected(0), nil
}

func (s stubStmt) Query([]driver.Value) (driver.Rows, error) {
	if s.fail {
		return nil, errStub
	}
	return stubRows{}, nil
}

type stubRows struct{}

func (stubRows) Columns() []string         { return nil }
func (stubRows) Close() error              { return nil }
func (stubRows) Next([]driver.Value) error { return io.EOF }

func init() { sql.Register("watch-stub", stubDriver{}) }

func stubDB(t *testing.T, mode string) *sql.DB {
	t.Helper()
	db, err := sql.Open("watch-stub", mode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

const vid = "11111111-1111-1111-1111-111111111111"

func do(t *testing.T, db *sql.DB, p *principal.Principal, method, target, body string) int {
	t.Helper()
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(context.Background(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

type call struct{ method, target, body string }

var routes = []call{
	{http.MethodPost, "/api/watch/start", `{"video_id":"` + vid + `"}`},
	{http.MethodPost, "/api/watch/heartbeat", `{"session_id":"` + vid + `","position_sec":5}`},
	{http.MethodPost, "/api/watch/stop", `{"session_id":"` + vid + `"}`},
	{http.MethodGet, "/api/me/history", ""},
	{http.MethodGet, "/api/me/history/" + vid, ""},
	{http.MethodDelete, "/api/me/history/" + vid, ""},
	{http.MethodGet, "/api/me/activity", ""},
	{http.MethodGet, "/api/me/activity/settings", ""},
	{http.MethodPut, "/api/me/activity/settings", `{"track_enabled":false}`},
}

func user() *principal.Principal { return &principal.Principal{UserID: "u1"} }

func TestRoutes_RequireAuth(t *testing.T) {
	for _, c := range routes {
		if got := do(t, stubDB(t, "empty"), nil, c.method, c.target, c.body); got != http.StatusForbidden {
			t.Errorf("%s %s without principal: status %d, want 403", c.method, c.target, got)
		}
	}
}

func TestRoutes_DatabaseErrorIs500(t *testing.T) {
	for _, c := range routes {
		if got := do(t, stubDB(t, "fail"), user(), c.method, c.target, c.body); got != http.StatusInternalServerError {
			t.Errorf("%s %s with failing db: status %d, want 500", c.method, c.target, got)
		}
	}
}

func TestBadInputIs400(t *testing.T) {
	for _, c := range []call{
		{http.MethodPost, "/api/watch/start", `{"video_id":"nope"}`},
		{http.MethodPost, "/api/watch/start", `not json`},
		{http.MethodPost, "/api/watch/heartbeat", `{"session_id":"nope"}`},
		{http.MethodPost, "/api/watch/heartbeat", `[`},
		{http.MethodPost, "/api/watch/stop", `{"session_id":"nope"}`},
		{http.MethodPost, "/api/watch/stop", `[`},
		{http.MethodGet, "/api/me/history?limit=x", ""},
		{http.MethodGet, "/api/me/history?offset=x", ""},
		{http.MethodGet, "/api/me/history?from=yesterday", ""},
		{http.MethodGet, "/api/me/history?to=tomorrow", ""},
		{http.MethodGet, "/api/me/history/nope", ""},
		{http.MethodDelete, "/api/me/history/nope", ""},
		{http.MethodPut, "/api/me/activity/settings", `[`},
	} {
		if got := do(t, stubDB(t, "empty"), user(), c.method, c.target, c.body); got != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, want 400", c.method, c.target, got)
		}
	}
}

func TestUnknownSessionIs404(t *testing.T) {
	for _, c := range routes[1:3] {
		if got := do(t, stubDB(t, "empty"), user(), c.method, c.target, c.body); got != http.StatusNotFound {
			t.Errorf("%s %s for unknown session: status %d, want 404", c.method, c.target, got)
		}
	}
}

func TestEmptyHistoryAndDelete(t *testing.T) {
	for _, c := range []call{
		{http.MethodGet, "/api/me/history?limit=500&offset=-3&from=2026-01-01&to=2026-02-01T00:00:00Z", ""},
		{http.MethodGet, "/api/me/activity?types=watched", ""},
	} {
		if got := do(t, stubDB(t, "empty"), user(), c.method, c.target, c.body); got != http.StatusOK {
			t.Errorf("%s %s on empty db: status %d, want 200", c.method, c.target, got)
		}
	}
	if got := do(t, stubDB(t, "empty"), user(), http.MethodDelete, "/api/me/history/"+vid, ""); got != http.StatusNoContent {
		t.Errorf("delete history: status %d, want 204", got)
	}
}

func TestHashIP(t *testing.T) {
	h := &Handler{IPSalt: "s"}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	a := h.hashIP(r)
	r.RemoteAddr = "10.0.0.1"
	if b := h.hashIP(r); a != b || len(a) != 16 {
		t.Fatalf("hash %q vs %q: same IP with or without port must hash alike", a, b)
	}
	r.RemoteAddr = ""
	if h.hashIP(r) != "" {
		t.Fatal("no address must hash to empty")
	}
	if truncate("abcdef", 3) != "abc" || truncate("ab", 3) != "ab" {
		t.Fatal("truncate")
	}
}
