package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// homeClock is the home's clock as this machine sees it: the offset of the home
// database's now() from this machine's own, measured on the home's connection (against
// the middle of the call), at most every homeClockEvery. What the home stamps (the
// copy's beat) is compared on it, never on this machine's clock: two machines' clocks
// drift apart (a Windows PC outside a domain sets its clock weekly). The last offset is
// kept (path): after a restart in an outage the home's clock is not this machine's.
type homeClock struct {
	mu     sync.Mutex
	offset time.Duration
	at     time.Time // when it was last measured ("": never: no offset)
	// now is this machine's clock (tests); nil: time.Now
	now func() time.Time
	// path is where the offset is kept ("": nowhere)
	path string
}

// savedClock is a kept offset.
type savedClock struct {
	OffsetNS int64     `json:"offset_ns"`
	At       time.Time `json:"at"`
}

// keepAt keeps the offset at path, and takes the one kept there (a measure since replaces
// it).
func (c *homeClock) keepAt(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path = path
	if path == "" {
		return
	}
	var s savedClock
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &s) == nil && c.at.IsZero() {
		c.offset = time.Duration(s.OffsetNS)
	}
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
	path, saved := c.path, savedClock{OffsetNS: int64(c.offset), At: t1.UTC()}
	c.mu.Unlock()
	saveClock(path, saved)
}

// saveClock keeps an offset at path (best-effort; "": nowhere).
func saveClock(path string, s savedClock) {
	if path == "" {
		return
	}
	b, err := json.Marshal(s)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}
