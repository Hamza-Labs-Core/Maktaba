package relay

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/lib/pq"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// StandingStore says which homes may hold a tunnel: the server exists and its owner is active.
type StandingStore interface {
	// Standing returns the ids, of those given, that may.
	Standing(ctx context.Context, serverIDs []string) (map[string]bool, error)
}

// Standing reads the servers and their owners, in one query.
func (d dbTunnelStore) Standing(ctx context.Context, serverIDs []string) (map[string]bool, error) {
	rows, err := d.s.DB.QueryContext(ctx, `
        SELECT s.id::text FROM servers s JOIN users u ON u.id = s.owner_user_id
         WHERE s.id = ANY($1::uuid[]) AND u.status = 'active'`, pq.Array(serverIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]bool, len(serverIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Revoker closes the live tunnels of a home that may not hold one any more: its owner was
// suspended, or the server was deleted. The handshake refuses such a home (Authenticate, Resume);
// this ends a tunnel that was already up. Each relay process closes its own.
//
// It hears of a change from the database's server_changed notification (every relay process
// listens; the trigger of migration 00500003 sends it on a suspension and on a deletion, in
// the same transaction), checks each tunnel once it is registered, checks them all when the
// listener reconnects (notifications may have been missed), and all again every Every (default
// 1m) as the backstop. While the database is away it closes nothing: a failover must not drop
// homes.
//
// What is to be checked is a set, and Run, its one worker, reads it in batches, one query at a
// time: a storm of notifications or of reconnects is a few queries, whatever its size, and never
// takes the pool from the handshakes.
type Revoker struct {
	Registry *Registry
	Store    StandingStore
	Logger   *slog.Logger
	Every    time.Duration

	mu      sync.Mutex
	pending map[*Tunnel]struct{}
	all     bool
	wake    chan struct{}
}

// standingBatch bounds one query.
const standingBatch = 1000

// Changed queues slug's tunnels for a check (a server_changed notification).
func (r *Revoker) Changed(slug string) { r.Check(r.Registry.OfSlug(slug)...) }

// Check queues ts for a check.
func (r *Revoker) Check(ts ...*Tunnel) {
	if len(ts) == 0 {
		return
	}
	r.mu.Lock()
	if r.pending == nil {
		r.pending = map[*Tunnel]struct{}{}
	}
	for _, t := range ts {
		r.pending[t] = struct{}{}
	}
	r.mu.Unlock()
	r.signal()
}

// All queues every tunnel for a check.
func (r *Revoker) All() {
	r.mu.Lock()
	r.all = true
	r.mu.Unlock()
	r.signal()
}

func (r *Revoker) wakeCh() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wake == nil {
		r.wake = make(chan struct{}, 1)
	}
	return r.wake
}

func (r *Revoker) signal() {
	select {
	case r.wakeCh() <- struct{}{}:
	default: // the worker is already to look
	}
}

// Run does the checks, until ctx ends. One per Revoker.
func (r *Revoker) Run(ctx context.Context) {
	every := r.Every
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	wake := r.wakeCh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.drainQueue(ctx, r.Registry.All())
		case <-wake:
			r.drainQueue(ctx, nil)
		}
	}
}

// drainQueue checks ts and what is queued.
func (r *Revoker) drainQueue(ctx context.Context, ts []*Tunnel) {
	r.mu.Lock()
	pending, all := r.pending, r.all
	r.pending, r.all = nil, false
	r.mu.Unlock()
	if all && ts == nil {
		ts = r.Registry.All()
	}
	// always the queued ones too: one queued after ts was read is not in it
	for _, t := range ts {
		delete(pending, t)
	}
	for t := range pending {
		ts = append(ts, t)
	}
	for len(ts) > 0 && ctx.Err() == nil {
		n := min(len(ts), standingBatch)
		r.check(ctx, ts[:n])
		ts = ts[n:]
	}
}

// check closes those of ts whose home has no standing.
func (r *Revoker) check(ctx context.Context, ts []*Tunnel) {
	ids := make([]string, 0, len(ts))
	for _, t := range ts {
		if !t.closed.Load() {
			ids = append(ids, t.ServerID)
		}
	}
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ok, err := r.Store.Standing(ctx, ids)
	if err != nil {
		r.logger().Warn("relay: the homes' standing could not be read; checked again within a minute", "err", err, "event", "relay_standing_failed")
		return
	}
	for _, t := range ts {
		if !ok[t.ServerID] && !t.closed.Load() {
			r.logger().Info("relay: tunnel closed: its server is gone or its owner suspended", "home_h", mlog.HashHome(t.ServerID), "event", "tunnel_revoked")
			t.Close()
		}
	}
}

func (r *Revoker) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}
