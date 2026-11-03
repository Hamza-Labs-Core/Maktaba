package pqlisten

import (
	"database/sql"
	"os"
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
	l, err := New(dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if _, err := db.Exec(`SELECT pg_notify($1, '{"revision":9,"id":"llm","node":null}')`, store.Channel); err != nil {
		t.Fatal(err)
	}
	if e := next(t, l); e.Revision != 9 || e.ID != "llm" {
		t.Fatalf("got %+v", e)
	}
	if _, err := db.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE query LIKE 'LISTEN%' AND pid <> pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	if e := next(t, l); !e.Reload {
		t.Fatalf("after terminate %+v, want Reload", e)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
