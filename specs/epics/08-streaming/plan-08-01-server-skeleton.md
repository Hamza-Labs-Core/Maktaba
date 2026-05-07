# Implementation Plan — Story 8.1 Server Skeleton, Signed URL Middleware

> Companion to [story-08-01-server-skeleton.md](story-08-01-server-skeleton.md).
> The story states *what* and *why*; this plan states *how*.
> Anchored on [architecture.md §4](../../architecture.md#4-streaming-service)
> and the JWT model in [§9.4](../../architecture.md#94-streaming-service)
> / [§9.8](../../architecture.md#98-jwt-claims). Token issuance is owned
> by [Epic 10 Story 10.6](../10-auth-security/story-10-06-jwks.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Binary | `streaming/cmd/maktaba-streaming/main.go` — single Go binary, statically linked, FFmpeg invoked as a child process. |
| HTTP framework | `net/http` + `chi` v5 — small, well-known, middleware-first. No gin/echo because the surface is narrow and we want stdlib semantics for ranges. |
| JWT library | `github.com/lestrrat-go/jwx/v2` — best-in-class JWKS handling (auto-refresh, key rotation, RS256 verification). Avoid `golang-jwt` because its JWKS story requires extra glue. |
| Listen address | Configurable via `streaming.toml` `[server] addr = "0.0.0.0:8081"`; defaults to `:8081`. |
| Metrics | Prometheus on `:9091/metrics` (separate listener; never mix with the byte-pump port). |
| TLS | Off by default; the Streaming binary sits behind nginx/Caddy. A `[tls]` section enables direct TLS (cert/key paths) for single-host installs. |
| Config | `streaming/internal/config/config.go` — `[server]`, `[jwt]`, `[cache]`, `[ffmpeg]`, `[hwaccel]`, `[transcode]`, `[tls]` blocks parsed by `pelletier/go-toml/v2`. |
| Out of scope | The byte handlers themselves (8.3/8.4/8.5/8.11/8.13), session store (8.9), gRPC (8.8). This story stops at "every signed URL is validated, every error is problem+json, /metrics works." |

## 1. Architecture diagram

```
                ┌──────────────────────────────────────────────────┐
                │  Player (web / iOS / tvOS / etc.)                │
                │   GET /stream/{session_id}/manifest.m3u8?sig=…   │
                └────────────────────────┬─────────────────────────┘
                                         │ HTTP/1.1 or h2c
                                         ▼
        ┌───────────────────────────────────────────────────────────┐
        │  streaming/cmd/maktaba-streaming (chi.Router)              │
        │                                                            │
        │   ┌────────────────────────────────────────────────────┐   │
        │   │ middleware chain (in order):                       │   │
        │   │   1. requestID         (X-Request-Id)              │   │
        │   │   2. realIP            (parse XFF, RemoteAddr)     │   │
        │   │   3. structuredLogger  (zerolog → stdout JSON)     │   │
        │   │   4. recoverer         (panic → 500 problem+json)  │   │
        │   │   5. metrics           (Prom histograms)           │   │
        │   │   6. signedURL         ★ THIS STORY                │   │
        │   │   7. libraryGuard      (lib[] vs probe.library_id) │   │
        │   └────────────────────────────────────────────────────┘   │
        │                                                            │
        │   ┌────────────────────────────────────────────────────┐   │
        │   │ Routes (registered, but most return 501 here):     │   │
        │   │   GET  /healthz       (no auth)                    │   │
        │   │   GET  /readyz        (no auth, checks JWKS+probe) │   │
        │   │   GET  /metrics       (Prom; separate listener)    │   │
        │   │   GET  /stream/{sid}/manifest.{m3u8,mpd}    8.5/.6 │   │
        │   │   GET  /stream/{sid}/{rendition}/index.m3u8 8.5    │   │
        │   │   GET  /stream/{sid}/{rendition}/seg-{n}.ts 8.5    │   │
        │   │   GET  /stream/{sid}/subs/{lang}.vtt        8.11   │   │
        │   │   GET  /stream/{sid}/subs/{lang}.m3u8       8.11   │   │
        │   │   GET  /stream/{sid}/chapters.json          8.12   │   │
        │   │   GET  /stream/direct/{video_id}            8.3    │   │
        │   │   HEAD /stream/direct/{video_id}            8.3    │   │
        │   │   GET  /stream/posters/{video_id}.jpg       8.13   │   │
        │   │   GET  /stream/sprites/{video_id}.{webp,vtt}8.13   │   │
        │   │   GET  /stream/thumbs/{video_id}/...        8.13   │   │
        │   └────────────────────────────────────────────────────┘   │
        └───────────────────────────────────────────────────────────┘
                                         │
                                         ▼
        ┌───────────────────────────────────────────────────────────┐
        │  jwks.Cache (lestrrat jwk.AutoRefresh)                    │
        │   - Periodic refresh: 300 s default                       │
        │   - On-miss refresh:  triggered when kid not in cache     │
        │   - Holds N keys during rotation (Epic 10 §10.6)          │
        │   - Stale-on-error: keep last good keyset on fetch fail   │
        └────────────────────────────────────────────────────────────┘
```

The middleware is the **single trust boundary**. Once a request reaches a
handler, three context values are guaranteed present:

```go
ctx.Value(ctxKeyClaims) // *streamingClaims (typed)
ctx.Value(ctxKeySubject) // string — session_id, video_id, or artifact-hash
ctx.Value(ctxKeyLibIDs)  // []uuid.UUID — the lib[] claim, parsed
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/cmd/maktaba-streaming/main.go` | Entrypoint: parse flags/config, build router, start listeners, wire signal handlers. |
| `streaming/internal/config/config.go` | `Config` struct, TOML loading, validation, defaults. |
| `streaming/internal/config/config_test.go` | Defaults, missing-section errors, env override (`MAKTABA_STREAMING_*`). |
| `streaming/internal/server/server.go` | `New(cfg) *http.Server`, route registration, graceful shutdown helper. |
| `streaming/internal/server/router.go` | All route paths in one place; `RegisterRoutes(r chi.Router, h *Handlers)`. Stub handlers return `501` until later stories land. |
| `streaming/internal/server/health.go` | `/healthz`, `/readyz` handlers. Readiness gates on JWKS-loaded + probe-DB-reachable. |
| `streaming/internal/auth/claims.go` | `StreamingClaims` typed struct (`aud`, `sub`, `usr`, `lib[]`, `exp`, `iat`, `nbf`). |
| `streaming/internal/auth/jwks.go` | `jwks.Cache` wrapping `jwk.AutoRefresh`; tracks `jwks_refresh_failed_total`. |
| `streaming/internal/auth/middleware.go` | `SignedURL(audPolicy)` middleware + `LibraryGuard()` middleware. |
| `streaming/internal/auth/middleware_test.go` | Unit tests from §6. |
| `streaming/internal/auth/audpolicy.go` | `AudPolicy` enum: `streaming`, `streaming-direct`, `streaming-static`. The middleware is constructed per-route family. |
| `streaming/internal/httpx/problem.go` | RFC 7807 problem+json writer. `Write(w, status, problemType, title, detail string)`. |
| `streaming/internal/httpx/problem_test.go` | Round-trip + Content-Type check. |
| `streaming/internal/observability/log.go` | `zerolog`-backed structured logger; `FromContext(ctx)`. |
| `streaming/internal/observability/metrics.go` | Counters, histograms, registry. |
| `streaming/internal/observability/metrics_test.go` | Histogram bucket sanity. |
| `shared/proto/streaming.proto` | gRPC schema (referenced here so service-name constants can live in the generated pkg; full server lands in 8.8). |
| `streaming/internal/probe/probe.go` | Tiny stub interface `Lookup(videoID) (*ProbeRow, error)` — implementation in 8.15; we need it now for the library check. |
| `streaming/configs/streaming.toml.example` | Documented example config. |
| `streaming/Dockerfile` | Multi-stage: `golang:1.23-alpine` build → `alpine:3.20` runtime + ffmpeg from `linuxserver/ffmpeg` static. |
| `streaming/go.mod`, `streaming/go.sum` | Module init; pin chi, jwx, zerolog, prometheus, pgx, viper-equivalent. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `Makefile` (or top-level) | Add `streaming-build`, `streaming-run`, `streaming-test` targets. |
| `specs/epics/08-streaming/README.md` | Tick story 8.1 once landed. |

### 2.3 Type definitions

```go
// streaming/internal/auth/claims.go
package auth

import (
    "encoding/json"
    "fmt"
    "time"

    "github.com/google/uuid"
    "github.com/lestrrat-go/jwx/v2/jwt"
)

// AudPolicy is the per-route family that the middleware enforces. Each
// HTTP route registers with one policy; the middleware is constructed
// once per policy and reused.
type AudPolicy string

const (
    AudSession AudPolicy = "streaming"        // /stream/{sid}/...
    AudDirect  AudPolicy = "streaming-direct" // /stream/direct/{vid}
    AudStatic  AudPolicy = "streaming-static" // /stream/posters|sprites|thumbs|subs
)

// StreamingClaims is the canonical decoded shape after JWT validation.
// Wraps jwt.Token to keep the public claims explicit in code.
type StreamingClaims struct {
    Audience string
    Subject  string      // session_id, video_id, or artifact-hash (depends on aud)
    UserID   uuid.UUID   // 'usr' claim — used by metrics, logs, watch progress
    LibIDs   []uuid.UUID // 'lib[]' — must include the resource's library
    IssuedAt time.Time
    Expires  time.Time
    NotBefore time.Time
}

func parseStreamingClaims(t jwt.Token) (*StreamingClaims, error) {
    aud, ok := t.Get("aud")
    if !ok {
        return nil, errClaimMissing("aud")
    }
    audStr, ok := aud.(string)
    if !ok {
        // jwx returns []string for multi-aud tokens; we forbid them — Streaming JWTs
        // must have exactly one aud per Epic 10 §10.8.
        return nil, errClaimWrongType("aud")
    }

    subj, _ := t.Get("sub")
    subStr, _ := subj.(string)
    if subStr == "" {
        return nil, errClaimMissing("sub")
    }

    usrRaw, _ := t.Get("usr")
    usrStr, _ := usrRaw.(string)
    usr, err := uuid.Parse(usrStr)
    if err != nil {
        return nil, errClaimInvalid("usr")
    }

    libsRaw, _ := t.Get("lib")
    libIDs, err := parseLibClaim(libsRaw)
    if err != nil {
        return nil, err
    }

    return &StreamingClaims{
        Audience:  audStr,
        Subject:   subStr,
        UserID:    usr,
        LibIDs:    libIDs,
        IssuedAt:  t.IssuedAt(),
        Expires:   t.Expiration(),
        NotBefore: t.NotBefore(),
    }, nil
}

// parseLibClaim accepts either a JSON array of strings or a single string.
// The API mints arrays per Epic 10 §10.6, but we accept a string for the
// trivial single-library case used in unit tests.
func parseLibClaim(raw any) ([]uuid.UUID, error) {
    if raw == nil {
        return nil, errClaimMissing("lib")
    }
    var ids []string
    switch v := raw.(type) {
    case []any:
        for _, item := range v {
            s, ok := item.(string)
            if !ok {
                return nil, errClaimWrongType("lib[]")
            }
            ids = append(ids, s)
        }
    case []string:
        ids = v
    case string:
        ids = []string{v}
    default:
        return nil, errClaimWrongType("lib")
    }
    out := make([]uuid.UUID, 0, len(ids))
    for _, s := range ids {
        u, err := uuid.Parse(s)
        if err != nil {
            return nil, errClaimInvalid("lib[]")
        }
        out = append(out, u)
    }
    return out, nil
}
```

```go
// streaming/internal/auth/middleware.go
package auth

import (
    "context"
    "net/http"
    "strings"
    "time"

    "github.com/lestrrat-go/jwx/v2/jwa"
    "github.com/lestrrat-go/jwx/v2/jwt"

    "maktaba/streaming/internal/httpx"
)

type ctxKey int

const (
    ctxKeyClaims ctxKey = iota
    ctxKeySubject
    ctxKeyLibIDs
)

// SignedURL returns a chi-compatible middleware that enforces:
//   - aud == required (per AudPolicy)
//   - sub == the URL parameter named subParam (e.g. "session_id")
//   - exp > now() within clock_skew_leeway_sec
//   - signature verifies against a key from the JWKS cache
//   - lib[] is well-formed (list of UUIDs)
//
// Library/sub checks beyond presence run in LibraryGuard — that one needs
// to look up the resource (probe cache) and isn't a static comparison.
func SignedURL(j *Cache, policy AudPolicy, subParam string, leeway time.Duration) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            tok, problem := extractAndVerify(r, j, leeway)
            if problem != "" {
                httpx.WriteSignedURLError(w, problem)
                return
            }

            cl, err := parseStreamingClaims(tok)
            if err != nil {
                httpx.WriteSignedURLError(w, err.Error())
                return
            }

            if cl.Audience != string(policy) {
                httpx.WriteSignedURLError(w, "wrong-aud")
                return
            }

            // Sub must match the URL parameter (session_id, video_id, or
            // artifact hash for static).
            wanted := chi.URLParam(r, subParam)
            if wanted == "" || cl.Subject != wanted {
                httpx.WriteSignedURLError(w, "wrong-sub")
                return
            }

            ctx := context.WithValue(r.Context(), ctxKeyClaims, cl)
            ctx = context.WithValue(ctx, ctxKeySubject, cl.Subject)
            ctx = context.WithValue(ctx, ctxKeyLibIDs, cl.LibIDs)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// extractAndVerify reads the JWT from ?sig=… (preferred) or
// `Authorization: Bearer …` (fallback for native players that prefer
// headers, per AC-4).
func extractAndVerify(r *http.Request, j *Cache, leeway time.Duration) (jwt.Token, string) {
    raw := r.URL.Query().Get("sig")
    if raw == "" {
        if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
            raw = strings.TrimPrefix(h, "Bearer ")
        }
    }
    if raw == "" {
        return nil, "missing"
    }

    keyset, err := j.Get(r.Context())
    if err != nil {
        // We never reject because of a transient JWKS fetch failure — the
        // cache holds the previous key as long as it can.
        return nil, "bad-signature"
    }

    tok, err := jwt.Parse(
        []byte(raw),
        jwt.WithKeySet(keyset),
        jwt.WithValidate(true),
        jwt.WithAcceptableSkew(leeway),
        jwt.WithRequiredClaim("sub"),
        jwt.WithRequiredClaim("aud"),
        jwt.WithRequiredClaim("exp"),
    )
    if err != nil {
        switch {
        case strings.Contains(err.Error(), "exp"):
            return nil, "expired"
        case strings.Contains(err.Error(), "verify"):
            return nil, "bad-signature"
        default:
            return nil, "bad-signature"
        }
    }
    return tok, ""
}

// LibraryGuard runs after SignedURL and verifies the lib[] claim covers
// the resource being served. Resource → library lookup is done via the
// probe cache (8.15) for video-scoped routes; for session-scoped routes,
// the session store (8.9) supplies the video. The probe.Lookup interface
// keeps this story decoupled from those.
func LibraryGuard(p probe.Lookup) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            cl := r.Context().Value(ctxKeyClaims).(*StreamingClaims)
            target, err := resolveResourceLibrary(r, p)
            if err != nil {
                httpx.Write(w, http.StatusNotFound, "resource-not-found",
                    "resource not found", err.Error())
                return
            }
            if !containsUUID(cl.LibIDs, target) {
                httpx.WriteSignedURLError(w, "wrong-lib")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

```go
// streaming/internal/auth/jwks.go
package auth

import (
    "context"
    "errors"
    "sync/atomic"
    "time"

    "github.com/lestrrat-go/jwx/v2/jwk"
    "github.com/prometheus/client_golang/prometheus"
)

type Cache struct {
    ar          *jwk.AutoRefresh
    url         string
    lastSuccess atomic.Int64 // unix-nanos
    refreshFail prometheus.Counter
}

func NewCache(ctx context.Context, jwksURL string, refresh time.Duration, m prometheus.Registerer) (*Cache, error) {
    ar := jwk.NewAutoRefresh(ctx)
    ar.Configure(jwksURL,
        jwk.WithMinRefreshInterval(refresh),
        jwk.WithFetchBackoff(jwk.NewBackoff(15*time.Second, 5*time.Minute)),
    )
    // Block at boot for one fetch; the readiness probe gates on this.
    if _, err := ar.Refresh(ctx, jwksURL); err != nil {
        return nil, err
    }

    refreshFail := prometheus.NewCounter(prometheus.CounterOpts{
        Name: "maktaba_streaming_jwks_refresh_failed_total",
        Help: "JWKS refresh attempts that returned an error.",
    })
    m.MustRegister(refreshFail)

    c := &Cache{ar: ar, url: jwksURL, refreshFail: refreshFail}
    c.lastSuccess.Store(time.Now().UnixNano())
    return c, nil
}

func (c *Cache) Get(ctx context.Context) (jwk.Set, error) {
    set, err := c.ar.Fetch(ctx, c.url)
    if err != nil {
        c.refreshFail.Inc()
        // Stale-on-error: AutoRefresh keeps the last good set in memory and
        // returns it from Fetch on cache hit even if the next refresh failed,
        // so this branch only triggers when we've never had a good set OR
        // the in-memory copy was evicted; either way, callers get an error
        // and the request fails closed.
        return nil, err
    }
    c.lastSuccess.Store(time.Now().UnixNano())
    return set, nil
}

func (c *Cache) LastSuccess() time.Time {
    return time.Unix(0, c.lastSuccess.Load())
}
```

### 2.4 Function signatures

```go
// streaming/internal/server/server.go
package server

func New(cfg config.Config, deps Dependencies) (*Server, error)

type Server struct {
    HTTP    *http.Server
    Metrics *http.Server
    closer  func(context.Context) error
}

func (s *Server) Start() error
func (s *Server) Shutdown(ctx context.Context) error
```

```go
// streaming/internal/httpx/problem.go
package httpx

func Write(w http.ResponseWriter, status int, problemType, title, detail string)
func WriteSignedURLError(w http.ResponseWriter, subType string) // 401 + type=signed-url-{subType}
```

## 3. Error envelope — RFC 7807 problem+json

The single helper makes every error consistent:

```go
// streaming/internal/httpx/problem.go
package httpx

import (
    "encoding/json"
    "net/http"
)

type Problem struct {
    Type   string `json:"type"`
    Title  string `json:"title"`
    Status int    `json:"status"`
    Detail string `json:"detail,omitempty"`
}

func Write(w http.ResponseWriter, status int, problemType, title, detail string) {
    w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
    w.Header().Set("X-Content-Type-Options", "nosniff")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(Problem{
        Type:   "https://maktaba.invalid/problems/" + problemType,
        Title:  title,
        Status: status,
        Detail: detail,
    })
}

// WriteSignedURLError centralizes the AC-1 sub-types so tests and handlers
// agree on the exact strings.
func WriteSignedURLError(w http.ResponseWriter, subType string) {
    title := "Signed URL invalid"
    detail := signedURLDetail(subType)
    Write(w, http.StatusUnauthorized, "signed-url-"+subType, title, detail)
}

func signedURLDetail(s string) string {
    switch s {
    case "missing":      return "the request did not carry a JWT (no ?sig= and no Authorization header)"
    case "expired":      return "the JWT is past its exp claim (with leeway applied)"
    case "wrong-aud":    return "the JWT's aud does not match this endpoint's required audience"
    case "wrong-sub":    return "the JWT's sub does not match the URL's subject (session, video, or artifact)"
    case "wrong-lib":    return "the JWT's lib[] claim does not include the resource's library"
    case "bad-signature":return "the JWT signature did not verify against any key in the JWKS"
    default:             return "the JWT is not acceptable for this request"
    }
}
```

## 4. Configuration shape

`streaming/configs/streaming.toml.example`:

```toml
[server]
addr            = "0.0.0.0:8081"
metrics_addr    = "0.0.0.0:9091"
read_header_ms  = 5000
write_ms        = 0           # 0 = unlimited (range-served large files)
shutdown_ms     = 30000

[jwt]
jwks_url               = "http://api.maktaba.local/.well-known/jwks.json"
jwks_refresh_sec       = 300
clock_skew_leeway_sec  = 60

[cache]
root            = "/var/maktaba/cache/streaming"
max_gib         = 50

[ffmpeg]
binary          = "ffmpeg"
probe_binary    = "ffprobe"

[hwaccel]
prefer          = "auto"      # 'auto' | 'videotoolbox' | 'nvenc' | 'qsv' | 'software'

[transcode]
max_concurrent  = 0           # 0 = auto-derive from (num_cores / 4)

[tls]
enabled         = false
cert_file       = ""
key_file        = ""
```

## 5. Test plan

### 5.1 Middleware unit tests (`streaming/internal/auth/middleware_test.go`)

Test fixtures use a self-signed RSA keypair and a hand-rolled JWKS server
served by `httptest.Server`.

| Test | What it pins |
|---|---|
| `TestSignedURL_Missing` | No `?sig` and no `Authorization` → 401 with `type=signed-url-missing`, body is problem+json. |
| `TestSignedURL_BadSignature` | Token signed by a different key → 401 `type=bad-signature`. |
| `TestSignedURL_Expired` | `exp = now - 5m` → 401 `type=expired`. |
| `TestSignedURL_ExpiredWithinLeeway` | `exp = now - 30s`, leeway = 60s → 200 (proves AC-1 leeway). |
| `TestSignedURL_WrongAud` | Aud = `streaming-direct` on a session route → 401 `type=wrong-aud`. |
| `TestSignedURL_WrongSub` | Sub = a different session id → 401 `type=wrong-sub`. |
| `TestSignedURL_AcceptsBearerHeader` | No `?sig`, `Authorization: Bearer <jwt>` → passes (AC-4 path). |
| `TestSignedURL_QuerySigBeatsHeader` | Both present, `?sig` is invalid, header is valid → 401 (the query param is canonical, never silently ignored). |
| `TestSignedURL_LibClaimWellFormed` | Token without `lib[]` → 401 `type=wrong-lib`. |
| `TestLibraryGuard_RejectsForeignLib` | Token has `lib=[A]`, video belongs to library B → 401 `type=wrong-lib`. |
| `TestLibraryGuard_AcceptsMatchingLib` | Token has `lib=[A,B]`, video belongs to A → 200. |
| `TestSignedURL_KidNotInJWKS` | Token references an unknown `kid` → triggers on-miss refresh; if still unknown, 401 `type=bad-signature`. |
| `TestSignedURL_StaticPolicySubIsHash` | `aud=streaming-static`, sub = sha256("posters/<hash>.jpg") → passes; sub = video_id → 401 `type=wrong-sub`. |

### 5.2 JWKS cache tests (`streaming/internal/auth/jwks_test.go`)

| Test | What it pins |
|---|---|
| `TestJWKSCache_FetchesOnBoot` | `NewCache` blocks until JWKS is loaded; ready immediately after. |
| `TestJWKSCache_RefreshesPeriodically` | Bump JWKS server's key set, advance fake clock, observe new keys without restart. |
| `TestJWKSCache_StaleOnError` | Kill JWKS server after first success; `Get` continues returning the cached set; `jwks_refresh_failed_total` increments. |
| `TestJWKSCache_KeyRotationOverlap` | JWKS contains old + new keys (Epic 10 §10.6); tokens signed by either verify. |
| `TestJWKSCache_ConcurrentFetch` | 1000 parallel `Get` calls during a refresh trigger at most one HTTP fetch (jwx AutoRefresh property). |

### 5.3 Integration (`streaming/internal/server/server_test.go`)

| Test | What it pins |
|---|---|
| `TestEndToEnd_HealthzReadyz` | `/healthz` always 200; `/readyz` 503 before JWKS load, 200 after. |
| `TestEndToEnd_MetricsListenerSeparate` | Posting to `:8081/metrics` returns 404; `:9091/metrics` returns Prom text. |
| `TestEndToEnd_SegmentNotFound404Problem` | Authenticated request to a missing path → 404 problem+json (AC-3). The handler stub for now writes `Write(w, 404, "not-found", ...)`. |
| `TestEndToEnd_PanicRecovers500` | A handler that panics returns 500 with problem+json; subsequent requests succeed. |
| `TestEndToEnd_SubAsArtifactHash` | A request to `/stream/posters/{vid}.jpg` whose JWT carries `sub=sha256(path)` succeeds; mismatched hash → 401. |
| `TestEndToEnd_RevokedLibraryAfterMint` | Mint JWT with lib=[A]; then call simulating the API revoking lib A — the JWT keeps working until exp (per the AC-3 edge case in story 8.1). |
| `TestEndToEnd_ParallelSessionsOneJWKSFetch` | 1000 parallel signed requests during a JWKS refresh window → JWKS endpoint sees ≤ 1 request (single-flight). Backed by `httptest.Server` request counter. |

### 5.4 Performance baseline

`TestPerformance_MiddlewareOverhead` — measure end-to-end overhead of the
middleware chain under a no-op handler:

- 10 000 requests, single connection, RS256 verification.
- Target: p99 < 1 ms per request on local hardware.
- Failure mode: jwx allocs blow up the heap → fix is to switch to
  `jwt.ParseWithClaims` over a re-used `[]byte` buffer.

## 6. Test code scaffolding

```go
// streaming/internal/auth/middleware_test.go
package auth_test

import (
    "context"
    "crypto/rand"
    "crypto/rsa"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/lestrrat-go/jwx/v2/jwa"
    "github.com/lestrrat-go/jwx/v2/jwk"
    "github.com/lestrrat-go/jwx/v2/jwt"
    "github.com/stretchr/testify/require"
)

type fixture struct {
    priv     *rsa.PrivateKey
    keyID    string
    jwksSrv  *httptest.Server
    cache    *auth.Cache
    sessID   uuid.UUID
    libID    uuid.UUID
    userID   uuid.UUID
    probe    *fakeProbe
}

func newFixture(t *testing.T) *fixture {
    t.Helper()
    priv, err := rsa.GenerateKey(rand.Reader, 2048)
    require.NoError(t, err)

    pub := jwk.NewRSAPublicKey()
    require.NoError(t, pub.FromRaw(&priv.PublicKey))
    require.NoError(t, pub.Set(jwk.KeyIDKey, "test-1"))
    require.NoError(t, pub.Set(jwk.AlgorithmKey, jwa.RS256))

    set := jwk.NewSet()
    set.Add(pub)

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        require.NoError(t, json.NewEncoder(w).Encode(set))
    }))

    cache, err := auth.NewCache(context.Background(), srv.URL, 5*time.Minute, prometheus.NewRegistry())
    require.NoError(t, err)

    libID := uuid.Must(uuid.NewV7())
    return &fixture{
        priv:    priv,
        keyID:   "test-1",
        jwksSrv: srv,
        cache:   cache,
        sessID:  uuid.Must(uuid.NewV7()),
        libID:   libID,
        userID:  uuid.Must(uuid.NewV7()),
        probe:   &fakeProbe{libByVideo: map[uuid.UUID]uuid.UUID{}, libBySession: map[uuid.UUID]uuid.UUID{}},
    }
}

func (f *fixture) mint(t *testing.T, claims map[string]any) string {
    t.Helper()
    tok := jwt.New()
    for k, v := range claims {
        require.NoError(t, tok.Set(k, v))
    }
    if _, ok := claims[jwt.ExpirationKey]; !ok {
        require.NoError(t, tok.Set(jwt.ExpirationKey, time.Now().Add(15*time.Minute)))
    }
    if _, ok := claims[jwt.IssuedAtKey]; !ok {
        require.NoError(t, tok.Set(jwt.IssuedAtKey, time.Now()))
    }

    headers := jws.NewHeaders()
    require.NoError(t, headers.Set(jws.KeyIDKey, f.keyID))

    raw, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, f.priv, jws.WithProtectedHeaders(headers)))
    require.NoError(t, err)
    return string(raw)
}

func TestSignedURL_Missing(t *testing.T) {
    f := newFixture(t)
    h := auth.SignedURL(f.cache, auth.AudSession, "session_id", time.Minute)(
        http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
    )

    r := chi.NewRouter()
    r.With(h).Get("/stream/{session_id}/foo", nil)

    req := httptest.NewRequest("GET", "/stream/"+f.sessID.String()+"/foo", nil)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    require.Equal(t, 401, rec.Code)
    require.Contains(t, rec.Body.String(), `"type":"https://maktaba.invalid/problems/signed-url-missing"`)
    require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))
}

func TestSignedURL_AcceptsBearerHeader(t *testing.T) {
    f := newFixture(t)
    sig := f.mint(t, map[string]any{
        "aud": "streaming-direct",
        "sub": f.sessID.String(),  // here the path uses video_id, see below
        "usr": f.userID.String(),
        "lib": []string{f.libID.String()},
    })
    f.probe.libByVideo[f.sessID] = f.libID

    h := auth.SignedURL(f.cache, auth.AudDirect, "video_id", time.Minute)(
        auth.LibraryGuard(f.probe)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.WriteHeader(200)
        })),
    )
    r := chi.NewRouter()
    r.With(h).Get("/stream/direct/{video_id}", nil)

    req := httptest.NewRequest("GET", "/stream/direct/"+f.sessID.String(), nil)
    req.Header.Set("Authorization", "Bearer "+sig)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    require.Equal(t, 200, rec.Code)
}
```

## 7. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Clock skew between API and Streaming up to ±60 s | `jwt.WithAcceptableSkew(leeway)` accepts `exp - leeway < now`. Tested with `exp = now-30s` and `leeway = 60s`. | `TestSignedURL_ExpiredWithinLeeway` |
| API down so JWKS fails to refresh | `jwk.AutoRefresh` keeps the cached key; `jwks_refresh_failed_total` increments; existing sessions keep working. | `TestJWKSCache_StaleOnError` |
| Key rotation — old + new keys overlap in JWKS | jwx tries each candidate key; both old and new tokens verify until the old is dropped. | `TestJWKSCache_KeyRotationOverlap` |
| Player retries with expired URL | First request 401 `type=expired`; player must call API to mint a fresh URL. Streaming never extends. | `TestSignedURL_Expired` |
| Library access revoked mid-session | JWT still valid until exp (≤ 30 min revocation lag in §10.5). The API can call `streaming.CloseSession` for immediate cutoff (Story 8.8). | `TestEndToEnd_RevokedLibraryAfterMint` |
| Multi-aud token | Rejected — `parseStreamingClaims` requires `aud` to be a single string. | Unit test on `parseStreamingClaims`. |
| `lib` claim missing | 401 `type=wrong-lib` (we treat absence as failed scope check, not as "no claim required"). | `TestSignedURL_LibClaimWellFormed` |
| Signed URL leak — attacker has a stolen manifest URL | Can only access that session's segments (`sub` pins the session) for the remaining TTL, only for libraries on the original mint. The static-asset variant pins to one artifact via `sub=hash(path)`. | `TestSignedURL_StaticPolicySubIsHash` |
| 4xx response shape | All 4xx/5xx use `httpx.Write` → `application/problem+json`. The helper sets `X-Content-Type-Options: nosniff` to keep browsers from sniffing. | `TestEndToEnd_SegmentNotFound404Problem` |
| Empty 200 body confused for "stream ended" | Forbidden — handlers either return bytes (with correct Content-Length) or a problem+json. The middleware never produces an empty body on its error paths. | AC-3 + middleware tests |
| Panic in a handler | `chi/middleware.Recoverer` wraps and writes a problem+json 500; the request id is logged. | `TestEndToEnd_PanicRecovers500` |
| Health check before JWKS loaded | `/readyz` returns 503 with `last_jwks_load_at` so the LB excludes the box. | `TestEndToEnd_HealthzReadyz` |
| Reserved chars in `?sig=` (e.g., URL-encoded `+`) | Tokens are base64url; `+` is not in the alphabet. We `query.Get` (which already URL-decodes) and pass straight to jwx. Test ensures `%2B` round-trips. | `TestSignedURL_URLEncodedSig` |

## 8. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `github.com/go-chi/chi/v5` | ^5.0 | Lightweight router with composable middleware; matches Go std `net/http` semantics needed for ranged GETs (story 8.3). |
| `github.com/lestrrat-go/jwx/v2` | ^2.0 | First-class JWKS auto-refresh with on-miss refresh and key rotation overlap. |
| `github.com/rs/zerolog` | ^1.32 | Zero-alloc structured logger; matches the JSON shape expected by the API. |
| `github.com/prometheus/client_golang` | ^1.19 | Standard for Go services. |
| `github.com/google/uuid` | ^1.6 | UUID v7 generation matches the rest of the stack. |
| `github.com/pelletier/go-toml/v2` | ^2.2 | TOML parser; matches how the rest of the project loads config (no viper drift). |
| `github.com/jackc/pgx/v5` | ^5.5 | Postgres driver (used here for the `streaming_sessions` and `media_info` reads in 8.9/8.15; declared now to keep go.mod stable). |

No FFmpeg in this story (8.1 is HTTP-only).

## 9. Acceptance checklist

**Server skeleton**
- [ ] `streaming/cmd/maktaba-streaming` builds with `go build ./...`.
- [ ] `/healthz` always returns 200.
- [ ] `/readyz` returns 503 until JWKS is loaded and probe DB pings, 200 after.
- [ ] `/metrics` is on a separate listener (port 9091 default).
- [ ] Graceful shutdown: SIGTERM closes accept(), drains in-flight requests for `shutdown_ms`, then closes idle connections.

**Signed URL middleware**
- [ ] AC-1: Wrong aud / sub / lib / signature each yields 401 with the correct `type=signed-url-…` sub-type.
- [ ] AC-2: JWKS refreshes asynchronously every `jwks_refresh_sec`; failures don't invalidate the cached key.
- [ ] AC-3: 404 on missing segment is `application/problem+json` (not empty 200).
- [ ] AC-4: `?sig=` AND `Authorization: Bearer …` both accepted on direct routes.
- [ ] AC-5: `aud=streaming-static` requires `sub` == sha256(artifact path); rejected if mismatched.

**Observability**
- [ ] Counter `maktaba_streaming_jwks_refresh_failed_total` exported.
- [ ] Histogram `maktaba_streaming_request_duration_seconds{route, method, status_class}` exported.
- [ ] Per-request structured log: `request_id`, `method`, `path`, `status`, `duration_ms`, `aud`, `sub`, `usr`, `lib_count`.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` is checked in and matches the config struct.
- [ ] `specs/epics/08-streaming/README.md` ticks story 8.1.
