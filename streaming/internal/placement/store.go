package placement

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Placement states.
const (
	StatePlacing  = "placing"
	StateLive     = "live"
	StateMoving   = "moving"
	StateReleased = "released"
)

// Placement reasons.
const (
	ReasonFirstRequest = "first_request"
	ReasonFailover     = "failover"
	ReasonDrain        = "drain"
	ReasonMove         = "move"
	ReasonPin          = "pin"
	ReasonRemotePush   = "remote_push"
)

// Placement is a channel_placements row.
type Placement struct {
	Channel  uuid.UUID
	Host     string
	Gen      int64
	State    string
	Reason   string
	PlacedAt time.Time
	AckedAt  *time.Time
	// Age is how long ago it was placed (database clock).
	Age time.Duration
}

// Active reports whether the placement holds its host (placing, live or moving).
func (p Placement) Active() bool {
	return p.State == StatePlacing || p.State == StateLive || p.State == StateMoving
}

// Takeover reports whether a station starting for this placement takes the channel
// over from another host (its HLS starts with a discontinuity).
func (p Placement) Takeover() bool {
	return p.Reason == ReasonFailover || p.Reason == ReasonMove || p.Reason == ReasonDrain
}

func (p Placement) String() string {
	return fmt.Sprintf("%s:%s:%d", p.Channel, p.Host, p.Gen)
}

// Decision is what Store.Place's decide callback returns: keep the current row, or
// place on Host with Reason.
type Decision struct {
	Keep   bool
	Host   string
	Reason string
}

// Store is the placement table.
type Store interface {
	HostSource
	Get(ctx context.Context, ch uuid.UUID) (Placement, bool, error)
	// Place runs decide under the channel's lock (pg_advisory_xact_lock) with the
	// current row (nil: none) and the active placements per host (the channel's own
	// excluded), and writes a new generation for a non-Keep decision (NOTIFY
	// tv_placement).
	Place(ctx context.Context, ch uuid.UUID, decide func(cur *Placement, used map[string]int) (Decision, error)) (Placement, error)
	// Stop releases the channel everywhere with a new generation (every station of it stops).
	Stop(ctx context.Context, ch uuid.UUID) (Placement, bool, error)
	Ack(ctx context.Context, ch uuid.UUID, gen int64) (bool, error)
	Release(ctx context.Context, ch uuid.UUID, gen int64) (bool, error)
	OnHost(ctx context.Context, host string) ([]Placement, error)
	All(ctx context.Context) ([]Placement, error)
	ChannelSlug(ctx context.Context, ch uuid.UUID) (string, error)
	HasLiveBlocks(ctx context.Context, ch uuid.UUID, within time.Duration) (bool, error)
}

// MemStore is an in-memory Store (tests; a single machine without Postgres).
type MemStore struct {
	mu       sync.Mutex
	rows     map[uuid.UUID]Placement
	gen      int64
	hosts    []Host
	slugs    map[uuid.UUID]string
	live     map[uuid.UUID]bool
	Now      func() time.Time
	OnNotify func(Placement)
	locks    sync.Map // channel → *sync.Mutex
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{rows: map[uuid.UUID]Placement{}, slugs: map[uuid.UUID]string{}, live: map[uuid.UUID]bool{}}
}

// SetHosts replaces the host list.
func (m *MemStore) SetHosts(h []Host) {
	m.mu.Lock()
	m.hosts = append([]Host(nil), h...)
	m.mu.Unlock()
}

// SetSlug / SetLive set channel facts.
func (m *MemStore) SetSlug(ch uuid.UUID, slug string) { m.mu.Lock(); m.slugs[ch] = slug; m.mu.Unlock() }
func (m *MemStore) SetLive(ch uuid.UUID, v bool)      { m.mu.Lock(); m.live[ch] = v; m.mu.Unlock() }

// Put writes a row as is (tests).
func (m *MemStore) Put(p Placement) {
	m.mu.Lock()
	m.rows[p.Channel] = p
	if p.Gen > m.gen {
		m.gen = p.Gen
	}
	m.mu.Unlock()
}

func (m *MemStore) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *MemStore) Hosts(context.Context) ([]Host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Host(nil), m.hosts...), nil
}

func (m *MemStore) withAge(p Placement) Placement {
	p.Age = m.now().Sub(p.PlacedAt)
	return p
}

func (m *MemStore) Get(_ context.Context, ch uuid.UUID) (Placement, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[ch]
	return m.withAge(p), ok, nil
}

func (m *MemStore) chLock(ch uuid.UUID) *sync.Mutex {
	l, _ := m.locks.LoadOrStore(ch, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// global serialises decisions for capacity (Postgres: the per-channel advisory lock
// plus the counts read in the same transaction; two channels at once are serialised
// there by the row writes. Here one mutex keeps it simple).
var memGlobal sync.Mutex

func (m *MemStore) Place(_ context.Context, ch uuid.UUID, decide func(cur *Placement, used map[string]int) (Decision, error)) (Placement, error) {
	l := m.chLock(ch)
	l.Lock()
	defer l.Unlock()
	memGlobal.Lock()
	defer memGlobal.Unlock()
	m.mu.Lock()
	var cur *Placement
	if p, ok := m.rows[ch]; ok {
		p = m.withAge(p)
		cur = &p
	}
	used := map[string]int{}
	for id, p := range m.rows {
		if id != ch && p.Active() {
			used[p.Host]++
		}
	}
	m.mu.Unlock()
	d, err := decide(cur, used)
	if err != nil {
		return Placement{}, err
	}
	if d.Keep && cur != nil {
		return *cur, nil
	}
	m.mu.Lock()
	m.gen++
	p := Placement{Channel: ch, Host: d.Host, Gen: m.gen, State: StatePlacing, Reason: d.Reason, PlacedAt: m.now()}
	if d.Reason == ReasonMove || d.Reason == ReasonDrain {
		p.State = StateMoving
	}
	m.rows[ch] = p
	notify := m.OnNotify
	m.mu.Unlock()
	if notify != nil {
		notify(p)
	}
	return p, nil
}

func (m *MemStore) Stop(_ context.Context, ch uuid.UUID) (Placement, bool, error) {
	m.mu.Lock()
	p, ok := m.rows[ch]
	if !ok || p.State == StateReleased {
		m.mu.Unlock()
		return p, false, nil
	}
	m.gen++
	p.Gen, p.State = m.gen, StateReleased
	m.rows[ch] = p
	notify := m.OnNotify
	m.mu.Unlock()
	if notify != nil {
		notify(p)
	}
	return p, true, nil
}

func (m *MemStore) Ack(_ context.Context, ch uuid.UUID, gen int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[ch]
	if !ok || p.Gen != gen || !p.Active() {
		return false, nil
	}
	t := m.now()
	p.State, p.AckedAt = StateLive, &t
	m.rows[ch] = p
	return true, nil
}

func (m *MemStore) Release(_ context.Context, ch uuid.UUID, gen int64) (bool, error) {
	m.mu.Lock()
	p, ok := m.rows[ch]
	if !ok || p.Gen != gen || p.State == StateReleased {
		m.mu.Unlock()
		return false, nil
	}
	p.State = StateReleased
	m.rows[ch] = p
	notify := m.OnNotify
	m.mu.Unlock()
	if notify != nil {
		notify(p)
	}
	return true, nil
}

func (m *MemStore) OnHost(_ context.Context, host string) ([]Placement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Placement
	for _, p := range m.rows {
		if p.Host == host && p.Active() {
			out = append(out, m.withAge(p))
		}
	}
	return out, nil
}

func (m *MemStore) All(_ context.Context) ([]Placement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Placement
	for _, p := range m.rows {
		out = append(out, m.withAge(p))
	}
	return out, nil
}

func (m *MemStore) ChannelSlug(_ context.Context, ch uuid.UUID) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.slugs[ch], nil
}

func (m *MemStore) HasLiveBlocks(_ context.Context, ch uuid.UUID, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live[ch], nil
}
