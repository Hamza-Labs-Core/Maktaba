package staffauth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
)

var testKey = bytes.Repeat([]byte("k"), 32)

func TestStaffTokens(t *testing.T) {
	s := NewSigner(testKey)
	now := time.Unix(1_800_000_000, 0)
	tok, err := s.Issue("staff-1", "sess-1", []string{AMRPassword, AMRTOTP}, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, now.Add(9*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "staff-1" || c.SID != "sess-1" || c.Aud != Audience || !c.HasMFA() || c.AuthTime != now.Add(-time.Minute).Unix() {
		t.Fatalf("claims: %+v", c)
	}
	if _, err := s.Verify(tok, now.Add(AccessTTL)); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("after 10 minutes: %v, want expired", err)
	}
	if _, err := NewSigner(bytes.Repeat([]byte("x"), 32)).Verify(tok, now); !errors.Is(err, ErrTokenSignature) {
		t.Errorf("another key: %v, want a signature error", err)
	}
	// a password alone is not MFA (a test signer could mint it; §3 step 4 refuses it)
	pwOnly, _ := s.Issue("staff-1", "sess-1", []string{AMRPassword}, now, now)
	if c, _ := s.Verify(pwOnly, now); c.HasMFA() {
		t.Error("amr [pwd] counted as MFA")
	}
}

// A customer's access token, even signed with the very same key, is not a staff token: the
// issuer and audience differ, and its header is checked.
func TestACustomerTokenIsNoStaffToken(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	customer, err := token.NewSigner(testKey).Issue("user-1", "u@example.test", "free", "sess-9", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSigner(testKey).Verify(customer, now); err == nil {
		t.Fatal("a customer token verified as a staff token")
	}
	for _, bad := range []string{"", "a.b", "a.b.c", strings.Repeat(".", 2)} {
		if _, err := NewSigner(testKey).Verify(bad, now); err == nil {
			t.Errorf("%q verified", bad)
		}
	}
}

func TestBox(t *testing.T) {
	b, err := NewBox(testKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal("JBSWY3DPEHPK3PXP", "staff-1")
	if err != nil || strings.Contains(sealed, "JBSWY3DPEHPK3PXP") {
		t.Fatalf("seal: %q %v", sealed, err)
	}
	if got, err := b.Open(sealed, "staff-1"); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := b.Open(sealed, "staff-2"); err == nil {
		t.Error("a secret opened for another account")
	}
	if _, err := NewBox([]byte("short")); err == nil {
		t.Error("a short key was accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes()
	if err != nil || len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("%d codes, %d hashes, %v", len(codes), len(hashes), err)
	}
	seen := map[string]bool{}
	for i, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Errorf("code %q", c)
		}
		seen[c] = true
		// typed with capitals and spaces, it still matches its hash
		if argon2id.Verify(NormalizeRecoveryCode(" "+strings.ToUpper(c)+" "), hashes[i]) != nil {
			t.Errorf("code %q does not match its hash", c)
		}
	}
}
