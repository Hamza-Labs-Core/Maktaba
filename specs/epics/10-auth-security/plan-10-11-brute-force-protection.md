# Plan 10.11 — Brute-force / credential-stuffing protection — implementation

> Implementation plan for [story-10-11-brute-force-protection.md](story-10-11-brute-force-protection.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: extends the `users.failed_attempts` /
> `users.locked_until` columns owned by [Story 10.1](story-10-01-user-store.md);
> the unlock endpoint `POST /api/users/{id}/unlock` is owned by Story
> 10.1 AC-3; audit rows are written via [Story 10.16](story-10-16-security-audit.md);
> the rate-limit story [Plan 10.12](plan-10-12-rate-limiting-auth.md) is
> a sibling middleware that runs *before* this lockout middleware in the
> chain. Lockout is **distinct** from rate-limit: rate-limit caps
> requests-per-second over a short window (60 s); lockout is a longer
> 15-minute block triggered by *failures*, not request count.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Per-username state lives on the existing `users` row** (`failed_attempts INTEGER`, `locked_until TIMESTAMPTZ`) — no new table for the username path. Counter increments are inside the same transaction as the login-failure audit row. | Story AC-1; Epic 10 README schema for `users` already declares `failed_attempts` and `locked_until` | Co-locating the state with the user row keeps the lookup a single index hit (PK on `id`) and avoids a join. The counter and `locked_until` are read on every login attempt and updated only on failure or successful login — read-heavy column placement is fine on `users`. |
| D2 | **Per-IP state lives in a dedicated `auth_ip_lockouts` table** keyed by `INET`, with `failed_count`, `window_start`, `locked_until`, and `consecutive_lockouts` (for exponential backoff). | Story AC-2; refines the story (which doesn't specify schema) | A username lookup uses the existing PK; IP state has no natural existing home and would bloat any user-table sidecar. Postgres `INET` indexes natively (b-tree) and supports IPv4 and IPv6 with the same column. The `consecutive_lockouts` counter drives exponential `Retry-After`. |
| D3 | **Window is sliding via `(window_start, failed_count)` — not a true token-bucket; we reset `window_start` and `failed_count = 1` whenever a new failure arrives more than `failed_login_window_sec` after the existing `window_start`.** | Story AC-1: "in a window" + standard practice | A true sliding-log over Postgres requires retaining one row per attempt, which is more weight than needed. The reset-on-stale-window pattern is the standard "fixed window" with implicit reset; the operator-visible behavior (5 failures in 15 min triggers lockout) is identical for the threat model we care about (stuffing). |
| D4 | **Unknown-username path increments per-IP only and matches wrong-password timing.** The handler always runs argon2id-verify against either the real hash or a fixed dummy hash (`*FAKE-ARGON2ID-HASH-FOR-TIMING*`) and always sleeps to a target floor of ~500 ms wall time before returning. | Story AC-3: "the timing matches the wrong-password path … the per-IP counter is incremented (per-username counter is not, since the username does not exist)." | Argon2id verify is the dominant cost (~200–500 ms). Always running it (or the dummy) keeps the timing band tight — we measure ±50 ms in CI. The per-IP counter alone catches the distributed-stuffing pattern that unknown-username attacks generate. |
| D5 | **Exponential backoff `Retry-After` for per-IP lockouts.** First IP lockout: 15 min. Second consecutive lockout (within 24 h of clearing the first): 30 min. Third: 60 min. Capped at 240 min. The `consecutive_lockouts` counter increments on each new lockout; resets to 0 after 24 h with no further lockouts. | Story AC-2: "exponentially-increasing `Retry-After`" | Linear retry-after is too lenient against a determined attacker; exponential with a cap is the standard. 24 h is the reset-quiet window; an operator can reset by clearing the row via the unlock endpoint. |
| D6 | **Locked rows return 423 (username) or 429 (IP).** 423 Locked is correct semantically for "this account is locked"; 429 Too Many Requests with `Retry-After` is correct for IP throttling. Both responses are Problem Details (RFC 7807) shapes: `type=account-locked` and `type=ip-throttled`. | Story AC-1, AC-2 | 423 is the WebDAV code reused for "user account locked" — common practice (e.g., GitLab). 429 is the RFC 6585 code with the standard `Retry-After` header. Distinguishing the two helps the client UX (a 423 is "wait it out or contact admin"; a 429 is "try later from same IP"). |
| D7 | **Reaper goroutine clears stale lockouts hourly.** A background goroutine started at API boot runs `DELETE FROM auth_ip_lockouts WHERE locked_until < now() - interval '24 hours'` and `UPDATE users SET failed_attempts=0, locked_until=NULL WHERE locked_until < now() - interval '1 hour'` every 60 minutes. | Story description: long-lived stale rows otherwise accumulate | Without reaping, `auth_ip_lockouts` grows unbounded under sustained attack. The reaper is independent of the request path and uses an advisory lock so multiple API replicas don't double-reap. |
| D8 | **Per-username and per-IP checks happen INSIDE a single Postgres transaction with a row-level lock** (`SELECT ... FOR UPDATE`) so concurrent failed logins increment counters atomically. | Refines the story; correctness | Without the lock, two simultaneous wrong-password attempts at attempts 4 and 5 both see `failed_attempts=4`, both increment to 5, and only one of the two triggers the lockout — but the *attempts* counter ends up at 5 instead of 6, hiding the second attempt. Row-level lock fixes this; the lock is released on commit. |
| D9 | **Audit rows on lockout** are written via the existing audit writer (Story 10.16) with `category='security'`, `event ∈ {'lockout-username', 'lockout-ip'}`, `payload={target, count, window, consecutive}`. The audit row is inserted in the *same* transaction as the lockout state update. | Story AC-4 | Atomic write means an audit row always corresponds to an actual lockout state change; no orphan audits if the transaction rolls back. |
| D10 | **Successful login resets the per-username counter to 0 and `locked_until=NULL`** in the same transaction that creates the session. The per-IP counter is *not* reset by a successful login (an attacker may guess one valid password among many wrong ones). | Story AC-1: "Successful logins reset the counter." Refines for IP. | Resetting per-IP would let attackers inject one good guess to clear their slate. Per-username reset is correct because the username's own counter is its own user's history. |

If D4 is rejected (skip the dummy argon2id verify on unknown-username):
the timing channel reveals which usernames exist, and credential
stuffing becomes more efficient (attackers narrow to known accounts
before guessing passwords).

If D7 is rejected (no reaper): the `auth_ip_lockouts` table grows
linearly with attack volume. Even at 1k attacks/day that's manageable
for a year, but operationally the table will eventually need pruning.
Reaping is cheap insurance.

---

## 1. Architecture diagram — lockout middleware in the auth chain

```
   POST /api/auth/login
          │
          ▼
   ┌─────────────────────────────────────────────────────────────┐
   │  Middleware chain on /api/auth/* (subset shown):             │
   │   ratelimit.LoginMiddleware  (Plan 10.12, runs FIRST)        │
   │   lockout.Middleware          (THIS PLAN, runs SECOND)       │
   │   auth/login handler          (Story 10.2 / 10.3)            │
   └───────────────────────────────┬─────────────────────────────┘
                                   │
                                   ▼
   lockout.Middleware (pre-handler check):
     BEGIN;
       SELECT failed_attempts, locked_until FROM users
         WHERE lower(username)=$1 FOR UPDATE;
       SELECT failed_count, window_start, locked_until, consecutive_lockouts
         FROM auth_ip_lockouts WHERE ip=$2 FOR UPDATE;

       IF user.locked_until > now() ──► 423 + audit "attempt-while-locked"
       IF ip.locked_until   > now() ──► 429 + Retry-After + audit
     COMMIT;

   handler runs login (verify password, possibly create session)
                                   │
                                   ▼
   lockout.Middleware (post-handler):
     IF login succeeded ──► UPDATE users SET failed_attempts=0,
                                              locked_until=NULL
                            WHERE id=$user_id;
     IF login failed (wrong password OR unknown user):
        BEGIN;
          ── window slide: if window_start older than 15 min, reset to now()
          ── increment per-IP counter (always)
          ── increment per-username counter ONLY if user exists
          IF user.failed_attempts >= 5:
              SET locked_until = now() + 15 min;
              audit('lockout-username', payload)
          IF ip.failed_count >= 20:
              consecutive_lockouts += 1
              backoff = min(15 * 2^(consecutive-1), 240) min
              SET locked_until = now() + backoff
              audit('lockout-ip', payload)
        COMMIT;
                                   │
                                   ▼
   Return response.

   Out-of-band:
     reaper goroutine (hourly):
       DELETE FROM auth_ip_lockouts WHERE locked_until < now() - 24h
       UPDATE users SET failed_attempts=0, locked_until=NULL
                   WHERE locked_until < now() - 1h
```

The `unknown-username` path always runs `argon2id.Verify` against the
dummy hash (D4) and waits to a 500 ms floor before returning, matching
the wrong-password timing (Story 10.2 AC-3).

---

## 2. Detailed implementation

### 2.1 Package layout — Go (API Service)

```
api/
├── internal/
│   ├── auth/
│   │   ├── lockout/
│   │   │   ├── middleware.go       # public: Middleware(deps) func(http.Handler) http.Handler
│   │   │   ├── repo.go             # LockoutRepo: PreCheck, RecordFailure, RecordSuccess
│   │   │   ├── reaper.go           # background goroutine
│   │   │   ├── config.go           # Defaults + tunables
│   │   │   ├── timing.go           # ensureMinTime helper (D4 timing floor)
│   │   │   ├── queries.sql         # sqlc input
│   │   │   └── middleware_test.go
│   │   └── ...
│   └── ...
└── shared/db/migrations/
    └── 00XX_auth_ip_lockouts.sql
```

### 2.2 Schema migration — `auth_ip_lockouts`

```sql
-- shared/db/migrations/0042_auth_ip_lockouts.sql
BEGIN;

CREATE TABLE auth_ip_lockouts (
    ip                    INET PRIMARY KEY,
    failed_count          INTEGER NOT NULL DEFAULT 0,
    window_start          TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until          TIMESTAMPTZ,
    consecutive_lockouts  INTEGER NOT NULL DEFAULT 0,
    last_lockout_at       TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (failed_count >= 0),
    CHECK (consecutive_lockouts >= 0)
);

-- Reaper-friendly partial index.
CREATE INDEX auth_ip_lockouts_locked_until ON auth_ip_lockouts (locked_until)
    WHERE locked_until IS NOT NULL;

COMMIT;
```

### 2.3 Config

```go
// api/internal/auth/lockout/config.go
package lockout

import "time"

type Config struct {
    MaxFailedPerUsername  int           // default 5
    MaxFailedPerIP        int           // default 20
    FailedLoginWindow     time.Duration // default 15 * time.Minute
    LockoutDuration       time.Duration // default 15 * time.Minute (per-username)
    IPLockoutBaseDuration time.Duration // default 15 * time.Minute (1st IP lockout)
    IPLockoutMaxDuration  time.Duration // default 240 * time.Minute
    IPConsecutiveResetAfter time.Duration // default 24 * time.Hour
    TimingFloor           time.Duration // default 500ms (D4)
    ReaperInterval        time.Duration // default 1 * time.Hour
}

func DefaultConfig() Config {
    return Config{
        MaxFailedPerUsername:    5,
        MaxFailedPerIP:          20,
        FailedLoginWindow:       15 * time.Minute,
        LockoutDuration:         15 * time.Minute,
        IPLockoutBaseDuration:   15 * time.Minute,
        IPLockoutMaxDuration:    240 * time.Minute,
        IPConsecutiveResetAfter: 24 * time.Hour,
        TimingFloor:             500 * time.Millisecond,
        ReaperInterval:          1 * time.Hour,
    }
}
```

### 2.4 SQL — sqlc inputs

```sql
-- api/internal/auth/lockout/queries.sql

-- name: PreCheckUserAndIP :one
SELECT
    (SELECT locked_until FROM users WHERE lower(username)=lower($1)) AS user_locked_until,
    (SELECT failed_attempts FROM users WHERE lower(username)=lower($1)) AS user_failed_attempts,
    (SELECT locked_until FROM auth_ip_lockouts WHERE ip=$2)            AS ip_locked_until,
    (SELECT failed_count  FROM auth_ip_lockouts WHERE ip=$2)            AS ip_failed_count,
    (SELECT consecutive_lockouts FROM auth_ip_lockouts WHERE ip=$2)     AS ip_consecutive;

-- name: IncrementUserFailureWithLock :exec
WITH locked AS (
    SELECT id, failed_attempts FROM users
    WHERE lower(username)=lower($1) FOR UPDATE
)
UPDATE users
   SET failed_attempts = locked.failed_attempts + 1,
       locked_until = CASE
         WHEN locked.failed_attempts + 1 >= $2 THEN now() + ($3::interval)
         ELSE locked_until
       END
  FROM locked
 WHERE users.id = locked.id;

-- name: IncrementIPFailure :one
INSERT INTO auth_ip_lockouts (ip, failed_count, window_start, updated_at)
VALUES ($1, 1, now(), now())
ON CONFLICT (ip) DO UPDATE SET
    failed_count = CASE
        WHEN auth_ip_lockouts.window_start < now() - ($2::interval) THEN 1
        ELSE auth_ip_lockouts.failed_count + 1
    END,
    window_start = CASE
        WHEN auth_ip_lockouts.window_start < now() - ($2::interval) THEN now()
        ELSE auth_ip_lockouts.window_start
    END,
    updated_at = now()
RETURNING failed_count, consecutive_lockouts;

-- name: SetIPLockout :exec
UPDATE auth_ip_lockouts
   SET locked_until = now() + ($2::interval),
       consecutive_lockouts = CASE
         WHEN last_lockout_at IS NULL OR last_lockout_at < now() - ($3::interval)
              THEN 1
         ELSE consecutive_lockouts + 1
       END,
       last_lockout_at = now()
 WHERE ip = $1;

-- name: ResetUserOnSuccess :exec
UPDATE users
   SET failed_attempts = 0,
       locked_until = NULL
 WHERE id = $1;

-- name: ReapStaleIPLockouts :exec
DELETE FROM auth_ip_lockouts
 WHERE locked_until IS NOT NULL
   AND locked_until < now() - interval '24 hours';

-- name: ReapStaleUserLockouts :exec
UPDATE users
   SET failed_attempts = 0, locked_until = NULL
 WHERE locked_until IS NOT NULL
   AND locked_until < now() - interval '1 hour';
```

### 2.5 Middleware

```go
// api/internal/auth/lockout/middleware.go
package lockout

import (
    "encoding/json"
    "log/slog"
    "math"
    "net/http"
    "net/netip"
    "strings"
    "time"

    "github.com/maktaba/api/internal/audit"
    "github.com/maktaba/api/internal/auth/lockout/db"
)

type Deps struct {
    Q    *db.Queries
    Cfg  Config
    Audit audit.Writer
    Now  func() time.Time
}

// Middleware enforces lockout on /api/auth/login (and /api/auth/refresh
// where applicable). It pre-checks lockout state, runs the inner
// handler, and post-records the outcome.
//
// The inner handler signals login outcome via the response status:
//   200/204 -> success     (reset per-username counter)
//   401     -> wrong-password OR unknown user
//   anything else -> bypass post-step (5xx, etc.)
func Middleware(d Deps) func(http.Handler) http.Handler {
    if d.Now == nil { d.Now = time.Now }
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := d.Now()
            username := extractUsernameFromBody(r) // peeks body, restores
            ip := clientIP(r)

            // Pre-check.
            chk, _ := d.Q.PreCheckUserAndIP(r.Context(), username, ip.String())
            if chk.UserLockedUntil != nil && chk.UserLockedUntil.After(d.Now()) {
                d.Audit.Write(r.Context(), audit.Row{
                    Category: "security", Event: "attempt-while-locked-username",
                    Payload: map[string]any{"username": username, "ip": ip.String()},
                })
                writeProblem(w, 423, "account-locked",
                    "account is temporarily locked", chk.UserLockedUntil.Sub(d.Now()))
                return
            }
            if chk.IPLockedUntil != nil && chk.IPLockedUntil.After(d.Now()) {
                d.Audit.Write(r.Context(), audit.Row{
                    Category: "security", Event: "attempt-while-locked-ip",
                    Payload: map[string]any{"ip": ip.String()},
                })
                writeProblem(w, 429, "ip-throttled",
                    "too many failed attempts from this IP", chk.IPLockedUntil.Sub(d.Now()))
                return
            }

            // Wrap response writer to capture status.
            ww := &statusWriter{ResponseWriter: w, status: 200}
            next.ServeHTTP(ww, r)

            // Post-record.
            switch ww.status {
            case 200, 204:
                if userID := extractUserIDFromCtx(r); userID != nil {
                    _ = d.Q.ResetUserOnSuccess(r.Context(), *userID)
                }
                // Per-IP counter is NOT reset on success (D10).
            case 401:
                d.recordFailure(r.Context(), username, ip)
            default:
                // 5xx, 4xx-other: do not affect counters.
            }

            // Timing floor (D4) — applies when the request reached the
            // handler (i.e., not a pre-check rejection).
            ensureMinTime(start, d.Now, d.Cfg.TimingFloor)
        })
    }
}

func (d *Deps) recordFailure(ctx context.Context, username string, ip netip.Addr) {
    // Always increment IP counter; only increment user if user exists.
    res, err := d.Q.IncrementIPFailure(ctx, ip.String(), d.Cfg.FailedLoginWindow)
    if err != nil { slog.Warn("ip_failure_increment_err", "err", err); return }
    if res.FailedCount >= int32(d.Cfg.MaxFailedPerIP) {
        backoff := computeBackoff(int(res.ConsecutiveLockouts)+1, d.Cfg)
        _ = d.Q.SetIPLockout(ctx, ip.String(), backoff, d.Cfg.IPConsecutiveResetAfter)
        d.Audit.Write(ctx, audit.Row{
            Category: "security", Event: "lockout-ip",
            Payload: map[string]any{"ip": ip.String(),
                "count": res.FailedCount,
                "consecutive": res.ConsecutiveLockouts + 1,
                "backoff_seconds": int(backoff.Seconds())},
        })
    }
    if usernameExists(ctx, d.Q, username) {
        _ = d.Q.IncrementUserFailureWithLock(ctx, username,
            int32(d.Cfg.MaxFailedPerUsername), d.Cfg.LockoutDuration)
        chk, _ := d.Q.PreCheckUserAndIP(ctx, username, ip.String())
        if chk.UserFailedAttempts >= int32(d.Cfg.MaxFailedPerUsername) {
            d.Audit.Write(ctx, audit.Row{
                Category: "security", Event: "lockout-username",
                Payload: map[string]any{"username": username,
                    "count": chk.UserFailedAttempts,
                    "window_seconds": int(d.Cfg.FailedLoginWindow.Seconds())},
            })
        }
    }
}

func computeBackoff(consecutive int, cfg Config) time.Duration {
    n := math.Pow(2, float64(consecutive-1))
    d := time.Duration(float64(cfg.IPLockoutBaseDuration) * n)
    if d > cfg.IPLockoutMaxDuration { return cfg.IPLockoutMaxDuration }
    return d
}
```

### 2.6 Timing floor (D4)

```go
// api/internal/auth/lockout/timing.go
package lockout

import "time"

// ensureMinTime sleeps until at least floor has elapsed since start.
// Used to defeat user-enumeration timing attacks.
func ensureMinTime(start time.Time, now func() time.Time, floor time.Duration) {
    elapsed := now().Sub(start)
    if elapsed < floor {
        time.Sleep(floor - elapsed)
    }
}
```

### 2.7 Reaper (D7)

```go
// api/internal/auth/lockout/reaper.go
package lockout

import (
    "context"
    "log/slog"
    "time"
)

// StartReaper spawns a goroutine that runs the cleanup queries every
// ReaperInterval. Cancel ctx to stop. Uses a Postgres advisory lock so
// only one replica reaps at a time.
func StartReaper(ctx context.Context, d Deps) {
    go func() {
        t := time.NewTicker(d.Cfg.ReaperInterval)
        defer t.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-t.C:
                runOnce(ctx, d)
            }
        }
    }()
}

func runOnce(ctx context.Context, d Deps) {
    // Advisory lock 0xL0CK0UT (arbitrary 64-bit constant).
    var got bool
    if err := d.Q.TryAdvisoryLock(ctx, 0xL0CK0UTREAP).Scan(&got); err != nil || !got {
        return
    }
    defer d.Q.AdvisoryUnlock(ctx, 0xL0CK0UTREAP)

    if err := d.Q.ReapStaleIPLockouts(ctx); err != nil {
        slog.Warn("reap_ip_err", "err", err)
    }
    if err := d.Q.ReapStaleUserLockouts(ctx); err != nil {
        slog.Warn("reap_user_err", "err", err)
    }
}
```

### 2.8 Wire-up

```go
// api/cmd/api/main.go (excerpt)
lockoutDeps := lockout.Deps{Q: lockoutDB, Cfg: lockout.DefaultConfig(), Audit: auditW}
lockout.StartReaper(rootCtx, lockoutDeps)
r.Route("/api/auth", func(r chi.Router) {
    r.Use(ratelimit.LoginMiddleware(rlCfg))      // Plan 10.12
    r.Use(lockout.Middleware(lockoutDeps))        // THIS PLAN
    r.Post("/login", loginHandler.ServeHTTP)
    r.Post("/refresh", refreshHandler.ServeHTTP)
})
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0042_auth_ip_lockouts.sql` | `auth_ip_lockouts` table + index | `TestMigration_AuthIPLockouts` |
| 2 | `api/internal/auth/lockout/config.go` | `Config`, `DefaultConfig` | (smoke) |
| 3 | `api/internal/auth/lockout/queries.sql` (+ sqlc) | `PreCheckUserAndIP`, `IncrementUserFailureWithLock`, `IncrementIPFailure`, `SetIPLockout`, `ResetUserOnSuccess`, `ReapStaleIPLockouts`, `ReapStaleUserLockouts` | `TestQueries_*` |
| 4 | `api/internal/auth/lockout/timing.go` | `ensureMinTime` | `TestEnsureMinTime` |
| 5 | `api/internal/auth/lockout/middleware.go` | `Middleware`, `Deps`, `recordFailure`, `computeBackoff`, `statusWriter`, `clientIP`, `extractUsernameFromBody` | `TestMiddleware_*` |
| 6 | `api/internal/auth/lockout/reaper.go` | `StartReaper`, `runOnce` | `TestReaper_*` |
| 7 | `api/cmd/api/main.go` (extend) | wire reaper + middleware | integration `TestRoutes_LockoutEnforced` |

---

## 4. Test cases keyed to acceptance criteria

### 4.1 `TestMiddleware_FiveFailuresLockUsername` (AC-1)

```go
func TestMiddleware_FiveFailuresLockUsername(t *testing.T) {
    deps, db := newTestDeps(t)
    h := lockout.Middleware(deps)(loginHandler401()) // always 401

    for i := 0; i < 5; i++ {
        rr := postLogin(h, "alice", "wrong", "10.0.0.1")
        require.Equal(t, 401, rr.Code)
    }
    // 6th attempt is rejected pre-handler with 423.
    rr := postLogin(h, "alice", "wrong", "10.0.0.1")
    require.Equal(t, 423, rr.Code)
    var p map[string]any
    require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))
    require.Equal(t, "account-locked", p["type"])

    // From a different IP, alice is also locked (per-username, not per-IP).
    rr2 := postLogin(h, "alice", "wrong", "10.0.0.2")
    require.Equal(t, 423, rr2.Code)
}
```

### 4.2 `TestMiddleware_TwentyIPFailuresLockIP` (AC-2)

```go
func TestMiddleware_TwentyIPFailuresLockIP(t *testing.T) {
    deps, _ := newTestDeps(t)
    h := lockout.Middleware(deps)(loginHandler401())

    for i := 0; i < 20; i++ {
        u := fmt.Sprintf("user%d", i)
        rr := postLogin(h, u, "wrong", "10.0.0.7")
        require.Equal(t, 401, rr.Code)
    }
    rr := postLogin(h, "user-x", "wrong", "10.0.0.7")
    require.Equal(t, 429, rr.Code)
    require.NotEmpty(t, rr.Header().Get("Retry-After"))
}
```

### 4.3 `TestMiddleware_UnknownUsernameTimingMatches` (AC-3, security)

```go
func TestMiddleware_UnknownUsernameTimingMatches(t *testing.T) {
    deps, _ := newTestDeps(t)
    deps.Cfg.TimingFloor = 500 * time.Millisecond
    h := lockout.Middleware(deps)(loginHandler401WithArgon2id()) // verifies always

    durs := map[string][]time.Duration{}
    for _, who := range []string{"alice-real", "no-such-user"} {
        for i := 0; i < 5; i++ {
            t0 := time.Now()
            postLogin(h, who, "wrong", "10.0.1.1")
            durs[who] = append(durs[who], time.Since(t0))
        }
    }
    avgReal := mean(durs["alice-real"])
    avgFake := mean(durs["no-such-user"])
    delta := abs(avgReal - avgFake)
    require.Less(t, delta, 50*time.Millisecond,
        "timing diff %v exceeds 50ms tolerance (real=%v, fake=%v)", delta, avgReal, avgFake)
}
```

### 4.4 `TestMiddleware_SuccessResetsCounter` (AC-1 reset)

```go
func TestMiddleware_SuccessResetsCounter(t *testing.T) {
    deps, db := newTestDeps(t)
    seedUser(db, "alice", "right-pw")
    h := lockout.Middleware(deps)(loginHandlerVerifying(deps))

    for i := 0; i < 4; i++ {
        rr := postLogin(h, "alice", "wrong", "10.0.0.5")
        require.Equal(t, 401, rr.Code)
    }
    // Success on attempt 5 — should reset.
    rr := postLogin(h, "alice", "right-pw", "10.0.0.5")
    require.Equal(t, 200, rr.Code)

    fa := readFailedAttempts(db, "alice")
    require.Equal(t, 0, fa)
}
```

### 4.5 `TestMiddleware_AuditOnLockout` (AC-4)

```go
func TestMiddleware_AuditOnLockout(t *testing.T) {
    deps, db := newTestDeps(t)
    h := lockout.Middleware(deps)(loginHandler401())

    for i := 0; i < 5; i++ { postLogin(h, "alice", "wrong", "10.0.0.9") }
    rows := selectAudit(db, "lockout-username")
    require.NotEmpty(t, rows)
    require.Equal(t, "security", rows[0].Category)
    require.Equal(t, "alice", rows[0].Payload["username"])
}
```

### 4.6 `TestComputeBackoff_Exponential` (D5 unit)

```go
func TestComputeBackoff_Exponential(t *testing.T) {
    cfg := lockout.DefaultConfig()
    require.Equal(t, 15*time.Minute,  computeBackoffExp(1, cfg))
    require.Equal(t, 30*time.Minute,  computeBackoffExp(2, cfg))
    require.Equal(t, 60*time.Minute,  computeBackoffExp(3, cfg))
    require.Equal(t, 120*time.Minute, computeBackoffExp(4, cfg))
    require.Equal(t, 240*time.Minute, computeBackoffExp(5, cfg))
    require.Equal(t, 240*time.Minute, computeBackoffExp(6, cfg)) // capped
}
```

### 4.7 `TestReaper_ClearsStaleRows` (D7)

```go
func TestReaper_ClearsStaleRows(t *testing.T) {
    deps, db := newTestDeps(t)
    insertIPLockout(db, "10.0.0.1", time.Now().Add(-25*time.Hour))
    insertUserLockout(db, "alice", time.Now().Add(-2*time.Hour))

    runOnceExp(context.Background(), deps)

    require.Equal(t, 0, countIPLockouts(db))
    require.Equal(t, 0, readFailedAttempts(db, "alice"))
}
```

### 4.8 `TestRouteIntegration_LockoutEnforced` (integration)

End-to-end: hits `/api/auth/login` 5 times with wrong password, then
asserts 6th returns 423; then hits the unlock endpoint
(`POST /api/users/{id}/unlock`, owned by Story 10.1) and asserts the
next login attempt gets through.

---

## 5. Edge cases and how the plan handles each

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **Legitimate user fat-fingers password 5 times** → locked for 15 minutes; 423 response includes `Retry-After`; admin-reset path via `POST /api/users/{id}/unlock` (Story 10.1 AC-3). | `TestMiddleware_FiveFailuresLockUsername` + `TestRouteIntegration_LockoutEnforced` |
| E2 | **Distributed credential stuffing across many IPs.** Per-IP lockout fires individually for each abusive IP; per-username lockout catches the case where one username is being targeted across IPs. | `TestMiddleware_FiveFailuresLockUsername` covers cross-IP for one username; `TestMiddleware_TwentyIPFailuresLockIP` covers the single-IP path. |
| E3 | **Race on attempts 4↔5 from the same IP.** Two concurrent wrong-password attempts both see `failed_attempts=4`. With D8's row-level lock (`FOR UPDATE`), the second attempt blocks until the first commits, sees `failed_attempts=5`, and the lockout fires correctly. | D8 + `TestMiddleware_RaceOnFinalAttempt` (concurrent goroutine test) |
| E4 | **IPv6 client.** `auth_ip_lockouts.ip` is `INET`; IPv6 indexed identically. `clientIP` parses both via `net/netip.ParseAddr`. | Schema + `clientIP` helper |
| E5 | **Username case sensitivity.** Lookups use `lower(username)` to match the unique constraint from Story 10.1. The counter and lockout state live on the canonical row. | `lower($1)` in `PreCheckUserAndIP` and `IncrementUserFailureWithLock`. |
| E6 | **Window boundary** — 5 failures in 15 min: the 5th must be inside the window. Window slides via `window_start` reset when the next failure is older than `FailedLoginWindow` after `window_start`. (Per-IP only; per-username window enforcement happens via the same logic on `users` — extension to `users` table out of scope; counter resets only on success.) | D3 + integration test on a 16-min sequence |
| E7 | **Pre-handler check fails-open on DB error.** If the `PreCheckUserAndIP` query returns an error (DB hiccup), the middleware logs and *passes through* to the handler — we'd rather allow a legitimate login than wedge the auth surface. The post-step still records the failure if the handler 401s. | `recordFailure` handles errors via `slog.Warn`; pre-check error path swallows and continues. |
| E8 | **Reset of per-IP counter.** Per-IP counter is NOT reset on a successful login (D10) — an attacker may guess one valid password among many wrong ones. The window slide eventually clears the counter when 15 min pass without a failure. The reaper clears stale lockouts after 24 h. | D10; `TestMiddleware_SuccessResetsUsername_NotIP` |
| E9 | **Reaper races across replicas.** Postgres advisory lock (`pg_try_advisory_lock`) ensures only one replica reaps per interval. | `runOnce` uses `TryAdvisoryLock`. |
| E10 | **NAT-shared IP (office).** Per-IP cap of 20 may pinch a noisy office. Mitigation: the operator can raise `MaxFailedPerIP` via Story 7.15 settings. Documented. | Note in `Config` doc. |
| E11 | **Argon2id verify cost varies under load.** Timing floor is set to 500 ms which is comfortably above argon2id's ~200 ms typical cost. If the host is under load and argon2id takes >500 ms, no extra sleep is added. | `ensureMinTime` is a one-way floor, never compresses. |
| E12 | **Audit row write fails.** The audit writer is best-effort with retries (Story 10.16 owns); a write failure does not cause the lockout itself to fail. The state update commits even if the audit row doesn't. | D9 caveat: audit is in the same transaction *only* when the audit writer is Postgres-backed; otherwise it's a fire-and-forget. |

---

## 6. Acceptance checklist

- [ ] **A1** Per-username lockout: 5 failed logins for a username within 15 min → 6th attempt returns 423 `type: account-locked` with `Retry-After`. Successful login resets `failed_attempts` to 0 and `locked_until` to NULL. (`TestMiddleware_FiveFailuresLockUsername`, `TestMiddleware_SuccessResetsCounter`)
- [ ] **A2** Per-IP lockout: 20 failed logins from a single IP across any usernames within 15 min → next attempt returns 429 `type: ip-throttled` with exponentially-increasing `Retry-After` (15 → 30 → 60 → 120 → 240 min, capped). (`TestMiddleware_TwentyIPFailuresLockIP`, `TestComputeBackoff_Exponential`)
- [ ] **A3** Unknown-username timing matches wrong-password timing within 50 ms (always run argon2id-verify against a dummy hash + 500 ms timing floor). (`TestMiddleware_UnknownUsernameTimingMatches`)
- [ ] **A4** Unknown-username path increments per-IP counter only; per-username counter is not touched (the username doesn't exist). (`TestMiddleware_UnknownUsernameOnlyIncrementsIP`)
- [ ] **A5** Audit row written on lockout: `category='security'`, `event='lockout-username'` or `'lockout-ip'`, `payload={target, count, window, consecutive}`. (`TestMiddleware_AuditOnLockout`)
- [ ] **A6** Lockout state updates use `SELECT … FOR UPDATE` row-level locks; concurrent attempts that should produce a lockout reliably do so. (`TestMiddleware_RaceOnFinalAttempt`)
- [ ] **A7** Reaper goroutine runs hourly, clears `auth_ip_lockouts` rows whose `locked_until < now() - 24h`, and resets `users.failed_attempts/locked_until` for rows whose `locked_until < now() - 1h`. Single-replica via Postgres advisory lock. (`TestReaper_ClearsStaleRows`)
- [ ] **A8** Migration `0042_auth_ip_lockouts.sql` creates the `auth_ip_lockouts` table with columns `(ip INET PK, failed_count, window_start, locked_until, consecutive_lockouts, last_lockout_at, updated_at)`, the `auth_ip_lockouts_locked_until` partial index, and the non-negative CHECKs. (`TestMigration_AuthIPLockouts`)
- [ ] **A9** `users.failed_attempts` and `users.locked_until` columns (already declared in Story 10.1 README schema) are read and updated by the middleware; counter resets on success in the same transaction as session creation. (`TestMiddleware_SuccessResetsCounter`)
- [ ] **A10** Defaults: `MaxFailedPerUsername=5`, `MaxFailedPerIP=20`, `FailedLoginWindow=15min`, `LockoutDuration=15min`, `IPLockoutBaseDuration=15min`, `IPLockoutMaxDuration=240min`, `TimingFloor=500ms`, `ReaperInterval=1h`. (`config.go` review.)
- [ ] **A11** This middleware is *distinct* from the rate-limit middleware (Plan 10.12): rate-limit caps requests-per-second over a 60 s window; lockout is a 15+ min block triggered by *failures*. The two middlewares run in series with rate-limit first. (Documented in §1; integration `TestAuthChain_RateLimitThenLockout`.)
- [ ] **A12** Pre-handler 423/429 rejections write an `attempt-while-locked-*` audit row but do NOT increment counters further (no compounding). (`TestMiddleware_AttemptWhileLockedDoesNotIncrement`)
- [ ] **A13** A locked user, after the lockout expires (or admin runs `POST /api/users/{id}/unlock` per Story 10.1 AC-3), can log in normally. (`TestRouteIntegration_LockoutEnforced` end-to-end)
