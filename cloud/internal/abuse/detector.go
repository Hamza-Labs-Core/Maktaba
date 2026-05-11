// Package abuse records suspicious activity signals and consults the
// blocklist before serving sensitive operations.
//
// Signal kinds we collect (story 25.25):
//   - login_failed:   wrong password
//   - claim_brute:    too many wrong claim codes from one IP
//   - relay_excess:   server blew its bandwidth tier
//   - oauth_state:    bad state cookie on callback (CSRF attempt)
package abuse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Detector is the persistence + decision layer.
type Detector struct {
	DB *sql.DB
}

func New(db *sql.DB) *Detector { return &Detector{DB: db} }

// Subject identifies the entity being scored.
type Subject struct {
	Kind string // "user" | "server" | "ip"
	ID   string
}

// Record persists a signal. Severity is 1-5; the abuse thresholds are
// crossed at 10 cumulative units in 1h or 50 in 24h.
func (d *Detector) Record(ctx context.Context, s Subject, kind string, severity int, detail map[string]any) error {
	dj, _ := json.Marshal(detail)
	_, err := d.DB.ExecContext(ctx, `
        INSERT INTO abuse_signals (subject, subject_kind, kind, severity, detail)
        VALUES ($1,$2,$3,$4,$5)
    `, s.ID, s.Kind, kind, severity, dj)
	return err
}

// ShouldBlock returns true when cumulative severity exceeds the
// rolling thresholds. We also surface the reason for logging.
func (d *Detector) ShouldBlock(ctx context.Context, s Subject) (bool, string, error) {
	var h1, h24 int
	err := d.DB.QueryRowContext(ctx, `
        SELECT
            COALESCE(SUM(severity) FILTER (WHERE created_at > now() - INTERVAL '1 hour'), 0),
            COALESCE(SUM(severity) FILTER (WHERE created_at > now() - INTERVAL '24 hours'), 0)
        FROM abuse_signals WHERE subject_kind = $1 AND subject = $2
    `, s.Kind, s.ID).Scan(&h1, &h24)
	if err != nil {
		return false, "", err
	}
	if h1 >= 10 {
		return true, "rolling 1h severity >= 10", nil
	}
	if h24 >= 50 {
		return true, "rolling 24h severity >= 50", nil
	}
	return false, "", nil
}

// Block adds the subject to the blocklist with optional expiry.
func (d *Detector) Block(ctx context.Context, s Subject, reason string, expiresAt *time.Time) error {
	var exp sql.NullTime
	if expiresAt != nil {
		exp = sql.NullTime{Time: *expiresAt, Valid: true}
	}
	_, err := d.DB.ExecContext(ctx, `
        INSERT INTO blocklist (subject, subject_kind, reason, expires_at)
        VALUES ($1,$2,$3,$4)
        ON CONFLICT (subject_kind, subject) DO UPDATE SET
            reason = EXCLUDED.reason,
            expires_at = EXCLUDED.expires_at
    `, s.ID, s.Kind, reason, exp)
	return err
}

// IsBlocked checks both the persisted list and the expiry.
func (d *Detector) IsBlocked(ctx context.Context, s Subject) (bool, error) {
	var exp sql.NullTime
	err := d.DB.QueryRowContext(ctx, `SELECT expires_at FROM blocklist WHERE subject_kind = $1 AND subject = $2`, s.Kind, s.ID).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if exp.Valid && time.Now().After(exp.Time) {
		return false, nil
	}
	return true, nil
}
