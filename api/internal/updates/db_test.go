package updates

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

type backend struct {
	name string
	db   *sql.DB
}

// backends are SQLite (the TV change test tables + slot 0132's SQLite twin) and, when
// MAKTABA_TEST_PG_URL is set, a fully migrated Postgres.
func backends(t *testing.T) []backend {
	t.Helper()
	out := []backend{{"sqlite", sqliteDB(t)}}
	if os.Getenv("MAKTABA_TEST_PG_URL") != "" {
		db, _ := clustertest.ScratchDB(t)
		out = append(out, backend{"postgres", db})
	}
	return out
}

func sqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	db := tvchangetest.SQLite(t)
	for _, slot := range []string{"0132_update_runs.sqlite.sql", "0149_update_run_detail.sqlite.sql"} {
		for _, s := range slotStatements(t, slot) {
			// the API's SQLite applier takes ADD COLUMN IF NOT EXISTS (api/migrate_sqlite.go);
			// a fresh test database has none of them yet
			s = strings.Replace(s, "ADD COLUMN IF NOT EXISTS", "ADD COLUMN", 1)
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("%s: %v", s, err)
			}
		}
	}
	// what the update driver reads of the cluster and the jobs (slots 0103, 0135; 0002;
	// Epic 33's channel_runtime): the columns it uses
	for _, s := range []string{
		`CREATE TABLE IF NOT EXISTS cluster_nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL, state TEXT NOT NULL,
			platform TEXT, version TEXT, full_node INTEGER NOT NULL DEFAULT 0, drain INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS processing_jobs (id TEXT PRIMARY KEY, state TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS channel_runtime (channel_id TEXT PRIMARY KEY, viewer_count INTEGER NOT NULL DEFAULT 0,
			host_id TEXT, state TEXT NOT NULL DEFAULT 'idle')`,
		`CREATE TABLE IF NOT EXISTS cluster_switchovers (id TEXT PRIMARY KEY, state TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "db", "migrations")
}

// slotStatements are the Up statements of a migration file.
func slotStatements(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(migrationsDir(t), name))
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

// recNotifier records notices.
type recNotifier struct {
	mu  sync.Mutex
	got []recNotice
}

type recNotice struct {
	kind string
	p    notify.Payload
}

func (r *recNotifier) Notify(_ context.Context, kind string, p notify.Payload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, recNotice{kind, p})
	return nil
}

func (r *recNotifier) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, n := range r.got {
		out = append(out, n.kind)
	}
	return out
}

func (r *recNotifier) find(kind string) *recNotice {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.got {
		if r.got[i].kind == kind {
			return &r.got[i]
		}
	}
	return nil
}

// enableAutoInstall turns the phase-A build switch on for one test.
// disableAutoInstall is a build that turned automatic installs off at link time
// (the phase-A behaviour: check, catalog and notify only).
func disableAutoInstall(t *testing.T) {
	t.Helper()
	old := autoInstallEnabled
	autoInstallEnabled = "false"
	t.Cleanup(func() { autoInstallEnabled = old })
}

func enableAutoInstall(t *testing.T) {
	t.Helper()
	if autoInstallEnabled == "true" {
		// the tests' default: nothing to write (the parallel tests read it together; only
		// serial tests turn it off, and put it back before those run)
		return
	}
	old := autoInstallEnabled
	autoInstallEnabled = "true"
	t.Cleanup(func() { autoInstallEnabled = old })
}
