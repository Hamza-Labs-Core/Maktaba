package analytics

import (
	"sync"
	"time"
)

// summaryCache memoises the summary payload per range label for a short
// TTL to absorb dashboard refresh spam (D6). Mirrors streaming.capCache.
type summaryCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]summaryEntry
}

type summaryEntry struct {
	val Summary
	exp time.Time
}

func newSummaryCache(ttl time.Duration) *summaryCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &summaryCache{ttl: ttl, m: map[string]summaryEntry{}}
}

// get returns the cached summary for label when still fresh.
func (c *summaryCache) get(label string, now time.Time) (Summary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[label]
	if !ok || now.After(e.exp) {
		return Summary{}, false
	}
	return e.val, true
}

func (c *summaryCache) put(label string, v Summary, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[label] = summaryEntry{val: v, exp: now.Add(c.ttl)}
}
