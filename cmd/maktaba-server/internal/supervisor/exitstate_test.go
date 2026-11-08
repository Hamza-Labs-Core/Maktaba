package supervisor

import (
	"sync"
	"testing"
)

// A child that exits is not shown running: the snapshot taken while its exit is
// still being reported (the exit hook hears it before the restart policy runs)
// has no PID and is not in the running state. A crashed TV slot read as running
// with PID 0 in that moment; a wait that took any new PID as the restart passed
// on it.
func TestAnExitedChildIsNotShownRunning(t *testing.T) {
	p, addr := fakeProc(t, RoleTVSlot, "serve")
	p.Instance = "green"
	var s *Supervisor
	var mu sync.Mutex
	var seen []ChildStatus
	s = New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}), WithExitHook(func(Process, ExitInfo) {
		snap := s.Snapshot()
		mu.Lock()
		seen = append(seen, snap.Processes[0])
		mu.Unlock()
	}))
	stop, _ := running(t, s)
	waitFor(t, s, "green running", slotRunning("green"))
	get(t, "http://"+addr+"/crash?code=3")
	waitFor(t, s, "the exit heard", func(Snapshot) bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0
	})
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if c := seen[0]; c.PID != 0 || c.State == StateRunning {
		t.Fatalf("as its exit is reported, the child shows pid %d, state %s", c.PID, c.State)
	}
}
