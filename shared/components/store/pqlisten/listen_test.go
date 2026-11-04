package pqlisten

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func next(t *testing.T, l *Listener) store.Event {
	t.Helper()
	select {
	case e := <-l.Events():
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
		return store.Event{}
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

// T09 on lib/pq, against scratch Postgres: a notification arrives parsed; a killed
// connection comes back and says so with a Reload.
func TestRealPostgres(t *testing.T) {
	dsn := os.Getenv("MAKTABA_TEST_PG_URL")
	if dsn == "" {
		t.Skip("MAKTABA_TEST_PG_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	l, err := New(own(dsn, "pqlisten_test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if _, err := db.Exec(`SELECT pg_notify($1, '{"revision":9,"id":"pq-llm","node":null}')`, store.Channel); err != nil {
		t.Fatal(err)
	}
	if e := until(t, l, func(e store.Event) bool { return e.ID == "pq-llm" }); e.Revision != 9 {
		t.Fatalf("got %+v", e)
	}
	if _, err := db.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE application_name = 'pqlisten_test'`); err != nil {
		t.Fatal(err)
	}
	if e := until(t, l, func(e store.Event) bool { return e.Reload }); !e.Reload {
		t.Fatalf("after terminate %+v, want Reload", e)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
