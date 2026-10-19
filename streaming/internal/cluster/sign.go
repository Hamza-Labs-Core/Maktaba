// Package cluster is Maktaba TV across the cluster (Epic 32, story 32.1): who this
// streaming host is, its heartbeat (tv_host_beat), what it can do (encoders, pushes,
// live blocks), the front door's request signing with the cluster stream key, and the
// channel-art cache a node encodes with.
package cluster

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Signature headers and query parameter (design §4.2, §4.3).
const (
	// HeaderFront carries "<exp>.<hex hmac>" on a request the front door forwards.
	HeaderFront = "X-Maktaba-Front"
	// HeaderPlacement carries the placement generation the request is for.
	HeaderPlacement = "X-Maktaba-Placement"
	// QueryFront is the query form of the signature (redirected HLS segments only).
	QueryFront = "fx"
	// QueryPlacement is the query form of the placement generation.
	QueryPlacement = "fg"
)

// SignTTL is how long a signature is valid.
const SignTTL = 60 * time.Second

// previousKeyGrace: after a key rotation the previous key is still accepted this long
// (hosts pick the new key up within one beat).
const previousKeyGrace = 15 * time.Second

// ErrBadSignature is returned for a missing, expired or wrong signature.
var ErrBadSignature = errors.New("cluster: bad front-door signature")

// Keyring holds the cluster stream key (and, for a short while after a rotation, the
// previous one). The zero value has no key: nothing verifies.
type Keyring struct {
	mu        sync.RWMutex
	cur       []byte
	prev      []byte
	prevUntil time.Time
	now       func() time.Time
}

// NewKeyring builds a keyring holding key (may be nil: set later by the heartbeat).
func NewKeyring(key []byte) *Keyring {
	k := &Keyring{now: time.Now}
	if len(key) > 0 {
		k.cur = append([]byte(nil), key...)
	}
	return k
}

func (k *Keyring) clock() time.Time {
	if k.now == nil {
		return time.Now()
	}
	return k.now()
}

// Set installs the current key; a different key than before keeps the old one valid
// for previousKeyGrace.
func (k *Keyring) Set(key []byte) {
	if len(key) == 0 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if hmac.Equal(k.cur, key) {
		return
	}
	if len(k.cur) > 0 {
		k.prev, k.prevUntil = k.cur, k.clock().Add(previousKeyGrace)
	}
	k.cur = append([]byte(nil), key...)
}

// Ready reports whether a key is installed.
func (k *Keyring) Ready() bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.cur) > 0
}

// Sign returns "<exp>.<hex hmac>" for method and path (no placement
// generation), expiring ttl from now.
func (k *Keyring) Sign(method, path string, ttl time.Duration) string {
	return k.SignGen(method, path, 0, ttl)
}

// SignGen signs method, path and the placement generation gen (0: none): a
// request's generation cannot be changed without breaking its signature.
func (k *Keyring) SignGen(method, path string, gen int64, ttl time.Duration) string {
	k.mu.RLock()
	key := k.cur
	k.mu.RUnlock()
	exp := k.clock().Add(ttl).Unix()
	return strconv.FormatInt(exp, 10) + "." + mac(key, method, path, exp, gen)
}

// SignRequest adds the front-door signature and the placement generation to r.
func (k *Keyring) SignRequest(r *http.Request, gen int64) {
	r.Header.Set(HeaderFront, k.SignGen(r.Method, r.URL.Path, gen, SignTTL))
	if gen > 0 {
		r.Header.Set(HeaderPlacement, strconv.FormatInt(gen, 10))
	}
}

// SignedQuery is the query string of a redirected segment URL ("fx=…&fg=…").
func (k *Keyring) SignedQuery(path string, gen int64) string {
	q := QueryFront + "=" + k.SignGen(http.MethodGet, path, gen, SignTTL)
	if gen > 0 {
		q += "&" + QueryPlacement + "=" + strconv.FormatInt(gen, 10)
	}
	return q
}

// Check verifies a signature value for method and path (no placement generation).
func (k *Keyring) Check(sig, method, path string) error { return k.CheckGen(sig, method, path, 0) }

// CheckGen verifies a signature value for method, path and placement generation.
func (k *Keyring) CheckGen(sig, method, path string, gen int64) error {
	expS, sum, ok := strings.Cut(sig, ".")
	if !ok || sum == "" {
		return ErrBadSignature
	}
	exp, err := strconv.ParseInt(expS, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	now := k.clock()
	if now.Unix() > exp || exp > now.Add(2*SignTTL).Unix() {
		return ErrBadSignature
	}
	k.mu.RLock()
	keys := [][]byte{k.cur}
	if len(k.prev) > 0 && now.Before(k.prevUntil) {
		keys = append(keys, k.prev)
	}
	k.mu.RUnlock()
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		if hmac.Equal([]byte(mac(key, method, path, exp, gen)), []byte(sum)) {
			return nil
		}
	}
	return ErrBadSignature
}

// Verify checks r's signature: the header, or (allowQuery) the fx query parameter.
func (k *Keyring) Verify(r *http.Request, allowQuery bool) error {
	// the generation travels with the signature: the header's with the header,
	// the query's with the query (it is signed, so it cannot be swapped)
	sig, genS := r.Header.Get(HeaderFront), r.Header.Get(HeaderPlacement)
	if sig == "" && allowQuery {
		sig, genS = r.URL.Query().Get(QueryFront), r.URL.Query().Get(QueryPlacement)
	}
	if sig == "" {
		return ErrBadSignature
	}
	var gen int64
	if genS != "" {
		n, err := strconv.ParseInt(genS, 10, 64)
		if err != nil || n < 0 {
			return ErrBadSignature
		}
		gen = n
	}
	return k.CheckGen(sig, r.Method, r.URL.Path, gen)
}

// PlacementGen is the generation a verified request carries (header, else query), 0
// when none.
func PlacementGen(r *http.Request) int64 {
	v := r.Header.Get(HeaderPlacement)
	if r.Header.Get(HeaderFront) == "" { // the query form: its own (signed) generation
		v = r.URL.Query().Get(QueryPlacement)
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func mac(key []byte, method, path string, exp, gen int64) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(method + "\n" + path + "\n" + strconv.FormatInt(exp, 10) + "\n" + strconv.FormatInt(gen, 10)))
	return hex.EncodeToString(h.Sum(nil))
}

// segmentPath reports whether a path is an HLS segment (the only path that accepts the
// query form of the signature).
func segmentPath(p string) bool {
	i := strings.LastIndexByte(p, '/')
	name := p[i+1:]
	return strings.HasPrefix(name, "seg-") && strings.HasSuffix(name, ".ts")
}

// VerifyMiddleware rejects requests without a valid front-door signature with 403 and
// logs event=tv_front_reject. Segment paths also accept the query form.
func VerifyMiddleware(k *Keyring, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := k.Verify(r, segmentPath(r.URL.Path)); err != nil {
				logger.Warn("tv: unsigned or bad front-door request refused", "path", r.URL.Path,
					"remote", r.RemoteAddr, "event", "tv_front_reject")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PreviewTTL is how long a staging preview token is good for (story 33.6 AC1).
const PreviewTTL = time.Hour

// previewMAC is the preview token's HMAC: the cluster stream key over
// "preview", the change id and the expiry.
func previewMAC(key []byte, changeID string, exp int64) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte("preview\n" + changeID + "\n" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(h.Sum(nil))
}

// PreviewToken mints a staging preview token "<change>.<exp>.<hex>" (the API
// mints the same with the key from tv_cluster_secret).
func PreviewToken(key []byte, changeID string, exp time.Time) string {
	e := exp.Unix()
	return changeID + "." + strconv.FormatInt(e, 10) + "." + previewMAC(key, changeID, e)
}

// ErrPreviewToken is a missing, bad or expired preview token.
var ErrPreviewToken = errors.New("cluster: bad or expired preview token")

// CheckPreview verifies a preview token and returns its change id.
func (k *Keyring) CheckPreview(tok string) (string, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return "", ErrPreviewToken
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", ErrPreviewToken
	}
	now := k.clock()
	if now.Unix() > exp || exp > now.Add(PreviewTTL+time.Minute).Unix() {
		return "", ErrPreviewToken
	}
	k.mu.RLock()
	keys := [][]byte{k.cur}
	if len(k.prev) > 0 && now.Before(k.prevUntil) {
		keys = append(keys, k.prev)
	}
	k.mu.RUnlock()
	for _, key := range keys {
		if len(key) > 0 && hmac.Equal([]byte(previewMAC(key, parts[0], exp)), []byte(parts[2])) {
			return parts[0], nil
		}
	}
	return "", ErrPreviewToken
}
