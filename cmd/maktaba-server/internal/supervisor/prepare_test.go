package supervisor

import (
	"context"
	"errors"
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
