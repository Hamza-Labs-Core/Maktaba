package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The swap after its commit (story 33.7, design §8.3) and the rollback operations
// (story 33.9, design §10), as the controller sees them.
//
// The swap commit (the pipeline's tv_swap job, tvrelease/commit.py) is the database
// half: one transaction applies the release's lineup and settings, flips tv_routing
// to its generation and — with TV slots — writes the front door's swap operation
// (tv_swap_ops kind swap, a tv_channel_switch row per channel with its programme
// boundary). The change stays swapping while the front door switches the channels;
// this file moves it on: live when the operation is done (more than a quarter of
// the channels skipped: rolled back instead, story 33.7 EC2), rolled back when it
// failed (its new slot was lost or never answered). A rollback operation the front
// door wrote itself (the new live slot crashed inside the grace: the automatic
// rollback) gets its reverse commit enqueued here (the tv_rollback job).

// Pipeline stage of the reverse commit (tvrelease/rollback.py).
const StageTVRollback = "tv_rollback"

// Actions of the swap's completion and the rollback.
const (
	ActionSwapCommit    = "swap_commit"    // tv_swap job: the database half (swapping stays)
	ActionSwapDone      = "swap_done"      // controller: swapping -> live (every channel switched)
	ActionRollbackAsked = "rollback_asked" // an admin's or the controller's rollback request
)

// skippedRollbackShare: a swap that left more than this share of its channels on the
// old slot is rolled back (story 33.7 EC2).
const skippedRollbackShare = 0.25

// swapOp is a tv_swap_ops row (the fields the controller uses).
type swapOp struct {
	ID        int64
	ChangeID  string
	Kind      string
	State     string
	Committed bool
	Reason    string
	Detail    map[string]any
}

// frontDoorOps reports whether this database has the swap operations (Postgres with
// slot 0131; a SQLite home runs one TV process and the commit is the whole swap).
func (c *Controller) frontDoorOps(ctx context.Context) bool {
	if IsSQLite(c.DB) {
		return false
	}
	var ok bool
	return c.DB.QueryRowContext(ctx, `SELECT to_regclass('tv_swap_ops') IS NOT NULL`).Scan(&ok) == nil && ok
}

// tickOps moves the swapping changes whose operation ended and enqueues the reverse
// commit of the rollbacks nobody committed yet.
func (c *Controller) tickOps(ctx context.Context) error {
	if !c.frontDoorOps(ctx) {
		return nil
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT DISTINCT ON (o.change_id, o.kind) o.id, o.change_id::text, o.kind,
			o.state, o.committed, o.reason, o.detail
		FROM tv_swap_ops o JOIN tv_changes ch ON ch.id = o.change_id
		WHERE ch.state IN ('swapping', 'live')
		ORDER BY o.change_id, o.kind, o.id DESC`)
	if err != nil {
		return err
	}
	var ops []swapOp
	for rows.Next() {
		var o swapOp
		var detail []byte
		if err := rows.Scan(&o.ID, &o.ChangeID, &o.Kind, &o.State, &o.Committed, &o.Reason, &detail); err != nil {
			rows.Close()
			return err
		}
		o.Detail = map[string]any{}
		decodeJSON(detail, &o.Detail)
		ops = append(ops, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rolling := map[string]bool{}
	for _, o := range ops {
		if o.Kind == "rollback" && o.State != "cancelled" {
			rolling[o.ChangeID] = true
		}
	}
	for _, o := range ops {
		ch, err := c.Store.Get(ctx, c.DB, o.ChangeID)
		if err != nil {
			continue
		}
		switch {
		case o.Kind == "rollback" && !o.Committed && o.State != "cancelled":
			if err := c.enqueueRollbackCommit(ctx, ch, o); err != nil {
				c.Logger.Warn("tv rollback commit enqueue failed", "change_id", ch.ID, "error", err.Error())
			}
		case o.Kind == "swap" && ch.State == StateSwapping && !rolling[o.ChangeID]:
			if err := c.swapEnded(ctx, ch, o); err != nil {
				return err
			}
		}
	}
	return nil
}

// swapEnded moves a swapping change whose operation ended.
func (c *Controller) swapEnded(ctx context.Context, ch *Change, o swapOp) error {
	switch o.State {
	case "done":
		sw, _ := o.Detail["switched"].(float64)
		sk, _ := o.Detail["skipped"].(float64)
		if sw+sk > 0 && sk/(sw+sk) > skippedRollbackShare {
			_, err := c.requestRollback(ctx, ch, "auto:skipped", nil, nil)
			return err
		}
		return c.Store.Move(ctx, c.DB, ch, StateLive, ActionSwapDone, nil,
			map[string]any{"op_id": o.ID, "switched": sw, "skipped": sk, "rolling": o.Detail["rolling"],
				"live_slot": o.Detail["to_slot"], "previous_slot": o.Detail["from_slot"]}, nil)
	case "failed", "cancelled":
		reason := "auto:swap_failed"
		if r, _ := o.Detail["reason"].(string); r != "" {
			reason = r
		}
		_, err := c.requestRollback(ctx, ch, reason, nil, nil)
		return err
	}
	return nil
}

// enqueueRollbackCommit enqueues the reverse commit of a rollback operation once.
func (c *Controller) enqueueRollbackCommit(ctx context.Context, ch *Change, o swapOp) error {
	if at, _ := o.Detail["enqueued_at"].(string); at != "" {
		if t, err := time.Parse(time.RFC3339, at); err == nil && time.Since(t) < 2*time.Minute {
			return nil
		}
	}
	if c.Enqueue == nil {
		return errors.New("no queue")
	}
	payload := map[string]any{"change_id": ch.ID, "op_id": o.ID, "reason": o.Reason}
	if a, _ := o.Detail["actor"].(string); a != "" {
		payload["actor"] = a
	}
	if err := c.Enqueue(ctx, StageTVRollback, payload, "roll back TV change"); err != nil {
		return err
	}
	stamp, _ := json.Marshal(map[string]any{"enqueued_at": c.now().Format(time.RFC3339)})
	_, err := c.DB.ExecContext(ctx, `UPDATE tv_swap_ops SET detail = detail || $2::jsonb WHERE id = $1`, o.ID, string(stamp))
	return err
}

// RollbackPoint is what a rollback would do now (story 33.9 AC7).
type RollbackPoint struct {
	// Mode: warm (the previous slot is up: instant), cold (re-stage the previous
	// release: minutes) or none.
	Mode string `json:"mode"`
	// ChangeID is the live change a rollback ends.
	ChangeID     string `json:"change_id,omitempty"`
	ChangeNumber int64  `json:"change_number,omitempty"`
	// ReleaseID is the release that goes back on the air.
	ReleaseID string `json:"release_id,omitempty"`
	// Until: warm until (previous_until), or cold until (retention).
	Until *time.Time `json:"until,omitempty"`
	// Slot is the previous slot (warm, with TV slots).
	Slot string `json:"slot,omitempty"`
	// Why there is none (no_live_change, no_rollback_point, retention_expired).
	Reason string `json:"reason,omitempty"`
}

// liveChange is the change on the air now (live, or swapping).
func (c *Controller) liveChange(ctx context.Context) (*Change, error) {
	var id string
	err := c.DB.QueryRowContext(ctx, `SELECT id FROM tv_changes WHERE state IN ('live', 'swapping')
		ORDER BY swapped_at DESC, updated_at DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.Store.Get(ctx, c.DB, id)
}

// Point is the rollback point now.
func (c *Controller) Point(ctx context.Context) (RollbackPoint, error) {
	cfg, err := LoadConfig(ctx, c.DB)
	if err != nil {
		return RollbackPoint{}, err
	}
	ch, err := c.liveChange(ctx)
	if err != nil {
		return RollbackPoint{}, err
	}
	if ch == nil {
		return RollbackPoint{Mode: "none", Reason: "no_live_change"}, nil
	}
	p := RollbackPoint{ChangeID: ch.ID, ChangeNumber: ch.Number}
	if ch.State == StateSwapping {
		// story 33.9 EC1: still switching — the switched channels go back at once
		p.Mode = "warm"
		if ch.BaseReleaseID != nil {
			p.ReleaseID = *ch.BaseReleaseID
		}
		return p, nil
	}
	var prev sql.NullString
	var until, switched nullTime
	err = c.DB.QueryRowContext(ctx, `SELECT previous_release_id, previous_until, switched_at FROM tv_routing
		WHERE scope = 'cluster'`).Scan(&prev, &until, &switched)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RollbackPoint{}, err
	}
	if !prev.Valid || prev.String == "" {
		p.Mode, p.Reason = "none", "no_rollback_point"
		return p, nil
	}
	p.ReleaseID = prev.String
	now := c.now()
	if until.Valid && until.T.After(now) {
		slotsOn, slotName := c.previousSlot(ctx)
		if !slotsOn || slotName != "" {
			t := until.T
			p.Mode, p.Until, p.Slot = "warm", &t, slotName
			return p, nil
		}
	}
	keep := time.Duration(cfg.RetentionHours) * time.Hour
	if switched.Valid && switched.T.Add(keep).After(now) {
		t := switched.T.Add(keep)
		p.Mode, p.Until = "cold", &t
		return p, nil
	}
	p.Mode, p.Reason = "none", "retention_expired"
	return p, nil
}

// previousSlot reports whether TV slots run (a live slot beats) and the healthy
// previous slot ("" : none).
func (c *Controller) previousSlot(ctx context.Context) (bool, string) {
	if IsSQLite(c.DB) {
		return false, ""
	}
	var live int
	if c.DB.QueryRowContext(ctx, `SELECT count(*) FROM tv_slots WHERE role = 'live'
		AND beat_at > now() - interval '30 seconds'`).Scan(&live) != nil || live == 0 {
		return false, ""
	}
	var name string
	_ = c.DB.QueryRowContext(ctx, `SELECT slot FROM tv_slots WHERE role = 'previous'
		AND beat_at > now() - interval '15 seconds' ORDER BY slot LIMIT 1`).Scan(&name)
	return true, name
}

// RollbackResult is the answer of a rollback request.
type RollbackResult struct {
	Mode     string `json:"mode"`
	ChangeID string `json:"change_id"`
	// OpID is the front door's rollback operation (warm, with TV slots).
	OpID int64 `json:"op_id,omitempty"`
	// RollbackChangeID is the new rollback change (cold).
	RollbackChangeID string `json:"rollback_change_id,omitempty"`
}

// Rollback puts the previous release back (POST /api/tv/rollback, story 33.9): warm
// while the previous slot is up (the front door switches every channel back at once;
// the reverse commit follows), cold within the retention (a rollback change re-stages
// the previous release; the admin's request is its approval). toRelease, when set,
// must be the previous release.
func (c *Controller) Rollback(ctx context.Context, actor *string, toRelease string) (RollbackResult, error) {
	pt, err := c.Point(ctx)
	if err != nil {
		return RollbackResult{}, err
	}
	if pt.Mode == "none" {
		return RollbackResult{}, &ConflictError{State: "", Reason: "no_rollback_point"}
	}
	if toRelease != "" && toRelease != pt.ReleaseID {
		return RollbackResult{}, &ConflictError{Reason: "not_the_previous_release"}
	}
	ch, err := c.Store.Get(ctx, c.DB, pt.ChangeID)
	if err != nil {
		return RollbackResult{}, err
	}
	if pt.Mode == "cold" {
		return c.coldRollback(ctx, ch, pt, actor)
	}
	return c.requestRollback(ctx, ch, "manual", actor, nil)
}

// requestRollback asks for a warm rollback of a live (or swapping) change: with TV
// slots the front door's operation (every channel back at once), then the reverse
// commit. A second change in flight is cancelled first (story 33.9 EC2).
func (c *Controller) requestRollback(ctx context.Context, ch *Change, reason string, actor *string,
	detail map[string]any) (RollbackResult, error) {
	res := RollbackResult{Mode: "warm", ChangeID: ch.ID}
	if err := c.cancelOthers(ctx, ch.ID, actor); err != nil {
		return res, err
	}
	if c.frontDoorOps(ctx) {
		if slotsOn, _ := c.previousSlot(ctx); slotsOn {
			op, err := c.writeRollbackOp(ctx, ch, reason, actor)
			if err != nil {
				return res, err
			}
			res.OpID = op
		}
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["reason"], detail["op_id"] = reason, res.OpID
	_ = c.Store.Event(ctx, c.DB, ch.ID, Event{ActorUserID: actor, ActorSource: actorSource(actor, "system"),
		FromState: &ch.State, ToState: &ch.State, Action: ActionRollbackAsked, Detail: detail})
	if c.Enqueue == nil {
		return res, errors.New("no queue")
	}
	payload := map[string]any{"change_id": ch.ID, "reason": reason}
	if res.OpID != 0 {
		payload["op_id"] = res.OpID
	}
	if actor != nil {
		payload["actor"] = *actor
	}
	if err := c.Enqueue(ctx, StageTVRollback, payload, "roll back TV change"); err != nil {
		return res, err
	}
	if res.OpID != 0 {
		stamp, _ := json.Marshal(map[string]any{"enqueued_at": c.now().Format(time.RFC3339)})
		_, _ = c.DB.ExecContext(ctx, `UPDATE tv_swap_ops SET detail = detail || $2::jsonb WHERE id = $1`, res.OpID, string(stamp))
	}
	c.Logger.Info("tv rollback requested", "event", "tv_rollback_requested", "change_id", ch.ID, "reason", reason,
		"op_id", res.OpID)
	return res, nil
}

// writeRollbackOp writes the front door's rollback operation: every channel of both
// lineups back to the previous release at once.
func (c *Controller) writeRollbackOp(ctx context.Context, ch *Change, reason string, actor *string) (int64, error) {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('tv_swap'))`); err != nil {
		return 0, err
	}
	var existing int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tv_swap_ops WHERE change_id = $1 AND kind = 'rollback'
		AND state IN ('planned', 'switching') ORDER BY id DESC LIMIT 1`, ch.ID).Scan(&existing); err == nil {
		return existing, tx.Commit()
	}
	by := "api"
	if actor != nil {
		by = "api:" + *actor
	}
	detail := map[string]any{}
	if actor != nil {
		detail["actor"] = *actor
	}
	d, _ := json.Marshal(detail)
	var op int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO tv_swap_ops (change_id, kind, release_id, from_release_id, epoch,
			reason, created_by, detail)
		SELECT $1, 'rollback', r.previous_release_id, r.live_release_id, r.epoch, $2, $3, $4::jsonb
		FROM tv_routing r WHERE r.scope = 'cluster' RETURNING id`, ch.ID, reason, by, string(d)).Scan(&op); err != nil {
		return 0, fmt.Errorf("rollback operation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tv_channel_switch (op_id, change_id, channel_id, switch_at)
		SELECT $1, $2, ids.id, now() FROM (
			SELECT id FROM channels WHERE lifecycle = 'live'
			UNION SELECT rc.channel_id FROM tv_release_channels rc JOIN tv_routing r ON r.scope = 'cluster'
			  AND rc.release_id = r.previous_release_id) ids
		ON CONFLICT DO NOTHING`, op, ch.ID); err != nil {
		return 0, err
	}
	return op, tx.Commit()
}

// cancelOthers cancels a change in flight other than the live one (its draft stays).
func (c *Controller) cancelOthers(ctx context.Context, keep string, actor *string) error {
	open, err := c.Store.List(ctx, []string{StateStaged, StateVerifying, StateReady, StateApproved}, 50)
	if err != nil {
		return err
	}
	for _, o := range open {
		if o.ID == keep {
			continue
		}
		if err := c.Store.Move(ctx, c.DB, o, StateCancelled, ActionCancel, actor,
			map[string]any{"by": "rollback", "rolled_back": keep},
			map[string]any{"ended_at": c.now(), "end_reason": "rollback"}); err != nil {
			var ce *ConflictError
			if !errors.As(err, &ce) {
				return err
			}
		}
	}
	return nil
}

// coldRollback creates a rollback change that re-stages the previous release with a
// fresh generation (story 33.9 AC4): its pending edits turn the live snapshot into the
// previous release's, every channel the rolled-back release regenerated is planned
// anew from its boundary, and the admin's request is its approval
// (require_second_admin still applies).
func (c *Controller) coldRollback(ctx context.Context, live *Change, pt RollbackPoint, actor *string) (RollbackResult, error) {
	cfg, err := LoadConfig(ctx, c.DB)
	if err != nil {
		return RollbackResult{}, err
	}
	pending, err := c.rollbackPending(ctx, pt.ReleaseID, live)
	if err != nil {
		return RollbackResult{}, err
	}
	rb := &Change{State: StateDraft, Title: fmt.Sprintf("Roll back #%d", live.Number), CreatedBy: actor,
		Source: map[string]any{"kind": "rollback", "change_id": live.ID, "release_id": pt.ReleaseID},
		Diff:   map[string]any{"pending": pending}}
	if err := c.Store.Insert(ctx, c.DB, rb); err != nil {
		return RollbackResult{}, err
	}
	if actor != nil && !cfg.RequireSecondAdmin {
		if err := c.Store.Set(ctx, c.DB, rb.ID, map[string]any{"approved_by": []string{*actor},
			"approved_at": c.now()}); err != nil {
			return RollbackResult{}, err
		}
	}
	rb, err = c.Store.Get(ctx, c.DB, rb.ID)
	if err != nil {
		return RollbackResult{}, err
	}
	if err := c.RequestStage(ctx, rb, actor); err != nil {
		var ce *ConflictError
		if !errors.As(err, &ce) {
			return RollbackResult{}, err
		}
	}
	c.Logger.Info("tv cold rollback requested", "event", "tv_rollback_requested", "change_id", live.ID,
		"rollback_change_id", rb.ID, "release_id", pt.ReleaseID)
	return RollbackResult{Mode: "cold", ChangeID: live.ID, RollbackChangeID: rb.ID}, nil
}

// rollbackPending is the pending edits that make the live snapshot the release's:
// every channel definition and art of the release, the live channels it does not
// have removed, its settings and streaming binary, and the channels the live change
// regenerated planned anew.
func (c *Controller) rollbackPending(ctx context.Context, releaseID string, live *Change) (map[string]any, error) {
	chans := map[string]any{}
	art := map[string]any{}
	rows, err := c.DB.QueryContext(ctx, `SELECT channel_id::text, def, art FROM tv_release_channels WHERE release_id = $1`,
		releaseID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var def, a []byte
		if err := rows.Scan(&id, &def, &a); err != nil {
			rows.Close()
			return nil, err
		}
		var d map[string]any
		decodeJSON(def, &d)
		chans[id] = d
		var am map[string]any
		decodeJSON(a, &am)
		if len(am) > 0 {
			art[id] = am
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	lrows, err := c.DB.QueryContext(ctx, `SELECT id::text FROM channels WHERE lifecycle = 'live'`)
	if err != nil {
		return nil, err
	}
	for lrows.Next() {
		var id string
		if lrows.Scan(&id) == nil {
			if _, ok := chans[id]; !ok {
				chans[id] = nil
			}
		}
	}
	lrows.Close()
	// art overrides the live channels have and the release does not: removed
	if arows, err := c.DB.QueryContext(ctx, `SELECT channel_id::text, kind FROM channel_art_overrides`); err == nil {
		for arows.Next() {
			var id, kind string
			if arows.Scan(&id, &kind) != nil {
				continue
			}
			if d, ok := chans[id]; !ok || d == nil {
				continue
			}
			am, _ := art[id].(map[string]any)
			if am == nil {
				am = map[string]any{}
			}
			if _, ok := am[kind]; !ok {
				am[kind] = nil
			}
			art[id] = am
		}
		arows.Close()
	}
	var settings []byte
	var version, sum sql.NullString
	if err := c.DB.QueryRowContext(ctx, `SELECT settings, streaming_version, streaming_sha256 FROM tv_releases WHERE id = $1`,
		releaseID).Scan(&settings, &version, &sum); err != nil {
		return nil, err
	}
	var set map[string]any
	decodeJSON(settings, &set)
	var sched []string
	if live.ReleaseID != nil {
		srows, err := c.DB.QueryContext(ctx, `SELECT channel_id::text FROM tv_release_channels WHERE release_id = $1
			AND sched_changed`, *live.ReleaseID)
		if err == nil {
			for srows.Next() {
				var id string
				if srows.Scan(&id) == nil {
					sched = append(sched, id)
				}
			}
			srows.Close()
		}
	}
	p := map[string]any{"channels": chans, "art": art, "settings": set, "sched": sched, "rollback": true,
		"rollback_of": live.ID}
	if version.Valid && version.String != "" {
		p["streaming"] = []string{version.String, sum.String}
	}
	return p, nil
}
