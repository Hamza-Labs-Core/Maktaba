package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// WriteLock is the advisory lock key every admin write of desired state holds in its
// transaction (Postgres): two admins' batches run one after the other, each checked
// against the rows the other left.
const WriteLock = "components_write"

// BatchWrite is one row of a batch and its audit.
type BatchWrite struct {
	Desired Desired
	Audit   Audit
}

// BatchPlan is what one batch writes: its rows (in order), and the bundle when the batch
// applies one (component_meta.bundle is set to it; "" leaves it).
type BatchPlan struct {
	Writes []BatchWrite
	Bundle string
	// AfterWrite runs inside the transaction after each row is written, with the row as
	// it now is (nil: nothing): the API writes the component's older switch there (story
	// 51.07's mirrors), so the two commit or roll back together. An error rolls back the
	// whole batch.
	AfterWrite func(ctx context.Context, tx *sql.Tx, written Desired) error
}

// SetDesiredBatch writes a batch of desired rows in one transaction, or none of them.
// Under the write lock (Postgres: pg_advisory_xact_lock) it reads the home's rows and the
// revision through the transaction and hands them to plan, which checks the changes
// against them and answers the rows to write; an error from plan writes nothing and is
// answered as it is. Each row gets its audit_log row (with the batch id of its Audit) in
// the same transaction. It answers the rows as they now are, in plan order.
func (s *Store) SetDesiredBatch(ctx context.Context, plan func(current map[string]Desired, revision int64) (BatchPlan, error)) ([]Desired, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if !s.sqlite() {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, WriteLock); err != nil {
			return nil, fmt.Errorf("components: write lock: %w", err)
		}
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM component_meta`).Scan(&rev); err != nil {
		return nil, err
	}
	current, err := s.homeRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	p, err := plan(current, rev)
	if err != nil {
		return nil, err
	}
	for _, w := range p.Writes {
		if !validWant(w.Desired.Want) {
			return nil, fmt.Errorf("components: bad want %q", w.Desired.Want)
		}
		if w.Desired.ID == "" {
			return nil, errors.New("components: a desired row needs an id")
		}
		if err := validAudit(w.Audit); err != nil {
			return nil, err
		}
	}
	out := make([]Desired, 0, len(p.Writes))
	for _, w := range p.Writes {
		after, err := s.setDesired(ctx, tx, w.Desired, w.Audit)
		if err != nil {
			return nil, err
		}
		if p.AfterWrite != nil {
			if err := p.AfterWrite(ctx, tx, after); err != nil {
				return nil, err
			}
		}
		out = append(out, after)
	}
	if p.Bundle != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE component_meta SET bundle = $1`, p.Bundle); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}

func (s *Store) homeRows(ctx context.Context, q queryer) (map[string]Desired, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, want, hold_by, hold_at, generation FROM components ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Desired{}
	for rows.Next() {
		var d Desired
		var holdBy sql.NullString
		var holdAt nullTime
		if err := rows.Scan(&d.ID, &d.Want, &holdBy, &holdAt, &d.Generation); err != nil {
			return nil, err
		}
		if holdAt.Valid {
			d.Hold = &Hold{By: holdBy.String, At: holdAt.T}
		}
		out[d.ID] = d
	}
	return out, rows.Err()
}

// AuditRow is one audit_log row of a component.
type AuditRow struct {
	ID          int64           `json:"id"`
	At          time.Time       `json:"at"`
	Action      string          `json:"action"`
	ActorUserID string          `json:"actor_user_id,omitempty"`
	ActorSource string          `json:"actor_source,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// AuditOf is component id's last limit audit rows, newest first.
func (s *Store) AuditOf(ctx context.Context, id string, limit int) ([]AuditRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, action, actor_user_id, actor_source, payload
		FROM audit_log WHERE category = $1 AND target_kind = 'component' AND target_id = $2
		ORDER BY id DESC LIMIT $3`, AuditCategory, id, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []AuditRow{}
	for rows.Next() {
		var a AuditRow
		var at nullTime
		var actor, source sql.NullString
		var payload []byte
		if err := rows.Scan(&a.ID, &at, &a.Action, &actor, &source, &payload); err != nil {
			return nil, err
		}
		a.At, a.ActorUserID, a.ActorSource = at.T, actor.String, source.String
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		a.Payload = json.RawMessage(payload)
		out = append(out, a)
	}
	return out, rows.Err()
}
