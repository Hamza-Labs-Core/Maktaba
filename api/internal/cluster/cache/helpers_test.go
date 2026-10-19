package cache

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite")

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Most tests run on both databases: SQLite always (the 0104 twin as
// shipped, beside the health_samples and queue tables it needs), Postgres
// when MAKTABA_TEST_PG_URL is set (a scratch database with every
// migration, clustertest.ScratchDB).

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(s string) *fakeClock {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return &fakeClock{t: t}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Add(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
	return f.t
}

func (f *fakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the migrations")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations")
}

// upStatements are the Up statements of a migration file.
func upStatements(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(migrationsDir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	var out, buf []string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(s, "-- +goose Down"):
			return out
		case strings.HasPrefix(s, "-- +goose StatementBegin"):
			in, buf = true, nil
		case strings.HasPrefix(s, "-- +goose StatementEnd"):
			if stmt := strings.TrimSpace(strings.Join(buf, "\n")); stmt != "" {
				out = append(out, stmt)
			}
			in = false
		case in:
			buf = append(buf, line)
		}
	}
	return out
}

// sqliteSchema: what the cache touches besides slot 0104 (from the 0087
// and 0089 twins).
var sqliteSchema = []string{
	`CREATE TABLE IF NOT EXISTS health_samples (
    at      TEXT NOT NULL,
    host    TEXT NOT NULL,
    metric  TEXT NOT NULL,
    value   REAL NOT NULL,
    PRIMARY KEY (metric, host, at))`,
	`CREATE INDEX IF NOT EXISTS health_samples_at_idx ON health_samples (at)`,
	`CREATE TABLE IF NOT EXISTS job_stages (stage TEXT PRIMARY KEY, pool TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS processing_jobs (id INTEGER PRIMARY KEY, stage TEXT NOT NULL, state TEXT NOT NULL)`,
}

func sqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "cache.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range append(append([]string{}, sqliteSchema...), upStatements(t, "0104_cluster_cache.sqlite.sql")...) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v\n%s", err, s)
		}
	}
	return db
}

// dbCase is one database a test runs on.
type dbCase struct {
	name   string
	driver string
	open   func(t *testing.T) *sql.DB
}

var dbCases = []dbCase{
	{"sqlite", "sqlite", sqliteDB},
	{"postgres", "postgres", func(t *testing.T) *sql.DB { db, _ := clustertest.ScratchDB(t); return db }},
}

// eachDB runs f on SQLite, and on Postgres when it is available.
func eachDB(t *testing.T, f func(t *testing.T, db *sql.DB, driver string)) {
	t.Helper()
	for _, dc := range dbCases {
		t.Run(dc.name, func(t *testing.T) {
			f(t, dc.open(t), dc.driver)
		})
	}
}

// quietLogger drops everything.
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestCache(db *sql.DB, driver string, clk *fakeClock, window time.Duration) *Cache {
	c := New(db, Options{Window: window, Driver: driver, Now: clk.Now, Logger: quietLogger(), ReadRate: 1_000_000})
	return c
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func proc(worker, host, ip string) agent.Proc {
	return agent.Proc{SessionID: "s-" + worker, WorkerID: worker, Host: host, IP: ip}
}

var bg = context.Background()
