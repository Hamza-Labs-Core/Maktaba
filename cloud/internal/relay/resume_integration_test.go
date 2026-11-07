//go:build integration

package relay

import (
	"context"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Story 50.03 T07 on Postgres: a ticket's server must exist and its owner be
// active; a deleted server or a suspended owner is ErrServerRevoked.
func TestResumeChecksTheServersStanding(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	users := stores.NewUsers(pool.DB)
	srvs := stores.NewServers(pool.DB)
	u, err := users.Create(ctx, "resume@example.test", "x", "R", true)
	if err != nil {
		t.Fatal(err)
	}
	code, err := srvs.MintClaim(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	sv, err := srvs.RedeemClaim(ctx, code, "box", "resume-box", "h", "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := NewTunnelStore(srvs).(ResumeStore)
	got, err := ts.Resume(ctx, sv.ID)
	if err != nil || got.Slug != "resume-box" {
		t.Fatalf("an active server: %+v %v", got, err)
	}
	if _, err := ts.Resume(ctx, "not-a-uuid"); !errors.Is(err, ErrServerRevoked) {
		t.Errorf("an unknown id: %v", err)
	}
	if _, err := pool.DB.ExecContext(ctx, `UPDATE users SET status = 'suspended' WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Resume(ctx, sv.ID); !errors.Is(err, ErrServerRevoked) {
		t.Errorf("a suspended owner: %v", err)
	}
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM servers WHERE id = $1`, sv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Resume(ctx, sv.ID); !errors.Is(err, ErrServerRevoked) {
		t.Errorf("a deleted server: %v", err)
	}
}
