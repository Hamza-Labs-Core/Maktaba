package relay

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// HomeCache keeps the relay serving the homes it knows while Postgres fails
// over (Epic 50 story 50.05). BySlug results live in an LRU (50,000 homes,
// 5 minutes); an entry over 60 s old is served and refreshed in the
// background, so a plan change shows within a minute (at once with the
// tier_changed and server_changed notifications, which drop entries). When the
// database errors, an entry of any age is served for a home this relay
// served in the last hour. A home it never saw gets the error (503).
type HomeCache struct {
	Lookup ServerLookup
	// Size, Life, RefreshAfter, StaleFor: 50,000, 5 min, 60 s, 1 h by default.
	Size         int
	Life         time.Duration
	RefreshAfter time.Duration
	StaleFor     time.Duration
	now          func() time.Time

	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List
}

type homeEntry struct {
	slug       string
	sv         stores.Server
	fetched    time.Time // the database answered with sv
	served     time.Time // last request served from it
	refreshing bool
}

// Cache results (relay_cache_hits_total{result}).
const (
	CacheHit     = "hit"     // fresh
	CacheRefresh = "refresh" // served, refreshed in the background
	CacheMiss    = "miss"    // fetched
	CacheStale   = "stale"   // the database failed; served an old entry
	CacheError   = "error"   // the database failed and the home is unknown
)

var cacheStats = struct {
	mu sync.Mutex
	n  map[string]int64
}{n: map[string]int64{}}

func countCache(result string) {
	cacheStats.mu.Lock()
	cacheStats.n[result]++
	cacheStats.mu.Unlock()
}

// CacheCounts snapshots the cache counters.
func CacheCounts() map[string]int64 {
	cacheStats.mu.Lock()
	defer cacheStats.mu.Unlock()
	out := make(map[string]int64, len(cacheStats.n))
	for k, v := range cacheStats.n {
		out[k] = v
	}
	return out
}

func (c *HomeCache) init() {
	if c.entries == nil {
		c.entries = map[string]*list.Element{}
		c.lru = list.New()
	}
}

func (c *HomeCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// BySlug serves slug's server from the cache or the database (ServerLookup).
func (c *HomeCache) BySlug(ctx context.Context, slug string) (stores.Server, error) {
	now := c.clock()
	life := orDefault(c.Life, 5*time.Minute)
	c.mu.Lock()
	c.init()
	var e *homeEntry
	if el, ok := c.entries[slug]; ok {
		e = el.Value.(*homeEntry)
		c.lru.MoveToFront(el)
		age := now.Sub(e.fetched)
		switch {
		case age < orDefault(c.RefreshAfter, time.Minute):
			e.served = now
			sv := e.sv
			c.mu.Unlock()
			countCache(CacheHit)
			return sv, nil
		case age < life:
			e.served = now
			sv := e.sv
			if !e.refreshing {
				e.refreshing = true
				go c.refresh(slug)
			}
			c.mu.Unlock()
			countCache(CacheRefresh)
			return sv, nil
		}
	}
	c.mu.Unlock()

	sv, err := c.Lookup.BySlug(ctx, slug)
	if err == nil {
		c.put(slug, sv, now)
		countCache(CacheMiss)
		return sv, nil
	}
	if errors.Is(err, stores.ErrServerNotFound) {
		c.Invalidate(slug)
		return stores.Server{}, err
	}
	// the database is away: a home served in the last hour keeps working
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[slug]; ok {
		e := el.Value.(*homeEntry)
		if now.Sub(e.served) < orDefault(c.StaleFor, time.Hour) {
			e.served = now
			countCache(CacheStale)
			return e.sv, nil
		}
	}
	countCache(CacheError)
	return stores.Server{}, err
}

func (c *HomeCache) refresh(slug string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sv, err := c.Lookup.BySlug(ctx, slug)
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[slug]
	if !ok {
		return
	}
	e := el.Value.(*homeEntry)
	e.refreshing = false
	switch {
	case err == nil:
		e.sv, e.fetched = sv, c.clock()
	case errors.Is(err, stores.ErrServerNotFound):
		c.lru.Remove(el)
		delete(c.entries, slug)
	}
}

func (c *HomeCache) put(slug string, sv stores.Server, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	if el, ok := c.entries[slug]; ok {
		e := el.Value.(*homeEntry)
		e.sv, e.fetched, e.served = sv, now, now
		c.lru.MoveToFront(el)
		return
	}
	c.entries[slug] = c.lru.PushFront(&homeEntry{slug: slug, sv: sv, fetched: now, served: now})
	size := c.Size
	if size <= 0 {
		size = 50000
	}
	for c.lru.Len() > size {
		old := c.lru.Back()
		c.lru.Remove(old)
		delete(c.entries, old.Value.(*homeEntry).slug)
	}
}

// Invalidate drops slug (a notification said it changed): the next request
// reads the database.
func (c *HomeCache) Invalidate(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	if el, ok := c.entries[slug]; ok {
		c.lru.Remove(el)
		delete(c.entries, slug)
	}
}

// Expire marks every entry for a database read on its next use (kept for
// stale-on-error): the notification listener reconnected and may have missed
// changes.
func (c *HomeCache) Expire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	for el := c.lru.Front(); el != nil; el = el.Next() {
		el.Value.(*homeEntry).fetched = time.Time{}
	}
}

// Len is how many homes are cached.
func (c *HomeCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	return c.lru.Len()
}

// Warm reports whether the cache knows any home (readiness: a relay that
// knows homes can serve them while the database is away).
func (c *HomeCache) Warm() bool { return c.Len() > 0 }

// WriteCacheMetrics renders relay_cache_hits_total{result}.
func WriteCacheMetrics(w io.Writer) error {
	n := CacheCounts()
	var err error
	p := func(format string, a ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, a...)
		}
	}
	p("# HELP maktaba_relay_cache_hits_total Home lookups by how the relay's cache answered.\n")
	p("# TYPE maktaba_relay_cache_hits_total counter\n")
	for _, r := range []string{CacheHit, CacheRefresh, CacheMiss, CacheStale, CacheError} {
		p("maktaba_relay_cache_hits_total{result=%q} %d\n", r, n[r])
	}
	return err
}
