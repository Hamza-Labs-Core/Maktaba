# Plan 10.7 — Streaming-side offline JWT verification — implementation

> Implementation plan for [story-10-07-streaming-jwt-verify.md](story-10-07-streaming-jwt-verify.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: consumes the JWKS endpoint published by
> [Plan 10.6](plan-10-06-rs256-keys-jwks.md); the wire-format error
> envelope (`type: wrong-aud` etc.) is owned by
> [Epic 8 Plan 8.1](../08-streaming/plan-08-01-signed-url-verify.md);
> the in-memory probe cache and DB fallback used to resolve a resource's
> `library_id` is owned by
> [Epic 8 Plan 8.15](../08-streaming/plan-08-15-resource-library-resolution.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Two refresh paths: 5-min poll AND `LISTEN jwks_changed`.** Both run; whichever fires first triggers reload. | Story AC-2: "next 5 min poll, or sooner via `LISTEN jwks_changed`". | Belt-and-braces: NOTIFY misses (connection drop, replica behind a NAT) are caught by the poll; the poll's 5-min worst-case is shrunk to ~ms in the happy path. |
| D2 | **JWKS bootstrap timeout is hard at 10 s, but the cached JWKS is sticky forever.** On bootstrap failure the cache stays empty and signed-URL handlers return 503 `type: jwks-unavailable`. Once the first fetch succeeds, a *later* fetch failure does **not** clear the cache; the last-good JWKS keeps verifying tokens. | Story AC-1 + edge: "JWKS endpoint blocked by firewall — Streaming caches the last-seen JWKS indefinitely". | Bootstrap-fail-closed protects against a deploy that started before the API was healthy. Steady-state-fail-open protects in-progress playback against API outages — playback is the higher-priority property once JWKS is known. |
| D3 | **Verification middleware is generic over `aud`** (`streaming`, `streaming-direct`, `streaming-static`); the per-handler wrapper supplies the expected aud. | Story AC-3 enumerates three audiences. | One implementation, three call sites; matches the three handler classes (manifest/segment, direct, sidecar). |
| D4 | **`lib[]` check uses the in-memory probe cache** as the primary source of `resource → library_id`, with a single DB fallback per Plan 8.15. The fallback is rate-limited to one in-flight DB query per resource id (singleflight) so a cold-start thundering-herd doesn't stampede Postgres. | Story AC-3 + edge "probe cache cold". | Hot path is offline, cold path is bounded — exactly the offline trade-off §9.8 calls out. Singleflight is the standard Go idiom for this. |
| D5 | **Clock-skew leeway is 60 s on `exp` and `nbf`**, configurable via `auth.jwks_clock_skew_sec`. | Story edge: "30 s clock-skew at the Streaming box absorbed". | One minute is enough for typical NTP drift between two Linux hosts; configurable for ops where drift is larger. The skew window is symmetric. |
| D6 | **JWKS cache is by-value `map[kid]*rsa.PublicKey` behind a `sync.RWMutex`**, not `atomic.Value`. Updates rebuild the map and swap. | Read-heavy, write-rare. | RWMutex on read is a single atomic load + decrement; cheap. atomic.Value would also work but adds copy-on-read overhead because the Go runtime requires the same concrete type. RWMutex is more readable. |
| D7 | **Verification errors map 1:1 to the Story 8.1 envelope.** `kid` not in JWKS → `bad-signature`; `aud` mismatch → `wrong-aud`; `lib` mismatch → `wrong-lib`; `exp` past → `expired`; `nbf` future → `expired` (we don't add a separate type — same UX); missing or malformed token → `missing`. | Story AC-3 explicit list. | Aligning Streaming's verify path with the wire format keeps client error handling uniform and makes integration tests trivial. |

---

## 1. Architecture diagram — JWKS lifecycle on Streaming

```
   API host                                      Streaming host
   ─────────                                     ──────────────
                                                 ┌────────────────────────────────┐
   GET /api/.well-known/jwks.json  ◄─── poll ───┤  internal/auth/jwks            │
                                                 │    Bootstrap (10s budget)      │
                                                 │    Poller (5 min ticker)       │
                                                 │    Listener (LISTEN NOTIFY)    │
                                                 └─────────────┬──────────────────┘
   NOTIFY jwks_changed ─────────────────────────────────────────►
   (Postgres pub/sub)                                          │
                                                               ▼
                                                 ┌────────────────────────────────┐
                                                 │  Cache (RWMutex, by kid)       │
                                                 │   {kid → *rsa.PublicKey}       │
                                                 │   bootstrappedAt time          │
                                                 └─────────────┬──────────────────┘
                                                               │
                            HTTP request to Streaming          │
                            /stream/{sid}/manifest.m3u8?sig=…  │
                            /stream/direct/{vid}?sig=…         │
                            /stream/static/{path}?sig=…        │
                                                               ▼
                                                 ┌────────────────────────────────┐
                                                 │  jwt.Verify middleware (D3)    │
                                                 │   1. parse token (errs missing)│
                                                 │   2. lookup kid (D6 RWMutex)   │
                                                 │   3. RS256 verify              │
                                                 │   4. check iss=maktaba         │
                                                 │   5. check aud == handler aud  │
                                                 │   6. check exp/nbf ± leeway    │
                                                 │   7. resolve resource→lib (D4) │
                                                 │   8. assert lib in claims.lib  │
                                                 └─────────────┬──────────────────┘
                                                               │
                                          OK → handler         │  err → 401/403/503
                                                               │   per Story 8.1
                                                               ▼
```

---

## 2. Detailed implementation

### 2.1 Package layout — Go Streaming

```
streaming/
└── internal/
    └── auth/
        ├── jwks/
        │   ├── client.go        # HTTP fetch + parse
        │   ├── cache.go         # in-memory store
        │   ├── refresher.go     # poller + listener
        │   └── jwks_test.go
        ├── verify/
        │   ├── middleware.go    # net/http middleware factory
        │   ├── claims.go        # claim struct + checks
        │   ├── lib_resolver.go  # probe-cache + singleflight DB fallback
        │   └── verify_test.go
        └── errs/
            └── errs.go          # types: missing, expired, wrong-aud, wrong-sub, wrong-lib,
                                 # bad-signature, jwks-unavailable
```

### 2.2 `client.go` — HTTP JWKS fetch

```go
// streaming/internal/auth/jwks/client.go
package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

type rawJWK struct {
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type rawJWKS struct {
	Keys []rawJWK `json:"keys"`
}

type Client struct {
	hc  *http.Client
	url string
}

func NewClient(url string, timeout time.Duration) *Client {
	return &Client{
		hc:  &http.Client{Timeout: timeout},
		url: url,
	}
}

func (c *Client) Fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: status %d", resp.StatusCode)
	}
	var raw rawJWKS
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string]*rsa.PublicKey, len(raw.Keys))
	for _, k := range raw.Keys {
		if k.Kty != "RSA" || k.Alg != "RS256" || k.Kid == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}
	}
	if len(out) == 0 {
		return nil, errors.New("jwks: empty key set")
	}
	return out, nil
}
```

### 2.3 `cache.go` — in-memory store (D2, D6)

```go
// streaming/internal/auth/jwks/cache.go
package jwks

import (
	"crypto/rsa"
	"sync"
	"time"
)

type Cache struct {
	mu             sync.RWMutex
	keys           map[string]*rsa.PublicKey
	bootstrappedAt time.Time
}

func NewCache() *Cache { return &Cache{keys: map[string]*rsa.PublicKey{}} }

func (c *Cache) Get(kid string) (*rsa.PublicKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k, ok := c.keys[kid]
	return k, ok
}

func (c *Cache) Bootstrapped() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.bootstrappedAt.IsZero()
}

// Replace atomically swaps the cache.  Empty incoming maps are silently
// ignored so a transient empty JWKS does not orphan the verifier (D2).
func (c *Cache) Replace(next map[string]*rsa.PublicKey) {
	if len(next) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = next
	if c.bootstrappedAt.IsZero() {
		c.bootstrappedAt = time.Now()
	}
}
```

### 2.4 `refresher.go` — bootstrap, poll, LISTEN (D1)

```go
// streaming/internal/auth/jwks/refresher.go
package jwks

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Refresher struct {
	client       *Client
	cache        *Cache
	pool         *pgxpool.Pool
	pollEvery    time.Duration
	bootstrapTO  time.Duration
	log          *slog.Logger
}

func NewRefresher(c *Client, cache *Cache, pool *pgxpool.Pool, log *slog.Logger) *Refresher {
	return &Refresher{
		client: c, cache: cache, pool: pool,
		pollEvery: 5 * time.Minute, bootstrapTO: 10 * time.Second, log: log,
	}
}

// Bootstrap blocks for up to `bootstrapTO` trying to populate the cache.
// Returns nil if the first fetch succeeded.
func (r *Refresher) Bootstrap(ctx context.Context) error {
	bctx, cancel := context.WithTimeout(ctx, r.bootstrapTO)
	defer cancel()
	keys, err := r.client.Fetch(bctx)
	if err != nil {
		r.log.Error("jwks bootstrap failed", "err", err)
		return err
	}
	r.cache.Replace(keys)
	return nil
}

// Run is non-blocking; spawns the poller and listener goroutines.
func (r *Refresher) Run(ctx context.Context) {
	go r.poll(ctx)
	go r.listen(ctx)
}

func (r *Refresher) poll(ctx context.Context) {
	t := time.NewTicker(r.pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.refreshOnce(ctx)
		}
	}
}

// listen runs forever, holding a single pgx connection that issues
// LISTEN jwks_changed.  On every notification we re-fetch the JWKS.
// On disconnect we back off and reconnect; the poller covers any
// notifications missed during the gap.
func (r *Refresher) listen(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.listenOnce(ctx); err != nil {
			r.log.Warn("jwks listen disconnected", "err", err)
			time.Sleep(2 * time.Second)
		}
	}
}

func (r *Refresher) listenOnce(ctx context.Context) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN jwks_changed"); err != nil {
		return err
	}
	for {
		_, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		r.refreshOnce(ctx)
	}
}

func (r *Refresher) refreshOnce(ctx context.Context) {
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	keys, err := r.client.Fetch(fctx)
	if err != nil {
		// Steady-state fail-open (D2): keep the last-known good cache.
		r.log.Warn("jwks refresh failed", "err", err)
		return
	}
	r.cache.Replace(keys)
}
```

### 2.5 `verify/claims.go` — claim struct

```go
// streaming/internal/auth/verify/claims.go
package verify

import (
	"github.com/golang-jwt/jwt/v5"
)

type StreamingClaims struct {
	Iss string   `json:"iss"`
	Aud string   `json:"aud"`
	Sub string   `json:"sub"`
	Usr string   `json:"usr"`
	Lib []string `json:"lib"`
	Jti string   `json:"jti"`
	Exp int64    `json:"exp"`
	Nbf int64    `json:"nbf,omitempty"`
	Iat int64    `json:"iat,omitempty"`
	jwt.RegisteredClaims
}
```

### 2.6 `verify/middleware.go` — generic verifier (D3, D5, D7)

```go
// streaming/internal/auth/verify/middleware.go
package verify

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"streaming/internal/auth/errs"
	"streaming/internal/auth/jwks"
)

const Issuer = "maktaba"

type ResourceLookup interface {
	// Resource returns the library_id of the addressed resource (session,
	// video, or static asset path). Implementation lives in lib_resolver.go.
	Resource(ctx context.Context, aud, sub string) (libraryID string, err error)
}

type Middleware struct {
	cache    *jwks.Cache
	resolver ResourceLookup
	leeway   time.Duration
}

func New(cache *jwks.Cache, resolver ResourceLookup, leeway time.Duration) *Middleware {
	return &Middleware{cache: cache, resolver: resolver, leeway: leeway}
}

// Wrap returns a net/http middleware that asserts the request carries a
// valid JWT for the given expected aud.
func (m *Middleware) Wrap(expectedAud string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.cache.Bootstrapped() {
			errs.Write(w, http.StatusServiceUnavailable, errs.JWKSUnavailable)
			return
		}
		token := tokenFromRequest(r)
		if token == "" {
			errs.Write(w, http.StatusUnauthorized, errs.Missing)
			return
		}
		claims := &StreamingClaims{}
		parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, errors.New("alg not RS256")
			}
			kidI, ok := t.Header["kid"]
			if !ok {
				return nil, errors.New("kid missing")
			}
			kid, _ := kidI.(string)
			pub, ok := m.cache.Get(kid)
			if !ok {
				return nil, errors.New("kid unknown")
			}
			return pub, nil
		}, jwt.WithLeeway(m.leeway))
		if err != nil {
			errs.Write(w, http.StatusUnauthorized, errs.MapJWTErr(err))
			return
		}
		if !parsed.Valid {
			errs.Write(w, http.StatusUnauthorized, errs.BadSignature)
			return
		}
		// iss check
		if claims.Iss != Issuer {
			errs.Write(w, http.StatusUnauthorized, errs.BadSignature)
			return
		}
		// aud check
		if claims.Aud != expectedAud {
			errs.Write(w, http.StatusUnauthorized, errs.WrongAud)
			return
		}
		// lib check (D4)
		libID, err := m.resolver.Resource(r.Context(), claims.Aud, claims.Sub)
		if err != nil {
			errs.Write(w, http.StatusUnauthorized, errs.WrongSub)
			return
		}
		if !contains(claims.Lib, libID) {
			errs.Write(w, http.StatusUnauthorized, errs.WrongLib)
			return
		}
		// Stash claims for downstream handlers.
		ctx := context.WithValue(r.Context(), ctxKeyClaims{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type ctxKeyClaims struct{}

func ClaimsFromContext(ctx context.Context) *StreamingClaims {
	v, _ := ctx.Value(ctxKeyClaims{}).(*StreamingClaims)
	return v
}

func tokenFromRequest(r *http.Request) string {
	if t := r.URL.Query().Get("sig"); t != "" {
		return t
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// Compile-time guard that the rsa import is used (parsing).
var _ = (*rsa.PublicKey)(nil)
```

### 2.7 `verify/lib_resolver.go` — probe cache + singleflight (D4)

```go
// streaming/internal/auth/verify/lib_resolver.go
package verify

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"streaming/internal/probe"
)

// LibraryResolver resolves the library_id for a resource (session,
// video, or static path) using the in-memory probe cache first and a
// single DB query as a fallback.
type LibraryResolver struct {
	pool  *pgxpool.Pool
	probe probe.Cache // populated by Plan 8.15
	sf    singleflight.Group
}

func NewLibraryResolver(pool *pgxpool.Pool, probe probe.Cache) *LibraryResolver {
	return &LibraryResolver{pool: pool, probe: probe}
}

func (r *LibraryResolver) Resource(ctx context.Context, aud, sub string) (string, error) {
	switch aud {
	case "streaming":
		return r.bySession(ctx, sub)
	case "streaming-direct":
		return r.byVideo(ctx, sub)
	case "streaming-static":
		return r.byArtifactHash(ctx, sub)
	default:
		return "", fmt.Errorf("unknown aud %q", aud)
	}
}

func (r *LibraryResolver) bySession(ctx context.Context, sid string) (string, error) {
	if v, ok := r.probe.SessionLibrary(sid); ok {
		return v, nil
	}
	v, err, _ := r.sf.Do("sess:"+sid, func() (interface{}, error) {
		var libID string
		err := r.pool.QueryRow(ctx,
			`SELECT library_id::text FROM streaming_sessions WHERE id=$1`, sid).Scan(&libID)
		return libID, err
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (r *LibraryResolver) byVideo(ctx context.Context, vid string) (string, error) {
	if v, ok := r.probe.VideoLibrary(vid); ok {
		return v, nil
	}
	v, err, _ := r.sf.Do("vid:"+vid, func() (interface{}, error) {
		var libID string
		err := r.pool.QueryRow(ctx,
			`SELECT library_id::text FROM videos WHERE id=$1`, vid).Scan(&libID)
		return libID, err
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (r *LibraryResolver) byArtifactHash(ctx context.Context, h string) (string, error) {
	if v, ok := r.probe.ArtifactLibrary(h); ok {
		return v, nil
	}
	v, err, _ := r.sf.Do("art:"+h, func() (interface{}, error) {
		var libID string
		err := r.pool.QueryRow(ctx,
			`SELECT library_id::text FROM video_artifacts WHERE sha256=$1`, h).Scan(&libID)
		return libID, err
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

var ErrUnknownResource = errors.New("resource not found in probe cache or DB")
```

### 2.8 `errs/errs.go` — wire envelope (Story 8.1 compliance, D7)

```go
// streaming/internal/auth/errs/errs.go
package errs

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
)

type Type string

const (
	Missing          Type = "missing"
	Expired          Type = "expired"
	WrongAud         Type = "wrong-aud"
	WrongSub         Type = "wrong-sub"
	WrongLib         Type = "wrong-lib"
	BadSignature     Type = "bad-signature"
	JWKSUnavailable  Type = "jwks-unavailable"
	SigningUnavailable Type = "signing-unavailable"
)

type Envelope struct {
	Type    Type   `json:"type"`
	Message string `json:"message,omitempty"`
}

func Write(w http.ResponseWriter, status int, t Type) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Type: t})
}

func MapJWTErr(err error) Type {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired), errors.Is(err, jwt.ErrTokenNotValidYet):
		return Expired
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return BadSignature
	case errors.Is(err, jwt.ErrTokenMalformed):
		return Missing
	default:
		return BadSignature
	}
}
```

### 2.9 Boot wiring

```go
// streaming/cmd/maktaba-streaming/main.go (excerpt)
client := jwks.NewClient(cfg.APIBaseURL+"/api/.well-known/jwks.json", 5*time.Second)
cache := jwks.NewCache()
ref := jwks.NewRefresher(client, cache, pool, log)
if err := ref.Bootstrap(ctx); err != nil {
	log.Warn("jwks bootstrap failed; serving 503 until first success")
}
ref.Run(ctx)

resolver := verify.NewLibraryResolver(pool, probe.Get())
mw := verify.New(cache, resolver, 60*time.Second)

r.With(mw.Wrap("streaming")).Get("/stream/{sid}/manifest.m3u8", manifestHandler)
r.With(mw.Wrap("streaming")).Get("/stream/{sid}/seg/{seg}.ts", segmentHandler)
r.With(mw.Wrap("streaming-direct")).Get("/stream/direct/{vid}", directHandler)
r.With(mw.Wrap("streaming-static")).Get("/stream/static/{path:.+}", staticHandler)
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols | Tests |
|-------|------|---------|-------|
| 1 | `streaming/internal/auth/errs/errs.go` | `Type` consts, `Envelope`, `Write`, `MapJWTErr` | `TestMapJWTErr` |
| 2 | `streaming/internal/auth/jwks/client.go` | `Client.Fetch` | `TestClientFetchParsesJWKS` |
| 3 | `streaming/internal/auth/jwks/cache.go` | `Cache.{Get,Bootstrapped,Replace}` | `TestCacheReplaceIgnoresEmpty` |
| 4 | `streaming/internal/auth/jwks/refresher.go` | `Refresher.{Bootstrap,Run,refreshOnce,listen}` | `TestBootstrapTimeout`, `TestListenTriggersRefresh`, `TestRefreshFailureKeepsLastGood` |
| 5 | `streaming/internal/auth/verify/claims.go` | `StreamingClaims` | (n/a) |
| 6 | `streaming/internal/auth/verify/lib_resolver.go` | `LibraryResolver` | `TestResolverProbeHit`, `TestResolverDBFallbackSingleflight` |
| 7 | `streaming/internal/auth/verify/middleware.go` | `Middleware.Wrap`, `ClaimsFromContext` | `TestVerifyHappyPath`, `TestVerifyEachErrorType`, `TestClockSkewAbsorbed` |
| 8 | `streaming/cmd/maktaba-streaming/main.go` (extend) | wiring | boot integration test |

---

## 4. Test cases keyed to ACs

### 4.1 `TestBootstrapTimeoutFailsClosed` (AC-1)

```go
func TestBootstrapTimeoutReturns503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Second)
	}))
	defer srv.Close()
	client := jwks.NewClient(srv.URL, 100*time.Millisecond)
	cache := jwks.NewCache()
	ref := jwks.NewRefresher(client, cache, nil, slog.Default())
	ref.bootstrapTO = 100 * time.Millisecond
	require.Error(t, ref.Bootstrap(context.Background()))
	mw := verify.New(cache, fakeResolver{}, time.Minute)
	rr := httptest.NewRecorder()
	mw.Wrap("streaming", okHandler).ServeHTTP(rr,
		httptest.NewRequest("GET", "/stream/x/manifest.m3u8?sig=foo", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Contains(t, rr.Body.String(), "jwks-unavailable")
}
```

### 4.2 `TestListenTriggersRefreshUnder5s` (AC-2)

```go
func TestListenJwksChangedTriggersRefresh(t *testing.T) {
	ctx, db := newTestDB(t)
	requestCount := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		fmt.Fprintln(w, sampleJWKS(t))
	}))
	defer srv.Close()
	client := jwks.NewClient(srv.URL, time.Second)
	cache := jwks.NewCache()
	ref := jwks.NewRefresher(client, cache, db, slog.Default())
	require.NoError(t, ref.Bootstrap(ctx))
	ref.Run(ctx)
	startCount := requestCount.Load()
	// Simulate API rotation by emitting NOTIFY ourselves.
	_, err := db.Exec(ctx, "NOTIFY jwks_changed, 'test'")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return requestCount.Load() > startCount
	}, 5*time.Second, 50*time.Millisecond)
}
```

### 4.3 `TestVerifyHappyPath` and `TestVerifyEachErrorType` (AC-3, D7)

```go
func TestVerifyHappyPathStashesClaims(t *testing.T) {
	cache, signer := freshCacheAndSigner(t)
	mw := verify.New(cache, fakeResolver{lib: "lib1"}, time.Minute)
	tok := signToken(t, signer, map[string]any{
		"iss": "maktaba", "aud": "streaming", "sub": "sess1",
		"usr": "u1", "lib": []string{"lib1"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	rr := httptest.NewRecorder()
	mw.Wrap("streaming", checkClaims("u1", "sess1")).
		ServeHTTP(rr, withSig("/stream/sess1/m.m3u8", tok))
	require.Equal(t, 200, rr.Code)
}

func TestVerifyWrongAud(t *testing.T) {
	cache, signer := freshCacheAndSigner(t)
	mw := verify.New(cache, fakeResolver{lib: "lib1"}, time.Minute)
	tok := signToken(t, signer, map[string]any{
		"iss": "maktaba", "aud": "streaming", "sub": "vid1",
		"usr": "u1", "lib": []string{"lib1"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	rr := httptest.NewRecorder()
	mw.Wrap("streaming-direct", okHandler).
		ServeHTTP(rr, withSig("/stream/direct/vid1", tok))
	require.Equal(t, 401, rr.Code)
	require.Contains(t, rr.Body.String(), "wrong-aud")
}

func TestVerifyWrongLib(t *testing.T) {
	cache, signer := freshCacheAndSigner(t)
	mw := verify.New(cache, fakeResolver{lib: "lib1"}, time.Minute)
	tok := signToken(t, signer, map[string]any{
		"iss": "maktaba", "aud": "streaming", "sub": "sess1",
		"usr": "u1", "lib": []string{"lib2"}, // mismatch
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	rr := httptest.NewRecorder()
	mw.Wrap("streaming", okHandler).
		ServeHTTP(rr, withSig("/stream/sess1/m.m3u8", tok))
	require.Equal(t, 401, rr.Code)
	require.Contains(t, rr.Body.String(), "wrong-lib")
}

func TestVerifyExpired(t *testing.T) {
	cache, signer := freshCacheAndSigner(t)
	mw := verify.New(cache, fakeResolver{lib: "lib1"}, time.Minute)
	tok := signToken(t, signer, map[string]any{
		"iss": "maktaba", "aud": "streaming", "sub": "sess1",
		"usr": "u1", "lib": []string{"lib1"},
		"exp": time.Now().Add(-2 * time.Hour).Unix(),
	})
	rr := httptest.NewRecorder()
	mw.Wrap("streaming", okHandler).
		ServeHTTP(rr, withSig("/stream/sess1/m.m3u8", tok))
	require.Equal(t, 401, rr.Code)
	require.Contains(t, rr.Body.String(), "expired")
}

func TestVerifyMissing(t *testing.T) {
	cache, _ := freshCacheAndSigner(t)
	mw := verify.New(cache, fakeResolver{lib: "lib1"}, time.Minute)
	rr := httptest.NewRecorder()
	mw.Wrap("streaming", okHandler).
		ServeHTTP(rr, httptest.NewRequest("GET", "/stream/sess1/m.m3u8", nil))
	require.Equal(t, 401, rr.Code)
	require.Contains(t, rr.Body.String(), "missing")
}

func TestVerifyBadSignature(t *testing.T) {
	cache, _ := freshCacheAndSigner(t)
	otherSigner := freshSigner(t) // attacker has their own key
	mw := verify.New(cache, fakeResolver{lib: "lib1"}, time.Minute)
	tok := signToken(t, otherSigner, map[string]any{
		"iss": "maktaba", "aud": "streaming", "sub": "sess1",
		"usr": "u1", "lib": []string{"lib1"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	rr := httptest.NewRecorder()
	mw.Wrap("streaming", okHandler).
		ServeHTTP(rr, withSig("/stream/sess1/m.m3u8", tok))
	require.Equal(t, 401, rr.Code)
	require.Contains(t, rr.Body.String(), "bad-signature")
}
```

### 4.4 `TestClockSkewAbsorbed` (D5, edge)

```go
func TestVerifyAcceptsExpWithin60sLeeway(t *testing.T) {
	cache, signer := freshCacheAndSigner(t)
	mw := verify.New(cache, fakeResolver{lib: "lib1"}, 60*time.Second)
	tok := signToken(t, signer, map[string]any{
		"iss": "maktaba", "aud": "streaming", "sub": "sess1",
		"usr": "u1", "lib": []string{"lib1"},
		"exp": time.Now().Add(-30 * time.Second).Unix(), // 30s past
	})
	rr := httptest.NewRecorder()
	mw.Wrap("streaming", okHandler).
		ServeHTTP(rr, withSig("/stream/sess1/m.m3u8", tok))
	require.Equal(t, 200, rr.Code)
}
```

### 4.5 `TestResolverProbeHitAvoidsDB` (D4)

```go
func TestResolverHitsProbeCacheNoDB(t *testing.T) {
	probe := fakeProbe{sessionLib: map[string]string{"sess1": "lib1"}}
	r := verify.NewLibraryResolver(/*pool*/ nil, probe) // nil pool — must not be used
	got, err := r.Resource(context.Background(), "streaming", "sess1")
	require.NoError(t, err)
	require.Equal(t, "lib1", got)
}

func TestResolverColdFallbackSingleflight(t *testing.T) {
	ctx, db := newTestDB(t)
	insertSession(t, db, "sess1", "lib1")
	probe := fakeProbe{} // empty
	r := verify.NewLibraryResolver(db, probe)
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			got, err := r.Resource(ctx, "streaming", "sess1")
			require.NoError(t, err)
			require.Equal(t, "lib1", got)
		}()
	}
	wg.Wait()
	// pgx_stat_user_tables: at most a handful of queries.
	require.LessOrEqual(t, queryCount(t, db, "streaming_sessions"), int64(2))
}
```

### 4.6 `TestRefreshFailureKeepsLastGood` (D2 edge)

```go
func TestRefreshFailureDoesNotEvictKeys(t *testing.T) {
	failing := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			http.Error(w, "down", 503); return
		}
		fmt.Fprintln(w, sampleJWKS(t))
	}))
	defer srv.Close()
	client := jwks.NewClient(srv.URL, time.Second)
	cache := jwks.NewCache()
	ref := jwks.NewRefresher(client, cache, nil, slog.Default())
	require.NoError(t, ref.Bootstrap(context.Background()))
	pubBefore, ok := cache.Get(testKid())
	require.True(t, ok)
	failing.Store(true)
	ref.refreshOnce(context.Background())
	pubAfter, ok := cache.Get(testKid())
	require.True(t, ok)
	require.Equal(t, pubBefore, pubAfter)
}
```

---

## 5. Edge cases

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **JWKS endpoint returns empty `{keys:[]}`.** Client returns "empty key set" error; cache is unchanged. | `Client.Fetch` empty guard + `Cache.Replace` nil/empty short-circuit (D2). |
| E2 | **Two API replicas momentarily have different active kids during rotation.** Both kids are in JWKS; both verify. | Plan 10.6 D3 + cache holds N>1 keys. |
| E3 | **Clock skew of 30 s between API and Streaming.** 60 s leeway absorbs. | D5. |
| E4 | **Probe cache cold for a freshly-probed video.** Resolver falls back to one DB query, cached by probe afterward. | `LibraryResolver.byVideo` + singleflight (D4). |
| E5 | **`kid` header missing on token.** `jwt.ParseWithClaims` keyfunc returns "kid missing"; mapped to `bad-signature`. | `MapJWTErr`. |
| E6 | **Token's `alg` is `none` or HS256.** Keyfunc rejects: `alg not RS256`. Mapped to `bad-signature`. | Keyfunc explicit assertion. |
| E7 | **`sig` query param AND `Authorization: Bearer` both present.** `sig` wins (HLS players prefer URLs). | `tokenFromRequest` order. |
| E8 | **Token with `lib=[]` empty.** `contains` returns false → `wrong-lib`. | Existing logic; no special path. |
| E9 | **Resource not in probe cache and not in DB** (deleted between mint and play). Resolver returns DB error (`pgx.ErrNoRows`); middleware maps to `wrong-sub`. | `LibraryResolver` error path → `errs.WrongSub`. |
| E10 | **NOTIFY connection drops mid-flight.** `listenOnce` returns; outer loop sleeps and reconnects. The 5-min poller refreshes in the meantime. | D1 belt-and-braces. |
| E11 | **Pgxpool exhaustion blocks `listen` Acquire.** The listener backs off; the poller still runs because it does its own short-lived connections via the Client. | `listen` retry; poller path is independent. |
| E12 | **Token signed by a kid that was retired.** Still in JWKS until overlap ends → verifies. After overlap removal → `bad-signature`. | Plan 10.6 sweeper + cache replacement. |

---

## 6. Acceptance checklist

- [ ] **A1** `Refresher.Bootstrap` succeeds within 10 s on a healthy install; on failure the cache stays empty and signed-URL handlers return 503 `type: jwks-unavailable`. (`TestBootstrapTimeoutReturns503`)
- [ ] **A2** `Refresher.Run` spawns both a 5-min poll and a `LISTEN jwks_changed` goroutine; a NOTIFY triggers refresh in <5 s. (`TestListenJwksChangedTriggersRefresh`)
- [ ] **A3** Verification middleware enforces `iss=maktaba`, `aud ∈ {streaming, streaming-direct, streaming-static}`, `exp/nbf` with 60 s leeway, `kid` in JWKS, and `lib[]` contains the resource's library. (`TestVerifyHappyPath`, `TestVerifyWrongAud`, `TestVerifyWrongLib`, `TestVerifyExpired`, `TestVerifyMissing`, `TestVerifyBadSignature`, `TestVerifyAcceptsExpWithin60sLeeway`)
- [ ] **A4** Library resolution uses the in-memory probe cache first; cold paths fall back to one DB query coalesced via singleflight. (`TestResolverHitsProbeCacheNoDB`, `TestResolverColdFallbackSingleflight`)
- [ ] **A5** Steady-state JWKS fetch failure does not evict cached keys; the last-good map keeps verifying tokens. (`TestRefreshFailureDoesNotEvictKeys`)
- [ ] **A6** Error envelopes match Plan 8.1: `missing`, `expired`, `wrong-aud`, `wrong-sub`, `wrong-lib`, `bad-signature`, `jwks-unavailable`. (Each verify test asserts the `type` body field.)
- [ ] **A7** End-to-end: a JWT minted by the API (Plan 10.8) verifies on Streaming until expiry; an attacker-signed token with an unknown kid fails as `bad-signature`. (`TestVerifyHappyPath` + `TestVerifyBadSignature`.)
