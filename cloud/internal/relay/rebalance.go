package relay

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Rebalancing in the relay pool (story 50.16 §6). When the healthy set changes (a relay added,
// drained or dead), each relay checks its own tunnels and sends DRAIN{reason: "rebalance"},
// with the home's assignment, to homes no longer placed on it, at most Rate a second, so a new
// relay fills in minutes without a storm. A home still placed here but whose assignment moved
// (its standby changed) is sent RELAYS. A dead relay needs no drain: its homes are already on
// their standby tunnels.

// Rebalancer defaults (story 50.16 §6).
const (
	RebalanceEvery = 30 * time.Second
	RebalanceRate  = 20
)

// Rebalancer places this relay's homes in the pool: at the handshake (TunnelServer) and every
// Every after (Run).
type Rebalancer struct {
	Registry *Registry
	Pool     *Pool
	// Hosts is the host template a member's tunnel is dialled at ("{id}.relay.maktaba.tv").
	Hosts string
	// Every is how often every tunnel is checked (default RebalanceEvery); the check also runs
	// as soon as the healthy set changes.
	Every time.Duration
	// Rate is the most homes drained a second (default RebalanceRate).
	Rate int
	// Drain is a rebalance drain's timings (Within, Deadline).
	Drain DrainOptions
	// Mint gives a drained home a fresh resume ticket (nil: none).
	Mint   func(serverID string) string
	Logger *slog.Logger

	mu   sync.Mutex
	next time.Time // when the next drain may go
}

func (b *Rebalancer) logger() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

func (b *Rebalancer) gap() time.Duration {
	rate := b.Rate
	if rate <= 0 {
		rate = RebalanceRate
	}
	return time.Second / time.Duration(rate)
}

// reserve books the next drain slot and returns how long to wait for it.
func (b *Rebalancer) reserve() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.next.Before(now) {
		b.next = now
	}
	wait := b.next.Sub(now)
	b.next = b.next.Add(b.gap())
	return wait
}

// tryNow books a drain slot if one is free now (a handshake never waits for one).
func (b *Rebalancer) tryNow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.next.After(now) {
		return false
	}
	b.next = now.Add(b.gap())
	return true
}

// Payload is assignment a as the home is told it.
func (b *Rebalancer) Payload(a Assignment) RelaysPayload {
	p := RelaysPayload{Primary: RelayTarget{ID: a.Primary, Host: TunnelHost(b.Hosts, a.Primary)}, Here: b.Pool.Self}
	if a.Standby != "" {
		p.Standby = &RelayTarget{ID: a.Standby, Host: TunnelHost(b.Hosts, a.Standby)}
	}
	return p
}

// Placed reports whether tunnel t (a primary unless standby) belongs on this relay under a.
// A primary on the home's standby relay stays while its primary relay has not settled: the home
// moved here because that relay was gone, and goes back once it has stayed up (Pool.Settled).
func (b *Rebalancer) Placed(standby bool, a Assignment) bool {
	self := b.Pool.Self
	if standby {
		return a.Standby == self
	}
	return a.Primary == self || (a.Standby == self && !b.Pool.Settled(a.Primary))
}

// AtHandshake places a home's new tunnel: a primary on the wrong relay is drained to its
// assignment at once when a drain slot is free (else the next check does it).
func (b *Rebalancer) AtHandshake(t *Tunnel, a Assignment) {
	t.SetTold(a)
	if t.standby || b.Placed(false, a) || !b.tryNow() {
		return
	}
	b.drain(t, a)
}

func (b *Rebalancer) drain(t *Tunnel, a Assignment) {
	p := b.Payload(a)
	o := b.Drain
	o.Reason = DrainRebalance
	o.Relays = &p
	ticket := ""
	if b.Mint != nil && t.canTicket {
		ticket = b.Mint(t.ServerID)
	}
	t.SetTold(a)
	t.Drain(o, ticket)
	b.logger().Info("relay: a home moved to its place in the pool", "home_h", mlog.HashHome(t.ServerID),
		"standby", t.standby, "primary", a.Primary, "event", "relay_rebalance_drain")
}

// Check goes over this relay's tunnels once: each home misplaced is drained (paced at Rate a
// second), each placed home whose assignment changed is sent RELAYS.
func (b *Rebalancer) Check(ctx context.Context) (drained, told int) {
	healthy := b.Pool.Healthy(ctx)
	if len(healthy) == 0 {
		return 0, 0
	}
	for _, t := range b.Registry.All() {
		if ctx.Err() != nil {
			return drained, told
		}
		if !t.canPool || t.Draining() || t.closed.Load() {
			continue
		}
		a := Assign(t.ServerID, healthy)
		if b.Placed(t.standby, a) {
			if t.Told() != a {
				t.SetTold(a)
				if t.SendRelays(b.Payload(a)) == nil {
					told++
				}
			}
			continue
		}
		if w := b.reserve(); w > 0 && !sleepFor(ctx, w) {
			return drained, told
		}
		if t.Draining() || t.closed.Load() {
			continue
		}
		b.drain(t, a)
		drained++
	}
	return drained, told
}

// Run checks every Every, and as soon as the healthy set changes, until ctx ends.
func (b *Rebalancer) Run(ctx context.Context) {
	every := b.Every
	if every <= 0 {
		every = RebalanceEvery
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	poll := time.NewTicker(PoolListCache)
	defer poll.Stop()
	last := healthyKey(b.Pool.Healthy(ctx))
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		case <-poll.C:
			k := healthyKey(b.Pool.Healthy(ctx))
			if k == last {
				continue
			}
			last = k
		}
		if d, n := b.Check(ctx); d+n > 0 {
			b.logger().Info("relay: pool check", "drained", d, "told", n, "event", "relay_rebalance_check")
		}
	}
}

// healthyKey names a healthy set (its ids and capacities: either changes an assignment).
func healthyKey(rs []PoolRelay) string {
	var sb strings.Builder
	for _, r := range rs {
		sb.WriteString(r.ID)
		sb.WriteByte(':')
		sb.WriteString(strconv.Itoa(r.CapacityMbps))
		sb.WriteByte(',')
	}
	return sb.String()
}

// sleepFor waits d or until ctx ends (false).
func sleepFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
