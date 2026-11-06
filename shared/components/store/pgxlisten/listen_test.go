package pgxlisten

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/lib/pq" // the scratch-PG test's writer

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/testpg"
)

// fakeConn hands out the notes it is given, then drops.
type fakeConn struct {
	notes    chan *pgconn.Notification
	listened []string
	closed   bool
	mu       *sync.Mutex
}

func (f *fakeConn) Listen(_ context.Context, ch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listened = append(f.listened, ch)
	return nil
}

func (f *fakeConn) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	select {
	case n, ok := <-f.notes:
		if !ok {
			return nil, errors.New("connection dropped")
		}
		return n, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeConn) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func next(t *testing.T, l *Listener) store.Event {
	t.Helper()
	select {
	case e := <-l.Events():
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return store.Event{}
	}
}

// T09: a dropped connection reconnects, and the reconnect is a Reload (a notification
// sent while down is never lost to a runner).
func TestReconnectSendsReload(t *testing.T) {
	var mu sync.Mutex
	conns := make(chan *fakeConn, 4)
	dials := 0
	connect := func(context.Context) (Conn, error) {
		mu.Lock()
		dials++
		d := dials
		mu.Unlock()
		if d == 2 {
			return nil, errors.New("refused") // one failed dial between the two good ones
		}
		c := &fakeConn{notes: make(chan *pgconn.Notification, 4), mu: &mu}
		conns <- c
		return c, nil
	}
	var slept []time.Duration
	var errs int
	l := New(connect, Options{
		Sleep:   func(_ context.Context, d time.Duration) { mu.Lock(); slept = append(slept, d); mu.Unlock() },
		OnError: func(error) { mu.Lock(); errs++; mu.Unlock() },
	})
	defer func() { _ = l.Close() }()
	first := <-conns
	if e := next(t, l); !e.Reload {
		t.Fatalf("first event %+v, want Reload", e)
	}
	first.notes <- &pgconn.Notification{Channel: store.Channel, Payload: `{"revision":3,"id":"llm","node":null}`}
	if e := next(t, l); e.Revision != 3 || e.ID != "llm" || e.Reload {
		t.Fatalf("event %+v", e)
	}
	close(first.notes) // the connection drops
	second := <-conns
	if e := next(t, l); !e.Reload {
		t.Fatalf("after the drop %+v, want Reload", e)
	}
	mu.Lock()
	if !first.closed || len(second.listened) != 1 || second.listened[0] != store.Channel {
		t.Fatalf("closed %v, listened %v", first.closed, second.listened)
	}
	if errs != 2 || len(slept) != 2 || slept[0] != time.Second || slept[1] != time.Second {
		t.Fatalf("errors %d, sleeps %v", errs, slept)
	}
	mu.Unlock()
	_ = l.Close()
	if _, ok := <-l.Events(); ok {
		t.Fatal("events open after Close")
	}
}

func TestBackoffDoublesToTheCap(t *testing.T) {
	var mu sync.Mutex
	var slept []time.Duration
	enough := make(chan struct{})
	l := New(func(context.Context) (Conn, error) { return nil, errors.New("down") }, Options{
		Sleep: func(_ context.Context, d time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			slept = append(slept, d)
			if len(slept) == 7 {
				close(enough)
			}
		},
	})
	<-enough
	_ = l.Close()
	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i, w := range want {
		if slept[i] != w*time.Second {
			t.Fatalf("sleeps %v", slept)
		}
	}
}

// own tags dsn's connections with name, so the test terminates only its own listener: the
// other listener package's test shares the scratch database and the channel.
func own(dsn, name string) string {
	switch {
	case strings.Contains(dsn, "://") && strings.Contains(dsn, "?"):
		return dsn + "&application_name=" + name
	case strings.Contains(dsn, "://"):
		return dsn + "?application_name=" + name
	default:
		return dsn + " application_name=" + name
	}
}

// until is the next event ok accepts; events another test sent on the shared channel are
// skipped.
func until(t *testing.T, l *Listener, ok func(store.Event) bool) store.Event {
	t.Helper()
	for {
		if e := next(t, l); ok(e) {
			return e
		}
	}
}

// On scratch Postgres (MAKTABA_TEST_PG_URL, slot 0178 applied by the caller's test DB or
// here): a real notification and a reconnect after pg_terminate_backend.
func TestRealPostgres(t *testing.T) {
	dsn := testpg.Get()
	if dsn == "" {
		t.Skip("MAKTABA_TEST_PG_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	l := New(Dial(own(dsn, "pgxlisten_test")), Options{MinBackoff: 50 * time.Millisecond})
	defer func() { _ = l.Close() }()
	if e := until(t, l, func(e store.Event) bool { return e.Reload }); !e.Reload {
		t.Fatalf("first %+v", e)
	}
	if _, err := db.Exec(`SELECT pg_notify($1, '{"revision":41,"id":"pgx-x","node":null}')`, store.Channel); err != nil {
		t.Fatal(err)
	}
	if e := until(t, l, func(e store.Event) bool { return e.ID == "pgx-x" }); e.Revision != 41 {
		t.Fatalf("got %+v", e)
	}
	if _, err := db.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE application_name = 'pgxlisten_test'`); err != nil {
		t.Fatal(err)
	}
	if e := until(t, l, func(e store.Event) bool { return e.Reload }); !e.Reload {
		t.Fatalf("after terminate %+v, want Reload", e)
	}
}
