// Package sessions implements Story 10.2's `web_sessions` persistence
// layer (slot 0050).
//
// Surface area:
//
//   - Session: row shape exposed to handlers.
//   - Store: Create / Lookup / TouchLastSeen / Revoke + listing
//     primitives. Backed by *sql.DB so the same code works against
//     Postgres and SQLite.
//
// The session id is the cookie value; CSRF token is the double-submit
// half the SPA echoes back via `X-CSRF-Token` on mutating requests.
// Both are random url-safe strings — no JWT here; the row is the
// source of truth and revocation is instant.
package sessions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
)

// CSRFTokenLen is the random length of the CSRF double-submit token,
// in raw bytes (the wire form is base64url, ~43 chars).
const CSRFTokenLen = 32

// DefaultTTL is the canonical web session lifetime (28 days). Story
// 10.2 AC-1 calls this `auth.web_session_ttl_sec`.
const DefaultTTL = 28 * 24 * time.Hour

// TouchDebounce is how often `last_seen_at` is updated for an active
// session (AC-2). One write per minute per session keeps the table
// quiet under sustained polling.
const TouchDebounce = time.Minute

// Errors exported by Store.
var (
	// ErrNotFound is returned when no row matches the requested id —
	// or when the row is revoked or expired. Login flow maps this to
	// 401 with a cleared cookie.
	ErrNotFound = errors.New("session not found")
)

// Session is the row shape exposed to handlers. `id` is the cookie's
// value; `csrf_token` is what the SPA echoes back. Both are returned
// at create time; subsequent lookups only need the id.
type Session struct {
	ID          string
	UserID      string
	CSRFToken   string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	IP          *string
	UserAgent   *string
	RevokedAt   *time.Time
}

// Active reports whether `s` is currently usable (not revoked and not
// past expires_at). Callers should pass time.Now() so tests can stub
// the clock.
func (s *Session) Active(now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	return s.ExpiresAt.After(now)
}

// Store wraps the SQL access layer for `web_sessions`.
type Store struct {
	DB  *sql.DB
	TTL time.Duration
}

// New returns a Store with DefaultTTL.
func New(db *sql.DB) *Store {
	return &Store{DB: db, TTL: DefaultTTL}
}

// CreateInput is the public shape for Create. IP / UserAgent are
// nullable and used for audit only.
type CreateInput struct {
	UserID    string
	IP        string
	UserAgent string
}

// Create inserts a fresh session row and returns the in-memory shape
// (including the freshly minted id + csrf token). The caller stamps
// cookies from the result: `mkt_sess = Session.ID` and
// `mkt_csrf = Session.CSRFToken`.
//
// The id is a UUIDv4 (122 bits of entropy). The CSRF token is 32 bytes
// of url-safe random. Neither is derived from the user, so a stolen
// row id alone is enough to hijack — keep cookies httpOnly to mitigate.
func (s *Store) Create(ctx context.Context, in CreateInput) (*Session, error) {
	rowID := uuid.NewString()
	csrf, err := randomToken(CSRFTokenLen)
	if err != nil {
		return nil, err
	}

	ttl := s.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)

	var (
		ipArg sql.NullString
		uaArg sql.NullString
	)
	if in.IP != "" {
		ipArg = sql.NullString{Valid: true, String: in.IP}
	}
	if in.UserAgent != "" {
		uaArg = sql.NullString{Valid: true, String: in.UserAgent}
	}

	const q = `INSERT INTO web_sessions
	             (id, user_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent)
	             VALUES ($1, $2, $3, $4, $4, $5, $6, $7)`
	if _, err := s.DB.ExecContext(ctx, q,
		rowID, in.UserID, csrf, now, expires, ipArg, uaArg,
	); err != nil {
		return nil, err
	}

	out := &Session{
		ID:         rowID,
		UserID:     in.UserID,
		CSRFToken:  csrf,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expires,
	}
	if ipArg.Valid {
		v := ipArg.String
		out.IP = &v
	}
	if uaArg.Valid {
		v := uaArg.String
		out.UserAgent = &v
	}
	return out, nil
}

// Lookup fetches the session row by id. Returns ErrNotFound when the
// row is missing, revoked, or expired (so the handler can clear the
// cookie uniformly).
func (s *Store) Lookup(ctx context.Context, id string) (*Session, error) {
	const q = `SELECT id, user_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent, revoked_at
	             FROM web_sessions WHERE id = $1`
	row := s.DB.QueryRowContext(ctx, q, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !sess.Active(time.Now().UTC()) {
		return nil, ErrNotFound
	}
	return sess, nil
}

// TouchLastSeen bumps `last_seen_at` to `now` if the last touch is
// older than `TouchDebounce`. Returns nil on success or no-op; an error
// only when the underlying DB call fails. Concurrency-safe at the
// row level via the `last_seen_at < now() - $2` predicate.
func (s *Store) TouchLastSeen(ctx context.Context, id string, now time.Time) error {
	const q = `UPDATE web_sessions
	             SET last_seen_at = $2
	             WHERE id = $1
	               AND revoked_at IS NULL
	               AND last_seen_at < $3`
	threshold := now.Add(-TouchDebounce)
	_, err := s.DB.ExecContext(ctx, q, id, now, threshold)
	return err
}

// Revoke marks a single session as revoked. Idempotent — re-revoking
// a revoked row is a no-op.
func (s *Store) Revoke(ctx context.Context, id string) error {
	const q = `UPDATE web_sessions
	             SET revoked_at = now()
	             WHERE id = $1 AND revoked_at IS NULL`
	_, err := s.DB.ExecContext(ctx, q, id)
	return err
}

// RevokeAllForUser revokes every active web session for `userID`. Used
// by the logout-all endpoint (Story 10.5 AC-3) and by the admin
// "revoke user X" path.
func (s *Store) RevokeAllForUser(ctx context.Context, userID string) (int64, error) {
	const q = `UPDATE web_sessions
	             SET revoked_at = now()
	             WHERE user_id = $1 AND revoked_at IS NULL`
	res, err := s.DB.ExecContext(ctx, q, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListActive returns the (id, created_at, last_seen_at, ip, user_agent)
// of every active session for a user, newest-first. Used by the admin
// "list sessions" surface (Story 10.1 AC-3).
func (s *Store) ListActive(ctx context.Context, userID string) ([]Session, error) {
	const q = `SELECT id, user_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent, revoked_at
	             FROM web_sessions
	             WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
	             ORDER BY created_at DESC`
	rows, err := s.DB.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rowsAdapter{rows})
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

// rowsAdapter lets us share scanSession between *sql.Row and *sql.Rows.
type rowsAdapter struct {
	*sql.Rows
}

type rowLike interface {
	Scan(dest ...any) error
}

func scanSession(row rowLike) (*Session, error) {
	var s Session
	var ip, ua sql.NullString
	var revoked sql.NullTime
	if err := row.Scan(
		&s.ID, &s.UserID, &s.CSRFToken, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt,
		&ip, &ua, &revoked,
	); err != nil {
		return nil, err
	}
	if ip.Valid {
		v := ip.String
		s.IP = &v
	}
	if ua.Valid {
		v := ua.String
		s.UserAgent = &v
	}
	if revoked.Valid {
		v := revoked.Time
		s.RevokedAt = &v
	}
	return &s, nil
}

// randomToken returns a url-safe base64 of n random bytes (no padding).
// Used for the cookie id and the CSRF token.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
