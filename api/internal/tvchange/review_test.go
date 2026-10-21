package tvchange

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// Review fix: a swap that failed puts the change back to ready; the controller does not
// approve it again (approved_by kept) and swap it on the next tick, over and over - it
// waits for an admin.
func TestSwapFailureWaitsForAnAdmin(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newFeedWorld(t, be.DB)
			ctx := context.Background()
			admin := "11111111-1111-4111-8111-111111111111"
			c := w.change(StateReady, []string{"lineup"})
			w.move(c, StateApproved, ActionApprove, &admin, map[string]any{"approved_by": []string{admin}})
			w.move(c, StateSwapping, ActionSwapStart, nil, nil)
			w.move(c, StateReady, ActionSwapFailed, nil, nil)
			cfg, _ := LoadConfig(ctx, be.DB)
			ch, err := w.ctl.Store.Get(ctx, be.DB, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.ctl.ready(ctx, cfg, ch); err != nil {
				t.Fatal(err)
			}
			if ch.State != StateReady {
				t.Fatalf("re-approved by itself after a failed swap: %s", ch.State)
			}
			// an admin acts (re-verifies): the kept approval applies again
			st := StateReady
			if err := w.ctl.Store.Event(ctx, be.DB, c.ID, Event{ActorUserID: &admin, ActorSource: "admin",
				FromState: &st, ToState: &st, Action: "approve_ahead"}); err != nil {
				t.Fatal(err)
			}
			if err := w.ctl.ready(ctx, cfg, ch); err != nil {
				t.Fatal(err)
			}
			if ch.State != StateApproved {
				t.Fatalf("after the admin: %s", ch.State)
			}
		})
	}
}

// Review fix: the event feed waits at a hole in the ids (an earlier event whose
// transaction has not committed yet, e.g. the swap commit's "live"), instead of moving
// its cursor past it for good; a hole that stays is passed after the grace.
func TestFeedWaitsAtAnIDGap(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newFeedWorld(t, be.DB)
			w.feed()
			c := w.change(StateStaged, []string{"lineup"})
			w.move(c, StateVerifying, ActionStartVerify, nil, nil)
			w.feed()
			var top int64
			if err := be.DB.QueryRow(`SELECT MAX(id) FROM tv_change_events`).Scan(&top); err != nil {
				t.Fatal(err)
			}
			// the event numbered top+1 is "still committing"; top+2 is visible already
			if _, err := be.DB.Exec(`INSERT INTO tv_change_events (id, change_id, at, actor_source, from_state,
				to_state, action, detail) VALUES ($1, $2, $3, 'system', 'verifying', 'failed', 'fail', '{}')`,
				top+2, c.ID, ts(be.DB, w.now)); err != nil {
				t.Fatal(err)
			}
			w.feed()
			if got := w.rec.kinds(); len(got) != 0 {
				t.Fatalf("read past the hole: %v", got)
			}
			var pos int64
			_ = be.DB.QueryRow(`SELECT position FROM notification_cursors WHERE name = $1`, feedCursor).Scan(&pos)
			if pos != top {
				t.Fatalf("cursor %d, want %d", pos, top)
			}
			w.now = w.now.Add(feedGapGrace + time.Second)
			w.feed()
			if got := w.rec.kinds(); len(got) != 1 || got[0] != NotifyFailed {
				t.Fatalf("after the grace: %v", got)
			}
		})
	}
}
