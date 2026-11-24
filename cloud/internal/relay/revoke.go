package relay

import (
	"context"
	"log/slog"
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
// the same transaction), checks all its tunnels when the listener reconnects (notifications may
// have been missed), and checks them all again every Every (default 1m) as the backstop. While
// the database is away it closes nothing: a failover must not drop homes.
type Revoker struct {
	Registry *Registry
	Store    StandingStore
	Logger   *slog.Logger
	Every    time.Duration
}

// standingBatch bounds one query of a full check.
const standingBatch = 1000

// Changed checks slug's tunnels (a server_changed notification). It does not block the caller.
func (r *Revoker) Changed(slug string) {
	var ts []*Tunnel
	for _, t := range r.Registry.All() {
		if t.Slug == slug {
			ts = append(ts, t)
		}
	}
	if len(ts) > 0 {
		go r.check(ts)
	}
}

// All checks every tunnel. It does not block the caller.
func (r *Revoker) All() { go r.check(r.Registry.All()) }

// Run checks every tunnel each Every, until ctx ends.
func (r *Revoker) Run(ctx context.Context) {
	every := r.Every
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.check(r.Registry.All())
		}
	}
}

// check closes those of ts whose home has no standing.
func (r *Revoker) check(ts []*Tunnel) {
	for len(ts) > 0 {
		n := min(len(ts), standingBatch)
		r.checkBatch(ts[:n])
		ts = ts[n:]
	}
}

func (r *Revoker) checkBatch(ts []*Tunnel) {
	ids := make([]string, 0, len(ts))
	for _, t := range ts {
		ids = append(ids, t.ServerID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ok, err := r.Store.Standing(ctx, ids)
	if err != nil {
		r.logger().Warn("relay: the homes' standing could not be read; checked again within a minute", "err", err, "event", "relay_standing_failed")
		return
	}
	for _, t := range ts {
		if !ok[t.ServerID] {
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
