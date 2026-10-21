package supervisor

import (
	"strconv"
	"sync/atomic"
	"testing"
)

// Restart stops a process and starts it again at once, with its arguments read afresh
// (the bundled postgres: whether it listens beyond this machine): no backoff, and not
// an exit of a crash loop.
func TestRestartStartsItAgainWithFreshArguments(t *testing.T) {
	p, _ := fakeProc(t, RoleAPI, "serve")
	var starts atomic.Int32
	p.Args = func() []string {
		n := starts.Add(1)
		return []string{"-test.run=^$", "-fake.start=" + strconv.Itoa(int(n))}
	}
	s := New([]Process{p}, testPolicy())
	running(t, s)
	first := waitFor(t, s, "api running", func(sn Snapshot) bool { return proc(sn, RoleAPI).State == StateRunning })
	pid := proc(first, RoleAPI).PID
	if err := s.Restart(RoleAPI, ""); err != nil {
		t.Fatal(err)
	}
	again := waitFor(t, s, "api started again", func(sn Snapshot) bool {
		a := proc(sn, RoleAPI)
		return a.State == StateRunning && a.PID != 0 && a.PID != pid
	})
	a := proc(again, RoleAPI)
	if a.LastExit == nil || a.LastExit.Reason != ReasonRestarted {
		t.Errorf("last exit %+v, want reason %q", a.LastExit, ReasonRestarted)
	}
	if a.Restarts != 0 {
		t.Errorf("a restart on request counted as an exit of a crash loop: %d", a.Restarts)
	}
	if n := starts.Load(); n != 2 {
		t.Errorf("the arguments were read %d times, want once per start (2)", n)
	}
	if err := s.Restart(RoleAPI, "nope"); err == nil {
		t.Error("an unknown instance was accepted")
	}
}
