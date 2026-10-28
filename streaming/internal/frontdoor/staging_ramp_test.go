package frontdoor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/verify"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
)

// TV change #55 (2026-10-28): a binary change staged on a quiet home changed all 28
// channels, the budget was the whole host (28), and the staging slot started all 28 at
// once. Their output ran at 0.2-0.7x real time and every station fell further behind the
// wall clock (channel.position -38..-48 s, channel.wallclock -40..-50 s at 80 s; -80 s
// at 100 s on a later run), never settling. The rolling probes now start in a small
// window that grows only while the host keeps up and shrinks when it does not.

// rampRig is the staging rig with n changed channels and a budget of n.
func rampRig(t *testing.T, n int) (*slotRig, *fakeSlot, *stagingRun, []uuid.UUID) {
	t.Helper()
	g, amber, _, _ := stagingRig(t)
	// the rig's live slot airs one channel: what it leaves is n
	g.slots.Budget = func(context.Context) (int, int, int) { return n + 1, 0, 0 }
	ids := make([]uuid.UUID, n)
	changed := make([]string, n)
	for i := range ids {
		ids[i] = uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1))
		changed[i] = ids[i].String()
	}
	g.slots.mu.Lock()
	run := g.slots.stage
	run.plan.Probe = ids[0].String()
	run.plan.ProbeSeconds = 30
	run.plan.Changed = changed
	run.plan.Channels = changed
	g.slots.mu.Unlock()
	return g, amber, run, ids
}

func rollingWarm(g *slotRig) int {
	n := 0
	for _, w := range g.slots.StagingView().Warm {
		if w.Why == WhyRolling {
			n++
		}
	}
	return n
}

// setPace gives every warm staging station the same measured pace.
func setPace(g *slotRig, run *stagingRun, rate float64, behind time.Duration) {
	g.slots.mu.Lock()
	defer g.slots.mu.Unlock()
	run.pace = map[uuid.UUID]*stationPace{}
	for _, w := range run.warm {
		id := uuid.MustParse(w.Channel)
		run.pace[id] = &stationPace{markAt: time.Now(), markReached: time.Now(), rate: rate, measured: true, behind: behind}
	}
}

func TestStagingWarmRampsWithTheHost(t *testing.T) {
	g, _, run, _ := rampRig(t, 28)
	ctx := context.Background()
	g.slots.SyncWarm(ctx)
	v := g.slots.StagingView()
	if v.Budget != 28 {
		t.Fatalf("budget %d, want 28", v.Budget)
	}
	// the probe channel and rampStart rolling probes, not all 28 at once
	if got := rollingWarm(g); got != rampStart {
		t.Fatalf("%d rolling probes warm at the start (budget %d), want %d", got, v.Budget, rampStart)
	}
	if len(v.Warm) != rampStart+1 || len(v.Deferred) != 28-rampStart-1 {
		t.Fatalf("warm %d deferred %d", len(v.Warm), len(v.Deferred))
	}

	// every station measured and on the wall clock: the window doubles
	setPace(g, run, 1.0, 300*time.Millisecond)
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != 2*rampStart {
		t.Fatalf("on the clock: %d rolling probes, want %d", got, 2*rampStart)
	}
	// the new ones are not measured yet: no growth until they are
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != 2*rampStart {
		t.Fatalf("unmeasured stations: %d rolling probes, want %d", got, 2*rampStart)
	}
	// still catching up (behind, at 2x): no growth either
	setPace(g, run, 2.0, 6*time.Second)
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != 2*rampStart {
		t.Fatalf("catching up: %d rolling probes, want %d", got, 2*rampStart)
	}
	setPace(g, run, 1.0, 0)
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != 4*rampStart {
		t.Fatalf("on the clock again: %d rolling probes, want %d", got, 4*rampStart)
	}

	// the host cannot keep 16 up (output at 0.6x): the window halves at once, keeping
	// the oldest probes, and never reaches 16 again
	setPace(g, run, 0.6, 3*time.Second)
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != 2*rampStart {
		t.Fatalf("overloaded: %d rolling probes, want %d", got, 2*rampStart)
	}
	g.slots.mu.Lock()
	ceiling, paces := run.ceiling, len(run.pace)
	g.slots.mu.Unlock()
	if ceiling != 4*rampStart {
		t.Fatalf("ceiling %d, want %d", ceiling, 4*rampStart)
	}
	if paces != 0 && paces != 2*rampStart+1 {
		t.Fatalf("%d paces kept", paces)
	}
	for _, w := range g.slots.StagingView().Warm {
		if w.Why == WhyRolling && w.Channel > fmt.Sprintf("00000000-0000-4000-8000-%012d", 2*rampStart+1) {
			t.Fatalf("a newer probe %s kept over an older one", w.Channel)
		}
	}
	setPace(g, run, 1.0, 0)
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != 4*rampStart-1 {
		t.Fatalf("after an overload: %d rolling probes, want %d (below the size that overloaded)", got, 4*rampStart-1)
	}
}

// A release that cannot keep up even alone is still measured, one channel at a time:
// the window never goes below one, and the API judges the station as before.
func TestStagingWindowFloorIsOne(t *testing.T) {
	w, c, shrunk := rampWindow(1, 0, 1, 28, true, []stationPace{{measured: true, rate: 0.5, behind: 20 * time.Second}})
	if w != 1 || c != 1 || !shrunk {
		t.Fatalf("window %d ceiling %d shrunk %v", w, c, shrunk)
	}
	if w, _, _ = rampWindow(1, 1, 1, 28, true, []stationPace{{measured: true, rate: 1}}); w != 1 {
		t.Fatalf("grew past the ceiling: %d", w)
	}
	// nothing waits: no growth; a budget below the start: the budget
	if w, _, _ = rampWindow(4, 0, 4, 28, false, []stationPace{{measured: true, rate: 1}}); w != 4 {
		t.Fatalf("grew with nothing waiting: %d", w)
	}
	if w, _, _ = rampWindow(0, 0, 0, 2, true, nil); w != 2 {
		t.Fatalf("first window with budget 2: %d", w)
	}
}

// The pace is measured from the staging slot's statuses: output position against the
// status's time, over paceSpan.
func TestStationPaceFromStatuses(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 15, 39, 30, 0, time.UTC)
	var p stationPace
	p.observe(time.Time{}, t0) // no output yet
	if !p.markAt.IsZero() {
		t.Fatal("a station without output was marked")
	}
	// TV change #55's stations: the output 16 s behind, then 0.3 s of output a second
	p.observe(t0.Add(-16*time.Second), t0)
	p.observe(t0.Add(-16*time.Second+600*time.Millisecond), t0.Add(2*time.Second))
	if p.measured {
		t.Fatal("measured over less than paceSpan")
	}
	p.observe(t0.Add(-16*time.Second+1200*time.Millisecond), t0.Add(4*time.Second))
	if !p.measured || p.rate < 0.29 || p.rate > 0.31 || p.behind != 18800*time.Millisecond {
		t.Fatalf("pace %+v", p)
	}
	if w, _, shrunk := rampWindow(28, 0, 28, 28, true, []stationPace{p}); !shrunk || w != 14 {
		t.Fatalf("28 stations at 0.3x: window %d shrunk %v", w, shrunk)
	}
}

// The slot's status feeds the pace through SyncWarm.
func TestStagingPaceFromTheSlot(t *testing.T) {
	g, amber, run, ids := rampRig(t, 8)
	ctx := context.Background()
	g.slots.SyncWarm(ctx)
	now := time.Now()
	amber.mu.Lock()
	for _, id := range ids[:rampStart+1] {
		r := now.Add(-20 * time.Second)
		amber.chans = append(amber.chans, slot.StationInfo{ID: id.String(), Reached: &r})
	}
	amber.mu.Unlock()
	g.slots.probeAll(ctx)
	g.slots.SyncWarm(ctx)
	g.slots.mu.Lock()
	defer g.slots.mu.Unlock()
	if len(run.pace) != rampStart+1 {
		t.Fatalf("%d paces", len(run.pace))
	}
	for id, p := range run.pace {
		if p.markAt.IsZero() || p.behind < 19*time.Second {
			t.Fatalf("pace of %s: %+v", id, p)
		}
	}
}

// samplingWhile runs one staging sample pass and, once every warm station's live.ts
// read is open, calls during; it returns when the pass ends.
func samplingWhile(t *testing.T, g *slotRig, amber *fakeSlot, during func()) {
	t.Helper()
	g.slots.StreamSample = 800 * time.Millisecond
	n := int32(len(g.slots.StagingView().Warm))
	done := make(chan struct{})
	go func() { g.slots.sampleStaging(context.Background()); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for amber.reads.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d live.ts reads open", amber.reads.Load(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	during()
	<-done
}

// TV change #56 (2026-10-28): the window halved from 14 to 6 (overloaded) and stopped
// a station under its live.ts read; the broken read failed ts.conformity. A window that
// shrinks now keeps the stations being read until their reads end, and drops them after.
func TestStagingShrinkKeepsTheStationsBeingRead(t *testing.T) {
	g, amber, run, _ := rampRig(t, 8)
	ctx := context.Background()
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != rampStart {
		t.Fatalf("%d rolling probes", got)
	}
	samplingWhile(t, g, amber, func() {
		setPace(g, run, 0.6, 3*time.Second)
		g.slots.SyncWarm(ctx)
		if got := rollingWarm(g); got != rampStart {
			t.Errorf("a shrink during the reads kept %d of the %d rolling probes being read", got, rampStart)
		}
		draining := 0
		for _, w := range g.slots.StagingView().Warm {
			if w.Draining {
				draining++
			}
		}
		if draining != rampStart/2 {
			t.Errorf("%d draining, want %d", draining, rampStart/2)
		}
	})
	// the reads ended whole: scored as they are, not retried
	for id, cp := range g.slots.StagingView().Samples {
		for _, r := range cp.Results {
			if strings.HasPrefix(r.Value, "retry") {
				t.Errorf("%s: a whole read marked retry: %+v", id, r)
			}
		}
	}
	// and the window takes effect once they are done
	g.slots.SyncWarm(ctx)
	if got := rollingWarm(g); got != rampStart/2 {
		t.Errorf("after the reads: %d rolling probes, want %d", got, rampStart/2)
	}
}

// A station the budget must stop under a read: the sample is retried, never scored.
func TestStagingStopUnderAReadIsRetried(t *testing.T) {
	g, amber, run, ids := rampRig(t, 4)
	ctx := context.Background()
	g.slots.SyncWarm(ctx)
	samplingWhile(t, g, amber, func() {
		g.slots.Budget = func(context.Context) (int, int, int) { return 2, 0, 0 } // room for the probe channel only
		g.slots.SyncWarm(ctx)
		if got := rollingWarm(g); got != 0 {
			t.Errorf("%d rolling probes past the budget", got)
		}
	})
	v := g.slots.StagingView()
	cp := v.Samples[ids[1].String()]
	if cp == nil {
		t.Fatal("no sample")
	}
	retried := false
	for _, r := range cp.Results {
		if r.Result == verify.Fail || r.Result == verify.Warn {
			t.Errorf("a read cut short by a stop scored: %+v", r)
		}
		if r.Result == verify.Pending && strings.HasPrefix(r.Value, "retry") {
			retried = true
		}
	}
	if !retried {
		t.Errorf("not marked retry: %+v", cp.Results)
	}
	g.slots.mu.Lock()
	probed := run.probed[ids[1]]
	g.slots.mu.Unlock()
	if probed {
		t.Error("a retried probe counted as done")
	}
	// the probe channel stayed warm through it: its read is scored as it is
	for _, r := range v.Samples[ids[0].String()].Results {
		if strings.HasPrefix(r.Value, "retry") {
			t.Errorf("the probe channel's whole read marked retry: %+v", r)
		}
	}
}
