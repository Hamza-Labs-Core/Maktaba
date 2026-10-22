// Package nodedb is a full node's database (specs/epics/37-node-replica): one *sql.DB
// over two Postgres servers, this machine's own read-only copy of the home's database
// and the home's database itself. Every pooled connection is a pair of them, opened as
// needed, and each statement picks one:
//
//   - a query whose context says the copy may answer it (CopyReads: the node's reads,
//     marked per request), or a read-only transaction so marked, goes to the copy while
//     the copy answers;
//   - anything else (an Exec, a prepared statement, a read-write transaction, a query
//     no one marked) goes to the home's: a write in a request marked a read is still
//     the home's;
//   - with the home's database not answering, everything goes to the copy: the reads
//     are answered, a write fails as it would have (the copy is read only).
//
// A transaction stays on the server it began on. A copy that does not answer (being
// made, or rebuilt) is skipped until it does.
package nodedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type copyKey struct{}

// CopyReads marks ctx: its statements are reads the copy may answer.
func CopyReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, copyKey{}, true)
}

func copyOK(ctx context.Context) bool {
	ok, _ := ctx.Value(copyKey{}).(bool)
	return ok
}

// Source is one of the two servers.
type Source struct {
	Name string // "copy" or "home", for the log
	Conn driver.Connector
	// down: the last connection or ping failed (unknown counts as up)
	down atomic.Bool
}

// Down reports whether a source's last connection or check failed.
func (s *Source) Down() bool { return s != nil && s.down.Load() }

// Pair is the node's database as a driver.Connector (sql.OpenDB).
type Pair struct {
	Copy, Home *Source
	Logger     *slog.Logger

	onCopy   atomic.Bool // for the log: the home's database is down and the copy answers everything
	checking atomic.Bool

	upMu sync.Mutex
	ups  []func(*Source)
}

// WhenUp calls fn (in its own goroutine) each time a server that was down answers
// again.
func (p *Pair) WhenUp(fn func(*Source)) {
	p.upMu.Lock()
	p.ups = append(p.ups, fn)
	p.upMu.Unlock()
}

// mark records whether s answered; a server that was down and answers again is told
// to WhenUp's functions.
func (p *Pair) mark(s *Source, down bool) bool {
	was := s.down.Swap(down)
	if was && !down {
		p.upMu.Lock()
		ups := append([]func(*Source){}, p.ups...)
		p.upMu.Unlock()
		for _, fn := range ups {
			go fn(s)
		}
	}
	return was
}

var _ driver.Connector = (*Pair)(nil)

// Open is the pair's *sql.DB; connections live at most maxLife.
func (p *Pair) Open(maxLife time.Duration) *sql.DB {
	db := sql.OpenDB(p)
	if maxLife <= 0 {
		maxLife = time.Minute
	}
	db.SetConnMaxLifetime(maxLife)
	return db
}

// Driver implements driver.Connector.
func (p *Pair) Driver() driver.Driver { return p.Home.Conn.Driver() }

// Connect implements driver.Connector: a pair whose servers are opened as statements
// need them (one now, so a connection the pool counts is a real one).
func (p *Pair) Connect(ctx context.Context) (driver.Conn, error) {
	c := &conn{p: p}
	if _, err := c.get(ctx, p.pick(ctx)); err != nil {
		return nil, err
	}
	return c, nil
}

// pick is the server a statement goes to.
func (p *Pair) pick(ctx context.Context) *Source {
	switch {
	case p.Copy == nil:
		return p.Home
	case p.Home.Down():
		p.note(true)
		return p.Copy
	case copyOK(ctx) && !p.Copy.Down():
		return p.Copy
	}
	p.note(false)
	return p.Home
}

func (p *Pair) other(s *Source) *Source {
	if s == p.Home {
		return p.Copy
	}
	return p.Home
}

func (p *Pair) note(onCopy bool) {
	if p.onCopy.Swap(onCopy) == onCopy || p.Logger == nil {
		return
	}
	if onCopy {
		p.Logger.Warn("node: the home's database does not answer; this machine's copy answers everything (read only)",
			"event", "node_db_on_copy")
	} else {
		p.Logger.Info("node: the home's database answers again", "event", "node_db_on_home")
	}
}

// conn is one pooled connection: up to one of each server's.
type conn struct {
	p *Pair

	mu     sync.Mutex
	open   map[*Source]driver.Conn
	tx     driver.Conn // the server a transaction is open on
	closed bool
}

// get is the connection to s, opened if need be; a server that cannot be reached is
// marked down and the other one used (a statement fails on the server that could
// not answer it only when neither can).
func (c *conn) get(ctx context.Context, s *Source) (driver.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tx != nil {
		return c.tx, nil
	}
	if dc, ok := c.open[s]; ok {
		return dc, nil
	}
	dc, err := s.Conn.Connect(ctx)
	if err == nil {
		c.p.mark(s, false)
		if c.open == nil {
			c.open = map[*Source]driver.Conn{}
		}
		c.open[s] = dc
		return dc, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	c.p.mark(s, true)
	o := c.p.other(s)
	if o == nil {
		return nil, err
	}
	if dc, ok := c.open[o]; ok {
		return dc, nil
	}
	dc, err2 := o.Conn.Connect(ctx)
	if err2 != nil {
		return nil, err
	}
	c.p.mark(o, false)
	if c.open == nil {
		c.open = map[*Source]driver.Conn{}
	}
	c.open[o] = dc
	return dc, nil
}

func (c *conn) route(ctx context.Context) (driver.Conn, error) { return c.get(ctx, c.p.pick(ctx)) }

// routeWrite is route for what may write: the home's (the copy's when the home's does
// not answer: the write then fails as it would have).
func (c *conn) routeWrite(ctx context.Context) (driver.Conn, error) {
	return c.get(ctx, c.p.pick(context.WithValue(ctx, copyKey{}, false)))
}

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	dc, err := c.routeWrite(ctx)
	if err != nil {
		return nil, err
	}
	if pc, ok := dc.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, query)
	}
	return dc.Prepare(query)
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	dc, err := c.route(ctx)
	if err != nil {
		return nil, err
	}
	q, ok := dc.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return q.QueryContext(ctx, query, args)
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	dc, err := c.routeWrite(ctx)
	if err != nil {
		return nil, err
	}
	e, ok := dc.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return e.ExecContext(ctx, query, args)
}

func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	route := c.routeWrite
	if opts.ReadOnly {
		route = c.route
	}
	dc, err := route(ctx)
	if err != nil {
		return nil, err
	}
	var tx driver.Tx
	if b, ok := dc.(driver.ConnBeginTx); ok {
		tx, err = b.BeginTx(ctx, opts)
	} else {
		tx, err = dc.Begin() //nolint:staticcheck // a driver without BeginTx
	}
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.tx = dc
	c.mu.Unlock()
	return &txn{Tx: tx, c: c}, nil
}

type txn struct {
	driver.Tx
	c *conn
}

func (t *txn) end(err error) error {
	t.c.mu.Lock()
	t.c.tx = nil
	t.c.mu.Unlock()
	return err
}

func (t *txn) Commit() error   { return t.end(t.Tx.Commit()) }
func (t *txn) Rollback() error { return t.end(t.Tx.Rollback()) }

func (c *conn) Ping(ctx context.Context) error {
	dc, err := c.route(ctx)
	if err != nil {
		return err
	}
	if pg, ok := dc.(driver.Pinger); ok {
		return pg.Ping(ctx)
	}
	return nil
}

// ResetSession drops a server's connection the driver says is bad (it is opened again
// when a statement needs it); the pair stays.
func (c *conn) ResetSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for s, dc := range c.open {
		if r, ok := dc.(driver.SessionResetter); ok {
			if err := r.ResetSession(ctx); errors.Is(err, driver.ErrBadConn) {
				_ = dc.Close()
				delete(c.open, s)
			}
		}
	}
	return nil
}

// IsValid implements driver.Validator.
func (c *conn) IsValid() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	var first error
	for s, dc := range c.open {
		if err := dc.Close(); err != nil && first == nil {
			first = err
		}
		delete(c.open, s)
	}
	return first
}

// Watch pings each server every `every` (2 s each at most) until ctx ends, so a dead
// one is skipped at the next statement rather than waited on.
func (p *Pair) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		p.CheckAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckAll pings each server once.
func (p *Pair) CheckAll(ctx context.Context) {
	for _, s := range []*Source{p.Copy, p.Home} {
		if s != nil {
			p.check(ctx, s)
		}
	}
}

func (p *Pair) check(ctx context.Context, s *Source) {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := s.Conn.Connect(pctx)
	if err == nil {
		if pg, ok := c.(driver.Pinger); ok {
			err = pg.Ping(pctx)
		}
		_ = c.Close()
	}
	if ctx.Err() != nil {
		return
	}
	was := p.mark(s, err != nil)
	if was != (err != nil) && p.Logger != nil {
		if err != nil {
			p.Logger.Warn("node: a database does not answer", "source", s.Name, "err", err, "event", "node_db_down")
		} else {
			p.Logger.Info("node: a database answers again", "source", s.Name, "event", "node_db_up")
		}
	}
}

// Recheck pings both servers now (once at a time): a statement failed and the next ones
// should not wait for the watch to tell which server is gone.
func (p *Pair) Recheck() {
	if !p.checking.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.checking.Store(false)
		p.CheckAll(context.Background())
	}()
}

// Recent remembers who changed something, so their reads go to the home's database
// until the copy has applied the change (read-your-writes): until the copy holds a beat
// the home wrote after it (the beat and the change are stamped on the home's clock;
// replication applies the home's transactions in their order), however far behind the
// copy is.
type Recent struct {
	// Now is the home's clock (the node's measure of it).
	Now func() time.Time

	mu sync.Mutex
	m  map[string]time.Time // a credential's last change, on the home's clock
}

// RecentMargin is how much later than a change the copy's beat must be (the clocks'
// measure is a call's half round trip off).
const RecentMargin = time.Second

// RecentKeep is how long a change is remembered at most: a copy that has not caught up
// by then is behind the rule, and every read goes to the home anyway.
const RecentKeep = 10 * time.Minute

func (r *Recent) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Wrote notes a change by key (a credential's hash): called as it starts and once it
// is done (the later stamp counts).
func (r *Recent) Wrote(key string) {
	if key == "" {
		return
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]time.Time{}
	}
	if len(r.m) >= 4096 {
		for k, at := range r.m {
			if now.Sub(at) > RecentKeep {
				delete(r.m, k)
			}
		}
	}
	r.m[key] = now
}

// Changed reports whether key's last change may not be in the copy yet, whose beat is
// at synced (the home's clock).
func (r *Recent) Changed(key string, synced time.Time) bool {
	if key == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.m[key]
	switch {
	case !ok:
		return false
	case !synced.Before(at.Add(RecentMargin)), r.now().Sub(at) > RecentKeep:
		delete(r.m, key)
		return false
	}
	return true
}
