package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// autoWriteMode makes write_channels' write mode auto (the SQLite test schema has no
// job_stages: it gets the columns read here).
func autoWriteMode(t *testing.T, be tvchangetest.Backend) {
	t.Helper()
	if be.Name == "sqlite" {
		if _, err := be.DB.Exec(`CREATE TABLE IF NOT EXISTS job_stages (stage TEXT PRIMARY KEY,
			write_mode TEXT NOT NULL DEFAULT 'auto')`); err != nil {
			t.Fatal(err)
		}
		if _, err := be.DB.Exec(`INSERT INTO job_stages (stage, write_mode) VALUES ('write_channels', 'auto')
			ON CONFLICT (stage) DO UPDATE SET write_mode = 'auto'`); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := be.DB.Exec(`UPDATE job_stages SET write_mode = 'auto' WHERE stage = 'write_channels'`); err != nil {
		t.Fatal(err)
	}
}

// planChange inserts a channel plan's change (what write_channels opens) in draft.
func planChange(t *testing.T, s *Store, db *sql.DB, digest string) *Change {
	t.Helper()
	ctx := context.Background()
	plan := map[string]any{"c1": map[string]any{"id": "c1", "number": float64(105), "name": "News"}}
	c := &Change{State: StateDraft, Title: "Channel plan #90", Source: map[string]any{"kind": SourceWriteChannels},
		Diff: map[string]any{"pending": map[string]any{"channels": plan, PlanChannels: plan}}}
	if err := s.Insert(ctx, db, c); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, db, c.ID, map[string]any{"digest": digest}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, db, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// checked moves a draft as auto mode does: staged by the tv_stage job, verified and
// ready (no one approved it).
func checked(t *testing.T, s *Store, db *sql.DB, c *Change) {
	t.Helper()
	ctx := context.Background()
	for _, step := range []struct{ to, action string }{
		{StateStaged, ActionStage}, {StateVerifying, ActionStartVerify}, {StateReady, ActionPass}} {
		if err := s.Move(ctx, db, c, step.to, step.action, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set(ctx, db, c.ID, map[string]any{"verify_report": map[string]any{"passed": true}}); err != nil {
		t.Fatal(err)
	}
}

func lastReopen(t *testing.T, s *Store, id string) *Event {
	t.Helper()
	evs, err := s.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var got *Event
	n := 0
	for i := range evs {
		if evs[i].Action == ActionReopen {
			got = &evs[i]
			n++
		}
	}
	if n > 1 {
		t.Fatalf("%d reopen events", n)
	}
	return got
}

// Story 33.12 AC3 (the owner's rule): a plan opens #N; auto mode stages it and it is
// checked (ready, no one approved it); a rules PATCH joins #N and sends it back to
// draft, checked again (one stage asked); the next update joins #N too. Approved, #N
// takes no more: the next update opens #N+1.
func TestUpdatesJoinACheckedChangeUntilApproved(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			autoWriteMode(t, be)
			d := NewDrafts(be.DB)
			c := planChange(t, d.Store, be.DB, "plan-90")
			checked(t, d.Store, be.DB, c)
			open, err := d.Open(ctx)
			if err != nil || open == nil || open.ID != c.ID || open.State != StateReady {
				t.Fatalf("open %v %v", open, err)
			}
			// a rules PATCH of the tv section (rules.SettingsStager)
			id, num, err := d.SaveSettingsDraft(ctx, admin1, "tv",
				map[string]json.RawMessage{"guide_hours": json.RawMessage("6")})
			if err != nil {
				t.Fatal(err)
			}
			if id != c.ID || num != c.Number {
				t.Fatalf("a new change %s #%d, want #%d", id, num, c.Number)
			}
			got, err := d.Store.Get(ctx, be.DB, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StateDraft || len(got.Pending().Channels()) != 1 {
				t.Fatalf("state %s pending %v", got.State, got.Pending().M)
			}
			if s, _ := got.Pending().Settings()["tv"].(map[string]any); s["guide_hours"] != float64(6) {
				t.Fatalf("settings %v", got.Pending().Settings())
			}
			if got.VerifyReport["passed"] != true {
				t.Fatalf("the old report went: %v", got.VerifyReport)
			}
			if st, _ := got.Diff["stage"].(map[string]any); st == nil || st["requested_at"] == nil {
				t.Fatalf("auto mode: no stage asked: %v", got.Diff["stage"])
			}
			ev := lastReopen(t, d.Store, c.ID)
			if ev == nil || *ev.FromState != StateReady || *ev.ToState != StateDraft || ev.Detail["why"] != ReopenWhy {
				t.Fatalf("reopen event %+v", ev)
			}
			if r, _ := ev.Detail["report"].(map[string]any); r["passed"] != true {
				t.Fatalf("report not kept in the event: %v", ev.Detail)
			}
			if a := appends(t, d.Store, c.ID); !reflect.DeepEqual(a, []string{"settings_tv"}) {
				t.Fatalf("appends %v", a)
			}
			// checked again; the next update (an editor save) joins #N again
			checked(t, d.Store, be.DB, got)
			ref, err := d.Save(ctx, admin1, SourceEditor, "Channel editor", "channel", func(pd *Pending) error {
				pd.AddSched("c1")
				return nil
			})
			if err != nil || ref.ID != c.ID {
				t.Fatalf("joined %+v %v", ref, err)
			}
			got, _ = d.Store.Get(ctx, be.DB, c.ID)
			if got.State != StateDraft || got.Digest == nil || *got.Digest != "plan-90" {
				t.Fatalf("state %s digest %v", got.State, got.Digest)
			}
			// approved: the next update opens #N+1
			checked(t, d.Store, be.DB, got)
			if err := d.Store.Set(ctx, be.DB, c.ID, map[string]any{"approved_by": []string{"owner"}}); err != nil {
				t.Fatal(err)
			}
			if o, _ := d.Open(ctx); o != nil {
				t.Fatalf("an approved change is open: %v", o.ID)
			}
			next, err := d.Save(ctx, admin1, SourceSettings, "TV settings", "settings_tv", func(pd *Pending) error {
				pd.AddSched("c2")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if next.ID == c.ID || next.Number != c.Number+1 {
				t.Fatalf("joined the approved change: %+v", next)
			}
			got, _ = d.Store.Get(ctx, be.DB, c.ID)
			if got.State != StateReady || len(got.Pending().Sched()) != 1 {
				t.Fatalf("the approved change moved: %s %v", got.State, got.Pending().Sched())
			}
		})
	}
}

// Two changes no one approved open at once (live: #46 ready, #47 draft opened by a
// rules PATCH and plan #91 under the old rule): the controller's tick joins the newer
// into the older - superseded, end_reason joined_change - and the older goes back to
// draft holding both. Idempotent: the next tick does nothing.
func TestTwoOpenChangesCollapseIntoTheOlder(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			ctl := NewController(be.DB, nil)
			s := ctl.Store
			older := planChange(t, s, be.DB, "plan-90")
			checked(t, s, be.DB, older)
			newer := planChange(t, s, be.DB, "plan-91")
			pd := newer.Pending()
			pd.SetSettings("tv", map[string]json.RawMessage{"guide_hours": json.RawMessage("6")})
			newer.Diff["pending"] = pd.M
			if err := s.Set(ctx, be.DB, newer.ID, map[string]any{"diff": newer.Diff}); err != nil {
				t.Fatal(err)
			}
			if err := ctl.collapseOpen(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Get(ctx, be.DB, older.ID)
			if got.State != StateDraft || got.Digest == nil || *got.Digest != "plan-91" {
				t.Fatalf("older: %s %v", got.State, got.Digest)
			}
			if _, ok := got.Pending().Settings()["tv"]; !ok || len(got.Pending().Channels()) != 1 {
				t.Fatalf("pending %v", got.Pending().M)
			}
			if lastReopen(t, s, older.ID) == nil {
				t.Fatal("no reopen event")
			}
			if a := appends(t, s, older.ID); !reflect.DeepEqual(a, []string{"joined_change"}) {
				t.Fatalf("appends %v", a)
			}
			n, _ := s.Get(ctx, be.DB, newer.ID)
			if n.State != StateSuperseded || n.SupersededBy == nil || *n.SupersededBy != older.ID ||
				n.EndReason == nil || *n.EndReason != "joined_change" {
				t.Fatalf("newer: %s %v %v", n.State, n.SupersededBy, n.EndReason)
			}
			if err := ctl.collapseOpen(ctx); err != nil {
				t.Fatal(err)
			}
			open, _ := s.OpenChanges(ctx, be.DB)
			if len(open) != 1 || open[0].ID != older.ID || len(appends(t, s, older.ID)) != 1 {
				t.Fatalf("open %d", len(open))
			}
		})
	}
}
