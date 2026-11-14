package cloudlink

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// The relay pool, the home's side (Epic 50 story 50.16). A relay in a pool answers AUTH with the
// home's assignment: its primary relay and its standby. The home keeps its primary tunnel on
// the first and an idle standby tunnel on the second (PINGs only; it carries requests only once
// the primary's relay is gone and the relays fall through to it). When the primary tunnel drops,
// the home dials its standby's relay first: requests are already flowing there, and that relay
// retires the standby once the new primary is up. DRAIN (rebalance) and RELAYS bring a new
// assignment; the standby follows it.

// poolState is the home's place in the pool, as its relays last told it.
type poolState struct {
	mu      sync.Mutex
	relays  *RelaysPayload // nil: no pool (one tunnel, to the Endpoint)
	here    string         // the member the primary tunnel is on
	failed  string         // the host whose primary tunnel last dropped: dialled last
	standby string         // the member the standby tunnel is on ("" none)
	kick    chan struct{}  // wakes the standby loop: the assignment or the primary moved
}

func (p *poolState) wake() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// pool is s's pool state, made on first use.
func (s *Supervisor) pool() *poolState {
	s.poolOnce.Do(func() { s.ps = &poolState{kick: make(chan struct{}, 1)} })
	return s.ps
}

// adoptPrimary records what a new primary tunnel's AUTH_OK said: the assignment (nil: the relay
// runs no pool) and the member it is on.
func (s *Supervisor) adoptPrimary(res DialResult) {
	p := s.pool()
	p.mu.Lock()
	p.relays = res.Relays
	p.here = ""
	if res.Relays != nil {
		p.here = res.Relays.Here
	}
	p.failed = ""
	p.mu.Unlock()
	p.wake()
}

// adoptRelays records a new assignment (RELAYS, or DRAIN's).
func (s *Supervisor) adoptRelays(r *RelaysPayload) {
	if r == nil || r.Primary.Host == "" {
		return
	}
	p := s.pool()
	p.mu.Lock()
	cp := *r
	p.relays = &cp
	p.mu.Unlock()
	p.wake()
}

// primaryDropped records that the primary tunnel on res's relay dropped (not a drain): its host
// is dialled last next time.
func (s *Supervisor) primaryDropped(res DialResult) {
	p := s.pool()
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case res.Host != "":
		p.failed = res.Host
	case p.relays != nil && p.relays.Here == p.relays.Primary.ID:
		p.failed = p.relays.Primary.Host
	case p.relays != nil && p.relays.Standby != nil && p.relays.Here == p.relays.Standby.ID:
		p.failed = p.relays.Standby.Host
	}
	p.here = ""
}

// primaryTargets are the hosts a primary tunnel is dialled at, in order: the assigned primary,
// the standby, then the Endpoint's own ("") ; the host that just dropped goes last.
func (s *Supervisor) primaryTargets() []string {
	p := s.pool()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.relays == nil {
		return []string{""}
	}
	hosts := []string{p.relays.Primary.Host}
	if p.relays.Standby != nil {
		hosts = append(hosts, p.relays.Standby.Host)
	}
	if p.failed != "" {
		kept := hosts[:0:0]
		for _, h := range hosts {
			if h != p.failed {
				kept = append(kept, h)
			}
		}
		if len(kept) < len(hosts) {
			hosts = append(kept, p.failed)
		}
	}
	return append(hosts, "")
}

// primaryDial dials a primary tunnel at the first of primaryTargets that answers.
func (s *Supervisor) primaryDial(ctx context.Context) (DialResult, error) {
	var last error
	for _, h := range s.primaryTargets() {
		res, err := s.Dialer.DialTo(ctx, h, "")
		if err == nil {
			return res, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return DialResult{}, last
}

// standbyTarget is the relay the standby tunnel belongs on: the assignment's standby, unless the
// primary tunnel is on it (the home moved there when its primary's relay went), or none.
func (s *Supervisor) standbyTarget() relay.Target {
	p := s.pool()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.relays == nil || p.relays.Standby == nil || p.relays.Standby.ID == p.here || p.here == "" {
		return relay.Target{}
	}
	return *p.relays.Standby
}

func (s *Supervisor) setStandby(id string) {
	p := s.pool()
	p.mu.Lock()
	p.standby = id
	p.mu.Unlock()
	s.mu.Lock()
	s.state.StandbyRelay = id
	s.mu.Unlock()
}

// runStandby keeps the standby tunnel on standbyTarget until ctx ends: dials it, serves it, and
// moves it when the assignment does.
func (s *Supervisor) runStandby(ctx context.Context) {
	base := s.BaseWait
	if base <= 0 {
		base = time.Second
	}
	maxW := s.MaxWait
	if maxW <= 0 {
		maxW = 30 * time.Second
	}
	kick := s.pool().kick
	attempt := 0
	for ctx.Err() == nil {
		target := s.standbyTarget()
		if target.Host == "" {
			select {
			case <-ctx.Done():
				return
			case <-kick:
			}
			continue
		}
		res, err := s.Dialer.DialTo(ctx, target.Host, relay.RoleStandby)
		if err != nil {
			attempt++
			select {
			case <-ctx.Done():
				return
			case <-kick: // a new assignment: try it at once
			case <-time.After(s.jitter(attempt, base, maxW)):
			}
			continue
		}
		s.setStandby(target.ID)
		began := time.Now()
		s.serveStandby(ctx, res, target)
		s.setStandby("")
		if time.Since(began) >= standbyStable {
			attempt = 0
			continue
		}
		// refused or dropped at once (the relay holds our primary, or is going): back off
		attempt++
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-time.After(s.jitter(attempt, base, maxW)):
		}
	}
}

// standbyStable is how long a standby tunnel must last for its next dial to skip the backoff.
const standbyStable = 10 * time.Second

// standbyRetire bounds how long a standby tunnel the home is leaving keeps serving its requests.
var standbyRetire = 2 * time.Minute

// serveStandby runs the standby tunnel until it ends, ctx ends, or it no longer belongs on its
// relay (a DRAIN, or a new assignment): then it finishes its requests and closes with 4000.
func (s *Supervisor) serveStandby(ctx context.Context, res DialResult, target relay.Target) {
	moved := make(chan struct{}, 1)
	control := func(f Frame) {
		switch f.Kind {
		case KindTicket:
			var p TicketPayload
			if json.Unmarshal(f.Payload, &p) == nil {
				s.setTicket(p.Ticket)
			}
		case KindRelays:
			var p RelaysPayload
			if json.Unmarshal(f.Payload, &p) == nil {
				s.adoptRelays(&p)
			}
		case KindDrain:
			var p DrainPayload
			if json.Unmarshal(f.Payload, &p) == nil {
				s.setTicket(p.Ticket)
				s.adoptRelays(p.Relays)
			}
			select {
			case moved <- struct{}{}:
			default:
			}
		}
	}
	opts := []MuxOption{WithControl(control)}
	if res.Flow {
		opts = append(opts, WithFlowControl())
	}
	mux := NewMultiplexer(res.Conn, s.Proxy, opts...)
	pingEvery := s.PingEvery
	if pingEvery <= 0 {
		pingEvery = 25 * time.Second
	}
	pongWait := s.PongWait
	if pongWait <= 0 {
		pongWait = pingEvery + 10*time.Second
	}
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	check := time.NewTicker(max(pongWait/4, time.Millisecond))
	defer check.Stop()
	kick := s.pool().kick
	leave := func() {
		go s.finishDrained(mux, time.Now().Add(standbyRetire))
	}
	for {
		select {
		case <-ctx.Done():
			mux.Close()
			s.setCounters(mux.Stats())
			return
		case <-mux.Done():
			s.setCounters(mux.Stats())
			return
		case <-moved:
			leave()
			return
		case <-kick:
			if s.standbyTarget() != target {
				leave()
				return
			}
		case <-check.C:
			if time.Since(mux.LastRead()) > pongWait {
				mux.fatal(ErrTunnelSilent)
				s.setCounters(mux.Stats())
				return
			}
		case <-ping.C:
			_ = mux.writeFrame(Frame{Kind: KindPing})
		}
	}
}
