// Package signedurl is the API's signed-URL minter for the streaming service
// (Story 10.8; verified by Epic 8 Story 8.1's middleware).
//
// A minted URL carries ?sig=<RS256 JWT>:
//
//	/stream/{session_id}/manifest.{m3u8,mpd}  aud=streaming        sub=session_id
//	/stream/direct/{video_id}                 aud=streaming-direct sub=video_id
//
// and, for an account whose playback time the home keeps (Epic 57 story 57.14),
// tls=the stream session whose lease the URL needs: streaming serves such a URL
// only while the API keeps that session's lease alive, and refuses a URL without
// it for an account it holds a limit for. Every URL says which it is (tk "1" kept,
// "0" not kept): streaming refuses a URL that says neither, so a signer that was
// not told how to find out (KeepTime) signs URLs that do not play. Every URL also
// carries
// usr=user_id, lib=[the resource's library] (a singleton, architecture
// §9.4), exp bounded by the session's TTL and MaxTTL, jti and kid. The streaming
// service verifies the signature against the same key, the audience, that the
// subject is the session/video in the path, and that lib[] covers the resource's
// library; it copies the token onto the playlist's child URIs (rendition
// playlists, segments), so one token serves the whole session.
//
// The key is per install (StreamKey): <config dir>/stream-signing.key, shared by
// the machine's API (signs) and streaming role (verifies). A node signs and
// verifies its own sessions with its own key; a session the home serves (the
// via-home fallback) is signed by the home and verified by the home's streaming
// port, so no key ever crosses machines.
package signedurl

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
)

// EnvKeyFile names the per-install stream signing key (a PEM RSA private key).
const EnvKeyFile = "MAKTABA_STREAM_SIGNING_KEY"

// DefaultMaxTTL caps every minted URL (Story 10.8 AC-4, max_signed_url_ttl_sec).
const DefaultMaxTTL = 24 * time.Hour

// Audiences (Epic 8 Story 8.1).
const (
	AudSession = "streaming"
	AudDirect  = "streaming-direct"
)

// ErrKeyUnavailable: no signing key (Story 10.8 EC: signing-unavailable).
var ErrKeyUnavailable = errors.New("signedurl: signing key unavailable")

// Minter signs streaming URLs with one RS256 key.
type Minter struct {
	set    *keys.Set
	MaxTTL time.Duration
	Now    func() time.Time
	// kept says whether an account's playback time is kept (KeepTime).
	kept func(userID string) (bool, error)
}

// ErrTimeUnknown: whether the account's time is kept could not be read, so no URL
// is signed for it (a URL without the lease claim would play past a limit).
var ErrTimeUnknown = errors.New("signedurl: the account's time rules could not be read")

// KeepTime makes every URL signed from now on say whether its account's time is
// kept (tk), and carry the tls claim (the stream session it was signed for) when it
// is. When kept fails nothing is signed for that account. Without it a URL says
// neither, and streaming refuses it. Set once, before the first URL is signed.
func (m *Minter) KeepTime(kept func(userID string) (bool, error)) {
	if m != nil {
		m.kept = kept
	}
}

// New wraps a key set (its active key signs).
func New(set *keys.Set) *Minter { return &Minter{set: set} }

// FromKey wraps a single key.
func FromKey(k *keys.Key) *Minter {
	s := keys.NewSet(0)
	s.Replace(k)
	return New(s)
}

// KID is the signing key's id ("" without a key).
func (m *Minter) KID() string {
	if m == nil || m.set == nil || m.set.Active() == nil {
		return ""
	}
	return m.set.Active().KID
}

var (
	directRe  = regexp.MustCompile(`^/stream/(?:direct|clip)/([0-9a-fA-F-]{36})$`)
	sessionRe = regexp.MustCompile(`^/stream/([0-9a-fA-F-]{36})/`)
)

// SignStreamURL signs a streaming path the streaming service handed out for a
// session: the direct-play URL is signed for its video, every other one for the
// session. ttl is clamped to MaxTTL. Returns the signed URL and its expiry.
func (m *Minter) SignStreamURL(rawURL, sessionID, userID, libraryID string, ttl time.Duration) (string, time.Time, error) {
	if m == nil || m.set == nil || m.set.Active() == nil {
		return "", time.Time{}, ErrKeyUnavailable
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signedurl: %w", err)
	}
	aud, sub := AudSession, sessionID
	if mm := directRe.FindStringSubmatch(u.Path); mm != nil {
		aud, sub = AudDirect, strings.ToLower(mm[1])
	} else if mm := sessionRe.FindStringSubmatch(u.Path); mm != nil && !strings.EqualFold(mm[1], sessionID) {
		return "", time.Time{}, fmt.Errorf("signedurl: %s is not session %s's URL", u.Path, sessionID)
	}
	if sub == "" || userID == "" || libraryID == "" {
		return "", time.Time{}, errors.New("signedurl: session, user and library are required")
	}
	maxTTL := m.MaxTTL
	if maxTTL <= 0 {
		maxTTL = DefaultMaxTTL
	}
	if ttl <= 0 || ttl > maxTTL {
		ttl = maxTTL
	}
	now := time.Now().UTC()
	if m.Now != nil {
		now = m.Now().UTC()
	}
	exp := now.Add(ttl)
	claims := jwt.Claims{
		Iss: "maktaba", Aud: aud, Sub: sub, Iat: now.Unix(), Exp: exp.Unix(),
		Usr: userID, Lib: []string{libraryID},
	}
	if m.kept != nil {
		on, err := m.kept(userID)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("%w: %v", ErrTimeUnknown, err)
		}
		claims.Tk = "0"
		if on {
			claims.Tk = "1"
			// the caller's session id: a stream session the home opened has a
			// lease; anything else (a review clip's video id) has none, so
			// streaming refuses it
			claims.TLS = strings.ToLower(sessionID)
			if claims.TLS == "" {
				return "", time.Time{}, errors.New("signedurl: a kept account's URL needs its stream session")
			}
		}
	}
	tok, err := jwt.Sign(m.set, claims)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %v", ErrKeyUnavailable, err)
	}
	q := u.Query()
	q.Set("sig", tok)
	u.RawQuery = q.Encode()
	return u.String(), time.Unix(exp.Unix(), 0).UTC(), nil
}

// SignManifestURL is the session manifest's URL (Story 10.8 AC-1).
func (m *Minter) SignManifestURL(sessionID, userID, libraryID string, ttl time.Duration) (string, time.Time, error) {
	return m.SignStreamURL("/stream/"+sessionID+"/manifest.m3u8", sessionID, userID, libraryID, ttl)
}

// LoadOrCreateKey reads the PEM RSA private key at path, creating a 2048-bit
// one (mode 0600) when the file does not exist yet.
func LoadOrCreateKey(path string) (*keys.Key, bool, error) {
	if b, err := os.ReadFile(path); err == nil {
		k, err := ParseKey(b)
		return k, false, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, false, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, false, err
	}
	// another process may have written it meanwhile: keep the first one
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(tmp)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, false, err
		}
		k, err := ParseKey(b)
		return k, false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	k, err := ParseKey(b)
	return k, true, err
}

// ParseKey parses a PEM RSA private key (PKCS#8 or PKCS#1).
func ParseKey(b []byte) (*keys.Key, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("signedurl: no PEM block in the stream signing key")
	}
	var priv *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		priv = k
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("signedurl: the stream signing key is not RSA")
		}
		priv = rk
	default:
		return nil, fmt.Errorf("signedurl: unsupported PEM block %q", block.Type)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return keys.FromPEM(string(pem.EncodeToMemory(block)),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})))
}
