// Package users implements Story 10.1's persistence layer for the
// `users` table (slot 0029).
//
// Surface area:
//
//   - User: the row shape exposed to handlers, with `pw_hash` kept
//     private so handlers can't accidentally serialise it.
//   - Store: CRUD + locking primitives. Backed by *sql.DB so the same
//     code works against Postgres and SQLite (the schema parity is
//     enforced by the migrations).
//
// Handlers (Epic 7) wrap Store with HTTP semantics: 409 on username
// conflict, 409 on last-admin delete, 422 on oversized passwords.
// Those are tested at the handler level; this package owns the
// underlying constraints and constant-time verify.
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
)

// SentinelAdminID is the UUID pre-seeded by migration 0029 for the
// single-user / admin-token bypass path (Story 10.9). Audit rows for
// that path use this id; it is the only UUID with the
// `<unsalted-disabled>` placeholder hash.
const SentinelAdminID = "00000000-0000-0000-0000-000000000001"

// User is the canonical shape returned by Store. `pw_hash` is
// intentionally lower-cased and kept package-private — handlers must
// not serialise it. `LockedUntil` is nullable; callers compare against
// the current time.
type User struct {
	ID             string
	Username       string
	pwHash         string
	IsAdmin        bool
	FailedAttempts int
	LockedUntil    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IsLocked reports whether the user is currently in the brute-force
// lockout window. Story 10.11 owns the lockout policy; this is just
// the read-side primitive Story 10.1 AC-3 references.
func (u *User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && u.LockedUntil.After(now)
}

// VerifyPassword runs Verify against the stored PHC. Returns nil on
// match. ErrAuth on mismatch / disabled hash; the caller should map
// that to a 401. Other errors are pass-through (corrupt DB row, etc.).
func (u *User) VerifyPassword(password string) error {
	if argon2id.IsDisabled(u.pwHash) {
		return ErrAuth
	}
	if err := argon2id.Verify(password, u.pwHash); err != nil {
		if errors.Is(err, argon2id.ErrMismatch) {
			return ErrAuth
		}
		return err
	}
	return nil
}

// PWHash exposes the stored hash for callers that need to migrate it
// (e.g. an offline rehasher when defaults change). Handlers must NOT
// log this.
func (u *User) PWHash() string { return u.pwHash }

// Errors exported by Store.
var (
	// ErrUsernameExists is returned by Create / Update when the
	// case-insensitive username collides with an existing row.
	// Handlers map this to 409 `type: username-exists` per AC-Edge.
	ErrUsernameExists = errors.New("username already exists")

	// ErrLastAdmin is returned by Delete / Update when the requested
	// change would leave the system without an admin. Handlers map
	// this to 409 `type: last-admin`.
	ErrLastAdmin = errors.New("operation would leave the system with no admin")

	// ErrEmailExists is returned by Create / UpdateProfile when the
	// case-insensitive email collides with an existing row. Handlers map
	// this to 409 `type: email-exists` (web-pages-batch2).
	ErrEmailExists = errors.New("email already exists")

	// ErrNotFound is returned when the user_id has no row.
	ErrNotFound = errors.New("user not found")

	// ErrAuth is the generic "credentials don't match" error. All
	// flavours of mismatch (wrong password, disabled hash, locked
	// account) collapse to this so the login handler gives a uniform
	// 401 response (no oracle).
	ErrAuth = errors.New("authentication failed")
)

// Store wraps the SQL access layer for the users table.
type Store struct {
	DB     *sql.DB
	Params argon2id.Params
}

// New returns a Store with default argon2 params.
func New(db *sql.DB) *Store {
	return &Store{DB: db, Params: argon2id.DefaultParams()}
}

// CreateInput is the public CRUD shape used by the admin-create
// handler (Story 10.1 AC-3), the `adduser` CLI (AC-4), and the web
// self-service Register flow (web-pages-batch2). When `IsAdmin` is true
// the row is inserted with admin privileges. Email and DisplayName are
// optional self-service profile fields (slot 0068); empty strings are
// stored as SQL NULL so they don't collide on the partial-unique email
// index.
type CreateInput struct {
	Username    string
	Password    string
	IsAdmin     bool
	Email       string
	DisplayName string
}

// Create inserts a user row. Validates and casefolds username; hashes
// the password; returns the inserted row.
//
// AC-3: a username conflict (case-insensitively) returns
// ErrUsernameExists. The migration enforces uniqueness at the DB
// layer; Store maps the dialect-specific error to ErrUsernameExists.
func (s *Store) Create(ctx context.Context, in CreateInput) (*User, error) {
	if err := validateUsername(in.Username); err != nil {
		return nil, err
	}
	hash, err := argon2id.Hash(in.Password, s.Params)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	const q = `INSERT INTO users (id, username, pw_hash, is_admin, email, display_name)
	             VALUES ($1, $2, $3, $4, $5, $6)
	           RETURNING id, username, pw_hash, is_admin, failed_attempts, locked_until, created_at, updated_at`
	row := s.DB.QueryRowContext(ctx, q, id, in.Username, hash, in.IsAdmin,
		nullIfEmpty(in.Email), nullIfEmpty(in.DisplayName))
	u, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUsernameExists
		}
		return nil, err
	}
	return u, nil
}

// GetByID fetches one user by primary key.
func (s *Store) GetByID(ctx context.Context, id string) (*User, error) {
	const q = `SELECT id, username, pw_hash, is_admin, failed_attempts, locked_until, created_at, updated_at
	             FROM users WHERE id = $1`
	row := s.DB.QueryRowContext(ctx, q, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// GetByUsername fetches one user by case-insensitive username.
// Used by the login handler.
func (s *Store) GetByUsername(ctx context.Context, username string) (*User, error) {
	const q = `SELECT id, username, pw_hash, is_admin, failed_attempts, locked_until, created_at, updated_at
	             FROM users WHERE lower(username) = lower($1)`
	row := s.DB.QueryRowContext(ctx, q, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// UpdateInput models the partial-update shape the PATCH handler
// accepts. nil-valued pointers leave the field unchanged.
type UpdateInput struct {
	Username *string
	Password *string
	IsAdmin  *bool
}

// Update applies the partial update to `id`. Wraps the password
// rehash + the username uniqueness check in a single transaction so a
// concurrent rename can't slip past.
//
// Refuses to demote the last admin (returns ErrLastAdmin); callers
// can let that bubble back as a 409.
func (s *Store) Update(ctx context.Context, id string, in UpdateInput) (*User, error) {
	if in.Username == nil && in.Password == nil && in.IsAdmin == nil {
		return s.GetByID(ctx, id)
	}
	if in.Username != nil {
		if err := validateUsername(*in.Username); err != nil {
			return nil, err
		}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if in.IsAdmin != nil && !*in.IsAdmin {
		// Demoting an admin: assert another admin exists.
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE is_admin AND id <> $1`, id,
		).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			// Only blocks demotion if the *current* row is admin.
			var wasAdmin bool
			if err := tx.QueryRowContext(ctx,
				`SELECT is_admin FROM users WHERE id = $1`, id,
			).Scan(&wasAdmin); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, ErrNotFound
				}
				return nil, err
			}
			if wasAdmin {
				return nil, ErrLastAdmin
			}
		}
	}

	sets := []string{}
	args := []any{}
	idx := 1
	if in.Username != nil {
		sets = append(sets, fmt.Sprintf("username = $%d", idx))
		args = append(args, *in.Username)
		idx++
	}
	if in.Password != nil {
		hash, err := argon2id.Hash(*in.Password, s.Params)
		if err != nil {
			return nil, err
		}
		sets = append(sets, fmt.Sprintf("pw_hash = $%d", idx))
		args = append(args, hash)
		idx++
	}
	if in.IsAdmin != nil {
		sets = append(sets, fmt.Sprintf("is_admin = $%d", idx))
		args = append(args, *in.IsAdmin)
		idx++
	}
	sets = append(sets, "updated_at = now()")
	args = append(args, id)

	q := fmt.Sprintf(`UPDATE users SET %s WHERE id = $%d
	                  RETURNING id, username, pw_hash, is_admin, failed_attempts, locked_until, created_at, updated_at`,
		strings.Join(sets, ", "), idx)
	row := tx.QueryRowContext(ctx, q, args...)
	u, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUsernameExists
		}
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return u, nil
}

// Delete removes the user and cascades dependent rows (the FK chain
// in 0030 + future plan-10-02/03 migrations does the cascade in DB).
// Refuses to delete the last admin.
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var isAdmin bool
	err = tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id = $1`, id).Scan(&isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if isAdmin {
		var others int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE is_admin AND id <> $1`, id).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// clearLockoutSQL is the single canonical statement that zeroes the
// brute-force counter and drops the lockout window. Both the admin
// Unlock escape hatch and the reset-on-successful-login path use it so
// "cleared" means exactly one thing (DRY — no divergent clear).
const clearLockoutSQL = `UPDATE users SET failed_attempts = 0, locked_until = NULL, updated_at = now() WHERE id = $1`

// Unlock clears `failed_attempts` and `locked_until` (Story 10.1
// AC-3). Used by the admin unlock endpoint and by the lockout-window
// expiry path.
func (s *Store) Unlock(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, clearLockoutSQL, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetFailedAttempts zeroes the brute-force counter and clears the
// lockout window after a SUCCESSFUL authentication. Without this a user
// who once hit the threshold keeps failed_attempts pinned at the cap:
// the next isolated typo lands at cap+1 >= threshold and instantly
// re-locks for the full window, leaving admin Unlock as the only
// recovery (HLB-398 correctness). It reuses the exact same clear SQL as
// admin Unlock so a "cleared" account is identical regardless of which
// path cleared it. A missing row is not an error here: the caller has
// already authenticated against a loaded user, and a best-effort reset
// must never turn a successful login into a 500.
func (s *Store) ResetFailedAttempts(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, clearLockoutSQL, id)
	return err
}

// IncrementFailedAttempt bumps `failed_attempts` and, when the count
// crosses the threshold, applies a `locked_until` window. The threshold
// and window are owned by Story 10.11; we accept them as arguments so
// this package stays unaware of the policy.
//
// Stale-window correctness (HLB-398): if a prior lockout window has
// fully expired (locked_until is set but no longer in the future — the
// same condition User.IsLocked uses, just negated) the counter is NOT
// accumulated across it. The expired window is dropped and this attempt
// is counted as #1. Without this, an account that locked once stays
// pinned at the cap forever: the window elapses, the user is no longer
// IsLocked, but the very next typo is oldCount+1 >= threshold and
// instantly re-locks. A NULL locked_until (never locked) keeps the
// simple +1 accumulation toward the first lockout.
func (s *Store) IncrementFailedAttempt(ctx context.Context, id string, threshold int, lockFor time.Duration) error {
	const q = `UPDATE users SET
	             failed_attempts = CASE
	               WHEN locked_until IS NOT NULL AND locked_until <= now() THEN 1
	               ELSE failed_attempts + 1
	             END,
	             locked_until = CASE
	               WHEN (CASE
	                       WHEN locked_until IS NOT NULL AND locked_until <= now() THEN 1
	                       ELSE failed_attempts + 1
	                     END) >= $2 THEN now() + ($3 || ' seconds')::interval
	               WHEN locked_until IS NOT NULL AND locked_until <= now() THEN NULL
	               ELSE locked_until
	             END,
	             updated_at = now()
	           WHERE id = $1`
	_, err := s.DB.ExecContext(ctx, q, id, threshold, fmt.Sprintf("%d", int(lockFor.Seconds())))
	return err
}

// HasAnyUser reports whether at least one row exists with a real
// (non-disabled) password hash. The `adduser` CLI uses this to refuse
// to seed when the table is already populated, so an unattended
// rebuild can't accidentally create a second admin.
func (s *Store) HasAnyUser(ctx context.Context) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM users WHERE pw_hash <> $1)`
	var exists bool
	err := s.DB.QueryRowContext(ctx, q, "<unsalted-disabled>").Scan(&exists)
	return exists, err
}

// CountUsers returns the number of *real* user accounts — i.e. seats
// consumed for Epic 16 seat enforcement. The disabled-password
// sentinel rows (the single-user/admin-token bypass seed, Story 10.9)
// are excluded: they are an auth-bypass mechanism, not a provisioned
// seat, so they must not count against a paid tier's seat cap.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	const q = `SELECT COUNT(*) FROM users WHERE pw_hash <> $1`
	var n int
	err := s.DB.QueryRowContext(ctx, q, "<unsalted-disabled>").Scan(&n)
	return n, err
}

// Profile is the self-service projection of a user's editable profile
// (web-pages-batch2 Account page). Email / DisplayName are nullable in
// the schema; empty string here means "unset".
type Profile struct {
	ID          string
	Username    string
	Email       string
	DisplayName string
	IsAdmin     bool
}

// GetProfile returns the self-service profile fields for id.
func (s *Store) GetProfile(ctx context.Context, id string) (*Profile, error) {
	const q = `SELECT id, username, email, display_name, is_admin FROM users WHERE id = $1`
	var (
		p     Profile
		email sql.NullString
		dn    sql.NullString
	)
	err := s.DB.QueryRowContext(ctx, q, id).Scan(&p.ID, &p.Username, &email, &dn, &p.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Email = email.String
	p.DisplayName = dn.String
	return &p, nil
}

// IDByEmail returns the user id owning the case-insensitive email, or
// ErrNotFound. Used by the forgot-password flow and the register
// pre-check. Never reveals the result to the client directly (the
// forgot-password handler always responds 200).
func (s *Store) IDByEmail(ctx context.Context, email string) (string, error) {
	const q = `SELECT id FROM users WHERE email IS NOT NULL AND lower(email) = lower($1)`
	var id string
	err := s.DB.QueryRowContext(ctx, q, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// ProfileUpdate is the partial-update shape for UpdateProfile. nil
// pointers leave the field unchanged; a non-nil empty string clears it
// (sets SQL NULL).
type ProfileUpdate struct {
	Email       *string
	DisplayName *string
}

// UpdateProfile applies a partial profile update. Maps an email
// uniqueness collision to ErrEmailExists so the handler can return a
// precise 409.
func (s *Store) UpdateProfile(ctx context.Context, id string, in ProfileUpdate) error {
	if in.Email == nil && in.DisplayName == nil {
		return nil
	}
	sets := []string{}
	args := []any{}
	idx := 1
	if in.Email != nil {
		sets = append(sets, fmt.Sprintf("email = $%d", idx))
		args = append(args, nullIfEmpty(*in.Email))
		idx++
	}
	if in.DisplayName != nil {
		sets = append(sets, fmt.Sprintf("display_name = $%d", idx))
		args = append(args, nullIfEmpty(*in.DisplayName))
		idx++
	}
	sets = append(sets, "updated_at = now()")
	args = append(args, id)
	q := fmt.Sprintf(`UPDATE users SET %s WHERE id = $%d`, strings.Join(sets, ", "), idx)
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailExists
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// nullIfEmpty maps "" → SQL NULL so optional unique columns (email)
// don't collide across rows that simply left the field blank.
func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// validateUsername enforces a small set of rules: non-empty, ≤64 byes,
// no leading/trailing whitespace, and no control chars. Stricter rules
// (no `:`, no `@`) are punted to handlers since they're tied to UI
// expectations.
func validateUsername(u string) error {
	if u == "" || len(u) > 64 {
		return errors.New("username: must be 1..64 chars")
	}
	if strings.TrimSpace(u) != u {
		return errors.New("username: leading/trailing whitespace")
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f {
			return errors.New("username: contains a control character")
		}
	}
	return nil
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var locked sql.NullTime
	if err := row.Scan(
		&u.ID, &u.Username, &u.pwHash, &u.IsAdmin,
		&u.FailedAttempts, &locked, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if locked.Valid {
		u.LockedUntil = &locked.Time
	}
	return &u, nil
}

// isUniqueViolation reports whether `err` is the dialect-specific
// "duplicate key" error. We string-match because a typed `pq.Error`
// would couple this package to lib/pq, and SQLite's go-sqlite3
// returns its own error type.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate")
}
