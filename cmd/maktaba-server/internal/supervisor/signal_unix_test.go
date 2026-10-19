//go:build !windows

package supervisor

import (
	"syscall"
	"testing"
)

// POSIX: a child killed by a signal is restarted and the signal is
// recorded on its last exit.
func TestSignalledChildRecordsSignal(t *testing.T) {
	streaming, _ := fakeProc(t, RoleStreaming, "serve")
	s := New([]Process{streaming}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "running", runningPID(RoleStreaming))
	pid := proc(snap, RoleStreaming).PID
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	snap = waitFor(t, s, "restarted", func(s Snapshot) bool { return proc(s, RoleStreaming).Restarts == 1 })
	exit := proc(snap, RoleStreaming).LastExit
	if exit == nil || exit.Signal != "SIGKILL" || exit.Code != -1 || exit.Reason != ReasonCrash {
		t.Fatalf("last exit = %+v, want SIGKILL crash", exit)
	}
}
