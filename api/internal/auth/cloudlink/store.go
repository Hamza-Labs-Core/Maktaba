package cloudlink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
)

// NonceTTL is how long the link step's nonce lives: the cloud sign-in in
// a browser sheet happens in between.
const NonceTTL = 10 * time.Minute

// Kinds of credential a cloud sign-in made (cloud_sign_ins.kind).
const (
	KindSession = "session" // a web session id
	KindRefresh = "refresh" // a refresh token family id
)

var (
	// ErrNotLinked: the home user has no cloud link (or the cloud account
	// is linked to no home user here).
	ErrNotLinked = errors.New("cloudlink: not linked")
	// ErrCloudAccountTaken: the cloud account is linked to another home
	// user on this home (one cloud account per home user, and back).
	ErrCloudAccountTaken = errors.New("cloudlink: that cloud account is linked to another user here")
	// ErrNonce: the link nonce is unknown, spent, expired or another user's.
	ErrNonce = errors.New("cloudlink: link nonce refused")
	// ErrReplayed: the assertion was used already.
	ErrReplayed = errors.New("cloudlink: assertion already used")
)

// Link is a home user's link to a cloud account.
type Link struct {
	UserID      string    `json:"user_id"`
	CloudUserID string    `json:"cloud_user_id"`
	CloudLabel  string    `json:"cloud_label,omitempty"`
	Scope       string    `json:"scope"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store is the SQL access layer for home_user_links, cloud_link_nonces,
// cloud_assertions_used and cloud_sign_ins (slot 0153). Times are passed
// in, so the queries run on Postgres and SQLite alike.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
	// Changed, when set, is told after every link change (the reporter
	// that passes the linked cloud accounts to the cloud).
	Changed func()
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) changed() {
	if s.Changed != nil {
		s.Changed()
	}
}

func hashNonce(n string) string {
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])
}

// StartLink makes the link step's nonce for a home user. Only its hash is
// kept; an unfinished one expires.
func (s *Store) StartLink(ctx context.Context, userID string) (string, time.Time, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	exp := now.Add(NonceTTL)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM cloud_link_nonces WHERE expires_at < $1`, now); err != nil {
		return "", time.Time{}, err
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO cloud_link_nonces (nonce_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hashNonce(nonce), userID, exp); err != nil {
		return "", time.Time{}, err
	}
	return nonce, exp, nil
}

// consumeNonce spends a home user's live nonce (one use).
func consumeNonce(ctx context.Context, tx *sql.Tx, userID, nonce string, now time.Time) error {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM cloud_link_nonces WHERE nonce_hash = $1 AND user_id = $2 AND expires_at >= $3`,
		hashNonce(nonce), userID, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNonce
	}
	return nil
}

// useAssertion records an assertion's jti so it serves once.
func useAssertion(ctx context.Context, tx execer, jti string, exp, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cloud_assertions_used WHERE expires_at < $1`, now.Add(-time.Hour)); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO cloud_assertions_used (jti, expires_at) VALUES ($1, $2) ON CONFLICT (jti) DO NOTHING`,
		jti, exp)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrReplayed
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// UseAssertion spends a sign-in assertion (one use).
func (s *Store) UseAssertion(ctx context.Context, a Assertion) error {
	return useAssertion(ctx, s.DB, a.Jti, time.Unix(a.Exp, 0).UTC(), s.now())
}

// CompleteLink links the home user to the cloud account a verified link
// assertion names: its nonce must be this user's live one, the assertion
// unused, and the cloud account linked to no other user here. A new link
// is scope play; relinking to another cloud account ends the old link's
// sign-ins.
func (s *Store) CompleteLink(ctx context.Context, userID string, a Assertion) (*Link, error) {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := consumeNonce(ctx, tx, userID, a.Non, now); err != nil {
		return nil, err
	}
	if err := useAssertion(ctx, tx, a.Jti, time.Unix(a.Exp, 0).UTC(), now); err != nil {
		return nil, err
	}
	var other string
	err = tx.QueryRowContext(ctx, `SELECT CAST(user_id AS TEXT) FROM home_user_links WHERE cloud_user_id = $1`, a.Sub).Scan(&other)
	switch {
	case err == nil && other != userID:
		return nil, ErrCloudAccountTaken
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}
	var prev string
	err = tx.QueryRowContext(ctx, `SELECT cloud_user_id FROM home_user_links WHERE user_id = $1`, userID).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var revoked []string
	if prev != "" && prev != a.Sub {
		if revoked, err = revokeSignIns(ctx, tx, userID, "", now); err != nil {
			return nil, err
		}
	}
	label := strings.TrimSpace(a.Lbl)
	if len(label) > 200 {
		label = label[:200]
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO home_user_links (user_id, cloud_user_id, cloud_label, scope, created_at, updated_at)
        VALUES ($1, $2, $3, 'play', $4, $4)
        ON CONFLICT (user_id) DO UPDATE SET
            cloud_user_id = EXCLUDED.cloud_user_id,
            cloud_label   = EXCLUDED.cloud_label,
            scope         = CASE WHEN home_user_links.cloud_user_id = EXCLUDED.cloud_user_id
                                 THEN home_user_links.scope ELSE 'play' END,
            updated_at    = EXCLUDED.updated_at
    `, userID, a.Sub, nullable(label), now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	notify(revoked, "cloud link replaced")
	s.changed()
	return s.LinkFor(ctx, userID)
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func scanLink(row interface{ Scan(...any) error }) (*Link, error) {
	var l Link
	var label sql.NullString
	if err := row.Scan(&l.UserID, &l.CloudUserID, &label, &l.Scope, &l.CreatedAt, &l.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotLinked
		}
		return nil, err
	}
	l.CloudLabel = label.String
	return &l, nil
}

const linkCols = `CAST(user_id AS TEXT), cloud_user_id, cloud_label, scope, created_at, updated_at`

// LinkFor is a home user's link, or ErrNotLinked.
func (s *Store) LinkFor(ctx context.Context, userID string) (*Link, error) {
	return scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM home_user_links WHERE user_id = $1`, userID))
}

// LinkByCloudUser is the link of a cloud account on this home, or
// ErrNotLinked.
func (s *Store) LinkByCloudUser(ctx context.Context, cloudUserID string) (*Link, error) {
	return scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM home_user_links WHERE cloud_user_id = $1`, cloudUserID))
}

// Unlink removes a home user's link and ends every sign-in it made, at
// once (their web sessions and refresh families are revoked; an access
// token already out stops at the link gate).
func (s *Store) Unlink(ctx context.Context, userID string) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM home_user_links WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotLinked
	}
	revoked, err := revokeSignIns(ctx, tx, userID, "", now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	notify(revoked, "cloud link removed")
	s.changed()
	return nil
}

// SetScope changes a link's scope. Taking manage away ends the sign-ins
// made with it; a sign-in made as play stays play (sign in again).
func (s *Store) SetScope(ctx context.Context, userID, scope string) (*Link, error) {
	if scope != principal.CloudScopePlay && scope != principal.CloudScopeManage {
		return nil, errors.New("cloudlink: scope is play or manage")
	}
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE home_user_links SET scope = $1, updated_at = $2 WHERE user_id = $3`, scope, now, userID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotLinked
	}
	var revoked []string
	if scope == principal.CloudScopePlay {
		if revoked, err = revokeSignIns(ctx, tx, userID, principal.CloudScopeManage, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	notify(revoked, "cloud link scope lowered")
	return s.LinkFor(ctx, userID)
}

// revokeSignIns revokes the cloud sign-ins of a user (of one scope, or
// all when scope is ""), returning the web session ids it ended.
func revokeSignIns(ctx context.Context, tx *sql.Tx, userID, scope string, now time.Time) ([]string, error) {
	q := `SELECT id, kind FROM cloud_sign_ins WHERE user_id = $1`
	args := []any{userID}
	if scope != "" {
		q += ` AND scope = $2`
		args = append(args, scope)
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	type signIn struct{ id, kind string }
	var all []signIn
	for rows.Next() {
		var si signIn
		if err := rows.Scan(&si.id, &si.kind); err != nil {
			_ = rows.Close()
			return nil, err
		}
		all = append(all, si)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var sessIDs []string
	for _, si := range all {
		switch si.kind {
		case KindSession:
			if _, err := tx.ExecContext(ctx, `UPDATE web_sessions SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL`, now, si.id); err != nil {
				return nil, err
			}
			sessIDs = append(sessIDs, si.id)
		case KindRefresh:
			if _, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = $1 WHERE family_id = $2 AND revoked_at IS NULL`, now, si.id); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cloud_sign_ins WHERE id = $1`, si.id); err != nil {
			return nil, err
		}
	}
	return sessIDs, nil
}

// notify tells the cluster hub (and every node's cache) the sessions ended.
func notify(sessionIDs []string, reason string) {
	if len(sessionIDs) > 0 {
		sessions.NotifyRevoked(sessions.Revocation{SessionIDs: sessionIDs, Reason: reason})
	}
}

// RecordSignIn marks a web session or refresh family a cloud sign-in
// made, with the link's scope then.
func (s *Store) RecordSignIn(ctx context.Context, kind, id, userID, scope string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO cloud_sign_ins (id, kind, user_id, scope, created_at) VALUES ($1, $2, $3, $4, $5)`,
		id, kind, userID, scope, s.now())
	return err
}

// ScopeOf is the cloud scope a web session or refresh family was signed
// in with, or "" when a cloud sign-in did not make it. A database without
// slot 0153 (a machine's older copy) has no cloud sign-ins.
func (s *Store) ScopeOf(ctx context.Context, kind, id string) (string, error) {
	var scope string
	err := s.DB.QueryRowContext(ctx, `SELECT scope FROM cloud_sign_ins WHERE id = $1 AND kind = $2`, id, kind).Scan(&scope)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil && undefinedTable(err):
		return "", nil
	}
	return scope, err
}

func undefinedTable(err error) bool {
	var pe *pq.Error
	if errors.As(err, &pe) {
		return pe.Code == "42P01"
	}
	return strings.Contains(err.Error(), "no such table: cloud_sign_ins")
}

// LinkedCloudUsers lists every cloud account linked here (the report to
// the cloud, by id only).
func (s *Store) LinkedCloudUsers(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT cloud_user_id FROM home_user_links ORDER BY cloud_user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
