package channel

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrAtCapacity is returned by Admit when the per-host concurrent-channel
// cap is reached and no warm (zero-viewer) channel can be evicted (D6).
var ErrAtCapacity = errors.New("channel: per-host concurrent-channel cap reached")

// StopFunc is the teardown closure the engine attaches to a live
// channel; the registry calls it on eviction/reap.
type StopFunc func()

// registry tracks the active channels on this host, enforcing the
// concurrent cap with LRU eviction of warm (zero-viewer) channels.
type registry struct {
	mu      sync.Mutex
	cap     int
	now     func() time.Time
	entries map[uuid.UUID]*entry
}

type entry struct {
	channelID uuid.UUID
	stop      StopFunc
	viewers   int
	lastTouch time.Time
}

func newRegistry(capacity int, now func() time.Time) *registry {
	if capacity < 1 {
		capacity = 1
	}
	if now == nil {
		now = time.Now
	}
	return &registry{cap: capacity, now: now, entries: map[uuid.UUID]*entry{}}
}

// get returns the live entry for a channel, if any.
func (r *registry) get(id uuid.UUID) (*entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	return e, ok
}

// admit reserves a slot for `id`. If the channel is already active it is
// a no-op success. Otherwise, when at capacity, the least-recently-used
// WARM (zero-viewer) channel is evicted to make room; if none can be
// evicted (all slots have viewers) it returns ErrAtCapacity.
func (r *registry) admit(id uuid.UUID) error {
	r.mu.Lock()
	if _, ok := r.entries[id]; ok {
		r.mu.Unlock()
		return nil
	}
	if len(r.entries) < r.cap {
		r.mu.Unlock()
		return nil
	}
	victim := r.lruWarmLocked()
	if victim == nil {
		r.mu.Unlock()
		return ErrAtCapacity
	}
	stop := r.evictLocked(victim)
	r.mu.Unlock()
	runStops(stop) // never under the registry lock
	return nil
}

// put records a freshly-spawned channel as active with one viewer.
func (r *registry) put(id uuid.UUID, stop StopFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[id] = &entry{channelID: id, stop: stop, viewers: 1, lastTouch: r.now()}
}

// putWith records an active channel with an initial viewer count (HLS
// stations start at 0 viewers and live on segment-fetch touches).
func (r *registry) putWith(id uuid.UUID, stop StopFunc, viewers int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[id] = &entry{channelID: id, stop: stop, viewers: viewers, lastTouch: r.now()}
}

// remove drops a channel without calling its stop (it already ended).
func (r *registry) remove(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
}

// attach increments the viewer count (a re-tune or a new viewer) and
// refreshes the LRU touch. Returns false if the channel isn't active.
func (r *registry) attach(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return false
	}
	e.viewers++
	e.lastTouch = r.now()
	return true
}

// detach decrements the viewer count (a viewer left); the channel stays
// warm until the reaper's grace window expires.
func (r *registry) detach(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		if e.viewers > 0 {
			e.viewers--
		}
		e.lastTouch = r.now()
	}
}

// touch refreshes the LRU timestamp on a segment fetch.
func (r *registry) touch(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		e.lastTouch = r.now()
	}
}

// reapIdle stops + removes channels that have had zero viewers for longer
// than `grace`, returning the ids reaped (so the engine can clear their
// runtime rows). This is what the streaming reaper Sweep calls.
func (r *registry) reapIdle(grace time.Duration) []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-grace)
	var reaped []uuid.UUID
	var stops []StopFunc
	for id, e := range r.entries {
		if e.viewers == 0 && e.lastTouch.Before(cutoff) {
			stops = append(stops, r.evictLocked(e))
			reaped = append(reaped, id)
		}
	}
	r.mu.Unlock()
	runStops(stops...)
	r.mu.Lock() // re-taken for the deferred unlock
	return reaped
}

func (r *registry) lruWarmLocked() *entry {
	var victim *entry
	for _, e := range r.entries {
		if e.viewers != 0 {
			continue
		}
		if victim == nil || e.lastTouch.Before(victim.lastTouch) {
			victim = e
		}
	}
	return victim
}

// evictLocked removes e and returns its stop func; the caller runs it
// after releasing the lock (a stop may take time and must never be able
// to wedge the registry).
func (r *registry) evictLocked(e *entry) StopFunc {
	delete(r.entries, e.channelID)
	return e.stop
}

func runStops(stops ...StopFunc) {
	for _, s := range stops {
		if s != nil {
			s()
		}
	}
}

// setCap changes the concurrent cap (a settings change). Channels over a
// lowered cap keep running; new ones wait for room.
func (r *registry) setCap(c int) {
	if c < 1 {
		c = 1
	}
	r.mu.Lock()
	r.cap = c
	r.mu.Unlock()
}

// capacity reports the concurrent cap.
func (r *registry) capacity() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cap
}

// viewers reports a channel's attached viewer count.
func (r *registry) viewers(id uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		return e.viewers
	}
	return 0
}

// len reports the active-channel count (for tests/metrics).
func (r *registry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
