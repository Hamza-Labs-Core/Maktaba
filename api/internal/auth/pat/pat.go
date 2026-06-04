// Package pat implements personal access tokens (web-pages-batch2,
// slot 0071): long-lived bearer credentials a user creates for scripts,
// CI, and the CLI.
//
// Token format:
//
//	pat_<prefix>_<secret>
//
// where <prefix> is 12 hex chars (the public, UNIQUE-indexed lookup
// key) and <secret> is 32 bytes of url-safe random. Only SHA-256(secret)
// is stored; the raw token is shown to the user exactly once at
// creation. Verification is an O(1) lookup by prefix followed by a
// constant-time compare of the recomputed secret hash.
package pat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TokenPrefix identifies a personal access token on the wire.
const TokenPrefix = "pat_"

// prefixLen is the number of hex chars in the public prefix (6 bytes).
const prefixLen = 12

// secretLen is the raw entropy of the secret half, in bytes.
const secretLen = 32

// Errors exported by Store.
var (
	// ErrNotFound — no row matched (list/revoke by id), or the token
	// didn't resolve to a live row (authenticate).
	ErrNotFound = errors.New("personal access token not found")

	// ErrMalformed — the presented token didn't parse as pat_<prefix>_<secret>.
	ErrMalformed = errors.New("personal access token malformed")
)

// Token is the row shape returned to handlers. Hash is never exposed;
// Plaintext is populated only by Create.
type Token struct {
	ID         string
	UserID     string
	Name       string
	Prefix     string
	Scopes     []string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	CreatedAt  time.Time
	RevokedAt  *time.Time

	// Plaintext is the full `pat_<prefix>_<secret>` string, populated
	// only by Create. The caller MUST surface it once and drop it.
	Plaintext string
}

// Store wraps SQL access for `personal_access_tokens`.
type Store struct {
	DB *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// CreateInput is the public shape for Create.
type CreateInput struct {
	UserID    string
	Name      string
	Scopes    []string
	ExpiresAt *time.Time // nil ⇒ never expires
}

// Create mints a token, persists the hash, and returns the row with
// Plaintext populated.
func (s *Store) Create(ctx context.Context, in CreateInput) (*Token, error) {
	prefixBuf := make([]byte, prefixLen/2)
	if _, err := rand.Read(prefixBuf); err != nil {
		return nil, err
	}
	secretBuf := make([]byte, secretLen)
	if _, err := rand.Read(secretBuf); err != nil {
		return nil, err
	}
	prefix := hex.EncodeToString(prefixBuf)
	secret := base64.RawURLEncoding.EncodeToString(secretBuf)
	scopes := in.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	id := uuid.NewString()
	now := time.Now().UTC()
	var expiresArg sql.NullTime
	if in.ExpiresAt != nil {
		expiresArg = sql.NullTime{Valid: true, Time: *in.ExpiresAt}
	}

	const q = `INSERT INTO personal_access_tokens
	             (id, user_id, name, token_hash, prefix, scopes, expires_at, created_at)
	             VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	if _, err := s.DB.ExecContext(ctx, q,
		id, in.UserID, in.Name, hashSecret(secret), prefix,
		scopeArray(scopes), expiresArg, now,
	); err != nil {
		return nil, err
	}

	t := &Token{
		ID:        id,
		UserID:    in.UserID,
		Name:      in.Name,
		Prefix:    prefix,
		Scopes:    scopes,
		CreatedAt: now,
		ExpiresAt: in.ExpiresAt,
		Plaintext: TokenPrefix + prefix + "_" + secret,
	}
	return t, nil
}

// List returns the user's tokens (active + revoked), newest first. The
// hash is never selected.
func (s *Store) List(ctx context.Context, userID string) ([]Token, error) {
	const q = `SELECT id, user_id, name, prefix, scopes, last_used_at, expires_at, created_at, revoked_at
	             FROM personal_access_tokens
	             WHERE user_id = $1
	             ORDER BY created_at DESC`
	rows, err := s.DB.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Revoke soft-revokes the user's token by id. Owner-scoped: a token
// belonging to another user is reported as ErrNotFound. Idempotent —
// re-revoking an already-revoked row still returns nil (the row exists
// and is the owner's).
func (s *Store) Revoke(ctx context.Context, userID, id string) error {
	const q = `UPDATE personal_access_tokens
	             SET revoked_at = COALESCE(revoked_at, now())
	             WHERE id = $1 AND user_id = $2`
	res, err := s.DB.ExecContext(ctx, q, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate resolves a presented `pat_...` token to its owner. It
// verifies the secret, rejects revoked/expired tokens, best-effort
// touches last_used_at, and returns the token row (without Plaintext).
func (s *Store) Authenticate(ctx context.Context, plaintext string) (*Token, error) {
	prefix, secret, err := parseToken(plaintext)
	if err != nil {
		return nil, err
	}
	const q = `SELECT id, user_id, name, prefix, scopes, last_used_at, expires_at, created_at, revoked_at, token_hash
	             FROM personal_access_tokens WHERE prefix = $1`
	row := s.DB.QueryRowContext(ctx, q, prefix)
	t, hash, err := scanTokenWithHash(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(hashSecret(secret))) != 1 {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	if t.RevokedAt != nil {
		return nil, ErrNotFound
	}
	if t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
		return nil, ErrNotFound
	}
	// Best-effort touch; a failure here must not reject a valid token.
	_, _ = s.DB.ExecContext(ctx,
		`UPDATE personal_access_tokens SET last_used_at = $2 WHERE id = $1`, t.ID, now)
	t.LastUsedAt = &now
	return t, nil
}

// IsPAT reports whether a bearer value looks like a personal access
// token (cheap prefix check used by the middleware to skip non-PAT
// bearers without a DB hit).
func IsPAT(bearer string) bool {
	return strings.HasPrefix(bearer, TokenPrefix)
}

// parseToken splits `pat_<prefix>_<secret>` into its parts. The prefix
// is fixed-length hex so the split is unambiguous even though the
// base64url secret may itself contain '_'.
func parseToken(plaintext string) (prefix, secret string, err error) {
	if !strings.HasPrefix(plaintext, TokenPrefix) {
		return "", "", ErrMalformed
	}
	rest := plaintext[len(TokenPrefix):]
	if len(rest) < prefixLen+2 || rest[prefixLen] != '_' {
		return "", "", ErrMalformed
	}
	prefix = rest[:prefixLen]
	secret = rest[prefixLen+1:]
	if prefix == "" || secret == "" {
		return "", "", ErrMalformed
	}
	return prefix, secret, nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

type rowLike interface{ Scan(dest ...any) error }

func scanToken(row rowLike) (*Token, error) {
	var (
		t       Token
		scopes  scopeArray
		lastU   sql.NullTime
		expires sql.NullTime
		revoked sql.NullTime
	)
	if err := row.Scan(
		&t.ID, &t.UserID, &t.Name, &t.Prefix, &scopes,
		&lastU, &expires, &t.CreatedAt, &revoked,
	); err != nil {
		return nil, err
	}
	t.Scopes = scopes
	if t.Scopes == nil {
		t.Scopes = []string{}
	}
	if lastU.Valid {
		v := lastU.Time
		t.LastUsedAt = &v
	}
	if expires.Valid {
		v := expires.Time
		t.ExpiresAt = &v
	}
	if revoked.Valid {
		v := revoked.Time
		t.RevokedAt = &v
	}
	return &t, nil
}

func scanTokenWithHash(row rowLike) (*Token, string, error) {
	var (
		t       Token
		scopes  scopeArray
		lastU   sql.NullTime
		expires sql.NullTime
		revoked sql.NullTime
		hash    string
	)
	if err := row.Scan(
		&t.ID, &t.UserID, &t.Name, &t.Prefix, &scopes,
		&lastU, &expires, &t.CreatedAt, &revoked, &hash,
	); err != nil {
		return nil, "", err
	}
	t.Scopes = scopes
	if t.Scopes == nil {
		t.Scopes = []string{}
	}
	if lastU.Valid {
		v := lastU.Time
		t.LastUsedAt = &v
	}
	if expires.Valid {
		v := expires.Time
		t.ExpiresAt = &v
	}
	if revoked.Valid {
		v := revoked.Time
		t.RevokedAt = &v
	}
	return &t, hash, nil
}
