package tvchange

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// Notification kinds of TV changes (story 33.13 AC1). The web app's message keys are
// "notify.<kind>" (en / ar).
const (
	NotifyReady         = "tv.change.ready"          // verified, needs an approval
	NotifyAutoScheduled = "tv.change.auto_scheduled" // verified, an auto-promote rule puts it on the air
	NotifyFailed        = "tv.change.failed"         // verification failed
	NotifySwapFailed    = "tv.change.swap_failed"    // the swap commit failed (back to ready)
	NotifyLive          = "tv.change.live"           // swapped: on the air
	NotifyRolledBack    = "tv.change.rolled_back"    // manual or automatic, with the cause
	NotifyWaiting       = "tv.change.waiting"        // ready for more than a day
	NotifyRollbackEnds  = "tv.rollback.expiring"     // the rollback point goes within the hour
	// Sent by the controller's swap operations tick (swap.go).
	NotifySwapNotTaken         = "tv.change.swap_not_taken"  // no front door took the swap in time: rolled back
	NotifyRollbackDegraded     = "tv.rollback.degraded"      // a warm rollback without a live TV slot: the database only
	NotifyRollbackNotCommitted = "tv.rollback.not_committed" // the reverse commit found nothing to roll back
	// From the front door's slot events (tv_slot_events, slot 0137): notifySlotEvents
	// sends them through the same Notifier.
	NotifyFailover  = "tv.slot.failover"   // the live slot failed over to the hot standby
	NotifyCrashLoop = "tv.slot.crash_loop" // the staging slot keeps crashing
)

// feedCursor is the notification_cursors row of the TV change event feed.
const feedCursor = "tv_change_events"

// feedBatch bounds one tick's events.
const feedBatch = 200

// feedGapGrace is how long the feed waits at a hole in the event ids. Ids are taken
// when a row is inserted, not when its transaction commits: the swap commit's
// transaction (the pipeline's tv_swap) writes its "live" event early and commits after
// other, later-numbered events are already visible. Reading past the hole would skip
// that event for good (no notice, no audit row); a hole that stays (a rolled-back
// insert) is passed once the events after it are this old.
const feedGapGrace = 2 * time.Minute

// ChangeLink is the Changes page of a change.
func ChangeLink(id string) string { return "/admin/live-tv/changes/" + id }

type feedEvent struct {
	ID       int64
	ChangeID string
	Actor    sql.NullString
	Source   string
	From     sql.NullString
	To       sql.NullString
	Action   string
	Detail   map[string]any
	At       time.Time
}

// notifyEvents turns the change events written since the last tick - by the API, the
// controller and the pipeline's tv_stage / tv_swap jobs alike - into notices, and writes
// an audit_log row for every transition no admin made (story 33.13 AC1, AC3; an admin's
// action is audited by its handler). A first run starts at the newest event: nothing
// that happened before the feed existed is announced.
func (c *Controller) notifyEvents(ctx context.Context, cfg Config) error {
	var pos int64
	err := c.DB.QueryRowContext(ctx, `SELECT position FROM notification_cursors WHERE name = $1`, feedCursor).Scan(&pos)
	if errors.Is(err, sql.ErrNoRows) {
		var top sql.NullInt64
		if err := c.DB.QueryRowContext(ctx, `SELECT MAX(id) FROM tv_change_events`).Scan(&top); err != nil {
			return err
		}
		_, err = c.DB.ExecContext(ctx, `INSERT INTO notification_cursors (name, position) VALUES ($1, $2)
			ON CONFLICT (name) DO NOTHING`, feedCursor, top.Int64)
		return err
	}
	if err != nil {
		return err
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT id, change_id, actor_user_id, actor_source, from_state, to_state,
		action, detail, at FROM tv_change_events WHERE id > $1 ORDER BY id LIMIT $2`, pos, feedBatch)
	if err != nil {
		return err
	}
	evs := []feedEvent{}
	for rows.Next() {
		var e feedEvent
		var detail []byte
		var at nullTime
		if err := rows.Scan(&e.ID, &e.ChangeID, &e.Actor, &e.Source, &e.From, &e.To, &e.Action, &detail, &at); err != nil {
			rows.Close()
			return err
		}
		e.Detail = map[string]any{}
		e.At = at.T
		decodeJSON(detail, &e.Detail)
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	start := pos
	for _, e := range evs {
		if e.ID != pos+1 && c.now().Sub(e.At) < feedGapGrace {
			break // an earlier event may still be committing: the next tick reads on
		}
		if err := c.onEvent(ctx, cfg, e); err != nil {
			c.Logger.Warn("tv change notification failed", "event", "tv_change_notify_failed",
				"change_id", e.ChangeID, "error", err.Error())
		}
		pos = e.ID
	}
	if pos == start {
		return nil
	}
	_, err = c.DB.ExecContext(ctx, `UPDATE notification_cursors SET position = $2, updated_at = $3 WHERE name = $1`,
		feedCursor, pos, ts(c.DB, c.now()))
	return err
}

func (c *Controller) onEvent(ctx context.Context, cfg Config, e feedEvent) error {
	transition := e.To.Valid && (!e.From.Valid || e.From.String != e.To.String)
	if transition && !e.Actor.Valid {
		c.Store.AuditAs(ctx, c.DB, e.Source, "", e.Action, e.ChangeID, map[string]any{
			"from": nullStr(e.From), "to": e.To.String, "action": e.Action, "event_id": e.ID})
	}
	if !transition || c.Notifier == nil {
		return nil
	}
	ch, err := c.Store.Get(ctx, c.DB, e.ChangeID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	params := map[string]any{"number": ch.Number, "title": ch.Title, "kinds": ch.Kinds}
	p := notify.Payload{Params: params, Link: ChangeLink(ch.ID), Subject: "tv_change:" + ch.ID}
	var kind string
	switch {
	case e.To.String == StateReady && e.Action == ActionPass:
		if len(ch.ApprovedBy) > 0 {
			return nil // approved ahead: it goes on by itself
		}
		if rule := StrictestRule(cfg, ch.Kinds); rule != RuleApproval {
			kind, p.Severity = NotifyAutoScheduled, notify.SeverityInfo
			params["rule"] = rule
			params["window"] = cfg.QuietWindow
		} else {
			kind, p.Severity = NotifyReady, notify.SeverityWarn
		}
	case e.To.String == StateFailed:
		kind, p.Severity = NotifyFailed, notify.SeverityError
		params["failed"] = failedChecks(ch.VerifyReport)
	case e.Action == ActionSwapFailed:
		kind, p.Severity = NotifySwapFailed, notify.SeverityError
		if why, ok := e.Detail["error"].(string); ok {
			params["error"] = why
		}
	case e.To.String == StateLive:
		kind, p.Severity = NotifyLive, notify.SeveritySuccess
	case e.To.String == StateRolledBack:
		kind, p.Severity = NotifyRolledBack, notify.SeverityWarn
		cause := ""
		if ch.EndReason != nil {
			cause = *ch.EndReason
		}
		if s, ok := e.Detail["cause"].(string); ok && s != "" {
			cause = s
		}
		params["cause"] = cause
		params["automatic"] = !e.Actor.Valid
		if auto, ok := e.Detail["automatic"].(bool); ok {
			// the reverse commit (tv_rollback) says: its job is the actor either way
			params["automatic"] = auto
		}
	default:
		return nil
	}
	return c.Notifier.Notify(ctx, kind, p)
}

func nullStr(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

// failedChecks counts the failed checks of a report.
func failedChecks(report map[string]any) int {
	n := 0
	checks, _ := report["checks"].([]any)
	for _, x := range checks {
		if m, _ := x.(map[string]any); m["result"] == ResultFail {
			n++
		}
	}
	return n
}

// notifyTimers sends the notices that come from time passing: a ready change waiting
// more than a day (once a day), and a rollback point that ends within the hour.
func (c *Controller) notifyTimers(ctx context.Context, open []*Change) error {
	if c.Notifier == nil {
		return nil
	}
	now := c.now()
	for _, ch := range open {
		if ch.State != StateReady || ch.ReadyAt == nil || now.Sub(*ch.ReadyAt) < 24*time.Hour {
			continue
		}
		if err := c.Notifier.Notify(ctx, NotifyWaiting, notify.Payload{Severity: notify.SeverityWarn,
			Params: map[string]any{"number": ch.Number, "title": ch.Title, "hours": int(now.Sub(*ch.ReadyAt).Hours())},
			Link:   ChangeLink(ch.ID), Subject: "tv_change:" + ch.ID, Every: 24 * time.Hour}); err != nil {
			return err
		}
	}
	var prev sql.NullString
	var until nullTime
	err := c.DB.QueryRowContext(ctx, `SELECT previous_release_id, previous_until FROM tv_routing WHERE scope = 'cluster'`).
		Scan(&prev, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if prev.Valid && until.Valid && until.T.After(now) && until.T.Sub(now) <= time.Hour {
		return c.Notifier.Notify(ctx, NotifyRollbackEnds, notify.Payload{Severity: notify.SeverityInfo,
			Params: map[string]any{"until": until.T.Format(time.RFC3339), "minutes": int(until.T.Sub(now).Minutes())},
			Link:   "/admin/live-tv/changes", Subject: "tv_release:" + prev.String, Every: 24 * time.Hour})
	}
	return nil
}

// AuditAs writes a transition no admin made to audit_log (category tv_change,
// actor_source = the event's source: system, a pipeline stage). userID may be empty.
func (s *Store) AuditAs(ctx context.Context, q queryer, source, userID, action, changeID string, payload map[string]any) {
	if source == "" {
		source = "system"
	}
	s.audit(ctx, q, source, userID, action, changeID, payload)
}
