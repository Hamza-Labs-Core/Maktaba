package oauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IDTokenVerifier checks an OpenID Connect id_token: the signature
// against the provider's published keys (JWKS), then issuer, audience,
// expiry and — when the flow sent one — the nonce.
type IDTokenVerifier struct {
	JWKSURL  string
	Issuers  []string // accepted `iss` values
	Audience string   // our client id
	HTTP     *http.Client
	Now      func() time.Time

	mu      sync.Mutex
	keys    map[string]crypto.PublicKey
	fetched time.Time
}

// keyCacheTTL bounds how long fetched keys are trusted before a refetch;
// an unknown `kid` also forces one (providers rotate keys).
const keyCacheTTL = time.Hour

// idClaims is the subset of id_token claims we read. email_verified is
// a JSON boolean from Google and a string ("true") from Apple.
type idClaims struct {
	Iss           string   `json:"iss"`
	Sub           string   `json:"sub"`
	Aud           audience `json:"aud"`
	Exp           int64    `json:"exp"`
	Nonce         string   `json:"nonce"`
	Email         string   `json:"email"`
	EmailVerified flexBool `json:"email_verified"`
	Name          string   `json:"name"`
	Picture       string   `json:"picture"`
}

type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	*f = flexBool(s == "true")
	return nil
}

// Verify returns the identity an id_token asserts. wantNonce "" skips
// the nonce check (only for flows that did not send one).
func (v *IDTokenVerifier) Verify(ctx context.Context, idToken, wantNonce string) (Identity, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return Identity{}, errors.New("oauth: malformed id_token")
	}
	hb, err := jwtURLDecode(parts[0])
	if err != nil {
		return Identity{}, errors.New("oauth: bad id_token header")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return Identity{}, errors.New("oauth: bad id_token header")
	}
	key, err := v.key(ctx, hdr.Kid)
	if err != nil {
		return Identity{}, err
	}
	sig, err := jwtURLDecode(parts[2])
	if err != nil {
		return Identity{}, errors.New("oauth: bad id_token signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch hdr.Alg {
	case "RS256":
		pk, ok := key.(*rsa.PublicKey)
		if !ok || rsa.VerifyPKCS1v15(pk, crypto.SHA256, digest[:], sig) != nil {
			return Identity{}, errors.New("oauth: id_token signature invalid")
		}
	case "ES256":
		pk, ok := key.(*ecdsa.PublicKey)
		if !ok || len(sig) != 64 {
			return Identity{}, errors.New("oauth: id_token signature invalid")
		}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(pk, digest[:], r, s) {
			return Identity{}, errors.New("oauth: id_token signature invalid")
		}
	default:
		return Identity{}, fmt.Errorf("oauth: id_token alg %q not accepted", hdr.Alg)
	}
	pb, err := jwtURLDecode(parts[1])
	if err != nil {
		return Identity{}, errors.New("oauth: bad id_token payload")
	}
	var c idClaims
	if err := json.Unmarshal(pb, &c); err != nil {
		return Identity{}, errors.New("oauth: bad id_token payload")
	}
	if !contains(v.Issuers, c.Iss) {
		return Identity{}, fmt.Errorf("oauth: id_token issuer %q not accepted", c.Iss)
	}
	if !contains(c.Aud, v.Audience) {
		return Identity{}, errors.New("oauth: id_token audience mismatch")
	}
	if v.now().Unix() > c.Exp {
		return Identity{}, errors.New("oauth: id_token expired")
	}
	if wantNonce != "" && c.Nonce != wantNonce {
		return Identity{}, errors.New("oauth: id_token nonce mismatch")
	}
	if c.Sub == "" {
		return Identity{}, errors.New("oauth: id_token has no subject")
	}
	return Identity{
		Subject:       c.Sub,
		Email:         c.Email,
		EmailVerified: bool(c.EmailVerified),
		DisplayName:   c.Name,
		AvatarURL:     c.Picture,
	}, nil
}

func (v *IDTokenVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func (v *IDTokenVerifier) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok && v.now().Sub(v.fetched) < keyCacheTTL {
		return k, nil
	}
	keys, err := v.fetch(ctx)
	if err != nil {
		return nil, err
	}
	v.keys, v.fetched = keys, v.now()
	k, ok := keys[kid]
	if !ok {
		return nil, fmt.Errorf("oauth: no key %q in provider JWKS", kid)
	}
	return k, nil
}

func (v *IDTokenVerifier) fetch(ctx context.Context) (map[string]crypto.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	hc := v.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: fetch JWKS: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("oauth: parse JWKS: %w", err)
	}
	out := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		switch k.Kty {
		case "RSA":
			n, err1 := jwtURLDecode(k.N)
			e, err2 := jwtURLDecode(k.E)
			if err1 != nil || err2 != nil {
				continue
			}
			out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			x, err1 := jwtURLDecode(k.X)
			y, err2 := jwtURLDecode(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			out[k.Kid] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		}
	}
	return out, nil
}
