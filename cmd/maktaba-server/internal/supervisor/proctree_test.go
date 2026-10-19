package supervisor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// grandchildProc is a "serve" fake child that first starts a sleeping
// grandchild (standing in for ffmpeg under streaming) and writes its pid.
func grandchildProc(t *testing.T) (Process, string, string) {
	t.Helper()
	pidfile := filepath.Join(t.TempDir(), "grandchild.pid")
	p, addr := fakeProc(t, RoleStreaming, "serve", "FAKE_GRANDCHILD_PIDFILE="+pidfile)
	return p, addr, pidfile
}

// readGrandchild waits for the pid file and removes it, so the next
// incarnation's grandchild can be told apart.
func readGrandchild(t *testing.T, pidfile string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		raw, err := os.ReadFile(pidfile)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(pidfile)
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("grandchild never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitGone(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still running after %s", pid, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func killGrandchildOnCleanup(t *testing.T, pid int) {
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil && processAlive(pid) {
			_ = p.Kill()
		}
	})
}

// A crash takes the crashed child's descendants with it, so the restart
// never runs next to an orphaned ffmpeg.
func TestCrashTakesGrandchildrenDown(t *testing.T) {
	p, addr, pidfile := grandchildProc(t)
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleStreaming))
	gc := readGrandchild(t, pidfile)
	killGrandchildOnCleanup(t, gc)
	if !processAlive(gc) {
		t.Fatal("grandchild not running before the crash")
	}

	get(t, "http://"+addr+"/crash?code=3")
	waitFor(t, s, "restarted", func(s Snapshot) bool { return proc(s, RoleStreaming).Restarts == 1 })
	waitGone(t, gc, "its parent crashed")
	next := readGrandchild(t, pidfile) // the new incarnation has its own
	killGrandchildOnCleanup(t, next)
	if !processAlive(next) {
		t.Error("the restarted child's grandchild is not running")
	}
}

// A hang kill takes the whole tree.
func TestHangKillTakesGrandchildrenDown(t *testing.T) {
	p, addr, pidfile := grandchildProc(t)
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleStreaming))
	gc := readGrandchild(t, pidfile)
	killGrandchildOnCleanup(t, gc)

	get(t, "http://"+addr+"/hang")
	waitFor(t, s, "restarted after hang", func(s Snapshot) bool { return proc(s, RoleStreaming).Restarts >= 1 })
	waitGone(t, gc, "a hang kill")
}

// A supervisor shutdown leaves no grandchildren behind.
func TestShutdownTakesGrandchildrenDown(t *testing.T) {
	p, _, pidfile := grandchildProc(t)
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	waitFor(t, s, "running", runningPID(RoleStreaming))
	gc := readGrandchild(t, pidfile)
	killGrandchildOnCleanup(t, gc)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	waitGone(t, gc, "shutdown")
}
