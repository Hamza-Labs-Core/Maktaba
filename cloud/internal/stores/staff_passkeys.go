package stores

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
)

// Passkeys (Epic 50 story 50.33 part 2): staff_passkeys and staff_webauthn_challenges. The
// credential is kept as go-webauthn's JSON record (Credential, below), which the staffauth
// package encodes and decodes; the store only keeps it and the columns the console lists.

// StaffPasskey is a staff_passkeys row.
type StaffPasskey struct {
	ID           string
	StaffID      string
	CredentialID []byte
	SignCount    uint32
	Name         string
	// Credential is the whole WebAuthn credential record (JSON).
	Credential []byte
	Disabled   bool
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
}

// The ceremonies a challenge belongs to.
const (
	WebAuthnRegister = "register"
	WebAuthnLogin    = "login"
	WebAuthnReauth   = "reauth"
)

// ErrStaffPasskeyExists is a credential id already registered (to any account).
var ErrStaffPasskeyExists = errors.New("stores: passkey already registered")

// AddPasskey stores a new passkey for staffID.
func (s *Staff) AddPasskey(ctx context.Context, staffID string, credentialID, publicKey []byte, signCount uint32, name string, credential []byte) (StaffPasskey, error) {
	p := StaffPasskey{StaffID: staffID, CredentialID: credentialID, SignCount: signCount, Name: name, Credential: credential}
	err := s.DB.QueryRowContext(ctx, `INSERT INTO staff_passkeys (staff_id, credential_id, public_key, sign_count, name, credential)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		staffID, credentialID, publicKey, int64(signCount), name, credential).Scan(&p.ID, &p.CreatedAt)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return p, ErrStaffPasskeyExists
	}
	return p, err
}

// Passkeys are staffID's passkeys, the disabled ones only when withDisabled.
func (s *Staff) Passkeys(ctx context.Context, staffID string, withDisabled bool) ([]StaffPasskey, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, staff_id, credential_id, sign_count, name, credential,
		disabled_at IS NOT NULL, created_at, last_used_at
		FROM staff_passkeys WHERE staff_id = $1 AND ($2 OR disabled_at IS NULL) ORDER BY created_at, id`, staffID, withDisabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StaffPasskey
	for rows.Next() {
		var p StaffPasskey
		var count int64
		if err := rows.Scan(&p.ID, &p.StaffID, &p.CredentialID, &count, &p.Name, &p.Credential,
			&p.Disabled, &p.CreatedAt, &p.LastUsedAt); err != nil {
			return nil, err
		}
		p.SignCount = uint32(count) //nolint:gosec // stored from a uint32
		out = append(out, p)
	}
	return out, rows.Err()
}

// UsePasskey records a good assertion: the new counter and record, and when it was used. It
// changes only an enabled passkey of staffID.
func (s *Staff) UsePasskey(ctx context.Context, staffID string, credentialID []byte, signCount uint32, credential []byte, now time.Time) error {
	return s.exec1(ctx, `UPDATE staff_passkeys SET sign_count = $3, credential = $4, last_used_at = $5
		WHERE staff_id = $1 AND credential_id = $2 AND disabled_at IS NULL`,
		staffID, credentialID, int64(signCount), credential, now)
}

// DisablePasskey turns a passkey off (§2: its counter went backwards, so it may be a copy).
func (s *Staff) DisablePasskey(ctx context.Context, staffID string, credentialID []byte, now time.Time) error {
	return s.exec1(ctx, `UPDATE staff_passkeys SET disabled_at = $3
		WHERE staff_id = $1 AND credential_id = $2 AND disabled_at IS NULL`, staffID, credentialID, now)
}

// PutChallenge starts a ceremony for staffID: it replaces any one of the same purpose.
func (s *Staff) PutChallenge(ctx context.Context, staffID, purpose string, session []byte, expires time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO staff_webauthn_challenges (staff_id, purpose, session, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (staff_id, purpose) DO UPDATE SET session = EXCLUDED.session, expires_at = EXCLUDED.expires_at,
			created_at = now()`, staffID, purpose, session, expires)
	return err
}

// TakeChallenge ends a ceremony: it deletes the challenge and returns it if it was still live,
// so an answer is checked against a challenge at most once.
func (s *Staff) TakeChallenge(ctx context.Context, staffID, purpose string, now time.Time) ([]byte, error) {
	var session []byte
	var expires time.Time
	err := s.DB.QueryRowContext(ctx, `DELETE FROM staff_webauthn_challenges WHERE staff_id = $1 AND purpose = $2
		RETURNING session, expires_at`, staffID, purpose).Scan(&session, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !now.Before(expires)) {
		return nil, ErrStaffTokenGone
	}
	return session, err
}

// MFATokenAccount is the account of a live mfa_token from ip, without spending an attempt:
// a passkey's first step only draws a challenge, and its answer takes the attempt.
func (s *Staff) MFATokenAccount(ctx context.Context, tokenHash, ip string, now time.Time) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT staff_id FROM staff_mfa_tokens
		WHERE token_hash = $1 AND ip = $2 AND expires_at > $3 AND attempts < $4`,
		tokenHash, ip, now, staffauth.MFAMaxAttempts).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrStaffTokenGone
	}
	return id, err
}
