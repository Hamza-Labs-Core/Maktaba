// Package pgtest gives streaming's Postgres tests a scratch, fully migrated database.
// Tests using it run only when MAKTABA_TEST_PG_URL points at a Postgres whose user may
// create databases (and roles); otherwise they skip. The migrations are applied with
// the API's runner (`go run ./api migrate up`), so the schema is exactly production's.
// Never point it at a live database: it creates and drops only its own database, but
// roles it makes are cluster-wide.
package pgtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

// ScratchDB creates a migrated database, returns its DSN and a pool on it; both go
// away when the test ends. It is a copy of a template migrated once per set of
// migrations (a `go run ./api migrate up` per test took seconds each).
func ScratchDB(t testing.TB) (string, *pgxpool.Pool) {
	t.Helper()
	base := Base(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Skipf("scratch Postgres unreachable: %v", err)
	}
	tmpl := template(t, admin, base)
	name := "e32_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	var cerr error
	for i := 0; i < 20; i++ {
		_, cerr = admin.Exec(ctx, `CREATE DATABASE `+name+` TEMPLATE `+tmpl)
		if cerr == nil || !strings.Contains(cerr.Error(), "being accessed by other users") {
			break
		}
		time.Sleep(100 * time.Millisecond) // another process is copying it right now
	}
	_ = admin.Close(ctx)
	if cerr != nil {
		t.Fatalf("create database: %v", cerr)
	}
	dsn := dsnFor(t, base, name)
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
	})
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return dsn, pool
}

func dsnFor(t testing.TB, base, name string) string {
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// template returns the migrated template database of the current migrations,
// migrating it the first time under an advisory lock (the packages test in
// parallel processes). It is built under a temporary name and renamed when
// migrated, so a template that exists is complete.
func template(t testing.TB, admin *pgx.Conn, base string) string {
	t.Helper()
	ctx := context.Background()
	root := RepoRoot(t)
	dir := filepath.Join(root, "shared", "db", "migrations")
	name := "e32_tmpl_" + migrationsHash(t, dir)
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(hashtext('pgtest_template'))`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, `SELECT pg_advisory_unlock(hashtext('pgtest_template'))`) }()
	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		return name
	}
	// templates of older migrations: gone (best-effort; one in use stays)
	if rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'e32\_tmpl\_%'`); err == nil {
		var old []string
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				old = append(old, n)
			}
		}
		rows.Close()
		for _, n := range old {
			_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+n)
		}
	}
	build := name + "_b"
	_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+build)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+build); err != nil {
		t.Fatalf("create template database: %v", err)
	}
	cmd := exec.Command(GoBin(), "run", ".", "migrate", "--dir", dir, "up")
	cmd.Dir = filepath.Join(root, "api")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "DATABASE_URL="+dsnFor(t, base, build))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("migrate: %v\n%s", err, out)
	}
	if _, err := admin.Exec(ctx, `ALTER DATABASE `+build+` RENAME TO `+name); err != nil {
		t.Fatalf("rename template database: %v", err)
	}
	return name
}

// migrationsHash is a short hash of the migrations' names and contents.
func migrationsHash(t testing.TB, dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
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
