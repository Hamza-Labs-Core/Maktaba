package main

// A full node that starts with the home's database down (PR #145) does not refuse to
// start: the role check waits for the database, and playback reads the catalog the node
// kept from before.

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/catalog"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/vodfallback"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

// keptCatalog writes the catalog a node kept before the outage into dir.
func keptCatalog(t *testing.T, dir string) {
	t.Helper()
	src, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, q := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, path TEXT, duration_sec REAL)`,
		`INSERT INTO videos VALUES ('22222222-0000-4000-8000-000000000001', 'lib1', 'smb://nas/Originals/a.mkv', 1800)`,
	} {
		if _, err := src.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	c := catalog.Open(filepath.Join(dir, catalog.FileName), nil)
	if err := c.Sync(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
}

func TestNodeStartsWithTheHomeDatabaseDown(t *testing.T) {
	dir := t.TempDir()
	keptCatalog(t, dir)
	t.Setenv("MAKTABA_NODE_STATE_DIR", dir)
	t.Cleanup(func() { sharepath.Set(sharepath.Map{}) })

	// nothing listens there: the home's database is gone
	db, err := sql.Open("postgres", "postgres://node@127.0.0.1:1/maktaba?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := nodemode.Config{Enabled: true, NodeID: "6f1c9a2e-0000-4000-8000-000000000001", HomeAPIURL: "http://127.0.0.1:1"}
	rt, err := startNode(ctx, cfg, db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatalf("a node with the home's database down refused to start: %v", err)
	}
	if !rt.dbWatch.Down() {
		t.Fatal("the database is not known down")
	}
	lib, dur, ok := rt.offline().Video("22222222-0000-4000-8000-000000000001")
	if !ok || lib != "lib1" || dur != 1800 {
		t.Fatalf("the kept catalog: %q %v %v", lib, dur, ok)
	}
	if p, ok := rt.offline().Path("22222222-0000-4000-8000-000000000001"); !ok || p != "smb://nas/Originals/a.mkv" {
		t.Fatalf("path: %q %v", p, ok)
	}
	select {
	case err := <-rt.fatal:
		t.Fatalf("stopped: %v", err)
	default:
	}
}

// With the home's database known down, a node's own reads answer at once (the app keeps
// its notice) and playback still reaches its handler.
func TestNodeReadsWaitForTheDatabasePlaybackDoesNot(t *testing.T) {
	r := chi.NewRouter()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	r.Get("/api/videos", ok)           // any
	r.Post("/api/stream/sessions", ok) // local
	r.Get("/api/system/version", ok)   // local, allowlisted
	watch := &catalog.Watch{}
	rt := &nodeRuntime{resolver: &nodeauth.Resolver{Link: &fakeHome{}}, vod: &vodfallback.Fallback{}, dbWatch: watch,
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
	watch.Failed(errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"))
	rec := do(http.MethodGet, "/api/videos")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"home_unreachable"`) ||
		strings.Contains(rec.Body.String(), "readable") {
		t.Fatalf("a read with the database down: %d %s, want 503 home_unreachable (the notice, not the full page)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/system/version"); rec.Code != http.StatusOK {
		t.Fatalf("a local route with the database down: %d", rec.Code)
	}
}
