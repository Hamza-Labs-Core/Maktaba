// Package tvchangetest gives the Epic 33 tests a database with the TV change tables on
// both backends: SQLite (always: the slot 0127 / 0128 / 0130 SQLite twins plus the
// tables they read) and Postgres (a scratch, fully migrated database when
// MAKTABA_TEST_PG_URL is set).
package tvchangetest

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the SQLite driver

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Backend is one database to run a test against.
type Backend struct {
	Name string
	DB   *sql.DB
}

// Backends are SQLite and, when MAKTABA_TEST_PG_URL is set, Postgres.
func Backends(t *testing.T) []Backend {
	t.Helper()
	out := []Backend{{Name: "sqlite", DB: SQLite(t)}}
	if os.Getenv("MAKTABA_TEST_PG_URL") != "" {
		db, _ := clustertest.ScratchDB(t)
		out = append(out, Backend{Name: "postgres", DB: db})
	}
	return out
}

// base are the tables the TV change code reads that older slots own (the columns it
// reads; foreign keys left out).
const base = `
CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL, pw_hash TEXT NOT NULL DEFAULT '',
	is_admin INTEGER NOT NULL DEFAULT 0);
CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT (datetime('now')), updated_by TEXT);
CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, action TEXT NOT NULL,
	actor_user_id TEXT, target_id TEXT, payload TEXT NOT NULL DEFAULT '{}',
	ts TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), actor_source TEXT, target_kind TEXT);
CREATE TABLE channels (id TEXT PRIMARY KEY, number INTEGER NOT NULL, name TEXT NOT NULL DEFAULT '',
	slug TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT 'shuffle', lifecycle TEXT NOT NULL DEFAULT 'live',
	logo_path TEXT, mode_config TEXT);
CREATE TABLE channel_programs (id TEXT PRIMARY KEY, channel_id TEXT NOT NULL, seq INTEGER NOT NULL,
	kind TEXT NOT NULL DEFAULT 'program', video_id TEXT, start_at TEXT NOT NULL, end_at TEXT NOT NULL,
	source_offset INTEGER NOT NULL DEFAULT 0, source_duration INTEGER NOT NULL DEFAULT 0,
	title_snapshot TEXT NOT NULL DEFAULT '{}', sched_gen INTEGER NOT NULL DEFAULT 0);
`

// SQLite is a fresh SQLite file with the TV change tables.
func SQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tv.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// a scratch database: no fsync per statement (keeps the unit tier's
	// per-test budget for the many tests that build one)
	for _, p := range []string{"PRAGMA synchronous = OFF", "PRAGMA journal_mode = MEMORY"} {
		if _, err := db.Exec(p); err != nil {
			t.Fatal(err)
		}
	}
	stmts := splitSQL(base)
	stmts = append(stmts, slot(t, "0127_tv_releases_changes.sqlite.sql")...)
	stmts = append(stmts, slot(t, "0130_notifications.sqlite.sql")...)
	for _, s := range stmts {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "INSERT INTO JOB_STAGES") {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

func splitSQL(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ";") {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}

// slot is the Up statements of a migration file.
func slot(t *testing.T, name string) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(b), "-- +goose Down")
	var stmts []string
	for _, blk := range strings.Split(up, "-- +goose StatementBegin") {
		blk, _, _ = strings.Cut(blk, "-- +goose StatementEnd")
		for _, s := range strings.Split(blk, ";") {
			var keep []string
			for _, l := range strings.Split(s, "\n") {
				if x := strings.TrimSpace(l); x != "" && !strings.HasPrefix(x, "--") {
					keep = append(keep, l)
				}
			}
			if len(keep) > 0 {
				stmts = append(stmts, strings.Join(keep, "\n"))
			}
		}
	}
	return stmts
}
