package cloudlink

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// The golden vector cloud/internal/homeassert signs (its TestGoldenVector):
// the home must accept exactly what the cloud makes.
const (
	goldenPubB64 = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
	goldenToken  = "mkha1.eyJ0eXAiOiJob21lLWFzc2VydGlvbiIsInB1ciI6InNpZ25pbiIsInN1YiI6ImNsb3VkLXVzZXItMSIsImF1ZCI6InNlcnZlci0xIiwiaWF0IjoxNzAwMDAwMDAwLCJleHAiOjE3MDAwMDAwNjAsImp0aSI6Imp0aS0xIn0.Q_5X6lqSgRnZbx9J_hdGUfXhSJ1FeJaSTFbIK7X53jk326al3CaIOJv1K_p2EEN8XcwojQb53mtazi9XkMu7Dw"
)

func goldenPub(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, err := ParsePublicKey(goldenPubB64)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestGoldenAssertionVerifies(t *testing.T) {
	a, err := VerifyAssertion(goldenToken, goldenPub(t), PurposeSignIn, "server-1", time.Unix(1700000010, 0))
	if err != nil {
		t.Fatal(err)
	}
	if a.Sub != "cloud-user-1" || a.Jti != "jti-1" {
		t.Fatalf("payload %+v", a)
	}
}

func TestAssertionRefusals(t *testing.T) {
	pub := goldenPub(t)
	at := time.Unix(1700000010, 0)
	other := make([]byte, ed25519.PublicKeySize)
	other[0] = 1
	cases := map[string]func() error{
		"another home": func() error {
			_, err := VerifyAssertion(goldenToken, pub, PurposeSignIn, "server-2", at)
			return err
		},
		"a link assertion at sign-in, or the reverse": func() error {
			_, err := VerifyAssertion(goldenToken, pub, PurposeLink, "server-1", at)
			return err
		},
		"expired": func() error {
			_, err := VerifyAssertion(goldenToken, pub, PurposeSignIn, "server-1", time.Unix(1700000200, 0))
			return err
		},
		"not yet valid": func() error {
			_, err := VerifyAssertion(goldenToken, pub, PurposeSignIn, "server-1", time.Unix(1699999000, 0))
			return err
		},
		"another key": func() error {
			_, err := VerifyAssertion(goldenToken, ed25519.PublicKey(other), PurposeSignIn, "server-1", at)
			return err
		},
		"no pinned key": func() error {
			_, err := VerifyAssertion(goldenToken, nil, PurposeSignIn, "server-1", at)
			return err
		},
		"tampered payload": func() error {
			i := len(assertionPrefix) + 10
			bad := goldenToken[:i] + string(rune(goldenToken[i]^1)) + goldenToken[i+1:]
			_, err := VerifyAssertion(bad, pub, PurposeSignIn, "server-1", at)
			return err
		},
		"not an assertion": func() error {
			_, err := VerifyAssertion(strings.TrimPrefix(goldenToken, "mkha1."), pub, PurposeSignIn, "server-1", at)
			return err
		},
	}
	for name, f := range cases {
		if err := f(); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

func TestParsePublicKey(t *testing.T) {
	if _, err := ParsePublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("a short key parsed")
	}
}
