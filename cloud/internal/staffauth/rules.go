package staffauth

import (
	"errors"
	"time"
)

// The time rules of story 50.33 §2, §4, §5 and §6, as pure functions of a given now.
const (
	// IdleTimeout: no admin request for this long ends the session (§4).
	IdleTimeout = 15 * time.Minute
	// SessionCap: a session ends this long after its MFA sign-in, whatever the activity (§4).
	SessionCap = 8 * time.Hour
	// ReauthWindow: a sensitive action needs an MFA check this recent (§5).
	ReauthWindow = 5 * time.Minute
	// MFATokenTTL and MFAMaxAttempts bound the step between password and second factor (§2).
	MFATokenTTL    = 5 * time.Minute
	MFAMaxAttempts = 5
	// LockFailures failed steps within LockWindow lock the account for LockFor (§6).
	LockFailures = 5
	LockWindow   = 15 * time.Minute
	LockFor      = 30 * time.Minute
	// EnrolTTL is an enrolment link's life (§1).
	EnrolTTL = 24 * time.Hour
)

var (
	// ErrSessionIdle and ErrSessionCapped are the two ways a live session ends by time.
	ErrSessionIdle   = errors.New("staffauth: session idle")
	ErrSessionCapped = errors.New("staffauth: session past its cap")
)

// SessionTimes is what the time rules read from a session.
type SessionTimes struct {
	SignedInAt   time.Time // the MFA sign-in
	LastActiveAt time.Time // the last admin request (or the sign-in)
}

// CheckSession is ErrSessionCapped past the cap, ErrSessionIdle past the idle timeout, else nil.
func CheckSession(s SessionTimes, now time.Time) error {
	if now.Sub(s.SignedInAt) >= SessionCap {
		return ErrSessionCapped
	}
	if now.Sub(s.LastActiveAt) >= IdleTimeout {
		return ErrSessionIdle
	}
	return nil
}

// ReauthFresh is §5's window: the token's last MFA check is under five minutes old.
func ReauthFresh(authTime, now time.Time) bool {
	return !authTime.IsZero() && now.Sub(authTime) < ReauthWindow
}

// Lockout is an account's failure state (§6).
type Lockout struct {
	Failures    []time.Time // failed steps, oldest first
	LockedUntil time.Time
}

// Locked reports whether the account is locked at now, and until when.
func (l Lockout) Locked(now time.Time) (bool, time.Time) {
	return now.Before(l.LockedUntil), l.LockedUntil
}

// Fail records a failed step at now: the failures older than the window drop out, and the
// LockFailures-th within it locks the account for LockFor. It reports whether this failure
// locked it.
func (l *Lockout) Fail(now time.Time) bool {
	kept := l.Failures[:0]
	for _, f := range l.Failures {
		if now.Sub(f) < LockWindow {
			kept = append(kept, f)
		}
	}
	l.Failures = append(kept, now)
	if len(l.Failures) >= LockFailures {
		l.LockedUntil = now.Add(LockFor)
		l.Failures = nil
		return true
	}
	return false
}
