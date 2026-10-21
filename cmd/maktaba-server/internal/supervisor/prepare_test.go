package supervisor

import (
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// Epic 38 story 38.12: a process with a Prepare (the app's pipeline waiting for
// its venv) starts only once it succeeded; meanwhile /processes shows what it
// waits on, and a failure shows as failed with its reason until the next try.
func TestPrepareRunsBeforeTheFirstStart(t *testing.T) {
	prev := prepareRetryFirst
	prepareRetryFirst = 50 * time.Millisecond
	t.Cleanup(func() { prepareRetryFirst = prev })

	p, _ := fakeProc(t, RolePipeline, "serve")
	var tries atomic.Int32
	release := make(chan struct{})
	p.Prepare = func(ctx context.Context, report func(string)) error {
		if tries.Add(1) == 1 {
			return errors.New("no network")
		}
		report("Installing the transcription tools (41%)")
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)

	waitFor(t, s, "the failure shown", func(s Snapshot) bool {
		c := proc(s, RolePipeline)
		return c.State == StateFailed && c.Detail == "no network" && c.NextRestartAt != nil
	})
	waitFor(t, s, "the retry's progress shown", func(s Snapshot) bool {
		c := proc(s, RolePipeline)
		return c.State == StateStarting && c.Detail == "Installing the transcription tools (41%)" && c.PID == 0
	})
	close(release)
	waitFor(t, s, "started once prepared", runningPID(RolePipeline))
	if c := proc(s.Snapshot(), RolePipeline); c.Detail != "" {
		t.Errorf("detail after the start = %q", c.Detail)
	}
	if !hasEvent(s.Snapshot(), RolePipeline, EventFailed) {
		t.Error("the failed Prepare is in the event log")
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if n := tries.Load(); n != 2 {
		t.Fatalf("Prepare ran %d times, want 2 (not again at a restart)", n)
	}
}

// A shutdown while a Prepare runs (a long venv build) ends it and stops cleanly;
// the process never starts, and the others are not held up by it.
func TestShutdownDuringPrepare(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "serve")
	p.Prepare = func(ctx context.Context, report func(string)) error {
		report("building")
		<-ctx.Done()
		return ctx.Err()
	}
	api, _ := fakeProc(t, RoleAPI, "serve")
	s := New([]Process{p, api}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	waitFor(t, s, "the api runs while the pipeline prepares", runningPID(RoleAPI))
	if c := proc(s.Snapshot(), RolePipeline); c.Detail != "building" || c.PID != 0 {
		t.Fatalf("pipeline = %+v", c)
	}
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the stop waited on the Prepare")
	}
	if c := proc(s.Snapshot(), RolePipeline); c.State != StateStopped || c.LastExit != nil {
		t.Fatalf("pipeline = %+v, want stopped and never started", c)
	}
}

// A Halt (an update that cannot go on in this run) leaves the process failed
// with its reason and no next try: the Prepare is not run again, the others
// run, and a stop ends it as stopped.
func TestHaltedPrepareIsNotTriedAgain(t *testing.T) {
	prev := prepareRetryFirst
	prepareRetryFirst = 10 * time.Millisecond // a retry would come at once
	t.Cleanup(func() { prepareRetryFirst = prev })
	var tries atomic.Int32
	halted := Process{Role: RoleStreaming, Cmd: exec.Command("maktaba-never-started"),
		Prepare: func(context.Context, func(string)) error {
			tries.Add(1)
			return Halt(errors.New("the update's migrations failed"))
		}}
	if !Halted(halted.Prepare(context.Background(), nil)) || Halted(errors.New("x")) || Halt(nil) != nil {
		t.Fatal("Halted does not tell a Halt")
	}
	tries.Store(0)
	s := New([]Process{halted}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	snap := waitFor(t, s, "the halt shown", func(s Snapshot) bool {
		c := proc(s, RoleStreaming)
		return c.State == StateFailed && c.Detail == "the update's migrations failed"
	})
	if c := proc(snap, RoleStreaming); c.NextRestartAt != nil || c.PID != 0 {
		t.Fatalf("halted = %+v, want no next try and never started", c)
	}
	if !hasEvent(snap, RoleStreaming, EventFailed) {
		t.Error("the halt is in the event log")
	}
	time.Sleep(100 * time.Millisecond)
	if n := tries.Load(); n != 1 {
		t.Fatalf("Prepare ran %d times, want 1", n)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if c := proc(s.Snapshot(), RoleStreaming); c.State != StateStopped {
		t.Fatalf("after the stop = %+v", c)
	}
}
