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

// fakeFailedLogins records IncrementFailedAttempt calls so a unit test
// can prove the (previously dead) brute-force counter is now driven by
// the login path.
type fakeFailedLogins struct {
	calls     int
	lastID    string
	lastThr   int
	lastLock  time.Duration
	returnErr error
}

func (f *fakeFailedLogins) IncrementFailedAttempt(_ context.Context, id string, threshold int, lockFor time.Duration) error {
	f.calls++
	f.lastID = id
	f.lastThr = threshold
	f.lastLock = lockFor
	return f.returnErr
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
func TestRecordFailedLogin_NilSeamNoPanic(t *testing.T) {
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
func TestUsersStore_SatisfiesFailedLoginsSeam(t *testing.T) {
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
