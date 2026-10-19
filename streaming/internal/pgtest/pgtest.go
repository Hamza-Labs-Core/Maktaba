// Package pgtest gives streaming's Postgres tests a scratch, fully migrated database.
// Tests using it run only when MAKTABA_TEST_PG_URL points at a Postgres whose user may
// create databases (and roles); otherwise they skip. The migrations are applied with
// the API's runner (`go run ./api migrate up`), so the schema is exactly production's.
// Never point it at a live database: it creates and drops only its own database, but
// roles it makes are cluster-wide.
package pgtest

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RepoRoot is the repository root (from this file's location).
func RepoRoot(t testing.TB) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("pgtest: cannot locate the repository")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// GoBin is the go tool on PATH's GOROOT (`go env GOROOT`), else "go".
func GoBin() string {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "go"
	}
	p := filepath.Join(strings.TrimSpace(string(out)), "bin", "go")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if _, err := os.Stat(p + ".exe"); err == nil {
		return p + ".exe"
	}
	return "go"
}

// Base returns MAKTABA_TEST_PG_URL or skips the test.
func Base(t testing.TB) string {
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset: skipping the real-Postgres test")
	}
	return base
}

// ScratchDB creates and migrates a database, returns its DSN and a pool on it; both
// go away when the test ends.
func ScratchDB(t testing.TB) (string, *pgxpool.Pool) {
	t.Helper()
	base := Base(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Skipf("scratch Postgres unreachable: %v", err)
	}
	name := "e32_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	_ = admin.Close(ctx)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
	})
	root := RepoRoot(t)
	cmd := exec.Command(GoBin(), "run", ".", "migrate", "--dir", filepath.Join(root, "shared", "db", "migrations"), "up")
	cmd.Dir = filepath.Join(root, "api")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "DATABASE_URL="+dsn)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("migrate: %v\n%s", err, out)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return dsn, pool
}

// WithUser rewrites dsn to log in as user/password.
func WithUser(t testing.TB, dsn, user, password string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
