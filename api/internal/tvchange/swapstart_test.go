package tvchange

import (
	"context"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// The swap job could not be enqueued: the change goes back to approved and its
// history says so (swapping -> approved, swap_not_started) — the swapping event
// is not left standing as if the swap had begun.
func TestSwapNotStartedRevertsWithAnEvent(t *testing.T) {
	be := tvchangetest.Backends(t)[0]
	w := newSVWorld(t, be.DB)
	c := w.staged("v2")
	w.exec(`UPDATE tv_changes SET state = 'approved' WHERE id = $1`, c.ID)
	w.ctl.Enqueue = func(context.Context, string, map[string]any, string) error { return errors.New("queue down") }
	actor := "u1"
	if err := w.ctl.Promote(context.Background(), w.get(c.ID), &actor); err == nil {
		t.Fatal("no error with the queue down")
	}
	if got := w.get(c.ID); got.State != StateApproved {
		t.Fatalf("state %s after the failed enqueue", got.State)
	}
	evs, err := w.ctl.Store.Events(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var last *Event
	for i := range evs {
		if last == nil || evs[i].ID > last.ID {
			last = &evs[i]
		}
	}
	if last == nil || last.Action != ActionSwapNotStarted || last.FromState == nil || *last.FromState != StateSwapping ||
		last.ToState == nil || *last.ToState != StateApproved || last.Detail["error"] != "queue down" {
		t.Fatalf("last event %+v", last)
	}
}
