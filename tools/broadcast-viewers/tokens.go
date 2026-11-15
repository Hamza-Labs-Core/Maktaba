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
	Since  string `json:"since,omitempty"`
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

// ControlPost signs a cloud control post of type typ (cloud/internal/broadcast.SignControl): the
// payload's fields plus typ, kid, iat and exp, valid ttl from at.
func ControlPost(key ed25519.PrivateKey, typ string, payload map[string]any, at time.Time, ttl time.Duration) (string, error) {
	p := make(map[string]any, len(payload)+4)
	for k, v := range payload {
		p[k] = v
	}
	p["typ"] = typ
	p["kid"] = Kid(key.Public().(ed25519.PublicKey))
	p["iat"] = at.Unix()
	p["exp"] = at.Add(ttl).Unix()
	return signCloud(ControlPrefix, ControlDomain, p, key)
}

// Plan is a publisher's plan state as the cloud pushes it to PUT /v1/control/plan
// (cloud/internal/broadcast.Plan): the mode a NEW viewer session gets.
type Plan struct {
	Pub        string `json:"pub"`
	Version    int64  `json:"version"`
	Mode       string `json:"mode"`
	MaxRungs   int    `json:"max_rungs,omitempty"`
	MaxTopKbps int    `json:"max_top_kbps,omitempty"`
	MaxViewers int    `json:"max_viewers,omitempty"`
}

// PlanPost signs a plan push (the cloud's PlanTTL, 15 minutes).
func PlanPost(key ed25519.PrivateKey, p Plan, at time.Time) (string, error) {
	m := map[string]any{"pub": p.Pub, "version": p.Version, "mode": p.Mode}
	if p.MaxRungs > 0 {
		m["max_rungs"] = p.MaxRungs
	}
	if p.MaxTopKbps > 0 {
		m["max_top_kbps"] = p.MaxTopKbps
	}
	if p.MaxViewers > 0 {
		m["max_viewers"] = p.MaxViewers
	}
	return ControlPost(key, "plan", m, at, 15*time.Minute)
}

// AccessChannel is one channel's viewer access (cloud/internal/broadcast.AccessChannel).
type AccessChannel struct {
	Access     string `json:"access"`
	MaxViewers int    `json:"max_viewers"`
	Indexable  bool   `json:"indexable"`
}

// AccessState is a publisher's viewer access as the cloud pushes it to PUT /v1/control/access
// (cloud/internal/broadcast.AccessState).
type AccessState struct {
	Pub              string                   `json:"pub"`
	Version          int64                    `json:"version"`
	Channels         map[string]AccessChannel `json:"channels"`
	OpenAllowed      bool                     `json:"open_allowed"`
	MaxSessionsPerIP int                      `json:"max_sessions_per_ip"`
	DailyMinutes     int                      `json:"daily_minutes"`
	RevokedInvites   []string                 `json:"revoked_invites"`
	RevokedPasses    []string                 `json:"revoked_passes"`
	Codes            map[string]string        `json:"codes"`
}

// AccessPost signs an access state (the cloud's AccessTTL is a day).
func AccessPost(key ed25519.PrivateKey, s AccessState, at time.Time) (string, error) {
	if s.Channels == nil {
		s.Channels = map[string]AccessChannel{}
	}
	if s.RevokedInvites == nil {
		s.RevokedInvites = []string{}
	}
	if s.RevokedPasses == nil {
		s.RevokedPasses = []string{}
	}
	if s.Codes == nil {
		s.Codes = map[string]string{}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	return ControlPost(key, "access", m, at, 24*time.Hour)
}

// The invite format of web/packages/broadcast-edge/src/invites.ts.
const (
	InvitePrefix = "mkbn1."
	InviteDomain = "maktaba-broadcast-invite-v1"
)

// InviteClaims are an invite's claims (cloud/internal/broadcast.InviteClaims): who publishes,
// the channels it opens (none: all), its id and how many devices may redeem it.
type InviteClaims struct {
	Pub      string
	Channels []string
	Inv      string
	Max      int
	IssuedAt time.Time
	TTL      time.Duration
}

// Invite signs an invite as the cloud's IssueInvite does, in its field order.
func Invite(key ed25519.PrivateKey, c InviteClaims) (string, error) {
	if c.Pub == "" || c.Inv == "" || c.Max < 1 {
		return "", fmt.Errorf("invite: pub, inv and max are needed")
	}
	ttl := c.TTL
	if ttl == 0 {
		ttl = 30 * 24 * time.Hour
	}
	chs := c.Channels
	if chs == nil {
		chs = []string{}
	}
	iat := c.IssuedAt.Unix()
	return signCloud(InvitePrefix, InviteDomain, struct {
		Typ string   `json:"typ"`
		Pub string   `json:"pub"`
		Chs []string `json:"chs"`
		Inv string   `json:"inv"`
		Max int      `json:"max"`
		Iat int64    `json:"iat"`
		Exp int64    `json:"exp"`
		Kid string   `json:"kid"`
	}{"invite", c.Pub, chs, c.Inv, c.Max, iat, iat + int64(ttl/time.Second), Kid(key.Public().(ed25519.PublicKey))}, key)
}

// The edge's signature on its posts to the cloud (web/packages/broadcast-edge/src/edge_sign.ts,
// cloud/internal/broadcast.VerifyEdgeRequest).
const (
	EdgeDomain          = "maktaba-broadcast-edge-v1"
	HeaderEdgeTime      = "X-Maktaba-Edge-Time"
	HeaderEdgeSignature = "X-Maktaba-Edge-Signature"
)

// RehearsalEdgeSeed is the edge's EDGE_SIGNING_KEY in the rehearsal (a base64url Ed25519 seed,
// the SHA-256 of a label that says what it is). server.mjs gives the edge the same.
var RehearsalEdgeSeed = func() string {
	s := sha256.Sum256([]byte("maktaba-broadcast rehearsal edge key, never for production"))
	return b64.EncodeToString(s[:])
}()

// VerifyEdgePost checks the edge's signature on a post to the cloud, as the cloud does.
func VerifyEdgePost(pub ed25519.PublicKey, method, path, ms, sigText string, body []byte) bool {
	sum := sha256.Sum256(body)
	sig, err := b64.DecodeString(sigText)
	if err != nil {
		return false
	}
	in := EdgeDomain + "\n" + method + "\n" + path + "\n" + ms + "\n" + hex.EncodeToString(sum[:])
	return ed25519.Verify(pub, []byte(in), sig)
}

// EdgePublicKey is the public half of a base64url seed.
func EdgePublicKey(seed string) (ed25519.PublicKey, error) {
	raw, err := b64.DecodeString(seed)
	if err != nil || len(raw) != ed25519.SeedSize {
		return nil, fmt.Errorf("edge seed: %v", err)
	}
	return ed25519.NewKeyFromSeed(raw).Public().(ed25519.PublicKey), nil
}
