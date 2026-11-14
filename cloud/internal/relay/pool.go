package relay

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// A pool of relays (Epic 50 story 50.16). The cloud decides which relays a home uses: for each
// home, weighted rendezvous hashing over the healthy relays ranks them, the first is the
// home's primary (its tunnel) and the second its standby (an idle second tunnel). Any relay
// answers for any home through the tunnel directory (50.04), so a stale view or a stale app
// hint costs one hop, never a failure.
//
// A pool member is a relay machine ("r1", "r2", …): the relays table. The relay processes on it
// (a blue/green pair, 50.08) share its member id and each heartbeat its row; the tunnel
// directory's relay_nodes stay per process.

// Relay states (relays.state).
const (
	RelayActive   = "active"
	RelayDraining = "draining"
	RelayDown     = "down"
)

// Pool timings (story 50.16 §1, §2).
const (
	PoolHeartbeat    = 5 * time.Second  // a relay writes its row this often
	PoolHealthyAge   = 15 * time.Second // a heartbeat older than this: not healthy
	PoolListCache    = 5 * time.Second  // each node reads the list this often at most
	PoolSettleBefore = time.Minute      // a relay healthy this long takes its homes back
)

// PoolRelay is one row of the relays table. Age is how old its heartbeat was when read (by the
// database's clock, so nodes' clocks never skew the health rule).
type PoolRelay struct {
	ID           string
	PublicIP     string
	PrivateIP    string
	State        string
	Age          time.Duration
	CapacityMbps int
}

// Healthy: active, with a heartbeat under PoolHealthyAge old.
func (r PoolRelay) Healthy() bool { return r.State == RelayActive && r.Age < PoolHealthyAge }

// Assignment is a home's place in the pool: its primary relay and its standby ("" when the pool
// has one healthy relay).
type Assignment struct {
	Primary string `json:"primary"`
	Standby string `json:"standby,omitempty"`
}

// Has reports whether relay id is the home's primary or standby.
func (a Assignment) Has(id string) bool { return id != "" && (a.Primary == id || a.Standby == id) }

// rendezvousScore is relay relayID's weighted rendezvous score for home serverID:
// -capacity / ln(u), u the first 53 bits of SHA-256(serverID ‖ relayID) as a number in (0, 1).
// The highest score wins; a relay with twice the capacity wins about twice as many homes.
func rendezvousScore(serverID, relayID string, capacity int) float64 {
	sum := sha256.Sum256([]byte(serverID + relayID))
	u := (float64(binary.BigEndian.Uint64(sum[:8])>>11) + 0.5) / (1 << 53)
	return -float64(max(capacity, 1)) / math.Log(u)
}

// Assign ranks relays for home serverID: the best score is its primary, the second its standby.
// Deterministic: any node with the same list gives the same answer. relays should hold the
// healthy ones only.
func Assign(serverID string, relays []PoolRelay) Assignment {
	var a Assignment
	best, second := math.Inf(-1), math.Inf(-1)
	for _, r := range relays {
		s := rendezvousScore(serverID, r.ID, r.CapacityMbps)
		switch {
		case s > best || (s == best && r.ID < a.Primary):
			a.Standby, second = a.Primary, best
			a.Primary, best = r.ID, s
		case s > second || (s == second && r.ID < a.Standby):
			a.Standby, second = r.ID, s
		}
	}
	return a
}

// PoolStore is the relays table.
type PoolStore interface {
	// Relays lists every member, healthy or not.
	Relays(ctx context.Context) ([]PoolRelay, error)
	// Beat writes member r's heartbeat (and its addresses and capacity); never its state.
	Beat(ctx context.Context, r PoolRelay) error
}

// Pool is one node's view of the pool: the healthy relays, read at most every PoolListCache, so
// a new or dead relay is seen within PoolHealthyAge + PoolListCache (20 s).
type Pool struct {
	Store PoolStore
	// Self is this relay's member id ("" for a node that only reads: the api).
	Self string

	now func() time.Time

	mu      sync.Mutex
	healthy []PoolRelay
	readAt  time.Time
	read    bool
	since   map[string]time.Time // when each healthy relay was first seen healthy, unbroken
	known   map[string]bool      // every relay this node has seen healthy
}

// NewPool is a view of the pool in store, for member self.
func NewPool(store PoolStore, self string) *Pool {
	return &Pool{Store: store, Self: self, now: time.Now, since: map[string]time.Time{}, known: map[string]bool{}}
}

func (p *Pool) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// Healthy is the healthy relays, sorted by id. When the store cannot be read, the last list
// read is kept (a database blip moves no home).
func (p *Pool) Healthy(ctx context.Context) []PoolRelay {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	if p.read && now.Sub(p.readAt) < PoolListCache {
		return p.healthy
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	all, err := p.Store.Relays(ctx)
	if err != nil {
		if p.read {
			p.readAt = now // retried after the cache window, not on every request
		}
		return p.healthy
	}
	out := make([]PoolRelay, 0, len(all))
	seen := map[string]bool{}
	for _, r := range all {
		if !r.Healthy() {
			continue
		}
		out = append(out, r)
		seen[r.ID] = true
		if _, ok := p.since[r.ID]; !ok {
			if p.known[r.ID] {
				p.since[r.ID] = now // back after this node saw it go: it waits to settle
			} else {
				p.since[r.ID] = time.Time{} // new to this node: settled
			}
			p.known[r.ID] = true
		}
	}
	for id := range p.since {
		if !seen[id] {
			delete(p.since, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	p.healthy, p.readAt, p.read = out, now, true
	return out
}

// Assign is home serverID's assignment over the healthy relays; false when there are none.
func (p *Pool) Assign(ctx context.Context, serverID string) (Assignment, bool) {
	h := p.Healthy(ctx)
	if len(h) == 0 {
		return Assignment{}, false
	}
	return Assign(serverID, h), true
}

// Settled reports whether relay id has been seen healthy, unbroken, for PoolSettleBefore: a
// relay back from a blip (this node saw it go) does not pull its homes back until it has stayed
// up. A relay new to this node is settled at once.
func (p *Pool) Settled(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	at, ok := p.since[id]
	return ok && p.clock().Sub(at) >= PoolSettleBefore
}

// Start heartbeats member self every PoolHeartbeat until ctx ends. The first beat is written
// before it returns, so the relay is in the pool when it starts taking tunnels.
func (p *Pool) Start(ctx context.Context, self PoolRelay) error {
	if err := p.Store.Beat(ctx, self); err != nil {
		return err
	}
	go func() {
		tk := time.NewTicker(PoolHeartbeat)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				bctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				_ = p.Store.Beat(bctx, self)
				cancel()
			}
		}
	}()
	return nil
}

// PGPoolStore is the relays table in the cloud database (migration 00500015).
type PGPoolStore struct{ DB *sql.DB }

// Relays reads every member, its heartbeat's age by the database's clock.
func (s PGPoolStore) Relays(ctx context.Context) ([]PoolRelay, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, host(public_ip), host(private_ip), state,
               extract(epoch FROM now() - heartbeat_at), capacity_mbps
          FROM relays ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PoolRelay
	for rows.Next() {
		var r PoolRelay
		var age float64
		if err := rows.Scan(&r.ID, &r.PublicIP, &r.PrivateIP, &r.State, &age, &r.CapacityMbps); err != nil {
			return nil, err
		}
		r.Age = time.Duration(age * float64(time.Second))
		out = append(out, r)
	}
	return out, rows.Err()
}

// Beat upserts member r's row: its addresses, capacity and heartbeat. A new member starts
// active; an existing member's state is left as a deploy or staff set it.
func (s PGPoolStore) Beat(ctx context.Context, r PoolRelay) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO relays (id, public_ip, private_ip, state, heartbeat_at, capacity_mbps)
        VALUES ($1, $2::inet, $3::inet, 'active', now(), $4)
        ON CONFLICT (id) DO UPDATE
           SET public_ip = EXCLUDED.public_ip, private_ip = EXCLUDED.private_ip,
               heartbeat_at = now(), capacity_mbps = EXCLUDED.capacity_mbps`,
		r.ID, r.PublicIP, r.PrivateIP, r.CapacityMbps)
	return err
}

// SetState sets member id's state (active, draining, down): staff's tool and a deploy's.
func (s PGPoolStore) SetState(ctx context.Context, id, state string) (bool, error) {
	switch state {
	case RelayActive, RelayDraining, RelayDown:
	default:
		return false, errBadRelayState
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE relays SET state = $2 WHERE id = $1`, id, state)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

var errBadRelayState = poolError("relay: a relay's state is active, draining or down")

type poolError string

func (e poolError) Error() string { return string(e) }

// MemPoolStore is a relays table in memory (tests, the in-process pool).
type MemPoolStore struct {
	mu     sync.Mutex
	relays map[string]PoolRelay
	beats  map[string]time.Time
	now    func() time.Time
}

// NewMemPoolStore is an empty pool.
func NewMemPoolStore() *MemPoolStore {
	return &MemPoolStore{relays: map[string]PoolRelay{}, beats: map[string]time.Time{}, now: time.Now}
}

// Relays lists the members, each heartbeat's age by the store's clock.
func (m *MemPoolStore) Relays(context.Context) ([]PoolRelay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PoolRelay, 0, len(m.relays))
	for id, r := range m.relays {
		r.Age = m.now().Sub(m.beats[id])
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Beat records r's heartbeat now (a new member starts active).
func (m *MemPoolStore) Beat(_ context.Context, r PoolRelay) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.relays[r.ID]; ok {
		r.State = cur.State
	} else if r.State == "" {
		r.State = RelayActive
	}
	m.relays[r.ID] = r
	m.beats[r.ID] = m.now()
	return nil
}

// SetState sets member id's state.
func (m *MemPoolStore) SetState(id, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.relays[id]
	r.State = state
	m.relays[id] = r
}

// Stop makes member id's heartbeat stale at once (its machine powered off).
func (m *MemPoolStore) Stop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beats[id] = m.now().Add(-PoolHealthyAge - time.Second)
}

// TunnelHost is the host a member's tunnel is dialled at: tmpl with {id} replaced
// ("{id}.relay.maktaba.tv" → "r3.relay.maktaba.tv").
func TunnelHost(tmpl, id string) string { return strings.ReplaceAll(tmpl, "{id}", id) }
