package tvchange

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// The owner's rules of 2026-10-28: "only manual needs approval" and "tv changes should not
// block updates" (rules.go ChangeRule, updatehold.go).

// lineupChange inserts a draft from producer source touching n channels (diff.lineup
// changed), the way a channel plan or the editor leaves one.
func (w *auWorld) lineupChange(title, source string, n int) *Change {
	w.t.Helper()
	rows := make([]any, n)
	for i := range rows {
		rows[i] = map[string]any{"id": fmt.Sprintf("ch-%02d", i)}
	}
	c := &Change{State: StateDraft, Title: title, Source: map[string]any{"kind": source},
		Diff: map[string]any{"pending": map[string]any{}, "lineup": map[string]any{"changed": rows}}}
	if err := w.ctl.Store.Insert(context.Background(), w.db, c); err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *auWorld) tick() {
	w.t.Helper()
	if err := w.ctl.Tick(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// notices wires a recorder behind the controller's notifier; the first tick starts the
// event feed at the newest event.
func (w *auWorld) notices() *recorder {
	rec := &recorder{}
	hub := notify.NewHub(w.db, nil)
	hub.Now = w.ctl.Now
	hub.Subscribe(rec)
	w.ctl.Notifier = hub
	w.tick()
	return rec
}

func (w *auWorld) lastAction(id string) string {
	w.t.Helper()
	var a string
	if err := w.db.QueryRow(`SELECT action FROM tv_change_events WHERE change_id = $1 ORDER BY id DESC LIMIT 1`, id).Scan(&a); err != nil {
		w.t.Fatal(err)
	}
	return a
}

func (w *auWorld) actions(id string) []string {
	w.t.Helper()
	rows, err := w.db.Query(`SELECT action FROM tv_change_events WHERE change_id = $1 ORDER BY id`, id)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		out = append(out, a)
	}
	return out
}

func count(xs []string, x string) int {
	n := 0
	for _, y := range xs {
		if y == x {
			n++
		}
	}
	return n
}

// Change #51 (2026-10-28): the pipeline's channel plan touched 27 channels and sat in
// ready waiting for an admin nobody asked. An automatic change goes on the air by its
// kind's rule, with no approval and no limit on the channels it touches.
func TestAutomaticScheduleChangeOf27ChannelsGoesLiveWithoutApproval(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			c := w.lineupChange("Channel plan #51", SourceWriteChannels, 27)
			if c.Origin != OriginAuto {
				t.Fatalf("a channel plan's change: origin %q", c.Origin)
			}
			w.toReady(c.ID, []string{"schedule", "lineup"})
			w.tick()
			got := w.change(c.ID)
			if got.State != StateSwapping || len(got.ApprovedBy) != 0 || w.lastAction(c.ID) != ActionAutoPromote {
				t.Fatalf("the automatic 27-channel change: %s approved_by %v, last %s", got.State, got.ApprovedBy, w.lastAction(c.ID))
			}
			if !slices.Contains(w.enqueued, StageTVSwap) {
				t.Fatalf("no swap: %v", w.enqueued)
			}
		})
	}
}

// A change a person made waits for an admin's approval however small, whatever its
// kind's rule (schedule: immediate); the admin is told once; once approved it goes on.
func TestManualChangeOfOneChannelWaitsForApproval(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			rec := w.notices()
			c := w.lineupChange("Channel editor", SourceEditor, 1)
			if c.Origin != OriginManual {
				t.Fatalf("an editor's change: origin %q", c.Origin)
			}
			w.toReady(c.ID, []string{"schedule"})
			for i := 0; i < 3; i++ {
				w.tick()
			}
			if got := w.change(c.ID); got.State != StateReady {
				t.Fatalf("a manual one-channel change went on without an approval: %s", got.State)
			}
			if got := rec.kinds(); count(got, NotifyReady) != 1 {
				t.Fatalf("the admin is told once that it waits: %v", got)
			}
			admin := "11111111-1111-4111-8111-111111111111"
			ch := w.change(c.ID)
			if err := w.ctl.Store.Move(context.Background(), be.DB, ch, StateApproved, ActionApprove, &admin, nil,
				map[string]any{"approved_by": []string{admin}}); err != nil {
				t.Fatal(err)
			}
			w.tick()
			if got := w.change(c.ID); got.State != StateSwapping {
				t.Fatalf("approved: %s", got.State)
			}
		})
	}
}

// A person's edit joining an automatic change makes it manual: it waits for an approval.
func TestEditJoiningAnAutomaticChangeMakesItManual(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			c := w.lineupChange("Channel plan #52", SourceWriteChannels, 2)
			d := &Drafts{DB: be.DB, Store: w.ctl.Store, EnabledFunc: func(context.Context) bool { return true }}
			ref, err := d.Save(context.Background(), nil, SourceEditor, "Channel editor", "strip",
				func(pd *Pending) error { pd.SetChannel("ch-00", map[string]any{"number": 1.0}); return nil })
			if err != nil || ref.ID != c.ID {
				t.Fatalf("save: %+v %v", ref, err)
			}
			if got := w.change(c.ID); got.Origin != OriginManual {
				t.Fatalf("origin after an edit joined: %s", got.Origin)
			}
		})
	}
}

// An update while a manual change waits for its approval (ready): the update is not held
// (updates.HomeBlockers); the change stays ready until the update's own binary change
// needs the staging slot, is set aside then (not failed, not lost), and once the update
// ended it is checked again on the new release and waits for its approval again - the
// admin not told twice.
func TestManualReadyChangeIsSetAsideByAnUpdateAndCheckedAgain(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			rec := w.notices()
			c := w.lineupChange("Channel editor", SourceEditor, 1)
			w.toReady(c.ID, []string{"lineup"})
			w.tick()
			if n := count(rec.kinds(), NotifyReady); n != 1 {
				t.Fatalf("ready notices %v", rec.kinds())
			}

			run := "run-1"
			w.ctl.UpdateRun = func(context.Context) string { return run }
			w.enqueued = nil
			w.tick()
			if got := w.change(c.ID); got.State != StateReady || holdOf(got)["run_id"] != run {
				t.Fatalf("while the home restarts the change is left as it is: %s %v", got.State, holdOf(got))
			}
			// the new release's binary change: it gets the staging slot first
			st := w.begin(run)
			w.tick()
			got := w.change(c.ID)
			if got.State != StateDraft || w.lastAction(c.ID) != ActionSetAside || !stageRequested(got) {
				t.Fatalf("set aside for the binary change: %s, last %s, stage %v", got.State, w.lastAction(c.ID), got.Diff["stage"])
			}
			if acts := w.actions(c.ID); slices.Contains(acts, ActionFail) {
				t.Fatalf("set aside counted as a failure: %v", acts)
			}
			if count(w.enqueued, StageTVStage) != 1 {
				t.Fatalf("the binary change staged: %v", w.enqueued)
			}
			bin := w.toReady(st.ChangeID, []string{"binary"})
			w.tick()
			if bin = w.change(bin.ID); bin.State != StateSwapping {
				t.Fatalf("the binary change: %s", bin.State)
			}
			if err := w.ctl.Store.Move(context.Background(), be.DB, bin, StateLive, ActionSwap, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			// the update still runs (its watch, its commit): nothing else is staged
			w.enqueued = nil
			w.tick()
			if count(w.enqueued, StageTVStage) != 0 {
				t.Fatalf("staged while the update runs: %v", w.enqueued)
			}

			// the update ended: the change is checked again on the new release
			run = ""
			w.tick()
			if count(w.enqueued, StageTVStage) != 1 {
				t.Fatalf("not staged again after the update: %v", w.enqueued)
			}
			w.toReady(c.ID, []string{"lineup"})
			for i := 0; i < 2; i++ {
				w.tick()
			}
			if got := w.change(c.ID); got.State != StateReady {
				t.Fatalf("a manual change after the update: %s", got.State)
			}
			if n := count(rec.kinds(), NotifyReady); n != 1 {
				t.Fatalf("the admin was told again: %v", rec.kinds())
			}
		})
	}
}

// With no binary change to make, a ready change is left as it is for the whole update
// (neither put on the air by its rule nor checked again), then checked again on the new
// release once it ended.
func TestReadyChangeLeftAsItIsDuringAnUpdate(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			c := w.lineupChange("Channel plan #53", SourceWriteChannels, 1)
			run := "run-2"
			w.ctl.UpdateRun = func(context.Context) string { return run }
			w.toReady(c.ID, []string{"schedule"})
			w.tick()
			w.tick()
			if got := w.change(c.ID); got.State != StateReady {
				t.Fatalf("an automatic change went on during the update: %s", got.State)
			}
			run = ""
			w.enqueued = nil
			w.tick()
			got := w.change(c.ID)
			if got.State != StateDraft || !slices.Contains(w.actions(c.ID), ActionRecheckAfterUpdate) ||
				count(w.enqueued, StageTVStage) != 1 {
				t.Fatalf("after the update: %s %v %v", got.State, w.actions(c.ID), w.enqueued)
			}
			// checked again, it goes on by its rule (automatic)
			w.toReady(c.ID, []string{"schedule"})
			w.tick()
			if got := w.change(c.ID); got.State != StateSwapping {
				t.Fatalf("checked again: %s", got.State)
			}
		})
	}
}

// A change staged or verifying when the update holds the TV is set aside (its staging
// slot released, not failed), stays a draft while the update runs, and is staged and
// verified again once it ended.
func TestStagedChangeIsSetAsideByAnUpdateAndStagedAgain(t *testing.T) {
	for _, state := range []string{StateStaged, StateVerifying} {
		for _, be := range tvchangetest.Backends(t) {
			t.Run(state+"/"+be.Name, func(t *testing.T) {
				w := newAUWorld(t, be.DB)
				c := w.lineupChange("Channel plan #54", SourceWriteChannels, 3)
				ch := w.change(c.ID)
				if err := w.ctl.Store.Move(context.Background(), be.DB, ch, StateStaged, ActionStage, nil, nil,
					map[string]any{"kinds": []string{"schedule"}, "staged_at": w.now}); err != nil {
					t.Fatal(err)
				}
				if state == StateVerifying {
					if err := w.ctl.Store.Move(context.Background(), be.DB, ch, StateVerifying, ActionStartVerify, nil, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				run := "run-3"
				w.ctl.UpdateRun = func(context.Context) string { return run }
				w.enqueued = nil
				w.tick()
				got := w.change(c.ID)
				if got.State != StateDraft || w.lastAction(c.ID) != ActionSetAside || !stageRequested(got) {
					t.Fatalf("set aside: %s %s", got.State, w.lastAction(c.ID))
				}
				w.tick()
				if count(w.enqueued, StageTVStage) != 0 || w.change(c.ID).State != StateDraft {
					t.Fatalf("staged during the update: %v", w.enqueued)
				}
				run = ""
				w.tick()
				if count(w.enqueued, StageTVStage) != 1 {
					t.Fatalf("not staged again after the update: %v", w.enqueued)
				}
				if acts := w.actions(c.ID); slices.Contains(acts, ActionFail) {
					t.Fatalf("counted as failed: %v", acts)
				}
			})
		}
	}
}

// A change verifying on the staging slot when the update holds the TV: set aside, and its
// staging slot released (the update's binary change takes it).
func TestSetAsideReleasesTheStagingSlot(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newSVWorld(t, be.DB)
			w.results = passing()
			c := w.staged("v2")
			w.tick()
			if got := w.get(c.ID); got.State != StateVerifying || len(w.fs.plans) == 0 {
				t.Fatalf("verifying on the slot: %s, plans %d", got.State, len(w.fs.plans))
			}
			w.ctl.UpdateRun = func(context.Context) string { return "run-4" }
			w.tick()
			if got := w.get(c.ID); got.State != StateDraft {
				t.Fatalf("set aside: %s", got.State)
			}
			w.fs.mu.Lock()
			defer w.fs.mu.Unlock()
			if !slices.Contains(w.fs.stopped, c.ID) || w.fs.active != nil {
				t.Fatalf("the staging slot kept: stopped %v", w.fs.stopped)
			}
		})
	}
}

// The controller reads the update that holds the TV from update_runs: a run draining
// the home or applying holds it; a run preparing, or one that ended, does not.
func TestUpdateHoldingReadsUpdateRuns(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" {
		t.Skip("MAKTABA_TEST_PG_URL not set: update_runs is a Postgres table here")
	}
	for _, be := range tvchangetest.Backends(t) {
		if be.Name != "postgres" {
			continue
		}
		w := newAUWorld(t, be.DB)
		if got := w.ctl.UpdateHolding(context.Background()); got != "" {
			t.Fatalf("no run: %q", got)
		}
		var id string
		if err := be.DB.QueryRow(`INSERT INTO update_runs (version, trigger, state) VALUES ('1.5.0', 'auto', 'home_prepared')
			RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if got := w.ctl.UpdateHolding(context.Background()); got != "" {
			t.Fatalf("a run waiting for its gates holds the TV: %q", got)
		}
		for _, st := range []string{"draining", "home_applying"} {
			w.exec(`UPDATE update_runs SET state = $2 WHERE id = $1`, id, st)
			if got := w.ctl.UpdateHolding(context.Background()); got != id {
				t.Fatalf("%s: %q", st, got)
			}
		}
		w.exec(`UPDATE update_runs SET state = 'home_committed' WHERE id = $1`, id)
		if got := w.ctl.UpdateHolding(context.Background()); got != "" {
			t.Fatalf("committed: %q", got)
		}
	}
}
