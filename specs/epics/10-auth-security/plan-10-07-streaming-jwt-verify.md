# Implementation Plan — Story 10.7 Streaming-side offline JWT verification

> Companion to [story-10-07-streaming-jwt-verify.md](story-10-07-streaming-jwt-verify.md).
> The wire format is owned by Epic 8 Story 8.1; this story owns
> *Streaming's behavior of trust* — JWKS bootstrap, refresh, rotation,
> and the `lib[]` enforcement check. Keys are minted by
> [Story 10.6](plan-10-06-rs256-keys-jwks.md). Probe cache shape comes
> from Epic 8 Story 8.15.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/jwks/` — `Cache` struct + JWKSPoller. |
| Verify middleware | `streaming/internal/http/middleware/jwt.go` — generic JWT verifier; per-route `aud` policy. |
| LISTEN integration | `streaming/internal/jwks/listener.go` — Postgres LISTEN `jwks_changed` to refresh sooner than poll cadence. |
| Probe-cache lookup | `streaming/internal/sessions/lib_lookup.go` — given a video_id (or session_id), return the resource's `library_id` from the in-memory probe cache. |
| Out of scope | The `Streaming JWT shape` (Epic 8 Story 8.1). Signed-URL minting (Story 10.8). DB fallback when probe cache is cold (Epic 8 Story 8.15 owns the implementation; this story consumes it). |

## 1. Architecture diagram

```
                     ┌──────────────────────────────┐
                     │ JWKSPoller (every 5 min)     │
                     │   GET /api/.well-known/jwks  │
                     │   if 200: cache.Replace(j)   │
                     │   if err: keep last-good     │
                     └─────────┬────────────────────┘
                               │ refresh
                               ▼
              ┌────────────────────────────────────────┐
              │ jwks.Cache                             │
              │   - keys: map[kid]*rsa.PublicKey       │
              │   - lastGood: time.Time                │
              │   - source: "fetched" | "stale" | "miss"│
              │   LookupRSA(kid) (*rsa.PublicKey, ok)  │
              └─────────┬──────────────────────────────┘
                        ▲                          ▲
                        │ Reload immediately       │ verify
                        │ on jwks_changed          │
                        │                          │
              ┌─────────┴─────────┐    ┌───────────┴────────────────┐
              │ LISTEN jwks_changed│   │ JWTMiddleware              │
              │ (Postgres path)    │   │   - parse token            │
              │  fall back to poll │   │   - kid → cache.LookupRSA  │
              │  if listener gone  │   │   - VerifyAccess(opts)     │
              └────────────────────┘   │   - resource lib check     │
                                       └────────┬───────────────────┘
                                                │ on success
                                                ▼
                                       ┌──────────────────┐
                                       │ next handler     │
                                       └──────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/jwks/cache.go` | `Cache` with `Replace`, `LookupRSA`, `Source`, `LastGood`. |
| `streaming/internal/jwks/poller.go` | 5-min poll loop; bootstrap; back-off. |
| `streaming/internal/jwks/listener.go` | LISTEN jwks_changed; SQLite uses the in-process bus. |
| `streaming/internal/http/middleware/jwt.go` | Verify middleware with audience policy + lib check. |
| `streaming/internal/sessions/lib_lookup.go` | `LibraryForResource(ctx, kind, id) (uuid.UUID, error)`. |
| `streaming/internal/jwks/cache_test.go` | Cache unit tests. |
| `streaming/internal/jwks/poller_test.go` | Poller tests with httptest. |
| `streaming/internal/http/middleware/jwt_test.go` | End-to-end verify tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/config/config.go` | Add `JWKS.URL` (e.g., `https://api.maktaba.local/api/.well-known/jwks.json`), `JWKS.PollInterval` (5m), `JWKS.InitialTimeout` (10s), `JWKS.LeewaySec` (60). |
| `streaming/cmd/streaming/main.go` | Boot `Cache` + `Poller` before mounting routes. |
| `streaming/internal/http/router.go` | Mount JWT middleware on `/stream/*`, `/stream/direct/*`, `/stream/static/*` with the audience policy table from §4. |

### 2.3 Type definitions

```go
// streaming/internal/jwks/cache.go
package jwks

import (
    "crypto/rsa"
    "sync"
    "time"
)

type Source string
const (
    SourceMiss     Source = "miss"        // never had a successful fetch
    SourceFetched  Source = "fetched"     // last call succeeded
    SourceStale    Source = "stale"       // serving last-good after a fetch failure
)

type Cache struct {
    mu       sync.RWMutex
    keys     map[string]*rsa.PublicKey
    lastGood time.Time
    source   Source
}

func (c *Cache) Replace(jwks JWKS, now time.Time)
func (c *Cache) LookupRSA(kid string) (*rsa.PublicKey, bool)
func (c *Cache) Status() (Source, time.Time, int)   // src, lastGood, key count
```

```go
// streaming/internal/jwks/poller.go
type Poller struct {
    cache         *Cache
    url           string
    interval      time.Duration
    httpClient    *http.Client
}

func (p *Poller) Bootstrap(ctx context.Context, timeout time.Duration) error
func (p *Poller) Start(ctx context.Context)
```

```go
// streaming/internal/http/middleware/jwt.go
type AudPolicy struct {
    Allowed     []string                                      // {"streaming"}, etc.
    SubKind     SubKind                                        // session | video | artifact-hash
    ResourceID  func(r *http.Request) (uuid.UUID, error)       // extract from URL
}

type SubKind int
const (
    SubSession SubKind = iota
    SubVideo
    SubArtifactHash
)
```

## 3. JWKS Cache

```go
// streaming/internal/jwks/cache.go
func NewCache() *Cache {
    return &Cache{keys: map[string]*rsa.PublicKey{}, source: SourceMiss}
}

func (c *Cache) Replace(jwks JWKS, now time.Time) {
    keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
    for _, k := range jwks.Keys {
        if k.Kty != "RSA" || k.Alg != "RS256" || k.Use != "sig" { continue }
        n, err1 := base64.RawURLEncoding.DecodeString(k.N)
        e, err2 := base64.RawURLEncoding.DecodeString(k.E)
        if err1 != nil || err2 != nil { continue }
        pk := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
        keys[k.KID] = pk
    }
    c.mu.Lock(); defer c.mu.Unlock()
    c.keys = keys
    c.lastGood = now
    c.source = SourceFetched
}

func (c *Cache) LookupRSA(kid string) (*rsa.PublicKey, bool) {
    c.mu.RLock(); defer c.mu.RUnlock()
    pk, ok := c.keys[kid]
    return pk, ok
}

func (c *Cache) MarkStale() {
    c.mu.Lock(); defer c.mu.Unlock()
    if c.source == SourceFetched { c.source = SourceStale }
}
```

The cache's source field is exported via `/healthz` (Story 10.16
audit + Epic 22 Story 22.4 owns the health endpoint shape) so an
operator can tell whether Streaming is on a stale JWKS.

## 4. Poller + bootstrap

```go
// streaming/internal/jwks/poller.go
func (p *Poller) Bootstrap(ctx context.Context, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    backoff := 100 * time.Millisecond
    for {
        if err := p.fetchOnce(ctx); err == nil {
            return nil
        }
        if time.Now().After(deadline) {
            return fmt.Errorf("jwks bootstrap: deadline %v exceeded", timeout)
        }
        select {
        case <-time.After(backoff):
        case <-ctx.Done():
            return ctx.Err()
        }
        backoff *= 2
        if backoff > time.Second { backoff = time.Second }
    }
}

func (p *Poller) fetchOnce(ctx context.Context) error {
    req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
    resp, err := p.httpClient.Do(req)
    if err != nil {
        p.cache.MarkStale()
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        p.cache.MarkStale()
        return fmt.Errorf("jwks: status %d", resp.StatusCode)
    }
    var j JWKS
    if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
        p.cache.MarkStale()
        return err
    }
    p.cache.Replace(j, time.Now())
    return nil
}

func (p *Poller) Start(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(p.interval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done(): return
            case <-ticker.C:
                _ = p.fetchOnce(ctx)
            }
        }
    }()
}
```

`main.go`:

```go
cache := jwks.NewCache()
poller := jwks.NewPoller(cache, cfg.JWKS.URL, cfg.JWKS.PollInterval)
if err := poller.Bootstrap(ctx, cfg.JWKS.InitialTimeout); err != nil {
    slog.Warn("jwks bootstrap failed; running in 503 mode until first poll succeeds", "err", err)
    // Do NOT exit — story AC-1 says "the binary still starts but rejects ...".
}
poller.Start(ctx)
listener := jwks.NewListener(ctx, dbpool, "jwks_changed", func() {
    _ = poller.fetchOnce(ctx)
})
```

## 5. JWT verify middleware

```go
// streaming/internal/http/middleware/jwt.go
package middleware

func JWT(cache *jwks.Cache, libLookup sessions.LibLookup, policy AudPolicy, leeway time.Duration) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Token can come from "?sig=…" (signed URL) OR Authorization header.
            tok := r.URL.Query().Get("sig")
            if tok == "" {
                if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
                    tok = strings.TrimPrefix(h, "Bearer ")
                }
            }
            if tok == "" {
                problemUnauthorized(w, "missing"); return
            }

            // Story AC-1 fallback: if cache has never had a successful fetch,
            // return 503 instead of 401.
            src, _, n := cache.Status()
            if src == jwks.SourceMiss || n == 0 {
                w.Header().Set("Retry-After", "5")
                problem(w, http.StatusServiceUnavailable, "jwks-unavailable", "")
                return
            }

            claims, err := jwtlib.VerifyClaims(tok, jwtlib.VerifyOpts{
                JWKS:        cache,
                AllowedAuds: policy.Allowed,
                LeewaySec:   int64(leeway / time.Second),
                Issuer:      "maktaba",
            })
            if err != nil {
                problemUnauthorized(w, classify(err)); return
            }

            // Resource binding: sub must match the resource id.
            wantSub, err := policy.ResourceID(r)
            if err != nil { problemUnauthorized(w, "wrong-sub"); return }
            if claims.Sub != wantSub.String() {
                problemUnauthorized(w, "wrong-sub"); return
            }

            // Library check: lib[] must contain the resource's library.
            resLib, err := libLookup.LibraryForResource(r.Context(), policy.SubKind, wantSub)
            if err != nil {
                problemUnauthorized(w, "wrong-lib"); return
            }
            if !contains(claims.Lib, resLib.String()) {
                problemUnauthorized(w, "wrong-lib"); return
            }

            ctx := jwtlib.WithClaims(r.Context(), claims)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

func classify(err error) string {
    switch {
    case errors.Is(err, jwt.ErrTokenExpired):     return "expired"
    case errors.Is(err, jwt.ErrSignatureInvalid): return "bad-signature"
    case errors.Is(err, jwt.ErrTokenNotValidYet): return "not-yet-valid"
    case strings.Contains(err.Error(), "kid"):    return "unknown-kid"
    case strings.Contains(err.Error(), "audience"): return "wrong-aud"
    default: return "invalid"
    }
}
```

`jwtlib.VerifyClaims` is the shared verify helper that lives in
`shared/jwtgo` (also used by API's bearer middleware in Story 10.3).
The verify path enforces `WithValidMethods([]string{"RS256"})` —
critical guard against alg-confusion attacks. RFC 7517's JWKS holds
public keys only, but a confused verifier could be tricked into
treating the modulus bytes as an HMAC secret; pinning RS256 closes it.

## 6. Library lookup against probe cache

```go
// streaming/internal/sessions/lib_lookup.go
type LibLookup interface {
    LibraryForResource(ctx context.Context, kind middleware.SubKind, id uuid.UUID) (uuid.UUID, error)
}

type cacheLookup struct {
    probe ProbeCache   // Epic 8 Story 8.15
    db    *db.Queries  // single fallback row read
}

func (l *cacheLookup) LibraryForResource(ctx context.Context, kind middleware.SubKind, id uuid.UUID) (uuid.UUID, error) {
    switch kind {
    case middleware.SubVideo:
        if v, ok := l.probe.GetVideo(id); ok { return v.LibraryID, nil }
        // Cold: single DB fallback (story AC EC: "freshly-probed video isn't immediately rejected").
        v, err := l.db.GetVideoByID(ctx, id)
        if err != nil { return uuid.Nil, ErrUnknownVideo }
        l.probe.PutVideo(v)   // warm the cache
        return v.LibraryID, nil

    case middleware.SubSession:
        if s, ok := l.probe.GetSession(id); ok { return s.LibraryID, nil }
        s, err := l.db.GetStreamingSession(ctx, id)
        if err != nil { return uuid.Nil, ErrUnknownSession }
        return s.LibraryID, nil

    case middleware.SubArtifactHash:
        // Static-asset case (Story 10.8 AC-3): the artifact-hash → library
        // mapping is held in the probe cache as well; on miss, derive from
        // the artifact path's video_id segment.
        v, ok := l.probe.GetVideoForArtifact(id)
        if !ok { return uuid.Nil, ErrUnknownArtifact }
        return v.LibraryID, nil
    }
    return uuid.Nil, ErrUnknownResource
}
```

The ProbeCache is the in-memory video-metadata cache populated by
Streaming's session opener; we reuse it without changes.

## 7. Audience policy table

| Route prefix | Allowed `aud` | `SubKind` | ResourceID extractor |
|---|---|---|---|
| `/stream/{session_id}/manifest.m3u8` | `"streaming"` | `SubSession` | parse `session_id` from path |
| `/stream/{session_id}/seg-*.ts` | `"streaming"` | `SubSession` | parse `session_id` from path |
| `/stream/direct/{video_id}` | `"streaming-direct"` | `SubVideo` | parse `video_id` from path |
| `/stream/static/{artifact}` | `"streaming-static"` | `SubArtifactHash` | sha256 of the artifact path resolved from URL |

Each route in `streaming/internal/http/router.go` wraps with
`middleware.JWT(cache, lookup, policy, leeway)`.

## 8. LISTEN integration

```go
// streaming/internal/jwks/listener.go
func NewListener(ctx context.Context, db *pgxpool.Pool, channel string, onSignal func()) *Listener {
    l := &Listener{}
    if db == nil {
        // SQLite branch: subscribe to the in-process bus.
        ch, _ := sharedbus.Subscribe(ctx, "jwks_changed")
        go func() {
            for range ch { onSignal() }
        }()
        return l
    }
    go func() {
        for ctx.Err() == nil {
            err := pgnotify.Listen(ctx, db, channel, func(_ string) { onSignal() })
            if errors.Is(err, context.Canceled) { return }
            slog.Warn("listener exit; reconnect in 5s", "err", err)
            time.Sleep(5 * time.Second)
        }
    }()
    return l
}
```

The listener is a *fast path*; the 5-min poll is the always-on
guarantee. If the listener is broken, rotation propagates via poll.

## 9. Test plan

### 9.1 Cache (`cache_test.go`)

| Test | What it pins |
|---|---|
| `TestReplaceParsesRSA` | A JWKS with one RS256 entry → cache has 1 key whose `N`/`E` decode correctly. |
| `TestReplaceSkipsNonRSA` | A JWKS with one EC entry and one RSA entry → cache holds only the RSA key. |
| `TestMarkStaleAfterFetched` | After a successful Replace, Status returns `SourceFetched`; after MarkStale, returns `SourceStale`. |
| `TestSourceMissOnEmptyCache` | Fresh cache → Status `SourceMiss`. |
| `TestLookupRSAUnknownKID` | Lookup with random kid → `ok=false`. |

### 9.2 Poller (`poller_test.go`)

| Test | What it pins |
|---|---|
| `TestBootstrapSucceedsOnFirstFetch` | httptest server returns 200 → Bootstrap returns nil; cache `SourceFetched`. |
| `TestBootstrapBacksOffOnFailure` | Server returns 500 twice then 200 → Bootstrap returns nil; total elapsed < timeout. |
| `TestBootstrapDeadlineExceeded` | Server always 500; timeout 1s → Bootstrap returns deadline error after ~1s. |
| `TestStartFetchesEveryInterval` | Start with 100ms interval; 5 ticks → 5 fetches recorded by httptest. |
| `TestFetchFailureMarksStale` | Server starts 200, then 500; cache transitions Fetched → Stale; LookupRSA still works against last-good keys. |
| `TestRotationPickedUpVia LISTEN` | Insert a new DB key (triggering pg_notify) → listener fires onSignal → next request verifies the new kid within 1s without waiting for the 5-min poll. |

### 9.3 JWT middleware (`jwt_test.go`)

| Test | What it pins |
|---|---|
| `TestVerifyAllowsValidStreamingToken` | Mint via API (Story 10.6 signer) with `aud=streaming, sub=session_id, lib=[L1]`; configure session/library bindings; GET manifest → 200. |
| `TestVerifyRejectsBadSignature` | Flip a byte → 401 `bad-signature`. |
| `TestVerifyRejectsExpired` | Force `exp=now-1s`; with leeway 0 → 401 `expired`. |
| `TestVerifyRejectsWrongAudOnDirectRoute` | Token `aud=streaming` against `/stream/direct/{vid}` (which expects `streaming-direct`) → 401 `wrong-aud`. |
| `TestVerifyRejectsWrongSub` | Token `sub != session_id` → 401 `wrong-sub`. |
| `TestVerifyRejectsWrongLib` | Token `lib=[Lother]` and resource is in L1 → 401 `wrong-lib`. |
| `TestVerifyAcceptsLibAfterColdProbeFallback` | Probe cache empty for video V; DB has V→L1 mapping; token `lib=[L1]` → 200; ProbeCache now warm. |
| `TestVerifyMissingTokenReturnsMissing` | No `?sig` and no Authorization → 401 `missing`. |
| `TestVerifyJWKSUnavailableReturns503` | Cache is `SourceMiss`; request → 503 `jwks-unavailable` with `Retry-After`. |
| `TestVerifyAcceptsTokensSignedWithOldKidDuringOverlap` | Two keys in JWKS; old-kid token verifies. |
| `TestVerifyRejectsTokensWithUnknownKid` | Token's kid not in JWKS → 401 `unknown-kid`. |
| `TestVerifyClockSkewLeewayHonored` | Token `exp = now-30s` + leeway 60s → 200. |
| `TestVerifyRejectsHS256` | An HS256-signed token whose payload claims `kid` of an RSA key in the JWKS → rejected by `WithValidMethods`. |

### 9.4 Cross-dialect

`jwks_dialect_test.go` runs the LISTEN flow against PG; the SQLite
path uses the in-process bus and is covered by a separate sibling
test.

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| First boot before API is up | Bootstrap retries with back-off until `JWKS.InitialTimeout` (10s); on failure, the binary keeps running and serves 503 `jwks-unavailable`. | `TestBootstrapDeadlineExceeded` (control) |
| API down for hours | Cache stays at last-good (`SourceStale`); requests continue to verify against the cached keys. New rotations don't propagate until API returns. Documented in operations. | `TestFetchFailureMarksStale` |
| Two API replicas with different active keys mid-rotation | Both keys are in the union JWKS; both verify. The 5-min poll smooths this; LISTEN narrows the window further. | Story 10.6 plan EC |
| Streaming clock 30s behind API | Leeway absorbs (default 60s). Beyond → 401 `not-yet-valid`. | `TestVerifyClockSkewLeewayHonored` |
| Probe cache cold for a freshly-probed video | One DB read fallback warms the cache; the same JWT then verifies on the next call without the fallback. | `TestVerifyAcceptsLibAfterColdProbeFallback` |
| `lib[]` contains a UUID that isn't a real library | Doesn't matter — only the resource's library needs to be in `lib[]`. The minter (Story 10.8) is the source of truth for what's in `lib[]`. | n/a |
| A token where `lib` is `null` instead of `[]` | The verifier's `Lib` defaults to `[]string{}` (the Claims struct uses `Lib []string`); a `null` JSON decodes to nil, and `contains(nil, ...)` is `false` → 401 `wrong-lib`. | `TestVerifyRejectsNullLib` |
| Listener reconnect storm | 5s back-off prevents tight loops; the poller continues at its interval regardless. | `TestListenerReconnects` |
| JWKS endpoint returns 304 Not Modified | Not implemented in v1; we always re-decode. The 5-min cadence makes the bandwidth cost negligible. | n/a |
| Token in a query parameter logged accidentally | Story 10.14 plan covers logger redaction of `?sig=…`. | Story 10.14 plan |

## 11. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | v5.x | Verify. |
| `github.com/jackc/pgx/v5` | already | LISTEN. |
| `github.com/google/uuid` | already | UUID parsing for path params. |

No new heavy deps.

## 12. Acceptance checklist

**Bootstrap**
- [ ] AC-1: first JWKS fetch succeeds within `JWKS.InitialTimeout` (default 10s); on failure, the binary stays up and serves 503 `jwks-unavailable`.

**Refresh**
- [ ] 5-min poll re-fetches; `Cache-Control` cooperation is best-effort.
- [ ] LISTEN `jwks_changed` triggers an immediate refetch (within ~1s).

**Verify**
- [ ] AC-3: enforces `iss="maktaba"`, `aud ∈ {streaming, streaming-direct, streaming-static}` per route, `exp/nbf` with leeway, `kid` in JWKS, `lib[]` contains the resource library.
- [ ] HS256/`none` rejected (alg-confusion guard).
- [ ] Resource-id sub-binding enforced (token `sub` must equal the URL resource id).

**Edge cases**
- [ ] Cold probe cache → one DB fallback before 401.
- [ ] Stale source label exposed via /healthz.

**Tests**
- [ ] All §9 tests pass.

**Docs**
- [ ] README.md ticks story 10.7.
