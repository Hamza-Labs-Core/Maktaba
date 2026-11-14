//go:build integration

package db_test

import (
	"context"
	"database/sql"
	neturl "net/url"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// Story 50.18 T02, in relay.yml's PgBouncer run (MAKTABA_CLOUD_DB_URL is PgBouncer's
// transaction pool, MAKTABA_CLOUD_DB_SESSION_URL its session pool; skipped elsewhere). The
// leader lock and the cache bus pass their own tests there through the session pool; these
// show what the transaction pool can't carry, which is why database.direct_url exists.

func pgbouncerURLs(t *testing.T) (pooled, session string) {
	t.Helper()
	if os.Getenv(dbtest.SessionEnv) == "" {
		t.Skip(dbtest.SessionEnv + " not set: not behind PgBouncer")
	}
	_, session = dbtest.MigratedURL(t)
	// the same scratch database through the transaction pool
	pooled = sessionToPooled(t, session)
	return pooled, session
}

// sessionToPooled is session's database on MAKTABA_CLOUD_DB_URL's server.
func sessionToPooled(t *testing.T, session string) string {
	t.Helper()
	s, err := neturl.Parse(session)
	if err != nil {
		t.Fatal(err)
	}
	p, err := neturl.Parse(os.Getenv("MAKTABA_CLOUD_DB_URL"))
	if err != nil {
		t.Fatal(err)
	}
	p.Path = s.Path
	return p.String()
}

// A session advisory lock taken through the transaction pool outlives the client that took
// it: the server connection goes back to the pool holding it, and nobody can release it.
func TestTransactionPoolStrandsASessionLock(t *testing.T) {
	pooled, session := pgbouncerURLs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const id = 50180002
	c, err := sql.Open("postgres", pooled)
	if err != nil {
		t.Fatal(err)
	}
	var got bool
	if err := c.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, id).Scan(&got); err != nil || !got {
		t.Fatalf("lock through the transaction pool: %v %v", got, err)
	}
	_ = c.Close()

	s, err := db.Open(session, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	held := func() int {
		var n int
		_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = $1 AND granted`, id).Scan(&n)
		return n
	}
	if n := held(); n != 1 {
		t.Fatalf("the lock's client is gone and %d servers hold it; want 1 (stranded on a pooled server connection)", n)
	}
	// clean up the stranded server connection for the next test
	_, _ = s.DB.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype = 'advisory' AND objid = $1`, id)

	// through the session pool the lock ends with its client
	sc, err := sql.Open("postgres", session)
	if err != nil {
		t.Fatal(err)
	}
	sc.SetMaxOpenConns(1)
	if err := sc.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, id).Scan(&got); err != nil || !got {
		t.Fatalf("lock through the session pool: %v %v", got, err)
	}
	_ = sc.Close()
	deadline := time.Now().Add(5 * time.Second)
	for held() != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := held(); n != 0 {
		t.Fatalf("the session pool's client is gone and the lock is still held by %d", n)
	}
}

// A notification sent through the transaction pool reaches a LISTEN on the session pool, and
// the migrator's lock works through the transaction pool (two at once both finish).
func TestSessionPoolListensAndTheMigratorRunsPooled(t *testing.T) {
	pooled, session := pgbouncerURLs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	l := pq.NewListener(session, time.Second, 5*time.Second, nil)
	defer func() { _ = l.Close() }()
	if err := l.Listen("cloud_cache"); err != nil {
		t.Fatal(err)
	}
	p, err := db.Open(pooled, 8, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if _, err := p.DB.ExecContext(ctx, `SELECT pg_notify('cloud_cache', 'plan:1')`); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-l.Notify:
		if n == nil || n.Extra != "plan:1" {
			t.Fatalf("notification: %+v", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no notification on the session pool's LISTEN")
	}

	if _, err := p.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 500001`); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- db.NewMigrator(p, migrations.FS, ".").Up(ctx) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("Up through the transaction pool: %v", err)
		}
	}
}
