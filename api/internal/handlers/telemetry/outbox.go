package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Kinds of what waits in the outbox.
const (
	KindServer = "server" // the home's daily report (the admin's switch)
	KindApp    = "app"    // an app's batch (that app's user's consent)
	KindForget = "forget" // delete what an app install sent
)

const (
	// MaxAge is how long a row waits for Maktaba Cloud: older, it is dropped
	// unsent (a home without internet keeps nothing for long).
	MaxAge = 7 * 24 * time.Hour
	// MaxRows bounds the outbox: past it, the oldest rows go first.
	MaxRows = 5000
)

// Row is one batch waiting to go.
type Row struct {
	ID        int64
	Kind      string
	InstallID string
	Body      json.RawMessage
	Attempts  int
	CreatedAt time.Time
}

// Store is where batches wait (telemetry_outbox, slot 0161).
type Store interface {
	Add(ctx context.Context, kind, installID string, body any) error
	// Due is up to limit rows whose next attempt is at or before now, oldest first.
	Due(ctx context.Context, now time.Time, limit int) ([]Row, error)
	Sent(ctx context.Context, ids []int64) error
	// Retry puts a row off until next, counting the attempt.
	Retry(ctx context.Context, id int64, next time.Time, reason string) error
	// Prune drops rows older than before, then the oldest past MaxRows.
	Prune(ctx context.Context, before time.Time) (int64, error)
	// DropKind drops every waiting row of a kind (the admin turned the switch off).
	DropKind(ctx context.Context, kind string) error
	// DropInstall drops every waiting row of an install (its app forgot it).
	DropInstall(ctx context.Context, installID string) error
	// Pending counts the waiting rows of a kind and when the oldest was added.
	Pending(ctx context.Context, kind string) (int, *time.Time, error)
}

// PGStore is the Store on the home's database.
type PGStore struct{ DB *sql.DB }

func (s PGStore) Add(ctx context.Context, kind, installID string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO telemetry_outbox (kind, install_id, body) VALUES ($1, $2, $3)`,
		kind, installID, string(raw))
	return err
}

func (s PGStore) Due(ctx context.Context, now time.Time, limit int) ([]Row, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, kind, install_id, body, attempts, created_at
		FROM telemetry_outbox WHERE next_attempt_at <= $1 ORDER BY id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var body []byte
		if err := rows.Scan(&r.ID, &r.Kind, &r.InstallID, &body, &r.Attempts, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Body = body
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s PGStore) Sent(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_outbox WHERE id = $1`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s PGStore) Retry(ctx context.Context, id int64, next time.Time, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE telemetry_outbox
		SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3 WHERE id = $1`, id, next, clip(reason, 200))
	return err
}

func (s PGStore) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_outbox WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	res, err = s.DB.ExecContext(ctx, `DELETE FROM telemetry_outbox WHERE id IN (
		SELECT id FROM telemetry_outbox ORDER BY id DESC OFFSET $1)`, MaxRows)
	if err != nil {
		return n, err
	}
	m, _ := res.RowsAffected()
	return n + m, nil
}

func (s PGStore) DropKind(ctx context.Context, kind string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_outbox WHERE kind = $1`, kind)
	return err
}

func (s PGStore) DropInstall(ctx context.Context, installID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_outbox WHERE install_id = $1 AND kind <> $2`,
		installID, KindForget)
	return err
}

func (s PGStore) Pending(ctx context.Context, kind string) (int, *time.Time, error) {
	var n int
	var oldest sql.NullTime
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*), min(created_at) FROM telemetry_outbox WHERE kind = $1`,
		kind).Scan(&n, &oldest); err != nil {
		return 0, nil, err
	}
	if !oldest.Valid {
		return n, nil, nil
	}
	t := oldest.Time.UTC()
	return n, &t, nil
}
