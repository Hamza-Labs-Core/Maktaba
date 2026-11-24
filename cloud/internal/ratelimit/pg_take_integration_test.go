//go:build integration

package ratelimit

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

// A refused take must say refused even when its statement's time equals the bucket's last
// take: two requests whose transactions start in the same microsecond (90 at once over three
// pools) share now(), and a decision read from "updated_at = now()" counted the refused one
// as allowed (31 of 90 with a burst of 30). One transaction gives both statements one now().
func TestPGRefusedTakeAtTheSameInstantIsRefused(t *testing.T) {
	pool, _ := dbtest.MigratedURL(t)
	ctx := context.Background()
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// the last allowed take left half a token, at this transaction's now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloud_rate_buckets (key, tokens, updated_at) VALUES ('auth:k', 0.5, now())`); err != nil {
		t.Fatal(err)
	}
	ok, err := take(ctx, tx, "auth:k", 30, 0.001)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("half a token and no time since: allowed")
	}
	var tokens float64
	if err := tx.QueryRowContext(ctx, `SELECT tokens FROM cloud_rate_buckets WHERE key = 'auth:k'`).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if tokens != 0.5 {
		t.Fatalf("a refusal changed the bucket: %v tokens", tokens)
	}
}

// A take whose transaction started before the bucket's last take (it waited on the row's
// lock) refills nothing and does not move updated_at back: moving it back would refill the
// gap again on the next take.
func TestPGTakeNeverMovesTheBucketBack(t *testing.T) {
	pool, _ := dbtest.MigratedURL(t)
	ctx := context.Background()
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloud_rate_buckets (key, tokens, updated_at) VALUES ('auth:k', 3, now() + interval '1 second')`); err != nil {
		t.Fatal(err)
	}
	ok, err := take(ctx, tx, "auth:k", 30, 1)
	if err != nil || !ok {
		t.Fatalf("take: %v %v", ok, err)
	}
	var tokens float64
	var ahead bool
	if err := tx.QueryRowContext(ctx, `SELECT tokens, updated_at = now() + interval '1 second' FROM cloud_rate_buckets WHERE key = 'auth:k'`).Scan(&tokens, &ahead); err != nil {
		t.Fatal(err)
	}
	if tokens != 2 || !ahead {
		t.Fatalf("tokens %v, updated_at kept: %v; want 2 and kept", tokens, ahead)
	}
}
