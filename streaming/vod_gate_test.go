package main

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// fakeSlots is the TV slots as the front door sees them: each Sync they take
// the budget slotBudget gives them (unless nack) and report what they hold.
type fakeSlots struct {
	mu                sync.Mutex
	ready             bool
	nack              bool // a slot does not take its set (refused, restarting)
	want              int  // sessions the slots' channels hold
	used, limit, need int
	vtLimit           int
	budget, syncs     int
	gate              *vodGate // whose holds the budget leaves out
}

func (f *fakeSlots) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeSlots) Room(_ context.Context, vt bool) (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if vt {
		return f.vtLimit, 0, f.used
	}
	return f.limit, f.need, f.used
}

func (f *fakeSlots) Sync(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	if f.nack {
		return false
	}
	f.budget = f.limit - f.gate.Held()
	f.used = f.want
	return true
}

func (f *fakeSlots) set(fn func(f *fakeSlots)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
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

func giveAll(rs []func(bool)) {
	for _, r := range rs {
		r(false)
	}
}

// With TV slots: a transcode's sessions are reserved at once, the slots' budget
// drops by them in one sync, and VOD keeps them only within half the cap, what
// the live and standby channels need, and what the slots' fresh report leaves.
func TestVODGateCountsTheSlotsLive(t *testing.T) {
	g := &vodGate{gate: channel.NewEncoderGate(6)}
	slots := &fakeSlots{ready: true, limit: 6, gate: g}
	withSlots(t, slots)
	ctx := context.Background()

	got := g.AcquireN(ctx, uuid.New(), 4)
	if len(got) != 3 || g.Held() != 3 || slots.syncs != 1 || slots.budget != 3 {
		t.Fatalf("got %d (held %d), %d syncs, slots' budget %d; want 3, 1 sync, 3", len(got), g.Held(), slots.syncs, slots.budget)
	}
	giveAll(got)

	// one channel live and one the standby keeps warm: 4 of the 6 are theirs
	slots.set(func(f *fakeSlots) { f.need = 4 })
	if got = g.AcquireN(ctx, uuid.New(), 3); len(got) != 2 {
		t.Errorf("beside the channels' 4 VOD got %d, want 2", len(got))
	}
	giveAll(got)

	// the slots hold 5 by their fresh report: 1 left
	slots.set(func(f *fakeSlots) { f.need, f.want = 0, 5 })
	if got = g.AcquireN(ctx, uuid.New(), 3); len(got) != 1 || g.Held() != 1 {
		t.Errorf("beside 5 slot sessions VOD got %d (held %d), want 1", len(got), g.Held())
	}
	giveAll(got)

	// a slot that did not take its budget, or slots not read yet: software
	slots.set(func(f *fakeSlots) { f.want, f.used, f.nack = 0, 0, true })
	if got = g.AcquireN(ctx, uuid.New(), 3); len(got) != 0 || g.Held() != 0 {
		t.Errorf("without every slot's word VOD got %d (held %d)", len(got), g.Held())
	}
	slots.set(func(f *fakeSlots) { f.nack, f.ready = false, false })
	if got = g.AcquireN(ctx, uuid.New(), 3); len(got) != 0 {
		t.Errorf("before the slots were read VOD got %d", len(got))
	}
	slots.set(func(f *fakeSlots) { f.ready = true })

	// VideoToolbox: its own cap (0: none), not NVENC's
	vt := &vodGate{gate: channel.NewEncoderGate(0), vt: true}
	if got = vt.AcquireN(ctx, uuid.New(), 8); len(got) != 8 {
		t.Errorf("VideoToolbox without a cap: VOD got %d of 8", len(got))
	}
	giveAll(got)
	slots.set(func(f *fakeSlots) { f.vtLimit = 4 })
	if got = vt.AcquireN(ctx, uuid.New(), 4); len(got) != 2 {
		t.Errorf("VideoToolbox capped at 4: VOD got %d, want 2", len(got))
	}
	giveAll(got)
	if g.Held() != 0 || vt.Held() != 0 {
		t.Errorf("held after the releases: %d, %d", g.Held(), vt.Held())
	}
}

// Transcodes opening at once take no more than the rules allow.
func TestVODGateConcurrent(t *testing.T) {
	g := &vodGate{gate: channel.NewEncoderGate(8)}
	withSlots(t, &fakeSlots{ready: true, limit: 8, gate: g})
	var mu sync.Mutex
	var got []func(bool)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := g.AcquireN(context.Background(), uuid.New(), 3)
			mu.Lock()
			got = append(got, r...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(got) != 4 || g.Held() != 4 {
		t.Errorf("10 transcodes at once got %d (held %d), want 4: half of 8", len(got), g.Held())
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
	got := g.AcquireN(context.Background(), uuid.New(), 3)
	if len(got) != 1 || g.Held() != 1 {
		t.Errorf("beside 5 channel sessions VOD got %d (held %d), want 1", len(got), g.Held())
	}
	giveAll(got)
	giveAll(channels)
	if got = g.AcquireN(context.Background(), uuid.New(), 4); len(got) != 3 {
		t.Errorf("alone VOD got %d, want 3", len(got))
	}
	giveAll(got)
	if r, ok := g.Acquire(context.Background(), uuid.New(), 0); !ok {
		t.Error("Acquire: none")
	} else {
		r(false)
	}
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
