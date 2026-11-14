// Package viewers is the broadcast rehearsal's load generator (Epic 50 story 50.29): N simulated
// HLS players against the broadcast edge, each playing, heartbeating, fetching its playlist every
// target duration and each new segment once, spread over several simulated locations. It also
// signs what the rehearsal needs as the cloud and watch.maktaba.tv sign it: ingest tokens and the
// revocation list (Ed25519, the cloud's TEST key) and viewer tokens (HMAC). Rehearsal only: the
// keys come from public labels and are never production keys.
package viewers

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// The token formats of web/packages/broadcast-edge/src/tokens.ts.
const (
	IngestPrefix  = "mkbi1."
	ControlPrefix = "mkbc1."
	ViewerPrefix  = "mkbv1."
	IngestDomain  = "maktaba-broadcast-ingest-v1"
	ControlDomain = "maktaba-broadcast-control-v1"
	ViewerDomain  = "maktaba-broadcast-viewer-v1"
)

// RehearsalViewerKey is the edge's VIEWER_TOKEN_KEY in the rehearsal (base64url): its text says
// what it is. tools/rehearsal-cloud/broadcast/edge/server.mjs gives the edge the same.
const RehearsalViewerKey = "cmVoZWFyc2FsLXZpZXdlci1rZXktbmV2ZXItcHJvZHVjdGlvbg"

// CloudTestKey is the cloud's TEST signing key for a label, as cloud/internal/broadcast's tests
// derive it (testdata/vectors.json carries the public halves).
func CloudTestKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("maktaba-broadcast TEST KEY, never for production: " + label))
	return ed25519.NewKeyFromSeed(seed[:])
}

// Kid is a public key's id: the first 16 hex digits of its SHA-256.
func Kid(pub ed25519.PublicKey) string {
	s := sha256.Sum256(pub)
	return hex.EncodeToString(s[:])[:16]
}

var b64 = base64.RawURLEncoding

// signCloud signs claims as the cloud does: prefix + b64url(JSON) + "." + b64url(sig), the
// signature over "<domain>.<body>".
func signCloud(prefix, domain string, claims any, key ed25519.PrivateKey) (string, error) {
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := b64.EncodeToString(raw)
	sig := ed25519.Sign(key, []byte(domain+"."+body))
	return prefix + body + "." + b64.EncodeToString(sig), nil
}

// IngestClaims are a home's ingest token's claims (story 50.21 §2).
type IngestClaims struct {
	Pub, Channel, Home string
	Rungs              []string
	Kbps               int
	IssuedAt           time.Time
	TTL                time.Duration
}

// IngestToken signs a home's ingest token with key.
func IngestToken(key ed25519.PrivateKey, c IngestClaims) (string, error) {
	iat := c.IssuedAt.Unix()
	ttl := c.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	// the cloud's field order (cloud/internal/broadcast), so a token is the cloud's byte for byte
	return signCloud(IngestPrefix, IngestDomain, struct {
		Typ   string   `json:"typ"`
		Pub   string   `json:"pub"`
		Ch    string   `json:"ch"`
		Rungs []string `json:"rungs"`
		Home  string   `json:"home"`
		Kbps  int      `json:"kbps"`
		Iat   int64    `json:"iat"`
		Exp   int64    `json:"exp"`
		Kid   string   `json:"kid"`
	}{"ingest", c.Pub, c.Channel, c.Rungs, c.Home, c.Kbps, iat, iat + int64(ttl/time.Second),
		Kid(key.Public().(ed25519.PublicKey))}, key)
}

// RevocationEntry is one entry of the kill switch's list (story 50.25 §2).
type RevocationEntry struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Ref    string `json:"ref,omitempty"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	Until  string `json:"until,omitempty"`
}

// RevocationList signs the full list at version, as the cloud pushes it to the edge.
func RevocationList(key ed25519.PrivateKey, version int64, entries []RevocationEntry, at time.Time) (string, error) {
	t := at.Unix()
	if entries == nil {
		entries = []RevocationEntry{}
	}
	return signCloud(ControlPrefix, ControlDomain, map[string]any{
		"schema": 1, "version": version, "typ": "revocations", "entries": entries,
		"issued_at":  time.Unix(t, 0).UTC().Format(time.RFC3339),
		"expires_at": time.Unix(t+48*3600, 0).UTC().Format(time.RFC3339),
		"kid":        Kid(key.Public().(ed25519.PublicKey)), "iat": t, "exp": t + 48*3600,
	}, key)
}

// ViewerClaims are a viewer token's claims (story 50.26): one session on one channel.
type ViewerClaims struct {
	Pub     string `json:"pub"`
	Channel string `json:"ch"`
	Session string `json:"session"`
	Mode    string `json:"mode"`
	Exp     int64  `json:"exp"`
}

// ViewerToken signs a viewer token with the edge's VIEWER_TOKEN_KEY (base64url).
func ViewerToken(secret string, c ViewerClaims) (string, error) {
	key, err := b64.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("viewer key: %w", err)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := b64.EncodeToString(raw)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ViewerDomain + "." + body))
	return ViewerPrefix + body + "." + b64.EncodeToString(m.Sum(nil)), nil
}
