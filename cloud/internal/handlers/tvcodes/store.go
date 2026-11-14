package tvcodes

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Row is one code's public facts: what a home is shown before it approves.
type Row struct {
	Code       string
	Platform   string
	AppVersion string
	Country    string
	ExpiresAt  time.Time
}

// PollResult is what a poll found.
type PollResult int

const (
	// PollPending: nobody approved the code yet.
	PollPending PollResult = iota
	// PollSlowDown: polled again sooner than the interval.
	PollSlowDown
	// PollDelivered: approved; the payload is handed over now, and only now.
	PollDelivered
	// PollGone: expired, or already delivered.
	PollGone
	// PollNotFound: no such code, or the wrong poll secret.
	PollNotFound
)

var (
	// ErrExists: the code is taken (a new random code is tried).
	ErrExists = errors.New("tvcodes: code exists")
	// ErrNotFound: no open code (never made, expired, or swept).
	ErrNotFound = errors.New("tvcodes: no such open code")
	// ErrClaimed: another approval claimed the code first.
	ErrClaimed = errors.New("tvcodes: code already claimed")
)

// Store keeps the codes (table tv_codes, migration 00580001).
type Store interface {
	Create(ctx context.Context, r Row, secretHash []byte, now time.Time) error
	// Open is a code that can still be approved: not claimed, not expired.
	Open(ctx context.Context, code string, now time.Time) (Row, error)
	// Claim gives an open code to serverID with its sealed payload.
	Claim(ctx context.Context, code, serverID string, sealed []byte, now time.Time) (Row, error)
	// Poll is the TV's poll: the payload once it is approved (cleared as it is returned).
	Poll(ctx context.Context, code string, secretHash []byte, now time.Time, interval time.Duration) (PollResult, []byte, error)
	// Sweep deletes the rows that expired before cutoff.
	Sweep(ctx context.Context, cutoff time.Time) (int64, error)
}

// SQLStore is Store on Postgres.
type SQLStore struct{ DB *sql.DB }

// Create inserts a new code.
func (s *SQLStore) Create(ctx context.Context, r Row, secretHash []byte, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO tv_codes (code, poll_secret_hash, platform, app_version, client_ip_country, created_at, expires_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.Code, secretHash, r.Platform, r.AppVersion, r.Country, now, r.ExpiresAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return ErrExists
	}
	return err
}

// Open reads an open code.
func (s *SQLStore) Open(ctx context.Context, code string, now time.Time) (Row, error) {
	r := Row{Code: code}
	err := s.DB.QueryRowContext(ctx, `
        SELECT platform, app_version, client_ip_country, expires_at FROM tv_codes
         WHERE code = $1 AND claimed_at IS NULL AND expires_at > $2`, code, now).
		Scan(&r.Platform, &r.AppVersion, &r.Country, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	return r, err
}

// Claim marks an open code claimed by serverID, in one statement: two approvals of one code
// cannot both win.
func (s *SQLStore) Claim(ctx context.Context, code, serverID string, sealed []byte, now time.Time) (Row, error) {
	r := Row{Code: code}
	err := s.DB.QueryRowContext(ctx, `
        UPDATE tv_codes SET server_id = $2, payload_sealed = $3, claimed_at = $4
         WHERE code = $1 AND claimed_at IS NULL AND expires_at > $4
        RETURNING platform, app_version, client_ip_country, expires_at`, code, serverID, sealed, now).
		Scan(&r.Platform, &r.AppVersion, &r.Country, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		var claimed bool
		e := s.DB.QueryRowContext(ctx, `SELECT claimed_at IS NOT NULL FROM tv_codes WHERE code = $1 AND expires_at > $2`,
			code, now).Scan(&claimed)
		if e == nil && claimed {
			return Row{}, ErrClaimed
		}
		return Row{}, ErrNotFound
	}
	return r, err
}

// Poll answers a TV's poll under a row lock: the payload goes out once and is cleared in the
// same transaction.
func (s *SQLStore) Poll(ctx context.Context, code string, secretHash []byte, now time.Time, interval time.Duration) (PollResult, []byte, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var (
		hash      []byte
		expires   time.Time
		claimed   sql.NullTime
		delivered sql.NullTime
		lastPoll  sql.NullTime
		payload   []byte
	)
	err = tx.QueryRowContext(ctx, `
        SELECT poll_secret_hash, expires_at, claimed_at, delivered_at, last_poll_at, payload_sealed
          FROM tv_codes WHERE code = $1 FOR UPDATE`, code).
		Scan(&hash, &expires, &claimed, &delivered, &lastPoll, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return PollNotFound, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	if subtle.ConstantTimeCompare(hash, secretHash) != 1 {
		return PollNotFound, nil, nil
	}
	res := decide(now, interval, expires, claimed.Valid, delivered.Valid, lastPoll, payload != nil)
	switch res {
	case PollDelivered:
		_, err = tx.ExecContext(ctx, `UPDATE tv_codes SET delivered_at = $2, payload_sealed = NULL, last_poll_at = $2 WHERE code = $1`, code, now)
	case PollPending, PollSlowDown:
		_, err = tx.ExecContext(ctx, `UPDATE tv_codes SET last_poll_at = $2 WHERE code = $1`, code, now)
	default:
		return res, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	if res != PollDelivered {
		payload = nil
	}
	return res, payload, nil
}

// decide is a poll's answer from the row (shared with the tests' store): an approved code is
// delivered at once, even to a quick poll, so the TV signs in within one interval.
func decide(now time.Time, interval time.Duration, expires time.Time, claimed, delivered bool, lastPoll sql.NullTime, hasPayload bool) PollResult {
	switch {
	case delivered:
		return PollGone
	case claimed && hasPayload:
		return PollDelivered
	case !now.Before(expires):
		return PollGone
	case lastPoll.Valid && now.Sub(lastPoll.Time) < interval-time.Second:
		return PollSlowDown
	default:
		return PollPending
	}
}

// Sweep deletes rows that expired before cutoff.
func (s *SQLStore) Sweep(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM tv_codes WHERE expires_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
