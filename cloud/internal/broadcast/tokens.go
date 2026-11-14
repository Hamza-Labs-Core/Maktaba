// Package broadcast is the cloud's side of the broadcast edge (Epic 50 story 50.21): it signs
// the credentials the edge checks with the cloud's public keys, so no shared secret sits at the
// edge (unlike Epic 34's single HOME_HMAC).
//
//	ingest token  mkbi1.<base64url(payload JSON)>.<base64url(Ed25519 signature)>
//	control post  mkbc1.<base64url(payload JSON)>.<base64url(Ed25519 signature)>
//
// Each signature covers "<domain>." + <base64url(payload)>, the domain naming the kind
// (IngestDomain, ControlDomain), so a signature made for one kind never passes as another, or
// as one of the cloud's other Ed25519 formats.
//
// The payload names its key (kid): the edge's CLOUD_PUBKEYS holds every key it accepts, so a key
// rotates by adding the new public key at the edge, issuing with the new key, then removing the
// old one once the last token signed with it expired (24 h).
//
// The edge's verifier is web/packages/broadcast-edge/src/tokens.ts; both check the vectors in
// testdata/vectors.json (go test -run TestVectors -update rewrites them).
package broadcast

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	IngestPrefix  = "mkbi1."
	ControlPrefix = "mkbc1."
	IngestDomain  = "maktaba-broadcast-ingest-v1"
	ControlDomain = "maktaba-broadcast-control-v1"
	// IngestTTL is an ingest token's life; the home refreshes it every hour (RefreshEvery), so
	// a cloud outage stops no broadcast for up to a day (README §14.8).
	IngestTTL    = 24 * time.Hour
	RefreshEvery = time.Hour
	// MaxRungs bounds a token's rung list whatever a plan says.
	MaxRungs = 4
	// SpeedTestType and SpeedTestTTL: a speed-test token measures the upload before a channel is
	// published (50.22); the edge takes it on /v1/ingest/speedtest only, never for a segment.
	SpeedTestType = "ingest-speedtest"
	SpeedTestTTL  = 10 * time.Minute
)

var b64 = base64.RawURLEncoding

// IngestClaims is what an ingest token binds: one publisher, one channel, its rungs (top
// first) and the plan's top bitrate, for one home.
type IngestClaims struct {
	Typ   string   `json:"typ"`
	Pub   string   `json:"pub"`
	Ch    string   `json:"ch"`
	Rungs []string `json:"rungs"`
	Home  string   `json:"home"`
	Kbps  int      `json:"kbps"`
	// Geo is the channel's country rule with Maktaba's deny list merged in (story 50.23 §5);
	// nil: none. The edge answers 451 geo_blocked to a viewer it keeps out.
	Geo *Geo   `json:"geo,omitempty"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Kid string `json:"kid"`
}

// Geo is an ingest token's country rule: when Allow is set only those countries may watch;
// Deny never may (it wins).
type Geo struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

var isoRE = regexp.MustCompile(`^[A-Z]{2}$`)

// MergeGeo is the rule a token carries: the channel's own (mode allow or deny, its
// countries) and Maktaba's deny list, which a home can never leave out. nil: no rule.
func MergeGeo(mode string, countries, maktabaDeny []string) *Geo {
	clean := func(in []string) []string {
		seen := map[string]bool{}
		var out []string
		for _, c := range in {
			c = strings.ToUpper(strings.TrimSpace(c))
			if isoRE.MatchString(c) && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
		sort.Strings(out)
		return out
	}
	g := &Geo{}
	if mode == "deny" {
		g.Deny = clean(append(append([]string(nil), countries...), maktabaDeny...))
	} else {
		g.Allow = clean(countries)
		g.Deny = clean(maktabaDeny)
	}
	if len(g.Allow) == 0 && len(g.Deny) == 0 {
		return nil
	}
	return g
}

// Key is one of the cloud's broadcast signing keys.
type Key struct {
	Kid  string
	Priv ed25519.PrivateKey
}

// KidFor names a public key: the first 8 bytes of its SHA-256, in hex.
func KidFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// KeyFromSeed makes a key from a 32-byte seed, its kid from its public half.
func KeyFromSeed(seed []byte) (Key, error) {
	if len(seed) != ed25519.SeedSize {
		return Key{}, fmt.Errorf("broadcast: a key seed is %d bytes, not %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return Key{Kid: KidFor(priv.Public().(ed25519.PublicKey)), Priv: priv}, nil
}

// LoadKey reads a key file: the seed as base64url (or standard base64), or 32 raw bytes.
func LoadKey(path string) (Key, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Key{}, fmt.Errorf("broadcast: read key: %w", err)
	}
	if len(b) == ed25519.SeedSize {
		return KeyFromSeed(b)
	}
	text := strings.TrimSpace(string(b))
	seed, err := b64.DecodeString(strings.TrimRight(text, "="))
	if err != nil {
		seed, err = base64.StdEncoding.DecodeString(text)
	}
	if err != nil {
		return Key{}, fmt.Errorf("broadcast: key file %s: not base64: %w", path, err)
	}
	return KeyFromSeed(seed)
}

// PublicKeysJSON is the value of the edge's CLOUD_PUBKEYS secret for these keys.
func PublicKeysJSON(keys ...Key) string {
	m := map[string]string{}
	for _, k := range keys {
		m[k.Kid] = b64.EncodeToString(k.Priv.Public().(ed25519.PublicKey))
	}
	out, _ := json.Marshal(m)
	return string(out)
}

func sign(k Key, prefix, domain string, payload any) (string, error) {
	j, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := b64.EncodeToString(j)
	sig := ed25519.Sign(k.Priv, []byte(domain+"."+body))
	return prefix + body + "." + b64.EncodeToString(sig), nil
}

var (
	rungRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,15}$`)
	// ErrBadClaims is an ingest request the edge would refuse anyway.
	ErrBadClaims = errors.New("broadcast: bad claims")
)

// IssueIngest signs an ingest token valid IngestTTL from now. Rung names must be the edge's
// (lower-case, at most 16 characters, not "slate"), 1 to MaxRungs of them.
func IssueIngest(k Key, c IngestClaims, now time.Time) (string, error) {
	if c.Pub == "" || c.Ch == "" || c.Home == "" || c.Kbps <= 0 || len(c.Rungs) == 0 || len(c.Rungs) > MaxRungs {
		return "", ErrBadClaims
	}
	seen := map[string]bool{}
	for _, r := range c.Rungs {
		if !rungRE.MatchString(r) || r == "slate" || seen[r] {
			return "", fmt.Errorf("%w: rung %q", ErrBadClaims, r)
		}
		seen[r] = true
	}
	c.Typ = "ingest"
	c.Kid = k.Kid
	c.Iat = now.Unix()
	c.Exp = now.Add(IngestTTL).Unix()
	return sign(k, IngestPrefix, IngestDomain, c)
}

// IssueSpeedTest signs a speed-test token, valid SpeedTestTTL: no rungs and no country rule, so
// it can push nothing a viewer sees.
func IssueSpeedTest(k Key, c IngestClaims, now time.Time) (string, error) {
	if c.Pub == "" || c.Ch == "" || c.Home == "" || c.Kbps <= 0 {
		return "", ErrBadClaims
	}
	c.Typ = SpeedTestType
	c.Rungs = []string{}
	c.Geo = nil
	c.Kid = k.Kid
	c.Iat = now.Unix()
	c.Exp = now.Add(SpeedTestTTL).Unix()
	return sign(k, IngestPrefix, IngestDomain, c)
}

// SignControl signs a control post of one type (plan, 50.20; revocations, 50.25) for the
// edge's /v1/control/<typ>. typ, kid, iat and exp are set here; ttl bounds how long the edge
// accepts it.
func SignControl(k Key, typ string, payload map[string]any, now time.Time, ttl time.Duration) (string, error) {
	p := map[string]any{}
	for key, v := range payload {
		p[key] = v
	}
	p["typ"] = typ
	p["kid"] = k.Kid
	p["iat"] = now.Unix()
	p["exp"] = now.Add(ttl).Unix()
	return sign(k, ControlPrefix, ControlDomain, p)
}

// VerifyIngest checks an ingest token as the edge does (the tests and the operator's tools).
func VerifyIngest(pubkeys map[string]ed25519.PublicKey, token string, now time.Time) (IngestClaims, error) {
	rest, ok := strings.CutPrefix(token, IngestPrefix)
	if !ok {
		return IngestClaims{}, errors.New("broadcast: not an ingest token")
	}
	body, sigText, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sigText, ".") {
		return IngestClaims{}, errors.New("broadcast: malformed token")
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return IngestClaims{}, err
	}
	var c IngestClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return IngestClaims{}, err
	}
	pub, ok := pubkeys[c.Kid]
	if !ok {
		return IngestClaims{}, errors.New("broadcast: unknown key")
	}
	sig, err := b64.DecodeString(sigText)
	if err != nil || !ed25519.Verify(pub, []byte(IngestDomain+"."+body), sig) {
		return IngestClaims{}, errors.New("broadcast: bad signature")
	}
	if c.Typ != "ingest" || now.Unix() >= c.Exp {
		return IngestClaims{}, errors.New("broadcast: expired or not an ingest token")
	}
	return c, nil
}

// ChannelID is the edge's opaque id for a home's channel: the same home and channel always get
// the same id, and the id says nothing about either (viewers see it in URLs).
func ChannelID(serverID, localChannel string) string {
	sum := sha256.Sum256([]byte("maktaba-broadcast-channel-v1\x00" + serverID + "\x00" + localChannel))
	return hex.EncodeToString(sum[:16])
}
