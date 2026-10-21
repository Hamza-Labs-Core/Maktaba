package supervisor

import (
	"sync/atomic"
	"testing"
	"time"
)

// A child with its own Stop (postgres on Windows: pg_ctl's fast shutdown) is asked
// through it, not sent a signal, and stops without waiting out the grace.
func TestStopHookAsksTheChild(t *testing.T) {
	p, addr := fakeProc(t, RoleAPI, "serve")
	var asked atomic.Int32
	p.Stop = func() {
		asked.Add(1)
		get(t, "http://"+addr+"/exit") // the child's own graceful exit
	}
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	waitFor(t, s, "the child running", func(snap Snapshot) bool { return proc(snap, RoleAPI).State == StateRunning })
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if asked.Load() == 0 {
		t.Fatal("Stop was not called")
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("stopping took %s: the grace ran out (the child was killed)", took)
	}
}
