package pgbundle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestStaleLockHelper is the process another program is: it waits (run only as a child).
func TestStaleLockHelper(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_STALE_LOCK_HELPER") != "1" {
		t.Skip("a helper process")
	}
	time.Sleep(30 * time.Second)
}

// A postmaster.pid naming another program (the system gave the PID again after a
// postmaster that did not end cleanly) goes before the server starts; one naming no
// process, or one that does not read, is left to Postgres.
func TestAStaleLockOfAnotherProgramGoes(t *testing.T) {
	b := Bundle{Data: t.TempDir()}
	lock := filepath.Join(b.Data, "postmaster.pid")
	write := func(first string) {
		t.Helper()
		if err := os.WriteFile(lock, []byte(first+"\n"+b.Data+"\n1700000000\n5432\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	there := func() bool {
		_, err := os.Stat(lock)
		return err == nil
	}
	b.ClearStaleLock() // none: nothing to do

	other := exec.Command(os.Args[0], "-test.run=^TestStaleLockHelper$")
	other.Env = append(os.Environ(), "MAKTABA_TEST_STALE_LOCK_HELPER=1")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	write(strconv.Itoa(other.Process.Pid))
	b.ClearStaleLock()
	if there() {
		t.Fatal("a lock naming another program was kept")
	}

	write("not a pid")
	b.ClearStaleLock()
	if !there() {
		t.Fatal("a lock that does not read was removed")
	}
	_ = other.Process.Kill()
	_ = other.Wait()
	write(strconv.Itoa(other.Process.Pid)) // no such process now
	b.ClearStaleLock()
	if !there() {
		t.Fatal("a lock naming no process was removed (Postgres takes it over itself)")
	}
	write(strconv.Itoa(os.Getpid()))
	b.ClearStaleLock()
	if !there() {
		t.Fatal("a lock naming this process was removed")
	}
}
