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
	if _, err := b.Backup(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// a second Prepare (the next start) touches nothing
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
}
