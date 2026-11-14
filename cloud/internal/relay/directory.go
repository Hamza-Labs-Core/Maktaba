package relay

import (
	"context"
	"database/sql"
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
//
// Story 50.16 adds a home's standby tunnel, on another relay: its row is in
// relay_standby_tunnels, and a lookup lists the primary first, then the
// standby, so a request reaches the standby at once when the primary's relay
// is gone.

// Peer is a relay process that holds a tunnel.
type Peer struct {
	RelayID string
	Addr    string // host:port of its peer listener
	Seq     uint64
	Role    string // RolePrimary or RoleStandby (story 50.16)
}

// Directory is what the relay reads and writes about where tunnels are.
type Directory interface {
	// Up records that this process holds slug's tunnel (seq) in role (RolePrimary,
	// RoleStandby).
	Up(ctx context.Context, slug, serverID, role string, seq uint64) error
	// Down removes the record of that tunnel, if it is still this process's.
	Down(ctx context.Context, slug, role string, seq uint64) error
	// Lookup finds another live process holding slug's tunnel: the primary's when it is
	// live, else the standby's.
	Lookup(ctx context.Context, slug string) (Peer, bool)
	// Peers lists every other live process holding a tunnel of slug's, primary first.
	Peers(ctx context.Context, slug string) []Peer
	// Forget drops a cached lookup (its peer did not answer).
	Forget(slug string)
}

// tunnelTable is the directory table of a role's rows.
func tunnelTable(role string) string {
	if role == RoleStandby {
		return "relay_standby_tunnels"
	}
	return "relay_tunnels"
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
	peers []Peer
	at    time.Time
}

// NewPGDirectory is the directory for relay process relayID, reached by its
// peers at addr.
func NewPGDirectory(db *sql.DB, relayID, addr string) *PGDirectory {
	return &PGDirectory{DB: db, RelayID: relayID, Addr: addr, cache: map[string]cached{}, now: time.Now}
}

// Up upserts slug's row of role when seq is newer than the one recorded.
func (d *PGDirectory) Up(ctx context.Context, slug, serverID, role string, seq uint64) error {
	table := tunnelTable(role)
	_, err := d.DB.ExecContext(ctx, `
        INSERT INTO `+table+` (slug, server_id, relay_id, relay_addr, seq, connected_at)
        VALUES ($1, $2::uuid, $3, $4, $5, now())
        ON CONFLICT (slug) DO UPDATE
           SET server_id = EXCLUDED.server_id, relay_id = EXCLUDED.relay_id,
               relay_addr = EXCLUDED.relay_addr, seq = EXCLUDED.seq, connected_at = now()
         WHERE `+table+`.seq < EXCLUDED.seq`,
		slug, serverID, d.RelayID, d.Addr, int64(seq))
	d.Forget(slug)
	return err
}

// Down deletes slug's row of role only when it is still this process's tunnel seq.
func (d *PGDirectory) Down(ctx context.Context, slug, role string, seq uint64) error {
	_, err := d.DB.ExecContext(ctx, `DELETE FROM `+tunnelTable(role)+` WHERE slug = $1 AND relay_id = $2 AND seq = $3`,
		slug, d.RelayID, int64(seq))
	d.Forget(slug)
	return err
}

// Lookup is the first of Peers.
func (d *PGDirectory) Lookup(ctx context.Context, slug string) (Peer, bool) {
	ps := d.Peers(ctx, slug)
	if len(ps) == 0 {
		return Peer{}, false
	}
	return ps[0], true
}

// Peers reads slug's rows (cached 2 s), ignoring this process and dead nodes: the primary
// first, then the standby.
func (d *PGDirectory) Peers(ctx context.Context, slug string) []Peer {
	d.mu.Lock()
	c, hit := d.cache[slug]
	d.mu.Unlock()
	if hit && d.now().Sub(c.at) < lookupCache {
		return c.peers
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rows, err := d.DB.QueryContext(ctx, `
        SELECT t.relay_id, t.relay_addr, t.seq, t.role FROM (
                 SELECT relay_id, relay_addr, seq, 'primary' AS role, 0 AS rank FROM relay_tunnels WHERE slug = $1
                 UNION ALL
                 SELECT relay_id, relay_addr, seq, 'standby', 1 FROM relay_standby_tunnels WHERE slug = $1) t
          JOIN relay_nodes n ON n.relay_id = t.relay_id
         WHERE t.relay_id <> $2
           AND n.heartbeat_at > now() - make_interval(secs => $3)
         ORDER BY t.rank`,
		slug, d.RelayID, NodeDeadAfter.Seconds())
	var peers []Peer
	if err == nil {
		for rows.Next() {
			var p Peer
			var seq int64
			if err = rows.Scan(&p.RelayID, &p.Addr, &seq, &p.Role); err != nil {
				break
			}
			p.Seq = uint64(seq)
			peers = append(peers, p)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
	}
	if err != nil {
		// the database is away (50.05): keep using the last answer we had
		if hit {
			return c.peers
		}
		return nil
	}
	d.mu.Lock()
	d.cache[slug] = cached{peers: peers, at: d.now()}
	d.mu.Unlock()
	return peers
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
	if _, err := d.DB.ExecContext(ctx, `DELETE FROM relay_standby_tunnels WHERE relay_id = $1`, d.RelayID); err != nil {
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
            DELETE FROM relay_nodes WHERE heartbeat_at < now() - make_interval(secs => $1) RETURNING relay_id),
        standby AS (
            DELETE FROM relay_standby_tunnels WHERE relay_id IN (SELECT relay_id FROM dead))
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

// memKey is a row's key: the slug, and its role for a standby.
func memKey(slug, role string) string {
	if role == RoleStandby {
		return slug + "\x00standby"
	}
	return slug
}

// For is the view of relay process relayID (reached at addr).
func (m *MemDirectory) For(relayID, addr string) Directory {
	return memView{m: m, id: relayID, addr: addr}
}

// Drop removes every row of relayID (its node died: what Sweep does).
func (m *MemDirectory) Drop(relayID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.rows {
		if r.peer.RelayID == relayID {
			delete(m.rows, k)
		}
	}
}

// Row is slug's current primary entry.
func (m *MemDirectory) Row(slug string) (Peer, bool) { return m.RoleRow(slug, RolePrimary) }

// RoleRow is slug's current entry of role.
func (m *MemDirectory) RoleRow(slug, role string) (Peer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[memKey(slug, role)]
	return r.peer, ok
}

type memView struct {
	m        *MemDirectory
	id, addr string
}

func (v memView) Up(_ context.Context, slug, serverID, role string, seq uint64) error {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	if role != RoleStandby {
		role = RolePrimary
	}
	k := memKey(slug, role)
	if cur, ok := v.m.rows[k]; ok && cur.peer.Seq >= seq {
		return nil
	}
	v.m.rows[k] = memRow{peer: Peer{RelayID: v.id, Addr: v.addr, Seq: seq, Role: role}, serverID: serverID}
	return nil
}

func (v memView) Down(_ context.Context, slug, role string, seq uint64) error {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	k := memKey(slug, role)
	if cur, ok := v.m.rows[k]; ok && cur.peer.RelayID == v.id && cur.peer.Seq == seq {
		delete(v.m.rows, k)
	}
	return nil
}

func (v memView) Lookup(ctx context.Context, slug string) (Peer, bool) {
	ps := v.Peers(ctx, slug)
	if len(ps) == 0 {
		return Peer{}, false
	}
	return ps[0], true
}

func (v memView) Peers(_ context.Context, slug string) []Peer {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	var out []Peer
	for _, role := range []string{RolePrimary, RoleStandby} {
		if r, ok := v.m.rows[memKey(slug, role)]; ok && r.peer.RelayID != v.id {
			out = append(out, r.peer)
		}
	}
	return out
}

func (memView) Forget(string) {}
