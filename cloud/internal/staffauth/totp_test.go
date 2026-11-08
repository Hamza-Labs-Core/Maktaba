package staffauth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B's SHA-1 vectors (the 20-byte ASCII secret "12345678901234567890"),
// cut to 6 digits.
func TestTOTPMatchesRFC6238(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{
		59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037",
	} {
		got, err := TOTPCode(secret, time.Unix(unix, 0))
		if err != nil || got != want {
			t.Errorf("at %d: %q %v, want %q", unix, got, err, want)
		}
	}
}

// T04: the right code; the previous step's (drift); the same step twice; two steps old.
func TestVerifyTOTP(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_015, 0)
	code := func(at time.Time) string { c, _ := TOTPCode(secret, at); return c }

	step, ok := VerifyTOTP(secret, code(now), now, 0)
	if !ok || step != TOTPStep(now) {
		t.Fatalf("the right code: %d %v", step, ok)
	}
	if _, ok := VerifyTOTP(secret, code(now), now, step); ok {
		t.Error("the same step twice was accepted")
	}
	prev := now.Add(-30 * time.Second)
	if s, ok := VerifyTOTP(secret, code(prev), now, 0); !ok || s != TOTPStep(prev) {
		t.Error("the previous step (clock drift) was refused")
	}
	if _, ok := VerifyTOTP(secret, code(now.Add(-60*time.Second)), now, 0); ok {
		t.Error("a code two steps old was accepted")
	}
	// a later step after an earlier one is fine; an earlier one after a later is not
	if _, ok := VerifyTOTP(secret, code(prev), now, TOTPStep(now)); ok {
		t.Error("an older step after a newer one was accepted")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := VerifyTOTP(secret, bad, now, 0); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestTOTPURI(t *testing.T) {
	u := TOTPURI("JBSWY3DPEHPK3PXP", "ops@example.test")
	for _, want := range []string{"otpauth://totp/Maktaba%20console:ops@example.test?", "secret=JBSWY3DPEHPK3PXP", "digits=6", "period=30"} {
		if !strings.Contains(u, want) {
			t.Errorf("%s lacks %s", u, want)
		}
	}
}
