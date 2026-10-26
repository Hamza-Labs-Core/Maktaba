// Package sessions stores cloud sessions backed by hashed refresh
// tokens. The cookie/refresh value never lands in the DB raw — we
// store SHA-256 so a DB leak doesn't enable session takeover.
package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// DefaultTTL is the cloud session lifetime: 30 days.
const DefaultTTL = 30 * 24 * time.Hour

var ErrNotFound = errors.New("sessions: not found")

// ErrReplayed is returned by Lookup when the token belongs to a session
// that was already rotated: whoever presents it is replaying a token
// the legitimate client has replaced. The caller revokes every session
// of the account.
var ErrReplayed = errors.New("sessions: rotated refresh token replayed")

// ReplayGrace is how long after a rotation the old token is treated as
// a plain miss rather than a replay. Two tabs refreshing at the same
// moment both present the same token; the loser should get a 401, not
// sign the account out everywhere.
const ReplayGrace = 10 * time.Second

// Revocation reasons stored in sessions.revoke_reason.
const (
	ReasonRotated = "rotated"
	ReasonLogout  = "logout"
	ReasonRevoked = "revoked"
)

// Session is the row shape exposed to handlers.
type Session struct {
	ID        string
	UserID    string
	UserAgent string
	IP        string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
	LastUsed  *time.Time
}

// Store persists sessions. We hash the refresh token at rest and
// return the raw token to the caller exactly once (at Create).
type Store struct {
	DB  *sql.DB
	TTL time.Duration
}

func New(db *sql.DB) *Store {
	return &Store{DB: db, TTL: DefaultTTL}
}

// Create issues a fresh refresh token, stores its hash, and returns the
// session metadata plus the raw token. The token is *only* ever shown
// at this moment.
func (s *Store) Create(ctx context.Context, userID, userAgent, ip string) (Session, string, error) {
	if userID == "" {
		return Session{}, "", errors.New("sessions: user id required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, "", err
	}
	rawToken := base64.RawURLEncoding.EncodeToString(raw)
	hash := hashToken(rawToken)
	expires := time.Now().UTC().Add(s.TTL)
	var sess Session
	err := s.DB.QueryRowContext(ctx, `
        INSERT INTO sessions (user_id, refresh_token_hash, user_agent, ip, expires_at)
        VALUES ($1,$2,$3,NULLIF($4,'')::inet,$5)
        RETURNING id, user_id, COALESCE(user_agent,''), COALESCE(host(ip),''), expires_at, created_at
    `, userID, hash, userAgent, ip, expires).Scan(
		&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP, &sess.ExpiresAt, &sess.CreatedAt,
	)
	if err != nil {
		return Session{}, "", err
	}
	return sess, rawToken, nil
}

// Lookup resolves a raw refresh token to its session row. Returns
// ErrNotFound for missing, expired, or revoked sessions — callers must
// not distinguish these because doing so would leak existence.
func (s *Store) Lookup(ctx context.Context, rawToken string) (Session, error) {
	hash := hashToken(rawToken)
	var sess Session
	var revoked sql.NullTime
	var reason sql.NullString
	err := s.DB.QueryRowContext(ctx, `
        SELECT id, user_id, COALESCE(user_agent,''), COALESCE(host(ip),''),
               expires_at, revoked_at, created_at, revoke_reason
        FROM sessions
        WHERE refresh_token_hash = $1
    `, hash).Scan(&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP,
		&sess.ExpiresAt, &revoked, &sess.CreatedAt, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if revoked.Valid {
		if reason.String == ReasonRotated && time.Since(revoked.Time) > ReplayGrace {
			return Session{UserID: sess.UserID}, ErrReplayed
		}
		return Session{}, ErrNotFound
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return Session{}, ErrNotFound
	}
	return sess, nil
}

// Revoke marks the session inactive (a sign-out). Idempotent.
func (s *Store) Revoke(ctx context.Context, id string) error {
	return s.revoke(ctx, id, ReasonLogout)
}

// Rotate retires a session whose refresh token is being exchanged for
// a new one. It reports false when the session was already revoked —
// a concurrent refresh won the race — so the caller does not mint a
// second live session from one token.
func (s *Store) Rotate(ctx context.Context, id string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at = now(), revoke_reason = $2 WHERE id = $1 AND revoked_at IS NULL`, id, ReasonRotated)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) revoke(ctx context.Context, id, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at = now(), revoke_reason = $2 WHERE id = $1 AND revoked_at IS NULL`, id, reason)
	return err
}

// RevokeForUser revokes one session of the given account. It reports
// false when the id is not a live session of that account, so a caller
// cannot revoke someone else's session by guessing an id.
func (s *Store) RevokeForUser(ctx context.Context, id, userID string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at = now(), revoke_reason = $3 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, userID, ReasonRevoked)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RevokeAllForUser is used by password change, suspension, deletion and
// "sign out everywhere".
func (s *Store) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at = now(), revoke_reason = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, ReasonRevoked)
	return err
}

// ListActive returns the account's live sessions, newest first.
func (s *Store) ListActive(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, user_id, COALESCE(user_agent,''), COALESCE(host(ip),''), expires_at, created_at, last_used_at
        FROM sessions
        WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
        ORDER BY created_at DESC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var sess Session
		var used sql.NullTime
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP, &sess.ExpiresAt, &sess.CreatedAt, &used); err != nil {
			return nil, err
		}
		if used.Valid {
			t := used.Time
			sess.LastUsed = &t
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// CountActive is the number of live sessions an account holds.
func (s *Store) CountActive(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, userID).Scan(&n)
	return n, err
}

// hashToken returns the at-rest representation of a refresh token.
// SHA-256 is fine here — the token already has 256 bits of entropy so
// we don't need PBKDF-style stretching; we just need a one-way map.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
