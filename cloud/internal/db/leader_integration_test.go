//go:build integration

package db_test

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

// The worker's leader lock (Epic 50 story 50.17 §2), with periods scaled down: Every stands
// for production's 15 s, StaleAfter for its 60 s.
const every = 300 * time.Millisecond

// node is one api node's worker: its own pool (its own connections), its Leader.
type node struct {
	l      *db.Leader
	cancel context.CancelFunc
	done   chan struct{}
}

func startNode(t *testing.T, url, name string, lead func(context.Context)) *node {
	t.Helper()
	p, err := db.Open(url, 4, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	n := &node{l: &db.Leader{DB: p.DB, LockID: db.WorkerLockID, Node: name, Every: every,
		StaleAfter: 2 * time.Second, Logger: slog.New(slog.DiscardHandler)}, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	go func() { defer close(n.done); n.l.Run(ctx, lead) }()
	t.Cleanup(func() { cancel(); <-n.done })
	return n
}

func holder(t *testing.T, pool *db.Pool) (string, int) {
	t.Helper()
	var name string
	var pid int
	err := pool.DB.QueryRow(`SELECT c.node, c.backend_pid FROM cloud_leaders c
        JOIN pg_locks k ON k.pid = c.backend_pid AND k.locktype = 'advisory' AND k.granted AND k.objid = $1
        WHERE c.lock_id = $1`, db.WorkerLockID).Scan(&name, &pid)
	if err != nil {
		return "", 0
	}
	return name, pid
}

func waitHolder(t *testing.T, pool *db.Pool, within time.Duration, ok func(string) bool) (string, time.Duration) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if name, _ := holder(t, pool); name != "" && ok(name) {
			return name, time.Since(start)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no node took the lock within %s", within)
	return "", 0
}

// T01: three workers on one database. The jobs run on exactly one node at a time, once per
// period: a job ticking every 50 ms runs about as many times as there were periods.
func TestLeaderJobsRunOnOneNode(t *testing.T) {
	pool, url := dbtest.MigratedURL(t)
	var active, maxActive, runs atomic.Int64
	var mu sync.Mutex
	ranOn := map[string]int{}
	job := func(name string) func(context.Context) {
		return func(ctx context.Context) {
			if a := active.Add(1); a > maxActive.Load() {
				maxActive.Store(a)
			}
			defer active.Add(-1)
			tk := time.NewTicker(50 * time.Millisecond)
			defer tk.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
					runs.Add(1)
					mu.Lock()
					ranOn[name]++
					mu.Unlock()
				}
			}
		}
	}
	nodes := []*node{
		startNode(t, url, "api-1", job("api-1")),
		startNode(t, url, "api-2", job("api-2")),
		startNode(t, url, "api-3", job("api-3")),
	}
	waitHolder(t, pool, 2*time.Second, func(string) bool { return true })
	const run = 3 * time.Second
	time.Sleep(run)
	if m := maxActive.Load(); m != 1 {
		t.Fatalf("the jobs ran on %d nodes at once", m)
	}
	leading := 0
	for _, n := range nodes {
		if n.l.Leading() {
			leading++
		}
	}
	if leading != 1 {
		t.Fatalf("%d nodes say they lead", leading)
	}
	periods := int64(run / (50 * time.Millisecond))
	if r := runs.Load(); r < periods*8/10 || r > periods+5 {
		t.Fatalf("%d runs in %d periods", r, periods)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ranOn) != 1 {
		t.Fatalf("the jobs ran on %v", ranOn)
	}
}

// T02: the leader's connection is killed (its node gone): another node holds the lock within
// one period (production: 15 s), and the old one stops its jobs.
func TestLeaderConnectionKilledMovesTheLock(t *testing.T) {
	pool, url := dbtest.MigratedURL(t)
	cutURL, cut := dbtest.Cuttable(t, url)
	var firstStopped atomic.Bool
	first := startNode(t, cutURL, "api-1", func(ctx context.Context) { <-ctx.Done(); firstStopped.Store(true) })
	waitHolder(t, pool, 2*time.Second, func(n string) bool { return n == "api-1" })
	noop := func(ctx context.Context) { <-ctx.Done() }
	startNode(t, url, "api-2", noop)
	startNode(t, url, "api-3", noop)
	time.Sleep(2 * every)
	if n, _ := holder(t, pool); n != "api-1" {
		t.Fatalf("the lock moved from a healthy leader to %s", n)
	}

	cut.Down() // api-1's network goes: Postgres sees its session end and frees the lock
	name, took := waitHolder(t, pool, 2*every, func(n string) bool { return n != "api-1" })
	t.Logf("%s took the lock %s after api-1's connection died", name, took)
	waitFor(t, 3*every, "api-1 to stop its jobs", func() bool { return firstStopped.Load() && !first.l.Leading() })
}

// A node that stops (SIGTERM) gives the lock up at once: its session ends.
func TestLeaderStoppingHandsOver(t *testing.T) {
	pool, url := dbtest.MigratedURL(t)
	noop := func(ctx context.Context) { <-ctx.Done() }
	a := startNode(t, url, "api-1", noop)
	waitHolder(t, pool, 2*time.Second, func(n string) bool { return n == "api-1" })
	startNode(t, url, "api-2", noop)
	a.cancel()
	<-a.done
	waitHolder(t, pool, 2*every, func(n string) bool { return n == "api-2" })
}

// The edge case of a hung holder: it keeps its connection, and so the lock, but its heartbeat
// stops. Past StaleAfter another node ends its backend and takes over.
func TestLeaderEndsAHungHolder(t *testing.T) {
	pool, url := dbtest.MigratedURL(t)
	ctx := context.Background()
	hung, err := pool.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hung.Close() }()
	var got bool
	if err := hung.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, db.WorkerLockID).Scan(&got); err != nil || !got {
		t.Fatalf("lock: %v %v", got, err)
	}
	if _, err := hung.ExecContext(ctx, `INSERT INTO cloud_leaders (lock_id, node, backend_pid, heartbeat_at)
        VALUES ($1, 'hung', pg_backend_pid(), now())`, db.WorkerLockID); err != nil {
		t.Fatal(err)
	}
	startNode(t, url, "api-2", func(ctx context.Context) { <-ctx.Done() })
	// a fresh heartbeat is left alone
	time.Sleep(3 * every)
	if n, _ := holder(t, pool); n != "hung" {
		t.Fatalf("a holder with a fresh heartbeat lost the lock to %s", n)
	}
	// StaleAfter (2 s) after its last heartbeat, api-2 ends it and takes the lock
	name, took := waitHolder(t, pool, 4*time.Second, func(n string) bool { return n == "api-2" })
	t.Logf("%s took the lock from the hung holder %s later", name, took)
}

func waitFor(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited %s for %s", within, what)
}
