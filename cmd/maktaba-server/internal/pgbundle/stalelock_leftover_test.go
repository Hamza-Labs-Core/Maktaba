//go:build !windows

package pgbundle

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestLeftoverPostmasterHelper is a postmaster the last server left: it waits, and
// with MAKTABA_TEST_IGNORE_INT it ignores the fast-shutdown ask (run only as a child).
func TestLeftoverPostmasterHelper(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_LEFTOVER_HELPER") != "1" {
		t.Skip("a helper process")
	}
	if os.Getenv("MAKTABA_TEST_IGNORE_INT") == "1" {
		signal.Ignore(syscall.SIGINT)
	}
	time.Sleep(30 * time.Second)
}

// A postmaster.pid naming a postgres the system adopted (its server was killed: macOS
// has no launcher reaping what it left) is stopped before the next start, fast first,
// killed when it does not end; one whose parent still runs is left to Postgres.
func TestALeftoverPostmasterIsStopped(t *testing.T) {
	cases := []struct {
		name      string
		ppid      int
		ignoreInt bool
		stopped   bool
	}{
		{"adopted, stops on the ask", 1, false, true},
		{"adopted, ignores the ask", 1, true, true},
		{"its server still runs", os.Getpid(), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			helper := exec.Command(os.Args[0], "-test.run=^TestLeftoverPostmasterHelper$")
			helper.Env = append(os.Environ(), "MAKTABA_TEST_LEFTOVER_HELPER=1")
			if tc.ignoreInt {
				helper.Env = append(helper.Env, "MAKTABA_TEST_IGNORE_INT=1")
			}
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { _ = helper.Wait(); close(done) }() // reaps it, so it reads as gone
			t.Cleanup(func() { _ = helper.Process.Kill(); <-done })
			pid := helper.Process.Pid

			oldLook, oldParent, oldWait := lookProcess, parentOf, leftoverWait
			t.Cleanup(func() { lookProcess, parentOf, leftoverWait = oldLook, oldParent, oldWait })
			lookProcess = func(p int) (string, bool) {
				select {
				case <-done:
					return "", false
				default:
					return "postgres", true
				}
			}
			parentOf = func(int) (int, bool) { return tc.ppid, true }
			leftoverWait = 500 * time.Millisecond
			time.Sleep(200 * time.Millisecond) // the helper's signal setup

			b := Bundle{Data: t.TempDir()}
			lock := filepath.Join(b.Data, "postmaster.pid")
			if err := os.WriteFile(lock, []byte(strconv.Itoa(pid)+"\n"+b.Data+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			b.ClearStaleLock()
			select {
			case <-done:
				if !tc.stopped {
					t.Fatal("a postgres whose server still runs was stopped")
				}
			case <-time.After(time.Second):
				if tc.stopped {
					t.Fatal("the leftover postmaster still runs")
				}
			}
			if _, err := os.Stat(lock); err != nil {
				t.Fatal("the lock was removed: Postgres takes over a gone postmaster's lock itself")
			}
		})
	}
}

// parentPID reads the real parent where it is looked at (macOS).
func TestParentPIDIsTheParent(t *testing.T) {
	ppid, ok := parentPID(os.Getpid())
	if !ok {
		t.Skip("not looked at on this system")
	}
	if ppid != os.Getppid() {
		t.Fatalf("parentPID = %d, want %d", ppid, os.Getppid())
	}
}
