package staffauth

import (
	"errors"
	"testing"
	"time"
)

// T06: 14 min idle then a request; 16 min idle; 8 h 1 min with activity.
func TestCheckSession(t *testing.T) {
	in := time.Unix(1_800_000_000, 0)
	s := SessionTimes{SignedInAt: in, LastActiveAt: in}
	if err := CheckSession(s, in.Add(14*time.Minute)); err != nil {
		t.Errorf("14 min idle: %v", err)
	}
	if err := CheckSession(s, in.Add(16*time.Minute)); !errors.Is(err, ErrSessionIdle) {
		t.Errorf("16 min idle: %v, want idle", err)
	}
	busy := SessionTimes{SignedInAt: in, LastActiveAt: in.Add(8*time.Hour + 55*time.Second)}
	if err := CheckSession(busy, in.Add(8*time.Hour+time.Minute)); !errors.Is(err, ErrSessionCapped) {
		t.Errorf("8 h 1 min with activity: %v, want capped", err)
	}
	if err := CheckSession(busy, in.Add(7*time.Hour+59*time.Minute)); err != nil {
		t.Errorf("7 h 59 min with activity: %v", err)
	}
}

// T07's clock: an MFA check 6 minutes old is stale, 1 minute old is fresh; none never is.
func TestReauthFresh(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	if ReauthFresh(now.Add(-6*time.Minute), now) || !ReauthFresh(now.Add(-time.Minute), now) || ReauthFresh(time.Time{}, now) {
		t.Error("the five-minute window is wrong")
	}
}

// T09's account rule: 5 failures in 15 minutes lock for 30; old failures drop out.
func TestLockout(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var l Lockout
	for i := range 4 {
		if l.Fail(now.Add(time.Duration(i) * time.Minute)) {
			t.Fatalf("locked after %d failures", i+1)
		}
	}
	if !l.Fail(now.Add(4 * time.Minute)) {
		t.Fatal("the fifth failure in 15 minutes did not lock")
	}
	if locked, until := l.Locked(now.Add(5 * time.Minute)); !locked || !until.Equal(now.Add(4*time.Minute+LockFor)) {
		t.Errorf("locked %v until %v", locked, until)
	}
	if locked, _ := l.Locked(now.Add(4*time.Minute + LockFor)); locked {
		t.Error("still locked after 30 minutes")
	}
	// failures spread over more than the window never lock
	var spread Lockout
	for i := range 10 {
		if spread.Fail(now.Add(time.Duration(i) * 4 * time.Minute)) {
			t.Fatalf("locked by failures 4 minutes apart (failure %d)", i+1)
		}
	}
}

func TestSensitiveListsDontOverlap(t *testing.T) {
	for r := range NotSensitive {
		if Sensitive[r] {
			t.Errorf("%s is in both lists", r)
		}
	}
	if !IsSensitive("POST", "/v1/admin/accounts/{id}/suspend") || IsSensitive("GET", "/v1/admin/accounts") {
		t.Error("IsSensitive")
	}
}
