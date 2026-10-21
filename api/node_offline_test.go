package main

// A full node that starts with the home's database down does not refuse to start: the
// role check waits for the database, and playback reads this machine's copy of it
// (specs/epics/37-node-replica).

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodedb"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/vodfallback"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

// copyOf is a stand-in for this machine's copy of the home's database: one video.
func copyOf(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, path TEXT, duration_sec REAL)`,
		`INSERT INTO videos VALUES ('22222222-0000-4000-8000-000000000001', 'lib1', 'smb://nas/Originals/a.mkv', 1800)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestNodeStartsWithTheHomeDatabaseDown(t *testing.T) {
	t.Setenv("MAKTABA_NODE_STATE_DIR", t.TempDir())
	t.Cleanup(func() { sharepath.Set(sharepath.Map{}) })

	// nothing listens there: the home's database is gone
	dbs, err := openNodeDBs("postgres://node@127.0.0.1:1/maktaba?sslmode=disable&connect_timeout=2", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dbs.close()
	dbs.copyDB = copyOf(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := nodemode.Config{Enabled: true, NodeID: "6f1c9a2e-0000-4000-8000-000000000001", HomeAPIURL: "http://127.0.0.1:1"}
	rt, err := startNode(ctx, cfg, dbs, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatalf("a node with the home's database down refused to start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !rt.offline().DBDown() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !rt.offline().DBDown() {
		t.Fatal("the database is not known down")
	}
	lib, dur, ok := rt.offline().Video("22222222-0000-4000-8000-000000000001")
	if !ok || lib != "lib1" || dur != 1800 {
		t.Fatalf("the video from the copy: %q %v %v", lib, dur, ok)
	}
	if p, ok := rt.offline().Path("22222222-0000-4000-8000-000000000001"); !ok || p != "smb://nas/Originals/a.mkv" {
		t.Fatalf("path: %q %v", p, ok)
	}
	if _, _, ok := rt.offline().Video("22222222-0000-4000-8000-000000000009"); ok {
		t.Fatal("a video the copy does not have was found")
	}
	select {
	case err := <-rt.fatal:
		t.Fatalf("stopped: %v", err)
	default:
	}
}

// deadOrNot is a database that answers or refuses connections.
type deadOrNot struct{ dead bool }

func (d *deadOrNot) Connect(context.Context) (driver.Conn, error) {
	if d.dead {
		return nil, errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
	}
	return idleConn{}, nil
}
func (d *deadOrNot) Driver() driver.Driver { return nil }

type idleConn struct{}

func (idleConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (idleConn) Close() error                        { return nil }
func (idleConn) Begin() (driver.Tx, error)           { return nil, errors.New("not used") }

// With the home's database known down and no copy to stand in, a node's own reads
// answer at once (the app keeps its notice) and playback still reaches its handler; with
// a copy that answers, the reads are served.
func TestNodeReadsWaitForTheDatabasePlaybackDoesNot(t *testing.T) {
	r := chi.NewRouter()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	r.Get("/api/videos", ok)           // any
	r.Post("/api/stream/sessions", ok) // local
	r.Get("/api/system/version", ok)   // local, allowlisted
	home, cp := &deadOrNot{}, &deadOrNot{dead: true}
	pair := &nodedb.Pair{Home: &nodedb.Source{Name: "home", Conn: home}, Copy: &nodedb.Source{Name: "copy", Conn: cp}}
	rt := &nodeRuntime{resolver: &nodeauth.Resolver{Link: &fakeHome{}}, vod: &vodfallback.Fallback{},
		dbs:   nodeDBs{pair: pair},
		proxy: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })}
	h := rt.handler(&authState{}, r, r)
	do := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: nodeauth.CookieSession, Value: "6f1d2c3b-0000-4000-8000-000000000003"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do(http.MethodGet, "/api/videos"); rec.Code != http.StatusOK {
		t.Fatalf("a read with the database up: %d", rec.Code)
	}
	home.dead = true
	pair.CheckAll(context.Background())
	rec := do(http.MethodGet, "/api/videos")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"home_unreachable"`) ||
		strings.Contains(rec.Body.String(), "readable") {
		t.Fatalf("a read with the database down and no copy: %d %s, want 503 home_unreachable (the notice, not the full page)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/system/version"); rec.Code != http.StatusOK {
		t.Fatalf("a local route with the database down: %d", rec.Code)
	}
	cp.dead = false
	pair.CheckAll(context.Background())
	if rec := do(http.MethodGet, "/api/videos"); rec.Code != http.StatusOK {
		t.Fatalf("a read with the database down and the copy up: %d %s", rec.Code, rec.Body.String())
	}
}
