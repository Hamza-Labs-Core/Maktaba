package nodedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeServer answers every statement with one row: its own name. A write on the copy
// fails as a read-only server's would.
type fakeServer struct {
	name     string
	readOnly bool
	down     atomic.Bool
	mu       sync.Mutex
	got      []string
}

func (f *fakeServer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func (f *fakeServer) Connect(context.Context) (driver.Conn, error) {
	if f.down.Load() {
		return nil, errors.New(f.name + ": dial tcp: connection refused")
	}
	return &fakeConn{f: f}, nil
}
func (f *fakeServer) Driver() driver.Driver { return nil }

type fakeConn struct{ f *fakeServer }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }
func (c *fakeConn) Ping(context.Context) error {
	if c.f.down.Load() {
		return driver.ErrBadConn
	}
	return nil
}
func (c *fakeConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.f.down.Load() {
		return nil, driver.ErrBadConn
	}
	c.f.mu.Lock()
	c.f.got = append(c.f.got, q)
	c.f.mu.Unlock()
	return &oneRow{v: c.f.name}, nil
}
func (c *fakeConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if c.f.down.Load() {
		return nil, driver.ErrBadConn
	}
	if c.f.readOnly {
		return nil, errors.New("pq: cannot execute UPDATE in a read-only transaction")
	}
	c.f.mu.Lock()
	c.f.got = append(c.f.got, q)
	c.f.mu.Unlock()
	return driver.RowsAffected(1), nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type oneRow struct {
	v    string
	done bool
}

func (r *oneRow) Columns() []string { return []string{"server"} }
func (r *oneRow) Close() error      { return nil }
func (r *oneRow) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.v
	return nil
}

func rig() (*sql.DB, *Pair, *fakeServer, *fakeServer) {
	cp, home := &fakeServer{name: "copy", readOnly: true}, &fakeServer{name: "home"}
	p := &Pair{Copy: &Source{Name: "copy", Conn: cp}, Home: &Source{Name: "home", Conn: home}}
	db := p.Open(0)
	db.SetMaxOpenConns(1) // one pair: every statement chooses within it
	return db, p, cp, home
}

func server(ctx context.Context, t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMarkedReadsGoToTheCopy(t *testing.T) {
	db, _, _, home := rig()
	defer db.Close()
	reads := CopyReads(context.Background())
	if got := server(reads, t, db); got != "copy" {
		t.Fatalf("a marked read: %s", got)
	}
	if got := server(context.Background(), t, db); got != "home" {
		t.Fatalf("an unmarked statement: %s", got)
	}
	// a write goes to the home whatever its context says it is
	if _, err := db.ExecContext(reads, "UPDATE x SET y = 1"); err != nil {
		t.Fatalf("a write in a request marked a read: %v", err)
	}
	if s := home.seen(); s[len(s)-1] != "UPDATE x SET y = 1" {
		t.Fatalf("the write did not reach the home: %v", s)
	}
	// a transaction stays where it began, marked reads included
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := tx.QueryRowContext(reads, "SELECT 2").Scan(&s); err != nil || s != "home" {
		t.Fatalf("a read in a transaction begun on the home: %s %v", s, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// a read-write transaction in a request marked a read is the home's; a read-only one the copy's
	for _, ro := range []bool{false, true} {
		tx, err := db.BeginTx(reads, &sql.TxOptions{ReadOnly: ro})
		if err != nil {
			t.Fatal(err)
		}
		var s string
		if err := tx.QueryRowContext(reads, "SELECT 3").Scan(&s); err != nil || s != map[bool]string{false: "home", true: "copy"}[ro] {
			t.Fatalf("a transaction (read only %v) in a marked request: %s %v", ro, s, err)
		}
		_ = tx.Rollback()
	}
	if got := server(reads, t, db); got != "copy" {
		t.Fatalf("a marked read after the transaction: %s", got)
	}
}

func TestACopyThatDoesNotAnswerIsSkipped(t *testing.T) {
	db, p, cp, _ := rig()
	defer db.Close()
	cp.down.Store(true) // being made, or rebuilt
	reads := CopyReads(context.Background())
	if got := server(reads, t, db); got != "home" {
		t.Fatalf("a marked read with the copy down: %s", got)
	}
	if !p.Copy.Down() {
		t.Fatal("the copy is not marked down")
	}
	cp.down.Store(false)
	p.CheckAll(context.Background())
	if got := server(reads, t, db); got != "copy" {
		t.Fatalf("a marked read with the copy back: %s", got)
	}
}

func TestTheHomeDownSendsEverythingToTheCopy(t *testing.T) {
	db, p, _, home := rig()
	defer db.Close()
	if got := server(context.Background(), t, db); got != "home" {
		t.Fatalf("home up: %s", got)
	}
	home.down.Store(true)
	p.CheckAll(context.Background())
	if got := server(context.Background(), t, db); got != "copy" {
		t.Fatalf("an unmarked read with the home down: %s", got)
	}
	// a write fails as the copy fails it
	if _, err := db.ExecContext(context.Background(), "UPDATE x SET y = 1"); err == nil {
		t.Fatal("a write succeeded with the home down")
	}
	home.down.Store(false)
	p.CheckAll(context.Background())
	if got := server(context.Background(), t, db); got != "home" {
		t.Fatalf("the home back: %s", got)
	}
}

// A home that goes away between the watch's checks: the statement's new connection
// fails, the home is marked down and the copy answers.
func TestTheHomeFoundDownOnConnect(t *testing.T) {
	db, p, _, home := rig()
	defer db.Close()
	db.SetMaxIdleConns(0) // every statement connects anew
	home.down.Store(true)
	if got := server(context.Background(), t, db); got != "copy" {
		t.Fatalf("an unmarked read with the home just gone: %s", got)
	}
	if !p.Home.Down() {
		t.Fatal("the home is not marked down")
	}
}

func TestWithoutACopyEverythingIsTheHomes(t *testing.T) {
	home := &fakeServer{name: "home"}
	p := &Pair{Home: &Source{Name: "home", Conn: home}}
	db := p.Open(0)
	defer db.Close()
	if got := server(CopyReads(context.Background()), t, db); got != "home" {
		t.Fatalf("a marked read with no copy: %s", got)
	}
}
