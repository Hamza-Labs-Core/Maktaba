package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
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
	p.Prepare = func(ctx context.Context, report func(Detail)) error {
		if tries.Add(1) == 1 {
			return Coded(Detail{Code: "venv_failed", Params: map[string]any{"error": "no network"}, Text: "no network"})
		}
		report(Detail{Code: "venv_building", Params: map[string]any{"percent": 41},
			Text: "Installing the transcription tools (41%)"})
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
		return c.State == StateFailed && c.Detail == "no network" && c.DetailCode == "venv_failed" &&
			c.DetailParams["error"] == "no network" && c.NextRestartAt != nil
	})
	waitFor(t, s, "the retry's progress shown", func(s Snapshot) bool {
		c := proc(s, RolePipeline)
		return c.State == StateStarting && c.Detail == "Installing the transcription tools (41%)" &&
			c.DetailCode == "venv_building" && c.DetailParams["percent"] == 41 && c.PID == 0
	})
	close(release)
	waitFor(t, s, "started once prepared", runningPID(RolePipeline))
	if c := proc(s.Snapshot(), RolePipeline); c.Detail != "" || c.DetailCode != "" || c.DetailParams != nil {
		t.Errorf("detail after the start = %q %q %v", c.Detail, c.DetailCode, c.DetailParams)
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
	p.Prepare = func(ctx context.Context, report func(Detail)) error {
		report(Detail{Text: "building"})
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
		Prepare: func(context.Context, func(Detail)) error {
			tries.Add(1)
			return Halt(Coded(Detail{Code: "db_restored", Params: map[string]any{"release": "v0.2.0"},
				Text: "the update's migrations failed"}))
		}}
	err := halted.Prepare(context.Background(), nil)
	if !Halted(err) || Halted(errors.New("x")) || Halt(nil) != nil {
		t.Fatal("Halted does not tell a Halt")
	}
	if d := DetailOf(err); d.Code != "db_restored" || d.Text != "the update's migrations failed" {
		t.Fatalf("a halt's detail = %+v", d)
	}
	if d := DetailOf(errors.New("plain")); d.Code != "" || d.Text != "plain" {
		t.Fatalf("an uncoded error's detail = %+v", d)
	}
	tries.Store(0)
	s := New([]Process{halted}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	snap := waitFor(t, s, "the halt shown", func(s Snapshot) bool {
		c := proc(s, RoleStreaming)
		return c.State == StateFailed && c.Detail == "the update's migrations failed" && c.DetailCode == "db_restored" &&
			c.DetailParams["release"] == "v0.2.0"
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

// A HaltUntil (the database step's halt, 2026-10-28) has a way out: the process stays
// failed with its reason while what it watches is unchanged, then its Prepare runs again
// and the process starts, with no restart of the server.
func TestHaltUntilStartsOnceItsWayOutCame(t *testing.T) {
	prev := haltPoll
	haltPoll = 10 * time.Millisecond
	t.Cleanup(func() { haltPoll = prev })
	var fixed atomic.Bool
	var tries atomic.Int32
	p, _ := fakeProc(t, RoleAPI, "serve")
	p.Prepare = func(context.Context, func(Detail)) error {
		tries.Add(1)
		if !fixed.Load() {
			return HaltUntil(Coded(Detail{Code: "db_restore_failed", Text: "putting the database back failed"}), fixed.Load)
		}
		return nil
	}
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	waitFor(t, s, "the halt shown", func(s Snapshot) bool {
		c := proc(s, RoleAPI)
		return c.State == StateFailed && c.DetailCode == "db_restore_failed" && c.NextRestartAt == nil
	})
	time.Sleep(50 * time.Millisecond)
	if n := tries.Load(); n != 1 {
		t.Fatalf("Prepare ran %d times before its way out, want 1", n)
	}
	fixed.Store(true) // the record put right
	waitFor(t, s, "started once its way out came", runningPID(RoleAPI))
	if n := tries.Load(); n != 2 {
		t.Fatalf("Prepare ran %d times, want 2", n)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

// The coded detail is in /processes beside the English, and absent when there is none.
func TestDetailFieldsInJSON(t *testing.T) {
	raw, err := json.Marshal(ChildStatus{Role: RoleAPI, State: StateStarting, Detail: "Saving the library database before the update to v0.3.0",
		DetailCode: "db_dumping", DetailParams: map[string]any{"from": "v0.2.0", "to": "v0.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"detail_code":"db_dumping","detail_params":{"from":"v0.2.0","to":"v0.3.0"}`) {
		t.Fatalf("json = %s", raw)
	}
	raw, _ = json.Marshal(ChildStatus{Role: RoleAPI, State: StateRunning})
	if strings.Contains(string(raw), "detail") {
		t.Fatalf("json without a detail = %s", raw)
	}
}
