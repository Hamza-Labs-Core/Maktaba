package oauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testIDP struct {
	rsa *rsa.PrivateKey
	ec  *ecdsa.PrivateKey
	srv *httptest.Server
}

func newTestIDP(t *testing.T) *testIDP {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p := &testIDP{rsa: rk, ec: ek}
	b64 := base64.RawURLEncoding.EncodeToString
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":"r1","n":%q,"e":%q},{"kty":"EC","kid":"e1","crv":"P-256","x":%q,"y":%q}]}`,
			b64(rk.N.Bytes()), b64(big.NewInt(int64(rk.E)).Bytes()), b64(ek.X.FillBytes(make([]byte, 32))), b64(ek.Y.FillBytes(make([]byte, 32))))
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *testIDP) token(alg, kid string, claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid})
	c, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	d := sha256.Sum256([]byte(in))
	var sig []byte
	if alg == "RS256" {
		sig, _ = rsa.SignPKCS1v15(rand.Reader, p.rsa, crypto.SHA256, d[:])
	} else {
		r, s, _ := ecdsa.Sign(rand.Reader, p.ec, d[:])
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func claims(over map[string]any) map[string]any {
	c := map[string]any{"iss": "https://issuer.test", "aud": "client-1", "sub": "s-1", "exp": time.Now().Add(time.Hour).Unix(),
		"nonce": "n-1", "email": "a@example.test", "email_verified": true}
	for k, v := range over {
		c[k] = v
	}
	return c
}

func TestIDTokenVerifier(t *testing.T) {
	p := newTestIDP(t)
	v := &IDTokenVerifier{JWKSURL: p.srv.URL, Issuers: []string{"https://issuer.test"}, Audience: "client-1"}
	ctx := context.Background()

	id, err := v.Verify(ctx, p.token("RS256", "r1", claims(nil)), "n-1")
	if err != nil || id.Subject != "s-1" || !id.EmailVerified {
		t.Fatalf("RS256: %+v %v", id, err)
	}
	// Apple: ES256, email_verified as a string, audience as a list.
	id, err = v.Verify(ctx, p.token("ES256", "e1", claims(map[string]any{"email_verified": "true", "aud": []string{"client-1"}})), "n-1")
	if err != nil || !id.EmailVerified {
		t.Fatalf("ES256: %+v %v", id, err)
	}
	if id, _ := v.Verify(ctx, p.token("RS256", "r1", claims(map[string]any{"email_verified": "false"})), "n-1"); id.EmailVerified {
		t.Fatal(`"false" read as verified`)
	}

	bad := map[string]string{
		"wrong audience": p.token("RS256", "r1", claims(map[string]any{"aud": "someone-else"})),
		"wrong issuer":   p.token("RS256", "r1", claims(map[string]any{"iss": "https://evil.test"})),
		"expired":        p.token("RS256", "r1", claims(map[string]any{"exp": time.Now().Add(-time.Minute).Unix()})),
		"nonce mismatch": p.token("RS256", "r1", claims(map[string]any{"nonce": "other"})),
		"no subject":     p.token("RS256", "r1", claims(map[string]any{"sub": ""})),
		"unknown kid":    p.token("RS256", "zz", claims(nil)),
		"alg none":       strings.Join(strings.Split(p.token("RS256", "r1", claims(nil)), ".")[:2], ".") + ".",
	}
	// Signed by the RSA key but labelled as the EC key.
	bad["key confusion"] = p.token("RS256", "e1", claims(nil))
	// Payload swapped after signing.
	parts := strings.Split(p.token("RS256", "r1", claims(nil)), ".")
	evil, _ := json.Marshal(claims(map[string]any{"sub": "victim"}))
	bad["tampered payload"] = parts[0] + "." + base64.RawURLEncoding.EncodeToString(evil) + "." + parts[2]
	for name, tok := range bad {
		if _, err := v.Verify(ctx, tok, "n-1"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPKCEChallenge(t *testing.T) {
	// RFC 7636 appendix B.
	if got := PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge %s", got)
	}
}
