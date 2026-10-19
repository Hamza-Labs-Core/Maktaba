package tvchange

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// slotFeedCursor is the notification_cursors row of the TV slot event feed.
const slotFeedCursor = "tv_slot_events"

// Slot event kinds the front door writes (tv_slot_events.kind, slot 0137).
const (
	SlotEventFailover  = "failover"
	SlotEventCrashLoop = "crash_loop"
)

// slotNoticeLink is where a slot notice leads: the Live TV page (slots, on air).
const slotNoticeLink = "/admin/live-tv"

// notifySlotEvents turns the front door's slot events (a failover to the hot standby,
// a staging slot that keeps crashing) into notices (story 33.13 AC1). Like the change
// event feed, a first run starts at the newest event: nothing that happened before the
// feed existed is announced. A repeat about the same host is limited by the hub (once
// an hour, EC1).
func (c *Controller) notifySlotEvents(ctx context.Context) error {
	if c.Notifier == nil {
		return nil
	}
	var pos int64
	err := c.DB.QueryRowContext(ctx, `SELECT position FROM notification_cursors WHERE name = $1`, slotFeedCursor).Scan(&pos)
	if errors.Is(err, sql.ErrNoRows) {
		var top sql.NullInt64
		if err := c.DB.QueryRowContext(ctx, `SELECT MAX(id) FROM tv_slot_events`).Scan(&top); err != nil {
			return err
		}
		_, err = c.DB.ExecContext(ctx, `INSERT INTO notification_cursors (name, position) VALUES ($1, $2)
			ON CONFLICT (name) DO NOTHING`, slotFeedCursor, top.Int64)
		return err
	}
	if err != nil {
		return err
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT id, host_id, kind, slot, detail FROM tv_slot_events
		WHERE id > $1 ORDER BY id LIMIT $2`, pos, feedBatch)
	if err != nil {
		return err
	}
	type slotEvent struct {
		id               int64
		host, kind, slot string
		detail           map[string]any
	}
	evs := []slotEvent{}
	for rows.Next() {
		var e slotEvent
		var detail []byte
		if err := rows.Scan(&e.id, &e.host, &e.kind, &e.slot, &detail); err != nil {
			rows.Close()
			return err
		}
		e.detail = map[string]any{}
		decodeJSON(detail, &e.detail)
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(evs) == 0 {
		return nil
	}
	for _, e := range evs {
		params := map[string]any{"host": e.host, "slot": e.slot}
		for k, v := range e.detail {
			params[k] = v
		}
		var kind, sev string
		switch e.kind {
		case SlotEventFailover:
			kind, sev = NotifyFailover, notify.SeverityWarn
		case SlotEventCrashLoop:
			kind, sev = NotifyCrashLoop, notify.SeverityError
		default:
			continue
		}
		if err := c.Notifier.Notify(ctx, kind, notify.Payload{Severity: sev, Params: params, Link: slotNoticeLink,
			Subject: "tv_slot:" + e.host}); err != nil {
			c.Logger.Warn("tv slot notification failed", "event", "tv_slot_notify_failed",
				"kind", e.kind, "error", err.Error())
		}
	}
	_, err = c.DB.ExecContext(ctx, `UPDATE notification_cursors SET position = $2, updated_at = $3 WHERE name = $1`,
		slotFeedCursor, evs[len(evs)-1].id, ts(c.DB, c.now()))
	return err
}
