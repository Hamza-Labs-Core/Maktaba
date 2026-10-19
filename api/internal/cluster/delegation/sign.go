// Package delegation is how a full node acts for a user without ever reading the
// sessions table (Epic 35 story 35.9, design §10): every node → home HTTP request is
// signed with the node's delegation key, a per-node HMAC key the home issues over the
// cluster channel (`keys` frame), keeps sealed in home_secrets, rotates daily and deletes
// when the machine is revoked.
//
//	X-Maktaba-Node:      <node id>
//	X-Maktaba-Node-Key:  <key id>
//	X-Maktaba-Node-Ts:   <unix seconds>
//	X-Maktaba-Node-Nonce: <16 random bytes, base64url>
//	X-Maktaba-Node-Sig:  b64(HMAC-SHA256(key, method ‖ "\n" ‖ path?query ‖ "\n" ‖ ts ‖ "\n" ‖
//	                     nonce ‖ "\n" ‖ sha256(body) ‖ "\n" ‖ delegated-session-hash))
//
// The nonce line is an addition to design §10.2's string: the replay cache keys on
// (node, ts, nonce), so two identical requests within one second (a page loading the
// same resource twice) are both accepted while a replayed one is refused.
//
// sha256(body) and delegated-session-hash are lower-case hex; the body hash of a request
// without a body is the hash of the empty string, a streamed body (an upload, a WebSocket
// upgrade) signs "UNSIGNED-PAYLOAD"; the session hash is empty when no session is
// delegated (X-Maktaba-Delegated-Session absent). Vectors:
// shared/testdata/cluster/delegation_vectors.json.
//
// On the cluster link (story 35.16) the same checks travel on the machine's mTLS control
// channel; this signed HTTP is the legacy path while cluster.link.mode = prefer.
package delegation

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header names.
const (
	HeaderNode                   = "X-Maktaba-Node"
	HeaderKey                    = "X-Maktaba-Node-Key"
	HeaderTs                     = "X-Maktaba-Node-Ts"
	HeaderNonce                  = "X-Maktaba-Node-Nonce"
	HeaderSig                    = "X-Maktaba-Node-Sig"
	HeaderBody                   = "X-Maktaba-Node-Body" // "unsigned" when the body is not hashed
	HeaderDelegatedSession       = "X-Maktaba-Delegated-Session"
	HeaderOriginNode             = "X-Maktaba-Origin-Node"
	HeaderVia                    = "X-Maktaba-Via"
	ViaNode                      = "node"
	UnsignedPayload              = "UNSIGNED-PAYLOAD"
	KeySize                      = 32
	MaxSkew                      = 60 * time.Second
	maxSignedBody          int64 = 8 << 20
)

// testNonce lets the vector tests fix the nonce (never set outside tests).
var testNonce = false

// EmptyBodyHash is sha256("") in hex.
var EmptyBodyHash = hexSHA256(nil)

func hexSHA256(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// SessionHash is the delegated-session-hash of a session id ("" for none).
func SessionHash(session string) string {
	if session == "" {
		return ""
	}
	return hexSHA256([]byte(session))
}

// Canonical is the signed string.
func Canonical(method, pathQuery string, ts int64, nonce, bodyHash, sessionHash string) string {
	return strings.ToUpper(method) + "\n" + pathQuery + "\n" + strconv.FormatInt(ts, 10) + "\n" + nonce + "\n" +
		bodyHash + "\n" + sessionHash
}

// Sign is b64(HMAC-SHA256(key, Canonical(...))).
func Sign(key []byte, method, pathQuery string, ts int64, nonce, bodyHash, sessionHash string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(Canonical(method, pathQuery, ts, nonce, bodyHash, sessionHash)))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// Key is one delegation key.
type Key struct {
	ID       string    `json:"id"`
	Secret   []byte    `json:"key"`
	NotAfter time.Time `json:"not_after"`
}

// pathQuery is the request's path and query as signed.
func pathQuery(r *http.Request) string {
	pq := r.URL.EscapedPath()
	if pq == "" {
		pq = "/"
	}
	if r.URL.RawQuery != "" {
		pq += "?" + r.URL.RawQuery
	}
	return pq
}

// SignRequest adds the signature headers to r (a request about to be sent to the home).
// The body is read and put back when it is small and replayable; a streamed body
// (unknown length, an upload over 8 MiB, an Upgrade) signs UNSIGNED-PAYLOAD.
func SignRequest(r *http.Request, nodeID string, key Key, now time.Time) error {
	if nodeID == "" || key.ID == "" || len(key.Secret) == 0 {
		return errors.New("delegation: no key")
	}
	bodyHash := EmptyBodyHash
	switch {
	case r.Header.Get("Upgrade") != "":
		bodyHash = UnsignedPayload
	case r.Body != nil && r.Body != http.NoBody:
		if r.ContentLength < 0 || r.ContentLength > maxSignedBody {
			bodyHash = UnsignedPayload
		} else {
			b, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				return err
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
			r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
			r.ContentLength = int64(len(b))
			bodyHash = hexSHA256(b)
		}
	}
	ts := now.Unix()
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		return err
	}
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	if fixed := r.Header.Get(HeaderNonce); fixed != "" && testNonce {
		nonce = fixed
	}
	r.Header.Set(HeaderNonce, nonce)
	r.Header.Set(HeaderNode, nodeID)
	r.Header.Set(HeaderKey, key.ID)
	r.Header.Set(HeaderTs, strconv.FormatInt(ts, 10))
	if bodyHash == UnsignedPayload {
		r.Header.Set(HeaderBody, "unsigned")
	} else {
		r.Header.Del(HeaderBody)
	}
	r.Header.Set(HeaderSig, Sign(key.Secret, r.Method, pathQuery(r), ts, nonce, bodyHash,
		SessionHash(r.Header.Get(HeaderDelegatedSession))))
	return nil
}

// Signed reports whether a request carries any node signature header.
func Signed(r *http.Request) bool {
	return r.Header.Get(HeaderNode) != "" || r.Header.Get(HeaderSig) != "" ||
		strings.EqualFold(r.Header.Get(HeaderVia), ViaNode)
}
