package security

import (
	"strings"
	"testing"
	"time"
)

// ---- validation -----------------------------------------------------------

func TestValidateUUIDAcceptsLowercase(t *testing.T) {
	if err := ValidateUUID("id", "00000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateUUIDRejectsBad(t *testing.T) {
	for _, bad := range []string{"", "not-a-uuid", "ABC-0000-0000-0000-000000000001"} {
		if err := ValidateUUID("id", bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestValidateSearchQueryRejectsEmpty(t *testing.T) {
	if err := ValidateSearchQuery("q", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateSearchQueryRejectsTooLong(t *testing.T) {
	if err := ValidateSearchQuery("q", strings.Repeat("x", 257)); err == nil {
		t.Fatal("expected length error")
	}
}

func TestValidateSearchQueryRejectsNUL(t *testing.T) {
	if err := ValidateSearchQuery("q", "ab\x00c"); err == nil {
		t.Fatal("expected NUL rejection")
	}
}

func TestValidateLibraryPathRejectsDotDot(t *testing.T) {
	if err := ValidateLibraryPath("p", "subdir/../etc/passwd"); err == nil {
		t.Fatal("expected ..-rejection")
	}
}

func TestValidateLibraryPathRejectsAbsolute(t *testing.T) {
	if err := ValidateLibraryPath("p", "/etc/passwd"); err == nil {
		t.Fatal("expected absolute-path rejection")
	}
}

func TestValidateLangTag(t *testing.T) {
	for _, ok := range []string{"en", "en-US", "ar"} {
		if err := ValidateLangTag("l", ok); err != nil {
			t.Fatalf("%s should be ok: %v", ok, err)
		}
	}
	for _, bad := range []string{"english", "EN", "x"} {
		if err := ValidateLangTag("l", bad); err == nil {
			t.Fatalf("%s should fail", bad)
		}
	}
}

func TestFirstError(t *testing.T) {
	if FirstError(nil, nil) != nil {
		t.Fatal("expected nil")
	}
	want := ValidationError{Field: "x", Message: "bad"}
	if got := FirstError(nil, want, nil); got != want {
		t.Fatal("did not return first non-nil")
	}
}

// ---- rate limit -----------------------------------------------------------

func TestTokenBucketAllowsBurst(t *testing.T) {
	tb := NewTokenBucket(1.0, 3.0)
	now := time.Now()
	tb.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !tb.Allow("k") {
			t.Fatalf("burst exhausted early at i=%d", i)
		}
	}
	if tb.Allow("k") {
		t.Fatal("4th allow should fail")
	}
}

func TestTokenBucketRefills(t *testing.T) {
	tb := NewTokenBucket(2.0, 2.0)
	now := time.Now()
	tb.now = func() time.Time { return now }
	_ = tb.Allow("k")
	_ = tb.Allow("k")
	now = now.Add(time.Second)
	if !tb.Allow("k") {
		t.Fatal("refill failed")
	}
}

func TestTokenBucketKeysAreIndependent(t *testing.T) {
	tb := NewTokenBucket(1.0, 1.0)
	if !tb.Allow("a") {
		t.Fatal()
	}
	if !tb.Allow("b") {
		t.Fatal("b should not be limited by a")
	}
}

func TestTokenBucketPanicsOnBadParams(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = NewTokenBucket(0, 1)
}

// ---- sbom -----------------------------------------------------------------

func TestSBOMLoadValid(t *testing.T) {
	raw := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5","version":1,"metadata":{"timestamp":"2026-01-01T00:00:00Z"},"components":[{"type":"library","name":"chi","version":"5.0.10","purl":"pkg:golang/github.com/go-chi/chi/v5@5.0.10","license":"MIT"}]}`)
	s, err := LoadSBOM(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Summary().TotalComponents; got != 1 {
		t.Fatalf("components: %d", got)
	}
	if c := s.FindByPURL("pkg:golang/github.com/go-chi/chi/v5@5.0.10"); c == nil || c.Name != "chi" {
		t.Fatal("FindByPURL missed")
	}
}

func TestSBOMLoadRejectsBadFormat(t *testing.T) {
	if _, err := LoadSBOM([]byte(`{"bomFormat":"SPDX","specVersion":"1.5","version":1}`)); err == nil {
		t.Fatal("expected CycloneDX-only rejection")
	}
}

func TestSBOMLoadRejectsZeroVersion(t *testing.T) {
	if _, err := LoadSBOM([]byte(`{"bomFormat":"CycloneDX","specVersion":"1.5","version":0}`)); err == nil {
		t.Fatal("expected version=0 rejection")
	}
}

// ---- disclosure -----------------------------------------------------------

func TestDefaultPolicyValidates(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatalf("default invalid: %v", err)
	}
}

func TestDisclosurePolicyValidateRequiresContact(t *testing.T) {
	if err := (DisclosurePolicy{Expires: time.Now()}).Validate(); err == nil {
		t.Fatal("expected contact-required error")
	}
}

func TestSecurityTxtIncludesContactAndExpires(t *testing.T) {
	out := DefaultPolicy().SecurityTxt()
	if !strings.Contains(out, "Contact: mailto:security@maktaba.dev") {
		t.Fatalf("missing contact: %s", out)
	}
	if !strings.Contains(out, "Expires:") {
		t.Fatal("missing Expires")
	}
	if !strings.Contains(out, "Preferred-Languages: en, ar") {
		t.Fatal("missing preferred languages")
	}
}
