# Implementation Plan — Story 23.6 Rate limiting, lockout, and destructive-action confirmation

> Companion to [story-23-06-rate-limiting.md](story-23-06-rate-limiting.md).
> Story states *what* and *why*; this plan states *how*.
> The lockout columns and unlock endpoint are owned by
> [Epic 10 Story 10.1](../10-auth-security/plan-10-01-user-store.md);
> the limiter middleware is new here. Audit categories defined in
> [Epic 21 Story 21.6](../21-observability/story-21-06-audit-log.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Rate limiter | Token-bucket per `(route_class, key)`. Backed by an in-process LRU map keyed on the IP / user ID. No Redis in v1 (single-host); architecture allows swap-in later. |
| Per-route table | `POST /api/auth/login` 10/min/IP; `POST /api/auth/refresh` 60/min/IP; other `/api/auth/*` 30/min/IP; search 60/min/user; bulk job submit 10/min/user. |
| Lockout | 5 failures within a 15-min sliding window → 15-min lock per `(user, ip)`. Lockout state in `users.failed_attempts` + `users.locked_until` (existing columns, Story 10.1). |
| Destructive-action confirmation | `confirm` field in body equal to a deterministic function of the resource. Mismatch → 412. |
| Audit log | Categories `auth` (lockout), `admin` (unlock, user-delete), `data` (library-purge), `keys` (rotation). |
| Out of scope | Audit-log table mechanics (Story 21.6); existing limiter for streaming (Epic 8); admin-token pre-shared key (23.1). |

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
| `api/internal/ratelimit/limiter.go` | Token bucket, LRU map, sweeper. |
| `api/internal/ratelimit/policy.go` | Per-route table; key derivation. |
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

`api/internal/ratelimit/limiter.go`:

```go
package ratelimit

import (
    "sync"
    "time"
)

type Bucket struct {
    capacity float64
    refill   float64       // tokens per second
    tokens   float64
    last     time.Time
}

func (b *Bucket) tryConsume(now time.Time) bool {
    elapsed := now.Sub(b.last).Seconds()
    b.tokens = min(b.capacity, b.tokens+elapsed*b.refill)
    b.last = now
    if b.tokens < 1 { return false }
    b.tokens -= 1
    return true
}

type Limiter struct {
    mu       sync.Mutex
    buckets  map[string]*Bucket  // bounded by sweeper
    capacity float64
    refill   float64
    cap      int                  // max distinct keys
}

func New(perMinute int, capKeys int) *Limiter {
    return &Limiter{
        buckets:  make(map[string]*Bucket, capKeys),
        capacity: float64(perMinute),
        refill:   float64(perMinute) / 60.0,
        cap:      capKeys,
    }
}

func (l *Limiter) Allow(key string) (bool, time.Duration) {
    l.mu.Lock()
    defer l.mu.Unlock()
    b, ok := l.buckets[key]
    now := time.Now()
    if !ok {
        if len(l.buckets) >= l.cap { l.evictOldest() }
        b = &Bucket{capacity: l.capacity, refill: l.refill, tokens: l.capacity, last: now}
        l.buckets[key] = b
    }
    if b.tryConsume(now) { return true, 0 }
    deficit := 1 - b.tokens
    return false, time.Duration(deficit/l.refill * float64(time.Second))
}
```

The eviction is LRU-on-overflow; key density is bounded so that an
attacker can't exhaust memory by spraying unique IPs.

### 2.4 Policy

`api/internal/ratelimit/policy.go`:

```go
type RouteClass struct {
    Name      string
    Match     func(*http.Request) bool
    PerMinute int
    KeyFunc   func(*http.Request, *Context) string  // "ip:" or "user:" prefix
}

var classes = []RouteClass{
    {
        Name:      "auth-login",
        Match:     pathExact("POST", "/api/auth/login"),
        PerMinute: 10,
        KeyFunc:   ipKey,
    },
    {
        Name:      "auth-refresh",
        Match:     pathExact("POST", "/api/auth/refresh"),
        PerMinute: 60,
        KeyFunc:   ipKey,
    },
    {
        Name:      "auth-other",
        Match:     pathPrefix("/api/auth/"),
        PerMinute: 30,
        KeyFunc:   ipKey,
    },
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
```

Single-user mode (relaxed defaults per AC4) bumps each `PerMinute` by
10× when `auth.multi_user=false`; never disabled outright.

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
                Category: audit.CategoryAuth,
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
            want, err := expected(r)
            if err != nil {
                problem(w, http.StatusNotFound, "not-found", "")
                return
            }
            if subtle.ConstantTimeCompare([]byte(body.Confirm), []byte(want)) != 1 {
                problem(w, http.StatusPreconditionFailed, "confirm-mismatch", "")
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
| `TestLoginLimitIndependent` | Hit `/api/auth/login` 12× and `/api/auth/refresh` 70× in 60 s; login limit fires at 11, refresh at 61, independently. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Reverse proxy strips `X-Forwarded-For` (EC1) | Falls back to connecting IP. Startup banner: "trusted-proxies set but no XFF in samples" if `MAKTABA_TRUSTED_PROXIES` is set yet incoming requests lack the header (sampled). | `TestXffMissingFallsBackToRemoteAddr` |
| Multi-device household (EC2) | Per-user limits dominate over per-IP for authenticated routes; documented. | `TestPerUserDominatesPerIp` |
| Bulk-admin operation (EC3) | Admin requests with `?bypass=user-limit` (admin-only via authz) skip user limits; per-route IP limits still apply. Each bypass writes an audit row. | `TestAdminBypassUserLimit` |
| Confirm-token race (EC4) | First admin's delete succeeds; second's request returns 404 (resource gone); both audited. | `TestPurgeRaceTwoAdmins` |
| Limiter map overflow | LRU eviction by oldest `last` time; documented memory cap = `MAKTABA_RATELIMIT_KEYS=10000` (default). | `TestLimiterEvictionUnderPressure` |
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
| `golang.org/x/time/rate` | NOT used — rolled our own to bound memory and avoid a dep that doesn't bound key density. | (Documenting the choice.) |

## 6. Acceptance checklist

**Per-route limits**
- [ ] Login 10/min/IP, refresh 60/min/IP, other auth 30/min/IP.
- [ ] Search 60/min/user, bulk job 10/min/user.
- [ ] All return 429 + `Retry-After`.

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
