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

// TestRealStartAfterAKillWaitsForTheBackendsLeft is the supervisor's kill of a postmaster
// that did not stop in time, against a real Postgres: its own processes are slow to
// notice (stopped here, as one in the middle of a long write is) and still hold the
// shared memory. A start made at once is refused ("pre-existing shared memory block is
// still in use": the home's four failed starts in a row); AwaitShmFree returns when those
// processes are gone, and the start after it comes up on the same data.
func TestRealStartAfterAKillWaitsForTheBackendsLeft(t *testing.T) {
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
	start := func() (*exec.Cmd, <-chan error) {
		t.Helper()
		srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
		srv.Stdout, srv.Stderr = &logs, &logs
		if err := srv.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan error, 1)
		go func() { exited <- srv.Wait() }()
		return srv, exited
	}
	ready := func(exited <-chan error) error {
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

	srv, exited := start()
	if err := ready(exited); err != nil {
		t.Fatalf("first start: %v", err)
	}
	// the postmaster's own processes (the checkpointer, the writers), stopped: they
	// cannot notice the postmaster's death, and keep the shared memory attached
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(srv.Process.Pid)).Output()
	if err != nil || len(strings.Fields(string(out))) == 0 {
		t.Skipf("the postmaster's processes cannot be listed here (pgrep: %v)", err)
	}
	var slow []int
	for _, f := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("pgrep answered %q", out)
		}
		slow = append(slow, pid)
	}
	signalSlow := func(sig syscall.Signal) {
		for _, pid := range slow {
			_ = syscall.Kill(pid, sig)
		}
	}
	signalSlow(syscall.SIGSTOP)
	t.Cleanup(func() { signalSlow(syscall.SIGKILL) })
	if err := srv.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-exited
	lock := filepath.Join(b.Data, "postmaster.pid")
	if raw, err := os.ReadFile(lock); err != nil || !shmInUse(string(raw)) {
		t.Fatalf("the stopped process does not hold the killed postmaster's shared memory (lock: %v)", err)
	}

	// what crash-looped: the start made at once
	logs.WriteString("--- the start made at once after the kill ---\n")
	_, exited = start()
	if err := ready(exited); err == nil {
		t.Fatal("postgres started next to a process of the killed postmaster: the reproduction no longer holds")
	}
	if !strings.Contains(logs.String(), "shared memory block") {
		t.Fatal("postgres did not stop on the shared memory still in use")
	}

	// the fix: the start waits until those processes are gone (they end 2 s from now)
	const held = 2 * time.Second
	time.AfterFunc(held, func() { signalSlow(syscall.SIGKILL) })
	began := time.Now()
	b.ClearStaleLock()
	b.AwaitShmFree(ctx)
	if d := time.Since(began); d < held-500*time.Millisecond || d > time.Minute {
		t.Fatalf("waited %s for a process that held the memory %s", d, held)
	}
	logs.WriteString("--- the start after AwaitShmFree ---\n")
	srv, exited = start()
	t.Cleanup(func() {
		_ = srv.Process.Signal(os.Interrupt)
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if _, alive := processName(srv.Process.Pid); !alive {
				return
			}
		}
	})
	if err := ready(exited); err != nil {
		t.Fatalf("the start after the wait: %v", err)
	}
	if _, err := b.Backup(ctx, time.Now()); err != nil {
		t.Fatalf("the restarted cluster's data: %v", err)
	}
}
