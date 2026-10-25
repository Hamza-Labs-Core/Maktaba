// Package dltoken is the home's download token for a machine that has no delegation key
// yet: a worker-only machine converting to a full node on its update (Epic 35 story 35.8
// AC8). The home puts one in that machine's update.convert job; it lets the bearer fetch
// one release's files from the home's release mirror (GET
// /api/cluster/updates/artifacts/{version}/{name}), for one machine, for a few hours.
//
// A token is "mkt_dl_" + base64url(claims JSON) + "." + base64url(HMAC-SHA256(claims)),
// keyed by a secret only the home holds. It grants nothing else: no other route takes it,
// and the files it fetches are verified by the machine itself as every other source's.
package dltoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Prefix starts every token (the API's auth gate lets a request carrying one reach the
// mirror's artifact route, whose handler verifies it).
const Prefix = "mkt_dl_"

// Lifetime is how long a token lets its machine download: the conversion's timeout (two
// hours) and room for a slow link.
const Lifetime = 4 * time.Hour

// Claims are what a token says: the machine, the release, until when.
type Claims struct {
	Node    string `json:"n"`
	Version string `json:"v"`
	Expires int64  `json:"e"` // Unix seconds
}

// Errors of Verify.
var (
	ErrMalformed = errors.New("dltoken: malformed")
	ErrSignature = errors.New("dltoken: bad signature")
	ErrExpired   = errors.New("dltoken: expired")
)

var enc = base64.RawURLEncoding

// Mint is a token for node to download version's files until exp, signed with key.
func Mint(key []byte, node, version string, exp time.Time) string {
	body, _ := json.Marshal(Claims{Node: node, Version: version, Expires: exp.Unix()})
	return Prefix + enc.EncodeToString(body) + "." + enc.EncodeToString(sign(key, body))
}

// Verify checks a token's signature and expiry at now and returns what it says (the caller
// checks the machine and the release against the request).
func Verify(key []byte, tok string, now time.Time) (Claims, error) {
	rest, ok := strings.CutPrefix(tok, Prefix)
	if !ok || len(key) == 0 {
		return Claims{}, ErrMalformed
	}
	b64, mac64, ok := strings.Cut(rest, ".")
	if !ok {
		return Claims{}, ErrMalformed
	}
	body, err := enc.DecodeString(b64)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	mac, err := enc.DecodeString(mac64)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	if !hmac.Equal(mac, sign(key, body)) {
		return Claims{}, ErrSignature
	}
	var c Claims
	if json.Unmarshal(body, &c) != nil || c.Node == "" || c.Version == "" || c.Expires == 0 {
		return Claims{}, ErrMalformed
	}
	if !now.Before(time.Unix(c.Expires, 0)) {
		return Claims{}, ErrExpired
	}
	return c, nil
}

// FromRequest is the token a request carries (Authorization: Bearer mkt_dl_…), "" when
// none.
func FromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || !strings.HasPrefix(tok, Prefix) {
		return ""
	}
	return strings.TrimSpace(tok)
}

func sign(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("maktaba-dltoken-v1\n"))
	m.Write(body)
	return m.Sum(nil)
}
