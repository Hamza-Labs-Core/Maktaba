package tvchange

import (
	"context"
	"time"
)

// TV changes during an app update (the owner's rule of 2026-10-28: "tv changes should not
// block updates"; Epic 35 story 35.3 AC5 as amended). An update always starts: no TV
// change holds it. From the moment the home is drained for its restart until the update
// ends (its run in draining or home_applying: UpdateHolding), the change controller
// handles the change in flight like this:
//
//   - draft: left as it is; not staged while the update runs (the update's own binary
//     change, source auto_update, is the only change staged then).
//   - staged or verifying: set aside (ActionSetAside, back to draft with its stage asked
//     again; the staging slot is released by reconcileStaging). It is not failed and
//     not lost: once the update ends it is staged and verified again, on the new release.
//   - ready (waiting for an approval or a quiet window): left as it is, neither checked
//     again nor put on the air while the update runs; set aside only when the update's
//     binary change needs the staging slot (the binary change gets it first). Once the
//     update ends it is checked again on the new release (ActionRecheckAfterUpdate,
//     ready -> draft, staged again) and comes back to ready: an automatic change then
//     goes on the air by its rule, a manual one waits for its approval.
//   - approved or swapping: goes on (a swap takes seconds); the update's restart step
//     waits for a swap in progress, at most updates.TVSwapWait, then goes on (a swap cut
//     by the restart follows the swap-failure rollback).
//
// diff.update_hold records it: {run_id, from, at[, released_at]}.

// Actions of the update hold.
const (
	// ActionSetAside: an update set the change aside (staged, verifying or ready ->
	// draft, its stage asked again for after the update).
	ActionSetAside = "set_aside"
	// ActionRecheckAfterUpdate: the update ended; a change that waited in ready is
	// checked again on the new release (ready -> draft, staged again).
	ActionRecheckAfterUpdate = "recheck_after_update"
)

// HoldKey is the diff key that records an update's hold on a change.
const HoldKey = "update_hold"

// SetAsideWhy is the set-aside event's reason.
const SetAsideWhy = "set aside for an app update: checked again on the new release after it"

// holdingStates are the update_runs states in which the update holds the TV: the home is
// drained for its restart, restarting, or watching the new release (its TV step
// included) - until it commits or ends.
const holdingStates = `'draining', 'home_applying'`

// UpdateHolding is the id of the app update that holds the TV now ("" when none). A
// database without update_runs is never held.
func (c *Controller) UpdateHolding(ctx context.Context) string {
	if c.UpdateRun != nil {
		return c.UpdateRun(ctx)
	}
	var id string
	if err := c.DB.QueryRowContext(ctx, `SELECT id FROM update_runs WHERE finished_at IS NULL
		AND state IN (`+holdingStates+`) ORDER BY started_at DESC LIMIT 1`).Scan(&id); err != nil {
		return ""
	}
	return id
}

// isAutoUpdate reports the automatic updater's own binary change: an update never holds it.
func isAutoUpdate(ch *Change) bool { return ch.SourceKind() == SourceAutoUpdate }

func holdOf(ch *Change) map[string]any {
	h, _ := ch.Diff[HoldKey].(map[string]any)
	return h
}

// tickUpdateHold applies the update hold to the open changes (oldest last, as the tick
// lists them) and returns the holding run ("" when none). Moves update the changes in
// place, so the tick goes on with their new states.
func (c *Controller) tickUpdateHold(ctx context.Context, open []*Change) string {
	run := c.UpdateHolding(ctx)
	if run == "" {
		c.releaseHold(ctx, open)
		return ""
	}
	binaryWaits := false
	for _, ch := range open {
		if isAutoUpdate(ch) && ch.State == StateDraft && stageRequested(ch) {
			binaryWaits = true
		}
	}
	for i := len(open) - 1; i >= 0; i-- {
		ch := open[i]
		if isAutoUpdate(ch) {
			continue
		}
		switch ch.State {
		case StateStaged, StateVerifying:
			c.setAside(ctx, ch, run)
		case StateReady:
			if len(ch.ApprovedBy) > 0 {
				continue // approved ahead: it goes on by itself (approved, then swapped)
			}
			if binaryWaits {
				c.setAside(ctx, ch, run) // the update's binary change gets the staging slot first
				continue
			}
			if h := holdOf(ch); h == nil || h["run_id"] != run {
				diff := copyMap(ch.Diff)
				diff[HoldKey] = map[string]any{"run_id": run, "from": StateReady, "at": c.now().Format(time.RFC3339)}
				if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"diff": diff}); err == nil {
					ch.Diff = diff
				}
			}
		}
	}
	return run
}

// setAside sends a change the update holds back to draft, its stage asked again for
// after the update.
func (c *Controller) setAside(ctx context.Context, ch *Change, run string) {
	from := ch.State
	diff := copyMap(ch.Diff)
	diff["stage"] = map[string]any{"requested_at": c.now().Format(time.RFC3339)}
	diff[HoldKey] = map[string]any{"run_id": run, "from": from, "at": c.now().Format(time.RFC3339)}
	if err := c.Store.Move(ctx, c.DB, ch, StateDraft, ActionSetAside, nil,
		map[string]any{"why": SetAsideWhy, "run_id": run, "from": from, "report": nonNilMap(ch.VerifyReport)},
		map[string]any{"diff": diff}); err != nil {
		if !moved(err) {
			c.Logger.Warn("tv change set aside failed", "change_id", ch.ID, "error", err.Error())
		}
		return
	}
	ch.Diff = diff
	c.Logger.Info("tv change set aside for an app update", "event", "tv_change_set_aside",
		"change_id", ch.ID, "from", from, "run_id", run)
}

// releaseHold ends the hold once the update ended: a change that waited in ready is
// checked again on the new release; a change set aside is staged again (its stage is
// asked; the tick stages it when its turn comes).
func (c *Controller) releaseHold(ctx context.Context, open []*Change) {
	for _, ch := range open {
		h := holdOf(ch)
		if h == nil || h["released_at"] != nil || isAutoUpdate(ch) {
			continue
		}
		diff := copyMap(ch.Diff)
		nh := copyMap(h)
		nh["released_at"] = c.now().Format(time.RFC3339)
		diff[HoldKey] = nh
		run, _ := h["run_id"].(string)
		var err error
		switch {
		case ch.State == StateReady && len(ch.ApprovedBy) == 0:
			diff["stage"] = map[string]any{"requested_at": c.now().Format(time.RFC3339)}
			err = c.Store.Move(ctx, c.DB, ch, StateDraft, ActionRecheckAfterUpdate, nil,
				map[string]any{"why": "the app update ended: checked again on the new release", "run_id": run,
					"report": nonNilMap(ch.VerifyReport)},
				map[string]any{"diff": diff})
		default:
			err = c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"diff": diff})
		}
		if err != nil {
			if !moved(err) {
				c.Logger.Warn("tv change hold release failed", "change_id", ch.ID, "error", err.Error())
			}
			continue
		}
		ch.Diff = diff
		c.Logger.Info("tv change back after an app update", "event", "tv_change_hold_released",
			"change_id", ch.ID, "state", ch.State, "run_id", run)
	}
}

// heldByUpdate reports a change the update holds now: it is not staged, checked again or
// put on the air by a rule until the update ends.
func heldByUpdate(run string, ch *Change) bool {
	return run != "" && !isAutoUpdate(ch)
}

// afterUpdateRecheck reports a change checked again because an app update held it: its
// ready notice was (or would have been) sent before, so it is not sent again.
func afterUpdateRecheck(ch *Change) bool {
	return holdOf(ch) != nil
}
