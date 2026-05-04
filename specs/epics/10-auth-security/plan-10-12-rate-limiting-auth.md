# Implementation Plan — Story 10.12 Rate limiting on auth endpoints

> Companion to [story-10-12-rate-limiting-auth.md](story-10-12-rate-limiting-auth.md).
> The general rate-limit middleware is owned by Epic 7 Story 7.19; this
> story adds the *narrow auth-specific caps* on top. Brute-force lockout
> (Story 10.11) is a different mechanism — it counts *failed* attempts;
> rate-limit counts *all* attempts.

## 0. Canonical numbers and limiter (cross-epic)

This plan defines the **canonical rate-limit numbers and limiter** for
auth endpoints:

- `POST /api/auth/login` → **10/min/IP**
- `POST /api/auth/refresh` → **6/min/family + 30/min/IP**
- other `/api/auth/*` → **30/min/IP**
- Limiter: **`golang.org/x/time/rate`** (per-key map with janitor sweep)
- Eviction: **janitor** (periodic sweep of stale entries), NOT
  LRU-on-overflow

[plan-23-06](../23-security/plan-23-06-rate-limiting.md) **references
rather than redefines** these. Auth-route classes exposed by this
package are imported into plan-23-06's registry directly.

Cross-link: [plan-23-06 — Rate Limiting (Epic 23)](../23-security/plan-23-06-rate-limiting.md).

## 0.1 Scope and placement

| Concern | Decision |
|---|---|
| Backend | In-memory token bucket (`golang.org/x/time/rate`) keyed by `(scope, key)`. The full architecture uses Postgres for cross-replica share-state; for v1 we run per-replica buckets and accept the 2× headroom on multi-replica deployments. |
| Three caps | (a) `/api/auth/*` excluding `/login` → 30/min/IP. (b) `/api/auth/login` → 10/min/IP. (c) `/api/auth/refresh` → 6/min per *refresh family* (Per-token-family). |
| Middleware | `api/internal/http/middleware/ratelimit_auth.go` — composed of a `Bucket` registry + path-based selector. |
| Coordination | Epic 7 Story 7.19's general middleware runs *before* this one; this one trips first if the auth-specific cap is tighter. |
| Out of scope | The general per-route rate-limit (7.19). The brute-force counters (10.11). |

## 1. Architecture diagram

```
incoming /api/auth/* request
   ▼
[ general rate-limit (Epic 7 Story 7.19) ]
   ▼
┌─────────────────────────────────────────────────────────────┐
│ middleware/ratelimit_auth.go                                  │
│   1. select rule by path:                                      │
│        /api/auth/login        → loginRule (10/min/IP)         │
│        /api/auth/refresh      → broaderRule(30/min/IP)         │
│                                  + familyRule(6/min/family)    │
│        /api/auth/*            → broaderRule(30/min/IP)         │
│   2. for each rule:                                            │
│        bucket := registry.Get(rule.scope, rule.keyOf(r))       │
│        if !bucket.Allow():                                     │
│            w.Header().Set("Retry-After", strconv.Itoa(...))    │
│            problem(429, rule.errType)                          │
│            return                                              │
│   3. next                                                      │
└─────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/http/middleware/ratelimit_auth.go` | Middleware, rules, registry. |
| `api/internal/http/middleware/ratelimit_auth_test.go` | Integration tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.RateLogin` (10), `Auth.RateBroad` (30), `Auth.RateRefreshFamily` (6), `Auth.RateBurst` (5). |
| `api/internal/http/router.go` | Mount this middleware on `/api/auth/*` immediately before the per-handler logic. |

### 2.3 Type definitions

```go
// api/internal/http/middleware/ratelimit_auth.go
package middleware

import (
    "net/http"
    "strings"
    "sync"
    "time"

    "golang.org/x/time/rate"
)

type rule struct {
    scope    string                 // identifies the bucket family ("login", "broad", "fam")
    perMin   int
    burst    int
    keyOf    func(*http.Request) string
    errType  string                 // "ip-throttled-login" etc.
    headerHint string               // "X-RateLimit-Scope"
}

type registry struct {
    mu      sync.Mutex
    buckets map[string]*rate.Limiter   // key: scope|key
}

func (r *registry) get(scope, key string, perMin, burst int) *rate.Limiter {
    k := scope + "|" + key
    r.mu.Lock(); defer r.mu.Unlock()
    if b, ok := r.buckets[k]; ok { return b }
    b := rate.NewLimiter(rate.Every(time.Minute / time.Duration(perMin)), burst)
    r.buckets[k] = b
    return b
}
```

The registry is unbounded; a janitor goroutine runs every 10 min to
evict buckets last used > 1 h ago. The eviction key is approximated by
"the last token request time"; we store a `sync.Map` of last-touch
timestamps in parallel.

## 3. Middleware

```go
// api/internal/http/middleware/ratelimit_auth.go
func AuthRateLimit(cfg AuthRateLimitConfig, audit auth.AuditSink) func(http.Handler) http.Handler {
    reg := newRegistry()

    loginRule := rule{
        scope: "login", perMin: cfg.LoginPerMin, burst: cfg.Burst,
        keyOf: func(r *http.Request) string { return clientIP(r).String() },
        errType: "ip-throttled-login",
    }
    broadRule := rule{
        scope: "broad", perMin: cfg.BroadPerMin, burst: cfg.Burst,
        keyOf: func(r *http.Request) string { return clientIP(r).String() },
        errType: "ip-throttled-auth",
    }
    famRule := rule{
        scope: "fam", perMin: cfg.RefreshFamilyPerMin, burst: cfg.Burst,
        keyOf: refreshFamilyKey,                  // see §4
        errType: "refresh-family-throttled",
    }

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            rules := selectRules(r.URL.Path, loginRule, broadRule, famRule)
            for _, ru := range rules {
                key := ru.keyOf(r)
                if key == "" { continue }   // can't compute (e.g., bad refresh body) → skip
                b := reg.get(ru.scope, key, ru.perMin, ru.burst)
                if !b.Allow() {
                    w.Header().Set("Retry-After", strconv.Itoa(retryAfter(ru.perMin)))
                    audit.Record(r.Context(), auth.AuditRateLimited{
                        Scope: ru.scope, Key: key, Path: r.URL.Path,
                    })
                    problem(w, http.StatusTooManyRequests, ru.errType, "")
                    return
                }
            }
            next.ServeHTTP(w, r)
        })
    }
}

func selectRules(path string, login, broad, fam rule) []rule {
    switch {
    case path == "/api/auth/login":
        return []rule{login}
    case path == "/api/auth/refresh":
        return []rule{broad, fam}
    case strings.HasPrefix(path, "/api/auth/"):
        return []rule{broad}
    }
    return nil
}

func retryAfter(perMin int) int {
    // 60 / perMin, ceiling, with a 1s floor.
    s := 60 / perMin
    if s < 1 { s = 1 }
    return s
}
```

## 4. Per-token-family key extraction

The refresh handler's body is `{refresh_token: …}`. We need the `family_id`
to key the bucket. We can't fully verify the token in the middleware
(that's the handler's job), but we can parse it cheaply:

```go
// api/internal/http/middleware/ratelimit_auth.go
func refreshFamilyKey(r *http.Request) string {
    // Read up to 2KB of the body, parse JSON, extract refresh_token.
    // Restore the body for downstream readers.
    body, _ := io.ReadAll(io.LimitReader(r.Body, 2048))
    r.Body = io.NopCloser(bytes.NewReader(body))
    var b struct{ RefreshToken string `json:"refresh_token"` }
    _ = json.Unmarshal(body, &b)
    if b.RefreshToken == "" { return "" }
    id, _, err := auth.ParsePlaintext(b.RefreshToken)
    if err != nil { return "" }
    // We don't yet know the family_id — but the row's id is constant per
    // token, and rotation issues a new id with the same family_id. For
    // rate-limiting, keying by id is *almost* family-keyed because each
    // refresh consumes the id and a new one is issued. The buggy-client
    // case (story EC) keeps presenting the same id. Good enough.
    return id.String()
}
```

For v1 we accept the id-as-key approximation. v2 plan: cache `id →
family_id` in-process (looked up once, evicted on rotation).

## 5. Reconciliation matrix

Per the story's reconciliation note (REVIEW.md §1.4.a):

| Rule | Path | Cap | Source of truth |
|---|---|---|---|
| Broad auth | `/api/auth/*` excluding `/login` | 30/min/IP | This story AC-1 |
| Login (narrower, stricter) | `/api/auth/login` only | 10/min/IP | This story AC-3 + NFR Story 23.6 |
| Refresh per-family | `/api/auth/refresh` | 6/min/family | This story AC-2 |

The per-route burst defaults to 5 (config: `Auth.RateBurst`). NFR
Story 23.6's plan must reference these values rather than redefine.

## 6. Test plan

### 6.1 Middleware (`ratelimit_auth_test.go`)

| Test | What it pins |
|---|---|
| `TestLoginCapAt10PerMin` | 10 `POST /login` requests in 1s from one IP succeed; the 11th gets 429 `ip-throttled-login` with `Retry-After: 6`. |
| `TestLoginCapDifferentIPsIndependent` | 10 from IP A and 10 from IP B in the same window; no 429s. |
| `TestBroadCapAt30PerMin` | 30 `POST /refresh`/`/logout`/`/pair` mixed from one IP succeed; 31st → 429 `ip-throttled-auth`. |
| `TestRefreshAlsoBoundedByFamily` | 7 refreshes for the same token family in 1s → 7th gets 429 `refresh-family-throttled` (the per-family cap is 6, tighter than the 30-broad cap). |
| `TestRefreshBroadAndFamilyComposable` | 31 refreshes from one IP using 31 different families → 31st gets 429 from the broad cap (not the family cap, since each family only saw 1). |
| `TestNonAuthPathSkips` | `GET /api/videos/...` (non-`/api/auth/*`) → no rate-limit applied here (the general middleware in 7.19 covers it). |
| `TestRetryAfterReflectsCap` | `Retry-After` for login = 6 (60/10); for broad = 2 (60/30); for family = 10 (60/6). |
| `TestSafeMethodsCounted` | `OPTIONS /api/auth/login` is counted (rate-limit doesn't differentiate methods; auth surface is small). | (Or skip OPTIONS and document — see EC.) |
| `TestBucketJanitorEvictsStaleBuckets` | After 1h with no traffic for IP X, the bucket map no longer holds X. |
| `TestRateLimitWritesAudit` | 11th login from one IP writes one `audit_log` row `category='security', event='auth.rate-limited', payload={scope, key, path}`. |
| `TestBuggyClientSpammingRefreshThrottled` | Stub a client that hits /refresh every 5s; after the 6th, get 429s; the access tokens issued so far still verify. |
| `TestRateLimitMalformedRefreshBodySkipsFamilyRule` | Body is `{}` or invalid JSON → family rule's keyOf returns ""; skipped; broad rule still applies. |

### 6.2 Composition with general rate-limit (7.19)

| Test | What it pins |
|---|---|
| `TestGeneralMiddlewareTrumpsAuthCapWhenLooser` | If the general middleware allows 100/min and this one allows 30/min for `/api/auth/refresh`, the 31st refresh is blocked here, not by the general one. |
| `TestGeneralMiddlewareCanTripFirst` | If the operator sets the general cap to 5/min for `/api/auth/*`, the 6th request trips general's 429 *before* this middleware sees it. Both code paths return 429; the response body's `type` distinguishes. |

## 7. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| NAT-shared IP (office) | Login cap of 10/min may pinch. Admin raises via settings (Epic 7 Story 7.15); changes take effect on the next settings reload (the middleware reads cfg at construction; we add a `SwapConfig(cfg)` method to support hot-reload without restart). | `TestSwapConfigUpdatesCaps` |
| OPTIONS preflight to `/api/auth/login` | Counted by default; if this becomes a problem in browser-heavy traffic, we add `r.Method == OPTIONS` to the skip set. v1 keeps the simple shape. | Documented decision |
| Multi-replica deployment | Each replica has its own buckets → effective rate is up to N×limit. Documented as a known v1 limitation; v2 plan: shared Postgres-backed buckets via `pg_advisory_lock`. | Operations doc |
| Refresh body too large | LimitReader caps at 2KB; if the body is larger, we still parse the prefix (well within 2KB for `mkt_rt_v1.<id>.<32-byte secret>` ≈ 100 bytes). | `TestRateLimitLargeBodyHandled` |
| Bucket overflow between auth and lockout | Lockout (10.11) trips at 5 failures; rate-limit trips at 10 attempts (failed or successful). The 6th failed login from one IP in 1 min would 423 (lockout) before 429 (rate-limit). The handler's order is: rate-limit middleware → lockout check → verify. | `TestLockoutBeatsRateLimitOrder` |
| Empty refresh body | Family rule's keyOf returns ""; only the broad rule fires (correctly). | `TestRateLimitMalformedRefreshBodySkipsFamilyRule` |
| `clientIP(r)` returns 0.0.0.0 (proxy misconfig) | All zero-IP traffic shares one bucket — effectively rate-limited as one client. The Caddy front (Story 10.15) sets `X-Forwarded-For`. | Documented in operations |
| Legitimate user retries login after typo | Within 10/min budget; user can retry. After 10 attempts they hit the rate-limit before any per-username lock would matter. | `TestLoginCapAt10PerMin` |
| Memory growth | Per-IP unbounded; the janitor caps it. In a normal install, the bucket count stays under 1000. Pathological case (10K distinct IPs/min) the map grows to 10K entries — tens of MB. Documented. | `TestBucketJanitorEvictsStaleBuckets` |

## 8. Dependencies

| Dep | Version | Why |
|---|---|---|
| `golang.org/x/time/rate` | already (in stdlib-adjacent deps) | Token bucket. |

No new heavy deps.

## 9. Acceptance checklist

**Caps**
- [ ] AC-1: 31st `/api/auth/*` (excluding /login) request from one IP within 60s → 429.
- [ ] AC-2: 7th refresh for one family within 60s → 429.
- [ ] AC-3: 11th `/api/auth/login` from one IP within 60s → 429 (separate from AC-1's broader cap).

**Headers / payload**
- [ ] `Retry-After` reflects the cap: `ceil(60/perMin)`.
- [ ] Response body: problem+json with `type ∈ {ip-throttled-auth, ip-throttled-login, refresh-family-throttled}`.

**Composition**
- [ ] Plays correctly with Epic 7 Story 7.19's general middleware (whichever cap is tighter trips first).
- [ ] Skips non-`/api/auth/*` paths.

**Audit**
- [ ] Each 429 writes an `auth.rate-limited` audit row.

**Hot reload**
- [ ] `SwapConfig` allows changing limits without a restart.

**Tests**
- [ ] All §6 tests pass.

**Docs**
- [ ] README.md ticks story 10.12.
- [ ] Multi-replica caveat documented in operations.
