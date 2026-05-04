# Plan 10.12 — Rate limiting on auth endpoints — implementation

> Implementation plan for [story-10-12-rate-limiting-auth.md](story-10-12-rate-limiting-auth.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: coordinates with the broader rate-limit
> middleware from [Story 7.19](../07-api-service/story-07-19-rate-limit.md)
> (this story is a strictly-narrower wrapper for the auth surface), the
> server settings store from
> [Story 7.15](../07-api-service/story-07-15-settings.md) (`login_rate_per_min`,
> `auth_rate_per_min`, `refresh_rate_per_min`), and the lockout
> middleware in [Plan 10.11](plan-10-11-brute-force-protection.md) (this
> rate-limit middleware runs FIRST in the chain — fail-fast at the cheap
> check before paying the lockout DB lookup). REVIEW.md §1.4.a
> reconciliation: the narrow `/api/auth/login` cap is **10/min/IP**,
> aligning with NFR Story 23.6.
> **Distinction from lockout (Plan 10.11):** rate-limit caps
> requests-per-minute over a sliding 60 s window regardless of outcome;
> lockout is a 15+ min block triggered by repeated *failures*.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **In-process token-bucket via `golang.org/x/time/rate.Limiter`, with periodic write-back to Postgres for cross-replica visibility.** Each API replica keeps its own `*rate.Limiter` per (key) and increments a Postgres counter every 10 s for monitoring/alerting. The token-bucket decision is made *locally* in O(1). | Story prompt: "in-process limiter with periodic Postgres write-back for cross-replica state" | A pure Postgres-backed limiter would require a row-level lock on every auth request, which is exactly the operation we're trying to throttle. The in-process limiter is fast (~50 ns) and the per-bucket state survives until the next GC. The write-back is for observability, not enforcement. **Tradeoff:** two replicas each see the IP at 9/min ⇒ 18/min global. Acceptable because attackers don't know our replica count, and we'd need an external store (Redis) for tighter coordination, which adds an operational dependency. |
| D2 | **Three tiers, each its own limiter map keyed differently.** Tier-1 `/api/auth/login`: per-IP, **10/min** (narrow + strict — REVIEW.md §1.4.a reconciliation, NFR Story 23.6 alignment). Tier-2 `/api/auth/*` excluding login: per-IP, **30/min** (broader). Tier-3 `/api/auth/refresh`: per-token-family, **6/min** (per-family, not per-IP — a healthy device refreshes ~once per 10 min). | Story AC-1, AC-2, AC-3; REVIEW.md §1.4.a | Tier-1 has its own narrow limiter so tier-2's broader cap is independent: 10 logins + 20 other auth = 30 total fits the broader budget. Tier-3 is per-family because the threat is a buggy/malicious client spamming refresh, not a per-IP attack. |
| D3 | **Sliding-window approximation via `rate.Limiter` with `Burst = N` and `Limit = N/60 per second`.** A login bucket gets `rate.Every(6 * time.Second)` and `Burst = 10`, allowing up to 10 in any 60 s window with smoothing. | Standard `golang.org/x/time/rate` semantics | Token-bucket semantics most closely match "N per minute": tokens regenerate at `1/(60/N) sec`, burst is N. Empirically, a fixed-window counter is too coarse (allows 2N at the boundary); pure sliding-log requires per-request state. Token-bucket with the right parameters gives a smooth response. |
| D4 | **`Retry-After` is the seconds until the next token is available** (`limiter.Reserve().Delay() / time.Second`, rounded up). | Story AC-1: "429 with `Retry-After`" | Returning a precise second count helps polite clients back off correctly without retry-storms. The value is bounded above by the configured tier's regeneration rate. |
| D5 | **LRU eviction on the limiter map** with `max_buckets = 100_000` per tier. Evicted buckets are recreated fresh (regenerated on next request). | Standard practice for in-memory rate limiters; refines the story | Without eviction the map grows unbounded under attack (every spoofed IP gets a bucket). 100k buckets ≈ 32 MB at our struct size — fine. Eviction is hashicorp/golang-lru's standard `simplelru`; old buckets are typically already at max-capacity and recreating them just resets the counter, which is acceptable behavior. |
| D6 | **Configurable via `[server].login_rate_per_min`, `[server].auth_rate_per_min`, `[server].refresh_rate_per_min` (Story 7.15).** Settings reload triggers a rebuild of the LRU and a swap; in-flight requests use the old limiter (no panic). Defaults: `10`, `30`, `6`. | Story prompt + Edge case "Admin can raise the limits via settings; the changes take effect on the next settings reload." | Operator-tunable for high-volume deployments (NAT-shared offices) without redeploy. Atomic swap via `atomic.Pointer[tierState]` keeps the hot path lock-free. |
| D7 | **Postgres write-back to `auth_rate_limits` is observability-only.** Every 10 s, the API flushes per-key counters (`reqs_in_window`, `denials_in_window`) to a small table for dashboards/alerts. The decision path never reads this table. | Story prompt + standard SRE pattern | Decoupling enforcement from observability lets the hot path stay in-process while still giving operators a query surface. The schema is intentionally tiny so it can be `TRUNCATE`d on settings reload. |

If D1 is rejected (Postgres-backed enforcement): every auth request
takes a row-lock, p99 latency on `/api/auth/login` jumps to ~5 ms minimum
(round-trip to Postgres), and contention on a popular IP serializes
requests. In-process is the right call for v1; an external Redis store
can be added later if multi-replica coordination becomes necessary.

If D2's per-token-family tier-3 cap is rejected (use per-IP for
refresh): mobile devices behind a shared NAT can collectively exceed
30/min, but each device individually refreshes once per 10 min — the
per-family cap correctly identifies the misbehaving client.

---

## 1. Architecture diagram — three-tier rate-limit middleware

```
   POST /api/auth/login                       POST /api/auth/refresh
          │                                          │
          ▼                                          ▼
   ┌──────────────────────────────┐    ┌────────────────────────────────┐
   │ ratelimit.LoginMiddleware    │    │ ratelimit.AuthMiddleware       │
   │   key   = clientIP(r)        │    │   key   = clientIP(r)          │
   │   limit = 10/min, burst=10   │    │   limit = 30/min, burst=30     │
   │   bucket lookup in LRU       │    │   bucket lookup in LRU         │
   │   reserve()                  │    │   reserve()                    │
   │     delay==0 ─► allow        │    │     delay==0 ─► allow          │
   │     delay >0 ─► 429 + RA     │    │     delay >0 ─► 429 + RA       │
   └──────────────┬───────────────┘    └────────────────┬───────────────┘
                  │                                     │
                  ▼                                     ▼
   ┌──────────────────────────────┐    ┌────────────────────────────────┐
   │ ratelimit.RefreshFamilyMW    │    │  (no extra family check;       │
   │   key = parseFamilyFromBody  │    │   tier-2 cap is the only       │
   │   limit = 6/min, burst=6     │    │   guard for non-refresh)       │
   │   reserve()                  │    │                                │
   │     delay==0 ─► allow        │    │                                │
   │     delay >0 ─► 429 + RA     │    │                                │
   └──────────────┬───────────────┘    └────────────────┬───────────────┘
                  │                                     │
                  ▼                                     ▼
        lockout.Middleware (Plan 10.11)        login/refresh handler
                  │                                     │
                  ▼                                     ▼
        login/refresh handler                  Response

   Out-of-band:
     write-back goroutine (10 s):
       INSERT INTO auth_rate_limits (key, count, window_start)
         VALUES (...) ON CONFLICT DO UPDATE  -- observability only

     LRU eviction:
       max 100k buckets per tier; victim is the LRU-tail.
```

Decision is **always local** (no DB read on the hot path). The lockout
middleware (Plan 10.11) runs *after* this middleware in the chain so an
IP that's spamming gets cheap-rejected before paying the lockout DB
roundtrip.

---

## 2. Detailed implementation

### 2.1 Package layout — Go (API Service)

```
api/
├── internal/
│   ├── auth/
│   │   ├── ratelimit/
│   │   │   ├── tier.go            # tierState struct (LRU + rate.Limit)
│   │   │   ├── limiter.go         # AcquireOrDeny per-key
│   │   │   ├── login_mw.go        # LoginMiddleware factory
│   │   │   ├── auth_mw.go         # AuthMiddleware factory
│   │   │   ├── refresh_mw.go      # RefreshFamilyMiddleware factory
│   │   │   ├── settings.go        # SettingsListener — atomic swap on reload
│   │   │   ├── writeback.go       # background flush to auth_rate_limits
│   │   │   ├── error.go           # writeProblem helper
│   │   │   ├── queries.sql        # sqlc input
│   │   │   └── *_test.go
│   │   └── ...
│   └── ...
└── shared/db/migrations/
    └── 00XX_auth_rate_limits.sql
```

### 2.2 Schema migration — `auth_rate_limits` (observability-only)

```sql
-- shared/db/migrations/0043_auth_rate_limits.sql
BEGIN;

CREATE TABLE auth_rate_limits (
    key            TEXT PRIMARY KEY,         -- e.g. "login:10.0.0.1" / "refresh:fam:<uuid>"
    tier           TEXT NOT NULL,            -- 'login' | 'auth' | 'refresh'
    count          BIGINT NOT NULL DEFAULT 0,-- cumulative reqs in the window
    denied         BIGINT NOT NULL DEFAULT 0,-- cumulative 429s in the window
    window_start   TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (count >= 0),
    CHECK (denied >= 0)
);
CREATE INDEX auth_rate_limits_window_start ON auth_rate_limits (window_start);

COMMIT;
```

### 2.3 Tier state + LRU

```go
// api/internal/auth/ratelimit/tier.go
package ratelimit

import (
    "sync"
    "sync/atomic"
    "time"

    lru "github.com/hashicorp/golang-lru/v2"
    "golang.org/x/time/rate"
)

const MaxBucketsPerTier = 100_000

type tierState struct {
    name    string             // "login" | "auth" | "refresh"
    perMin  int                // current cap (settings-driven)
    cache   *lru.Cache[string, *rate.Limiter]
    mu      sync.Mutex         // guards Cache writes (lru.Cache is thread-safe for Get/Add but not for swap)
}

func newTierState(name string, perMin int) *tierState {
    c, _ := lru.New[string, *rate.Limiter](MaxBucketsPerTier)
    return &tierState{name: name, perMin: perMin, cache: c}
}

// limiterFor returns the rate.Limiter for key, creating one on miss.
func (t *tierState) limiterFor(key string) *rate.Limiter {
    if l, ok := t.cache.Get(key); ok { return l }
    t.mu.Lock()
    defer t.mu.Unlock()
    if l, ok := t.cache.Get(key); ok { return l } // double-check after lock
    every := time.Minute / time.Duration(t.perMin)
    l := rate.NewLimiter(rate.Every(every), t.perMin)
    t.cache.Add(key, l)
    return l
}
```

### 2.4 Atomic-swap settings holder (D6)

```go
// api/internal/auth/ratelimit/settings.go
package ratelimit

import "sync/atomic"

// Tiers holds the three current limiters. Replaced atomically on settings reload.
type Tiers struct {
    Login   *tierState
    Auth    *tierState
    Refresh *tierState
}

type Holder struct{ p atomic.Pointer[Tiers] }

func (h *Holder) Load() *Tiers       { return h.p.Load() }
func (h *Holder) Swap(t *Tiers)      { h.p.Store(t) }

// OnSettingsChange is called by the Story 7.15 settings reloader.
func (h *Holder) OnSettingsChange(loginPM, authPM, refreshPM int) {
    h.Swap(&Tiers{
        Login:   newTierState("login",   loginPM),
        Auth:    newTierState("auth",    authPM),
        Refresh: newTierState("refresh", refreshPM),
    })
}
```

### 2.5 Decision helper

```go
// api/internal/auth/ratelimit/limiter.go
package ratelimit

import "time"

type Decision struct {
    Allowed    bool
    RetryAfter time.Duration
}

// AcquireOrDeny consults the limiter; on deny, computes Retry-After.
func AcquireOrDeny(t *tierState, key string) Decision {
    l := t.limiterFor(key)
    res := l.Reserve()
    if !res.OK() {
        // Should be unreachable with our config but defend.
        return Decision{Allowed: false, RetryAfter: time.Minute}
    }
    delay := res.Delay()
    if delay == 0 {
        return Decision{Allowed: true}
    }
    res.Cancel() // give the token back; we are returning 429.
    if delay < time.Second { delay = time.Second }
    return Decision{Allowed: false, RetryAfter: delay}
}
```

### 2.6 Login middleware (Tier-1 — 10/min/IP)

```go
// api/internal/auth/ratelimit/login_mw.go
package ratelimit

import (
    "fmt"
    "net/http"
)

func LoginMiddleware(h *Holder) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := clientIP(r).String()
            t := h.Load()
            if t == nil { next.ServeHTTP(w, r); return } // not-yet-loaded; fail-open at boot
            d := AcquireOrDeny(t.Login, "login:"+ip)
            if !d.Allowed {
                w.Header().Set("Retry-After", fmt.Sprintf("%.0f", d.RetryAfter.Seconds()))
                writeProblem(w, http.StatusTooManyRequests, "rate-limited-login",
                    "login rate limit exceeded for this IP")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### 2.7 Auth middleware (Tier-2 — 30/min/IP, excluding login)

```go
// api/internal/auth/ratelimit/auth_mw.go
package ratelimit

import (
    "fmt"
    "net/http"
    "strings"
)

func AuthMiddleware(h *Holder) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Login has its own narrower bucket (Tier-1) — skip Tier-2 here.
            if strings.HasSuffix(r.URL.Path, "/auth/login") {
                next.ServeHTTP(w, r); return
            }
            ip := clientIP(r).String()
            t := h.Load()
            if t == nil { next.ServeHTTP(w, r); return }
            d := AcquireOrDeny(t.Auth, "auth:"+ip)
            if !d.Allowed {
                w.Header().Set("Retry-After", fmt.Sprintf("%.0f", d.RetryAfter.Seconds()))
                writeProblem(w, http.StatusTooManyRequests, "rate-limited-auth",
                    "auth rate limit exceeded for this IP")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### 2.8 Refresh-family middleware (Tier-3 — 6/min/family)

```go
// api/internal/auth/ratelimit/refresh_mw.go
package ratelimit

import (
    "fmt"
    "net/http"
)

// RefreshFamilyMiddleware extracts the refresh token's family_id from
// the request body or Authorization header (the refresh token itself is
// hashed; we read the family_id metadata that the client also sends as
// part of the refresh handshake — Story 10.4 owns the request shape).
func RefreshFamilyMiddleware(h *Holder, extractFamily func(*http.Request) string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            fam := extractFamily(r)
            if fam == "" {
                // Cannot derive family_id (malformed refresh body) — let the
                // refresh handler return its own 400.
                next.ServeHTTP(w, r); return
            }
            t := h.Load()
            if t == nil { next.ServeHTTP(w, r); return }
            d := AcquireOrDeny(t.Refresh, "refresh:fam:"+fam)
            if !d.Allowed {
                w.Header().Set("Retry-After", fmt.Sprintf("%.0f", d.RetryAfter.Seconds()))
                writeProblem(w, http.StatusTooManyRequests, "rate-limited-refresh",
                    "refresh rate limit exceeded for this token family")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### 2.9 Write-back goroutine (D7)

```go
// api/internal/auth/ratelimit/writeback.go
package ratelimit

import (
    "context"
    "log/slog"
    "time"
)

// StartWriteback flushes per-tier denial counts to auth_rate_limits
// every interval. Observability only — never read on the request path.
func StartWriteback(ctx context.Context, h *Holder, q *db.Queries, interval time.Duration) {
    go func() {
        t := time.NewTicker(interval)
        defer t.Stop()
        for {
            select {
            case <-ctx.Done(): return
            case <-t.C:
                tiers := h.Load()
                if tiers == nil { continue }
                flushTier(ctx, q, "login",   tiers.Login)
                flushTier(ctx, q, "auth",    tiers.Auth)
                flushTier(ctx, q, "refresh", tiers.Refresh)
            }
        }
    }()
}

func flushTier(ctx context.Context, q *db.Queries, name string, t *tierState) {
    // Snapshot+upsert; counters are read-only via the LRU cache.
    keys := t.cache.Keys()
    if len(keys) == 0 { return }
    if err := q.UpsertRateLimitObservability(ctx, name, keys, time.Now()); err != nil {
        slog.Warn("ratelimit_writeback_err", "tier", name, "err", err)
    }
}
```

### 2.10 SQL — observability upsert

```sql
-- api/internal/auth/ratelimit/queries.sql

-- name: UpsertRateLimitObservability :exec
INSERT INTO auth_rate_limits (key, tier, count, window_start, updated_at)
SELECT k, $1, 0, $3, now() FROM unnest($2::text[]) AS k
ON CONFLICT (key) DO UPDATE SET
    updated_at = now(),
    window_start = EXCLUDED.window_start;
```

### 2.11 Wire-up

```go
// api/cmd/api/main.go (excerpt)
holder := &ratelimit.Holder{}
holder.OnSettingsChange(
    serverCfg.LoginRatePerMin,    // default 10
    serverCfg.AuthRatePerMin,     // default 30
    serverCfg.RefreshRatePerMin,  // default 6
)
settingsBus.Subscribe(func(s settings.Server) {
    holder.OnSettingsChange(s.LoginRatePerMin, s.AuthRatePerMin, s.RefreshRatePerMin)
})
ratelimit.StartWriteback(rootCtx, holder, ratelimitDB, 10*time.Second)

extractFamily := refresh.NewFamilyExtractor() // owned by Story 10.4
r.Route("/api/auth", func(r chi.Router) {
    r.Use(ratelimit.AuthMiddleware(holder))   // tier-2: 30/min/IP for non-login
    r.Group(func(r chi.Router) {
        r.Use(ratelimit.LoginMiddleware(holder))  // tier-1: 10/min/IP
        r.Post("/login", loginHandler)
    })
    r.Group(func(r chi.Router) {
        r.Use(ratelimit.RefreshFamilyMiddleware(holder, extractFamily)) // tier-3: 6/min/family
        r.Post("/refresh", refreshHandler)
    })
    r.Post("/logout", logoutHandler) // covered by tier-2 only
})
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0043_auth_rate_limits.sql` | `auth_rate_limits` + index | `TestMigration_AuthRateLimits` |
| 2 | `api/internal/auth/ratelimit/tier.go` | `tierState`, `newTierState`, `limiterFor`, `MaxBucketsPerTier` | `TestTierState_*` |
| 3 | `api/internal/auth/ratelimit/settings.go` | `Tiers`, `Holder`, `OnSettingsChange` | `TestHolder_AtomicSwap` |
| 4 | `api/internal/auth/ratelimit/limiter.go` | `Decision`, `AcquireOrDeny` | `TestAcquireOrDeny_*` |
| 5 | `api/internal/auth/ratelimit/error.go` | `writeProblem` (private), `clientIP` | (covered by mw tests) |
| 6 | `api/internal/auth/ratelimit/login_mw.go` | `LoginMiddleware` | `TestLoginMiddleware_*` |
| 7 | `api/internal/auth/ratelimit/auth_mw.go` | `AuthMiddleware` | `TestAuthMiddleware_*` |
| 8 | `api/internal/auth/ratelimit/refresh_mw.go` | `RefreshFamilyMiddleware` | `TestRefreshFamilyMiddleware_*` |
| 9 | `api/internal/auth/ratelimit/queries.sql` (+ sqlc) | `UpsertRateLimitObservability` | `TestUpsertRateLimitObservability` |
| 10 | `api/internal/auth/ratelimit/writeback.go` | `StartWriteback`, `flushTier` | `TestStartWriteback` |
| 11 | `api/cmd/api/main.go` (extend) | wire holder + settings subscribe + writeback | integration `TestRoutes_AuthRateLimits` |

Settings keys (Story 7.15 owns the schema; this story consumes):
`[server].login_rate_per_min` (default 10), `[server].auth_rate_per_min`
(default 30), `[server].refresh_rate_per_min` (default 6).

---

## 4. Test cases keyed to acceptance criteria

### 4.1 `TestLoginMiddleware_ElevenInOneMinute_AtLeastOne429` (AC-3)

```go
func TestLoginMiddleware_ElevenInOneMinute_AtLeastOne429(t *testing.T) {
    h := newHolderForTest(10, 30, 6)
    mw := ratelimit.LoginMiddleware(h)(noopHandler())

    rejections := 0
    for i := 0; i < 11; i++ {
        rr := postLogin(mw, "10.0.0.1")
        if rr.Code == http.StatusTooManyRequests {
            rejections++
        }
    }
    require.GreaterOrEqual(t, rejections, 1,
        "11 logins in <60s should produce >=1 429 from the 10/min cap")
}
```

### 4.2 `TestAuthMiddleware_ThirtyOneRefreshes_429FromBroaderCap` (AC-1, refresh in broader surface)

```go
func TestAuthMiddleware_ThirtyOneRefreshes_429FromBroaderCap(t *testing.T) {
    h := newHolderForTest(10, 30, 60_000) // bump refresh tier so it doesn't fire
    mwAuth    := ratelimit.AuthMiddleware(h)
    mwRefresh := ratelimit.RefreshFamilyMiddleware(h, fixedFamily("F1"))
    chain := mwAuth(mwRefresh(noopHandler()))

    rejections := 0
    for i := 0; i < 31; i++ {
        rr := postRefresh(chain, "10.0.0.7", "F"+strconv.Itoa(i)) // unique fams
        if rr.Code == http.StatusTooManyRequests { rejections++ }
    }
    require.GreaterOrEqual(t, rejections, 1)
    rr := postRefresh(chain, "10.0.0.7", "Fz")
    require.Equal(t, http.StatusTooManyRequests, rr.Code)
    require.NotEmpty(t, rr.Header().Get("Retry-After"))
}
```

### 4.3 `TestRefreshFamilyMiddleware_SevenInARow_429AfterSix` (AC-2)

```go
func TestRefreshFamilyMiddleware_SevenInARow_429AfterSix(t *testing.T) {
    h := newHolderForTest(60_000, 60_000, 6) // bump login/auth so tier-3 fires
    mw := ratelimit.RefreshFamilyMiddleware(h, fixedFamily("BAD-CLIENT"))(noopHandler())

    for i := 0; i < 6; i++ {
        rr := postRefresh(mw, "10.0.0.1", "BAD-CLIENT")
        require.Equal(t, http.StatusOK, rr.Code, "iter %d", i)
    }
    rr := postRefresh(mw, "10.0.0.1", "BAD-CLIENT")
    require.Equal(t, http.StatusTooManyRequests, rr.Code)
    require.NotEmpty(t, rr.Header().Get("Retry-After"))
}
```

### 4.4 `TestLoginMiddleware_PerIPIsolation`

```go
func TestLoginMiddleware_PerIPIsolation(t *testing.T) {
    h := newHolderForTest(10, 30, 6)
    mw := ratelimit.LoginMiddleware(h)(noopHandler())

    for i := 0; i < 10; i++ {
        rr := postLogin(mw, "10.0.0.1")
        require.Equal(t, http.StatusOK, rr.Code)
    }
    // 11th from .1 → 429
    require.Equal(t, http.StatusTooManyRequests, postLogin(mw, "10.0.0.1").Code)
    // .2 unaffected
    require.Equal(t, http.StatusOK, postLogin(mw, "10.0.0.2").Code)
}
```

### 4.5 `TestHolder_SettingsReloadHotSwap` (D6)

```go
func TestHolder_SettingsReloadHotSwap(t *testing.T) {
    h := &ratelimit.Holder{}
    h.OnSettingsChange(2, 30, 6)
    mw := ratelimit.LoginMiddleware(h)(noopHandler())
    require.Equal(t, http.StatusOK,            postLogin(mw, "1.1.1.1").Code)
    require.Equal(t, http.StatusOK,            postLogin(mw, "1.1.1.1").Code)
    require.Equal(t, http.StatusTooManyRequests, postLogin(mw, "1.1.1.1").Code)
    // Operator raises the limit.
    h.OnSettingsChange(20, 30, 6)
    // New tier state has fresh buckets — request proceeds.
    require.Equal(t, http.StatusOK, postLogin(mw, "1.1.1.1").Code)
}
```

### 4.6 `TestAcquireOrDeny_RetryAfterRoundedUp` (D4)

```go
func TestAcquireOrDeny_RetryAfterRoundedUp(t *testing.T) {
    ts := newTierStateExp("test", 10) // 1 token / 6s
    // Exhaust burst.
    for i := 0; i < 10; i++ { ratelimit.AcquireOrDenyExp(ts, "k") }
    d := ratelimit.AcquireOrDenyExp(ts, "k")
    require.False(t, d.Allowed)
    require.GreaterOrEqual(t, d.RetryAfter, time.Second)
    require.LessOrEqual(t, d.RetryAfter, 6*time.Second)
}
```

### 4.7 `TestLRU_Eviction` (D5)

```go
func TestLRU_Eviction(t *testing.T) {
    ts := newTierStateExp("test", 10)
    // Fill the LRU + 1.
    for i := 0; i < ratelimit.MaxBucketsPerTier+1; i++ {
        ratelimit.AcquireOrDenyExp(ts, fmt.Sprintf("k%d", i))
    }
    require.Equal(t, ratelimit.MaxBucketsPerTier, ts.LenExp())
}
```

### 4.8 `TestWriteback_FlushesToPostgres` (D7)

```go
func TestWriteback_FlushesToPostgres(t *testing.T) {
    db := openTestDB(t)
    h := newHolderForTest(10, 30, 6)
    mw := ratelimit.LoginMiddleware(h)(noopHandler())
    for i := 0; i < 5; i++ { postLogin(mw, "10.0.0.5") }

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    ratelimit.StartWriteback(ctx, h, db, 100*time.Millisecond)
    time.Sleep(300 * time.Millisecond)

    var n int
    require.NoError(t, db.QueryRow(
        "SELECT count(*) FROM auth_rate_limits WHERE tier='login' AND key='login:10.0.0.5'",
    ).Scan(&n))
    require.Equal(t, 1, n)
}
```

### 4.9 Integration `TestRoutes_AuthRateLimits` (story-acceptance e2e)

Spin up the full chi router with all three tiers wired. Issue 11 logins
from `10.0.0.1` → at least one 429 with `Retry-After` headed (AC-3).
Issue 31 refreshes from `10.0.0.2` with unique family ids → at least
one 429 from tier-2 (AC-1). Issue 7 refreshes from `10.0.0.3` with the
same family id → 7th gets 429 from tier-3 (AC-2).

### 4.10 `TestRetryAfterIsSecondsUntilNextToken` (D4 negative)

```go
func TestRetryAfterIsSecondsUntilNextToken(t *testing.T) {
    h := newHolderForTest(10, 30, 6)
    mw := ratelimit.LoginMiddleware(h)(noopHandler())
    for i := 0; i < 10; i++ { postLogin(mw, "10.0.0.1") }
    rr := postLogin(mw, "10.0.0.1")
    ra := rr.Header().Get("Retry-After")
    n, err := strconv.Atoi(ra)
    require.NoError(t, err)
    require.GreaterOrEqual(t, n, 1)
    require.LessOrEqual(t, n, 6)
}
```

---

## 5. Edge cases and how the plan handles each

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **NAT-shared IP (office).** A 10/min/login cap can pinch a busy office. Mitigation: operator raises `[server].login_rate_per_min` via Story 7.15 settings; `Holder.OnSettingsChange` swaps the tier state atomically. Documented for operators in §2.11. | D6, `TestHolder_SettingsReloadHotSwap` |
| E2 | **IPv6 client.** `clientIP` returns a `netip.Addr` whose `String()` works for IPv4 and IPv6; the limiter key is the string form. No special path. | `clientIP` helper |
| E3 | **Two replicas, same IP.** Each replica has its own LRU and bucket; combined effective rate is up to `2 * cap`. Documented tradeoff in D1. For tighter coordination, see "v2 roadmap" below. | D1; documentation |
| E4 | **Refresh request with malformed body** (no derivable family_id). `extractFamily` returns `""`; the middleware passes through to the refresh handler which returns its own 400. The auth tier-2 cap still applies (the request still counts against IP). | `RefreshFamilyMiddleware` early return |
| E5 | **Settings reload while requests are in flight.** The atomic pointer swap publishes a fresh `Tiers`. In-flight requests holding a `tierState` reference continue against the old state until they return — no panic, no torn read. | `atomic.Pointer[Tiers]`; `TestHolder_SettingsReloadHotSwap` |
| E6 | **Login + refresh in parallel from same IP.** Login uses tier-1's bucket; refresh uses tier-2's bucket; the per-family bucket is independent. They do not interfere; each is checked separately. | Tier separation by middleware factory |
| E7 | **Burst at minute boundary.** Token-bucket smoothing prevents the classic "double window" issue where 2N reqs land at the boundary; tokens regenerate continuously at `1/(60/N) sec`. | D3 |
| E8 | **Adversary spawning many family ids on refresh** to evade tier-3. The tier-3 cap is per-family, but the request *also* counts against tier-2's per-IP cap (30/min) — so an attacker spamming refresh with rotating family ids hits tier-2 at the 31st request. | Verified by `TestAuthMiddleware_ThirtyOneRefreshes_429FromBroaderCap` (with unique fam-ids per request, tier-2 still fires.) |
| E9 | **Holder not yet loaded at request time** (race during boot). `Holder.Load()` returns nil → middleware passes through (fail-open). The risk is a sub-second window at boot before settings are first applied. We accept it; calling `OnSettingsChange` synchronously before mounting routes is the standard wire-up pattern. | `LoginMiddleware`/`AuthMiddleware`/`RefreshFamilyMiddleware` early returns |
| E10 | **Bucket evicted from LRU mid-attack.** Recreated fresh on next request → counter resets, attacker may proceed slightly. Acceptable: 100k buckets is large enough that legitimate flows aren't affected; victim of eviction is the LRU-tail (least recently used), which is by definition not the active attacker. | D5; `TestLRU_Eviction` |
| E11 | **Postgres write-back fails repeatedly.** The hot path is unaffected — observability is decoupled. `StartWriteback` logs the error and continues. Operators see missing metrics in their dashboards as the alert. | D7; `flushTier` `slog.Warn` |
| E12 | **REVIEW.md §1.4.a NFR alignment.** This plan ships the 10/min/IP login cap that NFR Story 23.6 requires. If NFR Story 23.6 specifies a different number, this plan's defaults need a one-line edit in `cmd/api/main.go` defaults; the constants live in settings, not code. | Documented in §1; `[server].login_rate_per_min = 10` is the authoritative value. |

---

## 6. Acceptance checklist

- [ ] **A1** Tier-1 (`/api/auth/login`): per-IP cap of 10/min (configurable via `[server].login_rate_per_min`); 11th request in any 60 s window returns 429 with `Retry-After`. (`TestLoginMiddleware_ElevenInOneMinute_AtLeastOne429`)
- [ ] **A2** Tier-2 (`/api/auth/*` excluding login): per-IP cap of 30/min (configurable via `[server].auth_rate_per_min`); 31st request returns 429 with `Retry-After`. (`TestAuthMiddleware_ThirtyOneRefreshes_429FromBroaderCap`)
- [ ] **A3** Tier-3 (`/api/auth/refresh`): per-token-family cap of 6/min (configurable via `[server].refresh_rate_per_min`); 7th refresh for the same family returns 429 with `Retry-After`. (`TestRefreshFamilyMiddleware_SevenInARow_429AfterSix`)
- [ ] **A4** `Retry-After` header is the integer seconds until the next token is available, ≥1, ≤ tier-regeneration period. (`TestRetryAfterIsSecondsUntilNextToken`, `TestAcquireOrDeny_RetryAfterRoundedUp`)
- [ ] **A5** Decision is in-process (no DB read) on the hot path; per-tier LRU caches up to `MaxBucketsPerTier=100_000` buckets with LRU eviction. (`TestLRU_Eviction`; code review on `tier.go`/`limiter.go`.)
- [ ] **A6** Periodic write-back to `auth_rate_limits` (Postgres) every 10 s for observability only — never read on the request path. (`TestWriteback_FlushesToPostgres`)
- [ ] **A7** Settings reload via Story 7.15 `[server].*_rate_per_min` triggers atomic-swap of tier state; in-flight requests continue against the old state without panic. (`TestHolder_SettingsReloadHotSwap`)
- [ ] **A8** Migration `0043_auth_rate_limits.sql` creates the `auth_rate_limits` table with `(key TEXT PK, tier TEXT, count BIGINT, denied BIGINT, window_start TIMESTAMPTZ, updated_at TIMESTAMPTZ)` plus the `window_start` index. (`TestMigration_AuthRateLimits`)
- [ ] **A9** Per-IP isolation: hitting the 10/min cap from `10.0.0.1` does not affect requests from `10.0.0.2`. (`TestLoginMiddleware_PerIPIsolation`)
- [ ] **A10** This middleware is *distinct* from the lockout middleware (Plan 10.11): rate-limit caps requests/minute over a sliding 60 s window; lockout is a 15+ min block triggered by *failures*. Rate-limit runs FIRST in the middleware chain. (Documented in §1; integration `TestAuthChain_RateLimitThenLockout`.)
- [ ] **A11** REVIEW.md §1.4.a reconciliation: the narrow `/api/auth/login` cap is **10/min/IP**, aligning with NFR Story 23.6's "10/min per IP for login". The broader `/api/auth/*` cap is **30/min/IP**. (Documented in §0 and the settings defaults.)
- [ ] **A12** Token-bucket via `golang.org/x/time/rate`: `rate.Every(time.Minute / N)` and `Burst = N` for each tier, giving smoothing within the window. (`TestAcquireOrDeny_RetryAfterRoundedUp`)
- [ ] **A13** Multi-replica behavior is documented as a tradeoff: each replica enforces its own cap; combined effective cap is `replicas * cap`. Acceptable for v1; v2 hook to use Redis if tighter coordination becomes necessary. (Documented in §0 D1 and §5 E3.)
- [ ] **A14** 429 responses follow Problem Details (RFC 7807) with `type=rate-limited-{login|auth|refresh}`, `Content-Type: application/problem+json`. (`writeProblem` review.)
