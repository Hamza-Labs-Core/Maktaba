package supervisor

import (
	"strings"
	"testing"
	"time"
)

// TestAJobThatEnds: a job's clean exit leaves it exited (not restarted, not wanted:
// nothing is down), whatever the restart policy.
func TestAJobThatEnds(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "exit0")
	p.Instance, p.Once, p.Tries = "job", true, 3
	p.Progress = func() Detail { return Detail{Text: "copying"} }
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "exited", func(s Snapshot) bool { return proc(s, RolePipeline).State == StateExited })
	time.Sleep(300 * time.Millisecond) // past the policy's backoff: still not restarted
	c := proc(s.Snapshot(), RolePipeline)
	if c.Restarts != 0 || c.Wanted || c.Detail != "" {
		t.Errorf("after the job: %+v", c)
	}
	if n := countEvents(snap, RolePipeline, EventStarted); n != 1 {
		t.Errorf("started %d times", n)
	}
}

// TestAJobThatKeepsFailing: a failing job is started again until its Tries, then left
// failed with an event that says so; the stack keeps running.
func TestAJobThatKeepsFailing(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "crash", "FAKE_CODE=1")
	p.Instance, p.Once, p.Tries = "job", true, 3
	pol := testPolicy()
	pol.Restart = RestartNever // not the stack's end: a job's failure is its own
	log := &syncBuffer{}
	s := New([]Process{p}, pol, WithLog(log))
	running(t, s)
	snap := waitFor(t, s, "failed", func(s Snapshot) bool { return proc(s, RolePipeline).State == StateFailed })
	time.Sleep(300 * time.Millisecond)
	c := proc(s.Snapshot(), RolePipeline)
	if c.Restarts != 2 || c.NextRestartAt != nil || !c.CrashLoop {
		t.Errorf("after 3 failures: %+v", c)
	}
	if n := countEvents(snap, RolePipeline, EventFailed); n != 1 {
		t.Errorf("%d failed events", n)
	}
	if !strings.Contains(log.String(), "gave up: 3 failed runs in a row") {
		t.Errorf("log: %s", log.String())
	}
}
