package tvchange

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The open change (story 33.12 AC3, the owner's rule): "when an update happens it
// needs to start a change; anything after that is added to the same change, unless
// it's approved".
//
// The open change is the change no one approved in draft, staged, verifying or ready.
// Every update to the TV joins it, whatever its producer (merge.go). Joined after it
// was checked (staged … ready), it goes back to draft with a reopen event ("updated
// after it was checked: it is checked again"), its verify report kept in that event as
// history; when the write mode stages by itself it is staged once more (one stage per
// update: diff.stage asked once, as the stage job and a failed check clear it). Only a
// change approved (approved_by set), swapping or live takes no more updates: the next
// one opens a new change. The pipeline's tvrelease/store.py open_change and reopen,
// and tvrelease/stage.py collapse_open, are the same.

// OpenStates are the states the open change can be in.
var OpenStates = []string{StateDraft, StateStaged, StateVerifying, StateReady}

// ReopenWhy is the reopen event's reason.
const ReopenWhy = "updated after it was checked: it is checked again"

// ActionReopen: an update joined a checked change (staged … ready): back to draft.
const ActionReopen = "reopen"

// OpenChanges are the changes no one approved in draft … ready, oldest first. There is
// one at most; a second (opened before the rule, or while the first was in flight)
// joins the first (CollapseOpen). The automatic updater's change of the streaming binary
// is none of them (story 35.6): its own change, which no save joins and which joins no
// one's, queued behind the change in flight like any newer draft.
func (s *Store) OpenChanges(ctx context.Context, q queryer) ([]*Change, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+changeCols+` FROM tv_changes
		WHERE state IN ('draft','staged','verifying','ready') ORDER BY number, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Change{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		if len(c.ApprovedBy) == 0 && c.SourceKind() != SourceAutoUpdate {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// OpenChange is the open change, or nil: the oldest change no one approved in draft …
// ready. Every update joins it (Drafts.Save reopens it when it was checked).
func (s *Store) OpenChange(ctx context.Context, q queryer) (*Change, error) {
	got, err := s.OpenChanges(ctx, q)
	if err != nil || len(got) == 0 {
		return nil, err
	}
	return got[0], nil
}

// countOpen counts the changes no one approved in draft … ready without reading their
// diffs (a channel plan's is tens of MB): the controller's tick looks every few
// seconds for a second one.
func (s *Store) countOpen(ctx context.Context, q queryer) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT approved_by, source FROM tv_changes
		WHERE state IN ('draft','staged','verifying','ready')`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var appr, src []byte
		if err := rows.Scan(&appr, &src); err != nil {
			return 0, err
		}
		by := []string{}
		decodeJSON(appr, &by)
		ch := Change{}
		decodeJSON(src, &ch.Source)
		if len(by) == 0 && ch.SourceKind() != SourceAutoUpdate {
			n++
		}
	}
	return n, rows.Err()
}

// AutoRestage reports whether the write mode stages a change by itself (write_channels
// in auto, the default when the stage has no row): a change an update reopened is
// then staged again. A database without job_stages stages nothing by itself.
func (s *Store) AutoRestage(ctx context.Context, q queryer) bool {
	var mode string
	err := q.QueryRowContext(ctx, `SELECT write_mode FROM job_stages WHERE stage = 'write_channels'`).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	return err == nil && mode == "auto"
}

// Reopen sends a checked change (staged … ready) an update joined back to draft: a
// reopen event (the reason, the state it left, its verify report as history), its
// approval withdrawn (it had none: approved, it would not be open), its stage request
// cleared and, when the write mode stages by itself, asked once more. Its release is
// built anew when it is staged again (the pipeline's stage_change). A draft is left as
// it is. Callers hold the drafts' lock.
func (s *Store) Reopen(ctx context.Context, q queryer, ch *Change, actor *string, detail map[string]any) error {
	if ch.State == StateDraft {
		return nil
	}
	diff := copyMap(ch.Diff)
	delete(diff, "stage")
	if s.AutoRestage(ctx, q) {
		diff["stage"] = map[string]any{"requested_at": s.now().Format(time.RFC3339)}
	}
	d := map[string]any{"why": ReopenWhy, "from": ch.State, "report": nonNilMap(ch.VerifyReport)}
	for k, v := range detail {
		d[k] = v
	}
	if err := s.Move(ctx, q, ch, StateDraft, ActionReopen, actor, d, map[string]any{"diff": diff}); err != nil {
		return err
	}
	ch.Diff = diff
	return nil
}

// CollapseOpen is the open change with any other change no one approved joined into it
// (nil when none): the oldest is the open change (sent back to draft when it was
// checked); each newer one joins it, oldest first - its pending edits merged
// (MergePending), its plan's digest, source and change set taken (its approval is the
// queue's Apply of that digest) - and ends superseded (end_reason joined_change) with
// an append event on the open change naming it. Callers hold the drafts' lock, in a
// transaction. Idempotent: with one open change it only returns it.
func (s *Store) CollapseOpen(ctx context.Context, q queryer, actorSource string) (*Change, error) {
	open, err := s.OpenChanges(ctx, q)
	if err != nil || len(open) == 0 {
		return nil, err
	}
	ch := open[0]
	if len(open) == 1 {
		return ch, nil
	}
	newer := open[1:]
	ids := make([]string, len(newer))
	for i, n := range newer {
		ids[i] = n.ID
	}
	if err := s.Reopen(ctx, q, ch, nil, map[string]any{"joined": ids}); err != nil {
		return nil, err
	}
	if err := s.absorb(ctx, q, ch, newer, actorSource); err != nil {
		return nil, err
	}
	return ch, nil
}

// absorb joins newer into ch (a draft), oldest first.
func (s *Store) absorb(ctx context.Context, q queryer, ch *Change, newer []*Change, actorSource string) error {
	if len(newer) == 0 {
		return nil
	}
	live, err := s.LiveDefs(ctx, q)
	if err != nil {
		return err
	}
	if actorSource == "" {
		actorSource = "controller"
	}
	pending := WithPlanLayer(ch.Pending().M, ch.Digest)
	fields := map[string]any{}
	for _, n := range newer {
		pending = MergePending(pending, WithPlanLayer(n.Pending().M, n.Digest), live)
		if n.Digest != nil {
			// the newer plan's: its approval is the queue's Apply of that digest
			fields["digest"], fields["source"] = *n.Digest, n.Source
			if len(n.ChangeSet) > 0 {
				fields["change_set"] = string(n.ChangeSet)
			}
			d := *n.Digest
			ch.Digest = &d
		}
		at := s.now()
		if err := s.Move(ctx, q, n, StateSuperseded, ActionSupersede, nil,
			map[string]any{"joined": ch.ID, "number": ch.Number},
			map[string]any{"superseded_by": ch.ID, "ended_at": at, "end_reason": "joined_change"}); err != nil {
			return err
		}
		st := StateDraft
		if err := s.Event(ctx, q, ch.ID, Event{ActorSource: actorSource, FromState: &st, ToState: &st,
			Action: "append", Detail: map[string]any{"what": "joined_change", "kind": n.SourceKind(),
				"change_id": n.ID, "number": n.Number}}); err != nil {
			return err
		}
	}
	pd := &Pending{M: pending}
	diff := copyMap(ch.Diff)
	diff["pending"] = pending
	diff["summary"] = Summarize(pd)
	fields["diff"] = diff
	ch.Diff = diff
	return s.Set(ctx, q, ch.ID, fields)
}

// collapseOpen brings a second open change into line (the controller's tick): one
// left over from before the rule (live: #46 ready and #47 draft), or one opened while
// the first was in flight. Cheap when there is one: no diff is read.
func (c *Controller) collapseOpen(ctx context.Context) error {
	n, err := c.Store.countOpen(ctx, c.DB)
	if err != nil || n < 2 {
		return err
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if !IsSQLite(c.DB) {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey()); err != nil {
			return err
		}
	}
	ch, err := c.Store.CollapseOpen(ctx, tx, "controller")
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if ch != nil {
		c.Logger.Info("tv changes joined into the open change", "event", "tv_change_collapsed",
			"change_id", ch.ID, "number", ch.Number)
	}
	return nil
}
