//go:build integration

package broadcast

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	billingh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// An invite created while a plan change that ends guest sharing is in flight can't slip past
// the revocation (story 50.20's edge cases). The interleaving is fixed, not timed: the plan
// change's transaction runs SetEffectivePlan and holds its row locks; the create, which already
// read the old plan, then waits on the home's row lock (seen in pg_stat_activity), and the plan
// change commits. The create must read the plan again under that lock and refuse; before that
// re-check it inserted a live invite on a Free home (201).
func TestInviteCreateRacingAPlanChange(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "R", true)
	if err != nil {
		t.Fatal(err)
	}
	var home string
	if _, err := pool.DB.ExecContext(ctx, `UPDATE users SET status = 'active', plan = 'pro' WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan, broadcast_test_publisher)
        VALUES ($1, 'r', $2, 'x', 'pro', true) RETURNING id`, u.ID, "r"+uuid.NewString()[:8]).Scan(&home); err != nil {
		t.Fatal(err)
	}
	var seed [32]byte
	key, err := bc.KeyFromSeed(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{DB: pool.DB, Store: &billingpkg.BroadcastStore{DB: pool.DB}, Key: &key}

	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := billingh.SetEffectivePlan(ctx, tx, u.ID, billingpkg.PlanFree); err != nil {
		t.Fatal(err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		d.CreateInvite(w, httptest.NewRequest(http.MethodPost, "/v1/broadcast/invites", strings.NewReader(`{}`)), actor{server: home})
		done <- w
	}()
	// the create has passed its first plan check (Pro: the plan change hasn't committed) and
	// waits for the home's row, which the plan change holds
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
            WHERE datname = current_database() AND wait_event_type = 'Lock'
              AND query LIKE '%FROM servers WHERE id = $1 FOR UPDATE%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case w := <-done:
			t.Fatalf("the create finished without waiting for the plan change: %d %s", w.Code, w.Body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the create never waited on the home's row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	w := <-done
	if w.Code != http.StatusPaymentRequired || !strings.Contains(w.Body.String(), "broadcast_needs_plan") {
		t.Fatalf("an invite made across the plan's end: %d %s", w.Code, w.Body)
	}
	var live int
	if err := pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_invites WHERE server_id = $1 AND revoked_at IS NULL`, home).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("a Free home has %d live invites", live)
	}
}
