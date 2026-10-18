// Package clustertest gives the Epic 31 tests a scratch, fully migrated
// Postgres database. Tests using it run only when MAKTABA_TEST_PG_URL
// points at a Postgres whose user may create databases and roles, e.g.
//
//	MAKTABA_TEST_PG_URL=postgresql://maktaba:maktaba@localhost:5432/maktaba_test?sslmode=disable \
//	  go test ./internal/cluster/... ./internal/handlers/cluster/...
//
// The scratch database is dropped when the test ends. Never point it at a
// live database: it only creates and drops its own t1_cluster_* database,
// but the machine roles it makes are cluster-wide.
package clustertest

import (
	"context"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// ScratchDB creates and migrates a scratch database and returns a handle
// and its DSN. It skips the test when MAKTABA_TEST_PG_URL is unset.
func ScratchDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset: skipping the real-Postgres cluster test")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "t1_cluster_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(name)); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + pq.QuoteIdentifier(name)); err != nil {
			t.Logf("drop scratch database %s: %v", name, err)
		}
	})
	migrate(t, db)
	return db, dsn
}

// WithUser rewrites dsn to log in as user/password.
func WithUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the migrations")
	}
	// api/internal/cluster/clustertest/pg.go -> repo root
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations")
}

// migrate applies every Postgres slot (slot 0000 is the bootstrap goose
// cannot run; the SQLite twins are skipped) from a staged copy.
func migrate(t *testing.T, db *sql.DB) {
	t.Helper()
	src := migrationsDir(t)
	stage := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".sqlite.sql") || strings.HasPrefix(n, "0000_") {
			continue
		}
		if err := copyFile(filepath.Join(src, n), filepath.Join(stage, n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetLogger(goose.NopLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := goose.UpContext(ctx, db, stage); err != nil {
		t.Fatalf("goose up: %v", err)
	}
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
