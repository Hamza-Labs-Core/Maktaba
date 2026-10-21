package tvchange

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// failReason is the reason of a change's last fail event.
func failReason(t *testing.T, w *svWorld, id string) string {
	t.Helper()
	evs, err := w.ctl.Store.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Action == ActionFail {
			r, _ := evs[i].Detail["reason"].(string)
			return r
		}
	}
	return ""
}

func appends(t *testing.T, s *Store, id string) []string {
	t.Helper()
	evs, err := s.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range evs {
		if e.Action == "append" {
			w, _ := e.Detail["what"].(string)
			out = append(out, w)
		}
	}
	return out
}

var admin1 = &principal.Principal{UserID: "11111111-1111-4111-8111-111111111111", IsAdmin: true}

// Story 33.12 AC3 (the owner's rule): an editor or filler save joins the open change
// whatever opened it - here a channel plan - keeping its number; the plan stays.
func TestSaveJoinsTheOpenChange(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			d := NewDrafts(be.DB)
			digest := "plan-1"
			plan := map[string]any{"c1": map[string]any{"id": "c1", "number": float64(105), "name": "News"}}
			c := &Change{State: StateDraft, Title: "Channel plan #1", Source: map[string]any{"kind": SourceWriteChannels},
				Diff: map[string]any{"pending": map[string]any{"channels": plan, PlanChannels: plan}}}
			if err := d.Store.Insert(ctx, be.DB, c); err != nil {
				t.Fatal(err)
			}
			if err := d.Store.Set(ctx, be.DB, c.ID, map[string]any{"digest": digest}); err != nil {
				t.Fatal(err)
			}
			ref, err := d.Save(ctx, admin1, SourceEditor, "Channel editor", "filler_pool", func(pd *Pending) error {
				pd.SetFillerPool("p1", map[string]any{"name": "Stings"})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.Store.Get(ctx, be.DB, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if ref.ID != c.ID || ref.Number != got.Number {
				t.Fatalf("a new change %+v, want #%d", ref, got.Number)
			}
			if got.SourceKind() != SourceWriteChannels || got.Digest == nil || *got.Digest != digest {
				t.Fatalf("the plan's source changed: %v %v", got.Source, got.Digest)
			}
			if len(got.Pending().FillerPools()) != 1 || len(got.Pending().Channels()) != 1 {
				t.Fatalf("pending %v", got.Pending().M)
			}
			if a := appends(t, d.Store, c.ID); !reflect.DeepEqual(a, []string{"filler_pool"}) {
				t.Fatalf("appends %v", a)
			}
			var n int
			_ = be.DB.QueryRow(`SELECT count(*) FROM tv_changes`).Scan(&n)
			if n != 1 {
				t.Fatalf("%d changes", n)
			}
		})
	}
}

// An approved draft takes no more updates (the owner's rule): the next save opens the
// next change, and the approval stays on the one it was given to.
func TestApprovedDraftTakesNoMoreUpdates(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			d := NewDrafts(be.DB)
			ref, err := d.Save(ctx, admin1, SourceEditor, "Editor", "channel", func(pd *Pending) error {
				pd.AddSched("c1")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			b := "22222222-2222-4222-8222-222222222222"
			if err := d.Store.Set(ctx, be.DB, ref.ID, map[string]any{"approved_by": []string{b},
				"approved_at": time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			next, err := d.Save(ctx, admin1, SourceSettings, "TV settings", "settings_tv", func(pd *Pending) error {
				pd.AddSched("c2")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if next.ID == ref.ID || next.Number != ref.Number+1 {
				t.Fatalf("joined the approved change: %+v after %+v", next, ref)
			}
			c, err := d.Store.Get(ctx, be.DB, ref.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(c.ApprovedBy) != 1 || c.Pending().Sched()[0] != "c1" || len(c.Pending().Sched()) != 1 {
				t.Fatalf("the approved change moved: %v %v", c.ApprovedBy, c.Pending().M)
			}
			open, _ := d.Open(ctx)
			if open == nil || open.ID != next.ID {
				t.Fatalf("open %v", open)
			}
		})
	}
}

// A change that fails verification goes back to draft and is the open change again:
// its report kept, its approval withdrawn, no stage asked; the draft opened while it
// was in flight joins it. The next save joins it too.
func TestFailedVerificationReturnsToTheOpenChange(t *testing.T) {
	be := tvchangetest.Backends(t)[0]
	w := newSVWorld(t, be.DB)
	w.results = passing()
	ctx := context.Background()
	c := w.staged("v3") // the fake slot reports v2: it never becomes healthy
	if err := w.ctl.Store.Set(ctx, w.db, c.ID, map[string]any{"approved_by": []string{"x"},
		"diff": map[string]any{"pending": map[string]any{"sched": []string{"a"}},
			"stage": map[string]any{"requested_at": "2026-06-15T20:00:00Z"}}}); err != nil {
		t.Fatal(err)
	}
	d := NewDrafts(w.db)
	d.Store.Now = w.ctl.Now
	queued, err := d.Save(ctx, admin1, SourceEditor, "Channel editor", "channel", func(pd *Pending) error {
		pd.AddSched("b")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w.tick()
	w.now = w.now.Add(slotStartTimeout + time.Second)
	w.tick()
	got := w.get(c.ID)
	if got.State != StateDraft || failReason(t, w, c.ID) != "slot_not_healthy" {
		t.Fatalf("state %s", got.State)
	}
	if len(got.ApprovedBy) != 0 || got.Diff["stage"] != nil || got.VerifyReport["passed"] != false {
		t.Fatalf("approval %v stage %v report %v", got.ApprovedBy, got.Diff["stage"], got.VerifyReport)
	}
	if s := got.Pending().Sched(); !reflect.DeepEqual(s, []string{"a", "b"}) {
		t.Fatalf("sched %v", s)
	}
	q := w.get(queued.ID)
	if q.State != StateSuperseded || q.SupersededBy == nil || *q.SupersededBy != c.ID ||
		q.EndReason == nil || *q.EndReason != "joined_change" {
		t.Fatalf("the queued draft: %s %v %v", q.State, q.SupersededBy, q.EndReason)
	}
	if a := appends(t, w.ctl.Store, c.ID); !reflect.DeepEqual(a, []string{"joined_change"}) {
		t.Fatalf("appends %v", a)
	}
	// the joined draft's author is an author of the change (require_second_admin)
	if authors, err := w.ctl.Store.Contributors(ctx, w.db, got); err != nil ||
		!reflect.DeepEqual(authors, []string{admin1.UserID}) {
		t.Fatalf("contributors %v %v", authors, err)
	}
	ref, err := d.Save(ctx, admin1, SourceEditor, "Channel editor", "art", func(pd *Pending) error {
		pd.AddSched("c")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != c.ID || ref.Number != got.Number {
		t.Fatalf("the next save opened %+v", ref)
	}
	w.tick() // no stage asked: it waits for the owner
	if got := w.get(c.ID); got.State != StateDraft || got.Diff["stage"] != nil {
		t.Fatalf("restaged by itself: %s %v", got.State, got.Diff["stage"])
	}
}

// MergePending is the pipeline's merge.py (tests/tvrelease/test_merge.py).
func TestMergePendingRules(t *testing.T) {
	const live, gone, oldNew, newNew = "live", "gone", "a1", "b1"
	def := func(id string, number float64, name string) map[string]any {
		return map[string]any{"id": id, "number": number, "name": name, "mode": "shuffle"}
	}
	liveDefs := map[string]map[string]any{live: def(live, 101, "News"), gone: def(gone, 102, "Old")}
	plan := func(number float64, newID string) map[string]any {
		ch := map[string]any{live: def(live, number, "News"), newID: def(newID, 110, "Kids")}
		return map[string]any{"channels": ch, PlanChannels: copyMap(ch),
			"links": []any{map[string]any{"key": "kids", "channel_id": newID}},
			"pools": map[string]any{"write": []any{number}}, "sched": []any{}}
	}
	t.Run("a newer plan replaces the older plan's part", func(t *testing.T) {
		first, second := plan(105, oldNew), plan(106, newNew)
		first["sched"] = []any{live}
		got := MergePending(first, second, liveDefs)
		if !reflect.DeepEqual(got["channels"], second["channels"]) || !reflect.DeepEqual(got["pools"], second["pools"]) {
			t.Fatalf("got %v", got)
		}
		if !reflect.DeepEqual(got["sched"], []string{live}) {
			t.Fatalf("sched %v", got["sched"])
		}
	})
	t.Run("hand edits survive a newer plan", func(t *testing.T) {
		held := plan(105, oldNew)
		ch := copyMap(mapOf(held["channels"]))
		ch[live] = def(live, 105, "News by hand")
		ch[oldNew] = def(oldNew, 110, "Kids by hand")
		ch[gone] = nil
		held["channels"] = ch
		held["filler"] = map[string]any{"pools": map[string]any{"p1": map[string]any{"name": "Stings"}}}
		held["art"] = map[string]any{oldNew: map[string]any{"logo": map[string]any{"path": "/a.png"}}}
		got := MergePending(held, plan(106, newNew), liveDefs)
		out := mapOf(got["channels"])
		if !reflect.DeepEqual(out[live], def(live, 106, "News by hand")) {
			t.Fatalf("live %v", out[live])
		}
		if !reflect.DeepEqual(out[newNew], def(newNew, 110, "Kids by hand")) {
			t.Fatalf("moved %v", out[newNew])
		}
		if _, ok := out[oldNew]; ok {
			t.Fatalf("the older plan's channel stayed: %v", out)
		}
		if v, ok := out[gone]; !ok || v != nil {
			t.Fatalf("hand removal %v", out[gone])
		}
		if !reflect.DeepEqual(got["filler"], held["filler"]) || mapOf(got["art"])[newNew] == nil {
			t.Fatalf("filler %v art %v", got["filler"], got["art"])
		}
	})
	t.Run("newer hand edits win field by field", func(t *testing.T) {
		older := map[string]any{"channels": map[string]any{live: def(live, 101, "Renamed")}, "sched": []any{"a"}}
		newer := map[string]any{"channels": map[string]any{live: def(live, 120, "News")}, "sched": []any{"b"}}
		got := MergePending(older, newer, liveDefs)
		if !reflect.DeepEqual(mapOf(got["channels"])[live], def(live, 120, "Renamed")) {
			t.Fatalf("%v", got["channels"])
		}
		if !reflect.DeepEqual(got["sched"], []string{"a", "b"}) {
			t.Fatalf("sched %v", got["sched"])
		}
	})
	t.Run("a channel the plan adds, deleted by hand, stays deleted", func(t *testing.T) {
		held := plan(105, oldNew)
		pd := &Pending{M: held}
		pd.DropChannel(oldNew)
		got := MergePending(held, map[string]any{"sched": []any{"x"}}, liveDefs)
		if v, ok := mapOf(got["channels"])[oldNew]; !ok || v != nil {
			t.Fatalf("the plan brought it back: %v", got["channels"])
		}
	})
}
