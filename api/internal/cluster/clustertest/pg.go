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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	tmpl := template(t, admin, base)
	name := "t1_cluster_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	// a copy of the migrated template: one goose run per set of migrations, not
	// one per test (every slot again took ~2 s a test and kept the integration
	// tier near its budget)
	var cerr error
	for i := 0; i < 20; i++ {
		_, cerr = admin.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(name) + ` TEMPLATE ` + pq.QuoteIdentifier(tmpl))
		if cerr == nil || !strings.Contains(cerr.Error(), "being accessed by other users") {
			break
		}
		time.Sleep(100 * time.Millisecond) // another process is copying it right now
	}
	if cerr != nil {
		t.Fatalf("create scratch database: %v", cerr)
	}
	dsn := dsnFor(t, base, name)
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
	return db, dsn
}

func dsnFor(t *testing.T, base, name string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// templatePrefix names the migrated template databases; the suffix is a hash of
// the migrations, so a changed migration makes a new template.
const templatePrefix = "t1_cluster_tmpl_"

// template returns the migrated template database for the current migrations,
// making it the first time (under an advisory lock: the packages of a module
// test in parallel processes). It is built under a temporary name and renamed
// once migrated, so a template that exists is complete.
func template(t *testing.T, admin *sql.DB, base string) string {
	t.Helper()
	sum := migrationsHash(t)
	name := templatePrefix + sum
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	conn, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('clustertest_template'))`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('clustertest_template'))`)
	}()
	var exists bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		return name
	}
	// templates of older migrations: gone (best-effort; one in use stays)
	if rows, err := conn.QueryContext(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 't1\_cluster\_tmpl\_%'`); err == nil {
		var old []string
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				old = append(old, n)
			}
		}
		rows.Close()
		for _, n := range old {
			_, _ = conn.ExecContext(ctx, `DROP DATABASE IF EXISTS `+pq.QuoteIdentifier(n))
		}
	}
	build := name + "_b"
	_, _ = conn.ExecContext(ctx, `DROP DATABASE IF EXISTS `+pq.QuoteIdentifier(build)) // a half-built one
	if _, err := conn.ExecContext(ctx, `CREATE DATABASE `+pq.QuoteIdentifier(build)); err != nil {
		t.Fatalf("create template database: %v", err)
	}
	db, err := sql.Open("postgres", dsnFor(t, base, build))
	if err != nil {
		t.Fatal(err)
	}
	migrate(t, db)
	_ = db.Close()
	if _, err := conn.ExecContext(ctx, `ALTER DATABASE `+pq.QuoteIdentifier(build)+` RENAME TO `+pq.QuoteIdentifier(name)); err != nil {
		t.Fatalf("rename template database: %v", err)
	}
	return name
}

// migrationsHash is a short hash of the Postgres migrations' names and contents.
func migrationsHash(t *testing.T) string {
	t.Helper()
	src := migrationsDir(t)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".sqlite.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, n))
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
func migrate(t *testing.T, db *sql.DB) { MigrateTo(t, db, 0) }

// MigrateTo applies the Postgres slots up to version (0: every one) to db, as db's
// user.
func MigrateTo(t *testing.T, db *sql.DB, version int64) {
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
	if version > 0 {
		err = goose.UpToContext(ctx, db, stage, version)
	} else {
		err = goose.UpContext(ctx, db, stage)
	}
	if err != nil {
		t.Fatalf("goose up: %v", err)
	}
}

// DSN is base's URL for database name as user (password pw; "" keeps base's user).
func DSN(t *testing.T, base, name, user, pw string) string {
	t.Helper()
	dsn := dsnFor(t, base, name)
	if user == "" {
		return dsn
	}
	return WithUser(t, dsn, user, pw)
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
