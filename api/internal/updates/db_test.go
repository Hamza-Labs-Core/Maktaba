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
	for _, s := range slotStatements(t, "0132_update_runs.sqlite.sql") {
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
func enableAutoInstall(t *testing.T) {
	t.Helper()
	old := autoInstallEnabled
	autoInstallEnabled = "true"
	t.Cleanup(func() { autoInstallEnabled = old })
}
