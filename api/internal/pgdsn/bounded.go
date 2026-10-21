package pgdsn

import (
	"context"
	"database/sql/driver"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/lib/pq"
)

// BoundedConnector is Connector whose connections give a statement up the moment its
// context ends, even on a connection that hangs. lib/pq, when a context ends, asks
// the server to cancel (over a new connection) and goes on waiting for its answer;
// from a server that is switched off neither ever comes, and the request waits for
// as long as the socket lives. Here the socket is cut instead (its deadline set in
// the past): the statement fails at once, lib/pq takes the connection for bad, and
// the pool drops it. A full node's connections to its home's database are these
// (cluster/nodedb): its home may be a Mac that was switched off.
func BoundedConnector(dsn string) (driver.Connector, error) {
	return connector(dsn, true)
}

// boundedPQ is pq.NewConnector whose connections are bounded.
func boundedPQ(dsn string) (driver.Connector, error) {
	c, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	c.Dialer(capturingDialer{d: net.Dialer{KeepAlive: 15 * time.Second}})
	return &boundedConnector{pq: c}, nil
}

type captureKey struct{}

// capturingDialer dials as lib/pq's own dialer does, and hands the connection it made
// to the Connect that asked for it (through the context: lib/pq dials with it).
type capturingDialer struct{ d net.Dialer }

func (c capturingDialer) Dial(network, address string) (net.Conn, error) {
	return c.d.Dial(network, address)
}

func (c capturingDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	d := c.d
	d.Timeout = timeout
	return d.Dial(network, address)
}

func (c capturingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	nc, err := c.d.DialContext(ctx, network, address)
	if s, ok := ctx.Value(captureKey{}).(*dialSlot); ok && err == nil {
		s.set(nc)
	}
	return nc, err
}

// dialSlot is the connection a Connect dialed; cut once Connect's context ended
// (lib/pq bounds the startup that follows the dial only by connect_timeout).
type dialSlot struct {
	mu  sync.Mutex
	nc  net.Conn
	cut bool
}

func (s *dialSlot) set(nc net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nc = nc
	if s.cut {
		_ = nc.SetDeadline(time.Unix(1, 0))
	}
}

func (s *dialSlot) cutNow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cut = true
	if s.nc != nil {
		_ = s.nc.SetDeadline(time.Unix(1, 0))
	}
}

type boundedConnector struct{ pq driver.Connector }

func (c *boundedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	s := &dialSlot{}
	stop := context.AfterFunc(ctx, s.cutNow)
	dc, err := c.pq.Connect(context.WithValue(ctx, captureKey{}, s))
	if !stop() && err == nil {
		// ctx ended as the connection was made: it may be cut already
		_ = dc.Close()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	nc := s.nc
	s.mu.Unlock()
	return &boundedConn{Conn: dc, nc: nc}, nil
}

func (c *boundedConnector) Driver() driver.Driver { return c.pq.Driver() }

// boundedConn is a lib/pq connection whose statements are cut when their context
// ends (watch); it has the optional interfaces lib/pq's has.
type boundedConn struct {
	driver.Conn
	nc net.Conn
}

var (
	_ driver.ConnPrepareContext = (*boundedConn)(nil)
	_ driver.QueryerContext     = (*boundedConn)(nil)
	_ driver.ExecerContext      = (*boundedConn)(nil)
	_ driver.ConnBeginTx        = (*boundedConn)(nil)
	_ driver.Pinger             = (*boundedConn)(nil)
	_ driver.SessionResetter    = (*boundedConn)(nil)
	_ driver.Validator          = (*boundedConn)(nil)
)

// watch cuts the socket if ctx ends before stop is called.
func (c *boundedConn) watch(ctx context.Context) (stop func() bool) {
	if c.nc == nil || ctx.Done() == nil {
		return func() bool { return true }
	}
	return context.AfterFunc(ctx, func() { _ = c.nc.SetDeadline(time.Unix(1, 0)) })
}

func (c *boundedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stop := c.watch(ctx)
	defer stop()
	st, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &boundedStmt{Stmt: st, c: c}, nil
}

func (c *boundedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	stop := c.watch(ctx)
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		stop()
		return nil, err
	}
	return &boundedRows{Rows: rows, stop: stop}, nil
}

func (c *boundedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	stop := c.watch(ctx)
	defer stop()
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *boundedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	stop := c.watch(ctx)
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		stop()
		return nil, err
	}
	// the transaction lives under ctx (database/sql rolls it back when ctx ends)
	return &boundedTx{Tx: tx, stop: stop}, nil
}

func (c *boundedConn) Ping(ctx context.Context) error {
	stop := c.watch(ctx)
	defer stop()
	return c.Conn.(driver.Pinger).Ping(ctx)
}

func (c *boundedConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

func (c *boundedConn) IsValid() bool { return c.Conn.(driver.Validator).IsValid() }

type boundedStmt struct {
	driver.Stmt
	c *boundedConn
}

var (
	_ driver.StmtQueryContext = (*boundedStmt)(nil)
	_ driver.StmtExecContext  = (*boundedStmt)(nil)
)

func (s *boundedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	stop := s.c.watch(ctx)
	rows, err := s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		stop()
		return nil, err
	}
	return &boundedRows{Rows: rows, stop: stop}, nil
}

func (s *boundedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	stop := s.c.watch(ctx)
	defer stop()
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

type boundedTx struct {
	driver.Tx
	stop func() bool
}

func (t *boundedTx) Commit() error {
	defer t.stop()
	return t.Tx.Commit()
}

func (t *boundedTx) Rollback() error {
	defer t.stop()
	return t.Tx.Rollback()
}

// boundedRows are read under their statement's context until closed; they have the
// optional interfaces lib/pq's rows have.
type boundedRows struct {
	driver.Rows
	stop func() bool
}

var (
	_ driver.RowsNextResultSet              = (*boundedRows)(nil)
	_ driver.RowsColumnTypeScanType         = (*boundedRows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*boundedRows)(nil)
	_ driver.RowsColumnTypeLength           = (*boundedRows)(nil)
	_ driver.RowsColumnTypePrecisionScale   = (*boundedRows)(nil)
)

func (r *boundedRows) Close() error {
	defer r.stop()
	return r.Rows.Close()
}

func (r *boundedRows) HasNextResultSet() bool {
	n, ok := r.Rows.(driver.RowsNextResultSet)
	return ok && n.HasNextResultSet()
}

func (r *boundedRows) NextResultSet() error {
	if n, ok := r.Rows.(driver.RowsNextResultSet); ok {
		return n.NextResultSet()
	}
	return nil
}

func (r *boundedRows) ColumnTypeScanType(i int) reflect.Type {
	if t, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return t.ColumnTypeScanType(i)
	}
	return reflect.TypeOf(new(any)).Elem()
}

func (r *boundedRows) ColumnTypeDatabaseTypeName(i int) string {
	if t, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return t.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}

func (r *boundedRows) ColumnTypeLength(i int) (int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return t.ColumnTypeLength(i)
	}
	return 0, false
}

func (r *boundedRows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return t.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}
