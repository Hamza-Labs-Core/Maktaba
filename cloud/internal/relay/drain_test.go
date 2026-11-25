package relay

import (
	"context"
	"testing"
	"time"
)

// cutConn is a home that takes DRAIN, in takes; tun is asked about as it is sent.
type cutConn struct {
	nopConn
	tun     *Tunnel
	cutAtIn bool
	takes   time.Duration
}

func (c *cutConn) WriteFrame(f Frame) error {
	if f.Kind == KindDrain {
		c.cutAtIn = c.tun.cutAt.Load() != nil
		time.Sleep(c.takes)
	}
	return nil
}

// closedAndCounted waits for tun to close by itself and returns how many forced closes were
// counted by the time it was seen closed.
func closedAndCounted(t *testing.T, tun *Tunnel, before int64) int64 {
	t.Helper()
	select {
	case <-tun.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel was not closed")
	}
	return Counters().DrainForced - before
}

// The relay closes a drained tunnel at the deadline by itself: a home that never closes its
// side (stuck, or an older one) with a stream that never ends. A retired standby's too.
func TestTheRelayClosesADrainedTunnelAtItsDeadlineByItself(t *testing.T) {
	const deadline = 60 * time.Millisecond
	drain := func(tun *Tunnel) {
		tun.Drain(DrainOptions{Reason: DrainDeploy, Within: 10 * time.Millisecond, Deadline: deadline}, "")
	}
	cases := map[string]struct {
		moves bool // the home understands DRAIN
		ask   func(*Tunnel)
	}{
		"a home that moves":    {true, drain},
		"an older home":        {false, drain},
		"a standby is retired": {true, func(tun *Tunnel) { tun.Retire(deadline) }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tun := NewTunnel("srv", "slug", &nopConn{done: make(chan struct{})}, WithFeatures(c.moves, false))
			defer tun.Close()
			tun.inflight.Add(1)
			before, start := Counters().DrainForced, time.Now()
			c.ask(tun)
			if got := closedAndCounted(t, tun, before); got != 1 {
				t.Errorf("tunnel_drain_forced_total += %d, want 1", got)
			}
			if took := time.Since(start); took < deadline {
				t.Errorf("closed after %s, before the %s deadline", took, deadline)
			}
		})
	}
}

// A shutdown whose grace ends closes what is left, and counts each once: also one whose
// deadline has passed while its DRAIN was still being written.
func TestShutdownCountsATunnelItCutsOnce(t *testing.T) {
	for name, deadline := range map[string]time.Duration{
		"past its deadline": 60 * time.Millisecond, "before it": time.Hour,
	} {
		t.Run(name, func(t *testing.T) {
			conn := &cutConn{nopConn: nopConn{done: make(chan struct{})}, takes: 80 * time.Millisecond}
			tun := NewTunnel("srv", "slug", conn, WithFeatures(true, false))
			conn.tun = tun
			defer tun.Close()
			reg := NewRegistry()
			reg.Register("slug", tun)
			tun.inflight.Add(1)
			before := Counters().DrainForced
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
			defer cancel()
			reg.Shutdown(ctx, DrainOptions{Reason: DrainShutdown, Within: time.Second, Deadline: deadline}, 1000, nil)
			if got := closedAndCounted(t, tun, before); got != 1 {
				t.Errorf("tunnel_drain_forced_total += %d, want 1", got)
			}
		})
	}
}

// A drained tunnel closed at its deadline with a stream open counts as forced whoever closes
// it first: the home times the same deadline as the relay. One closed before it, or idle, or
// not draining, does not.
func TestADrainCutAtItsDeadlineCountsWhoeverClosesIt(t *testing.T) {
	cases := map[string]struct {
		draining bool
		deadline time.Duration
		streams  int64
		want     int64
	}{
		"at the deadline, with a stream": {true, -time.Millisecond, 1, 1},
		"at the deadline, idle":          {true, -time.Millisecond, 0, 0},
		"before it, with a stream":       {true, time.Hour, 1, 0},
		"not draining":                   {false, 0, 1, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tun := NewTunnel("srv", "slug", &nopConn{done: make(chan struct{})})
			if c.draining {
				tun.cutFrom(c.deadline)
			}
			tun.inflight.Add(c.streams)
			before := Counters().DrainForced
			tun.Close() // as its read loop does when the home closes it
			tun.Close()
			if got := Counters().DrainForced - before; got != c.want {
				t.Errorf("tunnel_drain_forced_total += %d, want %d", got, c.want)
			}
		})
	}
}

// The deadline is set before DRAIN is sent: the home times its own from that, so its close at
// the deadline is never before the relay's.
func TestDrainSetsItsDeadlineBeforeAskingTheHome(t *testing.T) {
	conn := &cutConn{nopConn: nopConn{done: make(chan struct{})}}
	tun := NewTunnel("srv", "slug", conn, WithFeatures(true, false))
	conn.tun = tun
	defer tun.Close()
	tun.Drain(DrainOptions{Reason: DrainDeploy, Within: time.Second, Deadline: time.Hour}, "")
	if !conn.cutAtIn {
		t.Error("DRAIN was sent before the deadline was set")
	}
}
