package parental

import "time"

// What the package's external tests need of the cache's inside.

const (
	EntryMaxAge  = entryMaxAge
	UnlimitedFor = unlimitedFor
	ReadTimeout  = readTimeout
)

// SetClock gives the cache a clock.
func (c *Cache) SetClock(now func() time.Time) { c.now = now }

// Resume makes the cache keep entries, as a connected listener does.
func (c *Cache) Resume() { c.resume() }

// Suspend is what a lost listener does.
func (c *Cache) Suspend() { c.suspend() }
