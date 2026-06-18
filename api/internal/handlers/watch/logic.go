// Package watch implements Epic 29's watch-session collection and the
// per-user history / activity reads built on top of it.
//
// The lifecycle logic that governs a session — how much watched time a
// heartbeat credits, when a session counts as "completed", and when an
// abandoned session is "interrupted" — lives here as pure functions so
// it is unit-tested without a database (the repo's convention; cf.
// streaming.SessionDebouncer). The HTTP handlers (watch.go) and the
// reaper (reaper.go) delegate to these.
package watch

import "time"

// Session lifecycle states. An append row starts 'active' and lands in
// exactly one terminal state.
const (
	StateActive      = "active"
	StateCompleted   = "completed"
	StateStopped     = "stopped"
	StateInterrupted = "interrupted"
)

const (
	// HeartbeatInterval is the cadence clients POST /api/watch/heartbeat.
	// Informational here; the server never assumes it (it measures the
	// real gap between heartbeats).
	HeartbeatInterval = 30 * time.Second

	// DefaultStaleTimeout is how long an active session may go without a
	// heartbeat before the reaper marks it 'interrupted'. It also caps
	// the watched time a single heartbeat can credit (D3): a gap longer
	// than this is an abandonment to be closed, not time to bank.
	DefaultStaleTimeout = 5 * time.Minute

	// completedThreshold is the fraction of a video that counts as
	// "watched to completion" (matches the streaming progress rule).
	completedThreshold = 95.0
)

// PercentComplete maps a playback position to a 0..100 percentage,
// clamped at both ends. A non-positive duration yields 0 (we cannot know
// the fraction of an unmeasured video).
func PercentComplete(positionSec, durationSec float64) float64 {
	if durationSec <= 0 || positionSec <= 0 {
		return 0
	}
	pct := positionSec / durationSec * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// CreditedSeconds returns the watched time a heartbeat at now should add
// to a session whose previous heartbeat was at prev. It is the real gap,
// clamped to [0, staleTimeout]: a backwards clock credits 0, and a gap
// longer than the stale window is a pause/abandonment and credits only
// up to the window (never the whole gap). This makes the heartbeat — not
// wall-clock between start and stop — the unit of truth for "watched".
func CreditedSeconds(prev, now time.Time, staleTimeout time.Duration) int {
	gap := now.Sub(prev)
	if gap <= 0 {
		return 0
	}
	if gap > staleTimeout {
		gap = staleTimeout
	}
	return int(gap.Seconds())
}

// StopState maps a final percentage to the terminal state for a clean
// stop: completed at/above the threshold, otherwise stopped.
func StopState(percent float64) string {
	if percent >= completedThreshold {
		return StateCompleted
	}
	return StateStopped
}

// IsStale reports whether an active session whose last heartbeat was at
// lastHeartbeat should be reaped as interrupted at now, given the stale
// timeout. The boundary is exclusive: exactly-at-timeout is not yet
// stale.
func IsStale(lastHeartbeat, now time.Time, staleTimeout time.Duration) bool {
	return now.Sub(lastHeartbeat) > staleTimeout
}

// staleTimeoutOr returns d when positive, else the default. Lets callers
// leave the timeout zero-valued and still get sane behaviour.
func staleTimeoutOr(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return DefaultStaleTimeout
}
