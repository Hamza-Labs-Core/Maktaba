package parental

// The parent PIN (Epic 57 story 57.06): a guardian sets six digits, and types them
// on the child's device to open one thing until the day ends. The PIN is kept as
// an argon2id hash, with the same code and cost as a password, and is never
// logged, audited or put in an event.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
)

const (
	// PINLength is the number of digits in a PIN.
	PINLength = 6
	// PINMaxWrong is how many wrong tries lock a guardian's PIN.
	PINMaxWrong = 5
	// PINLockBase is the first lock; each lock after it is twice the one before,
	// up to PINLockMax. A right PIN starts the count again.
	PINLockBase = 15 * time.Minute
	PINLockMax  = 24 * time.Hour
	// PINChildMaxWrong wrong tries from one child's account inside PINChildWindow
	// close PIN entry for that account for PINChildHold, whichever guardian's PIN
	// was tried.
	PINChildMaxWrong = 10
	PINChildWindow   = time.Hour
	PINChildHold     = time.Hour
)

// ValidPIN reports whether pin is six ASCII digits.
func ValidPIN(pin string) bool {
	if len(pin) != PINLength {
		return false
	}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return false
		}
	}
	return true
}

// SetPIN sets guardianID's PIN, and clears any lock on it. The caller has checked
// who guardianID is and confirmed their password. ErrInvalid when pin is not six
// digits; ErrUserNotFound when the user is gone.
func (s *Store) SetPIN(ctx context.Context, guardianID, pin string) error {
	if !ValidPIN(pin) {
		return fmt.Errorf("%w: pin", ErrInvalid)
	}
	if !isUUID(guardianID) {
		return ErrUserNotFound
	}
	hash, err := argon2id.Hash(pin, argon2id.DefaultParams())
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO parental_pins (guardian_id, pin_hash, updated_at) VALUES ($1, $2, $3)
		 ON CONFLICT (guardian_id) DO UPDATE
		 SET pin_hash = EXCLUDED.pin_hash, failed = 0, locks = 0, locked_until = NULL, updated_at = EXCLUDED.updated_at`,
		guardianID, hash, s.now().UTC())
	return translate(err)
}

// HasPIN reports whether guardianID has set a PIN.
func (s *Store) HasPIN(ctx context.Context, guardianID string) (bool, error) {
	if !isUUID(guardianID) {
		return false, nil
	}
	var ok bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM parental_pins WHERE guardian_id = $1)`, guardianID).Scan(&ok)
	return ok, err
}

// PINGuardians lists who can unlock for childID with a PIN: the child's named
// guardians and the admins, those of them who have set one. It is what the child's
// device shows to choose from.
func (s *Store) PINGuardians(ctx context.Context, childID string) ([]ChildRef, error) {
	out := []ChildRef{}
	if !isUUID(childID) {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT u.id, u.username FROM users u JOIN parental_pins p ON p.guardian_id = u.id
		  WHERE u.is_admin OR EXISTS (SELECT 1 FROM parental_guardians g WHERE g.guardian_id = u.id AND g.child_id = $1)
		  ORDER BY lower(u.username), u.id`, childID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g ChildRef
		if err := rows.Scan(&g.ID, &g.Username); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// What an unlock came to.
const (
	// UnlockOpened: the PIN was right and the subject is allowed until the day ends.
	UnlockOpened = "opened"
	// UnlockWrong: the PIN was wrong, or the user named is not someone who can
	// unlock for this child, or has no PIN. The three are not told apart.
	UnlockWrong = "wrong"
	// UnlockLocked: PIN entry is closed for now (see RetryAfter).
	UnlockLocked = "locked"
)

// Unlocked is what Unlock returns.
type Unlocked struct {
	Outcome string
	// Rule is the allow rule added (UnlockOpened).
	Rule *Rule
	// RetryAfter is how long PIN entry stays closed (UnlockLocked).
	RetryAfter time.Duration
	// LockedGuardian is the guardian whose PIN this try locked ("" for none), and
	// LockedAccount is set when this try closed PIN entry for the child's account.
	// A try refused because a lock was already on sets neither.
	LockedGuardian string
	LockedAccount  bool
}

// childLockedUntil is when PIN entry opens again for an account whose wrong tries
// were at times (oldest first): limit of them inside window close it for hold
// after the last. The zero time when it was never closed.
func childLockedUntil(times []time.Time, limit int, window, hold time.Duration) time.Time {
	var until time.Time
	for i := limit - 1; i < len(times); i++ {
		if times[i].Sub(times[i-limit+1]) <= window {
			until = times[i].Add(hold)
		}
	}
	return until
}

// lockFor is how long the n-th lock in a row lasts.
func lockFor(n int) time.Duration {
	d := PINLockBase
	for i := 1; i < n && d < PINLockMax; i++ {
		d *= 2
	}
	if d > PINLockMax {
		d = PINLockMax
	}
	return d
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// spendVerify does the work of checking a PIN when there is none to check, so a
// guardian with no PIN answers no faster than one with a PIN.
func spendVerify(pin string) {
	dummyOnce.Do(func() { dummyHash, _ = argon2id.Hash("parental-no-pin", argon2id.DefaultParams()) })
	if dummyHash != "" {
		_ = argon2id.Verify(pin, dummyHash)
	}
}

func (s *Store) childFailures(ctx context.Context, tx *sql.Tx, childID string, now time.Time) ([]time.Time, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT at FROM parental_pin_failures WHERE child_id = $1 AND at > $2 ORDER BY at`,
		childID, now.Add(-(PINChildWindow + PINChildHold)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, at)
	}
	return out, rows.Err()
}

// Unlock checks guardianID's PIN, typed on childID's device, and on a right one
// allows the subject for the child until the day ends (04:00 home time), through
// the same code as a rule a guardian adds by hand. all and libraries are the
// libraries the child's sign-in can open.
//
// A wrong try is counted against the guardian's PIN (PINMaxWrong lock it, for
// PINLockBase and then twice as long each time) and against the child's account
// (PINChildMaxWrong in an hour close PIN entry for an hour). While either lock is
// on, a try is refused without being checked or counted. Every outcome but an
// error is kept: the count of a wrong try is not rolled back.
//
// ErrNotRestricted when childID has no policy, ErrInvalid for a PIN that is not
// six digits or a bad subject, ErrSubjectNotFound (decided before the PIN is
// read, so it says nothing about the PIN), ErrNoLibraryAccess.
func (s *Store) Unlock(ctx context.Context, childID string, all bool, libraries []string, guardianID, pin, kind, id string) (*Unlocked, error) {
	if !ValidPIN(pin) {
		return nil, fmt.Errorf("%w: pin", ErrInvalid)
	}
	if err := (RuleInput{Effect: EffectAllow, SubjectKind: kind, SubjectID: id}).validate(); err != nil {
		return nil, err
	}
	out := &Unlocked{}
	loc := s.HomeLocation(ctx)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// the policy row is locked: the tries of one child run one after the other
		if _, err := policyOf(ctx, tx, childID, true); err != nil {
			return err
		}
		info, err := subjectOf(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		if info.Removed || info.For(all, libraries).Hidden {
			return ErrSubjectNotFound
		}
		now := s.now().UTC()
		times, err := s.childFailures(ctx, tx, childID, now)
		if err != nil {
			return err
		}
		if until := childLockedUntil(times, PINChildMaxWrong, PINChildWindow, PINChildHold); until.After(now) {
			out.Outcome, out.RetryAfter = UnlockLocked, until.Sub(now)
			return nil
		}

		var (
			hash   string
			failed int
			locks  int
			until  sql.NullTime
			hasPIN bool
		)
		eligible := false
		if isUUID(guardianID) {
			if err := tx.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM users u WHERE u.id = $1 AND (u.is_admin OR EXISTS (
				   SELECT 1 FROM parental_guardians g WHERE g.guardian_id = u.id AND g.child_id = $2)))`,
				guardianID, childID).Scan(&eligible); err != nil {
				return err
			}
		}
		if eligible {
			err := tx.QueryRowContext(ctx,
				`SELECT pin_hash, failed, locks, locked_until FROM parental_pins WHERE guardian_id = $1 FOR UPDATE`,
				guardianID).Scan(&hash, &failed, &locks, &until)
			switch {
			case err == nil:
				hasPIN = true
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		if hasPIN && until.Valid && until.Time.After(now) {
			out.Outcome, out.RetryAfter = UnlockLocked, until.Time.Sub(now)
			return nil
		}
		right := false
		if hasPIN {
			right = argon2id.Verify(pin, hash) == nil
		} else {
			spendVerify(pin)
		}

		if !right {
			out.Outcome = UnlockWrong
			var named any
			if eligible {
				named = guardianID
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO parental_pin_failures (child_id, guardian_id, at) VALUES ($1, $2, $3)`,
				childID, named, now); err != nil {
				return err
			}
			if hasPIN {
				failed++
				if failed >= PINMaxWrong {
					locks++
					hold := lockFor(locks)
					if _, err := tx.ExecContext(ctx,
						`UPDATE parental_pins SET failed = 0, locks = $2, locked_until = $3 WHERE guardian_id = $1`,
						guardianID, locks, now.Add(hold)); err != nil {
						return err
					}
					out.Outcome, out.RetryAfter, out.LockedGuardian = UnlockLocked, hold, guardianID
					if err := recordEvent(ctx, tx, Event{ChildID: childID, Kind: EventPINLocked,
						SubjectKind: EventSubjectGuardian, SubjectID: guardianID, ActorID: guardianID, At: now}, ""); err != nil {
						return err
					}
				} else if _, err := tx.ExecContext(ctx,
					`UPDATE parental_pins SET failed = $2 WHERE guardian_id = $1`, guardianID, failed); err != nil {
					return err
				}
			}
			if closed := childLockedUntil(append(times, now), PINChildMaxWrong, PINChildWindow, PINChildHold); closed.After(now) {
				out.LockedAccount = true
				if out.Outcome != UnlockLocked || closed.Sub(now) > out.RetryAfter {
					out.RetryAfter = closed.Sub(now)
				}
				out.Outcome = UnlockLocked
				if err := recordEvent(ctx, tx, Event{ChildID: childID, Kind: EventPINLocked,
					SubjectKind: EventSubjectAccount, SubjectID: childID, At: now}, ""); err != nil {
					return err
				}
			}
			return nil
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE parental_pins SET failed = 0, locks = 0, locked_until = NULL WHERE guardian_id = $1`, guardianID); err != nil {
			return err
		}
		end := DayEnd(now, loc).UTC()
		if out.Rule, err = s.addRuleTx(ctx, tx, guardianID, childID, RuleInput{Effect: EffectAllow,
			SubjectKind: kind, SubjectID: id, ExpiresAt: &end}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE parental_requests SET status = 'approved', decided_by = $4, decided_at = $5
			  WHERE child_id = $1 AND subject_kind = $2 AND subject_id = $3 AND status = 'pending'`,
			childID, kind, id, guardianID, now); err != nil {
			return err
		}
		out.Outcome = UnlockOpened
		return recordEvent(ctx, tx, Event{ChildID: childID, Kind: EventApproved, SubjectKind: kind, SubjectID: id,
			ActorID: guardianID, At: now}, "")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecordSourceLocked notes that PIN entry was closed for the address a child's
// tries came from. The address itself is not kept.
func (s *Store) RecordSourceLocked(ctx context.Context, childID string) error {
	return recordEvent(ctx, s.DB, Event{ChildID: childID, Kind: EventPINLocked, SubjectKind: EventSubjectSource,
		At: s.now()}, "")
}
