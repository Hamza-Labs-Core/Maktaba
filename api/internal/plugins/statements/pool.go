package statements

// The executor's connections (2026-11-15: on a home running 0.3.135 the plugins' kept role
// connections and six transactions left idle for 19 hours held the API's whole pool, and
// every login waited for a connection that never came).
//
//   - The executor holds at most Config.MaxConns connections at once: kept between calls,
//     running a statement, or holding a transaction, all count. On the home it has a pool of
//     its own (Open), so the API's pool is never the plugins'; on a pool it shares (New), the
//     cap leaves the API the rest. A call that finds every connection busy first gives back
//     the oldest kept one of any plugin, then waits at most Config.SlotWait and fails as
//     retryable (503 plugin_database).
//   - A kept connection unused for Config.IdleKeep goes back.
//   - A background reaper (started with the executor, stopped by Close) rolls back every
//     transaction idle past Config.TxIdle, without waiting for the next begin. Ending a
//     transaction never waits for a call that holds it: the transaction is marked, and the
//     call rolls it back as it returns. A statement inside a transaction runs for at most
//     Config.TxStmtTimeout, so that return comes.
//   - Postgres ends a role session left idle in a transaction past TxIdle plus a minute
//     (idle_in_transaction_session_timeout, set on each role connection), should all of
//     the above fail.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
)

const (
	// MaxConns is how many connections the executor holds at once, by default.
	MaxConns = 12
	// lookupConns are the connections of an executor's own pool beyond MaxConns: the scope
	// checks' lookups, which run outside the plugin's role.
	lookupConns = 4
	// IdlePerPlugin is how many role connections a plugin keeps between calls.
	IdlePerPlugin = 2
	// IdleKeep is how long a kept role connection waits for its plugin's next call.
	IdleKeep = time.Minute
	// TxStmtTimeout bounds one call inside a transaction.
	TxStmtTimeout = 5 * time.Minute
	// SlotWait is how long a call waits for a connection when all are busy.
	SlotWait = 10 * time.Second
)

// Config sizes an executor; a zero field takes its default.
type Config struct {
	MaxConns      int
	TxIdle        time.Duration
	ReapEvery     time.Duration
	IdleKeep      time.Duration
	TxStmtTimeout time.Duration
	SlotWait      time.Duration
	Now           func() time.Time
}

func (c Config) withDefaults() Config {
	if c.MaxConns <= 0 {
		c.MaxConns = MaxConns
	}
	if c.TxIdle <= 0 {
		c.TxIdle = TxIdle
	}
	if c.ReapEvery <= 0 {
		c.ReapEvery = c.TxIdle / 8
	}
	if c.IdleKeep <= 0 {
		c.IdleKeep = IdleKeep
	}
	if c.TxStmtTimeout <= 0 {
		c.TxStmtTimeout = TxStmtTimeout
	}
	if c.SlotWait <= 0 {
		c.SlotWait = SlotWait
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// dbIdleTimeout is the role sessions' idle_in_transaction_session_timeout: past the
// reaper's own deadline, so the reaper rolls back first.
func (c Config) dbIdleTimeout() time.Duration { return c.TxIdle + c.ReapEvery + time.Minute }

type parked struct {
	c     *sql.Conn
	since time.Time
}

// New answers an executor with no statements registered, on db: a pool it shares (the
// executor holds at most MaxConns of it).
func New(db *sql.DB) *Executor { return NewWith(db, Config{}) }

// NewWith is New, sized by cfg.
func NewWith(db *sql.DB, cfg Config) *Executor {
	cfg = cfg.withDefaults()
	e := &Executor{DB: db, cfg: cfg, reg: map[string]map[string]*Statement{},
		txs: map[string]*openTx{}, vlibs: map[string]map[string]string{}, idle: map[string][]parked{},
		slots: make(chan struct{}, cfg.MaxConns), stop: make(chan struct{}), reaped: make(chan struct{})}
	go e.loop()
	return e
}

// Open answers an executor on a pool of its own on dsn (the home's API): the plugins never
// take a connection of the API's pool. Close closes the pool.
func Open(dsn string, cfg Config) (*Executor, error) {
	cfg = cfg.withDefaults()
	db, err := pgdsn.Open(dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(cfg.MaxConns + lookupConns)
	db.SetMaxIdleConns(cfg.MaxConns + lookupConns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	e := NewWith(db, cfg)
	e.owned = true
	return e, nil
}

// loop is the reaper: idle transactions and kept connections, on a timer.
func (e *Executor) loop() {
	defer close(e.reaped)
	t := time.NewTicker(e.cfg.ReapEvery)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-t.C:
			e.reap()
			e.expireIdle()
		}
	}
}

func roleKey(sc Scope) string { return sc.PluginID + "@" + sc.Version }

// acquire answers a connection that runs as the plugin's role with its origin (step 4),
// set once per connection: SET ROLE and maktaba.origin at the session's level. Getting one
// waits at most SlotWait, whatever ctx allows.
func (e *Executor) acquire(ctx context.Context, sc Scope) (*sql.Conn, error) {
	key := roleKey(sc)
	e.mu.Lock()
	if l := e.idle[key]; len(l) > 0 {
		p := l[len(l)-1]
		if len(l) == 1 {
			delete(e.idle, key)
		} else {
			e.idle[key] = l[:len(l)-1]
		}
		e.mu.Unlock()
		return p.c, nil
	}
	e.mu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, e.cfg.SlotWait)
	defer cancel()
	if err := e.takeSlot(wctx); err != nil {
		return nil, err
	}
	c, err := e.DB.Conn(wctx)
	if err != nil {
		e.freeSlot()
		return nil, err
	}
	ms := e.cfg.dbIdleTimeout().Milliseconds()
	if _, err := c.ExecContext(wctx, fmt.Sprintf("SET idle_in_transaction_session_timeout = %d; ", ms)+
		"SET ROLE "+pq.QuoteIdentifier(Role(sc.PluginID))+
		"; SET maktaba.origin = "+pq.QuoteLiteral("plugin:"+sc.PluginID+"@"+sc.Version)); err != nil {
		e.discard(c)
		return nil, err
	}
	return c, nil
}

// takeSlot reserves one of the executor's connections: a free one, else the oldest kept
// one of any plugin given back, else the first freed before ctx ends.
func (e *Executor) takeSlot(ctx context.Context) error {
	for {
		select {
		case e.slots <- struct{}{}:
			return nil
		default:
		}
		p, ok := e.evictOldest()
		if !ok {
			break
		}
		e.discard(p.c)
	}
	// while a call waits, a released connection is given back, never kept (release)
	e.waiting.Add(1)
	defer e.waiting.Add(-1)
	if p, ok := e.evictOldest(); ok {
		e.discard(p.c)
	}
	select {
	case e.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("statements: all %d plugin database connections are busy: %w", cap(e.slots), ctx.Err())
	}
}

func (e *Executor) freeSlot() { <-e.slots }

// evictOldest takes the oldest kept connection of any plugin.
func (e *Executor) evictOldest() (parked, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var bestKey string
	best := -1
	for k, l := range e.idle {
		if len(l) > 0 && (best < 0 || l[0].since.Before(e.idle[bestKey][best].since)) {
			bestKey, best = k, 0
		}
	}
	if best < 0 {
		return parked{}, false
	}
	l := e.idle[bestKey]
	p := l[0]
	if len(l) == 1 {
		delete(e.idle, bestKey)
	} else {
		e.idle[bestKey] = l[1:]
	}
	return p, true
}

// expireIdle gives back the kept connections unused for IdleKeep.
func (e *Executor) expireIdle() {
	now := e.cfg.Now()
	var old []*sql.Conn
	e.mu.Lock()
	for k, l := range e.idle {
		keep := l[:0]
		for _, p := range l {
			if now.Sub(p.since) > e.cfg.IdleKeep {
				old = append(old, p.c)
			} else {
				keep = append(keep, p)
			}
		}
		if len(keep) == 0 {
			delete(e.idle, k)
		} else {
			e.idle[k] = keep
		}
	}
	e.mu.Unlock()
	for _, c := range old {
		e.discard(c)
	}
}

// release keeps a role connection for the plugin's next call, or gives it back (discard).
// Nothing is kept while another call waits for a connection.
func (e *Executor) release(sc Scope, c *sql.Conn, broken bool) {
	key := roleKey(sc)
	if !broken {
		e.mu.Lock()
		if !e.closed && e.waiting.Load() == 0 && len(e.idle[key]) < IdlePerPlugin {
			e.idle[key] = append(e.idle[key], parked{c: c, since: e.cfg.Now()})
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
	}
	e.discard(c)
}

// dropIdle closes the plugin's kept connections.
func (e *Executor) dropIdle(sc Scope) {
	key := roleKey(sc)
	e.mu.Lock()
	idle := e.idle[key]
	delete(e.idle, key)
	e.mu.Unlock()
	for _, p := range idle {
		e.discard(p.c)
	}
}

// discard ends a role connection: reset to the pool's own role and returned to it, or
// marked bad (closed) when the reset fails (on a shared pool it would run the API's
// queries as the plugin). Its slot is freed.
func (e *Executor) discard(c *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.ExecContext(ctx, "RESET ROLE; RESET maktaba.origin; RESET idle_in_transaction_session_timeout"); err != nil {
		_ = c.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = c.Close()
	e.freeSlot()
}

// Close stops the reaper, rolls back every open transaction, gives back every kept
// connection, and closes the executor's own pool (the API stopping, tests).
func (e *Executor) Close() {
	e.closeOnce.Do(func() {
		close(e.stop)
		<-e.reaped
		e.mu.Lock()
		e.closed = true
		idle, txs := e.idle, e.txs
		e.idle, e.txs = map[string][]parked{}, map[string]*openTx{}
		e.mu.Unlock()
		for _, ot := range txs {
			e.end(ot)
		}
		for _, l := range idle {
			for _, p := range l {
				e.discard(p.c)
			}
		}
		if e.owned {
			_ = e.DB.Close()
		}
	})
}

// lockTx takes a transaction for one call. One ended meanwhile (its invocation ended, the
// reaper) is refused, and rolled back as the call lets go of it. One committed or rolled
// back meanwhile (another call that found it first) is refused as it is: its connection
// is already given back, kept for the plugin's next call or the pool's, never this call's.
func (e *Executor) lockTx(ot *openTx, id string) *Error {
	ot.mu.Lock()
	if ot.done {
		ot.mu.Unlock()
		return refuse(409, CodeTx, "no open transaction "+id)
	}
	ot.busy.Store(true)
	ot.touch(e.cfg.Now())
	if ot.doomed.Load() {
		e.unlockTx(ot)
		return refuse(409, CodeTx, "no open transaction "+id)
	}
	return nil
}

// unlockTx lets go of a transaction, ending it when it was ended while held.
func (e *Executor) unlockTx(ot *openTx) {
	ot.touch(e.cfg.Now())
	ot.busy.Store(false)
	if ot.doomed.Load() {
		e.finish(ot)
	}
	ot.mu.Unlock()
	// end() may have marked it after the check above and failed its TryLock while this
	// call still held it: then this call ends it
	if ot.doomed.Load() && ot.mu.TryLock() {
		e.finish(ot)
		ot.mu.Unlock()
	}
}

// end rolls back a transaction already out of e.txs and gives its connection back: now
// when no call holds it, else when the call that holds it returns. It never waits.
func (e *Executor) end(ot *openTx) {
	ot.doomed.Store(true)
	if ot.mu.TryLock() {
		e.finish(ot)
		ot.mu.Unlock()
	}
}

// finish (ot.mu held) rolls back and discards, once.
func (e *Executor) finish(ot *openTx) {
	if ot.done {
		return
	}
	ot.done = true
	_ = ot.tx.Rollback()
	e.discard(ot.conn)
}

// reap rolls back the transactions idle past TxIdle, and those a call has held past
// TxIdle and TxStmtTimeout (a call that never returns: it ends them when it does).
func (e *Executor) reap() {
	now := e.cfg.Now()
	e.mu.Lock()
	var old []*openTx
	for k, ot := range e.txs {
		limit := e.cfg.TxIdle
		if ot.busy.Load() {
			limit += e.cfg.TxStmtTimeout
		}
		if now.Sub(ot.lastUsedAt()) > limit {
			old = append(old, ot)
			delete(e.txs, k)
		}
	}
	e.mu.Unlock()
	for _, ot := range old {
		e.end(ot)
	}
}

// Stats is what the executor holds now (the admin's health, /metrics).
type Stats struct {
	MaxConnections           int     `json:"max_connections"`
	ConnectionsHeld          int     `json:"connections_held"`
	ConnectionsKept          int     `json:"connections_kept"`
	Waiting                  int     `json:"waiting"`
	OpenTransactions         int     `json:"open_transactions"`
	OldestTransactionSeconds float64 `json:"oldest_transaction_seconds"`
	OwnPool                  bool    `json:"own_pool"`
}

// Stats answers what the executor holds now.
func (e *Executor) Stats() Stats {
	now := e.cfg.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	st := Stats{MaxConnections: cap(e.slots), ConnectionsHeld: len(e.slots), Waiting: int(e.waiting.Load()),
		OpenTransactions: len(e.txs), OwnPool: e.owned}
	for _, l := range e.idle {
		st.ConnectionsKept += len(l)
	}
	for _, ot := range e.txs {
		if age := now.Sub(ot.begun).Seconds(); age > st.OldestTransactionSeconds {
			st.OldestTransactionSeconds = age
		}
	}
	return st
}

var (
	published   atomic.Pointer[Executor]
	publishOnce sync.Once
)

// Publish puts this executor's Stats on /metrics (the home's: one executor).
func (e *Executor) Publish() {
	published.Store(e)
	publishOnce.Do(func() {
		gauge := func(name, help string, f func(Stats) float64) {
			_ = prometheus.DefaultRegisterer.Register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help},
				func() float64 {
					if p := published.Load(); p != nil {
						return f(p.Stats())
					}
					return 0
				}))
		}
		gauge("maktaba_plugin_relay_connections_max", "Database connections the first-party plugins' statements may hold (Epic 54).",
			func(s Stats) float64 { return float64(s.MaxConnections) })
		gauge("maktaba_plugin_relay_connections_held", "Database connections the first-party plugins' statements hold now (Epic 54).",
			func(s Stats) float64 { return float64(s.ConnectionsHeld) })
		gauge("maktaba_plugin_relay_waiting", "Plugin statement calls waiting for a database connection (Epic 54).",
			func(s Stats) float64 { return float64(s.Waiting) })
		gauge("maktaba_plugin_relay_open_transactions", "Transactions the first-party plugins hold open (Epic 54).",
			func(s Stats) float64 { return float64(s.OpenTransactions) })
		gauge("maktaba_plugin_relay_oldest_transaction_seconds", "Age of the oldest transaction a first-party plugin holds open (Epic 54).",
			func(s Stats) float64 { return s.OldestTransactionSeconds })
	})
}
