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
	"strconv"
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

// Signal kinds recorded by the account side, in addition to the ones in
// the package comment.
const (
	KindLoginFailed      = "login_failed"
	KindRefreshReplay    = "refresh_token_replay"
	KindSignup           = "signup"
	KindStripeForgery    = "stripe_signature_forgery"
	KindDispute          = "charge_dispute"
	KindOAuthState       = "oauth_state"
	KindPasswordResetReq = "password_reset_request"
)

// Login lockout (story 25.2): this many failed logins for one account
// inside LockoutWindow lock it for LockoutFor.
const (
	LockoutThreshold = 10
	LockoutWindow    = 15 * time.Minute
	LockoutFor       = 30 * time.Minute
)

// LoginLocked reports whether the account has LockoutThreshold failed
// logins in the last LockoutWindow, and if so how long until the oldest
// of them ages out of the LockoutFor lock.
func (d *Detector) LoginLocked(ctx context.Context, userID string) (bool, time.Duration, error) {
	var n int
	var newest sql.NullTime
	err := d.DB.QueryRowContext(ctx, `
        SELECT count(*), max(created_at) FROM abuse_signals
        WHERE subject_kind = 'user' AND subject = $1 AND kind = $2
          AND created_at > now() - make_interval(secs => $3)
    `, userID, KindLoginFailed, LockoutFor.Seconds()).Scan(&n, &newest)
	if err != nil {
		return false, 0, err
	}
	if n < LockoutThreshold {
		return false, 0, nil
	}
	// Count only the failures that fall inside one LockoutWindow ending
	// at the newest: a slow trickle spread over 30 minutes is not a
	// burst.
	var burst int
	if err := d.DB.QueryRowContext(ctx, `
        SELECT count(*) FROM abuse_signals
        WHERE subject_kind = 'user' AND subject = $1 AND kind = $2
          AND created_at > $3::timestamptz - make_interval(secs => $4)
    `, userID, KindLoginFailed, newest.Time, LockoutWindow.Seconds()).Scan(&burst); err != nil {
		return false, 0, err
	}
	if burst < LockoutThreshold {
		return false, 0, nil
	}
	left := time.Until(newest.Time.Add(LockoutFor))
	if left <= 0 {
		return false, 0, nil
	}
	return true, left, nil
}

// ClearLoginFailures forgets an account's failed logins after a
// successful one, so a user who mistypes a few times a day is never
// locked out by their own history.
func (d *Detector) ClearLoginFailures(ctx context.Context, userID string) error {
	_, err := d.DB.ExecContext(ctx, `DELETE FROM abuse_signals WHERE subject_kind = 'user' AND subject = $1 AND kind = $2`, userID, KindLoginFailed)
	return err
}

// Unblock removes a subject from the blocklist. It reports whether the
// subject was listed.
func (d *Detector) Unblock(ctx context.Context, s Subject) (bool, error) {
	res, err := d.DB.ExecContext(ctx, `DELETE FROM blocklist WHERE subject_kind = $1 AND subject = $2`, s.Kind, s.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Signal is a recorded abuse signal, for the operator console.
type Signal struct {
	ID          int64          `json:"id"`
	SubjectKind string         `json:"subject_kind"`
	Subject     string         `json:"subject"`
	Kind        string         `json:"kind"`
	Severity    int            `json:"severity"`
	Detail      map[string]any `json:"detail,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// RecentSignals lists the newest signals, optionally for one subject.
func (d *Detector) RecentSignals(ctx context.Context, subject *Subject, limit int) ([]Signal, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q := `SELECT id, subject_kind, subject, kind, severity, detail, created_at FROM abuse_signals`
	args := []any{}
	if subject != nil {
		q += ` WHERE subject_kind = $1 AND subject = $2`
		args = append(args, subject.Kind, subject.ID)
	}
	args = append(args, limit)
	q += ` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := d.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Signal{}
	for rows.Next() {
		var s Signal
		var dj []byte
		if err := rows.Scan(&s.ID, &s.SubjectKind, &s.Subject, &s.Kind, &s.Severity, &dj, &s.CreatedAt); err != nil {
			return nil, err
		}
		if len(dj) > 0 {
			_ = json.Unmarshal(dj, &s.Detail)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// BlockEntry is one blocklist row.
type BlockEntry struct {
	SubjectKind string     `json:"subject_kind"`
	Subject     string     `json:"subject"`
	Reason      string     `json:"reason,omitempty"`
	BlockedAt   time.Time  `json:"blocked_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// Blocklist lists the subjects currently blocked (expired rows are
// left out).
func (d *Detector) Blocklist(ctx context.Context) ([]BlockEntry, error) {
	rows, err := d.DB.QueryContext(ctx, `
        SELECT subject_kind, subject, COALESCE(reason,''), blocked_at, expires_at FROM blocklist
        WHERE expires_at IS NULL OR expires_at > now()
        ORDER BY blocked_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlockEntry{}
	for rows.Next() {
		var b BlockEntry
		var exp sql.NullTime
		if err := rows.Scan(&b.SubjectKind, &b.Subject, &b.Reason, &b.BlockedAt, &exp); err != nil {
			return nil, err
		}
		if exp.Valid {
			t := exp.Time
			b.ExpiresAt = &t
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
