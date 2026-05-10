package users

import (
	"testing"
	"time"
)

func TestValidateUsername(t *testing.T) {
	good := []string{"alice", "Bob", "user_42", "tom-cat"}
	bad := []string{"", " leading", "trailing ", "with\tcontrol", string(make([]byte, 65))}
	for _, u := range good {
		if err := validateUsername(u); err != nil {
			t.Errorf("validateUsername(%q): %v", u, err)
		}
	}
	for _, u := range bad {
		if err := validateUsername(u); err == nil {
			t.Errorf("validateUsername(%q): expected error", u)
		}
	}
}

func TestUser_IsLocked(t *testing.T) {
	now := time.Now()
	future := now.Add(5 * time.Minute)
	past := now.Add(-5 * time.Minute)

	u := &User{}
	if u.IsLocked(now) {
		t.Error("user with no LockedUntil should not be locked")
	}
	u.LockedUntil = &future
	if !u.IsLocked(now) {
		t.Error("user with future LockedUntil should be locked")
	}
	u.LockedUntil = &past
	if u.IsLocked(now) {
		t.Error("user with past LockedUntil should not be locked")
	}
}

func TestUser_VerifyPassword_DisabledHashRejects(t *testing.T) {
	u := &User{pwHash: "<unsalted-disabled>"}
	if err := u.VerifyPassword("anything"); err != ErrAuth {
		t.Errorf("disabled hash: got %v, want ErrAuth", err)
	}
}

func TestSentinelAdminID_Constant(t *testing.T) {
	// Asserts the sentinel matches the migration. If this changes, the
	// migration must change too — the constant is referenced both
	// here and in the auth-bypass middleware.
	if SentinelAdminID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("SentinelAdminID drift: got %q", SentinelAdminID)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	cases := map[string]bool{
		"":                                          false,
		"some random error":                         false,
		"pq: duplicate key value":                   true,
		"UNIQUE constraint failed: users.username":  true,
	}
	for s, want := range cases {
		var err error
		if s != "" {
			err = errStr(s)
		}
		if got := isUniqueViolation(err); got != want {
			t.Errorf("isUniqueViolation(%q) = %v, want %v", s, got, want)
		}
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
