package stores

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

// Purposes of the single-use mail tokens in email_verifications.
const (
	PurposeVerifyEmail   = "verify_email"
	PurposePasswordReset = "password_reset"
)

// Lifetimes of the mail tokens (stories 25.2: 24 h verification;
// password reset is shorter because it hands over the account).
const (
	VerifyEmailTTL   = 24 * time.Hour
	PasswordResetTTL = time.Hour
)

// ErrTokenInvalid covers an unknown, used, expired or wrong-purpose
// token; callers must not say which.
var ErrTokenInvalid = errors.New("tokens: invalid or expired")

// MailTokens issues and redeems the links mailed to users. Only the
// SHA-256 of a token is stored.
type MailTokens struct {
	DB *sql.DB
}

func NewMailTokens(db *sql.DB) *MailTokens { return &MailTokens{DB: db} }

// Issue mints a token for userID and purpose, retiring any earlier
// unused token of the same purpose so only the newest link works.
func (s *MailTokens) Issue(ctx context.Context, userID, purpose string, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE email_verifications SET used_at = now() WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`, userID, purpose); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO email_verifications (token_hash, user_id, purpose, expires_at) VALUES ($1,$2,$3,$4)`,
		hashMailToken(tok), userID, purpose, time.Now().UTC().Add(ttl)); err != nil {
		return "", err
	}
	return tok, tx.Commit()
}

// Redeem consumes a token atomically and returns its user id.
func (s *MailTokens) Redeem(ctx context.Context, tok, purpose string) (string, error) {
	if tok == "" {
		return "", ErrTokenInvalid
	}
	var uid string
	err := s.DB.QueryRowContext(ctx, `
        UPDATE email_verifications SET used_at = now()
        WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
        RETURNING user_id
    `, hashMailToken(tok), purpose).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrTokenInvalid
	}
	return uid, err
}

func hashMailToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
