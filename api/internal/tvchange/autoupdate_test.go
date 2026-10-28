package tvchange

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// The owner's rule of 2026-10-28, "only manual needs approval" (with Story 35.6 AC2): a
// change a person made needs an approval whatever its source and kinds, and whatever the
// rules say; an automatic one follows its kinds' auto rules - with every rule stored as
// "approval" (before the rule) that reads immediate - and the updater's binary change
// alone is immediate even under quiet_window.
func TestOnlyManualChangesNeedApproval(t *testing.T) {
	approval := Config{AutoPromote: map[string]string{}}
	for _, k := range Kinds {
		approval.AutoPromote[k] = RuleApproval
	}
	sources := []string{SourceAutoUpdate, SourceManual, SourceEditor, SourceWriteChannels, SourceSettings, SourceSchedule, ""}
	sets := [][]string{nil}
	for _, k := range Kinds {
		sets = append(sets, []string{k})
		if k != "binary" {
			sets = append(sets, []string{"binary", k}, []string{k, "binary"})
		}
	}
	for _, origin := range []string{OriginAuto, OriginManual, ""} {
		for _, src := range sources {
			for _, kinds := range sets {
				ch := &Change{Source: map[string]any{"kind": src}, Kinds: kinds, Origin: origin}
				want := RuleApproval
				if origin == OriginAuto {
					want = RuleImmediate
				}
				if got := ChangeRule(approval, ch); got != want {
					t.Errorf("origin %q source %q kinds %v: %s, want %s", origin, src, kinds, got, want)
				}
			}
		}
	}
	quiet := Config{AutoPromote: map[string]string{"binary": RuleQuietWindow, "schedule": RuleImmediate}}
	if got := ChangeRule(quiet, &Change{Origin: OriginAuto, Source: map[string]any{"kind": SourceSchedule},
		Kinds: []string{"schedule", "binary"}}); got != RuleQuietWindow {
		t.Errorf("an automatic change takes its kinds' strictest auto rule: %s", got)
	}
	if got := ChangeRule(quiet, &Change{Origin: OriginAuto, Source: map[string]any{"kind": SourceAutoUpdate},
		Kinds: []string{"binary"}}); got != RuleImmediate {
		t.Errorf("the updater's binary change: %s", got)
	}
	if got := ChangeRule(quiet, &Change{Origin: OriginManual, Source: map[string]any{"kind": SourceManual},
		Kinds: []string{"binary"}}); got != RuleApproval {
		t.Errorf("a manual binary change under quiet_window: %s", got)
	}
	// who opens a change decides its origin
	admin := "11111111-1111-4111-8111-111111111111"
	for kind, want := range map[string]string{SourceWriteChannels: OriginAuto, SourceSchedule: OriginAuto,
		SourceAutoUpdate: OriginAuto, SourceEditor: OriginManual, SourceSettings: OriginManual,
		SourceManual: OriginManual, SourceRollback: OriginAuto, "": OriginManual} {
		if got := OriginFor(kind, nil); got != want {
			t.Errorf("OriginFor(%q) = %s, want %s", kind, got, want)
		}
	}
	if OriginFor(SourceRollback, &admin) != OriginManual {
		t.Error("a rollback an admin pressed is manual")
	}
}

const sumNew = "1111111111111111111111111111111111111111111111111111111111111111"

// auWorld is a controller over one backend with a live TV release on the old binary.
type auWorld struct {
	t        *testing.T
	db       *sql.DB
	ctl      *Controller
	now      time.Time
	enqueued []string
	live     string
}

func newAUWorld(t *testing.T, db *sql.DB) *auWorld {
	w := &auWorld{t: t, db: db, now: time.Date(2026, 10, 25, 3, 10, 0, 0, time.UTC)}
	w.ctl = NewController(db, nil)
	w.ctl.Now = func() time.Time { return w.now }
	w.ctl.Store.Now = w.ctl.Now
	w.ctl.Enqueue = func(_ context.Context, stage string, _ map[string]any, _ string) error {
		w.enqueued = append(w.enqueued, stage)
		return nil
	}
	w.live = uuid.NewString()
	w.exec(`INSERT INTO tv_releases (id, digest, sched_gen, streaming_version, streaming_sha256) VALUES ($1, $2, 0, 'v1.4.0', '')`,
		w.live, "sha256:live")
	w.exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch) VALUES ('cluster', $1, 0, 0)`, w.live)
	return w
}

func (w *auWorld) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

func (w *auWorld) begin(run string) AutoUpdateStatus {
	w.t.Helper()
	st, err := w.ctl.BeginAutoUpdate(context.Background(), run, "v1.5.0", sumNew)
	if err != nil {
		w.t.Fatal(err)
	}
	return st
}

func (w *auWorld) change(id string) *Change {
	w.t.Helper()
	ch, err := w.ctl.Store.Get(context.Background(), w.db, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return ch
}

// toReady walks a change through its stage and checks as the pipeline and the
// controller would, with kinds.
func (w *auWorld) toReady(id string, kinds []string) *Change {
	w.t.Helper()
	ctx := context.Background()
	ch := w.change(id)
	if err := w.ctl.Store.Move(ctx, w.db, ch, StateStaged, ActionStage, nil, nil,
		map[string]any{"kinds": kinds, "staged_at": w.now}); err != nil {
		w.t.Fatal(err)
	}
	if err := w.ctl.Store.Move(ctx, w.db, ch, StateVerifying, ActionStartVerify, nil, nil, nil); err != nil {
		w.t.Fatal(err)
	}
	if err := w.ctl.Store.Move(ctx, w.db, ch, StateReady, ActionPass, nil, nil, map[string]any{"ready_at": w.now}); err != nil {
		w.t.Fatal(err)
	}
	return w.change(id)
}

func (w *auWorld) ready(id string) {
	w.t.Helper()
	cfg, err := LoadConfig(context.Background(), w.db)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.ctl.ready(context.Background(), cfg, w.change(id)); err != nil {
		w.t.Fatal(err)
	}
}

// TC1: the update's change is made for the live TV with the new binary, staged at once,
// and promoted without an approval once verified (swap at boundaries).
func TestAutoUpdateBinaryChangeAutoPromotes(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			st := w.begin("run-1")
			if st.Outcome != AutoOutcomeQueued || st.ChangeID == "" {
				t.Fatalf("begun: %+v", st)
			}
			ch := w.change(st.ChangeID)
			if ch.SourceKind() != SourceAutoUpdate || ch.Source["run_id"] != "run-1" || ch.SwapWait == nil || *ch.SwapWait != "boundary" {
				t.Fatalf("change: source %v swap_wait %v", ch.Source, ch.SwapWait)
			}
			got := ch.Pending().M["streaming"]
			if s, _ := got.([]any); len(s) != 2 || s[0] != "v1.5.0" || s[1] != sumNew {
				t.Fatalf("pending streaming %v", got)
			}
			if strings.Join(w.enqueued, ",") != StageTVStage {
				t.Fatalf("staged at once: %v", w.enqueued)
			}
			w.toReady(st.ChangeID, []string{"binary"})
			w.ready(st.ChangeID)
			if ch := w.change(st.ChangeID); ch.State != StateSwapping {
				t.Fatalf("not promoted: %s", ch.State)
			}
			if st, _ := w.ctl.AutoUpdate(context.Background(), "run-1"); st.Outcome != AutoOutcomeInProgress {
				t.Fatalf("swapping: %+v", st)
			}
			ch = w.change(st.ChangeID)
			if err := w.ctl.Store.Move(context.Background(), be.DB, ch, StateLive, ActionSwap, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if st, _ := w.ctl.AutoUpdate(context.Background(), "run-1"); st.Outcome != AutoOutcomeLive {
				t.Fatalf("live: %+v", st)
			}
		})
	}
}

// TC2: a binary change made by hand waits for an approval; the update's change whose
// kinds came out mixed does too.
func TestManualBinaryChangeNeedsApproval(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			c := &Change{State: StateDraft, Title: "by hand", Source: map[string]any{"kind": SourceManual},
				Diff: map[string]any{"pending": map[string]any{"streaming": []string{"v1.5.0", sumNew}}}}
			if err := w.ctl.Store.Insert(context.Background(), be.DB, c); err != nil {
				t.Fatal(err)
			}
			w.toReady(c.ID, []string{"binary"})
			w.ready(c.ID)
			if ch := w.change(c.ID); ch.State != StateReady {
				t.Fatalf("a manual binary change went on: %s", ch.State)
			}
		})
	}
}

// The update's change whose kinds came out mixed is still automatic: it follows its
// kinds' strictest auto rule (lineup at quiet_window: it waits for the window), never an
// approval.
func TestMixedKindsFollowTheStrictestAutoRule(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			if _, err := be.DB.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging',
				'{"auto_promote_lineup": "quiet_window", "quiet_window": "00:00-00:01"}')`); err != nil {
				t.Fatal(err)
			}
			st := w.begin("run-1")
			w.toReady(st.ChangeID, []string{"lineup", "binary"})
			cfg, err := LoadConfig(context.Background(), be.DB)
			if err != nil {
				t.Fatal(err)
			}
			if got := ChangeRule(cfg, w.change(st.ChangeID)); got != RuleQuietWindow {
				t.Fatalf("rule %s", got)
			}
			if ch := w.change(st.ChangeID); ch.State != StateReady || ch.Origin != OriginAuto {
				t.Fatalf("a mixed change: %s %s", ch.State, ch.Origin)
			}
		})
	}
}

// AC4: behind a change that waits for an approval the update's change stays a draft
// (not staged), and says what it waits for; EC1: one change per run however often asked.
func TestAutoUpdateChangeQueuesBehindAReadyChange(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			other := &Change{State: StateDraft, Title: "lineup", Source: map[string]any{"kind": SourceEditor},
				Diff: map[string]any{"pending": map[string]any{}}}
			if err := w.ctl.Store.Insert(context.Background(), be.DB, other); err != nil {
				t.Fatal(err)
			}
			w.toReady(other.ID, []string{"lineup"})
			st := w.begin("run-1")
			if len(w.enqueued) != 0 {
				t.Fatalf("staged while another is in flight: %v", w.enqueued)
			}
			if st.Outcome != AutoOutcomeQueued || st.QueuedBehind == nil || st.QueuedBehind.ID != other.ID ||
				st.QueuedBehind.State != StateReady || st.QueuedBehind.Approved {
				t.Fatalf("queued: %+v (%+v)", st, st.QueuedBehind)
			}
			again := w.begin("run-1")
			if again.ChangeID != st.ChangeID {
				t.Fatalf("a second change for the run: %s, then %s", st.ChangeID, again.ChangeID)
			}
			var n int
			if err := be.DB.QueryRow(`SELECT count(*) FROM tv_changes`).Scan(&n); err != nil || n != 2 {
				t.Fatalf("%d changes (%v)", n, err)
			}
		})
	}
}

// A person's save while the update's change is open opens a change of its own: the
// update's carries the binary alone.
func TestSaveDoesNotJoinTheUpdatesChange(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			st := w.begin("run-1")
			d := NewDrafts(be.DB)
			d.Store.Now = w.ctl.Now
			ref, err := d.Save(context.Background(), admin1, SourceEditor, "Channel editor", "channel", func(p *Pending) error {
				p.SetChannel("c1", map[string]any{"id": "c1", "number": float64(101)})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if ref.ID == st.ChangeID {
				t.Fatal("the save joined the update's change")
			}
			if p := w.change(st.ChangeID).Pending().M; len(p) != 1 || p["streaming"] == nil {
				t.Fatalf("the update's change: %v", p)
			}
		})
	}
}

// Nothing to swap: staging off (the restart's fallback put the binary on), no TV release
// yet, or the live release runs that binary.
func TestAutoUpdateSkipped(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAUWorld(t, be.DB)
			w.exec(`UPDATE tv_releases SET streaming_version = 'v1.5.0', streaming_sha256 = $1`, sumNew)
			if st := w.begin("run-1"); st.Outcome != AutoOutcomeSkipped || st.Skipped != SkipAlreadyLive {
				t.Fatalf("already live: %+v", st)
			}
			w.exec(`DELETE FROM tv_routing`)
			if st := w.begin("run-2"); st.Skipped != SkipNoTV {
				t.Fatalf("no TV: %+v", st)
			}
			w.exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging', '{"enabled": false}')`)
			if st := w.begin("run-3"); st.Skipped != SkipStagingOff {
				t.Fatalf("staging off: %+v", st)
			}
			if _, err := w.ctl.BeginAutoUpdate(ctx, "run-4", "../etc", sumNew); err == nil {
				t.Fatal("a bad version was taken")
			}
		})
	}
}

// held walks the run's change to a failed check: back to draft with its report.
func (w *auWorld) held(run string) AutoUpdateStatus {
	w.t.Helper()
	ctx := context.Background()
	st := w.begin(run)
	ch := w.toReady(st.ChangeID, []string{"binary"})
	if err := w.ctl.Store.Set(ctx, w.db, ch.ID, map[string]any{"verify_report": map[string]any{
		"checks": []any{map[string]any{"check": "stream_plays", "channel_id": "c1", "result": ResultFail, "value": "no video"},
			map[string]any{"check": "guide", "result": ResultPass}}}}); err != nil {
		w.t.Fatal(err)
	}
	ch = w.change(ch.ID)
	if err := w.ctl.Store.Move(ctx, w.db, ch, StateVerifying, ActionRecheckFail, nil, nil, nil); err != nil {
		w.t.Fatal(err)
	}
	if err := w.ctl.failToDraft(ctx, ch, nil, "checks failed"); err != nil {
		w.t.Fatal(err)
	}
	got, err := w.ctl.AutoUpdate(ctx, run)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

// The owner's decision (2026-10-28): a check that fails before any swap holds the change
// (a draft with its report) and leaves the live TV where it is: the run reads held, with
// the checks that failed and the streaming software the TV stays on.
func TestAutoUpdateCheckFailedIsHeld(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			got := w.held("run-1")
			if got.Outcome != AutoOutcomeHeld || got.Reason != "verify_failed" || got.State != StateDraft ||
				len(got.Failed) != 1 || got.Failed[0] != "stream_plays" || got.LiveStreaming != "v1.4.0" {
				t.Fatalf("held: %+v", got)
			}
			if len(got.FailedChecks) != 1 || got.FailedChecks[0].ChannelID != "c1" || got.FailedChecks[0].Value != "no video" {
				t.Fatalf("report summary %+v", got.FailedChecks)
			}
			// the live TV did not move
			var live string
			if err := be.DB.QueryRow(`SELECT live_release_id FROM tv_routing WHERE scope = 'cluster'`).Scan(&live); err != nil || live != w.live {
				t.Fatalf("live release %q (%v)", live, err)
			}
		})
	}
}

// The update to 0.3.39 (2026-10-28) said "the TV stays on 0.0.1": the live release was
// built from a source checkout and recorded the pipeline package's 0.0.1, while the live
// slot ran a stamped binary. The held run names what the live slot reports it runs; the
// live release's word stands only when no live slot beats (SQLite: no slots).
func TestAutoUpdateHeldNamesTheLiveSlotsSoftware(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newAUWorld(t, be.DB)
			w.exec(`UPDATE tv_releases SET streaming_version = '0.0.1' WHERE id = $1`, w.live)
			if be.Name == "sqlite" {
				if got := w.held("run-1"); got.Outcome != AutoOutcomeHeld || got.LiveStreaming != "0.0.1" {
					t.Fatalf("no slots: %+v", got)
				}
				return
			}
			// a live slot whose beat stopped (the app down) is not taken at its word
			w.exec(`INSERT INTO tv_slots (host_id, slot, role, state, version, beat_at)
				VALUES ('h1', 'green', 'live', 'healthy', 'v0.3.36', now() - interval '10 minutes')`)
			w.exec(`INSERT INTO tv_slots (host_id, slot, role, state, version, beat_at)
				VALUES ('h1', 'blue', 'standby', 'healthy', 'v0.3.39', now())`)
			got := w.held("run-1")
			if got.Outcome != AutoOutcomeHeld || got.LiveStreaming != "0.0.1" {
				t.Fatalf("a stale live slot: %+v", got)
			}
			// the live slot beats: its binary's version (the standby's is not the TV's)
			w.exec(`UPDATE tv_slots SET version = 'v0.3.37', beat_at = now() WHERE slot = 'green'`)
			if got, err := w.ctl.AutoUpdate(context.Background(), "run-1"); err != nil ||
				got.Outcome != AutoOutcomeHeld || got.LiveStreaming != "v0.3.37" {
				t.Fatalf("the live slot's software: %+v (%v)", got, err)
			}
		})
	}
}

// Retry checks the held change again: it is asked to be staged (staged at once when
// nothing is in flight) and reads in progress until its check ends; a pass swaps under its
// rule (immediate). Asked again once it is no longer held: ErrNotHeld.
func TestRetryAutoUpdateStagesTheHeldChangeAgain(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAUWorld(t, be.DB)
			st := w.held("run-1")
			w.enqueued = nil
			admin := "admin-1"
			got, err := w.ctl.RetryAutoUpdate(ctx, "run-1", &admin)
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != AutoOutcomeQueued || strings.Join(w.enqueued, ",") != StageTVStage {
				t.Fatalf("retried: %+v enqueued %v", got, w.enqueued)
			}
			if _, err := w.ctl.RetryAutoUpdate(ctx, "run-1", &admin); err != ErrNotHeld {
				t.Fatalf("retried twice: %v", err)
			}
			if _, err := w.ctl.DropAutoUpdate(ctx, "run-1", &admin); err != ErrNotHeld {
				t.Fatalf("dropped while checked again: %v", err)
			}
			// its check passes this time: ready, then swapped without an approval
			ch := w.toReady(st.ChangeID, []string{"binary"})
			w.ready(ch.ID)
			if got := w.change(ch.ID); got.State != StateSwapping {
				t.Fatalf("a retry that passed is %s, want swapping", got.State)
			}
			if got, _ := w.ctl.AutoUpdate(ctx, "run-1"); got.Outcome != AutoOutcomeInProgress {
				t.Fatalf("swapping reads %+v", got)
			}
			if _, err := w.ctl.RetryAutoUpdate(ctx, "run-none", &admin); err != ErrNotHeld {
				t.Fatalf("a run without a change: %v", err)
			}
		})
	}
}

// Retry while another change is in flight: the request is kept, and the controller stages
// it when its turn comes.
func TestRetryAutoUpdateWaitsForTheChangeInFlight(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAUWorld(t, be.DB)
			w.held("run-1")
			other := &Change{State: StateDraft, Title: "lineup", Source: map[string]any{"kind": SourceManual}, Diff: map[string]any{}}
			if err := w.ctl.Store.Insert(ctx, be.DB, other); err != nil {
				t.Fatal(err)
			}
			w.toReady(other.ID, []string{"lineup"})
			w.enqueued = nil
			got, err := w.ctl.RetryAutoUpdate(ctx, "run-1", nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != AutoOutcomeQueued || len(w.enqueued) != 0 || got.QueuedBehind == nil || got.QueuedBehind.ID != other.ID {
				t.Fatalf("retried behind another: %+v enqueued %v", got, w.enqueued)
			}
		})
	}
}

// Drop withdraws the held change: cancelled (end_reason dropped), the TV left as it is.
func TestDropAutoUpdateCancelsTheHeldChange(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAUWorld(t, be.DB)
			st := w.held("run-1")
			admin := "admin-1"
			got, err := w.ctl.DropAutoUpdate(ctx, "run-1", &admin)
			if err != nil {
				t.Fatal(err)
			}
			ch := w.change(st.ChangeID)
			if got.Outcome != AutoOutcomeCancelled || got.EndReason != EndDropped || ch.State != StateCancelled {
				t.Fatalf("dropped: %+v (%s)", got, ch.State)
			}
			if _, err := w.ctl.DropAutoUpdate(ctx, "run-1", &admin); err != ErrNotHeld {
				t.Fatalf("dropped twice: %v", err)
			}
			if _, err := w.ctl.RetryAutoUpdate(ctx, "run-1", &admin); err != ErrNotHeld {
				t.Fatalf("a dropped change retried: %v", err)
			}
			var live string
			if err := be.DB.QueryRow(`SELECT live_release_id FROM tv_routing WHERE scope = 'cluster'`).Scan(&live); err != nil || live != w.live {
				t.Fatalf("live release %q (%v)", live, err)
			}
		})
	}
}

// A later release that commits replaces an older run's held change (a Retry of it would
// air older software); its own change is left alone.
func TestReplaceOlderAutoUpdates(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAUWorld(t, be.DB)
			old := w.held("run-1")
			st2, err := w.ctl.BeginAutoUpdate(ctx, "run-2", "v1.6.0", strings.Repeat("2", 64))
			if err != nil {
				t.Fatal(err)
			}
			if w.change(old.ChangeID).State != StateDraft {
				t.Fatal("the held change went before the later release committed")
			}
			if err := w.ctl.ReplaceOlderAutoUpdates(ctx, "run-2"); err != nil {
				t.Fatal(err)
			}
			got, _ := w.ctl.AutoUpdate(ctx, "run-1")
			if got.Outcome != AutoOutcomeCancelled || got.EndReason != EndReplaced {
				t.Fatalf("older: %+v", got)
			}
			if ch := w.change(st2.ChangeID); Terminal(ch.State) {
				t.Fatalf("the committing run's own change is %s", ch.State)
			}
		})
	}
}

// AC3 inputs: a failed swap is a failure (the run rolls back); the run's withdraw cancels
// a change not on the air yet, a held one too (a release that rolls back for another
// reason after its TV was held).
func TestAutoUpdateFailuresAndWithdraw(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAUWorld(t, be.DB)
			st := w.begin("run-1")
			ch := w.toReady(st.ChangeID, []string{"binary"})
			if err := w.ctl.Store.Set(ctx, be.DB, ch.ID, map[string]any{"verify_report": map[string]any{
				"checks": []any{map[string]any{"check": "stream_plays", "result": ResultFail}}}}); err != nil {
				t.Fatal(err)
			}
			ch = w.change(ch.ID)
			if err := w.ctl.Store.Move(ctx, be.DB, ch, StateVerifying, ActionRecheckFail, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := w.ctl.failToDraft(ctx, ch, nil, "checks failed"); err != nil {
				t.Fatal(err)
			}
			got, _ := w.ctl.AutoUpdate(ctx, "run-1")
			if got.Outcome != AutoOutcomeHeld || got.Reason != "verify_failed" || len(got.Failed) != 1 || got.Failed[0] != "stream_plays" {
				t.Fatalf("failed verification: %+v", got)
			}
			got, err := w.ctl.WithdrawAutoUpdate(ctx, "run-1", CodeTVFailedForTest)
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != AutoOutcomeCancelled || got.Withdrawn != CodeTVFailedForTest || w.change(st.ChangeID).State != StateCancelled {
				t.Fatalf("withdrawn: %+v", got)
			}

			st2 := w.begin("run-2")
			ch2 := w.toReady(st2.ChangeID, []string{"binary"})
			if err := w.ctl.Store.Event(ctx, be.DB, ch2.ID, Event{ActorSource: "system", Action: ActionSwapFailed}); err != nil {
				t.Fatal(err)
			}
			if got, _ := w.ctl.AutoUpdate(ctx, "run-2"); got.Outcome != AutoOutcomeFailed || got.Reason != "swap_failed" {
				t.Fatalf("failed swap: %+v", got)
			}
		})
	}
}

// CodeTVFailedForTest is a withdraw reason.
const CodeTVFailedForTest = "failed_tv"

// AC8: a run that rolls back after its change went on the air rolls the TV back (Epic
// 33.9: warm while the previous release's slot is warm), once.
func TestWithdrawRollsBackALiveChange(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAUWorld(t, be.DB)
			st := w.begin("run-1")
			ch := w.toReady(st.ChangeID, []string{"binary"})
			rel := uuid.NewString()
			w.exec(`INSERT INTO tv_releases (id, digest, sched_gen, streaming_version, streaming_sha256) VALUES ($1, 'sha256:new', 1, 'v1.5.0', $2)`,
				rel, sumNew)
			w.exec(`UPDATE tv_changes SET release_id = $1 WHERE id = $2`, rel, ch.ID)
			w.exec(`UPDATE tv_routing SET live_release_id = $1, previous_release_id = $2, previous_until = $3, switched_at = $4`,
				rel, w.live, tsParam(be.DB, w.now.Add(15*time.Minute)), tsParam(be.DB, w.now))
			if err := w.ctl.Store.Move(ctx, be.DB, ch, StateSwapping, ActionAutoPromote, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			// withdrawn while it swaps: rolled back once it is live (the controller's tick)
			if _, err := w.ctl.WithdrawAutoUpdate(ctx, "run-1", "watch_timeout"); err != nil {
				t.Fatal(err)
			}
			w.enqueued = nil
			ch = w.change(ch.ID)
			if err := w.ctl.Store.Move(ctx, be.DB, ch, StateLive, ActionSwap, nil, nil, map[string]any{"swapped_at": w.now}); err != nil {
				t.Fatal(err)
			}
			if err := w.ctl.tickWithdrawn(ctx); err != nil {
				t.Fatal(err)
			}
			if strings.Join(w.enqueued, ",") != StageTVRollback {
				t.Fatalf("enqueued %v, want the rollback", w.enqueued)
			}
			if err := w.ctl.tickWithdrawn(ctx); err != nil || len(w.enqueued) != 1 {
				t.Fatalf("asked again: %v (%v)", w.enqueued, err)
			}
		})
	}
}
