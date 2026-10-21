//go:build integration && !windows

package pgbundle

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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
	if _, err := b.Backup(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// a second Prepare (the next start) touches nothing
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
}
