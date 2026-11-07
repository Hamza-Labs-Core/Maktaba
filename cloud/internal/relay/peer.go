package relay

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Relay-to-relay forwarding auth (story 50.04). A relay that forwards a
// request to the peer holding the home's tunnel signs it:
//
//	X-Maktaba-Relay-Hop:  1
//	X-Maktaba-Relay-Auth: <relay_id>.<unix seconds>.<base64url HMAC-SHA256(k_peer, relay_id ‖ slug ‖ unix)>
//	k_peer = HKDF-SHA256(token secret, info "relay-peer-v1")
//
// valid ±30 s. A request that already carries the hop header is never
// forwarded again (one hop at most), and the peer listener refuses (403) one
// without a good signature.

// Peer headers.
const (
	HeaderHop      = "X-Maktaba-Relay-Hop"
	HeaderPeerAuth = "X-Maktaba-Relay-Auth"
)

// PeerSkew is how far a peer signature's time may be from the verifier's.
const PeerSkew = 30 * time.Second

// Peer auth errors (story 50.04 T02).
var (
	ErrPeerAuthMissing = errors.New("relay: peer auth missing")
	ErrPeerAuthStale   = errors.New("relay: peer auth stale")
	ErrPeerAuthBad     = errors.New("relay: peer auth bad")
)

// PeerAuth signs and verifies forwarded requests with the key every relay
// derives from the shared token secret.
type PeerAuth struct {
	key []byte
	now func() time.Time
}

// NewPeerAuth derives the peer key; nil (no secret) refuses everything.
func NewPeerAuth(tokenSecret []byte) *PeerAuth {
	if len(tokenSecret) == 0 {
		return nil
	}
	k, err := hkdf.Key(sha256.New, tokenSecret, nil, "relay-peer-v1", 32)
	if err != nil {
		return nil
	}
	return &PeerAuth{key: k, now: time.Now}
}

func (a *PeerAuth) mac(relayID, slug string, unix int64) string {
	h := hmac.New(sha256.New, a.key)
	h.Write([]byte(relayID))
	h.Write([]byte{0})
	h.Write([]byte(slug))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(unix, 10)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Sign is the X-Maktaba-Relay-Auth value relayID sends for slug now.
func (a *PeerAuth) Sign(relayID, slug string) string {
	if a == nil {
		return ""
	}
	unix := a.now().Unix()
	return relayID + "." + strconv.FormatInt(unix, 10) + "." + a.mac(relayID, slug, unix)
}

// Verify checks a forwarded request's signature for slug and returns the
// relay that sent it.
func (a *PeerAuth) Verify(header, slug string) (string, error) {
	if a == nil || header == "" {
		return "", ErrPeerAuthMissing
	}
	i := strings.LastIndexByte(header, '.')
	if i < 0 {
		return "", ErrPeerAuthBad
	}
	j := strings.LastIndexByte(header[:i], '.')
	if j < 0 {
		return "", ErrPeerAuthBad
	}
	relayID, ts, sum := header[:j], header[j+1:i], header[i+1:]
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return "", ErrPeerAuthBad
	}
	if !hmac.Equal([]byte(sum), []byte(a.mac(relayID, slug, unix))) {
		return "", ErrPeerAuthBad
	}
	if d := a.now().Sub(time.Unix(unix, 0)); d > PeerSkew || d < -PeerSkew {
		return "", ErrPeerAuthStale
	}
	return relayID, nil
}
