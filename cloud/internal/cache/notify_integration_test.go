//go:build integration

package cache_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

// tierCache is a node's cache of a home's plan, as the relay's HomeCache keeps it.
type tierCache struct {
	mu      sync.Mutex
	entries map[string]string
	resets  int
}

func (c *tierCache) Invalidate(_, slug string) { c.mu.Lock(); delete(c.entries, slug); c.mu.Unlock() }
func (c *tierCache) Reset()                    { c.mu.Lock(); c.entries = map[string]string{}; c.resets++; c.mu.Unlock() }
func (c *tierCache) has(slug string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[slug]
	return ok
}

func startBus(t *testing.T, url string) *tierCache {
	t.Helper()
	c := &tierCache{entries: map[string]string{"home-a": "free", "home-b": "free"}}
	b := &cache.Bus{Logger: slog.New(slog.DiscardHandler)}
	b.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := b.Start(ctx, url); err != nil {
		t.Fatal(err)
	}
	return c
}

func within(t *testing.T, d time.Duration, what string, ok func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < d {
		if ok() {
			return time.Since(start)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: not within %s", what, d)
	return 0
}

// T04: a plan change written through one node (the billing webhook's UPDATE) drops the entry on
// every other node within a second, and nothing else; a writer's own Notify does the same.
func TestPlanChangeReachesEveryNode(t *testing.T) {
	pool, url := dbtest.MigratedURL(t)
	ctx := context.Background()
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO users (email, password_hash) VALUES ('o@example.test', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash)
        SELECT id, 'Home', 'home-a', 'x' FROM users`); err != nil {
		t.Fatal(err)
	}
	n1, n2 := startBus(t, url), startBus(t, url)
	if _, err := pool.DB.ExecContext(ctx, `UPDATE servers SET plan = 'basic' WHERE slug = 'home-a'`); err != nil {
		t.Fatal(err)
	}
	took := within(t, time.Second, "both nodes drop home-a", func() bool { return !n1.has("home-a") && !n2.has("home-a") })
	t.Logf("the plan change reached both nodes in %s", took)
	if !n1.has("home-b") || !n2.has("home-b") {
		t.Fatal("an unrelated home was dropped")
	}

	// a change in a transaction that rolls back tells nobody; one that commits does
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Notify(ctx, tx, cache.KindServer, "home-b"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	time.Sleep(300 * time.Millisecond)
	if !n1.has("home-b") {
		t.Fatal("a rolled-back change was announced")
	}
	tx, _ = pool.DB.BeginTx(ctx, nil)
	if err := cache.Notify(ctx, tx, cache.KindServer, "home-b"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	within(t, time.Second, "both nodes drop home-b", func() bool { return !n1.has("home-b") && !n2.has("home-b") })
}

// T05: node 2's LISTEN connection drops; when it reconnects, its cache is emptied (what it
// missed meanwhile is unknown). Node 1 is untouched.
func TestListenerReconnectEmptiesTheCache(t *testing.T) {
	_, url := dbtest.MigratedURL(t)
	cutURL, cut := dbtest.Cuttable(t, url)
	n1, n2 := startBus(t, url), startBus(t, cutURL)
	cut.Down()
	time.Sleep(200 * time.Millisecond)
	cut.Up()
	within(t, 5*time.Second, "node 2 emptied on reconnect", func() bool {
		n2.mu.Lock()
		defer n2.mu.Unlock()
		return n2.resets == 1 && len(n2.entries) == 0
	})
	if n1.resets != 0 || !n1.has("home-a") {
		t.Fatal("node 1's cache was emptied too")
	}
}
