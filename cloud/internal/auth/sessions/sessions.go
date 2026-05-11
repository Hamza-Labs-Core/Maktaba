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

// Session is the row shape exposed to handlers.
type Session struct {
	ID         string
	UserID     string
	UserAgent  string
	IP         string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
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
        RETURNING id, user_id, COALESCE(user_agent,''), COALESCE(ip::text,''), expires_at, created_at
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
	err := s.DB.QueryRowContext(ctx, `
        SELECT id, user_id, COALESCE(user_agent,''), COALESCE(ip::text,''),
               expires_at, revoked_at, created_at
        FROM sessions
        WHERE refresh_token_hash = $1
    `, hash).Scan(&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP,
		&sess.ExpiresAt, &revoked, &sess.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if revoked.Valid {
		t := revoked.Time
		sess.RevokedAt = &t
		return Session{}, ErrNotFound
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return Session{}, ErrNotFound
	}
	return sess, nil
}

// Revoke marks the session inactive. Idempotent.
func (s *Store) Revoke(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// RevokeAllForUser is used by password change and "sign out everywhere".
func (s *Store) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// hashToken returns the at-rest representation of a refresh token.
// SHA-256 is fine here — the token already has 256 bits of entropy so
// we don't need PBKDF-style stretching; we just need a one-way map.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
