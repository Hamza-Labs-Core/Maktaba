package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// jwksDoc is the subset of an RFC 7517 JWKS we consume: RSA signing
// keys with `kid`, `n`, `e`. Other key types are ignored at parse time.
type jwksDoc struct {
	Keys []jwksKey `json:"keys"`
}

type jwksKey struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKSCache holds the most recent keyset fetched from the API's
// well-known endpoint. The first fetch blocks (used by readiness);
// subsequent refreshes happen on a timer and on-miss when a token
// references an unknown kid.
//
// The cache is "stale on error" — a transient HTTP failure preserves
// the previous keyset so an in-flight watch session keeps working
// when the API is bouncing (Story 8.1 AC-2).
type JWKSCache struct {
	url      string
	client   *http.Client
	refresh  time.Duration
	keysMu   sync.RWMutex
	keys     map[string]*rsa.PublicKey
	lastOK   atomic.Int64 // unix nanos
	failures atomic.Uint64

	// inflight serializes refresh attempts so 1000 concurrent
	// requests during a refresh window only fire one HTTP fetch
	// (Story 8.1 AC-2 / test 5.2).
	inflight sync.Mutex
}

// NewJWKSCache builds a cache and immediately performs one fetch.
// Returns an error if the first fetch fails — callers gate readiness
// on this so the LB excludes the box until the JWKS is loaded.
func NewJWKSCache(ctx context.Context, jwksURL string, refresh time.Duration) (*JWKSCache, error) {
	if refresh <= 0 {
		refresh = 5 * time.Minute
	}
	c := &JWKSCache{
		url:     jwksURL,
		client:  &http.Client{Timeout: 5 * time.Second},
		refresh: refresh,
		keys:    map[string]*rsa.PublicKey{},
	}
	if jwksURL == "" {
		return c, nil
	}
	if err := c.fetchOnce(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Lookup returns the public key for a given kid, or nil if absent.
// Triggers an asynchronous refresh on miss so a fresh key id
// rotated in by the API resolves on the next request without restart.
func (c *JWKSCache) Lookup(kid string) *rsa.PublicKey {
	c.keysMu.RLock()
	k := c.keys[kid]
	c.keysMu.RUnlock()
	if k == nil {
		go c.tryRefresh(context.Background())
	}
	return k
}

// AnyKey is used in tests where the kid header is omitted.
func (c *JWKSCache) AnyKey() *rsa.PublicKey {
	c.keysMu.RLock()
	defer c.keysMu.RUnlock()
	for _, k := range c.keys {
		return k
	}
	return nil
}

// Set replaces the in-memory keyset. Used by tests; production reads
// from fetchOnce.
func (c *JWKSCache) Set(keys map[string]*rsa.PublicKey) {
	c.keysMu.Lock()
	c.keys = keys
	c.keysMu.Unlock()
	c.lastOK.Store(time.Now().UnixNano())
}

// LastSuccess returns the wall time of the most recent successful
// refresh — used by readiness probes (Story 8.1 AC-2).
func (c *JWKSCache) LastSuccess() time.Time {
	v := c.lastOK.Load()
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}

// FailureCount is exposed for the maktaba_streaming_jwks_refresh_failed_total
// metric (Story 8.1 §9 observability).
func (c *JWKSCache) FailureCount() uint64 { return c.failures.Load() }

// StartRefreshLoop runs the periodic refresh ticker. Cancel ctx to stop.
func (c *JWKSCache) StartRefreshLoop(ctx context.Context) {
	if c.url == "" {
		return
	}
	go func() {
		t := time.NewTicker(c.refresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = c.tryRefresh(ctx)
			}
		}
	}()
}

func (c *JWKSCache) tryRefresh(ctx context.Context) error {
	// single-flight: only one refresh in flight at a time
	if !c.inflight.TryLock() {
		return nil
	}
	defer c.inflight.Unlock()
	return c.fetchOnce(ctx)
}

func (c *JWKSCache) fetchOnce(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		c.failures.Add(1)
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		c.failures.Add(1)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.failures.Add(1)
		return fmt.Errorf("jwks: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		c.failures.Add(1)
		return err
	}
	var doc jwksDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		c.failures.Add(1)
		return err
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pk, err := decodeRSAJWK(k)
		if err != nil {
			continue
		}
		keys[k.Kid] = pk
	}
	if len(keys) == 0 {
		c.failures.Add(1)
		return errors.New("jwks: no usable keys")
	}
	c.keysMu.Lock()
	c.keys = keys
	c.keysMu.Unlock()
	c.lastOK.Store(time.Now().UnixNano())
	return nil
}

// decodeRSAJWK reconstructs an *rsa.PublicKey from its JWK form. Tries
// PEM first (some test fixtures stash a PEM in `n`) then base64url-int
// decoding for the standard JWK shape.
func decodeRSAJWK(k jwksKey) (*rsa.PublicKey, error) {
	// Try PEM-in-n path used by tests.
	if pem, err := base64.RawURLEncoding.DecodeString(k.N); err == nil {
		if pk, perr := x509.ParsePKIXPublicKey(pem); perr == nil {
			if rsaPub, ok := pk.(*rsa.PublicKey); ok {
				return rsaPub, nil
			}
		}
	}
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nb)
	e := 0
	for _, b := range eb {
		e = (e << 8) | int(b)
	}
	if e == 0 {
		return nil, errors.New("jwks: zero exponent")
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
