package cf

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SigningKey is a Stream signing key. PEM is the private key as
// Cloudflare returns it (base64 of the PEM text); it is a secret, kept in
// home_secrets (cf_signing_key) and never sent anywhere.
type SigningKey struct {
	ID      string `json:"id"`
	PEM     string `json:"pem,omitempty"`
	JWK     string `json:"jwk,omitempty"`
	Created string `json:"created,omitempty"`
}

// CreateSigningKey is POST /accounts/{id}/stream/keys.
func (c *Client) CreateSigningKey(ctx context.Context) (SigningKey, error) {
	var k SigningKey
	err := c.do(ctx, http.MethodPost, c.account()+"/stream/keys", map[string]any{}, &k)
	if err == nil && (k.ID == "" || k.PEM == "") {
		err = errors.New("cloudflare: signing key answer without id or pem")
	}
	return k, err
}

// ListSigningKeys lists the account's signing keys (ids only).
func (c *Client) ListSigningKeys(ctx context.Context) ([]SigningKey, error) {
	var out []SigningKey
	err := c.do(ctx, http.MethodGet, c.account()+"/stream/keys", nil, &out)
	return out, err
}

// DeleteSigningKey deletes a key, which invalidates every token it signed.
func (c *Client) DeleteSigningKey(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, c.account()+"/stream/keys/"+url.PathEscape(id), nil, nil)
}

// AccessRule is one entry of a token's accessRules (design §7.6).
type AccessRule struct {
	Type    string   `json:"type"`   // ip.geoip.country | any
	Action  string   `json:"action"` // allow | block
	Country []string `json:"country,omitempty"`
}

// TokenClaims are a Stream signed URL token's claims.
type TokenClaims struct {
	Sub         string       `json:"sub"`
	Kid         string       `json:"kid"`
	Exp         int64        `json:"exp"`
	Nbf         int64        `json:"nbf,omitempty"`
	AccessRules []AccessRule `json:"accessRules,omitempty"`
}

// MaxTokenTTL is Cloudflare's limit on a token's life.
const MaxTokenTTL = 24 * time.Hour

// ParseSigningKey decodes the key's PEM (base64 of PEM text, or PEM text).
func ParseSigningKey(pemField string) (*rsa.PrivateKey, error) {
	text := []byte(pemField)
	if !strings.Contains(pemField, "-----BEGIN") {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pemField))
		if err != nil {
			return nil, fmt.Errorf("cf: signing key is neither PEM nor base64 PEM: %w", err)
		}
		text = b
	}
	block, _ := pem.Decode(text)
	if block == nil {
		return nil, errors.New("cf: signing key has no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	anyKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cf: signing key: %w", err)
	}
	k, ok := anyKey.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("cf: signing key is not RSA")
	}
	return k, nil
}

// SignToken mints an RS256 token for a video or input id, locally (no API
// call per viewer). exp is capped at now + MaxTokenTTL.
func SignToken(key SigningKey, sub string, ttl time.Duration, now time.Time, rules []AccessRule) (string, error) {
	priv, err := ParseSigningKey(key.PEM)
	if err != nil {
		return "", err
	}
	if ttl <= 0 || ttl > MaxTokenTTL {
		ttl = MaxTokenTTL
	}
	claims := TokenClaims{Sub: sub, Kid: key.ID, Exp: now.Add(ttl).Unix(), Nbf: now.Add(-30 * time.Second).Unix(), AccessRules: rules}
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": key.ID, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(hdr) + "." + enc.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// PrivateJWK is a signing key as a private RSA JWK, base64 of its JSON (the
// form of Cloudflare's own jwk field), for a Worker that signs tokens with
// WebCrypto (which imports a JWK, not a PKCS#1 PEM).
func PrivateJWK(pemField, kid string) (string, error) {
	k, err := ParseSigningKey(pemField)
	if err != nil {
		return "", err
	}
	k.Precompute()
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	e := big.NewInt(int64(k.E)).Bytes()
	j := map[string]string{"kty": "RSA", "alg": "RS256", "kid": kid,
		"n": b64(k.N.Bytes()), "e": b64(e), "d": b64(k.D.Bytes()),
		"p": b64(k.Primes[0].Bytes()), "q": b64(k.Primes[1].Bytes()),
		"dp": b64(k.Precomputed.Dp.Bytes()), "dq": b64(k.Precomputed.Dq.Bytes()), "qi": b64(k.Precomputed.Qinv.Bytes())}
	blob, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(blob), nil
}
