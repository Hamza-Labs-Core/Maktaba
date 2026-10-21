package users

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
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
		"":                        false,
		"some random error":       false,
		"pq: duplicate key value": true,
		"UNIQUE constraint failed: users.username": true,
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

// A user created from a hash made by `maktaba-api hash-password` (the
// desktop app's setup) stores that hash as is; a malformed one is refused
// before anything is written.
func TestCreateWithPasswordHash(t *testing.T) {
	cols := []string{"id", "username", "pw_hash", "is_admin", "failed_attempts", "locked_until",
		"created_at", "updated_at", "must_change_password"}
	now := time.Now()
	hash, err := argon2id.Hash("pw", argon2id.DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	db := queuestest.New(&queuestest.Rule{Match: "insert into users", Cols: cols,
		Rows: [][]driver.Value{{"u1", "admin", hash, true, int64(0), nil, now, now, false}}})
	s := New(db.DB)
	u, err := s.Create(context.Background(), CreateInput{Username: "admin", PasswordHash: hash, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	ins := db.Find("insert into users")
	if len(ins) != 1 || ins[0].Args[2] != hash {
		t.Fatalf("stored %v, want the given hash", ins)
	}
	if err := u.VerifyPassword("pw"); err != nil {
		t.Fatalf("the user's password does not verify: %v", err)
	}
	if _, err := s.Create(context.Background(), CreateInput{Username: "x", PasswordHash: "$argon2id$nope"}); err == nil {
		t.Fatal("a malformed hash must be refused")
	}
	if len(db.Find("insert into users")) != 1 {
		t.Fatal("nothing may be written for a malformed hash")
	}
}
