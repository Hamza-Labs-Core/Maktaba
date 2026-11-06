package cloudlink

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// LinkState is the snapshot served at the local GET /admin/cloud-link
// endpoint (Story 25.7 AC: "surface state at local /admin/cloud-link").
type LinkState struct {
	// Status is one of: "connecting", "online", "reconnecting", "stopped".
	Status string `json:"status"`
	// Slug is the subdomain the cloud confirmed at AUTH_OK.
	Slug string `json:"slug,omitempty"`
	// ConnectedAt is when the current tunnel established (zero if down).
	ConnectedAt time.Time `json:"connected_at,omitempty"`
	// LastError is the most recent dial/tunnel failure, if any.
	LastError string `json:"last_error,omitempty"`
	// Attempts is the consecutive reconnect attempt counter.
	Attempts int `json:"attempts"`
	// BytesIn / BytesOut / Requests are cumulative across the process.
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
	Requests int64 `json:"requests"`
}

// Backoff computes exponential backoff with a cap. attempt is 1-based.
// Story 25.7 requires "exponential-backoff reconnect"; we cap at 30s so
// a long cloud outage does not strand the box for minutes.
func Backoff(attempt int, base, maxDelay time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := float64(base) * math.Pow(2, float64(attempt-1))
	if d > float64(maxDelay) || math.IsInf(d, 1) {
		return maxDelay
	}
	return time.Duration(d)
}

// Jitter is full-jitter backoff (story 50.03): a random wait in
// [0, Backoff(attempt, base, maxDelay)], so homes that lost their tunnels at
// the same instant do not all retry at the same instants. rnd draws in
// [0, 1); nil uses the package's random source.
func Jitter(attempt int, base, maxDelay time.Duration, rnd func() float64) time.Duration {
	if rnd == nil {
		rnd = rand.Float64
	}
	return time.Duration(rnd() * float64(Backoff(attempt, base, maxDelay)+1))
}

// Supervisor owns the connect → serve → reconnect lifecycle. It is the
// process-level state machine behind cmd/maktaba-cloudlink.
type Supervisor struct {
	Dialer   *Dialer
	Proxy    *LocalProxy
	BaseWait time.Duration // backoff base; default 1s
	MaxWait  time.Duration // backoff cap; default 30s
	// PingEvery is the client keepalive cadence. Story 25.7: PING every
	// 25s; default 25s.
	PingEvery time.Duration
	// PongWait is how long we tolerate silence before declaring the
	// tunnel dead. Story 25.7: PONG within 10s; default 35s
	// (PingEvery + 10s grace).
	PongWait time.Duration

	// dial is the seam used to obtain a connection. Nil means "use
	// Dialer.Dial". Tests inject an in-memory dial to exercise the
	// reconnect loop without a network.
	dial func(context.Context) (DialResult, error)
	// rnd draws in [0, 1) for jitter; nil: math/rand/v2 (tests seed it)
	rnd func() float64
	// legacy: behave as an agent from before story 50.03 (LegacyAgent)
	legacy bool

	mu    sync.Mutex
	state LinkState
}

// LegacyAgent makes s behave as an agent from before story 50.03: it
// announces no features, so the relay sends it no DRAIN and no tickets. The
// simulated homes of the rehearsal use it to prove the relay's fallback.
func LegacyAgent(s *Supervisor) {
	s.legacy = true
	if s.Dialer != nil {
		s.Dialer.legacy = true
	}
}

func (s *Supervisor) jitter(attempt int, base, maxW time.Duration) time.Duration {
	return Jitter(attempt, base, maxW, s.rnd)
}

// rand01 is a draw in [0, 1).
func (s *Supervisor) rand01() float64 {
	if s.rnd != nil {
		return s.rnd()
	}
	return rand.Float64()
}

func (s *Supervisor) setTicket(t string) {
	if t != "" && s.Dialer != nil {
		s.Dialer.SetTicket(t)
	}
}

// runSupervisorWithDial drives sup.Run with an injected dial function.
// Exposed (unexported) for tests; production calls Run which defaults
// to Dialer.Dial.
func runSupervisorWithDial(ctx context.Context, sup *Supervisor, dial func(context.Context) (DialResult, error)) error {
	sup.dial = dial
	return sup.Run(ctx)
}

// State returns a copy of the current link state for the admin handler.
func (s *Supervisor) State() LinkState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Supervisor) setStatus(status, lastErr string, attempts int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Status = status
	s.state.Attempts = attempts
	if lastErr != "" {
		s.state.LastError = lastErr
	}
	if status != "online" {
		s.state.ConnectedAt = time.Time{}
		s.state.Slug = ""
	}
}

func (s *Supervisor) setOnline(slug string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Status = "online"
	s.state.Slug = slug
	s.state.ConnectedAt = time.Now().UTC()
	s.state.LastError = ""
	s.state.Attempts = 0
}

func (s *Supervisor) setCounters(st Stats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.BytesIn += st.BytesIn
	s.state.BytesOut += st.BytesOut
	s.state.Requests += st.Requests
}

// Run blocks until ctx is cancelled, maintaining a single live tunnel
// and reconnecting with jittered exponential backoff on every drop. A
// tunnel the cloud drains (DRAIN) is replaced by a new one before it
// closes (serveTunnel), with no backoff.
func (s *Supervisor) Run(ctx context.Context) error {
	base := s.BaseWait
	if base <= 0 {
		base = time.Second
	}
	maxW := s.MaxWait
	if maxW <= 0 {
		maxW = 30 * time.Second
	}
	dial := s.dial
	if dial == nil {
		dial = s.Dialer.Dial
	}
	attempt := 0
	for {
		if ctx.Err() != nil {
			s.setStatus("stopped", "", attempt)
			return ctx.Err()
		}
		attempt++
		s.setStatus("connecting", "", attempt)
		res, err := dial(ctx)
		if err != nil {
			s.setStatus("reconnecting", err.Error(), attempt)
			if !sleepCtx(ctx, s.jitter(attempt, base, maxW)) {
				s.setStatus("stopped", err.Error(), attempt)
				return ctx.Err()
			}
			continue
		}
		connectedAt := time.Now()
		for {
			s.setOnline(res.Slug)
			s.setTicket(res.Ticket)
			next := s.serveTunnel(ctx, res.Conn, res.Flow, dial)
			if next == nil {
				break
			}
			// drained: the new tunnel is up; serve it at once
			res, connectedAt, attempt = *next, time.Now(), 0
		}
		// Tunnel dropped. Only treat this as a "fresh start" (reset the
		// backoff counter) if the tunnel actually stayed up for a
		// meaningful period. A server that accepts then instantly drops
		// must NOT trigger a zero-delay reconnect storm — keep the
		// exponential-backoff counter climbing in that case.
		if time.Since(connectedAt) >= stableThreshold {
			attempt = 0
			// the first try after a drop waits a random [0, 2×base): every
			// home of a dead relay does not knock on the next at once
			if !sleepCtx(ctx, time.Duration(s.rand01()*float64(2*base))) {
				s.setStatus("stopped", "", attempt)
				return ctx.Err()
			}
		} else {
			s.setStatus("reconnecting", "tunnel dropped immediately", attempt)
			if !sleepCtx(ctx, s.jitter(attempt, base, maxW)) {
				s.setStatus("stopped", "", attempt)
				return ctx.Err()
			}
		}
	}
}

// stableThreshold is how long a tunnel must stay connected before we
// consider it healthy enough to reset the reconnect backoff counter.
const stableThreshold = 30 * time.Second

// ErrTunnelSilent: no frame (not even a PONG to our PING) arrived from the
// cloud for PongWait, so the socket is dead (half-open TCP) and the
// supervisor reconnects.
var ErrTunnelSilent = errors.New("cloudlink: no word from the cloud; reconnecting")

// serveTunnel runs one connected tunnel: the multiplexer read loop plus
// a client-side PING keepalive and a liveness check, until the tunnel
// errors, goes silent for PongWait, or ctx ends. flow: the cloud agreed
// to window response bodies.
//
// On DRAIN (story 50.03) it dials a new tunnel after a random wait in
// [0, within_ms), presenting the ticket, and returns it once it is up: the
// cloud sends new requests there, and this tunnel is left to finish the
// streams it has and closes with 4000 when it has none, or at deadline_ms. A
// new tunnel that fails to open is retried with jitter while the old one
// keeps serving. It returns nil when the tunnel simply ended.
func (s *Supervisor) serveTunnel(ctx context.Context, conn FrameConn, flow bool, dial func(context.Context) (DialResult, error)) *DialResult {
	drains := make(chan DrainPayload, 1)
	control := func(f Frame) {
		switch f.Kind {
		case KindTicket:
			var p TicketPayload
			if json.Unmarshal(f.Payload, &p) == nil {
				s.setTicket(p.Ticket)
			}
		case KindDrain:
			var p DrainPayload
			if json.Unmarshal(f.Payload, &p) != nil {
				return
			}
			s.setTicket(p.Ticket)
			select {
			case drains <- p: // a second DRAIN while moving is ignored
			default:
			}
		}
	}
	var opts []MuxOption
	if !s.legacy {
		opts = append(opts, WithControl(control))
	}
	if flow {
		opts = append(opts, WithFlowControl())
	}
	mux := NewMultiplexer(conn, s.Proxy, opts...)
	handedOff := false
	defer func() {
		if !handedOff {
			mux.Close()
			s.setCounters(mux.Stats())
		}
	}()

	pingEvery := s.PingEvery
	if pingEvery <= 0 {
		pingEvery = 25 * time.Second
	}
	pongWait := s.PongWait
	if pongWait <= 0 {
		pongWait = pingEvery + 10*time.Second
	}
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	check := time.NewTicker(max(pongWait/4, time.Millisecond))
	defer check.Stop()

	moveCtx, stopMove := context.WithCancel(ctx)
	defer stopMove()
	moved := make(chan DialResult)
	var deadline time.Time
	moving := false

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-mux.Done():
			if err := mux.Err(); err != nil {
				s.setStatus("reconnecting", err.Error(), 1)
			}
			return nil
		case p := <-drains:
			if moving {
				continue
			}
			moving = true
			deadline = time.Now().Add(time.Duration(p.DeadlineMS) * time.Millisecond)
			go s.move(moveCtx, p, dial, moved)
		case next := <-moved:
			handedOff = true
			go s.finishDrained(mux, deadline)
			return &next
		case <-check.C:
			if time.Since(mux.LastRead()) > pongWait {
				mux.fatal(ErrTunnelSilent)
				s.setStatus("reconnecting", ErrTunnelSilent.Error(), 1)
				return nil
			}
		case <-t.C:
			// Client-initiated keepalive. The cloud's tunnel.go
			// dispatch answers KindPing with KindPong; a write failure
			// here means the socket is dead and Done() will fire.
			if err := mux.writeFrame(Frame{Kind: KindPing}); err != nil {
				return nil
			}
		}
	}
}

// move opens the tunnel that replaces a drained one: a random wait in
// [0, within_ms), then dials (with the ticket the dialer holds) until one is
// up or ctx ends, and hands it over on out.
func (s *Supervisor) move(ctx context.Context, p DrainPayload, dial func(context.Context) (DialResult, error), out chan<- DialResult) {
	if !sleepCtx(ctx, time.Duration(s.rand01()*float64(time.Duration(p.WithinMS)*time.Millisecond))) {
		return
	}
	base := s.BaseWait
	if base <= 0 {
		base = time.Second
	}
	maxW := s.MaxWait
	if maxW <= 0 {
		maxW = 30 * time.Second
	}
	for attempt := 1; ; attempt++ {
		res, err := dial(ctx)
		if err == nil {
			select {
			case out <- res:
			case <-ctx.Done():
				_ = res.Conn.Close()
			}
			return
		}
		if !sleepCtx(ctx, s.jitter(attempt, base, maxW)) {
			return
		}
	}
}

// drainPoll is how often a drained tunnel is checked for its last stream.
var drainPoll = 50 * time.Millisecond

// finishDrained lets the old tunnel finish its streams, then closes it with
// 4000: when it has none, or at the deadline, whichever comes first.
func (s *Supervisor) finishDrained(mux *Multiplexer, deadline time.Time) {
	defer s.setCounters(mux.Stats())
	tk := time.NewTicker(drainPoll)
	defer tk.Stop()
	for {
		if mux.Active() == 0 || !time.Now().Before(deadline) {
			mux.CloseNormal()
			return
		}
		select {
		case <-mux.Done():
			return
		case <-tk.C:
		}
	}
}

// sleepCtx sleeps for d or until ctx is done. Returns false if ctx
// ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
