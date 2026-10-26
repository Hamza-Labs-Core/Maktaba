// Package usage counts the API's use per app and day (Epic 44 story 44.5).
//
// Every request adds to a tally in memory keyed by (day UTC, app, platform):
// requests, errors (5xx), time in milliseconds and slow requests (over Slow,
// 1 s by default). Who used which app is kept as (day, app, person) with the
// app's version. Every minute, and once more at shutdown, the tallies are added
// to api_usage_daily and the people to api_usage_people_daily; a minute lost in
// a crash is acceptable.
//
// No request path, user agent or address is kept: only the app, its platform
// and version, the status class and the time.
package usage

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
)

// DefaultSlow is when a request counts as slow.
const DefaultSlow = time.Second

// DefaultFlushEvery is how often the tallies are written.
const DefaultFlushEvery = time.Minute

// maxKeys bounds the distinct (day, app, platform) tallies held between two
// flushes: the header is the caller's to choose, so past this every new key is
// counted as app and platform `other`.
const maxKeys = 512

// maxPeople bounds the people held between two flushes (more than any home has).
const maxPeople = 10000

type key struct{ day, app, platform string }

type tally struct{ requests, errors, totalMS, slow int64 }

type person struct{ day, app, userID string }

// Counter holds the tallies until they are written. The zero value counts
// nothing until a database is attached (Attach): a machine without the app
// database (a full node) keeps none.
type Counter struct {
	// Slow is when a request counts as slow (zero: DefaultSlow).
	Slow time.Duration
	// Logger reports a flush that failed (nil: none).
	Logger *slog.Logger
	// now is the clock (tests).
	now func() time.Time

	mu     sync.Mutex
	db     *sql.DB
	rows   map[key]*tally
	people map[person]string
	// written: the people already written today with that version (each is
	// written once a day, and again when the version changes)
	written map[person]string
}

// Attach sets the database the tallies go to. Counting starts then.
func (c *Counter) Attach(db *sql.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.db = db
}

func (c *Counter) clock() time.Time {
	if c.now != nil {
		return c.now().UTC()
	}
	return time.Now().UTC()
}

func (c *Counter) slow() time.Duration {
	if c.Slow > 0 {
		return c.Slow
	}
	return DefaultSlow
}

// Record counts one request: its app, the person (empty when none signed in),
// its status and how long it took.
func (c *Counter) Record(info client.Info, userID string, status int, took time.Duration) {
	if c == nil {
		return
	}
	day := c.clock().Format(time.DateOnly)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		return
	}
	if c.rows == nil {
		c.rows = map[key]*tally{}
		c.people = map[person]string{}
	}
	k := key{day, info.App, info.Platform}
	t := c.rows[k]
	if t == nil {
		if len(c.rows) >= maxKeys {
			k = key{day, "other", "other"}
			t = c.rows[k]
		}
		if t == nil {
			t = &tally{}
			c.rows[k] = t
		}
	}
	t.requests++
	if status >= 500 {
		t.errors++
	}
	t.totalMS += took.Milliseconds()
	if took > c.slow() {
		t.slow++
	}
	if userID == "" {
		return
	}
	if _, err := uuid.Parse(userID); err != nil {
		return
	}
	p := person{day, info.App, userID}
	if v, ok := c.written[p]; ok && v == info.Version {
		return
	}
	if _, ok := c.people[p]; ok || len(c.people) < maxPeople {
		c.people[p] = info.Version
	}
}

// take hands over what was counted since the last flush.
func (c *Counter) take() (*sql.DB, map[key]*tally, map[person]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, people := c.rows, c.people
	c.rows, c.people = nil, nil
	return c.db, rows, people
}

// giveBack keeps tallies a flush could not write, for the next one.
func (c *Counter) giveBack(rows map[key]*tally) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows == nil {
		c.rows = map[key]*tally{}
		c.people = map[person]string{}
	}
	for k, t := range rows {
		if have := c.rows[k]; have != nil {
			have.requests += t.requests
			have.errors += t.errors
			have.totalMS += t.totalMS
			have.slow += t.slow
		} else {
			c.rows[k] = t
		}
	}
}

// wrote notes the people written, so each is written once a day (the notes of
// earlier days go).
func (c *Counter) wrote(day string, people map[person]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.written == nil {
		c.written = map[person]string{}
	}
	for p := range c.written {
		if p.day < day {
			delete(c.written, p)
		}
	}
	for p, v := range people {
		c.written[p] = v
	}
}

// Flush adds what was counted to the tables. Tallies it could not write are
// kept for the next flush; a person it could not write (one deleted meanwhile)
// is dropped.
func (c *Counter) Flush(ctx context.Context) error {
	if c == nil {
		return nil
	}
	db, rows, people := c.take()
	if db == nil || (len(rows) == 0 && len(people) == 0) {
		return nil
	}
	if err := writeRows(ctx, db, rows); err != nil {
		c.giveBack(rows)
		return err
	}
	done := map[person]string{}
	var firstErr error
	for p, v := range people {
		// one statement each, outside a transaction: a person deleted since is
		// refused by the foreign key, and refuses only their own row
		_, err := db.ExecContext(ctx, `
			INSERT INTO api_usage_people_daily (day, client_app, user_id, client_version)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (day, client_app, user_id) DO UPDATE SET client_version = EXCLUDED.client_version`,
			p.day, p.app, p.userID, v)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		done[p] = v
	}
	c.wrote(c.clock().Format(time.DateOnly), done)
	return firstErr
}

// writeRows adds the tallies in one transaction.
func writeRows(ctx context.Context, db *sql.DB, rows map[key]*tally) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for k, t := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_usage_daily (day, client_app, platform, requests, errors, total_ms, slow)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (day, client_app, platform) DO UPDATE SET
			  requests = api_usage_daily.requests + EXCLUDED.requests,
			  errors   = api_usage_daily.errors + EXCLUDED.errors,
			  total_ms = api_usage_daily.total_ms + EXCLUDED.total_ms,
			  slow     = api_usage_daily.slow + EXCLUDED.slow`,
			k.day, k.app, k.platform, t.requests, t.errors, t.totalMS, t.slow); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Run flushes every interval (zero: DefaultFlushEvery) until ctx ends, then
// once more (bounded by 5 s) so a shutdown keeps its last minute.
func (c *Counter) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = DefaultFlushEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c.flushLogged(fctx)
			cancel()
			return
		case <-t.C:
			c.flushLogged(ctx)
		}
	}
}

func (c *Counter) flushLogged(ctx context.Context) {
	if err := c.Flush(ctx); err != nil && c.Logger != nil {
		c.Logger.Warn("usage: api use not written; kept for the next flush",
			"err", err, "event", "api_usage_flush_failed")
	}
}

// Purge deletes the days before cutoff (the analytics retention). Returns the
// rows deleted.
func Purge(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	day := cutoff.UTC().Format(time.DateOnly)
	var n int64
	for _, q := range []string{
		`DELETE FROM api_usage_daily WHERE day < $1`,
		`DELETE FROM api_usage_people_daily WHERE day < $1`,
	} {
		res, err := db.ExecContext(ctx, q, day)
		if err != nil {
			return n, err
		}
		if m, err := res.RowsAffected(); err == nil {
			n += m
		}
	}
	return n, nil
}
