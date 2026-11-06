package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Tunnel handover, the relay's side (Epic 50 story 50.03).
//
// A drained tunnel is asked to move: the relay sends DRAIN, the home dials a
// new tunnel (the registry routes new requests to it: it is the newer one),
// the old tunnel finishes the streams it has, and the home closes it with
// 4000. At the deadline the relay closes it whatever it still carries
// (tunnel_drain_forced_total). A home that does not understand DRAIN (no
// "drain" feature) is closed as soon as it is idle, or at the deadline, and
// reconnects by backoff as before.

// DrainOptions are one drain's timings.
type DrainOptions struct {
	Reason   string
	Within   time.Duration // the home opens its new tunnel within this
	Deadline time.Duration // the old tunnel closes after this, streams or not
}

// idlePoll is how often a drained tunnel of an older home is checked for
// idleness (a var for tests).
var idlePoll = 100 * time.Millisecond

// Draining reports whether DRAIN was sent on t.
func (t *Tunnel) Draining() bool { return t.draining.Load() }

// Drain asks the home to move and closes the tunnel when it has (or at the
// deadline). Calling it again is a no-op. ticket is a fresh resume ticket
// for the home ("" when tickets are off).
func (t *Tunnel) Drain(o DrainOptions, ticket string) {
	t.drainOnce.Do(func() {
		t.draining.Store(true)
		stats.drain(o.Reason)
		if t.canDrain {
			p, _ := json.Marshal(DrainPayload{
				Reason:     o.Reason,
				WithinMS:   o.Within.Milliseconds(),
				DeadlineMS: o.Deadline.Milliseconds(),
				Ticket:     ticket,
			})
			_ = t.writeFrame(Frame{Kind: KindDrain, Payload: p})
		}
		go t.drainWatch(o.Deadline, !t.canDrain)
	})
}

// drainWatch closes the tunnel at the deadline, and before it once the tunnel
// is idle when the home cannot move by itself (closeWhenIdle).
func (t *Tunnel) drainWatch(deadline time.Duration, closeWhenIdle bool) {
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	var poll <-chan time.Time
	if closeWhenIdle {
		tk := time.NewTicker(idlePoll)
		defer tk.Stop()
		poll = tk.C
		if !t.Busy() {
			t.Close()
			return
		}
	}
	for {
		select {
		case <-t.done:
			return
		case <-poll:
			if !t.Busy() {
				t.Close()
				return
			}
		case <-timer.C:
			if t.Busy() {
				stats.drainForced.Add(1)
			}
			t.Close()
			return
		}
	}
}

// Shutdown drains every tunnel of the registry for a process that is going
// away: it refuses new tunnels (Closing), sends DRAIN to rate homes a second,
// and waits until every tunnel is gone or ctx ends; then it closes the rest.
// mint gives each home a fresh resume ticket (nil: none).
func (r *Registry) Shutdown(ctx context.Context, o DrainOptions, rate int, mint func(serverID string) string) {
	r.closing.Store(true)
	if rate <= 0 {
		rate = 100
	}
	gap := time.Second / time.Duration(rate)
	for _, t := range r.All() {
		if ctx.Err() != nil {
			break
		}
		ticket := ""
		if mint != nil && t.canTicket {
			ticket = mint(t.ServerID)
		}
		t.Drain(o, ticket)
		select {
		case <-ctx.Done():
		case <-time.After(gap):
		}
	}
	tk := time.NewTicker(50 * time.Millisecond)
	defer tk.Stop()
	for r.Live() > 0 {
		select {
		case <-ctx.Done():
			for _, t := range r.All() {
				if t.Busy() {
					stats.drainForced.Add(1)
				}
				t.Close()
			}
			return
		case <-tk.C:
		}
	}
}

// Closing reports whether the registry is shutting down: new tunnels are
// refused (503) so homes go to the other process or node.
func (r *Registry) Closing() bool { return r.closing.Load() }

// All returns every open tunnel, draining ones included, oldest first.
func (r *Registry) All() []*Tunnel {
	r.mu.RLock()
	out := make([]*Tunnel, 0, len(r.open))
	for t := range r.open {
		out = append(out, t)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// Live is the number of open tunnels, draining ones included.
func (r *Registry) Live() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.open)
}

// track keeps t in the open set until it closes.
func (r *Registry) track(t *Tunnel) {
	if !t.tracked.CompareAndSwap(false, true) {
		return
	}
	r.open[t] = struct{}{}
	go func() {
		<-t.done
		r.mu.Lock()
		delete(r.open, t)
		r.mu.Unlock()
	}()
}

// tunnelStats are the handover counters served on /metrics.
type tunnelStats struct {
	mu            sync.Mutex
	drains        map[string]int64
	drainForced   atomic.Int64
	handsBearer   atomic.Int64
	handsTicket   atomic.Int64
	ticketRefused atomic.Int64
}

var stats = &tunnelStats{drains: map[string]int64{}}

func (s *tunnelStats) drain(reason string) {
	s.mu.Lock()
	s.drains[reason]++
	s.mu.Unlock()
}

// TunnelCounters is a snapshot of the handover counters (tests, metrics).
type TunnelCounters struct {
	Drains        map[string]int64
	DrainForced   int64
	HandsBearer   int64
	HandsTicket   int64
	TicketRefused int64
}

// Counters snapshots the handover counters.
func Counters() TunnelCounters {
	stats.mu.Lock()
	d := make(map[string]int64, len(stats.drains))
	for k, v := range stats.drains {
		d[k] = v
	}
	stats.mu.Unlock()
	return TunnelCounters{Drains: d, DrainForced: stats.drainForced.Load(),
		HandsBearer: stats.handsBearer.Load(), HandsTicket: stats.handsTicket.Load(),
		TicketRefused: stats.ticketRefused.Load()}
}

// WriteMetrics renders the handover counters in Prometheus text format, after
// the relay's other series on /metrics.
func WriteMetrics(w io.Writer) error {
	c := Counters()
	var err error
	p := func(format string, a ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, a...)
		}
	}
	p("# HELP maktaba_relay_tunnel_drains_total Tunnels asked to move (DRAIN), by reason.\n")
	p("# TYPE maktaba_relay_tunnel_drains_total counter\n")
	for _, reason := range []string{DrainDeploy, DrainRebalance, DrainShutdown} {
		p("maktaba_relay_tunnel_drains_total{reason=%q} %d\n", reason, c.Drains[reason])
	}
	p("# HELP maktaba_relay_tunnel_drain_forced_total Drained tunnels closed at the deadline with streams still open.\n")
	p("# TYPE maktaba_relay_tunnel_drain_forced_total counter\n")
	p("maktaba_relay_tunnel_drain_forced_total %d\n", c.DrainForced)
	p("# HELP maktaba_relay_tunnel_handshakes_total Tunnel handshakes accepted, by method.\n")
	p("# TYPE maktaba_relay_tunnel_handshakes_total counter\n")
	p("maktaba_relay_tunnel_handshakes_total{method=\"bearer\"} %d\n", c.HandsBearer)
	p("maktaba_relay_tunnel_handshakes_total{method=\"ticket\"} %d\n", c.HandsTicket)
	p("# HELP maktaba_relay_tunnel_tickets_refused_total Resume tickets refused (ticket_invalid).\n")
	p("# TYPE maktaba_relay_tunnel_tickets_refused_total counter\n")
	p("maktaba_relay_tunnel_tickets_refused_total %d\n", c.TicketRefused)
	return err
}
