// Package recovery is the recovery console (Epic 53 story 53.04): /recovery on the web
// port, served by the supervisor from its own binary, never through the API, so an admin
// can see what went wrong and fix it with the API or Postgres down.
//
// Sign-in needs neither (README D4): a recovery pass (a JWT the API gives each signed-in
// admin while it is up, in a cookie scoped to /recovery) or a Remote app's admin access
// token, both checked with the API's public key the supervisor already holds and a
// revocation file the API writes on demotion; or a one-time link the Mac app's tray asks
// the supervisor's loopback admin port for.
package recovery

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Audiences a token may carry: the pass's, and an access token's (a Remote app's bearer).
const (
	AudienceRecovery = "maktaba-recovery"
	AudienceAccess   = "api"
)

// Cookies the console reads: the API's pass, and the console's own session after a
// one-time link.
const (
	CookiePass    = "maktaba_recovery"
	CookieSession = "maktaba_recovery_session"
)

// ErrDenied is every refusal: the caller learns nothing more.
var ErrDenied = errors.New("recovery: not an admin of this home")

// Who signed in.
type Who struct {
	UserID string // "" for the tray
	Tray   bool
}

// Verifier checks passes and bearer tokens without the API or Postgres.
type Verifier struct {
	// PublicKeys are the API's signing keys' public halves (the supervisor's
	// jwt-public.pem, or MAKTABA_JWT_PUBLIC_KEY_PEM).
	PublicKeys func() []*rsa.PublicKey
	// RevokedPath is the API's revocation file (recovery-revoked.json).
	RevokedPath string
	Now         func() time.Time
}

type claims struct {
	Aud     string `json:"aud"`
	Sub     string `json:"sub"`
	Iat     int64  `json:"iat"`
	Exp     int64  `json:"exp"`
	IsAdmin bool   `json:"is_admin"`
}

// Verify checks a pass or an access token: RS256 under one of the API's keys, the right
// audience, unexpired, an admin's, not issued before that admin's revocation.
func (v *Verifier) Verify(tok string) (Who, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || v.PublicKeys == nil {
		return Who{}, ErrDenied
	}
	enc := base64.RawURLEncoding
	var hdr struct {
		Alg string `json:"alg"`
	}
	hb, err := enc.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &hdr) != nil || hdr.Alg != "RS256" {
		return Who{}, ErrDenied
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return Who{}, ErrDenied
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	ok := false
	for _, k := range v.PublicKeys() {
		if k != nil && rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil {
			ok = true
			break
		}
	}
	if !ok {
		return Who{}, ErrDenied
	}
	cb, err := enc.DecodeString(parts[1])
	var c claims
	if err != nil || json.Unmarshal(cb, &c) != nil {
		return Who{}, ErrDenied
	}
	now := v.now().Unix()
	switch {
	case c.Aud != AudienceRecovery && c.Aud != AudienceAccess:
		return Who{}, ErrDenied
	case !c.IsAdmin || c.Sub == "":
		return Who{}, ErrDenied
	case c.Exp == 0 || now > c.Exp:
		return Who{}, ErrDenied
	case v.revokedAfter(c.Sub, c.Iat):
		return Who{}, ErrDenied
	}
	return Who{UserID: c.Sub}, nil
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// revokedAfter: the API recorded a revocation of user at or after a token issued at iat.
func (v *Verifier) revokedAfter(user string, iat int64) bool {
	if v.RevokedPath == "" {
		return false
	}
	b, err := os.ReadFile(v.RevokedPath)
	if err != nil {
		return false
	}
	var list []struct {
		UserID string    `json:"user_id"`
		At     time.Time `json:"at"`
	}
	if json.Unmarshal(b, &list) != nil {
		// a file that cannot be read refuses everyone it might name: fail closed
		return true
	}
	for _, e := range list {
		if e.UserID == user && iat <= e.At.Unix() {
			return true
		}
	}
	return false
}

// PublicKeysFrom reads PEM public keys (PKIX "PUBLIC KEY" blocks) from pem text.
func PublicKeysFrom(text []byte) []*rsa.PublicKey {
	var out []*rsa.PublicKey
	for {
		var b *pem.Block
		b, text = pem.Decode(text)
		if b == nil {
			return out
		}
		if k, err := x509.ParsePKIXPublicKey(b.Bytes); err == nil {
			if rk, ok := k.(*rsa.PublicKey); ok {
				out = append(out, rk)
			}
		}
	}
}

// Codes are the tray's one-time links: 32 random bytes, valid CodeTTL, single use.
type Codes struct {
	Now func() time.Time
	mu  sync.Mutex
	m   map[string]time.Time
}

// CodeTTL is how long a tray link works.
const CodeTTL = 2 * time.Minute

// Issue makes a code.
func (c *Codes) Issue() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	code := hex.EncodeToString(b[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]time.Time{}
	}
	now := c.now()
	for k, exp := range c.m {
		if now.After(exp) {
			delete(c.m, k)
		}
	}
	c.m[code] = now.Add(CodeTTL)
	return code
}

// Redeem uses a code: true once, within its time.
func (c *Codes) Redeem(code string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.m[code]
	if !ok {
		return false
	}
	delete(c.m, code)
	return !c.now().After(exp)
}

func (c *Codes) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Sessions are the console's own cookies after a tray link (12 h), signed with a key that
// lives in memory only: a supervisor restart ends them.
type Sessions struct {
	key  []byte
	once sync.Once
	Now  func() time.Time
}

// SessionTTL is how long a tray sign-in lasts.
const SessionTTL = 12 * time.Hour

func (s *Sessions) k() []byte {
	s.once.Do(func() {
		s.key = make([]byte, 32)
		_, _ = rand.Read(s.key)
	})
	return s.key
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Mint is a session value for the tray.
func (s *Sessions) Mint() string {
	exp := strconv.FormatInt(s.now().Add(SessionTTL).Unix(), 10)
	m := hmac.New(sha256.New, s.k())
	m.Write([]byte("tray|" + exp))
	return "tray|" + exp + "|" + hex.EncodeToString(m.Sum(nil))
}

// Check reads a session value.
func (s *Sessions) Check(v string) bool {
	p := strings.Split(v, "|")
	if len(p) != 3 || p[0] != "tray" {
		return false
	}
	exp, err := strconv.ParseInt(p[1], 10, 64)
	if err != nil || s.now().Unix() > exp {
		return false
	}
	m := hmac.New(sha256.New, s.k())
	m.Write([]byte("tray|" + p[1]))
	want := hex.EncodeToString(m.Sum(nil))
	return hmac.Equal([]byte(want), []byte(p[2]))
}

// Lockout locks an address after LockFailures failures in a minute, for LockFor.
type Lockout struct {
	Now   func() time.Time
	mu    sync.Mutex
	fails map[string][]time.Time
	until map[string]time.Time
}

// The lockout's numbers.
const (
	LockFailures = 5
	LockFor      = 5 * time.Minute
)

func (l *Lockout) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Locked reports whether addr may not try now.
func (l *Lockout) Locked(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.now().Before(l.until[addr])
}

// Fail counts a failure from addr.
func (l *Lockout) Fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		l.fails, l.until = map[string][]time.Time{}, map[string]time.Time{}
	}
	now := l.now()
	var recent []time.Time
	for _, t := range l.fails[addr] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	l.fails[addr] = recent
	if len(recent) >= LockFailures {
		l.until[addr] = now.Add(LockFor)
		l.fails[addr] = nil
	}
}
