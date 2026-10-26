//go:build integration && !windows

package pgbundle

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lib/pq"
)

// realHome finds a Postgres install on this machine (PATH, or Debian/Ubuntu's
// /usr/lib/postgresql/<v>/bin, which GitHub's runners carry).
func realHome(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("postgres"); err == nil {
		if _, err := exec.LookPath("initdb"); err == nil {
			return filepath.Dir(filepath.Dir(p))
		}
	}
	dirs, _ := filepath.Glob("/usr/lib/postgresql/*/bin/postgres")
	if len(dirs) > 0 {
		return filepath.Dir(filepath.Dir(dirs[len(dirs)-1]))
	}
	t.Skip("no postgres/initdb on this machine")
	return ""
}

// TestRealPostgres runs the whole first start against a real Postgres: initdb,
// the bootstrap, the foreground server the supervisor would run, the readiness
// check as the app role, and a dump.
func TestRealPostgres(t *testing.T) {
	home := realHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	root, err := os.MkdirTemp("", "mkpg") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: port}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Signal(os.Interrupt); _ = srv.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := Ready(ctx, b.Addr(), AppRole, AppDatabase)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not ready: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// a replication role (a machine's copy's) makes, reads or drops no slot with SQL: the
	// functions are the app role's alone in the app's database
	superURL, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	super, err := sql.Open("postgres", superURL)
	if err != nil {
		t.Fatal(err)
	}
	defer super.Close()
	if _, err := super.ExecContext(ctx, `CREATE ROLE maktaba_rep_test LOGIN REPLICATION PASSWORD 'rep-test-pass'`); err != nil {
		t.Fatal(err)
	}
	rep, err := sql.Open("postgres", (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba_rep_test", "rep-test-pass"),
		Host: b.Addr(), Path: "/" + AppDatabase, RawQuery: "sslmode=disable"}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer rep.Close()
	var pe *pq.Error
	if _, err := rep.ExecContext(ctx, `SELECT pg_create_logical_replication_slot('maktaba_test', 'pgoutput')`); !errors.As(err, &pe) ||
		pe.Code != "42501" {
		t.Fatalf("a replication role makes a slot with SQL: %v", err)
	}
	appURL, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	app, err := sql.Open("postgres", appURL)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := app.ExecContext(ctx, `SELECT pg_create_logical_replication_slot('maktaba_test', 'pgoutput')`); err != nil {
		t.Fatalf("the app role makes a machine's slot: %v", err)
	}
	if _, err := app.ExecContext(ctx, `SELECT pg_drop_replication_slot('maktaba_test')`); err != nil {
		t.Fatalf("the app role drops a machine's slot: %v", err)
	}
	// pgvector is in the app's database exactly when this Postgres has it
	var available, made bool
	if err := app.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM pg_available_extensions WHERE name = 'vector'),
		EXISTS (SELECT FROM pg_extension WHERE extname = 'vector')`).Scan(&available, &made); err != nil {
		t.Fatal(err)
	}
	if available != made {
		t.Fatalf("vector available %v, created %v", available, made)
	}
	t.Logf("pgvector in this Postgres: %v", available)
	if made {
		if _, err := app.ExecContext(ctx, `CREATE TABLE vec_check (e halfvec(3));
			CREATE INDEX ON vec_check USING hnsw (e halfvec_cosine_ops);
			INSERT INTO vec_check VALUES ('[1,0,0]'), ('[0,1,0]');
			DROP TABLE vec_check`); err != nil {
			t.Fatalf("the app role uses pgvector: %v", err)
		}
	}
	if _, err := b.Backup(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// a second Prepare (the next start) touches nothing
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
}

// TestRealUpdateDumpAndRestore is the desktop app's way back from a failed
// update against a real Postgres: a pre-update dump of the database the
// migrations made (as the app role), the migrations' changes, then the dump
// put back into the running server: the data is as it was, the changes gone,
// the objects the app role's again.
func TestRealUpdateDumpAndRestore(t *testing.T) {
	home := realHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	root, err := os.MkdirTemp("", "mkpg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: port}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Signal(os.Interrupt); _ = srv.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for Ready(ctx, b.Addr(), AppRole, AppDatabase) != nil {
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	q, err := b.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	// the old release's schema, made as the app role (as its migrations are)
	if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`;
CREATE TABLE goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (143, true);
CREATE TABLE videos (id serial PRIMARY KEY, title text NOT NULL);
INSERT INTO videos (title) VALUES ('kept'), ('also kept');`); err != nil {
		t.Fatal(err)
	}
	dump, err := b.DumpBeforeUpdate(ctx, "v0.2.0", "v0.3.0", 3)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dump) != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("dump = %s", dump)
	}
	// the new release's migrations, the first applied, the second half-way
	if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`;
INSERT INTO goose_db_version (version_id, is_applied) VALUES (144, true);
ALTER TABLE videos ADD COLUMN slug text;
DELETE FROM videos WHERE title = 'also kept';
CREATE TABLE channels (id serial PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	// a connection left open (the server cuts it off)
	held := exec.Command(b.Bin("psql"), "-X", "-q", "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", SuperUser,
		"-d", AppDatabase, "-c", "SELECT pg_sleep(60)")
	held.Env = append(os.Environ(), "PGPASSWORD="+q.pw)
	if err := held.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Process.Kill(); _ = held.Wait() })
	time.Sleep(300 * time.Millisecond)

	if err := b.RestoreRunning(ctx, dump, io.Discard); err != nil {
		t.Fatal(err)
	}
	for sql, want := range map[string]string{
		`SELECT string_agg(title, ',' ORDER BY id) FROM videos`:                                                "kept,also kept",
		`SELECT max(version_id) FROM goose_db_version`:                                                         "143",
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'videos' AND column_name = 'slug'`: "0",
		`SELECT to_regclass('channels') IS NULL`:                                                               "t",
		`SELECT tableowner FROM pg_tables WHERE tablename = 'videos'`:                                          AppRole,
	} {
		rows, err := q.Query(ctx, AppDatabase, sql)
		if err != nil || len(rows) != 1 || rows[0] != want {
			t.Errorf("%s = %v, %v; want %s", sql, rows, err, want)
		}
	}
	if err := Ready(ctx, b.Addr(), AppRole, AppDatabase); err != nil {
		t.Fatalf("the app role after the restore: %v", err)
	}
}
