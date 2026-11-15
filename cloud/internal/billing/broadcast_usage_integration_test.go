//go:build integration

package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// PublisherTx reads the home inside the caller's transaction: it sees the transaction's own plan
// change, which a read on the pool (Publisher) does not until the commit. Invite creation relies
// on it to read the plan again under the home's row lock.
func TestPublisherTxReadsInsideTheTransaction(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "P", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan)
	        VALUES ($1, 'p', $2, 'x', 'pro') RETURNING id`, u.ID, "p"+uuid.NewString()[:8]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	store := &BroadcastStore{DB: pool.DB}
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE servers SET plan = 'free' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	in, err := PublisherTx(ctx, tx, id)
	if err != nil || in.Plan != PlanFree || in.Tier() != nil {
		t.Fatalf("inside the transaction: %+v %v", in, err)
	}
	out, err := store.Publisher(ctx, id)
	if err != nil || out.Plan != PlanPro || out.Tier() == nil {
		t.Fatalf("outside before the commit: %+v %v", out, err)
	}
	if _, err := PublisherTx(ctx, tx, "not-a-uuid"); !errors.Is(err, ErrUnknownPublisher) {
		t.Fatalf("a bad id: %v", err)
	}
}
