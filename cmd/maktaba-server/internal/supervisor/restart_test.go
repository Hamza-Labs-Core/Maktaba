package supervisor

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// The Windows postgres pre-start settle and at-once retry loop share ONE 2-minute deadline:
// time spent inside Settle counts against the retry budget, so a settle that already spent
// the bound does not get another full bound on the first failed start; becoming ready resets
// the window for future crashes.
func TestShmRetryGateSharesOneDeadlineAcrossSettleAndRetry(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	cur := t0
	held := true
	var gotDeadline time.Time
	g := newShmRetryGate(
		func(ctx context.Context) {
			d, ok := ctx.Deadline()
			if !ok {
				t.Error("settle context had no deadline")
			}
			gotDeadline = d
			// Simulate AwaitShmFree spending 90s of the 2-minute bound.
			cur = cur.Add(90 * time.Second)
		},
		func() bool { return held },
		func() time.Duration { return 2 * time.Minute },
	)
	g.now = func() time.Time { return cur }

	g.settle(context.Background())
	if !gotDeadline.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("settle deadline = %v, want %v", gotDeadline, t0.Add(2*time.Minute))
	}
	// At t0+90s, 30s remain in the shared window: retry is allowed.
	if why := g.retry(); why == "" {
		t.Fatal("expected at-once retry while inside shared 2m window")
	}
	// At t0+121s (only 31s after settle returned, NOT a fresh 2m), the shared deadline passed.
	cur = t0.Add(121 * time.Second)
	if why := g.retry(); why != "" {
		t.Fatalf("retry allowed past shared deadline: %q", why)
	}
	// Once postgres becomes ready, the gate resets for a later crash.
	g.reset()
	cur = t0.Add(time.Hour)
	g.settle(context.Background())
	if !gotDeadline.Equal(t0.Add(time.Hour + 2*time.Minute)) {
		t.Fatalf("after reset settle deadline = %v, want %v", gotDeadline, t0.Add(time.Hour+2*time.Minute))
	}
	if why := g.retry(); why == "" {
		t.Fatal("expected at-once retry in fresh window after reset")
	}
	// When shm is no longer held, retry returns "" and resets the window.
	held = false
	if why := g.retry(); why != "" {
		t.Fatalf("shm free: %q", why)
	}
}
