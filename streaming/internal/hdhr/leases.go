package hdhr

import (
	"errors"
	"sync"

	"github.com/google/uuid"
)

// ErrAllTunersInUse is returned when concurrent /auto/v{ch} connections
// exceed TunerCount (AC6) — Plex shows "no available tuners".
var ErrAllTunersInUse = errors.New("hdhr: all tuners in use")

// leaseRegistry caps concurrent tuner pulls at TunerCount (D5). One lease
// == one engine MPEG-TS consumer; released on disconnect. Pure, in-memory
// + race-safe so the cap logic is unit-tested without a network.
type leaseRegistry struct {
	mu     sync.Mutex
	cap    int
	active map[uuid.UUID]int // lease id → (unused value)
}

func newLeaseRegistry(capacity int) *leaseRegistry {
	if capacity < 1 {
		capacity = 1
	}
	return &leaseRegistry{cap: capacity, active: map[uuid.UUID]int{}}
}

// lease is a held tuner slot; Release frees it (idempotent).
type lease struct {
	id  uuid.UUID
	reg *leaseRegistry
	one sync.Once
}

// acquire reserves a tuner slot, or returns ErrAllTunersInUse at cap.
func (r *leaseRegistry) acquire() (*lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.active) >= r.cap {
		return nil, ErrAllTunersInUse
	}
	id := uuid.New()
	r.active[id] = 1
	return &lease{id: id, reg: r}, nil
}

// Release frees the slot. Safe to call multiple times.
func (l *lease) Release() {
	l.one.Do(func() {
		l.reg.mu.Lock()
		delete(l.reg.active, l.id)
		l.reg.mu.Unlock()
	})
}

// count reports the number of held leases (tests/metrics).
func (r *leaseRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

// setCap updates the cap (re-read from hdhr_device.tuner_count). Existing
// leases over the new cap are honoured until released.
func (r *leaseRegistry) setCap(c int) {
	if c < 1 {
		c = 1
	}
	r.mu.Lock()
	r.cap = c
	r.mu.Unlock()
}
