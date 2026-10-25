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
	AutoOutcomeFailed     = "failed"      // its verification or its swap failed, or it was rolled back
	AutoOutcomeCancelled  = "cancelled"   // an admin cancelled it, or its run withdrew it
)

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
	// Reason is why it failed (verify_failed, swap_failed, rolled_back).
	Reason string `json:"reason,omitempty"`
	// Failed is the checks that failed (its verify report's), for the admin's notice.
	Failed []string `json:"failed,omitempty"`
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
		if failed {
			// its verification failed: back to draft with its report (no retry loop)
			st.Outcome, st.Reason, st.Failed = AutoOutcomeFailed, "verify_failed", failedCheckNames(ch.VerifyReport)
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
