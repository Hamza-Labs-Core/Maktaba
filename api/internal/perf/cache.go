package perf

import (
	"sort"
	"sync"
	"time"
)

// Cache is the in-process LRU + TTL cache shared by hot-path handlers
// (Story 18.8). Each named cache (e.g. "manifest", "segment", "library")
// is independently size-bounded and instrumented.
//
// Tradeoffs:
//   - Bounded by entry count, not bytes — entries are small wire
//     structs, not response bodies.
//   - Single global mutex; the hit-rate target leaves headroom for it.
//   - No background reaper; entries past TTL expire on next access.
type Cache[T any] struct {
	mu      sync.Mutex
	name    string
	max     int
	ttl     time.Duration
	entries map[string]*cacheEntry[T]
	hits    uint64
	misses  uint64
	evicts  uint64
	now     func() time.Time
}

type cacheEntry[T any] struct {
	value    T
	expires  time.Time
	lastUsed time.Time
}

// NewCache creates a named cache. maxEntries=0 disables eviction (unbounded).
func NewCache[T any](name string, maxEntries int, ttl time.Duration) *Cache[T] {
	return &Cache[T]{
		name:    name,
		max:     maxEntries,
		ttl:     ttl,
		entries: map[string]*cacheEntry[T]{},
		now:     time.Now,
	}
}

// Name returns the cache name (used for metrics labels).
func (c *Cache[T]) Name() string { return c.name }

// Get returns the cached value and true on hit, zero+false on miss/expiry.
func (c *Cache[T]) Get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	var zero T
	if !ok {
		c.misses++
		return zero, false
	}
	if c.now().After(e.expires) {
		delete(c.entries, key)
		c.misses++
		return zero, false
	}
	e.lastUsed = c.now()
	c.hits++
	return e.value, true
}

// Put inserts or replaces the value at key. Evicts LRU if over capacity.
func (c *Cache[T]) Put(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &cacheEntry[T]{
		value:    value,
		expires:  c.now().Add(c.ttl),
		lastUsed: c.now(),
	}
	if c.max > 0 && len(c.entries) > c.max {
		c.evictOne()
	}
}

// Flush removes every entry. Plan-18-08 admin endpoint hits this.
func (c *Cache[T]) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]*cacheEntry[T]{}
}

// Stats is a snapshot of counters for /metrics export.
type Stats struct {
	Hits    uint64
	Misses  uint64
	Evicts  uint64
	Size    int
	HitRate float64
}

// Stats returns the current counters.
func (c *Cache[T]) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := c.hits + c.misses
	rate := 0.0
	if total > 0 {
		rate = float64(c.hits) / float64(total)
	}
	return Stats{Hits: c.hits, Misses: c.misses, Evicts: c.evicts,
		Size: len(c.entries), HitRate: rate}
}

// evictOne drops the least-recently-used entry; caller must hold lock.
func (c *Cache[T]) evictOne() {
	keys := make([]string, 0, len(c.entries))
	for k := range c.entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return c.entries[keys[i]].lastUsed.Before(c.entries[keys[j]].lastUsed)
	})
	delete(c.entries, keys[0])
	c.evicts++
}

// Registry tracks the set of named caches so the admin flush endpoint
// can find them by name. Concurrency-safe.
type Registry struct {
	mu     sync.RWMutex
	caches map[string]Flusher
}

// Flusher is the bit of Cache the registry holds.
type Flusher interface {
	Name() string
	Flush()
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{caches: map[string]Flusher{}} }

// Register adds a cache.
func (r *Registry) Register(c Flusher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caches[c.Name()] = c
}

// Lookup finds a cache by name.
func (r *Registry) Lookup(name string) (Flusher, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.caches[name]
	return c, ok
}

// Names returns the registered cache names (for admin diagnostics).
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.caches))
	for n := range r.caches {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
