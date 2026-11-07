package relay

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// The tunnel directory (Epic 50 story 50.04): which relay process holds each
// home's tunnel, so a process without it forwards the request to the one
// with it instead of answering 503. Written where the registry records a
// server online or offline (TunnelServer's Up / Down callbacks).
//
// Tunnel seqs order tunnels across processes: a seq is the time the tunnel
// was made in nanoseconds (tunnelSeq), so a reconnect to another process on
// the same node (or a chrony-synced peer) has the higher seq. Up writes a
// row only when its seq is higher; Down deletes one only when the relay and
// the seq still match, so a newer tunnel elsewhere is never erased.

// Peer is a relay process that holds a tunnel.
type Peer struct {
	RelayID string
	Addr    string // host:port of its peer listener
	Seq     uint64
}

// Directory is what the relay reads and writes about where tunnels are.
type Directory interface {
	// Up records that this process holds slug's tunnel (seq).
	Up(ctx context.Context, slug, serverID string, seq uint64) error
	// Down removes the record of that tunnel, if it is still this process's.
	Down(ctx context.Context, slug string, seq uint64) error
	// Lookup finds another live process holding slug's tunnel.
	Lookup(ctx context.Context, slug string) (Peer, bool)
	// Forget drops a cached lookup (its peer did not answer).
	Forget(slug string)
}

// Directory timings.
const (
	NodeHeartbeat = 5 * time.Second  // relay_nodes.heartbeat_at is written this often
	NodeDeadAfter = 20 * time.Second // a node silent this long is dead
	lookupCache   = 2 * time.Second
)

// PGDirectory is the directory in the cloud database (relay_tunnels,
// relay_nodes; migration 00500002).
type PGDirectory struct {
	DB      *sql.DB
	RelayID string
	Addr    string
	// Draining is reported in this node's heartbeat row.
	Draining func() bool

	mu    sync.Mutex
	cache map[string]cached
	now   func() time.Time
}

type cached struct {
	peer Peer
	ok   bool
	at   time.Time
}

// NewPGDirectory is the directory for relay process relayID, reached by its
// peers at addr.
func NewPGDirectory(db *sql.DB, relayID, addr string) *PGDirectory {
	return &PGDirectory{DB: db, RelayID: relayID, Addr: addr, cache: map[string]cached{}, now: time.Now}
}

// Up upserts slug's row when seq is newer than the one recorded.
func (d *PGDirectory) Up(ctx context.Context, slug, serverID string, seq uint64) error {
	_, err := d.DB.ExecContext(ctx, `
        INSERT INTO relay_tunnels (slug, server_id, relay_id, relay_addr, seq, connected_at)
        VALUES ($1, $2::uuid, $3, $4, $5, now())
        ON CONFLICT (slug) DO UPDATE
           SET server_id = EXCLUDED.server_id, relay_id = EXCLUDED.relay_id,
               relay_addr = EXCLUDED.relay_addr, seq = EXCLUDED.seq, connected_at = now()
         WHERE relay_tunnels.seq < EXCLUDED.seq`,
		slug, serverID, d.RelayID, d.Addr, int64(seq))
	d.Forget(slug)
	return err
}

// Down deletes slug's row only when it is still this process's tunnel seq.
func (d *PGDirectory) Down(ctx context.Context, slug string, seq uint64) error {
	_, err := d.DB.ExecContext(ctx, `DELETE FROM relay_tunnels WHERE slug = $1 AND relay_id = $2 AND seq = $3`,
		slug, d.RelayID, int64(seq))
	d.Forget(slug)
	return err
}

// Lookup reads slug's row (cached 2 s), ignoring this process and dead nodes.
func (d *PGDirectory) Lookup(ctx context.Context, slug string) (Peer, bool) {
	d.mu.Lock()
	c, hit := d.cache[slug]
	d.mu.Unlock()
	if hit && d.now().Sub(c.at) < lookupCache {
		return c.peer, c.ok
	}
	var p Peer
	var seq int64
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	err := d.DB.QueryRowContext(ctx, `
        SELECT t.relay_id, t.relay_addr, t.seq FROM relay_tunnels t
          JOIN relay_nodes n ON n.relay_id = t.relay_id
         WHERE t.slug = $1 AND t.relay_id <> $2
           AND n.heartbeat_at > now() - make_interval(secs => $3)`,
		slug, d.RelayID, NodeDeadAfter.Seconds()).Scan(&p.RelayID, &p.Addr, &seq)
	p.Seq = uint64(seq)
	ok := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// the database is away (50.05): keep using the last answer we had
		if hit {
			return c.peer, c.ok
		}
		return Peer{}, false
	}
	d.mu.Lock()
	d.cache[slug] = cached{peer: p, ok: ok, at: d.now()}
	d.mu.Unlock()
	return p, ok
}

// Forget drops slug's cached lookup.
func (d *PGDirectory) Forget(slug string) {
	d.mu.Lock()
	delete(d.cache, slug)
	d.mu.Unlock()
}

// Start clears what an earlier run of this relay id left (its tunnels are
// gone with it), then heartbeats every NodeHeartbeat until ctx ends, each
// time removing dead nodes and their tunnel rows.
func (d *PGDirectory) Start(ctx context.Context) error {
	if _, err := d.DB.ExecContext(ctx, `DELETE FROM relay_tunnels WHERE relay_id = $1`, d.RelayID); err != nil {
		return err
	}
	if err := d.Heartbeat(ctx); err != nil {
		return err
	}
	go func() {
		tk := time.NewTicker(NodeHeartbeat)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				_ = d.Heartbeat(ctx)
			}
		}
	}()
	return nil
}

// Heartbeat writes this node's row and sweeps dead ones.
func (d *PGDirectory) Heartbeat(ctx context.Context) error {
	draining := d.Draining != nil && d.Draining()
	if _, err := d.DB.ExecContext(ctx, `
        INSERT INTO relay_nodes (relay_id, relay_addr, heartbeat_at, draining)
        VALUES ($1, $2, now(), $3)
        ON CONFLICT (relay_id) DO UPDATE
           SET relay_addr = EXCLUDED.relay_addr, heartbeat_at = now(), draining = EXCLUDED.draining`,
		d.RelayID, d.Addr, draining); err != nil {
		return err
	}
	return d.Sweep(ctx)
}

// Sweep removes the tunnel rows of nodes silent for NodeDeadAfter (their
// homes reconnect anyway), and the nodes themselves.
func (d *PGDirectory) Sweep(ctx context.Context) error {
	_, err := d.DB.ExecContext(ctx, `
        WITH dead AS (
            DELETE FROM relay_nodes WHERE heartbeat_at < now() - make_interval(secs => $1) RETURNING relay_id)
        DELETE FROM relay_tunnels WHERE relay_id IN (SELECT relay_id FROM dead)`, NodeDeadAfter.Seconds())
	return err
}

// MemDirectory is a directory in memory shared by relay processes in one
// test or rehearsal process (no database). Every entry it holds is live.
type MemDirectory struct {
	mu   sync.Mutex
	rows map[string]memRow
}

type memRow struct {
	peer     Peer
	serverID string
}

// NewMemDirectory is an empty shared directory.
func NewMemDirectory() *MemDirectory { return &MemDirectory{rows: map[string]memRow{}} }

// For is the view of relay process relayID (reached at addr).
func (m *MemDirectory) For(relayID, addr string) Directory {
	return memView{m: m, id: relayID, addr: addr}
}

// Drop removes every row of relayID (its node died: what Sweep does).
func (m *MemDirectory) Drop(relayID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for slug, r := range m.rows {
		if r.peer.RelayID == relayID {
			delete(m.rows, slug)
		}
	}
}

// Row is slug's current entry.
func (m *MemDirectory) Row(slug string) (Peer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[slug]
	return r.peer, ok
}

type memView struct {
	m        *MemDirectory
	id, addr string
}

func (v memView) Up(_ context.Context, slug, serverID string, seq uint64) error {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	if cur, ok := v.m.rows[slug]; ok && cur.peer.Seq >= seq {
		return nil
	}
	v.m.rows[slug] = memRow{peer: Peer{RelayID: v.id, Addr: v.addr, Seq: seq}, serverID: serverID}
	return nil
}

func (v memView) Down(_ context.Context, slug string, seq uint64) error {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	if cur, ok := v.m.rows[slug]; ok && cur.peer.RelayID == v.id && cur.peer.Seq == seq {
		delete(v.m.rows, slug)
	}
	return nil
}

func (v memView) Lookup(_ context.Context, slug string) (Peer, bool) {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	r, ok := v.m.rows[slug]
	if !ok || r.peer.RelayID == v.id {
		return Peer{}, false
	}
	return r.peer, true
}

func (memView) Forget(string) {}
