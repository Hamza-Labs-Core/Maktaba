// Package passwordreset implements the persistence for the
// forgot-password / reset-password flow (web-pages-batch2, slot 0070).
//
// Token model:
//
//	the plaintext is 32 bytes of url-safe random, returned once to the
//	caller (which emails it). Only SHA-256(plaintext) is stored, so a
//	database read can't reconstruct a live reset link. The reset
//	endpoint recomputes the hash to find the row — SHA-256 (not
//	argon2id) is correct here because the lookup is BY hash and the
//	token already carries 256 bits of entropy.
//
// Single-use + time-boxed: Consume marks `used_at` inside the same
// statement that selects the row (UPDATE ... RETURNING) so a token
// can't be redeemed twice even under a race.
package passwordreset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// SecretLen is the raw entropy of the reset token, in bytes.
const SecretLen = 32

// DefaultTTL is how long a reset token stays valid (1 hour).
const DefaultTTL = time.Hour

// ErrInvalid is returned by Consume when the token doesn't match a
// live (unused, unexpired) row. Handlers map this to 400 with a generic
// message — never reveal whether the token existed.
var ErrInvalid = errors.New("password reset token invalid or expired")

// Store wraps SQL access for `password_reset_tokens`.
type Store struct {
	DB  *sql.DB
	TTL time.Duration
}

// New returns a Store with the default TTL.
func New(db *sql.DB) *Store {
	return &Store{DB: db, TTL: DefaultTTL}
}

// Create mints a reset token for userID and persists its hash. Returns
// the plaintext token the caller must deliver out-of-band (email).
func (s *Store) Create(ctx context.Context, userID string) (string, error) {
	buf := make([]byte, SecretLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	plaintext := base64.RawURLEncoding.EncodeToString(buf)
	ttl := s.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	now := time.Now().UTC()
	const q = `INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at)
	             VALUES ($1, $2, $3, $4, $5)`
	if _, err := s.DB.ExecContext(ctx, q,
		uuid.NewString(), userID, hashToken(plaintext), now.Add(ttl), now,
	); err != nil {
		return "", err
	}
	return plaintext, nil
}

// Consume validates the plaintext token and atomically marks it used,
// returning the owning user id. ErrInvalid covers missing / already-used
// / expired tokens with no oracle for the caller.
func (s *Store) Consume(ctx context.Context, plaintext string) (string, error) {
	if plaintext == "" {
		return "", ErrInvalid
	}
	const q = `UPDATE password_reset_tokens
	             SET used_at = now()
	             WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
	           RETURNING user_id`
	var userID string
	err := s.DB.QueryRowContext(ctx, q, hashToken(plaintext)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalid
	}
	if err != nil {
		return "", err
	}
	return userID, nil
}

// hashToken returns the hex SHA-256 of the plaintext token — the value
// stored in `token_hash` and recomputed on Consume.
func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
