package homeassert

import (
	"crypto/ed25519"
	"testing"
	"time"
)

// The golden vector the home's verifier (api/internal/auth/cloudassert)
// checks too: both ends must agree byte for byte.
const (
	goldenSeedHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	goldenPubB64  = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
	goldenToken   = "mkha1.eyJ0eXAiOiJob21lLWFzc2VydGlvbiIsInB1ciI6InNpZ25pbiIsInN1YiI6ImNsb3VkLXVzZXItMSIsImF1ZCI6InNlcnZlci0xIiwiaWF0IjoxNzAwMDAwMDAwLCJleHAiOjE3MDAwMDAwNjAsImp0aSI6Imp0aS0xIn0.Q_5X6lqSgRnZbx9J_hdGUfXhSJ1FeJaSTFbIK7X53jk326al3CaIOJv1K_p2EEN8XcwojQb53mtazi9XkMu7Dw"
)

func goldenSigner(t *testing.T) *Signer {
	t.Helper()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	return NewSigner(ed25519.NewKeyFromSeed(seed))
}

func goldenPayload() Payload {
	return Payload{Typ: Type, Pur: PurposeSignIn, Sub: "cloud-user-1", Aud: "server-1",
		Iat: 1700000000, Exp: 1700000060, Jti: "jti-1"}
}

func TestGoldenVector(t *testing.T) {
	s := goldenSigner(t)
	if got := s.PublicKeyB64(); got != goldenPubB64 {
		t.Fatalf("public key = %s", got)
	}
	tok, err := s.Sign(goldenPayload())
	if err != nil {
		t.Fatal(err)
	}
	if tok != goldenToken {
		t.Fatalf("token = %s", tok)
	}
}

func TestIssueVerify(t *testing.T) {
	s := goldenSigner(t)
	tok, p, err := s.Issue(PurposeLink, "cu", "sv", "nonce-1", "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(tok, s.PublicKey(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got != p || got.Non != "nonce-1" || got.Exp-got.Iat != 60 {
		t.Fatalf("payload = %+v", got)
	}
	if _, err := Verify(tok, s.PublicKey(), time.Now().Add(2*time.Minute)); err == nil {
		t.Fatal("expired assertion verified")
	}
	other := DeriveFromSeed([]byte("other"))
	if _, err := Verify(tok, other.PublicKey(), time.Now()); err == nil {
		t.Fatal("another key verified the assertion")
	}
	// a flipped payload byte breaks the signature
	bad := tok[:len(Prefix)+3] + "A" + tok[len(Prefix)+4:]
	if _, err := Verify(bad, s.PublicKey(), time.Now()); err == nil {
		t.Fatal("tampered assertion verified")
	}
}

func TestIssueRefusesIncomplete(t *testing.T) {
	s := goldenSigner(t)
	for _, c := range []struct{ pur, sub, aud, non string }{
		{"admin", "cu", "sv", ""},
		{PurposeSignIn, "", "sv", ""},
		{PurposeSignIn, "cu", "", ""},
		{PurposeLink, "cu", "sv", ""},
	} {
		if _, _, err := s.Issue(c.pur, c.sub, c.aud, c.non, ""); err == nil {
			t.Fatalf("issued %+v", c)
		}
	}
}

func TestDeriveIsStableAndSeparate(t *testing.T) {
	seed := make([]byte, 32)
	a, b := DeriveFromSeed(seed), DeriveFromSeed(seed)
	if a.PublicKeyB64() != b.PublicKeyB64() {
		t.Fatal("derivation not deterministic")
	}
	if a.PublicKeyB64() == NewSigner(ed25519.NewKeyFromSeed(seed)).PublicKeyB64() {
		t.Fatal("derived key equals the source key")
	}
}
