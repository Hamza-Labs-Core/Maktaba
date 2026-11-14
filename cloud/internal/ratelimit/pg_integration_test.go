//go:build integration

package ratelimit_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
)

// three api nodes: each its own pool and its own limiter, one table
func nodes(t *testing.T, url string, capacity, rate float64) []*ratelimit.PG {
	t.Helper()
	var out []*ratelimit.PG
	for i := 0; i < 3; i++ {
		p, err := db.Open(url, 4, 1, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = p.Close() })
		out = append(out, ratelimit.NewPG(p.DB, "auth", capacity, rate, slog.New(slog.DiscardHandler)))
	}
	return out
}

// T03: 31 sign-ins from one address, round robin over three nodes: the 31st is refused, as on
// one node (burst 30). Another address has its own bucket.
func TestPGLimitHoldsAcrossNodes(t *testing.T) {
	_, url := dbtest.MigratedURL(t)
	ns := nodes(t, url, 30, 5.0/60) // a slow refill, so the test's own pace refills nothing
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if !ns[i%3].Allow(ctx, "ip:203.0.113.7") {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if ns[30%3].Allow(ctx, "ip:203.0.113.7") {
		t.Fatal("the 31st attempt across three nodes was allowed")
	}
	if !ns[1].Allow(ctx, "ip:203.0.113.8") {
		t.Fatal("another address was refused")
	}
}

// A refusal takes nothing: the bucket refills at its rate whatever the refused requests, and
// each node sees the refill.
func TestPGRefillsAndRefusalsAreFree(t *testing.T) {
	_, url := dbtest.MigratedURL(t)
	ns := nodes(t, url, 2, 2) // one token every 500 ms
	ctx := context.Background()
	if !ns[0].Allow(ctx, "k") || !ns[1].Allow(ctx, "k") {
		t.Fatal("the burst was refused")
	}
	empty := time.Now()
	for i := 0; time.Since(empty) < 300*time.Millisecond; i++ { // hammering while empty must not push the refill back
		if ns[i%3].Allow(ctx, "k") {
			t.Fatal("allowed with the bucket empty")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(time.Until(empty.Add(600 * time.Millisecond)))
	if !ns[2].Allow(ctx, "k") {
		t.Fatal("no token after the refill period: refusals cost tokens")
	}
	if ns[0].Allow(ctx, "k") {
		t.Fatal("two tokens from one refill")
	}
}

// Many requests at once on three nodes take exactly the burst: the UPSERT serializes on the row.
func TestPGConcurrentTakesExactlyTheBurst(t *testing.T) {
	_, url := dbtest.MigratedURL(t)
	ns := nodes(t, url, 30, 0.001)
	ctx := context.Background()
	results := make(chan bool, 90)
	for i := 0; i < 90; i++ {
		go func(i int) { results <- ns[i%3].Allow(ctx, "ip:198.51.100.1") }(i)
	}
	allowed := 0
	for i := 0; i < 90; i++ {
		if <-results {
			allowed++
		}
	}
	if allowed != 30 {
		t.Fatalf("%d of 90 concurrent requests allowed, want 30", allowed)
	}
}

// With the database away the node's own bucket decides (those paths need the database anyway),
// and the middleware answers 429 with Retry-After.
func TestPGFallsBackToTheNodeBucket(t *testing.T) {
	_, url := dbtest.MigratedURL(t)
	cutURL, cut := dbtest.Cuttable(t, url)
	p, err := db.Open(cutURL, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	l := ratelimit.NewPG(p.DB, "mail", 2, 1.0/12, slog.New(slog.DiscardHandler))
	cut.Down()
	h := l.Middleware(func(*http.Request) string { return "ip:x" })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/auth/password/forgot", nil))
		codes = append(codes, rec.Code)
		if rec.Code == 429 && rec.Header().Get("Retry-After") != "12" {
			t.Fatalf("Retry-After %q", rec.Header().Get("Retry-After"))
		}
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("codes with the database away: %v", codes)
	}
}

// The worker's sweep deletes the buckets idle for a day, and only those.
func TestSweepPG(t *testing.T) {
	pool, _ := dbtest.MigratedURL(t)
	ctx := context.Background()
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO cloud_rate_buckets (key, tokens, updated_at) VALUES
        ('auth:old', 1, now() - INTERVAL '2 days'), ('auth:new', 1, now())`); err != nil {
		t.Fatal(err)
	}
	n, err := ratelimit.SweepPG(ctx, pool.DB, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("swept %d: %v", n, err)
	}
	var left string
	if err := pool.DB.QueryRowContext(ctx, `SELECT string_agg(key, ',') FROM cloud_rate_buckets`).Scan(&left); err != nil || left != "auth:new" {
		t.Fatalf("left %q: %v", left, err)
	}
}
