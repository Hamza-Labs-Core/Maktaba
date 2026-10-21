package main

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// homeClock is the home's clock as this machine sees it: the offset of the home
// database's now() from this machine's own, measured on the home's connection (against
// the middle of the call), at most every homeClockEvery. What the home stamps (the
// copy's beat) is compared on it, never on this machine's clock: two machines' clocks
// drift apart (a Windows PC outside a domain sets its clock weekly).
type homeClock struct {
	mu     sync.Mutex
	offset time.Duration
	at     time.Time // when it was last measured ("": never: no offset)
	// now is this machine's clock (tests); nil: time.Now
	now func() time.Time
}

const homeClockEvery = 30 * time.Second

func (c *homeClock) local() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Offset is the home's clock minus this machine's (0 before it was measured).
func (c *homeClock) Offset() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset
}

// Now is the home's time now.
func (c *homeClock) Now() time.Time { return c.local().Add(c.Offset()) }

// measure measures the offset on db (the home's database) when it is due.
func (c *homeClock) measure(ctx context.Context, db *sql.DB) {
	c.mu.Lock()
	due := c.at.IsZero() || c.local().Sub(c.at) >= homeClockEvery
	c.mu.Unlock()
	if !due || db == nil {
		return
	}
	mctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	t0 := c.local()
	var home time.Time
	if err := db.QueryRowContext(mctx, `SELECT now()`).Scan(&home); err != nil {
		return
	}
	t1 := c.local()
	c.mu.Lock()
	c.offset, c.at = home.Sub(t0.Add(t1.Sub(t0)/2)), t1
	c.mu.Unlock()
}
