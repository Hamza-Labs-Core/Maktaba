// Package securityaudit implements Story 10.16's vocabulary and
// write/read paths over the canonical `audit_log` table (slot 0036).
//
// Rows for security events are stored with `category='security'` and
// one of the canonical Event values. The payload (`payload_jsonb`)
// carries event-specific detail.
//
// Read side: ListRecent returns newest-first rows for the admin
// `/api/security/audit` endpoint.
//
// Dedupe: per Story 10.16 EC-1, high-volume events sample to at most
// one row per minute per (event, actor) — applied via a small
// in-memory map (no DB round-trips just to decide to skip).
package securityaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Event is the canonical vocabulary defined in Story 10.16 AC-1. Any
// caller that wants to log a new event MUST add the const here so the
// vocabulary is auditable from one place.
type Event string

const (
	EventLoginSuccess      Event = "login.success"
	EventLoginFailed       Event = "login.failed"
	EventLogout            Event = "logout"
	EventLogoutAll         Event = "logout-all"
	EventLockoutUsername   Event = "lockout-username"
	EventLockoutIP         Event = "lockout-ip"
	EventRefreshReplay     Event = "refresh.replay-detected"
	EventPasswordChanged   Event = "password.changed"
	EventKeyRotated        Event = "key.rotated"
	EventAdminTokenUsed    Event = "admin-token.used"
	EventPermissionDenied  Event = "permission.denied"
	EventStreamingDirect   Event = "streaming.direct.access"
	EventPairCodeIssued    Event = "pair.code-issued"
	EventPairCodeClaimed   Event = "pair.code-claimed"
	EventSessionRevoked    Event = "session.revoked"
	EventRefreshRevoked    Event = "refresh.revoked"
)

// Category is the audit_log.category value for every security event.
const Category = "security"

// Entry is the input to Write. ActorUserID may be empty (login.failed
// with an unknown user — store the row anyway). TargetID is the
// natural id of the thing acted on (session_id, user_id, family_id).
type Entry struct {
	Event       Event
	ActorUserID string
	TargetID    string
	Payload     map[string]any
}

// ListEntry is one row of ListRecent's result, JSON-friendly for the
// admin endpoint.
type ListEntry struct {
	ID          int64           `json:"id"`
	Event       Event           `json:"event"`
	ActorUserID string          `json:"actor_user_id,omitempty"`
	TargetID    string          `json:"target_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Timestamp   time.Time       `json:"ts"`
}

// Writer wraps a *sql.DB. The in-memory dedupe map is keyed on
// `<event>|<actor>` so a single hot loop doesn't fill the audit log.
type Writer struct {
	DB *sql.DB

	mu   sync.Mutex
	last map[string]time.Time

	// writeFn lets tests stub the DB write. Production code leaves it
	// nil; Write falls back to the *sql.DB path then.
	writeFn func(context.Context, Entry) error
}

// NewWriter returns a Writer bound to db.
func NewWriter(db *sql.DB) *Writer {
	return &Writer{DB: db, last: map[string]time.Time{}}
}

// Write inserts a `category='security'` row carrying e. Errors from
// the underlying DB are returned — but callers SHOULD log-and-continue
// rather than fail the surrounding request: an audit miss is less
// dangerous than a 500 on a login.
func (w *Writer) Write(ctx context.Context, e Entry) error {
	if e.Event == "" {
		return errors.New("securityaudit: empty event")
	}
	if w.writeFn != nil {
		return w.writeFn(ctx, e)
	}
	payload, err := marshalPayload(e.Payload)
	if err != nil {
		return err
	}
	var actor, target sql.NullString
	if e.ActorUserID != "" {
		actor = sql.NullString{Valid: true, String: e.ActorUserID}
	}
	if e.TargetID != "" {
		target = sql.NullString{Valid: true, String: e.TargetID}
	}
	const q = `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
	             VALUES ($1, $2, $3, $4, $5)`
	_, err = w.DB.ExecContext(ctx, q, Category, string(e.Event), actor, target, payload)
	return err
}

// WriteSampled is a debounced Write: emits at most one row per
// (event, actor) per minute. Used for admin-token use and other
// high-volume events. The first call always emits.
//
// Returns (emitted, err). emitted=false means the call was suppressed
// by the in-memory dedupe.
func (w *Writer) WriteSampled(ctx context.Context, e Entry, window time.Duration) (bool, error) {
	if window <= 0 {
		return true, w.Write(ctx, e)
	}
	key := string(e.Event) + "|" + e.ActorUserID
	now := time.Now()
	w.mu.Lock()
	last, seen := w.last[key]
	if seen && now.Sub(last) < window {
		w.mu.Unlock()
		return false, nil
	}
	w.last[key] = now
	w.mu.Unlock()
	return true, w.Write(ctx, e)
}

// ListRecent returns up to `limit` newest security-category rows older
// than `cursor` (omit by passing the zero time). For pagination,
// callers pass the oldest row's ts as the next cursor.
func (w *Writer) ListRecent(ctx context.Context, cursor time.Time, limit int) ([]ListEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, action, actor_user_id, target_id, payload, ts
	        FROM audit_log
	        WHERE category = $1`
	args := []any{Category}
	if !cursor.IsZero() {
		q += ` AND ts < $2`
		args = append(args, cursor)
		q += ` ORDER BY ts DESC LIMIT $3`
		args = append(args, limit)
	} else {
		q += ` ORDER BY ts DESC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := w.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListEntry
	for rows.Next() {
		var (
			le      ListEntry
			actor   sql.NullString
			target  sql.NullString
			payload sql.NullString
			action  string
		)
		if err := rows.Scan(&le.ID, &action, &actor, &target, &payload, &le.Timestamp); err != nil {
			return nil, err
		}
		le.Event = Event(action)
		if actor.Valid {
			le.ActorUserID = actor.String
		}
		if target.Valid {
			le.TargetID = target.String
		}
		if payload.Valid && payload.String != "" {
			le.Payload = json.RawMessage(payload.String)
		}
		out = append(out, le)
	}
	return out, rows.Err()
}

// marshalPayload normalises the payload into a JSON-encoded byte
// slice (for parameter binding), defaulting to `{}` for an empty map.
func marshalPayload(p map[string]any) (string, error) {
	if len(p) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	// Defensive: refuse a payload bigger than 16 KiB. Audit rows are
	// meant for short structured detail, not large blobs.
	if len(b) > 16*1024 {
		return "", errors.New("securityaudit: payload too large")
	}
	return string(b), nil
}

// EventsAreCanonical reports whether the given string is a registered
// Event. Used by tests that want to assert no typo'd events leak in.
func EventsAreCanonical(s string) bool {
	switch Event(strings.TrimSpace(s)) {
	case EventLoginSuccess, EventLoginFailed, EventLogout, EventLogoutAll,
		EventLockoutUsername, EventLockoutIP, EventRefreshReplay,
		EventPasswordChanged, EventKeyRotated, EventAdminTokenUsed,
		EventPermissionDenied, EventStreamingDirect,
		EventPairCodeIssued, EventPairCodeClaimed,
		EventSessionRevoked, EventRefreshRevoked:
		return true
	}
	return false
}
