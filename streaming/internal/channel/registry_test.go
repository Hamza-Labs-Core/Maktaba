package channel

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRegistry_AdmitUnderCap(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	r := newRegistry(2, clk.now)
	if err := r.admit(uuid.New()); err != nil {
		t.Fatalf("admit under cap should succeed: %v", err)
	}
}

func TestRegistry_WarmReTuneNoRespawn(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	r := newRegistry(2, clk.now)
	id := uuid.New()
	_ = r.admit(id)
	r.put(id, func() {})
	// Viewer leaves → warm.
	r.detach(id)
	// Re-tune attaches to the same entry.
	if !r.attach(id) {
		t.Fatal("attach to warm channel should succeed")
	}
	if e, _ := r.get(id); e.viewers != 1 {
		t.Errorf("viewers after re-tune = %d, want 1", e.viewers)
	}
}

func TestRegistry_CapEvictsLRUWarm(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	r := newRegistry(2, clk.now)
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	_ = r.admit(a)
	stoppedA := false
	r.put(a, func() { stoppedA = true })
	r.detach(a) // a is warm, touched at T0

	clk.advance(time.Minute)
	_ = r.admit(b)
	r.put(b, func() {})
	// b stays with 1 viewer (not warm).

	// Now at cap (2). Admitting c must evict the LRU warm channel = a.
	clk.advance(time.Minute)
	if err := r.admit(c); err != nil {
		t.Fatalf("admit should evict warm LRU, got %v", err)
	}
	if !stoppedA {
		t.Error("LRU warm channel a should have been stopped")
	}
	if _, ok := r.get(a); ok {
		t.Error("a should be evicted")
	}
}

func TestRegistry_CapRejectsWhenAllBusy(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	r := newRegistry(1, clk.now)
	a := uuid.New()
	_ = r.admit(a)
	r.put(a, func() {}) // 1 viewer, not warm
	if err := r.admit(uuid.New()); err != ErrAtCapacity {
		t.Errorf("expected ErrAtCapacity when all busy, got %v", err)
	}
}

func TestRegistry_ReapIdlePastGrace(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	r := newRegistry(4, clk.now)
	id := uuid.New()
	_ = r.admit(id)
	stopped := false
	r.put(id, func() { stopped = true })
	r.detach(id) // warm at T0

	// Within grace → not reaped.
	clk.advance(30 * time.Second)
	if got := r.reapIdle(60 * time.Second); len(got) != 0 {
		t.Errorf("should not reap within grace, reaped %v", got)
	}
	// Past grace → reaped + stopped.
	clk.advance(90 * time.Second)
	got := r.reapIdle(60 * time.Second)
	if len(got) != 1 || got[0] != id {
		t.Errorf("expected reap of %v, got %v", id, got)
	}
	if !stopped {
		t.Error("reaped channel should be stopped")
	}
}

func TestRegistry_ViewerKeepsAliveAcrossReap(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	r := newRegistry(4, clk.now)
	id := uuid.New()
	_ = r.admit(id)
	r.put(id, func() {}) // 1 viewer
	clk.advance(10 * time.Minute)
	if got := r.reapIdle(60 * time.Second); len(got) != 0 {
		t.Errorf("channel with a viewer must not be reaped, got %v", got)
	}
}
