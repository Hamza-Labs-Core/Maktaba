package nodedb

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
)

type fakeConn struct{ from string }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not used") }
func (c *fakeConn) Ping(context.Context) error          { return nil }

type fakeConnector struct {
	name  string
	down  atomic.Bool
	calls atomic.Int32
}

func (f *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	f.calls.Add(1)
	if f.down.Load() {
		return nil, errors.New(f.name + ": dial tcp: connection refused")
	}
	return &fakeConn{from: f.name}, nil
}
func (f *fakeConnector) Driver() driver.Driver { return nil }

func rig() (*Router, *fakeConnector, *fakeConnector) {
	local, home := &fakeConnector{name: "local"}, &fakeConnector{name: "home"}
	return &Router{Local: &Source{Name: "local", Conn: local}, Home: &Source{Name: "home", Conn: home}}, local, home
}

func from(t *testing.T, r *Router) string {
	t.Helper()
	c, err := r.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return c.(*fakeConn).from
}

func TestReadsGoToTheCopyWhileItAnswers(t *testing.T) {
	r, local, home := rig()
	if got := from(t, r); got != "local" {
		t.Fatalf("both up: %s", got)
	}
	local.down.Store(true) // being made, or rebuilt
	if got := from(t, r); got != "home" {
		t.Fatalf("the copy not answering: %s", got)
	}
	// known down: the next connection goes to the home without trying the copy
	n := local.calls.Load()
	if got := from(t, r); got != "home" || local.calls.Load() != n {
		t.Fatalf("a copy known down was tried (%s)", got)
	}
	local.down.Store(false)
	r.CheckAll(context.Background()) // one round of pings
	if got := from(t, r); got != "local" {
		t.Fatalf("the copy back: %s", got)
	}

	// the home gone (the outage): the copy answers
	home.down.Store(true)
	r.CheckAll(context.Background())
	if got := from(t, r); got != "local" {
		t.Fatalf("the home gone: %s", got)
	}
	// both gone: the copy's error
	local.down.Store(true)
	if _, err := r.Connect(context.Background()); err == nil {
		t.Fatal("no database answers and a connection was made")
	}
}

func TestWatchMarksASourceDownAndUp(t *testing.T) {
	r, local, _ := rig()
	local.down.Store(true)
	r.CheckAll(context.Background())
	if !r.Local.Down() {
		t.Fatal("a copy that does not answer is not marked down")
	}
	local.down.Store(false)
	r.CheckAll(context.Background())
	if r.Local.Down() {
		t.Fatal("still down after it answered")
	}
}
