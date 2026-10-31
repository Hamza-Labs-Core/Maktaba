package pgbundle

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

// A postmaster.pid naming a postgres the system adopted (its server was killed: macOS
// has no launcher reaping what it left) is stopped before the next start, fast first,
// killed when it does not end; one whose parent still runs is left to Postgres.
func TestALeftoverPostmasterIsStopped(t *testing.T) {
	const pid = 4242
	cases := []struct {
		name      string
		ppid      int
		ignoreInt bool
		want      []syscall.Signal
	}{
		{"adopted, stops on the ask", 1, false, []syscall.Signal{syscall.SIGINT}},
		{"adopted, ignores the ask", 1, true, []syscall.Signal{syscall.SIGINT, syscall.SIGKILL}},
		{"its server still runs", 777, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldLook, oldParent, oldSignal, oldWait := lookProcess, parentOf, signalPID, leftoverWait
			t.Cleanup(func() { lookProcess, parentOf, signalPID, leftoverWait = oldLook, oldParent, oldSignal, oldWait })
			alive := true
			var got []syscall.Signal
			lookProcess = func(int) (string, bool) {
				if !alive {
					return "", false
				}
				return "postgres", true
			}
			parentOf = func(int) (int, bool) { return tc.ppid, true }
			signalPID = func(p int, sig syscall.Signal) error {
				if p != pid {
					t.Fatalf("signalled %d, want %d", p, pid)
				}
				got = append(got, sig)
				if sig == syscall.SIGKILL || !tc.ignoreInt {
					alive = false
				}
				return nil
			}
			leftoverWait = 20 * time.Millisecond

			b := Bundle{Data: t.TempDir()}
			lock := filepath.Join(b.Data, "postmaster.pid")
			if err := os.WriteFile(lock, []byte("4242\n"+b.Data+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			b.ClearStaleLock()
			if !slices.Equal(got, tc.want) {
				t.Fatalf("signals = %v, want %v", got, tc.want)
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
