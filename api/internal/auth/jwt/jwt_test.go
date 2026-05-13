package jwt

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
)

// RSA keygen is the dominant test cost; share keys via sync.OnceValues (cf. keys_test.go).
var (
	sharedKey1 = sync.OnceValues(func() (*keys.Key, error) { return keys.Generate(keys.MinBits) })
	sharedKey2 = sync.OnceValues(func() (*keys.Key, error) { return keys.Generate(keys.MinBits) })
	sharedKey3 = sync.OnceValues(func() (*keys.Key, error) { return keys.Generate(keys.MinBits) })
)

func mustGen(t *testing.T) *keys.Key {
	t.Helper()
	k, err := sharedKey1()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return k
}

func mustGen2(t *testing.T) *keys.Key {
	t.Helper()
	k, err := sharedKey2()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return k
}

func mustGen3(t *testing.T) *keys.Key {
	t.Helper()
	k, err := sharedKey3()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return k
}

func newSet(t *testing.T) *keys.Set {
	t.Helper()
	s := keys.NewSet(time.Hour)
	s.Replace(mustGen(t))
	return s
}

func TestSignVerify_RoundTrip(t *testing.T) {
	s := newSet(t)
	in := Claims{
		Iss: "maktaba", Aud: "api", Sub: "user-123", Usr: "user-123",
		Lib: []string{"lib-a", "lib-b"}, IsAdmin: true,
	}
	tok, err := Sign(s, in)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if strings.Count(tok, ".") != 2 {
		t.Errorf("token should have two dots, got %q", tok)
	}
	out, err := Verify(s, tok, "api")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Sub != in.Sub || out.Aud != "api" || !out.IsAdmin {
		t.Errorf("decoded claims = %+v", out)
	}
	if len(out.Lib) != 2 || out.Lib[0] != "lib-a" {
		t.Errorf("lib = %v", out.Lib)
	}
	if out.Jti == "" {
		t.Error("Jti should be auto-filled")
	}
}

func TestVerify_RejectsTamperedSignature(t *testing.T) {
	s := newSet(t)
	tok, err := Sign(s, Claims{Iss: "maktaba", Aud: "api", Sub: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// Replace the signature with one that is well-formed base64 but
	// definitely not a real signature. Flipping a single byte was
	// flaky: if the chosen byte already matched the substitute char,
	// the input was unchanged.
	parts := strings.Split(tok, ".")
	parts[2] = strings.Repeat("A", len(parts[2]))
	bad := strings.Join(parts, ".")
	if _, err := Verify(s, bad, "api"); err != ErrSignature {
		t.Errorf("tampered sig: got %v, want ErrSignature", err)
	}
}

func TestVerify_RejectsUnknownKID(t *testing.T) {
	signer := newSet(t)
	tok, err := Sign(signer, Claims{Iss: "maktaba", Aud: "api", Sub: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// `other` must hold a distinct key from `signer`, so build it from
	// the second cached key rather than reusing newSet's first cache.
	other := keys.NewSet(time.Hour)
	other.Replace(mustGen2(t))
	if _, err := Verify(other, tok, "api"); err != ErrUnknownKID {
		t.Errorf("unknown kid: got %v, want ErrUnknownKID", err)
	}
}

func TestVerify_AcceptsTokensSignedByPreviousKey(t *testing.T) {
	s := newSet(t)
	tok, err := Sign(s, Claims{Iss: "maktaba", Aud: "api", Sub: "x"})
	if err != nil {
		t.Fatal(err)
	}

	s.Rotate(mustGen2(t), keys.RotateRoutine) // old key slides to previous
	if _, err := Verify(s, tok, "api"); err != nil {
		t.Errorf("token signed by previous key should still verify during overlap: %v", err)
	}

	// Immediate rotation invalidates the previous key.
	s.Rotate(mustGen3(t), keys.RotateImmediate)
	if _, err := Verify(s, tok, "api"); err == nil {
		t.Errorf("immediate rotation should invalidate the previous key's tokens")
	}
}

func TestVerify_Expired(t *testing.T) {
	s := newSet(t)
	c := Claims{Iss: "maktaba", Aud: "api", Sub: "x", Iat: time.Now().Add(-1 * time.Hour).Unix(), Exp: time.Now().Add(-30 * time.Minute).Unix()}
	tok, err := Sign(s, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(s, tok, "api"); err != ErrExpired {
		t.Errorf("expired: got %v, want ErrExpired", err)
	}
}

func TestVerify_AudienceMismatch(t *testing.T) {
	s := newSet(t)
	tok, err := Sign(s, Claims{Iss: "maktaba", Aud: "api", Sub: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(s, tok, "streaming"); err != ErrAudience {
		t.Errorf("audience: got %v, want ErrAudience", err)
	}
}

func TestVerify_RejectsHS256(t *testing.T) {
	// Hand-craft a token with alg=HS256: make sure we never accept it
	// even by accident, even when the signature would otherwise pass.
	// Easiest: take a real token and rewrite the header.
	s := newSet(t)
	tok, err := Sign(s, Claims{Iss: "maktaba", Aud: "api", Sub: "x"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	// Substitute the header with one whose alg=HS256.
	hs256Hdr := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // {"alg":"HS256","typ":"JWT"}
	parts[0] = hs256Hdr
	bad := strings.Join(parts, ".")
	if _, err := Verify(s, bad, "api"); err != ErrUnsupportedAlg {
		t.Errorf("HS256: got %v, want ErrUnsupportedAlg", err)
	}
}

func TestVerify_Malformed(t *testing.T) {
	s := newSet(t)
	cases := []string{"", "no-dots", "two.parts", "a.b.c.d"}
	for _, in := range cases {
		if _, err := Verify(s, in, ""); err != ErrMalformed {
			t.Errorf("Verify(%q) = %v, want ErrMalformed", in, err)
		}
	}
}
