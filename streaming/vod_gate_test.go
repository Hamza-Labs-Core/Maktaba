package main

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// fakeSlots is the TV slots as the front door sees them: each Sync the slots
// take the budget slotBudget gives them, and report what they hold, at most
// that budget (their gates cap new blocks at it; held ones stay).
type fakeSlots struct {
	mu                    sync.Mutex
	want                  int // sessions the slots' channels would hold
	used, nvencMax, vtMax int
	budget                int // the last NVENC budget sent
	syncs                 int
	syncFails             bool
	gate                  *vodGate // whose holds the budget leaves out
}

func (f *fakeSlots) Sessions(context.Context) (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.used, f.nvencMax, f.vtMax
}

func (f *fakeSlots) Sync(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	if f.syncFails {
		return false
	}
	f.budget = f.nvencMax
	if f.budget > 0 {
		f.budget -= f.gate.Held()
	}
	// what the channels hold stays (a gate keeps sessions held over a lower cap)
	f.used = f.want
	return true
}

func withSlots(t *testing.T, link tvSlotsLink) {
	t.Helper()
	prev := tvSlots.Load()
	tvSlots.Store(linkBox{link})
	t.Cleanup(func() {
		if prev != nil {
			tvSlots.Store(prev)
		} else {
			tvSlots.Store(linkBox{})
		}
	})
}

// take asks g for n sessions and returns the releases of those it got.
func take(g *vodGate, n int) []func(bool) {
	var got []func(bool)
	for i := 0; i < n; i++ {
		if r, ok := g.Acquire(context.Background(), uuid.New(), 0); ok {
			got = append(got, r)
		}
	}
	return got
}

func giveAll(rs []func(bool)) {
	for _, r := range rs {
		r(false)
	}
}

// With TV slots: VOD's NVENC sessions and the slots' stay within the channels'
// cap, counted live (each session taken after a sync whose budget leaves VOD's
// reservation out, and the slots' fresh report), VOD holding at most half.
func TestVODGateCountsTheSlotsLive(t *testing.T) {
	g := &vodGate{gate: channel.NewEncoderGate(6)}
	slots := &fakeSlots{nvencMax: 6, gate: g}
	withSlots(t, slots)

	got := take(g, 4)
	if len(got) != 3 || g.Held() != 3 {
		t.Fatalf("VOD alone got %d (held %d), want 3: half the cap", len(got), g.Held())
	}
	if slots.syncs != 3 || slots.budget != 3 {
		t.Errorf("syncs %d, the slots' budget %d; want a sync a session, budget 6-3", slots.syncs, slots.budget)
	}
	giveAll(got)
	if g.Held() != 0 {
		t.Fatalf("held %d after the releases", g.Held())
	}

	// the slots' channels hold 4: VOD gets the 2 left
	slots.want = 4
	if got = take(g, 3); len(got) != 2 {
		t.Errorf("beside 4 slot sessions VOD got %d, want 2", len(got))
	}
	giveAll(got)

	// no word from the slots: software (nothing reserved stays)
	slots.want, slots.syncFails = 0, true
	if got = take(g, 2); len(got) != 0 || g.Held() != 0 {
		t.Errorf("without a sync VOD got %d (held %d)", len(got), g.Held())
	}
	slots.syncFails = false

	// VideoToolbox has its own cap: the settings' VTMax (0: none), not NVENCMax
	vt := &vodGate{gate: channel.NewEncoderGate(0), vt: true}
	slots.nvencMax, slots.vtMax = 2, 0
	if got = take(vt, 8); len(got) != 8 {
		t.Errorf("VideoToolbox without a cap: VOD got %d of 8", len(got))
	}
	giveAll(got)
	slots.vtMax = 4
	if got = take(vt, 4); len(got) != 2 {
		t.Errorf("VideoToolbox capped at 4: VOD got %d, want 2", len(got))
	}
	giveAll(got)
}

// Many sessions at once take no more than the rule allows: the check and the
// take are one step.
func TestVODGateConcurrent(t *testing.T) {
	g := &vodGate{gate: channel.NewEncoderGate(8)}
	withSlots(t, &fakeSlots{nvencMax: 8, gate: g})
	var mu sync.Mutex
	var got []func(bool)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, ok := g.Acquire(context.Background(), uuid.New(), 0); ok {
				mu.Lock()
				got = append(got, r)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(got) != 4 || g.Held() != 4 {
		t.Errorf("20 at once got %d (held %d), want 4: half of 8", len(got), g.Held())
	}
	giveAll(got)
}

// With TV slots off the channels run in this process: its gate counts them and
// VOD together, VOD at most half of its cap.
func TestVODGateSharesTheProcessGate(t *testing.T) {
	withSlots(t, nil)
	gate := channel.NewEncoderGate(6)
	g := &vodGate{gate: gate}
	ch := uuid.New()
	var channels []func(bool)
	for i := 0; i < 5; i++ {
		r, ok := gate.Acquire(context.Background(), ch, 0)
		if !ok {
			t.Fatal("the channels' gate refused")
		}
		channels = append(channels, r)
	}
	got := take(g, 3)
	if len(got) != 1 {
		t.Errorf("beside 5 channel sessions VOD got %d, want 1", len(got))
	}
	giveAll(got)
	giveAll(channels)
	if got = take(g, 4); len(got) != 3 {
		t.Errorf("alone VOD got %d, want 3", len(got))
	}
	giveAll(got)
	if gate.Active() != 0 || g.Held() != 0 {
		t.Errorf("after the releases the gate holds %d, VOD %d", gate.Active(), g.Held())
	}
}

// The slots' budget leaves out what VOD holds, never below one session.
func TestSlotBudget(t *testing.T) {
	prevN, prevV := vodNVENC.held.Load(), vodVT.held.Load()
	t.Cleanup(func() { vodNVENC.held.Store(prevN); vodVT.held.Store(prevV) })
	vodNVENC.held.Store(2)
	vodVT.held.Store(1)
	if nv, vt := slotBudget(6, 0); nv != 4 || vt != 0 {
		t.Errorf("slotBudget(6, 0) = %d, %d; want 4, 0", nv, vt)
	}
	if nv, vt := slotBudget(2, 3); nv != 1 || vt != 2 {
		t.Errorf("slotBudget(2, 3) = %d, %d; want 1, 2", nv, vt)
	}
	if nv, _ := slotBudget(0, 0); nv != 0 {
		t.Errorf("no cap stays none: %d", nv)
	}
}
