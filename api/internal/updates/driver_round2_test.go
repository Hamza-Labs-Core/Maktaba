package updates

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// oldAgentCluster answers every update.* command for one machine the way a machine
// agent from before automatic updates does.
type oldAgentCluster struct {
	*clusterFake
	old string
}

func (c oldAgentCluster) Command(ctx context.Context, sid, name string, args any, d time.Duration) (bool, json.RawMessage, error) {
	if sid == "s-"+c.old && strings.HasPrefix(name, "update.") {
		return false, json.RawMessage(`"the machine agent does not run ` + name + `"`), nil
	}
	return c.clusterFake.Command(ctx, sid, name, args, d)
}

// review round 2 (A-O): an old machine agent's "does not run update.prepare" is the
// machine's install, not the release: it needs a person and holds nobody.
func TestDriverOldAgentNeedsAPerson(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
	r.d.Cluster = oldAgentCluster{clusterFake: r.cluster, old: "n1"}
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 60)
	ts := targetsByName(t, r.f.store, run.ID)
	if ts["alpha"].State != TargetNeedsPerson || ts["bravo"].State != TargetHealthy {
		t.Fatalf("run %s alpha %s bravo %s", run.State, ts["alpha"].State, ts["bravo"].State)
	}
}

// review round 2: no room on the home is a person's to mend, not an attempt of the
// release (freeing space lets the next window go).
func TestDriverNoRoomCountsNoAttempt(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.prepFail = "disk_space"
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 10)
	if run.State != RunFailed || r.f.notes.find(NoticeNeedsPerson) == nil {
		t.Fatalf("run %s notices %v", run.State, r.f.notes.kinds())
	}
	var attempts int
	_ = r.f.db.QueryRow(`SELECT attempts FROM release_catalog WHERE version = '1.5.0'`).Scan(&attempts)
	if attempts != 0 {
		t.Fatalf("attempts %d", attempts)
	}
}

// review round 2 (A-W): a release withdrawn upstream while the home's app waits for its
// click is found when the run looks again before the go: the run ends, nothing installs.
func TestDriverRechecksTheReleaseBeforeTheGo(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks = true
	var mu sync.Mutex
	gone, asked := false, 0
	r.d.Recheck = func(ctx context.Context, rel Release, _ string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		if !gone {
			return "", nil
		}
		return WithdrawnGone, r.f.store.SetWithdrawn(ctx, rel.Version, WithdrawnGone)
	}
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(8)
	run := r.active(t)
	if r.home.state() != MAwaitingClick {
		t.Fatalf("home %s", r.home.state())
	}
	mu.Lock()
	gone = true
	mu.Unlock()
	r.home.click()
	r.stepN(10) // the click drains, then the go is due: the release is looked at again first
	got, _ := r.f.store.GetRun(ctx, run.ID)
	if got.State != RunCancelled || !strings.Contains(string(got.Error), "withdrawn") || r.home.state() != MAborted {
		t.Fatalf("run %s %s home %s (asked %d)", got.State, got.Error, r.home.state(), asked)
	}
}

// review round 2 (A-N1): a run that only waits for a click does not hold a switch of the
// home: once one starts, the order is withdrawn and the run ends.
func TestDriverWaitingRunEndsForASwitchover(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks = true
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(8)
	run := r.active(t)
	if run.Step != StepWaitingClick {
		t.Fatalf("run %s/%s", run.State, run.Step)
	}
	if _, err := r.f.db.Exec(`INSERT INTO cluster_switchovers (id, state) VALUES ('s1', 'fencing')`); err != nil {
		t.Fatal(err)
	}
	_ = r.d.Step(ctx)
	got, _ := r.f.store.GetRun(ctx, run.ID)
	if got.State != RunCancelled || !strings.Contains(string(got.Error), "switchover") || r.home.state() != MAborted {
		t.Fatalf("run %s %s home %s", got.State, got.Error, r.home.state())
	}
}

// review round 2 (A-N2): a home that commits after its run gave up on it (home_timeout)
// raises the watermark, so the machines catch up to its release.
func TestDriverHomeCommittedAfterTheCeiling(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.home.outcome = "silent"
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(8)
	run := r.active(t)
	r.f.now = r.f.now.Add(homeCeiling + time.Minute)
	_ = r.d.Step(ctx)
	if got, _ := r.f.store.GetRun(ctx, run.ID); got.State != RunFailed {
		t.Fatalf("run %s", got.State)
	}
	if wm, _ := r.f.store.Watermark(ctx); wm == "1.5.0" {
		t.Fatal("the watermark rose before the home committed")
	}
	r.home.mu.Lock()
	r.home.st.State = MCommitted
	r.home.version = "1.5.0"
	r.home.mu.Unlock()
	r.f.now = r.f.now.Add(2 * time.Minute)
	_ = r.d.Step(ctx)
	if wm, _ := r.f.store.Watermark(ctx); wm != "1.5.0" {
		t.Fatalf("watermark %q after the home committed late", wm)
	}
}

// review round 2: a one-machine Retry touches only that machine, not every machine
// behind (outside the window too).
func TestDriverRetryOfOneMachineTouchesOnlyIt(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	b := r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
	if err := r.f.store.RaiseWatermark(ctx, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	run, err := r.f.store.InsertRunWith(ctx, "1.5.0", "1.5.0", TriggerRetry, "admin", RunDetail{NoHome: true, Targets: []string{"n1"}})
	if err != nil {
		t.Fatal(err)
	}
	got := r.drive(t, 40)
	if got.ID != run.ID {
		got, _ = r.f.store.GetRun(ctx, run.ID)
	}
	ts := targetsByName(t, r.f.store, run.ID)
	if ts["alpha"].State != TargetHealthy {
		t.Fatalf("run %s alpha %s", got.State, ts["alpha"].State)
	}
	if _, ok := ts["bravo"]; ok || b.version != "1.4.0" {
		t.Fatalf("bravo was taken in: %+v on %s", ts["bravo"], b.version)
	}
}

// Review round 3: the driver never gives up on a home still within its own limits (a
// large database's start steps can take longer than homeCeiling).
func TestHomeCeilingCoversTheHomesOwnBound(t *testing.T) {
	if got := homeCeilingFor(nil); got != homeCeiling {
		t.Fatalf("no report: %s", got)
	}
	if got := homeCeilingFor(&MachineStatus{BoundSec: 3600}); got != homeCeiling {
		t.Fatalf("a small bound: %s", got)
	}
	twelve := 12 * time.Hour
	if got := homeCeilingFor(&MachineStatus{BoundSec: int(twelve.Seconds())}); got != twelve+time.Hour {
		t.Fatalf("a large home's bound: %s", got)
	}
}
