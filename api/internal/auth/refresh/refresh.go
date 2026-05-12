// Package refresh implements Stories 10.3 (issue) and 10.4 (rotate /
// detect reuse) for opaque refresh tokens.
//
// Token format (plan-10-03 §5):
//
//	mkt_rt_v1.<row-uuid>.<32-bytes-base64url>
//
// The row id is a constant-time DB lookup. Only the secret half is
// argon2id-hashed and stored; the plaintext is returned exactly once
// at issue time.
//
// Family rotation:
//
//   - On Issue: insert a row with a fresh family_id (new login) or the
//     prior chain's family_id (rotate). Old row is marked
//     `revoked_at=now(), replaced_by=<new id>`.
//   - On reuse (Verify with an already-revoked row): every active
//     sibling in the same family_id is revoked. The caller writes an
//     audit row with event=`refresh.replay-detected`.
package refresh

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
)

// TokenPrefix identifies a v1 opaque refresh token. The version bump
// is reserved for a future migration; v1 is the only currently issued
// form.
const TokenPrefix = "mkt_rt_v1."

// SecretLen is the raw entropy of the secret half, in bytes.
const SecretLen = 32

// DefaultTTL is the canonical refresh token lifetime (60 days).
const DefaultTTL = 60 * 24 * time.Hour

// Errors exported by Store.
var (
	// ErrNotFound — no row matched the parsed id, or the token doesn't
	// parse. Handlers map to 401 `type: refresh-invalid`.
	ErrNotFound = errors.New("refresh token not found")

	// ErrExpired — row was found but its expires_at is past. Maps to
	// 401 `type: refresh-expired`.
	ErrExpired = errors.New("refresh token expired")

	// ErrRevoked — row is revoked (not because of rotation, just
	// directly revoked, e.g. by logout). Maps to 401
	// `type: refresh-revoked`.
	ErrRevoked = errors.New("refresh token revoked")

	// ErrReplay — row is revoked AND another row replaced it. This is
	// the theft-detection signal: the caller MUST revoke the entire
	// family and emit a `refresh.replay-detected` audit event.
	ErrReplay = errors.New("refresh token replayed (rotated already)")

	// ErrMalformed — the token didn't start with TokenPrefix or didn't
	// have the right number of dot-separated parts.
	ErrMalformed = errors.New("refresh token malformed")
)

// Token is the verified shape returned by Verify / Issue. `Plaintext`
// is only populated by Issue at create time.
type Token struct {
	ID         string
	UserID     string
	FamilyID   string
	DeviceID   *string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *string
	ClientMeta map[string]any

	// Plaintext is the full `mkt_rt_v1.<id>.<secret>` string. Populated
	// only by Issue — the caller MUST surface it to the user once and
	// then drop it.
	Plaintext string
}

// IssueInput is the public shape for Issue.
type IssueInput struct {
	UserID     string
	FamilyID   string         // empty ⇒ new family (fresh login)
	DeviceID   string         // empty ⇒ no device link
	ClientMeta map[string]any // attached for audit
	TTL        time.Duration  // 0 ⇒ DefaultTTL
}

// Store wraps SQL access for `refresh_tokens`.
type Store struct {
	DB     *sql.DB
	Params argon2id.Params
	TTL    time.Duration
}

// New returns a Store with the canonical argon2id params and TTL.
// argon2 cost is *not* free here: every refresh round trips one Hash +
// one Verify. We keep params at the defaults; tune via Params if
// throughput becomes a concern.
func New(db *sql.DB) *Store {
	return &Store{DB: db, Params: argon2id.DefaultParams(), TTL: DefaultTTL}
}

// Issue creates a fresh refresh row and returns Token.Plaintext.
//
// FamilyID semantics:
//   - "" → mint a new UUID and treat this as a fresh login.
//   - non-empty → inherit (rotation). Caller (Rotate) typically passes
//     the prior chain's family_id.
func (s *Store) Issue(ctx context.Context, in IssueInput) (*Token, error) {
	secret, err := randomBytes(SecretLen)
	if err != nil {
		return nil, err
	}
	secretStr := base64.RawURLEncoding.EncodeToString(secret)
	hash, err := argon2id.Hash(secretStr, s.Params)
	if err != nil {
		return nil, err
	}
	rowID := uuid.NewString()
	familyID := in.FamilyID
	if familyID == "" {
		familyID = uuid.NewString()
	}
	ttl := in.TTL
	if ttl == 0 {
		ttl = s.TTL
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)

	var meta []byte
	if len(in.ClientMeta) > 0 {
		meta, err = json.Marshal(in.ClientMeta)
		if err != nil {
			return nil, err
		}
	}
	if meta == nil {
		meta = []byte("{}")
	}

	var (
		devArg sql.NullString
	)
	if in.DeviceID != "" {
		devArg = sql.NullString{Valid: true, String: in.DeviceID}
	}

	const q = `INSERT INTO refresh_tokens
	             (id, user_id, hash, family_id, device_id, issued_at, expires_at, client_meta)
	             VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	if _, err := s.DB.ExecContext(ctx, q,
		rowID, in.UserID, hash, familyID, devArg, now, expires, string(meta),
	); err != nil {
		return nil, err
	}

	t := &Token{
		ID:         rowID,
		UserID:     in.UserID,
		FamilyID:   familyID,
		IssuedAt:   now,
		ExpiresAt:  expires,
		ClientMeta: in.ClientMeta,
		Plaintext:  TokenPrefix + rowID + "." + secretStr,
	}
	if devArg.Valid {
		v := devArg.String
		t.DeviceID = &v
	}
	return t, nil
}

// Verify parses the plaintext token, looks up the row, and verifies
// the secret half. Returns the row (without Plaintext) on success.
//
// Errors map to distinct 401 types so handlers can return precise
// `type:` values — but ErrReplay is the only one that triggers
// family-wide revocation.
func (s *Store) Verify(ctx context.Context, plaintext string) (*Token, error) {
	id, secret, err := parseToken(plaintext)
	if err != nil {
		return nil, err
	}
	const q = `SELECT id, user_id, hash, family_id, device_id, issued_at, expires_at, revoked_at, replaced_by, client_meta
	             FROM refresh_tokens WHERE id = $1`
	row := s.DB.QueryRowContext(ctx, q, id)
	t, hash, err := scanWithHash(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Verify the secret half. This is the constant-time check — a row
	// id leak alone doesn't grant a session.
	if err := argon2id.Verify(secret, hash); err != nil {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	if t.RevokedAt != nil {
		if t.ReplacedBy != nil {
			return t, ErrReplay
		}
		return t, ErrRevoked
	}
	if !t.ExpiresAt.After(now) {
		return t, ErrExpired
	}
	return t, nil
}

// Rotate verifies `plaintext`, revokes the matching row, and issues a
// fresh row in the same family. The returned Token carries the new
// plaintext. If Verify returns ErrReplay, Rotate revokes the entire
// family and returns ErrReplay; the caller logs the audit event.
func (s *Store) Rotate(ctx context.Context, plaintext string, meta map[string]any) (*Token, error) {
	old, err := s.Verify(ctx, plaintext)
	if errors.Is(err, ErrReplay) {
		// Theft signal — revoke entire family.
		_ = s.RevokeFamily(ctx, old.FamilyID)
		return old, ErrReplay
	}
	if err != nil {
		return nil, err
	}

	// Issue the new row first so we can stamp `replaced_by`.
	next, err := s.Issue(ctx, IssueInput{
		UserID:     old.UserID,
		FamilyID:   old.FamilyID,
		DeviceID:   derefStr(old.DeviceID),
		ClientMeta: meta,
	})
	if err != nil {
		return nil, err
	}
	const q = `UPDATE refresh_tokens
	             SET revoked_at = now(), replaced_by = $2
	             WHERE id = $1 AND revoked_at IS NULL`
	if _, err := s.DB.ExecContext(ctx, q, old.ID, next.ID); err != nil {
		return nil, err
	}
	return next, nil
}

// RevokeByPlaintext revokes the matching row. Used by native logout
// (Story 10.5 AC-2). Idempotent.
func (s *Store) RevokeByPlaintext(ctx context.Context, plaintext string) error {
	id, _, err := parseToken(plaintext)
	if err != nil {
		return err
	}
	return s.RevokeByID(ctx, id)
}

// RevokeByID revokes a single row by id. Idempotent.
func (s *Store) RevokeByID(ctx context.Context, id string) error {
	const q = `UPDATE refresh_tokens
	             SET revoked_at = now()
	             WHERE id = $1 AND revoked_at IS NULL`
	_, err := s.DB.ExecContext(ctx, q, id)
	return err
}

// RevokeFamily revokes every active row sharing a family_id. Used by
// reuse detection (Story 10.4 AC-2) and by the admin "revoke device"
// flow.
func (s *Store) RevokeFamily(ctx context.Context, familyID string) error {
	const q = `UPDATE refresh_tokens
	             SET revoked_at = now()
	             WHERE family_id = $1 AND revoked_at IS NULL`
	_, err := s.DB.ExecContext(ctx, q, familyID)
	return err
}

// RevokeAllForUser revokes every active refresh row for a user. Used
// by logout-all (Story 10.5 AC-3).
func (s *Store) RevokeAllForUser(ctx context.Context, userID string) (int64, error) {
	const q = `UPDATE refresh_tokens
	             SET revoked_at = now()
	             WHERE user_id = $1 AND revoked_at IS NULL`
	res, err := s.DB.ExecContext(ctx, q, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListFamiliesForUser returns the distinct active family ids for a
// user, with the most-recent issued_at per family. Used by the admin
// "list devices" surface.
func (s *Store) ListFamiliesForUser(ctx context.Context, userID string) ([]FamilySummary, error) {
	const q = `SELECT family_id, MAX(issued_at), MAX(expires_at)
	             FROM refresh_tokens
	             WHERE user_id = $1 AND revoked_at IS NULL
	             GROUP BY family_id
	             ORDER BY MAX(issued_at) DESC`
	rows, err := s.DB.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FamilySummary
	for rows.Next() {
		var f FamilySummary
		if err := rows.Scan(&f.FamilyID, &f.MostRecent, &f.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FamilySummary is one row of ListFamiliesForUser's result.
type FamilySummary struct {
	FamilyID   string
	MostRecent time.Time
	ExpiresAt  time.Time
}

// parseToken splits a `mkt_rt_v1.<id>.<secret>` into (id, secret).
// Returns ErrMalformed when the input doesn't have the prefix or has
// the wrong shape.
func parseToken(s string) (id, secret string, err error) {
	if !strings.HasPrefix(s, TokenPrefix) {
		return "", "", ErrMalformed
	}
	rest := s[len(TokenPrefix):]
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrMalformed
	}
	return parts[0], parts[1], nil
}

func scanWithHash(row *sql.Row) (*Token, string, error) {
	var (
		t    Token
		hash string
		dev  sql.NullString
		rev  sql.NullTime
		rep  sql.NullString
		meta sql.NullString
	)
	if err := row.Scan(
		&t.ID, &t.UserID, &hash, &t.FamilyID, &dev,
		&t.IssuedAt, &t.ExpiresAt, &rev, &rep, &meta,
	); err != nil {
		return nil, "", err
	}
	if dev.Valid {
		v := dev.String
		t.DeviceID = &v
	}
	if rev.Valid {
		v := rev.Time
		t.RevokedAt = &v
	}
	if rep.Valid {
		v := rep.String
		t.ReplacedBy = &v
	}
	if meta.Valid && meta.String != "" {
		_ = json.Unmarshal([]byte(meta.String), &t.ClientMeta)
	}
	return &t, hash, nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func randomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("refresh: rand: %w", err)
	}
	return buf, nil
}
