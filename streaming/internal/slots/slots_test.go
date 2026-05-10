package slots

import "testing"

func TestAllocator_Admit_UpToCap(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 2, QueueDepth: 0})
	d, _ := a.Decide(Request{})
	if d != DecisionAdmit {
		t.Fatalf("want admit, got %s", d)
	}
	d, _ = a.Decide(Request{})
	if d != DecisionAdmit {
		t.Fatalf("want admit, got %s", d)
	}
}

func TestAllocator_OverCap_Exhausted(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 1, QueueDepth: 0})
	_, _ = a.Decide(Request{})
	d, _ := a.Decide(Request{})
	if d != DecisionExhausted {
		t.Fatalf("want exhausted, got %s", d)
	}
}

func TestAllocator_OverCap_DirectCap(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 1})
	_, _ = a.Decide(Request{})
	d, _ := a.Decide(Request{CanDirectCap: true})
	if d != DecisionDirectCap {
		t.Fatalf("want direct-cap, got %s", d)
	}
}

func TestAllocator_OverCap_Queue(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 1, QueueDepth: 5})
	_, _ = a.Decide(Request{})
	d, _ := a.Decide(Request{AcceptQueue: true})
	if d != DecisionQueue {
		t.Fatalf("want queue, got %s", d)
	}
	if a.QueueLength() != 1 {
		t.Fatalf("queue len=%d", a.QueueLength())
	}
}

func TestAllocator_QueueOverflow(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 1, QueueDepth: 1})
	_, _ = a.Decide(Request{})
	if d, _ := a.Decide(Request{AcceptQueue: true}); d != DecisionQueue {
		t.Fatalf("want queue, got %s", d)
	}
	if d, _ := a.Decide(Request{AcceptQueue: true}); d != DecisionExhausted {
		t.Fatalf("want exhausted, got %s", d)
	}
}

func TestAllocator_ReleaseFreesSlot(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 1})
	_, hold := a.Decide(Request{})
	if a.Used() != 1 {
		t.Fatalf("used=%d", a.Used())
	}
	if err := a.Release(hold); err != nil {
		t.Fatal(err)
	}
	if a.Used() != 0 {
		t.Fatalf("used=%d after release", a.Used())
	}
	if d, _ := a.Decide(Request{}); d != DecisionAdmit {
		t.Fatalf("want admit after release, got %s", d)
	}
}

func TestAllocator_ReleaseUnknownHold(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 1})
	if err := a.Release("nope"); err != ErrNotHeld {
		t.Fatalf("err=%v", err)
	}
}

func TestAllocator_PromoteFromQueue(t *testing.T) {
	a := NewAllocator(AllocatorConfig{MaxTranscode: 1, QueueDepth: 5})
	_, hold := a.Decide(Request{})
	_, _ = a.Decide(Request{AcceptQueue: true})

	if _, ok := a.PromoteFromQueue(); ok {
		t.Fatal("should not promote when full")
	}
	_ = a.Release(hold)
	if _, ok := a.PromoteFromQueue(); !ok {
		t.Fatal("should promote after release")
	}
	if a.QueueLength() != 0 {
		t.Fatalf("queue len=%d", a.QueueLength())
	}
}

func TestAllocator_AutoDeriveCap(t *testing.T) {
	a := NewAllocator(AllocatorConfig{})
	if a.MaxConcurrent() < 1 {
		t.Fatal("auto-derive should be at least 1")
	}
}
