package broadcast

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The edge signs its posts to the API (usage reports, 50.20 §3) with EDGE_SIGNING_KEY; the cloud
// keeps the public half (broadcast.edge_public_key) and checks each post with VerifyEdgeRequest.
// The edge's signer is web/packages/broadcast-edge/src/edge_sign.ts.
const (
	EdgeDomain          = "maktaba-broadcast-edge-v1"
	HeaderEdgeTime      = "X-Maktaba-Edge-Time"
	HeaderEdgeSignature = "X-Maktaba-Edge-Signature"
	// EdgeMaxSkew is how far the edge's time may be from the cloud's.
	EdgeMaxSkew = 5 * time.Minute
)

// ErrEdgeSignature is a post the edge did not sign, or signed too long ago.
var ErrEdgeSignature = errors.New("broadcast: bad edge signature")

// EdgeSigningInput is what the edge signs for one request.
func EdgeSigningInput(method, path string, ms int64, body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%s\n%s\n%s\n%d\n%s", EdgeDomain, strings.ToUpper(method), path, ms, hex.EncodeToString(sum[:]))
}

// VerifyEdgeRequest checks a post's edge signature and time.
func VerifyEdgeRequest(pub ed25519.PublicKey, method, path string, h http.Header, body []byte, now time.Time) error {
	ms, err := strconv.ParseInt(h.Get(HeaderEdgeTime), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: no time", ErrEdgeSignature)
	}
	if d := now.Sub(time.UnixMilli(ms)); d > EdgeMaxSkew || d < -EdgeMaxSkew {
		return fmt.Errorf("%w: time off by %s", ErrEdgeSignature, d)
	}
	sig, err := b64.DecodeString(h.Get(HeaderEdgeSignature))
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, []byte(EdgeSigningInput(method, path, ms, body)), sig) {
		return ErrEdgeSignature
	}
	return nil
}
