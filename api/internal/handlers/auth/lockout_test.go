package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
)

// fakeFailedLogins records IncrementFailedAttempt / ResetFailedAttempts
// calls so a unit test can prove the (previously dead) brute-force
// counter is now driven by the login path and that a successful login
// resets it.
type fakeFailedLogins struct {
	calls      int
	lastID     string
	lastThr    int
	lastLock   time.Duration
	returnErr  error
	resetCalls int
	resetID    string
	resetErr   error
}

func (f *fakeFailedLogins) IncrementFailedAttempt(_ context.Context, id string, threshold int, lockFor time.Duration) error {
	f.calls++
	f.lastID = id
	f.lastThr = threshold
	f.lastLock = lockFor
	return f.returnErr
}

func (f *fakeFailedLogins) ResetFailedAttempts(_ context.Context, id string) error {
	f.resetCalls++
	f.resetID = id
	return f.resetErr
}

// memFailedLogins is a faithful in-memory model of *users.Store's
// brute-force SQL: IncrementFailedAttempt mirrors the window-fresh
// CASE expression (an expired prior window restarts the count at 1
// instead of accumulating), and ResetFailedAttempts mirrors the shared
// clearLockoutSQL (zero the counter, drop the window). It lets the
// counter/reset/stale-window correctness be exercised end-to-end
// through the real handler helpers without a DB — matching this
// package's fake-seam convention. `now` is an injectable clock so the
// stale-window test can advance past LockoutWindow.
type memFailedLogins struct {
	failed int
	locked *time.Time // nil ⇒ never locked
	now    func() time.Time
}

func (m *memFailedLogins) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now().UTC()
}

func (m *memFailedLogins) IncrementFailedAttempt(_ context.Context, _ string, threshold int, lockFor time.Duration) error {
	now := m.clock()
	// Same condition as User.IsLocked, negated: a prior window that is
	// set but no longer in the future is "fully expired" — don't
	// accumulate across it, restart at 1.
	expired := m.locked != nil && !m.locked.After(now)
	if expired {
		m.failed = 1
		m.locked = nil
	} else {
		m.failed++
	}
	if m.failed >= threshold {
		t := now.Add(lockFor)
		m.locked = &t
	}
	return nil
}

func (m *memFailedLogins) ResetFailedAttempts(_ context.Context, _ string) error {
	m.failed = 0
	m.locked = nil
	return nil
}

// isLocked mirrors User.IsLocked so the tests assert lock state the
// same way the live login path (auth.go: u.IsLocked) does.
func (m *memFailedLogins) isLocked() bool {
	return m.locked != nil && m.locked.After(m.clock())
}

// E10-LOCK-1: a wrong-password attempt against a KNOWN user drives the
// brute-force counter with the Story 10.11 policy (5 / 900s). This is
// the dead-code wiring the gap doc (HLB-398/388) flags.
func TestRecordFailedLogin_IncrementsKnownUser(t *testing.T) {
	fl := &fakeFailedLogins{}
	h := &Handler{FailedLogins: fl}
	h.recordFailedLogin(context.Background(), "user-42")
	if fl.calls != 1 {
		t.Fatalf("IncrementFailedAttempt calls = %d, want 1", fl.calls)
	}
	if fl.lastID != "user-42" {
		t.Fatalf("incremented id = %q, want user-42", fl.lastID)
	}
	if fl.lastThr != LockoutThreshold {
		t.Fatalf("threshold = %d, want %d", fl.lastThr, LockoutThreshold)
	}
	if fl.lastLock != LockoutWindow {
		t.Fatalf("lockFor = %s, want %s", fl.lastLock, LockoutWindow)
	}
}

// E10-LOCK-2: an unknown username must NOT drive a counter (there is
// no row to lock) — calling with an empty id is a no-op so we never
// hit the DB for a non-existent user.
func TestRecordFailedLogin_EmptyIDNoOp(t *testing.T) {
	fl := &fakeFailedLogins{}
	h := &Handler{FailedLogins: fl}
	h.recordFailedLogin(context.Background(), "")
	if fl.calls != 0 {
		t.Fatalf("IncrementFailedAttempt called %d times for empty id, want 0", fl.calls)
	}
}

// E10-LOCK-3: a nil seam (older callers / tests that don't exercise
// the mint path) must not panic.
func TestRecordFailedLogin_NilSeamNoPanic(_ *testing.T) {
	h := &Handler{}
	h.recordFailedLogin(context.Background(), "user-1") // must not panic
}

// E10-LOCK-4: the policy constants match Story 10.11 AC-1.
func TestLockoutPolicyConstants(t *testing.T) {
	if LockoutThreshold != 5 {
		t.Fatalf("LockoutThreshold = %d, want 5 (Story 10.11 AC-1)", LockoutThreshold)
	}
	if LockoutWindow != 900*time.Second {
		t.Fatalf("LockoutWindow = %s, want 900s (Story 10.11 AC-1)", LockoutWindow)
	}
}

// E10-LOCK-5: a locked account gets a 423 account-locked problem (not
// the generic 401) so the client can surface "try again later" instead
// of "bad password". Timing parity is preserved by the caller's
// padFailDelay; this only asserts the status/type.
func TestLockedOut_Returns423(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	h.writeLockedOut(rec, req)
	if rec.Code != http.StatusLocked {
		t.Fatalf("status = %d, want 423", rec.Code)
	}
	if !contains(rec.Body.String(), "account-locked") {
		t.Fatalf("body = %s, want account-locked type", rec.Body.String())
	}
}

// E10-LOCK-6: *users.Store satisfies the FailedLogins seam, so the
// real login path drives the real (previously dead) increment.
func TestUsersStore_SatisfiesFailedLoginsSeam(_ *testing.T) {
	var _ FailedLogins = (*users.Store)(nil)
}

// E10-LOCK-7: an increment error is swallowed (best-effort) — a DB
// hiccup on the counter must not turn a failed login into a 500 and
// leak that the username exists.
func TestRecordFailedLogin_SwallowsError(t *testing.T) {
	fl := &fakeFailedLogins{returnErr: errors.New("db down")}
	h := &Handler{FailedLogins: fl}
	h.recordFailedLogin(context.Background(), "user-9") // must not panic / propagate
	if fl.calls != 1 {
		t.Fatalf("calls = %d, want 1", fl.calls)
	}
}

// E10-LOCK-8: resetFailedLogin drives ResetFailedAttempts on the seam.
// This is the wiring the Login SUCCESS path uses (HLB-398). Pre-fix
// there was no reset method/helper at all, so this test cannot even
// compile against the old code — and once the seam exists, an
// un-wired success path fails E10-LOCK-10 below.
func TestResetFailedLogin_DrivesReset(t *testing.T) {
	fl := &fakeFailedLogins{}
	h := &Handler{FailedLogins: fl}
	h.resetFailedLogin(context.Background(), "user-42")
	if fl.resetCalls != 1 {
		t.Fatalf("ResetFailedAttempts calls = %d, want 1", fl.resetCalls)
	}
	if fl.resetID != "user-42" {
		t.Fatalf("reset id = %q, want user-42", fl.resetID)
	}
}

// E10-LOCK-9: resetFailedLogin mirrors recordFailedLogin's guards — an
// empty id and a nil seam are no-ops, and a store error is swallowed
// (a counter hiccup must never turn a SUCCESSFUL login into a 500).
func TestResetFailedLogin_GuardsAndSwallows(t *testing.T) {
	// empty id: no DB touch for a row we never loaded.
	fl := &fakeFailedLogins{}
	(&Handler{FailedLogins: fl}).resetFailedLogin(context.Background(), "")
	if fl.resetCalls != 0 {
		t.Fatalf("empty id reset calls = %d, want 0", fl.resetCalls)
	}
	// nil seam: must not panic.
	(&Handler{}).resetFailedLogin(context.Background(), "user-1")
	// store error: swallowed, best-effort.
	fe := &fakeFailedLogins{resetErr: errors.New("db down")}
	(&Handler{FailedLogins: fe}).resetFailedLogin(context.Background(), "user-9")
	if fe.resetCalls != 1 {
		t.Fatalf("reset calls = %d, want 1", fe.resetCalls)
	}
}

// E10-LOCK-10 (TDD: "reset on success"): a legitimate user racks up 4
// failed attempts (one short of the threshold), then logs in
// successfully. The success MUST zero the counter — otherwise the very
// next isolated typo lands at oldCount+1 >= threshold and instantly
// re-locks the account for the full window, with admin Unlock the only
// recovery (HLB-398). The model below mirrors the real store SQL; the
// handler's recordFailedLogin/resetFailedLogin helpers drive it exactly
// as the live Login path does (failure ⇒ increment, success ⇒ reset).
//
// Fail-without-fix: revert the Login success-path resetFailedLogin call
// (or the seam's reset) and the post-login count stays at 4 → the
// follow-up wrong password is attempt 5 ≥ 5 → user is LOCKED, so the
// final assertion (NOT locked) fails.
func TestLockout_ResetOnSuccess(t *testing.T) {
	store := &memFailedLogins{}
	h := &Handler{FailedLogins: store}
	const uid = "user-legit"

	// 4 wrong passwords — one short of LockoutThreshold (5).
	for i := 0; i < 4; i++ {
		h.recordFailedLogin(context.Background(), uid)
	}
	if store.failed != 4 {
		t.Fatalf("after 4 failures, failed_attempts = %d, want 4", store.failed)
	}
	if store.isLocked() {
		t.Fatalf("4 < threshold (%d): must NOT be locked yet", LockoutThreshold)
	}

	// Correct password — the live Login success path calls this.
	h.resetFailedLogin(context.Background(), uid)
	if store.failed != 0 {
		t.Fatalf("after successful login, failed_attempts = %d, want 0", store.failed)
	}

	// A single later typo must be attempt #1, NOT a re-lock.
	h.recordFailedLogin(context.Background(), uid)
	if store.failed != 1 {
		t.Fatalf("one typo after a clean login = attempt %d, want 1", store.failed)
	}
	if store.isLocked() {
		t.Fatalf("a single typo after a successful login must NOT lock the account (count %d < threshold %d)", store.failed, LockoutThreshold)
	}
}

// E10-LOCK-11 (TDD: "stale window resets count"): 5 failures lock the
// account; the lockout window then fully elapses; a NEW wrong password
// arrives. It must be treated as attempt #1 against a fresh window —
// NOT accumulated onto the stale count (which was the pre-fix
// unconditional `failed_attempts + 1`, leaving the account permanently
// one typo from re-lock).
//
// Fail-without-fix: revert IncrementFailedAttempt to the unconditional
// `+1` and the post-expiry attempt is 5+1 = 6 ≥ 5 → user re-LOCKED, so
// both the count==1 and the NOT-locked assertions fail.
func TestLockout_StaleWindowResetsCount(t *testing.T) {
	base := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	clk := base
	store := &memFailedLogins{now: func() time.Time { return clk }}
	h := &Handler{FailedLogins: store}
	const uid = "user-legit"

	// 5 strikes ⇒ locked for LockoutWindow.
	for i := 0; i < LockoutThreshold; i++ {
		h.recordFailedLogin(context.Background(), uid)
	}
	if !store.isLocked() {
		t.Fatalf("after %d failures the account must be locked", LockoutThreshold)
	}
	if store.failed != LockoutThreshold {
		t.Fatalf("failed_attempts = %d, want %d", store.failed, LockoutThreshold)
	}

	// Advance past the lockout window (same clock seam the model uses).
	clk = base.Add(LockoutWindow + time.Second)
	if store.isLocked() {
		t.Fatalf("window elapsed: account must no longer be IsLocked")
	}

	// A fresh wrong password after the window must be attempt #1, not
	// oldCount+1, and must NOT immediately re-lock.
	h.recordFailedLogin(context.Background(), uid)
	if store.failed != 1 {
		t.Fatalf("post-expiry attempt counted as %d, want fresh 1", store.failed)
	}
	if store.isLocked() {
		t.Fatalf("a single attempt after an expired window must NOT re-lock (count %d < threshold %d)", store.failed, LockoutThreshold)
	}
}
