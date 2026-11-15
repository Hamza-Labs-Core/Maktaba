package tvchange

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// claimEnqueue is the one gate in front of a rollback operation's reverse commit:
// the API's rollback request and the controller's tick both claim before they
// enqueue, so only one of them wins. A released claim (its enqueue failed) and a
// stale one (older than reaskAfter: the job may be lost) can be claimed again; a
// committed operation never.
func TestClaimEnqueueLetsOneCallerWin(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" {
		t.Skip("MAKTABA_TEST_PG_URL not set: tv_swap_ops is a Postgres table")
	}
	for _, be := range tvchangetest.Backends(t) {
		if be.Name != "postgres" {
			continue
		}
		ctx := context.Background()
		now := time.Now().UTC()
		c := NewController(be.DB, nil)
		c.Now = func() time.Time { return now }
		var change string
		if err := be.DB.QueryRow(`INSERT INTO tv_changes (state, title, source) VALUES ('live', 't', '{"kind": "manual"}')
			RETURNING id`).Scan(&change); err != nil {
			t.Fatal(err)
		}
		op := func(committed bool) int64 {
			var id int64
			if err := be.DB.QueryRow(`INSERT INTO tv_swap_ops (change_id, kind, state, committed, created_by, reason)
				VALUES ($1, 'rollback', 'planned', $2, 'test', 'manual') RETURNING id`, change, committed).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		claim := func(id int64) bool {
			t.Helper()
			ok, err := c.claimEnqueue(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}

		id := op(false)
		if !claim(id) {
			t.Fatal("the first claim lost")
		}
		if claim(id) {
			t.Fatal("a second claim won while the first is fresh")
		}
		c.releaseEnqueue(ctx, id)
		if !claim(id) {
			t.Fatal("a released claim could not be claimed again")
		}
		now = now.Add(reaskAfter + time.Second)
		if !claim(id) {
			t.Fatal("a stale claim could not be claimed again")
		}
		if claim(op(true)) {
			t.Fatal("a committed operation was claimed")
		}
	}
}
