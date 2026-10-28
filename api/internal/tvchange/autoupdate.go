package tvchange

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The automatic updater's TV change (Epic 35 story 35.6, design §7.1): once the home's new
// release runs, its supervisor asks for a change of the streaming binary alone,
// source {kind: auto_update, run_id, version}, release = the live one with the new
// streaming {version, sha256}. ChangeRule promotes it without an approval (the owner's
// override of Epic 33 decision 2), at programme boundaries; it is staged, verified and
// swapped like any change, queued behind the change in flight (README R9.4), and never
// joined to anyone's open change (OpenChanges). One change per run: asking again returns
// it (a supervisor that restarted mid-swap waits for the same one, EC1).

// Outcomes of an automatic update's TV change, as its run reads them.
const (
	AutoOutcomeNone       = "none"        // no change for the run
	AutoOutcomeSkipped    = "skipped"     // nothing to swap (Skipped says why)
	AutoOutcomeQueued     = "queued"      // a draft behind the change in flight
	AutoOutcomeInProgress = "in_progress" // staged … swapping
	AutoOutcomeLive       = "live"        // on the air (or it aired what was live already)
	AutoOutcomeHeld       = "held"        // its check failed before any swap: a draft with its report
	AutoOutcomeFailed     = "failed"      // its swap failed, or it was rolled back after it switched
	AutoOutcomeCancelled  = "cancelled"   // an admin cancelled (or dropped) it, or its run withdrew it
)

// End reasons of an automatic update's held change: an admin dropped it (DropAutoUpdate),
// or a later update's change replaced it (BeginAutoUpdate).
const (
	EndDropped  = "dropped"
	EndReplaced = "replaced"
)

// ErrNotHeld: the run's change is not held (its check did not fail, or it was retried,
// dropped or replaced since).
var ErrNotHeld = errors.New("not_held")

// Reasons an automatic update's change is skipped.
const (
	SkipStagingOff  = "staging_off"  // rules.tv_staging.enabled is off: the restart put the binary on
	SkipAlreadyLive = "already_live" // the live TV release runs that streaming binary already
	SkipNoTV        = "no_tv"        // no TV release yet (staging never used): nothing airs a release
)

// AutoUpdateStatus is a run's TV change as the supervisor follows it.
type AutoUpdateStatus struct {
	RunID     string     `json:"run_id"`
	Outcome   string     `json:"outcome"`
	Skipped   string     `json:"skipped,omitempty"`
	ChangeID  string     `json:"change_id,omitempty"`
	Number    int64      `json:"number,omitempty"`
	State     string     `json:"state,omitempty"`
	EndReason string     `json:"end_reason,omitempty"`
	Kinds     []string   `json:"kinds,omitempty"`
	StagedAt  *time.Time `json:"staged_at,omitempty"`
	// Reason is why it failed or is held (verify_failed, swap_failed, rolled_back).
	Reason string `json:"reason,omitempty"`
	// Failed is the checks that failed (its verify report's), for the admin's notice;
	// FailedChecks the same checks with their channel and value (the report's summary).
	Failed       []string `json:"failed,omitempty"`
	FailedChecks []Check  `json:"failed_checks,omitempty"`
	// LiveStreaming is the streaming software the live TV runs now: the version the
	// live slot reports (the binary's own), else the live TV release's ("" when none
	// airs). A held change leaves the TV on it.
	LiveStreaming string `json:"live_streaming,omitempty"`
	// QueuedBehind is the change in flight it waits for (a ready one waits for an
	// approval the run cannot get: the run gives up at its window's end).
	QueuedBehind *QueuedRef `json:"queued_behind,omitempty"`
	// TimeoutMinutes is how long the run waits once the change is staged:
	// boundary_max_wait_minutes + 10 (story 35.6 AC3).
	TimeoutMinutes int `json:"timeout_minutes,omitempty"`
	// Withdrawn is why its run took it back ("" when it did not).
	Withdrawn string `json:"withdrawn,omitempty"`
}

// QueuedRef names the change a queued one waits for.
type QueuedRef struct {
	ID       string `json:"id"`
	Number   int64  `json:"number"`
	State    string `json:"state"`
	Approved bool   `json:"approved"`
}

// autoVersionRe is a release version as the change records it (no path, no spaces).
var autoVersionRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+([.+-][0-9A-Za-z.+-]*)?$`)

// autoSHARe is a lowercase SHA-256.
var autoSHARe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// autoChangeID is the run's change ("" when none).
func (c *Controller) autoChangeID(ctx context.Context, q queryer, runID string) (string, error) {
	query := `SELECT id FROM tv_changes WHERE source->>'kind' = $1 AND source->>'run_id' = $2
		ORDER BY number DESC LIMIT 1`
	if IsSQLite(c.DB) {
		query = `SELECT id FROM tv_changes WHERE json_extract(source, '$.kind') = $1
			AND json_extract(source, '$.run_id') = $2 ORDER BY number DESC LIMIT 1`
	}
	var id string
	err := q.QueryRowContext(ctx, query, SourceAutoUpdate, runID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// BeginAutoUpdate is the run's TV change, made when there is none: version's streaming
// binary (sha256: its file's, from the release's .verified) for the live TV. Skipped when
// staging is off, when no TV release airs yet, or when the live release runs that binary.
func (c *Controller) BeginAutoUpdate(ctx context.Context, runID, version, sha256 string) (AutoUpdateStatus, error) {
	runID, version, sha256 = strings.TrimSpace(runID), strings.TrimSpace(version), strings.ToLower(strings.TrimSpace(sha256))
	if runID == "" || len(runID) > 64 || !autoVersionRe.MatchString(version) || !autoSHARe.MatchString(sha256) {
		return AutoUpdateStatus{}, &ConflictError{Reason: "bad_request"}
	}
	if id, err := c.autoChangeID(ctx, c.DB, runID); err != nil || id != "" {
		if err != nil {
			return AutoUpdateStatus{}, err
		}
		return c.AutoUpdate(ctx, runID)
	}
	cfg, err := LoadConfig(ctx, c.DB)
	if err != nil {
		return AutoUpdateStatus{}, err
	}
	if !cfg.Enabled {
		return AutoUpdateStatus{RunID: runID, Outcome: AutoOutcomeSkipped, Skipped: SkipStagingOff}, nil
	}
	var liveVer, liveSum sql.NullString
	err = c.DB.QueryRowContext(ctx, `SELECT r.streaming_version, r.streaming_sha256 FROM tv_routing t
		JOIN tv_releases r ON r.id = t.live_release_id WHERE t.scope = 'cluster'`).Scan(&liveVer, &liveSum)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AutoUpdateStatus{RunID: runID, Outcome: AutoOutcomeSkipped, Skipped: SkipNoTV}, nil
	case err != nil:
		return AutoUpdateStatus{}, err
	case sameVersion(liveVer.String, version) && strings.EqualFold(liveSum.String, sha256):
		return AutoUpdateStatus{RunID: runID, Outcome: AutoOutcomeSkipped, Skipped: SkipAlreadyLive}, nil
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return AutoUpdateStatus{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if !IsSQLite(c.DB) {
		// the drafts' lock: one change per run however often it is asked at once
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey()); err != nil {
			return AutoUpdateStatus{}, err
		}
	}
	if id, err := c.autoChangeID(ctx, tx, runID); err != nil || id != "" {
		if err != nil {
			return AutoUpdateStatus{}, err
		}
		_ = tx.Rollback()
		return c.AutoUpdate(ctx, runID)
	}
	now := c.now()
	ch := &Change{State: StateDraft, Title: "Maktaba " + strings.TrimPrefix(version, "v") + ": the TV's streaming software",
		Source: map[string]any{"kind": SourceAutoUpdate, "run_id": runID, "version": version},
		Diff: map[string]any{"pending": map[string]any{"streaming": []string{version, sha256}},
			"stage": map[string]any{"requested_at": now.Format(time.RFC3339)}}}
	if err := c.Store.Insert(ctx, tx, ch); err != nil {
		return AutoUpdateStatus{}, err
	}
	if err := c.Store.Set(ctx, tx, ch.ID, map[string]any{"kinds": []string{"binary"},
		"swap_wait": "boundary", "auto_rule": RuleImmediate}); err != nil {
		return AutoUpdateStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return AutoUpdateStatus{}, err
	}
	c.Logger.Info("tv change for an automatic update", "event", "tv_change_auto_update", "change_id", ch.ID,
		"run_id", runID, "version", version)
	// staged now when nothing else is in flight; otherwise the controller stages it when
	// its turn comes (it stays a draft with its stage asked)
	if other, err := c.Store.InFlightChange(ctx, c.DB); err == nil && other == nil {
		if got, err := c.Store.Get(ctx, c.DB, ch.ID); err == nil {
			if err := c.requestStage(ctx, got, false); err != nil {
				c.Logger.Warn("tv change stage enqueue failed", "change_id", ch.ID, "error", err.Error())
			}
		}
	}
	return c.AutoUpdate(ctx, runID)
}

// sameVersion compares two release versions as the slots do (a "v" prefix aside).
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

// AutoUpdate is the run's TV change as its run reads it.
func (c *Controller) AutoUpdate(ctx context.Context, runID string) (AutoUpdateStatus, error) {
	st := AutoUpdateStatus{RunID: runID, Outcome: AutoOutcomeNone}
	id, err := c.autoChangeID(ctx, c.DB, runID)
	if err != nil || id == "" {
		return st, err
	}
	ch, err := c.Store.Get(ctx, c.DB, id)
	if err != nil {
		return st, err
	}
	cfg, err := LoadConfig(ctx, c.DB)
	if err != nil {
		return st, err
	}
	st.ChangeID, st.Number, st.State, st.Kinds, st.StagedAt = ch.ID, ch.Number, ch.State, ch.Kinds, ch.StagedAt
	st.TimeoutMinutes = cfg.BoundaryMaxWaitMin + 10
	st.Withdrawn, _ = ch.Source["withdrawn"].(string)
	if ch.EndReason != nil {
		st.EndReason = *ch.EndReason
	}
	if st.LiveStreaming, err = c.liveStreaming(ctx); err != nil {
		return st, err
	}
	switch ch.State {
	case StateLive, StateSuperseded:
		st.Outcome = AutoOutcomeLive
	case StateCancelled:
		st.Outcome = AutoOutcomeCancelled
		if st.EndReason == "noop" {
			st.Outcome = AutoOutcomeLive // it aired exactly what was live: nothing to swap
		}
	case StateRolledBack, StateFailed:
		st.Outcome, st.Reason = AutoOutcomeFailed, "rolled_back"
		st.Failed = failedCheckNames(ch.VerifyReport)
	case StateDraft:
		failed, err := c.lastActionIs(ctx, ch.ID, ActionFail)
		if err != nil {
			return st, err
		}
		if failed && !stageRequested(ch) {
			// its check failed before any swap: back to draft with its report (no retry
			// loop), held there until an admin retries or drops it, and the live TV stays
			// on its streaming software (the owner's decision, 2026-10-28)
			st.Outcome, st.Reason = AutoOutcomeHeld, "verify_failed"
			st.Failed, st.FailedChecks = failedCheckNames(ch.VerifyReport), failedCheckList(ch.VerifyReport)
			break
		}
		st.Outcome = AutoOutcomeQueued
		if other, err := c.Store.InFlightChange(ctx, c.DB); err == nil && other != nil && other.ID != ch.ID {
			st.QueuedBehind = &QueuedRef{ID: other.ID, Number: other.Number, State: other.State,
				Approved: len(other.ApprovedBy) > 0}
		}
	case StateReady:
		held, err := c.Store.HeldAfterSwapFailure(ctx, c.DB, ch.ID)
		if err != nil {
			return st, err
		}
		if held {
			// its swap failed: it waits for an admin, whom its run cannot wait for
			st.Outcome, st.Reason = AutoOutcomeFailed, "swap_failed"
			break
		}
		st.Outcome = AutoOutcomeInProgress
	default:
		st.Outcome = AutoOutcomeInProgress
	}
	return st, nil
}

// failedCheckNames are the checks of a verify report that failed (each once).
func failedCheckNames(report map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	checks, _ := report["checks"].([]any)
	for _, x := range checks {
		m, _ := x.(map[string]any)
		name, _ := m["check"].(string)
		if m["result"] == ResultFail && name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// failedCheckList are the failed checks of a verify report (at most 20: a summary).
func failedCheckList(report map[string]any) []Check {
	var out []Check
	checks, _ := report["checks"].([]any)
	for _, x := range checks {
		m, _ := x.(map[string]any)
		if m["result"] != ResultFail {
			continue
		}
		c := Check{Result: ResultFail}
		c.Check, _ = m["check"].(string)
		c.ChannelID, _ = m["channel_id"].(string)
		c.Value, _ = m["value"].(string)
		if out = append(out, c); len(out) == 20 {
			break
		}
	}
	return out
}

// liveStreaming is the streaming software the live TV runs ("" when none airs).
//
// The live slot's report comes first: it is the version stamped in the binary that runs.
// A release's streaming_version is only what the release recorded when it was built (a
// release made from a source checkout says 0.0.1, the pipeline package's own version:
// the owner's live release since 2026-10-22, so the update to 0.3.39 said "the TV stays
// on 0.0.1"), and a slot started unbound runs the installed binary whatever it says.
func (c *Controller) liveStreaming(ctx context.Context) (string, error) {
	if !IsSQLite(c.DB) { // TV slots run on a Postgres home only
		var slotVer sql.NullString
		err := c.DB.QueryRowContext(ctx, `SELECT version FROM tv_slots WHERE role = 'live' AND state = 'healthy'
			AND COALESCE(version, '') <> '' AND beat_at > now() - interval '30 seconds'
			ORDER BY beat_at DESC LIMIT 1`).Scan(&slotVer)
		switch {
		case err == nil && slotVer.String != "":
			return slotVer.String, nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return "", err
		}
	}
	var v sql.NullString
	err := c.DB.QueryRowContext(ctx, `SELECT r.streaming_version FROM tv_routing t
		JOIN tv_releases r ON r.id = t.live_release_id WHERE t.scope = 'cluster'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v.String, err
}

// heldChange is the run's change when it is held: a draft whose check failed, not asked
// to be staged again (ErrNotHeld otherwise; nil, nil when the run has none).
func (c *Controller) heldChange(ctx context.Context, runID string) (*Change, error) {
	id, err := c.autoChangeID(ctx, c.DB, runID)
	if err != nil || id == "" {
		if err == nil {
			err = ErrNotHeld
		}
		return nil, err
	}
	ch, err := c.Store.Get(ctx, c.DB, id)
	if err != nil {
		return nil, err
	}
	if ch.State != StateDraft || stageRequested(ch) {
		return nil, ErrNotHeld
	}
	failed, err := c.lastActionIs(ctx, ch.ID, ActionFail)
	if err != nil {
		return nil, err
	}
	if !failed {
		return nil, ErrNotHeld
	}
	return ch, nil
}

// RetryAutoUpdate checks a held change again (an admin's Retry on the Updates page): it
// is staged and verified as before; one that passes swaps at a programme boundary under
// its rule (immediate, no approval), one that fails is held again. Staged at once when
// nothing else is in flight, else when its turn comes.
func (c *Controller) RetryAutoUpdate(ctx context.Context, runID string, actor *string) (AutoUpdateStatus, error) {
	ch, err := c.heldChange(ctx, runID)
	if err != nil {
		return AutoUpdateStatus{}, err
	}
	if err := c.RequestStage(ctx, ch, actor); err != nil {
		var ce *ConflictError
		if !errors.As(err, &ce) || ce.Reason != "change_in_flight" {
			return AutoUpdateStatus{}, err
		}
		// another change is in flight: it is staged when its turn comes (the request is kept)
	}
	who := ""
	if actor != nil {
		who = *actor
	}
	c.Logger.Info("tv change of an automatic update checked again", "event", "tv_change_auto_retry",
		"change_id", ch.ID, "run_id", runID, "actor", who)
	return c.AutoUpdate(ctx, runID)
}

// DropAutoUpdate withdraws a held change (an admin's Drop on the Updates page): it is
// cancelled (end_reason dropped), and the TV stays on the streaming software it runs
// until a later release's change.
func (c *Controller) DropAutoUpdate(ctx context.Context, runID string, actor *string) (AutoUpdateStatus, error) {
	ch, err := c.heldChange(ctx, runID)
	if err != nil {
		return AutoUpdateStatus{}, err
	}
	if err := c.Store.Move(ctx, c.DB, ch, StateCancelled, ActionCancel, actor,
		map[string]any{"why": "dropped from the Updates page: its check failed"},
		map[string]any{"ended_at": c.now(), "end_reason": EndDropped}); err != nil {
		var ce *ConflictError
		if errors.As(err, &ce) {
			return AutoUpdateStatus{}, ErrNotHeld
		}
		return AutoUpdateStatus{}, err
	}
	c.Logger.Info("tv change of an automatic update dropped", "event", "tv_change_auto_dropped",
		"change_id", ch.ID, "run_id", runID)
	return c.AutoUpdate(ctx, runID)
}

// ReplaceOlderAutoUpdates cancels the automatic updates' drafts of runs other than runID
// (end_reason replaced): once a later release committed, its streaming software is the
// one the TV moves to, and a Retry of an older held change would air older software.
// The run that commits calls it (a run that rolls back leaves the held change it found).
func (c *Controller) ReplaceOlderAutoUpdates(ctx context.Context, runID string) error {
	query := `SELECT id FROM tv_changes WHERE state = 'draft' AND source->>'kind' = $1
		AND COALESCE(source->>'run_id', '') <> $2`
	if IsSQLite(c.DB) {
		query = `SELECT id FROM tv_changes WHERE state = 'draft' AND json_extract(source, '$.kind') = $1
			AND COALESCE(json_extract(source, '$.run_id'), '') <> $2`
	}
	rows, err := c.DB.QueryContext(ctx, query, SourceAutoUpdate, runID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		ch, err := c.Store.Get(ctx, c.DB, id)
		if err != nil {
			return err
		}
		if ch.State != StateDraft {
			continue
		}
		if err := c.Store.Move(ctx, c.DB, ch, StateCancelled, ActionCancel, nil,
			map[string]any{"why": "replaced by a later update's change (run " + runID + ")"},
			map[string]any{"ended_at": c.now(), "end_reason": EndReplaced}); err != nil {
			if moved(err) {
				continue
			}
			return err
		}
		c.Logger.Info("tv change of an older automatic update replaced", "event", "tv_change_auto_replaced",
			"change_id", id, "run_id", runID)
	}
	return nil
}

// lastActionIs reports whether a change's newest event is action.
func (c *Controller) lastActionIs(ctx context.Context, id, action string) (bool, error) {
	var last string
	err := c.DB.QueryRowContext(ctx, `SELECT action FROM tv_change_events WHERE change_id = $1
		ORDER BY id DESC LIMIT 1`, id).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return last == action, err
}

// WithdrawAutoUpdate takes the run's change back (its run rolls back, design §5.6): one
// not on the air yet is cancelled; one live is rolled back (Epic 33.9: warm while the
// previous slot is warm, else cold); one swapping is rolled back by the controller once
// its swap ends. Withdrawing it again does nothing more.
func (c *Controller) WithdrawAutoUpdate(ctx context.Context, runID, reason string) (AutoUpdateStatus, error) {
	id, err := c.autoChangeID(ctx, c.DB, runID)
	if err != nil || id == "" {
		if err != nil {
			return AutoUpdateStatus{}, err
		}
		return AutoUpdateStatus{RunID: runID, Outcome: AutoOutcomeNone}, nil
	}
	ch, err := c.Store.Get(ctx, c.DB, id)
	if err != nil {
		return AutoUpdateStatus{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "run_rolled_back"
	}
	if len(reason) > 64 {
		reason = reason[:64]
	}
	if w, _ := ch.Source["withdrawn"].(string); w == "" {
		src := copyMap(ch.Source)
		src["withdrawn"] = reason
		if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"source": src}); err != nil {
			return AutoUpdateStatus{}, err
		}
		ch.Source = src
	}
	if err := c.settleWithdrawn(ctx, ch); err != nil {
		var ce *ConflictError
		if !errors.As(err, &ce) {
			return AutoUpdateStatus{}, err
		}
	}
	return c.AutoUpdate(ctx, runID)
}

// settleWithdrawn acts on a withdrawn automatic update's change: cancel it before the
// air, roll it back once live (once: the rollback request is remembered).
func (c *Controller) settleWithdrawn(ctx context.Context, ch *Change) error {
	why, _ := ch.Source["withdrawn"].(string)
	if why == "" {
		return nil
	}
	switch ch.State {
	case StateDraft, StateStaged, StateVerifying, StateReady, StateApproved:
		err := c.Store.Move(ctx, c.DB, ch, StateCancelled, ActionCancel, nil, map[string]any{"why": "withdrawn by its update run: " + why},
			map[string]any{"ended_at": c.now(), "end_reason": "withdrawn"})
		if err == nil {
			c.Logger.Info("tv change of an automatic update withdrawn", "event", "tv_change_auto_withdrawn",
				"change_id", ch.ID, "reason", why)
		}
		return err
	case StateLive:
		if asked, _ := ch.Source["rollback_asked"].(bool); asked {
			return nil
		}
		pt, err := c.Point(ctx)
		if err != nil {
			return err
		}
		if pt.ChangeID != ch.ID || pt.Mode == "none" {
			// another change went on the air after it, or nothing to go back to: the TV
			// stays as it is (the run's rollback says so in its log)
			return &ConflictError{State: ch.State, Reason: "no_rollback_point"}
		}
		var res RollbackResult
		if pt.Mode == "cold" {
			res, err = c.coldRollback(ctx, ch, pt, nil)
		} else {
			res, err = c.requestRollback(ctx, ch, "auto_update_withdrawn", nil, map[string]any{"withdrawn": why})
		}
		if err != nil {
			return err
		}
		src := copyMap(ch.Source)
		src["rollback_asked"] = true
		_ = c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"source": src})
		ch.Source = src
		c.Logger.Info("tv change of an automatic update rolled back", "event", "tv_change_auto_rollback",
			"change_id", ch.ID, "mode", res.Mode, "reason", why)
		return nil
	}
	return nil // swapping: rolled back once live (tickWithdrawn); ended: nothing to do
}

// tickWithdrawn rolls back the automatic updates' changes their runs withdrew while
// they swapped, once they are live.
func (c *Controller) tickWithdrawn(ctx context.Context) error {
	query := `SELECT id FROM tv_changes WHERE state = 'live' AND source->>'kind' = $1
		AND COALESCE(source->>'withdrawn', '') <> '' AND COALESCE(source->>'rollback_asked', '') = ''`
	if IsSQLite(c.DB) {
		query = `SELECT id FROM tv_changes WHERE state = 'live' AND json_extract(source, '$.kind') = $1
			AND COALESCE(json_extract(source, '$.withdrawn'), '') <> ''
			AND json_extract(source, '$.rollback_asked') IS NULL`
	}
	rows, err := c.DB.QueryContext(ctx, query, SourceAutoUpdate)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		ch, err := c.Store.Get(ctx, c.DB, id)
		if err != nil {
			continue
		}
		if err := c.settleWithdrawn(ctx, ch); err != nil {
			var ce *ConflictError
			if errors.As(err, &ce) {
				// nothing to go back to: remembered, so it is not asked every tick
				src := copyMap(ch.Source)
				src["rollback_asked"] = true
				_ = c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"source": src})
				continue
			}
			return fmt.Errorf("roll back withdrawn change %s: %w", id, err)
		}
	}
	return nil
}
