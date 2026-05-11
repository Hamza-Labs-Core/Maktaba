package log

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Story 21.6 — audit log writer. The canonical schema is in slot 0054.
// Two surfaces:
//
//   - AuditWriter — the production write-through-Postgres path.
//   - NoopAudit   — the test/dev double; captures rows in memory so a
//     test can assert "we emitted an `auth.login.ok` row".
//
// Categories are the canonical enum from slot 0054. Adding a new
// category requires extending the CHECK constraint there.

// AuditCategory is the table CHECK enum.
type AuditCategory string

const (
	AuditAuth         AuditCategory = "auth"
	AuditLibrary      AuditCategory = "library"
	AuditAdmin        AuditCategory = "admin"
	AuditData         AuditCategory = "data"
	AuditConfig       AuditCategory = "config"
	AuditKeys         AuditCategory = "keys"
	AuditDevice       AuditCategory = "device"
	AuditSecurity     AuditCategory = "security"
	AuditIntegrity    AuditCategory = "integrity"
	AuditSubscription AuditCategory = "subscription"
)

// AuditEvent is one row.
type AuditEvent struct {
	ID          string
	OccurredAt  time.Time
	Category    AuditCategory
	Action      string
	ActorUser   string // empty → NULL
	ActorIP     string // "" → NULL; we leave network parsing to the DB
	ActorSource string // "jwt"|"cookie"|"admin_token"|""
	TargetKind  string
	TargetID    string
	Payload     map[string]any
	ErrorID     string
}

// AuditWriter is the interface every emitter calls. Production uses
// PostgresAuditWriter; tests use NoopAudit.
type AuditWriter interface {
	Write(ctx context.Context, ev AuditEvent) error
}

// PostgresAuditWriter writes to the audit_log table created in slot
// 0036 and extended in slot 0054. The id column is BIGSERIAL so the
// writer omits it from the INSERT and lets the database assign it.
type PostgresAuditWriter struct {
	DB *sql.DB
}

// Write inserts a single row.
func (w *PostgresAuditWriter) Write(ctx context.Context, ev AuditEvent) error {
	if w == nil || w.DB == nil {
		return errors.New("audit: PostgresAuditWriter not configured")
	}
	if ev.Category == "" {
		return errors.New("audit: category required")
	}
	if ev.Action == "" {
		return errors.New("audit: action required")
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	payload := ev.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("audit: marshal payload: %w", err)
	}
	_, err = w.DB.ExecContext(ctx, `
		INSERT INTO audit_log
		    (category, action, actor_user_id, actor_ip,
		     actor_source, target_kind, target_id, payload, error_id,
		     ts, occurred_at)
		VALUES ($1,$2,
		    NULLIF($3,'')::uuid,
		    NULLIF($4,'')::inet,
		    NULLIF($5,''),
		    NULLIF($6,''), NULLIF($7,''), $8::jsonb, NULLIF($9,''),
		    $10, $10)
	`, ev.Category, ev.Action,
		ev.ActorUser, ev.ActorIP, ev.ActorSource,
		ev.TargetKind, ev.TargetID, string(pb), ev.ErrorID,
		ev.OccurredAt)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}

// NoopAudit captures events in memory for tests.
type NoopAudit struct {
	events []AuditEvent
}

// Write appends.
func (n *NoopAudit) Write(_ context.Context, ev AuditEvent) error {
	n.events = append(n.events, ev)
	return nil
}

// Events returns a copy of the captured events.
func (n *NoopAudit) Events() []AuditEvent {
	out := make([]AuditEvent, len(n.events))
	copy(out, n.events)
	return out
}

// CountByAction returns the number of captured events with the given
// (category, action) pair.
func (n *NoopAudit) CountByAction(cat AuditCategory, action string) int {
	c := 0
	for _, e := range n.events {
		if e.Category == cat && e.Action == action {
			c++
		}
	}
	return c
}
