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

// IssueTx mints a token for userID and purpose in the caller's transaction (the mail
// outbox): the link and the mail that carries it are kept together or not at all.
// The links made before stay good for their own life: asking for another never takes
// one away, so nobody can end the link in somebody's inbox. Redeem ends them all.
func (s *MailTokens) IssueTx(ctx context.Context, tx *sql.Tx, userID, purpose string, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := tx.ExecContext(ctx, `INSERT INTO email_verifications (token_hash, user_id, purpose, expires_at) VALUES ($1,$2,$3,$4)`,
		hashMailToken(tok), userID, purpose, time.Now().UTC().Add(ttl)); err != nil {
		return "", err
	}
	return tok, nil
}

// Redeem consumes a token atomically, with every other one of its account and purpose, and
// returns its user id.
func (s *MailTokens) Redeem(ctx context.Context, tok, purpose string) (string, error) {
	if tok == "" {
		return "", ErrTokenInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	// the account first: two of its links used at once wait for each other here, in one order
	var uid string
	err = tx.QueryRowContext(ctx, `
        SELECT u.id FROM users u JOIN email_verifications e ON e.user_id = u.id
        WHERE e.token_hash = $1 AND e.purpose = $2 FOR NO KEY UPDATE OF u`, hashMailToken(tok), purpose).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrTokenInvalid
	}
	if err != nil {
		return "", err
	}
	res, err := tx.ExecContext(ctx, `
        UPDATE email_verifications SET used_at = now()
        WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()`, hashMailToken(tok), purpose)
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", errors.Join(ErrTokenInvalid, err)
	}
	if _, err := tx.ExecContext(ctx, endSQL, uid, purpose); err != nil {
		return "", err
	}
	return uid, tx.Commit()
}

const endSQL = `UPDATE email_verifications SET used_at = now() WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`

// End ends the account's links of a purpose (a password changed, everything signed out).
func (s *MailTokens) End(ctx context.Context, userID, purpose string) error {
	_, err := s.DB.ExecContext(ctx, endSQL, userID, purpose)
	return err
}

func hashMailToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
