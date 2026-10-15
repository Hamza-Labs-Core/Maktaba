// Package queuestest is a tiny scripted database/sql driver for unit
// tests of the queue handlers. No Postgres (or sqlmock) is needed: a
// test registers rules ("when a statement contains this fragment,
// answer with these rows") and afterwards inspects the statements the
// handler actually ran, with their arguments.
//
// It deliberately verifies nothing about SQL *correctness* - that is
// the job of the //go:build integration tests against a migrated
// Postgres. What it does pin is: which statements run, in which order,
// with which arguments, and how the handler maps rows to JSON.
package queuestest

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// Rule answers every statement whose whitespace-normalised, lower-cased
// text contains Match (also lower-cased). The first matching rule wins.
type Rule struct {
	Match string
	Cols  []string
	Rows  [][]driver.Value
	// Err, when set, is returned instead of rows.
	Err error
	// Affected is the RowsAffected answer for Exec.
	Affected int64
	// Once makes the rule single-use (consumed on first match), so a
	// test can answer the same fragment differently on the next call.
	Once bool

	used bool
}

// Call is one executed statement.
type Call struct {
	SQL  string // normalised
	Args []driver.Value
	Exec bool
}

// DB is the scripted database handle.
type DB struct {
	*sql.DB
	state *state
}

type state struct {
	mu    sync.Mutex
	rules []*Rule
	calls []Call
}

var (
	regMu   sync.Mutex
	nextID  atomic.Int64
	states  = map[string]*state{}
	regOnce sync.Once
)

// New returns a *sql.DB backed by the scripted driver.
func New(rules ...*Rule) *DB {
	regOnce.Do(func() { sql.Register("queuestest", drv{}) })
	id := fmt.Sprintf("db%d", nextID.Add(1))
	st := &state{rules: rules}
	regMu.Lock()
	states[id] = st
	regMu.Unlock()
	db, err := sql.Open("queuestest", id)
	if err != nil {
		panic(err)
	}
	// One connection keeps transactions and ordering deterministic.
	db.SetMaxOpenConns(1)
	return &DB{DB: db, state: st}
}

// Add appends a rule after construction.
func (d *DB) Add(r *Rule) {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	d.state.rules = append(d.state.rules, r)
}

// Calls returns a copy of the recorded statements.
func (d *DB) Calls() []Call {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	return append([]Call(nil), d.state.calls...)
}

// Find returns the recorded calls whose SQL contains frag.
func (d *DB) Find(frag string) []Call {
	frag = Norm(frag)
	var out []Call
	for _, c := range d.Calls() {
		if strings.Contains(c.SQL, frag) {
			out = append(out, c)
		}
	}
	return out
}

// Norm lower-cases and collapses whitespace.
func Norm(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

type drv struct{}

func (drv) Open(name string) (driver.Conn, error) {
	regMu.Lock()
	st := states[name]
	regMu.Unlock()
	if st == nil {
		return nil, errors.New("queuestest: unknown db")
	}
	return &conn{st: st}, nil
}

type conn struct{ st *state }

func (c *conn) Prepare(q string) (driver.Stmt, error) { return &stmt{c: c, q: q}, nil }
func (c *conn) Close() error                          { return nil }
func (c *conn) Begin() (driver.Tx, error)             { return tx{c: c}, nil }

type tx struct{ c *conn }

func (t tx) Commit() error {
	t.c.record("COMMIT", nil, true)
	return nil
}
func (t tx) Rollback() error {
	t.c.record("ROLLBACK", nil, true)
	return nil
}

func (c *conn) record(q string, args []driver.Value, exec bool) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	c.st.calls = append(c.st.calls, Call{SQL: Norm(q), Args: args, Exec: exec})
}

func (c *conn) match(q string) *Rule {
	nq := Norm(q)
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	for _, r := range c.st.rules {
		if r.Once && r.used {
			continue
		}
		if strings.Contains(nq, Norm(r.Match)) {
			r.used = true
			return r
		}
	}
	return nil
}

type stmt struct {
	c *conn
	q string
}

func (s *stmt) Close() error  { return nil }
func (s *stmt) NumInput() int { return -1 }

func vals(a []driver.Value) []driver.Value { return append([]driver.Value(nil), a...) }

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	s.c.record(s.q, vals(args), true)
	r := s.c.match(s.q)
	if r == nil {
		return driver.RowsAffected(0), nil
	}
	if r.Err != nil {
		return nil, r.Err
	}
	return driver.RowsAffected(r.Affected), nil
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	s.c.record(s.q, vals(args), false)
	r := s.c.match(s.q)
	if r == nil {
		return &rows{}, nil
	}
	if r.Err != nil {
		return nil, r.Err
	}
	return &rows{cols: r.Cols, data: r.Rows}, nil
}

type rows struct {
	cols []string
	data [][]driver.Value
	i    int
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { return nil }
func (r *rows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.i])
	r.i++
	return nil
}
