# Implementation Plan — Story 10.9 Single-user mode (admin token bypass)

> Companion to [story-10-09-single-user-mode.md](story-10-09-single-user-mode.md).
> Sentinel UUID is seeded by [Story 10.1](plan-10-01-user-store.md). The
> `lib[]` enrichment for the admin path leans on
> [Story 10.13](story-10-13-permission-model.md). Internal mints reuse
> the [Story 10.8](plan-10-08-signed-url-minter.md) minter.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Token loader | `api/internal/auth/admintoken.go` — reads `MAKTABA_ADMIN_TOKEN` once at boot; refuses < 32 chars. |
| Auth middleware | `api/internal/http/middleware/admintoken.go` — sits *before* both bearer and session middleware. Constant-time compare. |
| Cookie variant | Same middleware also accepts `mkt_admin_token=<token>` cookie (story AC-1). |
| Sentinel user | UUID `00000000-0000-0000-0000-000000000001` (seeded by Story 10.1). |
| Internal-mint `lib[]` | When the minter is invoked under the admin-token path (e.g., a background task), it uses `AllLibraryIDs` (already implemented in Story 10.8 §3). |
| Out of scope | First-boot UI dialog (web frontend / Epic 11 owns it; this story exposes the env var and the route). |

## 1. Architecture diagram

```
                    env: MAKTABA_ADMIN_TOKEN (≥32 chars)
                              │
                              ▼
              ┌─────────────────────────────────┐
              │ admintoken.Loader                │
              │   - read once at boot            │
              │   - require length >= 32         │
              │   - hold canonical bytes         │
              └────────────┬─────────────────────┘
                           │
                           ▼
       ┌────────────────────────────────────────────────────┐
       │ middleware/admintoken.go (request path)             │
       │   - read Authorization: Bearer <…> OR cookie         │
       │   - subtle.ConstantTimeCompare(candidate, canonical) │
       │     (one branch only — no length-shortcut)           │
       │   - on match:                                        │
       │       ctx := auth.WithUser(ctx, sentinel)            │
       │       ctx := auth.WithAdminTokenPath(ctx, true)      │
       │       audit (sampled): event='admin-token.used'      │
       │   - on miss: pass through to bearer/session mw       │
       └─────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/auth/admintoken.go` | `Loader.Load()`, `Loader.Match([]byte) bool`. |
| `api/internal/http/middleware/admintoken.go` | Middleware. |
| `api/internal/auth/sentinel.go` | `var SentinelUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")`. |
| `api/internal/auth/admintoken_test.go` | Unit tests for loader + match. |
| `api/internal/http/middleware/admintoken_test.go` | Middleware integration tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Already has `App.AdminTokenEnv` (default `"MAKTABA_ADMIN_TOKEN"`). Add validator that warns when set on a multi-user install. Add `Auth.AdminTokenMinLen` (default 32). |
| `api/cmd/api/main.go` | Boot `Loader`; if `env != ""`, register middleware. If `env != ""` and `len(token) < 32`, refuse to start with `error: admin-token-too-short`. |
| `api/internal/http/router.go` | Install admintoken middleware *before* the bearer middleware (Story 10.3) and session middleware (Story 10.2). |
| `api/internal/auth/signedurl/minter.go` | The `resolveAccess` path already returns `AllLibraryIDs` for `IsAdmin=true` — the sentinel user is `IsAdmin=true` so this works without change. |
| `api/internal/audit/sink.go` | Add a token-bucket sampler keyed on `(ip, day)` so `admin-token.used` is sampled at 1/min per IP, with the first-per-IP-per-day always logged. |

### 2.3 Type definitions

```go
// api/internal/auth/admintoken.go
type Loader struct {
    canonical []byte   // raw bytes from env var
    enabled   bool
}

func NewLoader(env string, minLen int) (*Loader, error) {
    val := os.Getenv(env)
    if val == "" {
        return &Loader{enabled: false}, nil
    }
    if len(val) < minLen {
        return nil, fmt.Errorf("auth: admin-token-too-short: %s must be >= %d chars", env, minLen)
    }
    return &Loader{canonical: []byte(val), enabled: true}, nil
}

func (l *Loader) Enabled() bool { return l.enabled }

func (l *Loader) Match(candidate []byte) bool {
    if !l.enabled { return false }
    // ConstantTimeCompare returns 0 if lengths differ — safely false.
    return subtle.ConstantTimeCompare(candidate, l.canonical) == 1
}
```

`subtle.ConstantTimeCompare` already returns 0 (no match) when lengths
differ; per AC-2 we *do not* short-circuit on length and we never log
or compare the candidate string itself.

## 3. Middleware

```go
// api/internal/http/middleware/admintoken.go
package middleware

import (
    "net/http"
    "strings"

    "maktaba/api/internal/auth"
)

func AdminToken(loader *auth.Loader, sentinel auth.User, audit auth.AuditSink) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if !loader.Enabled() {
                next.ServeHTTP(w, r)
                return
            }

            cand := pickCandidate(r)
            if cand == "" {
                next.ServeHTTP(w, r)
                return
            }
            if !loader.Match([]byte(cand)) {
                // Wrong token presented — DO NOT 401 here. The request might
                // still authenticate via bearer (Story 10.3) or cookie
                // (Story 10.2). The downstream middleware decides.
                next.ServeHTTP(w, r)
                return
            }

            ctx := auth.WithUser(r.Context(), sentinel)
            ctx  = auth.WithAdminTokenPath(ctx, true)
            audit.Record(ctx, auth.AuditAdminTokenUsed{IP: clientIP(r)})

            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// pickCandidate returns the candidate token from either header or cookie.
// We return an empty string when neither is present — the caller treats
// that as "no admin-token presented". We never trim to length because
// trimming itself would be variable-time.
func pickCandidate(r *http.Request) string {
    if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
        // The same Bearer header may carry a real JWT; we let the downstream
        // bearer middleware decide. We try Match here only because if it
        // matches the admin token, we shortcut.
        return strings.TrimPrefix(h, "Bearer ")
    }
    if c, err := r.Cookie("mkt_admin_token"); err == nil {
        return c.Value
    }
    return ""
}
```

The middleware is placed first in the chain. If the admin token is
*present and matches*, we short-circuit. If it's present and doesn't
match, we fall through; the bearer middleware will see the same
`Authorization` header and try to parse it as a JWT — which will
correctly fail, returning 401. This composition is intentional: it
means a *real* JWT is never accidentally treated as an admin-token
attempt and vice versa.

## 4. Sentinel user object

```go
// api/internal/auth/sentinel.go
var SentinelUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

func BuildSentinelUser() User {
    return User{
        ID:       SentinelUserID,
        Username: "admin",
        IsAdmin:  true,
        // CreatedAt/etc. left zero — the row exists in DB for audit FK
        // resolution; the in-memory copy used for ctx.User is synthesized.
    }
}
```

Critically, the middleware does **not** call `users.GetByID` for the
sentinel — the env-var path has no DB dependency (story AC-1: "no DB
lookup is performed for authentication"). The DB row exists only so
that audit-log inserts can satisfy the `actor_user_id REFERENCES
users(id)` FK.

## 5. Audit sampling

```go
// api/internal/audit/sink.go (additions)
type sampler struct {
    mu        sync.Mutex
    perIPDay  map[string]struct{}     // first-per-IP-per-day already logged
    bucket    *rate.Limiter           // 1 token/min, burst 1 (golang.org/x/time/rate)
}

func (s *sampler) ShouldLog(ip string, now time.Time) bool {
    key := ip + "|" + now.UTC().Format("2006-01-02")
    s.mu.Lock(); defer s.mu.Unlock()
    if _, seen := s.perIPDay[key]; !seen {
        s.perIPDay[key] = struct{}{}
        return true
    }
    return s.bucket.Allow()
}
```

The map grows by at most one entry per IP per day; a daily reaper
clears entries older than 48h. This is the story's "first use per IP
per day is always logged" + "sample at 1/min" requirement.

## 6. Test plan

### 6.1 Loader (`admintoken_test.go`)

| Test | What it pins |
|---|---|
| `TestLoaderDisabledOnEmptyEnv` | `MAKTABA_ADMIN_TOKEN=""` → Loader.Enabled() == false; Match always false. |
| `TestLoaderRejectsShortToken` | env value < 32 chars → NewLoader returns `admin-token-too-short`. |
| `TestLoaderAcceptsLongToken` | env value of 64 chars → Loader.Enabled() == true. |
| `TestMatchExact` | candidate == canonical → true. |
| `TestMatchOneCharDifferent` | last char different → false (no early exit timing leak — see TimingTest). |
| `TestMatchLengthDifferent` | candidate is canonical + "x" → false. |
| `TestMatchEmptyCandidate` | candidate == "" → false. |
| `TestMatchTimingLastByteVsFirstByte` | 1000 trials each; means within 5 % of each other. |

### 6.2 Middleware (`admintoken_test.go` in middleware pkg)

| Test | What it pins |
|---|---|
| `TestMiddlewarePassesThroughWhenDisabled` | env unset; request without auth → next handler runs with no user in ctx. |
| `TestMiddlewareInjectsSentinelOnHeaderMatch` | `Authorization: Bearer <admin-token>` → handler sees `auth.UserFromContext(ctx) == sentinel`. |
| `TestMiddlewareInjectsSentinelOnCookieMatch` | `Cookie: mkt_admin_token=<admin-token>` → same. |
| `TestMiddlewarePassesThroughOnMismatch` | Bearer with a real-looking JWT (not admin token) → middleware doesn't authenticate; the downstream bearer middleware does. |
| `TestMiddlewareEmitsAuditOncePerMinute` | 5 requests in 30s from one IP → 1 audit row (the first); after 70s → second row appears. |
| `TestMiddlewareEmitsAuditFirstPerIPPerDay` | First request from IP1 today → audit row (regardless of bucket); second request from IP1 today → governed by bucket. |
| `TestMiddlewareSentinelHasIsAdminTrue` | After auth, `auth.UserFromContext(ctx).IsAdmin == true`. |
| `TestMiddlewareEnvRotationEnforced` | Restart with a different env value; old-token request → falls through to bearer middleware → 401. (Actually two separate test runs.) |

### 6.3 Integration with mint path

| Test | What it pins |
|---|---|
| `TestAdminTokenMintsURLWithAllLibsInLib` | Stand up two libraries; admin-token request to `MintManifestURL` → token's `lib[]` contains both. |
| `TestAdminTokenAuditAttribution` | An audit row written under the admin-token path has `actor_user_id == SentinelUserID`. |
| `TestAdminTokenWeakRefusesBoot` | env set to 8 chars → API exits with code != 0 and message `admin-token-too-short`. |

## 7. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Both admin token AND user table populated | Both auth paths work. The admin-token middleware runs first; if it matches, the request is the sentinel. If it doesn't match (empty or wrong token), the bearer/session middlewares decide. | `TestMiddlewarePassesThroughOnMismatch` |
| A user named "admin" creates real account | The sentinel's username is `admin`; uniqueness is by `lower(username)`, so creating another `admin` returns 409 (Story 10.1). The sentinel row stays put. | Story 10.1 plan EC |
| Operator rotates env to a longer token | Token refused if < 32 chars. There is no grace period (story AC-4). Old token requests get 401 from the bearer middleware (which doesn't know about the old token either). | `TestMiddlewareEnvRotationEnforced` |
| A 1-char-different admin token | Constant-time compare → false → falls through. | `TestMatchOneCharDifferent` + `TestMatchTimingLastByteVsFirstByte` |
| Empty `MAKTABA_ADMIN_TOKEN` (explicitly empty) | Loader.Enabled() == false; the cookie/header is ignored entirely (no `subtle` compare against empty canonical that could short-circuit). | `TestLoaderDisabledOnEmptyEnv` |
| Cookie and header both present with different values | We try Authorization first (per `pickCandidate`); if it doesn't match, we DO NOT then try the cookie (avoids two compares per request). The cookie is consulted only when Authorization is absent. | `TestMiddlewareHeaderTakesPrecedenceOverCookie` |
| Sentinel admin's `lib[]` is huge in mints | Story 10.13's lib cap (1000) applies; the minter logs WARN if exceeded. The v2 plan adds a `lib_all` sentinel; v1 caps. | Story 10.13 plan |
| Audit for admin-token used on a busy box | Sampler at 1/min per IP keeps the table from filling; first-per-IP-per-day is always recorded so an investigator sees at least one entry per day. | `TestMiddlewareEmitsAuditOncePerMinute` |
| The candidate string contains NUL bytes | `subtle.ConstantTimeCompare` operates byte-wise; NULs are fine. The `Authorization` header parser already strips around the `Bearer ` prefix only — no other normalization. | `TestMatchAcceptsArbitraryBytes` |

## 8. Dependencies

| Dep | Version | Why |
|---|---|---|
| `crypto/subtle` | stdlib | Constant-time compare. |
| `golang.org/x/time/rate` | already | Sampler bucket. |

No new deps.

## 9. Acceptance checklist

**Loader**
- [ ] AC: `MAKTABA_ADMIN_TOKEN` < 32 chars → API refuses to start with `admin-token-too-short`.
- [ ] Empty env → middleware is a no-op.

**Match**
- [ ] AC-2: comparison uses `subtle.ConstantTimeCompare`; no length short-circuit; timing test pinned.

**Sentinel**
- [ ] AC-1: matched request has `auth.UserFromContext(ctx) == sentinel`; `IsAdmin=true`; no DB lookup performed for authentication.
- [ ] Audit rows under this path use `SentinelUserID` as `actor_user_id`.

**`lib[]` enrichment**
- [ ] AC-5: minter under admin-token path emits `lib[]` containing every library id.

**Audit sampling**
- [ ] First use per IP per day always logged; subsequent uses sampled at ≤ 1/min/IP.

**Tests**
- [ ] All §6 tests pass.

**Docs**
- [ ] README.md ticks story 10.9.
- [ ] Operations doc spells out env-only (no DB) storage of the admin token.
