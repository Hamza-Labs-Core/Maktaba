package parental_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Review finding 7: a guardian's approval takes the child's policy row before the
// request's row, as a PIN unlock does. Here a transaction holds the policy row the
// way an unlock under way does, and then closes the waiting request: with the
// approval waiting on the policy row and holding nothing, that goes through at
// once. Taken the other way round the two wait on each other and the database
// rolls one back.
func TestApproveAndUnlockLockInOneOrder(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild)
	req, _, err := e.store.Ask(ctx, uChild, false, []string{libOpen}, parental.RequestTime, "30")
	if err != nil {
		t.Fatal(err)
	}

	unlock, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock.Rollback() }()
	if _, err := unlock.ExecContext(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := unlock.ExecContext(ctx, `SELECT 1 FROM parental_policies WHERE user_id = $1 FOR UPDATE`, uChild); err != nil {
		t.Fatal(err)
	}

	approved := make(chan error, 1)
	go func() {
		_, err := e.store.ApproveWith(ctx, uAdmin, req.ID, parental.Approval{})
		approved <- err
	}()
	// the approval is waiting by now: on the policy row, with the fix
	select {
	case err := <-approved:
		t.Fatalf("the approval did not wait for the policy row: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if _, err := unlock.ExecContext(ctx,
		`UPDATE parental_requests SET status = 'approved', decided_by = $2, decided_at = now() WHERE id = $1`,
		req.ID, uGuardian); err != nil {
		t.Fatalf("the unlock could not close the request while an approval waited: %v", err)
	}
	if err := unlock.Commit(); err != nil {
		t.Fatalf("the unlock was rolled back: %v", err)
	}
	select {
	case err := <-approved:
		if !errors.Is(err, parental.ErrRequestDecided) {
			t.Fatalf("the approval after the unlock = %v, want ErrRequestDecided", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the approval never returned")
	}
	if n := e.count(`SELECT count(*) FROM parental_time_grants WHERE user_id = $1`, uChild); n != 0 {
		t.Fatalf("%d grants: the approval gave minutes for a request the unlock had answered", n)
	}
}
