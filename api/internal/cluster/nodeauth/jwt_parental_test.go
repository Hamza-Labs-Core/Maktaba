package nodeauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
)

// Epic 57 story 57.04: a node reads the sign-in and the device's age limit from an
// access token as the home does. Without them a device limited by its own settings
// would be unlimited on every machine but the home.
func TestAnAccessTokenCarriesItsSignInAndDeviceLimitOnANode(t *testing.T) {
	g := newOfflineRig(t, true)
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	set := keys.NewSet(0)
	set.Replace(&keys.Key{KID: "k1", Private: priv, Public: &priv.PublicKey})
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1",
		"n": base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())}}})
	g.home.jwks = jwks
	sign := func(c jwt.Claims) string {
		c.Aud, c.Sub, c.Usr, c.Exp = "api", "u1", "u1", time.Now().Add(time.Hour).Unix()
		tok, err := jwt.Sign(set, c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	nine := 9
	p := g.r.verifyJWT(context.Background(), sign(jwt.Claims{Fam: "family-1", Ac: &nine}))
	if p == nil || p.UserID != "u1" || p.RefreshFamily != "family-1" || p.AgeCeiling == nil || *p.AgeCeiling != 9 {
		t.Fatalf("a limited device's token: %+v, want the sign-in family-1 and the limit 9", p)
	}
	zero := 0
	if p := g.r.verifyJWT(context.Background(), sign(jwt.Claims{Fam: "family-2", Ac: &zero})); p == nil || p.AgeCeiling == nil || *p.AgeCeiling != 0 {
		t.Fatalf("a device limited to all ages: %+v, want the limit 0, not none", p)
	}
	if p := g.r.verifyJWT(context.Background(), sign(jwt.Claims{})); p == nil || p.RefreshFamily != "" || p.AgeCeiling != nil {
		t.Fatalf("a token with neither: %+v, want no sign-in and no limit", p)
	}
	// Epic 58 story 58.03: a cloud code's TV (rst) is no admin on a node either
	if p := g.r.verifyJWT(context.Background(), sign(jwt.Claims{IsAdmin: true, Rst: true})); p == nil || p.IsAdmin || p.AccessAllLibraries || !p.MintsNoCredential() {
		t.Fatalf("a restricted admin token: %+v, want no admin", p)
	}
	if p := g.r.verifyJWT(context.Background(), sign(jwt.Claims{IsAdmin: true})); p == nil || !p.IsAdmin || p.Restricted {
		t.Fatalf("an admin token: %+v", p)
	}
}
