package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A child with its own Stop (postgres on Windows: pg_ctl's fast shutdown) is asked
// through it, not sent a signal, and stops without waiting out the grace.
func TestStopHookAsksTheChild(t *testing.T) {
	p, addr := fakeProc(t, RoleAPI, "serve")
	var asked atomic.Int32
	p.Stop = func() {
		asked.Add(1)
		get(t, "http://"+addr+"/exit") // the child's own graceful exit
	}
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	waitFor(t, s, "the child running", func(snap Snapshot) bool { return proc(snap, RoleAPI).State == StateRunning })
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if asked.Load() == 0 {
		t.Fatal("Stop was not called")
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("stopping took %s: the grace ran out (the child was killed)", took)
	}
}

// The Stop hook takes the place of the signal (CTRL_BREAK on Windows): no
// process is touched (a nil one would panic), and the ask cannot fail.
func TestStopHookTakesPrecedence(t *testing.T) {
	t.Parallel()
	called := make(chan struct{})
	p := Process{Role: RolePostgres, Stop: func() { close(called) }}
	if err := p.askStop(nil, nil); err != nil {
		t.Fatalf("askStop = %v", err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("Stop was not called")
	}
}

// A child that cannot be asked is killed and logged; having no console is said
// once however many children it hits, any other failure each time.
func TestCannotAskLogsNoConsoleOnce(t *testing.T) {
	t.Parallel()
	logs := &syncBuffer{}
	s := New(nil, testPolicy(), WithLog(logs))
	noConsole := fmt.Errorf("%w: the handle is invalid", errNoConsole)
	s.cannotAsk(Process{Role: RoleAPI}, noConsole)
	s.cannotAsk(Process{Role: RoleStreaming}, noConsole)
	s.cannotAsk(Process{Role: RoleTVSlot, Instance: "amber"}, noConsole)
	s.cannotAsk(Process{Role: RolePipeline}, errors.New("CTRL_BREAK: the parameter is incorrect"))
	got := logs.String()
	if n := strings.Count(got, "cannot be asked to stop"); n != 1 || !strings.Contains(got, "api cannot be asked to stop: no console") {
		t.Errorf("no console logged %d times, want once (for api):\n%s", n, got)
	}
	if !strings.Contains(got, "pipeline could not be asked to stop (CTRL_BREAK: the parameter is incorrect); killing it instead") {
		t.Errorf("the other failure is not logged:\n%s", got)
	}
}

// askedStop is a "serve" fake child that takes the ask to stop (FAKE_ON_STOP)
// and writes the marker it returns when it gets it.
func askedStop(t *testing.T, role Role, on string, extraEnv ...string) (Process, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "asked")
	p, _ := fakeProc(t, role, "serve", append([]string{"FAKE_ON_STOP=" + on, "FAKE_MARKER=" + marker}, extraEnv...)...)
	return p, marker
}

func wasAsked(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

// Shutdown asks every child to stop (SIGTERM on POSIX, CTRL_BREAK to its
// process group on Windows): one that drains exits 0 on its own long before the
// grace, and its exit is a shutdown, not a crash.
func TestAskedChildStopsCleanly(t *testing.T) {
	if !ownConsole(t) {
		return
	}
	p, marker := askedStop(t, RoleAPI, "exit")
	logs := &syncBuffer{}
	s := New([]Process{p}, testPolicy(), WithLog(logs))
	const grace = 20 * time.Second
	stop, _ := runningGrace(t, s, grace)
	waitFor(t, s, "running", runningPID(RoleAPI))
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if !wasAsked(marker) {
		t.Fatalf("the child was not asked to stop:\n%s", logs)
	}
	exit := proc(s.Snapshot(), RoleAPI).LastExit
	if exit == nil || exit.Code != 0 || exit.Signal != "" || exit.Reason != ReasonShutdown {
		t.Fatalf("last exit = %+v, want code 0, reason shutdown (it stopped on its own)", exit)
	}
	if took >= grace {
		t.Fatalf("stopping took %s: the grace ran out", took)
	}
	if !strings.Contains(logs.String(), "api stopped (pid") || strings.Contains(logs.String(), "asked to stop") {
		t.Errorf("log:\n%s", logs)
	}
}

// A child that gets the ask and does not stop is killed, with its tree, once
// the grace is over (not before).
func TestChildHoldingTheAskIsKilledAfterTheGrace(t *testing.T) {
	if !ownConsole(t) {
		return
	}
	pidfile := filepath.Join(t.TempDir(), "grandchild.pid")
	p, marker := askedStop(t, RoleStreaming, "hold", "FAKE_GRANDCHILD_PIDFILE="+pidfile)
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	const grace = 2 * time.Second
	stop, _ := runningGrace(t, s, grace)
	waitFor(t, s, "running", runningPID(RoleStreaming))
	gc := readGrandchild(t, pidfile) // it runs, and takes the ask as its parent does
	killGrandchildOnCleanup(t, gc)
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if !wasAsked(marker) {
		t.Fatal("the child was not asked to stop")
	}
	if took < grace {
		t.Fatalf("stopping took %s, less than the %s grace: the child stopped without the kill", took, grace)
	}
	exit := proc(s.Snapshot(), RoleStreaming).LastExit
	if exit == nil || exit.Code == 0 || exit.Reason != ReasonShutdown {
		t.Fatalf("last exit = %+v, want killed (non-zero code or a signal), reason shutdown", exit)
	}
	waitGone(t, gc, "the kill after the grace")
}

// An on-demand child stopped on request (a TV slot the front door no longer
// wants) is asked the same way: it exits 0 on its own, before the kill that
// would follow, and the exit reads "stopped".
func TestUnwantedChildIsAskedToStop(t *testing.T) {
	if !ownConsole(t) {
		return
	}
	p, marker := askedStop(t, RoleTVSlot, "exit")
	p.Instance, p.OnDemand = "amber", true
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	if err := s.Want(RoleTVSlot, "amber", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "running", runningPID(RoleTVSlot))
	start := time.Now()
	if err := s.Want(RoleTVSlot, "amber", false); err != nil {
		t.Fatal(err)
	}
	snap := waitFor(t, s, "stopped on request", func(snap Snapshot) bool {
		c := proc(snap, RoleTVSlot)
		return c.State == StateStopped && c.PID == 0 && c.LastExit != nil
	})
	if took := time.Since(start); took >= 10*time.Second {
		t.Fatalf("stopping took %s: the kill after the ask did it", took)
	}
	if !wasAsked(marker) {
		t.Fatal("the slot was not asked to stop")
	}
	if exit := proc(snap, RoleTVSlot).LastExit; exit.Code != 0 || exit.Reason != ReasonStopped {
		t.Fatalf("last exit = %+v, want code 0, reason stopped", exit)
	}
}

// A child with its own StopGrace (the index worker, whose drain lets a search index
// write finish) is killed only at the end of it; the others at the end of the
// supervisor's grace, not held for the longest.
func TestAChildsOwnStopGrace(t *testing.T) {
	if !ownConsole(t) {
		return
	}
	short, _ := askedStop(t, RoleStreaming, "hold")
	long, _ := askedStop(t, RolePipeline, "hold")
	long.Instance = IndexInstance
	const grace, own = 1 * time.Second, 3 * time.Second
	long.StopGrace = own
	s := New([]Process{short, long}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := runningGrace(t, s, grace)
	waitFor(t, s, "running", func(snap Snapshot) bool {
		return proc(snap, RoleStreaming).State == StateRunning && proc(snap, RolePipeline).State == StateRunning
	})
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	a, b := proc(snap, RoleStreaming).LastExit, proc(snap, RolePipeline).LastExit
	if a == nil || b == nil {
		t.Fatalf("exits: %+v / %+v", a, b)
	}
	if took := a.At.Sub(start); took < grace || took > own-500*time.Millisecond {
		t.Errorf("the child without its own grace was killed after %s, want about %s", took, grace)
	}
	if took := b.At.Sub(start); took < own {
		t.Errorf("the child with StopGrace %s was killed after %s", own, took)
	}
}

func TestGraceFor(t *testing.T) {
	t.Parallel()
	if g := (Process{}).graceFor(30 * time.Second); g != 30*time.Second {
		t.Errorf("no StopGrace: %s", g)
	}
	if g := (Process{StopGrace: 45 * time.Second}).graceFor(30 * time.Second); g != 45*time.Second {
		t.Errorf("a longer StopGrace: %s", g)
	}
	if g := (Process{StopGrace: 5 * time.Second}).graceFor(30 * time.Second); g != 30*time.Second {
		t.Errorf("a shorter StopGrace never shortens the supervisor's: %s", g)
	}
}
