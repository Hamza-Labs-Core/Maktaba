package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Story 10.11 AC-1 brute-force lockout policy: after LockoutThreshold
// consecutive failed logins a user row is locked for LockoutWindow.
// The threshold/window were always policy-owned here; the underlying
// counter (users.Store.IncrementFailedAttempt) already implemented the
// "lock when count crosses threshold" SQL but was never called by the
// login handler (HLB-398 / HLB-388 dead code). recordFailedLogin wires
// it into the live login path.
const (
	LockoutThreshold = 5
	LockoutWindow    = 900 * time.Second
)

// TypeAccountLocked is the problem-type for a brute-force-locked
// account. Distinct from invalid-credentials so a client can show
// "locked, try later" instead of "wrong password" — Story 10.11 AC-1
// mandates a 423 here, not the generic 401.
const TypeAccountLocked = "https://maktaba.dev/problems/account-locked"

// FailedLogins is the narrow seam the login path uses to drive the
// per-username brute-force counter. *users.Store satisfies it; unit
// tests inject a fake so the wiring is exercised without a DB. A nil
// seam disables increment/reset (used by unit tests that don't touch
// the counter), exactly like the ACL seam's nil handling.
//
// ResetFailedAttempts zeroes the counter on a SUCCESSFUL login so a
// user who once tripped the threshold isn't left one isolated typo
// away from re-lockout (HLB-398 correctness). It lives on the same
// seam as IncrementFailedAttempt because *users.Store owns both and
// they're a matched pair: every login outcome either increments
// (failure) or resets (success).
type FailedLogins interface {
	IncrementFailedAttempt(ctx context.Context, id string, threshold int, lockFor time.Duration) error
	ResetFailedAttempts(ctx context.Context, id string) error
}

// recordFailedLogin bumps the per-username failed-attempt counter using
// the Story 10.11 policy. It is best-effort: an empty id (unknown
// username — no row to lock) and a nil seam are no-ops, and a DB error
// is swallowed so a counter hiccup never turns a failed login into a
// 500 (which would also leak that the username exists).
func (h *Handler) recordFailedLogin(ctx context.Context, userID string) {
	if userID == "" || h.FailedLogins == nil {
		return
	}
	_ = h.FailedLogins.IncrementFailedAttempt(ctx, userID, LockoutThreshold, LockoutWindow)
}

// resetFailedLogin zeroes the per-user failed-attempt counter and drops
// any lockout window after a SUCCESSFUL authentication. Mirrors
// recordFailedLogin's guards: an empty id and a nil seam are no-ops,
// and a store error is swallowed so a counter hiccup never turns a
// successful login into a 500. Without this call a legitimate user who
// once hit the threshold stays pinned at the cap and the next single
// typo instantly re-locks them (HLB-398 correctness).
func (h *Handler) resetFailedLogin(ctx context.Context, userID string) {
	if userID == "" || h.FailedLogins == nil {
		return
	}
	_ = h.FailedLogins.ResetFailedAttempts(ctx, userID)
}

// writeLockedOut emits the 423 account-locked problem. Callers MUST
// still apply padFailDelay so the locked branch is timing-indistinguish
// -able from wrong-password (no enumeration via response latency).
func (h *Handler) writeLockedOut(w http.ResponseWriter, r *http.Request) {
	httperror.Write(w, r, &httperror.Error{
		Type:   TypeAccountLocked,
		Title:  "account locked",
		Status: http.StatusLocked,
		Detail: "too many failed login attempts; the account is temporarily locked",
	})
}
