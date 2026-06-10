package cloudlink

import (
	"context"
	"math"
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

	mu    sync.Mutex
	state LinkState
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
// and reconnecting with exponential backoff on every drop.
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
			if !sleepCtx(ctx, Backoff(attempt, base, maxW)) {
				s.setStatus("stopped", err.Error(), attempt)
				return ctx.Err()
			}
			continue
		}
		s.setOnline(res.Slug)
		connectedAt := time.Now()
		s.serveTunnel(ctx, res.Conn)
		// Tunnel dropped. Only treat this as a "fresh start" (reset the
		// backoff counter) if the tunnel actually stayed up for a
		// meaningful period. A server that accepts then instantly drops
		// must NOT trigger a zero-delay reconnect storm — keep the
		// exponential-backoff counter climbing in that case.
		if time.Since(connectedAt) >= stableThreshold {
			attempt = 0
		} else {
			s.setStatus("reconnecting", "tunnel dropped immediately", attempt)
			if !sleepCtx(ctx, Backoff(attempt, base, maxW)) {
				s.setStatus("stopped", "", attempt)
				return ctx.Err()
			}
		}
	}
}

// stableThreshold is how long a tunnel must stay connected before we
// consider it healthy enough to reset the reconnect backoff counter.
const stableThreshold = 30 * time.Second

// serveTunnel runs one connected tunnel: the multiplexer read loop plus
// a client-side PING keepalive, until the tunnel errors or ctx ends.
func (s *Supervisor) serveTunnel(ctx context.Context, conn FrameConn) {
	mux := NewMultiplexer(conn, s.Proxy)
	defer func() {
		mux.Close()
		s.setCounters(mux.Stats())
	}()

	pingEvery := s.PingEvery
	if pingEvery <= 0 {
		pingEvery = 25 * time.Second
	}
	t := time.NewTicker(pingEvery)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-mux.Done():
			if err := mux.Err(); err != nil {
				s.setStatus("reconnecting", err.Error(), 1)
			}
			return
		case <-t.C:
			// Client-initiated keepalive. The cloud's tunnel.go
			// dispatch answers KindPing with KindPong; a write failure
			// here means the socket is dead and Done() will fire.
			if err := mux.writeFrame(Frame{Kind: KindPing}); err != nil {
				return
			}
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
