package sessions

import (
	"testing"
	"time"
)

func TestSession_Active(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name    string
		s       Session
		want    bool
	}{
		{"fresh", Session{ExpiresAt: now.Add(time.Hour)}, true},
		{"expired", Session{ExpiresAt: now.Add(-time.Hour)}, false},
		{"revoked", Session{ExpiresAt: now.Add(time.Hour), RevokedAt: ptrTime(now)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Active(now); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRandomToken_Distinct(t *testing.T) {
	a, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	b, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("randomToken collided")
	}
	if len(a) < 32 {
		t.Errorf("randomToken too short: %d", len(a))
	}
}

func TestDefaults(t *testing.T) {
	if DefaultTTL <= 0 {
		t.Errorf("DefaultTTL = %v", DefaultTTL)
	}
	if TouchDebounce <= 0 {
		t.Errorf("TouchDebounce = %v", TouchDebounce)
	}
	if CSRFTokenLen < 16 {
		t.Errorf("CSRFTokenLen too short: %d", CSRFTokenLen)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
