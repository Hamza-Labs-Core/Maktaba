package supervisor

import (
	"strconv"
	"strings"
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

// An admin's restart of a component restarts each of its running processes, and nothing
// for a component with no process running.
func TestRestartComponent(t *testing.T) {
	p, _ := fakeProc(t, RoleAPI, "serve")
	p.Component = "transcription"
	s := New([]Process{p}, testPolicy())
	running(t, s)
	first := waitFor(t, s, "api running", func(sn Snapshot) bool { return proc(sn, RoleAPI).State == StateRunning })
	pid := proc(first, RoleAPI).PID
	if n := s.RestartComponent("transcription"); n != 1 {
		t.Fatalf("restarted %d", n)
	}
	waitFor(t, s, "started again", func(sn Snapshot) bool {
		a := proc(sn, RoleAPI)
		return a.State == StateRunning && a.PID != 0 && a.PID != pid
	})
	if n := s.RestartComponent("llm"); n != 0 {
		t.Fatalf("a component with no process: %d", n)
	}
}

// A start that met what its own pre-start wait (Settle) could not see — the bundled
// postgres on Windows, whose start ends while the shared memory of a postmaster that was
// killed is still held, the lock that wait goes by gone with the first failed start — is
// started again at once (RestartAtOnce): no backoff, not an exit of the crash loop, for
// as long as the hook says; then the crash policy takes over as for any crash.
func TestRestartAtOnceForAStartThatMetWhatTheWaitCouldNotSee(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "crash", "FAKE_CODE=1")
	var atOnce atomic.Int32
	atOnce.Store(3)
	p.RestartAtOnce = func() string {
		if atOnce.Add(-1) < 0 {
			return ""
		}
		return "the shared memory a postmaster that was killed left is still in use"
	}
	log := &syncBuffer{}
	s := New([]Process{p}, testPolicy(), WithLog(log))
	running(t, s)
	waitFor(t, s, "the at-once restarts done and the policy's backoff", func(sn Snapshot) bool {
		return strings.Count(log.String(), "started again at once") >= 3 &&
			strings.Contains(log.String(), "restarting in") && proc(sn, RolePipeline).Restarts >= 1
	})
	snap := s.Snapshot()
	c := proc(snap, RolePipeline)
	if c.Restarts < 1 {
		t.Errorf("the crashes after the hook said no did not count: %+v", c)
	}
	if c.CrashLoop {
		t.Errorf("the at-once restarts drove it to a crash loop: %+v", c)
	}
	if !strings.Contains(log.String(), "started again at once (the shared memory a postmaster that was killed left is still in use)") {
		t.Errorf("the log does not say why:\n%s", log.String())
	}
}
