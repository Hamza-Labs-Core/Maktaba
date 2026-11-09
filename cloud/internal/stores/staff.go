package stores

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
)

// Staff is the operator console's accounts and everything they sign in with (Epic 50 story
// 50.33): staff_accounts, staff_enrolments, staff_totp, staff_recovery_codes,
// staff_mfa_tokens and staff_sessions. Raw tokens never reach the database: callers pass
// staffauth.TokenHash of them.
type Staff struct {
	DB *sql.DB
}

var (
	ErrStaffNotFound   = errors.New("stores: staff account not found")
	ErrStaffExists     = errors.New("stores: staff account exists")
	ErrStaffTokenGone  = errors.New("stores: staff token unknown, used or expired")
	ErrStaffSessionEnd = errors.New("stores: staff session ended")
	// ErrStaffReplay is a rotated refresh cookie presented again past the grace: every
	// session of the account has been revoked.
	ErrStaffReplay = errors.New("stores: rotated staff cookie replayed")
	// ErrStaffRotatedRecently is a cookie rotated within the grace: a second tab lost the race.
	ErrStaffRotatedRecently = errors.New("stores: staff cookie rotated moments ago")
	// ErrStaffNoFactor is an enrolment finished with neither a TOTP code nor a passkey.
	ErrStaffNoFactor = errors.New("stores: enrolment without a second factor")
)

// StaffRoleServiceAdmin is the role every /v1/admin/* route requires today.
const StaffRoleServiceAdmin = "service_admin"

// StaffAccount is a staff_accounts row.
type StaffAccount struct {
	ID           string
	Email        string
	PasswordHash string
	Roles        []string
	Status       string
	FailedAt     []time.Time
	LockedUntil  time.Time
	CreatedAt    time.Time
	EnrolledAt   sql.NullTime
	LastSignInAt sql.NullTime
}

// HasRole reports whether the account holds role.
func (a StaffAccount) HasRole(role string) bool { return slices.Contains(a.Roles, role) }

// Active is an account that may sign in: not disabled, and enrolled.
func (a StaffAccount) Active() bool { return a.Status == "active" && a.EnrolledAt.Valid }

// failed_at comes back as epoch seconds: lib/pq scans no timestamptz[] into time.Time
const staffCols = `id, email, COALESCE(password_hash, ''), roles, status,
	ARRAY(SELECT extract(epoch FROM f)::float8 FROM unnest(failed_at) f),
	COALESCE(locked_until, 'epoch'::timestamptz), created_at, enrolled_at, last_signin_at`

func scanStaff(row interface{ Scan(...any) error }) (StaffAccount, error) {
	var a StaffAccount
	var failed []float64
	err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, pq.Array(&a.Roles), &a.Status,
		pq.Array(&failed), &a.LockedUntil, &a.CreatedAt, &a.EnrolledAt, &a.LastSignInAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrStaffNotFound
	}
	for _, f := range failed {
		a.FailedAt = append(a.FailedAt, time.UnixMicro(int64(f*1e6)).UTC())
	}
	return a, err
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Create adds an account with roles and no password (it enrols through a link).
func (s *Staff) Create(ctx context.Context, email string, roles []string) (StaffAccount, error) {
	if roles == nil {
		roles = []string{} // pq.Array(nil) is NULL
	}
	row := s.DB.QueryRowContext(ctx, `INSERT INTO staff_accounts (email, roles) VALUES ($1, $2)
		ON CONFLICT (email) DO NOTHING RETURNING `+staffCols, normEmail(email), pq.Array(roles))
	a, err := scanStaff(row)
	if errors.Is(err, ErrStaffNotFound) {
		return a, ErrStaffExists
	}
	return a, err
}

// ByEmail and ByID load an account.
func (s *Staff) ByEmail(ctx context.Context, email string) (StaffAccount, error) {
	return scanStaff(s.DB.QueryRowContext(ctx, `SELECT `+staffCols+` FROM staff_accounts WHERE email = $1`, normEmail(email)))
}

func (s *Staff) ByID(ctx context.Context, id string) (StaffAccount, error) {
	return scanStaff(s.DB.QueryRowContext(ctx, `SELECT `+staffCols+` FROM staff_accounts WHERE id = $1`, id))
}

// List is every account, by email.
func (s *Staff) List(ctx context.Context) ([]StaffAccount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+staffCols+` FROM staff_accounts ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StaffAccount
	for rows.Next() {
		a, err := scanStaff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Staff) exec1(ctx context.Context, q string, args ...any) error {
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaffNotFound
	}
	return nil
}

// SetRoles replaces an account's roles.
func (s *Staff) SetRoles(ctx context.Context, id string, roles []string) error {
	if roles == nil {
		roles = []string{}
	}
	return s.exec1(ctx, `UPDATE staff_accounts SET roles = $2 WHERE id = $1`, id, pq.Array(roles))
}

// SetStatus sets active or disabled. Disabling ends, in the same transaction, every session,
// the pending mfa_tokens and any unused enrolment link (§4: disable ends every session at once).
func (s *Staff) SetStatus(ctx context.Context, id, status string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE staff_accounts SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaffNotFound
	}
	if status == "disabled" {
		for _, q := range []string{
			`DELETE FROM staff_mfa_tokens WHERE staff_id = $1`,
			`DELETE FROM staff_enrolments WHERE staff_id = $1 AND used_at IS NULL`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE staff_sessions SET revoked_at = $2, revoke_reason = 'disabled'
			WHERE staff_id = $1 AND revoked_at IS NULL`, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Unlock clears the lockout.
func (s *Staff) Unlock(ctx context.Context, id string) error {
	return s.exec1(ctx, `UPDATE staff_accounts SET failed_at = '{}', locked_until = NULL WHERE id = $1`, id)
}

// RecordFailure applies a failed step at now (staffauth.Lockout's rule, under a row lock)
// and reports whether it locked the account.
func (s *Staff) RecordFailure(ctx context.Context, id string, now time.Time) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := scanStaff(tx.QueryRowContext(ctx, `SELECT `+staffCols+` FROM staff_accounts WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return false, err
	}
	l := staffauth.Lockout{Failures: a.FailedAt, LockedUntil: a.LockedUntil}
	locked := l.Fail(now)
	var until any // NULL unless a lock is set (now, or still from before)
	if l.LockedUntil.After(time.Unix(0, 0)) {
		until = l.LockedUntil
	}
	failed := make([]string, len(l.Failures))
	for i, f := range l.Failures {
		failed[i] = f.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE staff_accounts SET failed_at = $2::timestamptz[], locked_until = $3 WHERE id = $1`,
		id, pq.Array(failed), until); err != nil {
		return false, err
	}
	return locked, tx.Commit()
}

// ClearFailures forgets the failures after a full sign-in.
func (s *Staff) ClearFailures(ctx context.Context, id string, now time.Time) error {
	return s.exec1(ctx, `UPDATE staff_accounts SET failed_at = '{}', last_signin_at = $2 WHERE id = $1`, id, now)
}

// --- enrolment ---

// NewEnrolment stores a link token for id, valid until expires; earlier unused links die.
func (s *Staff) NewEnrolment(ctx context.Context, id, tokenHash string, expires time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM staff_enrolments WHERE staff_id = $1 AND used_at IS NULL`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO staff_enrolments (token_hash, staff_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, id, expires); err != nil {
		return err
	}
	return tx.Commit()
}

// Enrolment is the account a live (unused, unexpired) link token belongs to.
func (s *Staff) Enrolment(ctx context.Context, tokenHash string, now time.Time) (StaffAccount, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT staff_id FROM staff_enrolments
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2`, tokenHash, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return StaffAccount{}, ErrStaffTokenGone
	}
	if err != nil {
		return StaffAccount{}, err
	}
	return s.ByID(ctx, id)
}

// FinishEnrolment sets the password, confirms the TOTP factor (withTOTP) or else drops the
// unconfirmed one and requires a passkey, stores the recovery codes and spends the link, in
// one transaction: an enrolment is whole or not at all, and never without a second factor.
func (s *Staff) FinishEnrolment(ctx context.Context, tokenHash, id, passwordHash string, withTOTP bool, recoveryHashes []string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE staff_enrolments SET used_at = $3
		WHERE token_hash = $1 AND staff_id = $2 AND used_at IS NULL AND expires_at > $3`, tokenHash, id, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaffTokenGone
	}
	if withTOTP {
		res, err = tx.ExecContext(ctx, `UPDATE staff_totp SET confirmed_at = $2 WHERE staff_id = $1`, id, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrStaffNoFactor
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM staff_totp WHERE staff_id = $1 AND confirmed_at IS NULL`, id); err != nil {
			return err
		}
		var passkeys int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM staff_passkeys WHERE staff_id = $1 AND disabled_at IS NULL`, id).Scan(&passkeys); err != nil {
			return err
		}
		if passkeys == 0 {
			return ErrStaffNoFactor
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM staff_recovery_codes WHERE staff_id = $1`, id); err != nil {
		return err
	}
	for _, h := range recoveryHashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO staff_recovery_codes (staff_id, code_hash) VALUES ($1, $2)`, id, h); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE staff_accounts SET password_hash = $2, enrolled_at = $3,
		failed_at = '{}', locked_until = NULL WHERE id = $1`, id, passwordHash, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetMFA removes every factor and recovery code, un-enrols the account (it can't sign in
// until a new enrolment finishes) and ends every session.
func (s *Staff) ResetMFA(ctx context.Context, id string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM staff_totp WHERE staff_id = $1`,
		`DELETE FROM staff_passkeys WHERE staff_id = $1`,
		`DELETE FROM staff_recovery_codes WHERE staff_id = $1`,
		`DELETE FROM staff_mfa_tokens WHERE staff_id = $1`,
		`DELETE FROM staff_enrolments WHERE staff_id = $1 AND used_at IS NULL`,
		`UPDATE staff_accounts SET enrolled_at = NULL WHERE id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE staff_sessions SET revoked_at = $2, revoke_reason = 'reset_mfa'
		WHERE staff_id = $1 AND revoked_at IS NULL`, id, now); err != nil {
		return err
	}
	return tx.Commit()
}

// --- TOTP ---

// PutTOTP stores a new, unconfirmed sealed secret for id (enrolment's first step), replacing
// an unconfirmed one; a confirmed factor is never replaced this way.
func (s *Staff) PutTOTP(ctx context.Context, id, sealed string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO staff_totp (staff_id, secret_sealed) VALUES ($1, $2)
		ON CONFLICT (staff_id) DO UPDATE SET secret_sealed = EXCLUDED.secret_sealed, last_step = 0
		WHERE staff_totp.confirmed_at IS NULL`, id, sealed)
	return err
}

// TOTP is id's sealed secret, its last used step, and whether it is confirmed.
func (s *Staff) TOTP(ctx context.Context, id string) (sealed string, lastStep int64, confirmed bool, err error) {
	var c sql.NullTime
	err = s.DB.QueryRowContext(ctx, `SELECT secret_sealed, last_step, confirmed_at FROM staff_totp WHERE staff_id = $1`, id).
		Scan(&sealed, &lastStep, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, ErrStaffNotFound
	}
	return sealed, lastStep, c.Valid, err
}

// UseTOTPStep records step as used, only if it is newer than the last one: false means
// another request used it (or a later step) first, and the code must be refused.
func (s *Staff) UseTOTPStep(ctx context.Context, id string, step int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE staff_totp SET last_step = $2 WHERE staff_id = $1 AND last_step < $2`, id, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Methods are the second factors id can use: "totp" (confirmed), "passkey", "recovery".
func (s *Staff) Methods(ctx context.Context, id string) ([]string, error) {
	var totp, passkeys, recovery int
	err := s.DB.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM staff_totp WHERE staff_id = $1 AND confirmed_at IS NOT NULL),
		(SELECT count(*) FROM staff_passkeys WHERE staff_id = $1 AND disabled_at IS NULL),
		(SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NULL)`, id).
		Scan(&totp, &passkeys, &recovery)
	var m []string
	if passkeys > 0 {
		m = append(m, "passkey")
	}
	if totp > 0 {
		m = append(m, "totp")
	}
	if recovery > 0 && (totp > 0 || passkeys > 0) {
		m = append(m, "recovery")
	}
	return m, err
}

// --- recovery codes ---

// RecoveryCode is an unused code's row id and hash.
type RecoveryCode struct {
	ID   int64
	Hash string
}

// UnusedRecoveryCodes are id's codes not yet used.
func (s *Staff) UnusedRecoveryCodes(ctx context.Context, id string) ([]RecoveryCode, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, code_hash FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RecoveryCode
	for rows.Next() {
		var c RecoveryCode
		if err := rows.Scan(&c.ID, &c.Hash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SpendRecoveryCode marks a code used, once: false if it was already spent.
func (s *Staff) SpendRecoveryCode(ctx context.Context, codeID int64, now time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE staff_recovery_codes SET used_at = $2 WHERE id = $1 AND used_at IS NULL`, codeID, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// --- the mfa_token step ---

// NewMFAToken stores the token between password and second factor.
func (s *Staff) NewMFAToken(ctx context.Context, tokenHash, id, ip string, expires time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO staff_mfa_tokens (token_hash, staff_id, ip, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash, id, ip, expires)
	return err
}

// TakeMFAAttempt counts one attempt on a live token from ip and returns its account: the
// token must be unexpired, from the same client IP, and have attempts left (the fifth is
// its last). A token past them is deleted.
func (s *Staff) TakeMFAAttempt(ctx context.Context, tokenHash, ip string, now time.Time) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `UPDATE staff_mfa_tokens SET attempts = attempts + 1
		WHERE token_hash = $1 AND ip = $2 AND expires_at > $3 AND attempts < $4 RETURNING staff_id`,
		tokenHash, ip, now, staffauth.MFAMaxAttempts).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM staff_mfa_tokens WHERE token_hash = $1 AND (attempts >= $2 OR expires_at <= $3)`,
			tokenHash, staffauth.MFAMaxAttempts, now)
		return "", ErrStaffTokenGone
	}
	return id, err
}

// SpendMFAToken deletes a token after the step it served succeeded.
func (s *Staff) SpendMFAToken(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM staff_mfa_tokens WHERE token_hash = $1`, tokenHash)
	return err
}

// --- sessions ---

// StaffSession is a staff_sessions row.
type StaffSession struct {
	ID           string
	StaffID      string
	AMR          []string
	SignedInAt   time.Time
	LastActiveAt time.Time
	AuthTime     time.Time
	IP           string
	UserAgent    string
	RevokedAt    sql.NullTime
}

// Times is what staffauth.CheckSession reads.
func (s StaffSession) Times() staffauth.SessionTimes {
	return staffauth.SessionTimes{SignedInAt: s.SignedInAt, LastActiveAt: s.LastActiveAt}
}

const sessionCols = `id, staff_id, amr, signed_in_at, last_active_at, auth_time, ip, user_agent, revoked_at`

func scanSession(row interface{ Scan(...any) error }) (StaffSession, error) {
	var x StaffSession
	err := row.Scan(&x.ID, &x.StaffID, pq.Array(&x.AMR), &x.SignedInAt, &x.LastActiveAt, &x.AuthTime, &x.IP, &x.UserAgent, &x.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrStaffSessionEnd
	}
	return x, err
}

// NewSession starts a session after a full sign-in at now.
func (s *Staff) NewSession(ctx context.Context, staffID, tokenHash string, amr []string, ip, ua string, now time.Time) (StaffSession, error) {
	return scanSession(s.DB.QueryRowContext(ctx, `INSERT INTO staff_sessions
		(staff_id, token_hash, amr, signed_in_at, last_active_at, auth_time, ip, user_agent)
		VALUES ($1, $2, $3, $4, $4, $4, $5, $6) RETURNING `+sessionCols,
		staffID, tokenHash, pq.Array(amr), now, ip, ua))
}

// Session loads a live (unrevoked) session by id.
func (s *Staff) Session(ctx context.Context, id string) (StaffSession, error) {
	return scanSession(s.DB.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM staff_sessions WHERE id = $1 AND revoked_at IS NULL`, id))
}

// SessionByCookie finds the live session a refresh cookie belongs to. A cookie the session
// has already replaced is ErrStaffRotatedRecently within grace of the rotation, and past it a
// replay: every session of the account is revoked and ErrStaffReplay returned.
func (s *Staff) SessionByCookie(ctx context.Context, tokenHash string, now time.Time, grace time.Duration) (StaffSession, error) {
	x, err := scanSession(s.DB.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM staff_sessions WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash))
	if err == nil || !errors.Is(err, ErrStaffSessionEnd) {
		return x, err
	}
	var staffID string
	var rotated sql.NullTime
	err = s.DB.QueryRowContext(ctx, `SELECT staff_id, rotated_at FROM staff_sessions WHERE prev_token_hash = $1`, tokenHash).Scan(&staffID, &rotated)
	if errors.Is(err, sql.ErrNoRows) {
		return StaffSession{}, ErrStaffSessionEnd
	}
	if err != nil {
		return StaffSession{}, err
	}
	if rotated.Valid && now.Sub(rotated.Time) < grace {
		return StaffSession{}, ErrStaffRotatedRecently
	}
	if err := s.RevokeAll(ctx, staffID, "replay", now); err != nil {
		return StaffSession{}, err
	}
	return StaffSession{}, ErrStaffReplay
}

// Rotate gives a session a new cookie, keeping the old one's hash to recognise a replay.
func (s *Staff) Rotate(ctx context.Context, id, oldHash, newHash string, now time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE staff_sessions SET token_hash = $3, prev_token_hash = $2, rotated_at = $4
		WHERE id = $1 AND token_hash = $2 AND revoked_at IS NULL`, id, oldHash, newHash, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaffSessionEnd
	}
	return nil
}

// Touch moves a session's last activity to now (each admin request, §4).
func (s *Staff) Touch(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE staff_sessions SET last_active_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, now)
	return err
}

// Reauthed moves a session's last MFA check to now (§5) and adds the factor to its amr.
func (s *Staff) Reauthed(ctx context.Context, id, amr string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE staff_sessions SET auth_time = $2, last_active_at = $2,
		amr = CASE WHEN $3 = ANY(amr) THEN amr ELSE array_append(amr, $3) END
		WHERE id = $1 AND revoked_at IS NULL`, id, now, amr)
	return err
}

// End revokes one session with reason.
func (s *Staff) End(ctx context.Context, id, reason string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE staff_sessions SET revoked_at = $2, revoke_reason = $3 WHERE id = $1 AND revoked_at IS NULL`, id, now, reason)
	return err
}

// RevokeAll ends every session of an account.
func (s *Staff) RevokeAll(ctx context.Context, staffID, reason string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE staff_sessions SET revoked_at = $2, revoke_reason = $3
		WHERE staff_id = $1 AND revoked_at IS NULL`, staffID, now, reason)
	return err
}

// Sessions are an account's live sessions, newest first.
func (s *Staff) Sessions(ctx context.Context, staffID string) ([]StaffSession, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sessionCols+` FROM staff_sessions
		WHERE staff_id = $1 AND revoked_at IS NULL ORDER BY signed_in_at DESC`, staffID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StaffSession
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
