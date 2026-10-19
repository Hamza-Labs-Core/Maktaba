//go:build windows

package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

// On Windows a role binary is found by its .exe name, with no execute bits to check.
func TestResolveBinaryFindsTheExeOnWindows(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "maktaba-api.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveBinary("maktaba-api", "MAKTABA_TEST_NO_SUCH_BIN", dir)
	if err != nil || got != exe {
		t.Fatalf("resolveBinary = %q, %v; want %q", got, err, exe)
	}
	t.Setenv("MAKTABA_TEST_BIN", exe)
	if got, err := resolveBinary("maktaba-api", "MAKTABA_TEST_BIN", ""); err != nil || got != exe {
		t.Fatalf("override: %q, %v", got, err)
	}
}

// Windows: a child terminated from outside (TerminateProcess, as Task
// Manager does) is restarted; there is no signal, only the exit code.
func TestTerminatedChildIsRestartedOnWindows(t *testing.T) {
	streaming, _ := fakeProc(t, RoleStreaming, "serve")
	s := New([]Process{streaming}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "running", runningPID(RoleStreaming))
	p, err := os.FindProcess(proc(snap, RoleStreaming).PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	snap = waitFor(t, s, "restarted", func(s Snapshot) bool {
		c := proc(s, RoleStreaming)
		return c.Restarts == 1 && c.State == StateRunning
	})
	exit := proc(snap, RoleStreaming).LastExit
	if exit == nil || exit.Signal != "" || exit.Code != 1 || exit.Reason != ReasonCrash {
		t.Fatalf("last exit = %+v, want code 1 crash with no signal", exit)
	}
}
