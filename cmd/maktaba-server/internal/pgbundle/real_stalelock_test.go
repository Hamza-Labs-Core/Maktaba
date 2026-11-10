//go:build integration && !windows

package pgbundle

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// TestRealRestartAfterAKillWithItsPIDReused is a power cut, or `docker kill`, against a real
// Postgres: the postmaster is killed (SIGKILL), and its PID is another program's at the next
// start (a container's new PID namespace gives the same numbers again), in both the data
// directory's lock and the unix socket's. Postgres refuses to start on either lock while
// its PID runs; ClearStaleLock removes both, and the next start comes up on the same data.
func TestRealRestartAfterAKillWithItsPIDReused(t *testing.T) {
	home := realHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	root, err := os.MkdirTemp("", "mkpg") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: testport.Free(t)}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer // every start's log, kept in the test's report
	t.Cleanup(func() { t.Logf("postgres's log:\n%s", logs.String()) })
	start := func() *exec.Cmd {
		t.Helper()
		srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
		srv.Stdout, srv.Stderr = &logs, &logs
		if err := srv.Start(); err != nil {
			t.Fatal(err)
		}
		return srv
	}
	ready := func(srv *exec.Cmd) error {
		exited := make(chan error, 1)
		go func() { exited <- srv.Wait() }()
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			select {
			case err := <-exited:
				return &exitError{err}
			default:
			}
			err := Ready(ctx, b.Addr(), AppRole, AppDatabase)
			if err == nil {
				return nil
			}
			if time.Now().After(deadline) {
				return err
			}
		}
	}

	srv := start()
	if err := ready(srv); err != nil {
		t.Fatalf("first start: %v", err)
	}
	sock := filepath.Join(b.Data, ".s.PGSQL."+strconv.Itoa(b.Port)+".lock")
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("no socket lock in the data directory: %v", err)
	}
	// the power cut: the postmaster killed, its children end on its death and let go of the
	// shared memory; the locks stay
	if err := srv.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	dataLock := filepath.Join(b.Data, "postmaster.pid")
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		raw, err := os.ReadFile(dataLock)
		if err != nil {
			t.Fatalf("the kill took the lock: %v", err)
		}
		if !shmInUse(string(raw)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the killed postmaster's children still hold its shared memory")
		}
	}
	// the next start's PID namespace: the old postmaster's PID is another program's
	other := helperProcess(t)
	for _, f := range []string{dataLock, sock} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		_, rest, _ := strings.Cut(string(raw), "\n")
		if err := os.WriteFile(f, []byte(strconv.Itoa(other)+"\n"+rest), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// what crash-looped: the data directory's lock cleared alone, Postgres refuses the
	// socket's
	b.clearStaleDataLock()
	logs.WriteString("--- the start with the socket's stale lock left ---\n")
	if err := ready(start()); err == nil {
		t.Fatal("postgres started over a socket lock whose PID runs: the reproduction no longer holds")
	}
	if !strings.Contains(logs.String(), ".s.PGSQL."+strconv.Itoa(b.Port)+".lock") {
		t.Fatalf("postgres did not stop on the socket's lock")
	}

	// the fix: both locks go before the start
	if err := os.WriteFile(dataLock, []byte(strconv.Itoa(other)+"\n"+b.Data+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.ClearStaleLock()
	logs.WriteString("--- the start after ClearStaleLock ---\n")
	srv = start()
	t.Cleanup(func() {
		_ = srv.Process.Signal(os.Interrupt)
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if _, alive := processName(srv.Process.Pid); !alive {
				return
			}
		}
	})
	if err := ready(srv); err != nil {
		t.Fatalf("the start after the kill: %v", err)
	}
	if _, err := b.Backup(ctx, time.Now()); err != nil {
		t.Fatalf("the restarted cluster's data: %v", err)
	}
}

// exitError: postgres exited before it was ready.
type exitError struct{ err error }

func (e *exitError) Error() string { return "postgres exited: " + errString(e.err) }

func errString(err error) string {
	if err == nil {
		return "status 0"
	}
	return err.Error()
}
