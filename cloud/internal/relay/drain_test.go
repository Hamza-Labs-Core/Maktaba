package relay

import (
	"testing"
	"time"
)

// cutConn is a home that takes DRAIN; tun is asked about as it is sent.
type cutConn struct {
	nopConn
	tun     *Tunnel
	cutAtIn bool
}

func (c *cutConn) WriteFrame(f Frame) error {
	if f.Kind == KindDrain {
		c.cutAtIn = c.tun.cutAt.Load() != nil
	}
	return nil
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
