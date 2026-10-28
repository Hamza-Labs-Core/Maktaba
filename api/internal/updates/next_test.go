package updates

import (
	"context"
	"testing"
	"time"
)

// A machine's last update is its newest installed attempt: a rolled-back run's target,
// a failed start or a cancelled run never is, however recent (the home that rolled back
// a run to 0.3.36, then had 0.3.37 installed by hand, last updated to 0.3.37).
func TestLastUpdateIsTheNewestInstalled(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 28, 1, 30, 0, 0, time.UTC)
	byHand := Attempt{ID: "start-0.3.37", At: at, Source: "server", Trigger: "start", From: "0.3.35", To: "0.3.37",
		Outcome: OutcomeInstalled}
	rolledBack := Attempt{ID: "run-b3cc", At: at.Add(-4 * time.Hour), Source: SourceRun, From: "0.3.35", To: "0.3.36",
		Outcome: OutcomeRolledBack, Code: "failed_tv"}
	failed := Attempt{ID: "start-0.3.36", At: at.Add(time.Hour), Source: "server", To: "0.3.38", Outcome: OutcomeFailed}
	cancelled := Attempt{ID: "run-x", At: at.Add(2 * time.Hour), Source: SourceRun, To: "0.3.38", Outcome: OutcomeCancelled}
	got := lastUpdate([]Attempt{cancelled, failed, byHand, rolledBack})
	if got == nil || got.To != "0.3.37" || got.Source != "server" {
		t.Fatalf("last update %+v", got)
	}
	if got := lastUpdate([]Attempt{rolledBack, cancelled}); got != nil {
		t.Fatalf("a rolled-back or cancelled run is no update: %+v", got)
	}
	// an older run that ended healthy, past the attempts the page lists, still counts
	older := Attempt{ID: "run-old", At: at.Add(-72 * time.Hour), Source: SourceRun, To: "0.3.30", Outcome: OutcomeInstalled}
	if got := lastUpdate([]Attempt{rolledBack}, older); got == nil || got.To != "0.3.30" {
		t.Fatalf("with the run's %+v", got)
	}
	if got := lastUpdate(nil); got != nil {
		t.Fatalf("none %+v", got)
	}
}

// The home's last update is the newest run that installed it; a rolled-back run is not.
func TestHomeLastUpdateSkipsARolledBackRun(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.outcome = MRolledBack
	_, _ = r.f.svc.Tick(ctx)
	if run := r.drive(t, 40); run.State != RunRolledBack {
		t.Fatalf("run %s", run.State)
	}
	if a, ok := r.f.store.lastHomeInstall(ctx); ok {
		t.Fatalf("a rolled-back run counted: %+v", a)
	}
	ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.4.0"})
	if ms[0].LastUpdate != nil {
		t.Fatalf("home's last update %+v", ms[0].LastUpdate)
	}

	ok := newDriverRig(t)
	_, _ = ok.f.svc.Tick(ctx)
	run := ok.drive(t, 40)
	ms = ok.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.5.0"})
	if run.State != RunDone || ms[0].LastUpdate == nil || ms[0].LastUpdate.To != "1.5.0" || ms[0].LastUpdate.RunID != run.ID {
		t.Fatalf("run %s home's last update %+v", run.State, ms[0].LastUpdate)
	}
}

// During a run, a machine it has not reached yet says the run brings it after the home
// (not a catch-up, which waits for the run).
func TestMachinesInARunSayTheRunBringsThem(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	_ = r.d.Step(ctx) // the home prepares
	if a, _ := r.f.store.ActiveRun(ctx); a == nil {
		t.Fatal("no run under way")
	}
	ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.4.0"})
	if n := ms[1].Next; n == nil || n.Reason != NextInRun || n.Version != "1.5.0" {
		t.Fatalf("next %+v (state %s)", n, ms[1].State)
	}
}

// A machine behind the cluster's release says what happens next, as the catch-up decides
// it: offline (caught up once back), now (inside the window), the next window (outside
// it, for a machine that keeps TV on air), and the catch-up does exactly that.
func TestMachinesSayWhatHappensNext(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	n1 := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.cluster.offline["n1"] = true
	_, _ = r.f.svc.Tick(ctx)
	if run := r.drive(t, 30); run.State != RunDone {
		t.Fatalf("run %s", run.State)
	}
	next := func() *NextStep {
		t.Helper()
		ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.5.0"})
		if len(ms) != 2 {
			t.Fatalf("machines %+v", ms)
		}
		return ms[1].Next
	}
	if n := next(); n == nil || n.Reason != NextOffline || n.Version != "1.5.0" {
		t.Fatalf("offline: %+v", n)
	}

	// back, outside the window, keeping a stream on air: the next window, 03:00 tomorrow
	r.cluster.offline["n1"] = false
	r.f.now = time.Date(2026, 10, 22, 12, 0, 0, 0, r.f.loc)
	if _, err := r.f.db.Exec(`INSERT INTO channel_runtime (channel_id, viewer_count, host_id, state) VALUES ('c1', 0, 'n1', 'live')`); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 23, 3, 0, 0, 0, r.f.loc)
	if n := next(); n == nil || n.Reason != NextCatchUpWindow || n.At == nil || !n.At.Equal(want) {
		t.Fatalf("window: %+v (want %s)", n, want)
	}
	if err := r.d.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if a, _ := r.f.store.ActiveRun(ctx); a != nil {
		t.Fatalf("a catch-up started outside the window: %+v", a)
	}

	// no stream: catch_up "immediate" takes it now, and the catch-up does
	if _, err := r.f.db.Exec(`DELETE FROM channel_runtime`); err != nil {
		t.Fatal(err)
	}
	if n := next(); n == nil || n.Reason != NextCatchUpNow || n.At != nil {
		t.Fatalf("now: %+v", n)
	}
	cu := r.drive(t, 30)
	if cu.Trigger != TriggerCatchUp || cu.State != RunDone || n1.version != "1.5.0" {
		t.Fatalf("catch-up %+v machine %s", cu, n1.version)
	}
	ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.5.0"})
	if ms[1].Next != nil || ms[1].State != "up_to_date" || ms[1].LastUpdate == nil || ms[1].LastUpdate.To != "1.5.0" {
		t.Fatalf("caught up: %+v", ms[1])
	}
}
