// Package stores holds the SQL data-access layer for cloud entities.
// Each *Store is the only code allowed to touch its table — handlers
// and services go through these methods so query patterns stay
// auditable and so a future move off of database/sql does not ripple.
package stores

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// User mirrors the `users` row used by handlers.
type User struct {
	ID            string
	Email         string
	EmailVerified bool
	PasswordHash  sql.NullString
	DisplayName   sql.NullString
	Locale        string
	AvatarURL     sql.NullString
	Plan          string
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastLoginAt   sql.NullTime
}

var ErrUserNotFound = errors.New("users: not found")

// Users persists `users` and `oauth_links`.
type Users struct {
	DB *sql.DB
}

func NewUsers(db *sql.DB) *Users { return &Users{DB: db} }

// Create inserts a new user. PasswordHash may be empty for OAuth-only
// accounts; in that case email_verified is set true at create time
// because the OAuth provider already vouched for the address.
func (s *Users) Create(ctx context.Context, email, passwordHash, displayName string, oauthVerified bool) (User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return User{}, errors.New("users: email required")
	}
	var u User
	var pwHash sql.NullString
	if passwordHash != "" {
		pwHash = sql.NullString{String: passwordHash, Valid: true}
	}
	var dn sql.NullString
	if displayName != "" {
		dn = sql.NullString{String: displayName, Valid: true}
	}
	err := s.DB.QueryRowContext(ctx, `
        INSERT INTO users (email, password_hash, display_name, email_verified)
        VALUES ($1, $2, $3, $4)
        RETURNING id, email, email_verified, password_hash, display_name, locale, avatar_url, plan, status, created_at, updated_at, last_login_at
    `, email, pwHash, dn, oauthVerified).Scan(
		&u.ID, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.DisplayName, &u.Locale, &u.AvatarURL, &u.Plan, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt,
	)
	return u, err
}

// ByEmail looks up a user by lower-cased email.
func (s *Users) ByEmail(ctx context.Context, email string) (User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	var u User
	err := s.DB.QueryRowContext(ctx, `
        SELECT id, email, email_verified, password_hash, display_name, locale, avatar_url, plan, status, created_at, updated_at, last_login_at
        FROM users WHERE lower(email) = $1
    `, email).Scan(&u.ID, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.DisplayName, &u.Locale, &u.AvatarURL, &u.Plan, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

// ByID is the lookup used by middleware to hydrate the principal from
// a verified access token.
func (s *Users) ByID(ctx context.Context, id string) (User, error) {
	var u User
	err := s.DB.QueryRowContext(ctx, `
        SELECT id, email, email_verified, password_hash, display_name, locale, avatar_url, plan, status, created_at, updated_at, last_login_at
        FROM users WHERE id = $1
    `, id).Scan(&u.ID, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.DisplayName, &u.Locale, &u.AvatarURL, &u.Plan, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

// UpdateProfile patches the user-editable fields. Empty strings mean
// "no change" — callers wanting to clear a field pass a sentinel via
// dedicated method.
func (s *Users) UpdateProfile(ctx context.Context, id, displayName, locale, avatarURL string) error {
	_, err := s.DB.ExecContext(ctx, `
        UPDATE users SET
            display_name = COALESCE(NULLIF($2,''), display_name),
            locale       = COALESCE(NULLIF($3,''), locale),
            avatar_url   = COALESCE(NULLIF($4,''), avatar_url),
            updated_at   = now()
        WHERE id = $1
    `, id, displayName, locale, avatarURL)
	return err
}

// SetPasswordHash is used at registration AND at password-change. The
// caller has already validated the new password and produced a fresh
// PHC-string.
func (s *Users) SetPasswordHash(ctx context.Context, id, hash string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	return err
}

// SetEmailVerified marks the email confirmed (post token redemption).
func (s *Users) SetEmailVerified(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET email_verified = true, updated_at = now() WHERE id = $1`, id)
	return err
}

// SetPlan is the entrypoint for billing events: webhook → plan change.
func (s *Users) SetPlan(ctx context.Context, id, plan string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET plan = $2, updated_at = now() WHERE id = $1`, id, plan)
	return err
}

// TouchLogin records the last successful login. We do this opportunist-
// ically and ignore errors — it's audit metadata, not load-bearing.
func (s *Users) TouchLogin(ctx context.Context, id string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
}

// LinkOAuth creates an oauth_links row, or returns the existing user
// id if the (provider, subject) pair is already attached.
func (s *Users) LinkOAuth(ctx context.Context, userID, provider, subject, email string) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO oauth_links (user_id, provider, subject, email)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (provider, subject) DO NOTHING
    `, userID, provider, subject, email)
	return err
}

// UserByOAuth resolves a provider/subject pair to a user id, returning
// ErrUserNotFound if unlinked.
func (s *Users) UserByOAuth(ctx context.Context, provider, subject string) (User, error) {
	var u User
	err := s.DB.QueryRowContext(ctx, `
        SELECT u.id, u.email, u.email_verified, u.password_hash, u.display_name, u.locale, u.avatar_url, u.plan, u.status, u.created_at, u.updated_at, u.last_login_at
        FROM users u JOIN oauth_links l ON l.user_id = u.id
        WHERE l.provider = $1 AND l.subject = $2
    `, provider, subject).Scan(&u.ID, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.DisplayName, &u.Locale, &u.AvatarURL, &u.Plan, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}
