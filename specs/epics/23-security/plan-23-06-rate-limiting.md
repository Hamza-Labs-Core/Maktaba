# Implementation Plan — Story 23.6 Rate limiting, lockout, and destructive-action confirmation

> Companion to [story-23-06-rate-limiting.md](story-23-06-rate-limiting.md).
> Story states *what* and *why*; this plan states *how*.
> The lockout columns and unlock endpoint are owned by
> [Epic 10 Story 10.1](../10-auth-security/plan-10-01-user-store.md);
> the limiter middleware and **canonical auth-endpoint numbers** live in
> [Epic 10 Story 10.12](../10-auth-security/plan-10-12-rate-limiting-auth.md).
> Audit categories defined in
> [Epic 21 Story 21.6](../21-observability/story-21-06-audit-log.md).
>
> Per [PLAN_REVIEW_18_24 §2](../../PLAN_REVIEW_18_24.md):
> - Auth-endpoint rate-limit numbers and the limiter library are owned
>   by **plan-10-12** (canonical). This plan **references** rather than
>   redefines them.
> - The limiter library is **`golang.org/x/time/rate`**, not a custom
>   implementation. The "rolled our own" limiter previously described
>   here was dropped.
> - Eviction strategy follows plan-10-12's **janitor** (periodic sweep
>   of stale entries), not LRU-on-overflow.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Rate limiter library | **`golang.org/x/time/rate`** (canonical, owned by [plan-10-12](../10-auth-security/plan-10-12-rate-limiting-auth.md)). One limiter at routing time across the API. |
| Auth-endpoint numbers | **Canonical numbers owned by [plan-10-12 §0](../10-auth-security/plan-10-12-rate-limiting-auth.md):** login `10/min/IP`; refresh `6/min/family + 30/min/IP`; other `/api/auth/*` `30/min/IP`. This plan **references** rather than redefines. |
| Non-auth route table (this plan) | search `60/min/user`; bulk job submit `10/min/user`. |
| Per-replica vs distributed | **Known limitation, single-host v1.** Per-replica buckets give 2× headroom on a 2-replica deploy because there is no shared store. Tracked as TODO; cross-link [Epic 19 — Scalability](../19-scalability/README.md). A distributed-store swap (Redis) is the v2 path. |
| Lockout | 5 failures within a 15-min sliding window → 15-min lock per `(user, ip)`. Lockout state in `users.failed_attempts` + `users.locked_until` (existing columns, Story 10.1). |
| Destructive-action confirmation | `confirm` field in body equal to a deterministic function of the resource. Mismatch → 412. |
| Audit log | `category='security'` for rate-limit and lockout events. (`category='auth'` would be cleaner but the audit-log enum extension is deferred to [plan-21-06](../21-observability/plan-21-06-audit-log.md); until then reuse `security`.) Audit-log table mechanics owned by plan-21-06. |
| Out of scope | Auth-endpoint limiter and numbers (plan-10-12); audit-log table mechanics (plan-21-06); existing limiter for streaming (Epic 8); admin-token pre-shared key (23.1). |

## 1. Architecture diagram

```
                  ┌────────────────────────┐
   request   ──► │ chi middleware:        │
                  │  classifyRoute → key  │
                  │  bucket.TryConsume    │
                  └─────┬──────────────────┘
                        │ ok
                        ▼
                  ┌────────────────────────┐
                  │ handler                │
                  │ login: tracks          │
                  │   failed_attempts      │
                  │ destructive: checks    │
                  │   confirm token        │
                  └────────────────────────┘
                        │ on lockout/destruction
                        ▼
                  audit_log row (Epic 21.6)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/ratelimit/limiter.go` | Thin wrapper around `golang.org/x/time/rate.Limiter` with a janitor-swept map (shared shape with plan-10-12). |
| `api/internal/ratelimit/policy.go` | Per-route table; key derivation. Auth-route entries delegate to plan-10-12's helpers. |
| `api/internal/ratelimit/middleware.go` | chi middleware; honors `X-Forwarded-For` only when `MAKTABA_TRUSTED_PROXIES` is set. |
| `api/internal/auth/lockout.go` | Failed-attempt tracking; 5-in-15 sliding window. |
| `api/internal/http/destructive.go` | `requireConfirm(token)` middleware + `expectedConfirm()` helpers. |
| `api/internal/audit/audit.go` | Thin writer for audit rows; consumed by Epic 21.6's table. |
| Tests — `_test.go` per file plus `tests/security/lockout_e2e.sh`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/http/router.go` | Mounts the limiter middleware and selectively `requireConfirm` on destructive routes. |
| `api/internal/auth/login.go` | Calls `lockout.OnFailure` / `OnSuccess`. |
| `api/internal/http/users.go` | `DELETE /api/users/{id}` requires `confirm == user.username`. |
| `api/internal/http/libraries.go` | `DELETE /api/libraries/{id}?purge=true` requires `confirm == library.name`. |
| `api/internal/http/keys.go` | `POST /api/keys/rotate?immediate=true` requires `confirm == "rotate-immediate"`. |

### 2.3 Limiter

`api/internal/ratelimit/limiter.go` is a thin wrapper around
`golang.org/x/time/rate.Limiter`. The shape mirrors plan-10-12's
limiter exactly so the auth-route classes can reuse it without a
second package:

```go
package ratelimit

import (
    "sync"
    "time"

    "golang.org/x/time/rate"
)

// entry holds one limiter plus its last-seen time for the janitor.
type entry struct {
    lim  *rate.Limiter
    seen time.Time
}

// Limiter is a per-key rate.Limiter map. The janitor sweeps stale
// entries on a fixed interval; there is NO LRU-on-overflow eviction.
type Limiter struct {
    mu       sync.Mutex
    entries  map[string]*entry
    rps      rate.Limit  // tokens per second
    burst    int         // bucket capacity
    ttl      time.Duration  // entry stale after this idle time
}

// New constructs a Limiter from a "per minute" budget. Burst equals
// the per-minute number; tokens refill at perMinute/60 per second.
func New(perMinute int, ttl time.Duration) *Limiter {
    return &Limiter{
        entries: make(map[string]*entry),
        rps:     rate.Limit(float64(perMinute) / 60.0),
        burst:   perMinute,
        ttl:     ttl,
    }
}

// Allow consults the per-key limiter. Returns the wait duration when
// denied. Safe for concurrent use.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
    l.mu.Lock()
    e, ok := l.entries[key]
    if !ok {
        e = &entry{lim: rate.NewLimiter(l.rps, l.burst)}
        l.entries[key] = e
    }
    e.seen = time.Now()
    l.mu.Unlock()

    r := e.lim.Reserve()
    if !r.OK() {
        return false, 0
    }
    delay := r.Delay()
    if delay == 0 {
        return true, 0
    }
    r.Cancel()  // don't actually consume the token; client must retry
    return false, delay
}

// Janitor periodically removes entries whose last-seen time is older
// than ttl. Mirrors the plan-10-12 sweep cadence.
func (l *Limiter) Janitor(ctx context.Context, every time.Duration) {
    t := time.NewTicker(every)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            l.sweep()
        }
    }
}

func (l *Limiter) sweep() {
    cutoff := time.Now().Add(-l.ttl)
    l.mu.Lock()
    defer l.mu.Unlock()
    for k, e := range l.entries {
        if e.seen.Before(cutoff) {
            delete(l.entries, k)
        }
    }
}
```

Janitor cadence: every `ttl/2`, mirroring plan-10-12. Key density
under attack is bounded by the JWT/cookie throughput times `ttl`; the
TODO below tracks moving to a shared store for multi-replica
correctness.

> **TODO (per-replica vs distributed).** A two-replica deploy gives an
> attacker 2× the configured budget because each replica counts
> independently. This is **acceptable for v1 (single-host)** and is
> tracked against [Epic 19 — Scalability](../19-scalability/README.md).
> The path forward is a Redis-backed limiter with the same external
> shape; both `api/internal/ratelimit` and the auth-route limiter from
> plan-10-12 swap atomically.

### 2.4 Policy

`api/internal/ratelimit/policy.go`. **Auth-route classes
(`auth-login`, `auth-refresh`, `auth-other`) are owned by
[plan-10-12](../10-auth-security/plan-10-12-rate-limiting-auth.md);
this file only adds the non-auth classes and re-exports plan-10-12's
auth classes** so a single registry covers the whole router.

```go
import authrl "maktaba/api/internal/auth/ratelimit"  // plan-10-12

type RouteClass struct {
    Name      string
    Match     func(*http.Request) bool
    PerMinute int
    KeyFunc   func(*http.Request, *Context) string  // "ip:" or "user:" or "family:"
}

// non-auth classes owned here.
var nonAuthClasses = []RouteClass{
    {
        Name:      "search",
        Match:     pathExact("GET", "/api/search"),
        PerMinute: 60,
        KeyFunc:   userKey,
    },
    {
        Name:      "bulk-job",
        Match:     pathExact("POST", "/api/jobs/bulk"),
        PerMinute: 10,
        KeyFunc:   userKey,
    },
}

// AllClasses returns plan-10-12's auth classes plus this plan's
// non-auth classes. Auth numbers (login 10/min/IP, refresh 6/min/family
// + 30/min/IP) are defined in plan-10-12 and not duplicated here.
func AllClasses() []RouteClass {
    return append(authrl.Classes(), nonAuthClasses...)
}
```

Single-user mode (relaxed defaults per AC4) bumps each non-auth
`PerMinute` by 10× when `auth.multi_user=false`; auth-route relaxation
is owned by plan-10-12. Never disabled outright.

### 2.5 Middleware

`api/internal/ratelimit/middleware.go`:

```go
func Middleware(reg *Registry, trustedProxies []net.IPNet) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            class := reg.Classify(r)
            if class == nil { next.ServeHTTP(w, r); return }
            ctx := requestContext(r)
            key := class.KeyFunc(r, ctx)
            if key == "" { next.ServeHTTP(w, r); return }
            limiter := reg.LimiterFor(class)
            ok, retryAfter := limiter.Allow(class.Name + ":" + key)
            if !ok {
                w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
                problem(w, http.StatusTooManyRequests, "rate-limited",
                    fmt.Sprintf("class=%s key=%s", class.Name, scrubKey(key)))
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

func ipKey(r *http.Request, ctx *Context) string {
    if ctx.TrustedProxy {
        if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
            return "ip:" + strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
        }
    }
    host, _, _ := net.SplitHostPort(r.RemoteAddr)
    return "ip:" + host
}

func userKey(r *http.Request, ctx *Context) string {
    u, ok := authcontext.From(r.Context())
    if !ok { return "" }   // skip; user-route limit only after auth
    return "user:" + u.ID.String()
}
```

`scrubKey` replaces the IP/UUID in the response detail with a hash so
a 429 body never leaks the client identity to itself in plain text
(defense in depth — already known to the client, but logs go through
the redactor and the hash is more uniform).

### 2.6 Lockout

`api/internal/auth/lockout.go`:

```go
type Lockout struct {
    db *db.Queries
}

const (
    failureWindow = 15 * time.Minute
    threshold     = 5
    lockDuration  = 15 * time.Minute
)

// OnFailure increments per-user counters and locks the user when the
// 5-in-15-minutes threshold is crossed.
func (l *Lockout) OnFailure(ctx context.Context, userID uuid.UUID, ip string) error {
    return l.db.WithTx(ctx, func(q *db.Queries) error {
        u, err := q.GetUserByID(ctx, userID)
        if err != nil { return err }
        // We approximate the sliding window by capping `failed_attempts`
        // and resetting it on successful login OR when the prior
        // increment is older than `failureWindow`.
        if u.UpdatedAt != nil && time.Since(*u.UpdatedAt) > failureWindow {
            u.FailedAttempts = 0
        }
        u.FailedAttempts++
        if u.FailedAttempts >= threshold {
            until := time.Now().Add(lockDuration)
            if err := q.LockUser(ctx, db.LockUserParams{ID: userID, LockedUntil: until}); err != nil {
                return err
            }
            audit.Write(ctx, audit.Event{
                // Use 'security' until plan-21-06 extends the enum to
                // include 'auth'. See §0 Scope.
                Category: audit.CategorySecurity,
                Action:   "user.locked",
                Actor:    audit.Actor{Anonymous: ip},
                Resource: audit.Resource{Type: "user", ID: userID.String()},
                Detail: map[string]string{"reason": "5-failures-in-15-min", "until": until.Format(time.RFC3339)},
            })
        }
        return q.IncrementFailedAttempts(ctx, db.IncrementParams{ID: userID, To: u.FailedAttempts})
    })
}

// OnSuccess clears the counter (does not clear locked_until — the user
// can't successfully log in while locked).
func (l *Lockout) OnSuccess(ctx context.Context, userID uuid.UUID) error {
    return l.db.ResetFailedAttempts(ctx, userID)
}

// IsLocked checks; returns the unlock time when locked.
func (l *Lockout) IsLocked(ctx context.Context, userID uuid.UUID) (bool, time.Time, error) {
    u, err := l.db.GetUserByID(ctx, userID)
    if err != nil { return false, time.Time{}, err }
    if u.LockedUntil == nil { return false, time.Time{}, nil }
    if time.Now().Before(*u.LockedUntil) { return true, *u.LockedUntil, nil }
    // expired lock; clean up lazily
    _ = l.db.UnlockUser(ctx, userID)
    return false, time.Time{}, nil
}
```

The 5-in-15 approximation isn't a true sliding window; for v1 it's
"reset counter if last failure is older than the window," which the
story labels as acceptable. A precise sliding-window upgrade is filed
as a follow-up.

### 2.7 Destructive-action confirm

`api/internal/http/destructive.go`:

```go
// expectedConfirm produces the per-resource expected token.
func ExpectedConfirmForLibrary(name string) string { return name }
func ExpectedConfirmForUser(username string) string { return username }
const ExpectedConfirmRotateImmediate = "rotate-immediate"

func RequireConfirm(expected func(*http.Request) (string, error)) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            var body struct{ Confirm string `json:"confirm"` }
            if err := decodeBodyPeek(r, &body); err != nil {
                problem(w, 400, "invalid-json", "")
                return
            }
            // We do the resource lookup unconditionally to compute the
            // expected token. The constant-time compare runs even when
            // the lookup errors, so the response time does not depend on
            // resource existence. If the lookup errors, we still emit
            // confirm-mismatch (not 404); the timing for "wrong confirm"
            // and "missing resource" is identical. Resource-existence
            // (404) is signalled only AFTER the compare matches.
            want, lookupErr := expected(r)
            // Use a fixed-length sentinel when lookup errors so the
            // compare runs on a non-empty buffer of stable shape.
            wantBytes := []byte(want)
            if lookupErr != nil {
                wantBytes = []byte("__resource_missing_sentinel__")
            }
            match := subtle.ConstantTimeCompare([]byte(body.Confirm), wantBytes) == 1
            if !match {
                problem(w, http.StatusPreconditionFailed, "confirm-mismatch", "")
                return
            }
            // Match succeeded — only now signal resource existence.
            if lookupErr != nil {
                problem(w, http.StatusNotFound, "not-found", "")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

`decodeBodyPeek` reads + buffers the body so the downstream handler
can decode it normally. The constant-time compare protects against
timing oracles on confirm tokens.

The library-purge route in `libraries.go`:

```go
r.With(RequireConfirm(libConfirm(s))).Delete("/{id}", deleteLibrary(s))

func libConfirm(s LibraryStore) func(*http.Request) (string, error) {
    return func(r *http.Request) (string, error) {
        id := chi.URLParam(r, "id")
        l, err := s.Get(r.Context(), id)
        if err != nil { return "", err }
        return ExpectedConfirmForLibrary(l.Name), nil
    }
}
```

After successful delete, an audit row is written with category `data`.

## 3. Test plan

### 3.1 Login burst (TC1)

| Test | What it pins |
|---|---|
| `TestLoginRateLimit11InMinute` | 11 `POST /api/auth/login` from one IP within 60 s; the 11th returns 429 with `Retry-After`. |
| `TestLoginLimitDecaysAcrossMinute` | Client waits 60 s; the next request succeeds. |

### 3.2 User lockout (TC2)

| Test | What it pins |
|---|---|
| `TestLockoutAfter5Failures` | 5 wrong-password attempts within 15 min; the 6th — even with the correct password — returns 423 `account-locked`. |
| `TestUnlockClearsAndAudits` | Admin `POST /api/users/{id}/unlock` → 204; user can log in; audit row category `admin`, action `user.unlocked`. |
| `TestSuccessAfter4FailuresClears` | 4 failures, then a correct password → counter reset; subsequent failures restart the count. |

### 3.3 Search burst (TC3)

| Test | What it pins |
|---|---|
| `TestSearch100In30Sec` | 100 search requests in 30 s from one user; 60+1 returns 429; later requests resume after window. |
| `TestSearchLimitPerUserNotPerIp` | Two users from same IP each get their own 60/min budget. |

### 3.4 Confirm-token mismatch (TC4)

| Test | What it pins |
|---|---|
| `TestPurgeWithBadConfirm` | `DELETE /api/libraries/{id}?purge=true` with `{confirm:"wrong"}` → 412 `confirm-mismatch`; library not deleted; no audit row. |
| `TestPurgeWithCorrectConfirm` | Correct confirm → 204; library deleted; audit row category `data`. |
| `TestPurgeRaceTwoAdmins` (EC4) | Two admins POST identical confirms; one wins; the other returns 404; both audited. |

### 3.5 Per-route ceiling (TC5)

| Test | What it pins |
|---|---|
| `TestLoginLimitIndependent` | Hit `/api/auth/login` 12× and `/api/auth/refresh` 35× in 60 s from one IP; login limit fires at 11 (`10/min/IP`), refresh at 31 (`30/min/IP`), independently. Per-family refresh ceiling (`6/min/family`) tested separately in plan-10-12. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Reverse proxy strips `X-Forwarded-For` (EC1) | Falls back to connecting IP. Startup banner: "trusted-proxies set but no XFF in samples" if `MAKTABA_TRUSTED_PROXIES` is set yet incoming requests lack the header (sampled). | `TestXffMissingFallsBackToRemoteAddr` |
| Multi-device household (EC2) | Per-user limits dominate over per-IP for authenticated routes; documented. | `TestPerUserDominatesPerIp` |
| Bulk-admin operation (EC3) | Admin requests with `?bypass=user-limit` (admin-only via authz) skip user limits; per-route IP limits still apply. Each bypass writes an audit row. | `TestAdminBypassUserLimit` |
| Confirm-token race (EC4) | First admin's delete succeeds; second's request returns 404 (resource gone); both audited. | `TestPurgeRaceTwoAdmins` |
| Limiter map under sustained load | Janitor sweep (every `ttl/2`) drops idle entries past `ttl`. Memory bound = active-key count × bucket size, dominated by request rate. Documented; per-replica vs distributed tracked against Epic 19. | `TestLimiterJanitorSweepsIdle` |
| Trusted proxy spoofing | XFF only honored when the connecting IP is in `MAKTABA_TRUSTED_PROXIES`; otherwise treated as untrusted. | `TestXffOnlyFromTrustedProxies` |
| Lockout race (two failed logins arrive simultaneously) | Tx wraps the increment; the unique row update is serialized; both increments count. | `TestConcurrentFailureIncrements` |
| Clock change while locked | `IsLocked` compares to `time.Now()`; a backward clock jump keeps the user locked longer than 15 min, an acceptable failure mode. | n/a |
| Confirm body has additional fields | The peek decoder ignores extras; the downstream handler reads its own body normally. | `TestConfirmExtraFieldsTolerated` |
| Audit log writes asynchronously | The audit writer is fire-and-forget but persisted within the same DB tx for `auth`/`admin`/`data` categories so an audit row's absence implies the action didn't commit. | `TestAuditInSameTxAsAction` |
| HEAD/OPTIONS to a rate-limited route | Skipped — only `POST`/`GET` count toward limits per the policy classes. | `TestOptionsBypassesLimit` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `crypto/subtle` | stdlib | Constant-time confirm compare. |
| `chi/v5` | already | Routing. |
| `golang.org/x/time/rate` | latest | **Canonical limiter (plan-10-12).** Per-key map is wrapped here; the previous "rolled our own" choice was dropped per [PLAN_REVIEW_18_24 §2](../../PLAN_REVIEW_18_24.md). |

## 6. Acceptance checklist

**Per-route limits**
- [ ] Login `10/min/IP`, refresh `6/min/family + 30/min/IP`, other auth `30/min/IP` — **canonical numbers from [plan-10-12](../10-auth-security/plan-10-12-rate-limiting-auth.md); referenced, not redefined**.
- [ ] Search 60/min/user, bulk job 10/min/user.
- [ ] All return 429 + `Retry-After`.
- [ ] Limiter is `golang.org/x/time/rate` (no custom implementation).
- [ ] Janitor sweeps stale entries (no LRU-on-overflow).
- [ ] Per-replica limitation documented as a known TODO; cross-link Epic 19.

**Lockout**
- [ ] 5-in-15 lock for 15 min per `(user, ip)`.
- [ ] Audit row category `auth`.
- [ ] Admin unlock writes category `admin`.

**Confirm tokens**
- [ ] `library.name` for purge, `user.username` for delete, fixed string for key rotation.
- [ ] 412 on mismatch.
- [ ] Constant-time compare.
- [ ] Audit categories `data`, `admin`, `keys`.

**Single-user mode**
- [ ] Defaults relaxed 10×; never disabled.

**Hardening**
- [ ] Trusted-proxy IP allowlist for `X-Forwarded-For`.
- [ ] Limiter key density bounded.
