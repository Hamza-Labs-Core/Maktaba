package cloudlink

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestSupervisor_AcceptThenDropStorm_NoZeroDelayLoop_NoLeak is the I-1
// regression. It drives the real Supervisor against a dial that returns
// a connection which is accepted then INSTANTLY dropped, looped many
// times (an accept-then-drop flap storm).
//
// It asserts two things the spec reviewer flagged as untested:
//
//	(a) The stableThreshold gate works: a tunnel that never stays up for
//	    stableThreshold must NOT reset the backoff counter, so the
//	    reported attempt count climbs monotonically across the storm
//	    instead of being pinned at 1 (a zero-delay reconnect loop). If
//	    the gate regressed (e.g. attempt unconditionally reset to 0 on
//	    every drop), Attempts would oscillate at 1 and this fails.
//
//	(b) No goroutine leak across the cycles: every per-reconnect
//	    Multiplexer readLoop + keepalive + handle goroutine must exit
//	    when the tunnel drops, so NumGoroutine returns to baseline
//	    (within tolerance) after the storm.
func TestSupervisor_AcceptThenDropStorm_NoZeroDelayLoop_NoLeak(t *testing.T) {
	const cycles = 12

	// Let goroutines from earlier tests / runtime settle first.
	base := goroutineCountStable(runtime.NumGoroutine(), 500*time.Millisecond)

	var dials int64
	dial := func(_ context.Context) (DialResult, error) {
		atomic.AddInt64(&dials, 1)
		clientEnd, cloudEnd := newPipe()
		// Accept-then-instant-drop: close the cloud end immediately so the
		// client's readLoop errors out at once (tunnel up << stableThreshold).
		_ = cloudEnd.Close()
		return DialResult{Conn: clientEnd, Slug: "flap"}, nil
	}

	sup := &Supervisor{
		Dialer:    &Dialer{},
		Proxy:     &LocalProxy{BaseURL: "http://127.0.0.1:1"},
		BaseWait:  2 * time.Millisecond,
		MaxWait:   16 * time.Millisecond,
		PingEvery: time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = runSupervisorWithDial(ctx, sup, dial)
		close(done)
	}()

	// Sample the reported attempt counter across the storm. Because the
	// drop is always faster than stableThreshold (30s), attempt is never
	// reset to 0, so it must reach values well above 1.
	var maxAttempts int
	deadline := time.After(5 * time.Second)
sample:
	for {
		select {
		case <-deadline:
			break sample
		case <-time.After(3 * time.Millisecond):
			st := sup.State()
			if st.Attempts > maxAttempts {
				maxAttempts = st.Attempts
			}
			if atomic.LoadInt64(&dials) >= cycles && maxAttempts >= 5 {
				break sample
			}
		}
	}

	if got := atomic.LoadInt64(&dials); got < cycles {
		t.Fatalf("only %d dials in the storm window, want >= %d", got, cycles)
	}
	// stableThreshold gate proof: a flapping server must drive the
	// backoff counter UP, not pin it at 1 (zero-delay reconnect loop).
	if maxAttempts < 5 {
		t.Fatalf("max reported Attempts = %d; expected the stableThreshold gate to let "+
			"the backoff counter climb past 1 across an accept-then-drop storm "+
			"(a zero-delay reconnect loop / reset-on-every-drop bug)", maxAttempts)
	}

	cancel()
	<-done

	// Goroutine-leak proof: after the storm + shutdown every
	// per-reconnect readLoop/keepalive/handle goroutine must have exited.
	got := goroutineCountStable(base+2, 5*time.Second)
	if got > base+3 {
		t.Fatalf("goroutine leak across %d reconnect cycles: got %d, baseline %d "+
			"(a readLoop/keepalive/handle goroutine did not exit per reconnect)", cycles, got, base)
	}
}
