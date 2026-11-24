//go:build integration

package stores

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

// A home claimed after its owner chose a plan has that plan and its extra streams from the
// start, as the homes the account already had got them at the plan change: the column's
// defaults (free, 0) refused the relay to it (402 relay_needs_plan) until the plan was set again.
func TestRedeemClaimGivesTheHomeItsOwnersPlan(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	users := NewUsers(pool.DB)
	servers := NewServers(pool.DB)
	for _, tc := range []struct {
		plan  string
		extra int
	}{{"free", 0}, {"basic", 0}, {"pro", 2}} {
		owner, err := users.Create(ctx, "o-"+uuid.NewString()+"@example.com", "x", "O", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := users.SetPlan(ctx, owner.ID, tc.plan); err != nil {
			t.Fatal(err)
		}
		if tc.extra > 0 {
			if _, err := pool.DB.ExecContext(ctx, `INSERT INTO subscriptions (user_id, provider, plan, status, extra_streams)
				VALUES ($1, 'manual', $2, 'active', $3)`, owner.ID, tc.plan, tc.extra); err != nil {
				t.Fatal(err)
			}
		}
		code, err := servers.MintClaim(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		slug := "s" + uuid.NewString()[:8]
		sv, err := servers.RedeemClaim(ctx, code, "h", slug, "x", "", nil)
		if err != nil {
			t.Fatalf("%s: redeem: %v", tc.plan, err)
		}
		if sv.Plan != tc.plan || sv.ExtraStreams != tc.extra {
			t.Fatalf("%s: the claimed home has plan %q and %d extra streams, want %q and %d", tc.plan, sv.Plan, sv.ExtraStreams, tc.plan, tc.extra)
		}
		got, err := servers.BySlug(ctx, slug)
		if err != nil || got.Plan != tc.plan || got.ExtraStreams != tc.extra {
			t.Fatalf("%s: BySlug: %+v %v", tc.plan, got, err)
		}
	}
}
