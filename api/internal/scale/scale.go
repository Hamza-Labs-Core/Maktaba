// Package scale carries the horizontal-scaling stubs documented in
// Epic 19 plans 19.2–19.4 and 19.7.
//
// What lives here:
//
//   - Shard: stable, deterministic placement so a multi-host deploy can
//     spread libraries / users across worker pools without runtime
//     coordination. The hashing strategy is fnv64 — fast, no crypto,
//     stable across processes.
//   - EventBus: an interface every API replica satisfies, with an
//     in-process implementation for single-host installs and a contract
//     for the Postgres-LISTEN / Redis pub-sub adapter to ship later.
//   - Concurrency: a small token-bucket limiter that handlers wrap around
//     expensive operations (transcode start, full library re-scan) to
//     keep one replica from eating the host (Story 19.7).
//
// Postgres / Redis adapters are not in this stub — the contract is in
// place so they can be added without touching call sites.
package scale

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"time"
)

// Shard returns the assigned shard id for the given key. shardCount=0
// returns 0 always (single-host fallback).
func Shard(key string, shardCount int) int {
	if shardCount <= 1 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(shardCount))
}

// OwnerID is the stable identity a replica writes to processing_jobs.
// Used by Story 19.4's lease/heartbeat logic.
type OwnerID string

// EventBus is the cross-replica fan-out interface. Per Story 19.2,
// Job-status changes that one replica sees over its Postgres LISTEN
// channel are republished here so the websocket fan-out picks them up
// regardless of which replica owns the socket.
type EventBus interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Subscribe(topic string) (<-chan Event, func())
	Close() error
}

// Event is the wire shape on the bus.
type Event struct {
	Topic   string
	Payload []byte
	At      time.Time
}

// InMemoryBus is the single-host implementation. Adapters for Postgres
// LISTEN/NOTIFY or Redis pub-sub satisfy the same interface.
type InMemoryBus struct {
	mu          sync.Mutex
	subscribers map[string][]chan Event
}

// NewInMemoryBus returns an empty bus.
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{subscribers: map[string][]chan Event{}}
}

// Publish fans out to every subscriber on the topic. Slow subscribers
// drop messages rather than blocking publishers.
func (b *InMemoryBus) Publish(_ context.Context, topic string, payload []byte) error {
	b.mu.Lock()
	subs := append([]chan Event(nil), b.subscribers[topic]...)
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- Event{Topic: topic, Payload: payload, At: time.Now().UTC()}:
		default:
			// drop on full buffer (caller is too slow)
		}
	}
	return nil
}

// Subscribe returns a channel and an unsubscribe function.
func (b *InMemoryBus) Subscribe(topic string) (<-chan Event, func()) {
	b.mu.Lock()
	ch := make(chan Event, 16)
	b.subscribers[topic] = append(b.subscribers[topic], ch)
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.subscribers[topic]
		for i, c := range subs {
			if c == ch {
				b.subscribers[topic] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		close(ch)
	}
	return ch, cancel
}

// Close detaches every subscriber and closes their channels.
func (b *InMemoryBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subs := range b.subscribers {
		for _, ch := range subs {
			close(ch)
		}
	}
	b.subscribers = map[string][]chan Event{}
	return nil
}

// --- concurrency cap (Story 19.7) -----------------------------------------

// Limiter is a simple counting semaphore with a context-aware Acquire.
// Wraps an expensive operation in a per-replica cap; e.g. transcoder
// jobs limit themselves to N concurrent ffmpeg processes.
type Limiter struct {
	tokens chan struct{}
}

// NewLimiter returns a Limiter with `cap` slots. cap <= 0 panics; the
// invariant is enforced at construction so call sites stay clean.
func NewLimiter(cap int) *Limiter {
	if cap <= 0 {
		panic("scale: Limiter cap must be > 0")
	}
	return &Limiter{tokens: make(chan struct{}, cap)}
}

// Acquire takes a slot or returns ctx.Err() / ErrLimiterFull.
func (l *Limiter) Acquire(ctx context.Context) error {
	select {
	case l.tokens <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire is the non-blocking variant; returns ErrLimiterFull
// immediately if no slot is available.
func (l *Limiter) TryAcquire() error {
	select {
	case l.tokens <- struct{}{}:
		return nil
	default:
		return ErrLimiterFull
	}
}

// Release returns a slot. Must be paired with a successful Acquire/TryAcquire.
func (l *Limiter) Release() {
	select {
	case <-l.tokens:
	default:
		// release without acquire is a logic bug; ignore for safety.
	}
}

// InUse returns the slots currently held.
func (l *Limiter) InUse() int { return len(l.tokens) }

// ErrLimiterFull is returned by TryAcquire when no slot is free.
var ErrLimiterFull = errors.New("scale: limiter full")
