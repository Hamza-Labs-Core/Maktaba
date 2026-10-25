package updates

import (
	"context"
	"sync"
	"testing"
	"time"
)

type eventLog struct {
	mu   sync.Mutex
	recs []map[string]any
}

func (l *eventLog) add(f map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, f)
}

func (l *eventLog) named(name string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, r := range l.recs {
		if r["event"] == name {
			out = append(out, r)
		}
	}
	return out
}

// TC2: the events a run makes, as the driver's ticks see it, each carry run_id, version
// and host_id; the first look after a start says nothing of what was there already.
func TestUpdateEventsCarryRunID(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	log := &eventLog{}
	r.d.Events = log.add
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	seed(t, r.f.store, rel("1.4.9"))
	if err := r.f.store.SetVerifyError(ctx, "1.4.9", &VerifyError{Kind: "checksum"}, ""); err != nil {
		t.Fatal(err)
	}
	r.d.observe(ctx) // the start: learns what is there
	if len(log.recs) != 0 {
		t.Fatalf("the first look said %v", log.recs)
	}
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	for i := 0; i < 60; i++ {
		r.d.observe(ctx)
		if err := r.d.Step(ctx); err != nil {
			t.Logf("step: %v", err)
		}
		r.f.now = r.f.now.Add(time.Minute)
		if a, _ := r.f.store.ActiveRun(ctx); a == nil {
			break
		}
	}
	r.d.observe(ctx)
	seed(t, r.f.store, rel("1.6.0"))
	if err := r.f.store.SetVerifyError(ctx, "1.6.0", &VerifyError{Kind: "signature"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.f.store.SetWithdrawn(ctx, "1.6.0", "deleted upstream"); err != nil {
		t.Fatal(err)
	}
	r.f.now = r.f.now.Add(catalogLookEvery)
	r.d.observe(ctx)

	for _, name := range []string{EventPlanned, EventStepStarted, EventStepFinished, EventHomeCommitted,
		EventNodePrepared, EventNodeApplied, EventVerifyFailed, EventWithdrawn} {
		got := log.named(name)
		if len(got) == 0 {
			t.Fatalf("no %s in %v", name, log.recs)
		}
		for _, e := range got {
			if e["version"] == "" || e["version"] == nil || e["host_id"] == "" || e["host_id"] == nil {
				t.Fatalf("%s without version or host_id: %v", name, e)
			}
			if name != EventVerifyFailed && name != EventWithdrawn && (e["run_id"] == "" || e["run_id"] == nil) {
				t.Fatalf("%s without run_id: %v", name, e)
			}
		}
	}
	if got := log.named(EventNodeApplied); got[0]["host_id"] != "n1" {
		t.Fatalf("a machine's event names it: %v", got[0])
	}
	if got := log.named(EventVerifyFailed); len(got) != 1 || got[0]["version"] != "1.6.0" {
		t.Fatalf("only the new verification failure is said: %v", got)
	}
	if len(log.named(EventPlanned)) != 1 || len(log.named(EventHomeCommitted)) != 1 {
		t.Fatalf("an event said twice: %v", log.recs)
	}
}

// A release the launcher's guard took back (it did not stay running) says
// launcher_rollback beside update_rolled_back.
func TestLauncherRollbackEvent(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	log := &eventLog{}
	r.d.Events = log.add
	r.d.observe(ctx)
	run, err := r.f.store.InsertRun(ctx, "1.5.0", "1.4.0", TriggerAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	r.d.observe(ctx)
	if err := r.f.store.FinishRun(ctx, run.ID, RunRolledBack, map[string]any{"code": "did_not_stay_up"}); err != nil {
		t.Fatal(err)
	}
	r.d.observe(ctx)
	if len(log.named(EventRolledBack)) != 1 || len(log.named(EventLauncherRolled)) != 1 {
		t.Fatalf("events %v", log.recs)
	}
	if e := log.named(EventLauncherRolled)[0]; e["host_id"] != HomeHost || e["run_id"] != run.ID {
		t.Fatalf("launcher_rollback %v", e)
	}
}

// The last tick before an API restart: what the previous process's last Step wrote after
// its last look (a machine prepared, a step done) is said by the first look of the next
// process, once; what the previous process said is not said again.
func TestEventsMissedAcrossARestartAreSaidOnce(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	before := &eventLog{}
	r.d.Events = before.add
	r.d.observe(ctx) // a first start ever: nothing to say, the mark is set
	run, err := r.f.store.InsertRun(ctx, "1.5.0", "1.4.0", TriggerAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	r.f.now = r.f.now.Add(time.Minute)
	r.d.observe(ctx)
	if len(before.named(EventPlanned)) != 1 {
		t.Fatalf("planned: %v", before.recs)
	}
	// the last Step before the restart: the home's preflight done after the last look
	r.f.now = r.f.now.Add(time.Minute)
	started, fin := r.f.now.Add(-30*time.Second), r.f.now
	if err := r.f.store.ReplaceSteps(ctx, run.ID, HomeHost, stepSeqBase(0, true), []MachineStep{
		{Step: "preflight", State: "done", StartedAt: started, FinishedAt: &fin}}); err != nil {
		t.Fatal(err)
	}
	// the API restarts: a new driver over the same database
	after := &eventLog{}
	r.f.now = r.f.now.Add(time.Minute)
	d2 := &Driver{Store: r.d.Store, Svc: r.d.Svc, Now: r.d.Now, Events: after.add, Getenv: r.d.Getenv}
	d2.observe(ctx)
	if len(after.named(EventStepStarted)) != 1 || len(after.named(EventStepFinished)) != 1 {
		t.Fatalf("what the last tick wrote was not said after the restart: %v", after.recs)
	}
	if len(after.named(EventPlanned)) != 0 {
		t.Fatalf("an event said before the restart was said again: %v", after.recs)
	}
	d2.observe(ctx)
	d3 := &Driver{Store: r.d.Store, Svc: r.d.Svc, Now: r.d.Now, Events: after.add, Getenv: r.d.Getenv}
	d3.observe(ctx)
	if len(after.named(EventStepFinished)) != 1 {
		t.Fatalf("said twice: %v", after.recs)
	}
}
