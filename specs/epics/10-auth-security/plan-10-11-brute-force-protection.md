# Implementation Plan — Story 10.11 Brute-force / credential-stuffing protection

> Companion to [story-10-11-brute-force-protection.md](story-10-11-brute-force-protection.md).
> Per-IP raw rate limit is owned by [Story 10.12](plan-10-12-rate-limiting-auth.md);
> this story owns the *per-username* and *per-IP credential-attempt* counters,
> distinct from raw QPS. Audit rows go through [Story 10.16](story-10-16-security-audit.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Per-username state | `users.failed_attempts` and `users.locked_until` columns (already added by Story 10.1's plan, §3). |
| Per-IP state | New table `auth_ip_attempts` for the rolling 15-min window — see §3. |
| Counter logic | `api/internal/auth/lockout.go` — `RegisterFailure`, `RegisterSuccess`, `IsLocked`, `RegisterIPFailure`. |
| Hook into login | `auth/login` handler invokes RegisterFailure/RegisterSuccess at the appropriate branches; checks `IsLocked` *before* the argon2 verify so locked accounts don't burn CPU on the verify path. |
| Per-IP backoff response | `429` with exponential `Retry-After` whose backoff base is `failed_login_window_sec / max_failed_logins_per_ip` (≈ 45s). |
| Out of scope | The unlock endpoint (Story 10.1 AC-3 — `POST /api/users/{id}/unlock`). The general rate-limit middleware (10.12). |

## 1. Architecture diagram

```
POST /api/auth/login
   ▼
┌──────────────────────────────────────────────────────────────┐
│ http/auth_login.go                                            │
│   1. start := time.Now()                                       │
│   2. lock, _ := lockout.IsLocked(ctx, username, ip)            │
│        if locked → 423 account-locked (or 429 ip-locked) +    │
│                    enforce 500ms floor + audit                │
│   3. user, hash := store.GetByUsername(username)               │
│   4. ok := auth.Verify(password, hash)                         │
│        OR (user not found) verify against dummyHash for timing │
│   5. enforceMinDelay(start, 500ms)                             │
│   6. if !ok:                                                   │
│        if user found    → lockout.RegisterFailure(uid, ip)    │
│        if user not found → lockout.RegisterIPFailure(ip)       │
│      → 401 invalid-credentials                                 │
│   7. if ok:                                                    │
│        lockout.RegisterSuccess(user.ID)   // resets username    │
│        proceed to session/JWT issuance                          │
└──────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0024_auth_ip_attempts.sql` | Postgres table for per-IP rolling failures. |
| `shared/db/migrations/0024_auth_ip_attempts.sqlite.sql` | SQLite variant. |
| `shared/db/queries/auth_lockout.sql` | sqlc input — counters + queries. |
| `api/internal/auth/lockout.go` | `Lockout` struct with the methods above. |
| `api/internal/auth/lockout_test.go` | Unit tests against an in-memory store. |
| `api/internal/http/auth_login_lockout_test.go` | Integration tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.MaxFailedLoginsPerUsername` (default 5), `Auth.MaxFailedLoginsPerIP` (default 20), `Auth.FailedLoginWindowSec` (default 900). |
| `api/internal/http/auth_login.go` | Call `Lockout.IsLocked` before verify; call RegisterFailure/Success on the result. |
| `shared/db/queries/users.sql` | Add `IncrementFailedAttempts`, `LockUser`, `ResetFailedAttempts`. |

### 2.3 Type definitions

```go
// api/internal/auth/lockout.go
package auth

import (
    "context"
    "net"
    "time"

    "github.com/google/uuid"
)

type LockedKind string
const (
    LockedNone     LockedKind = ""
    LockedUsername LockedKind = "username"
    LockedIP       LockedKind = "ip"
)

type LockedState struct {
    Kind    LockedKind
    Until   time.Time     // when the lock expires
    Count   int           // current rolling-window count
}

type Lockout interface {
    IsLocked(ctx context.Context, username string, ip net.IP) (LockedState, error)
    RegisterFailure(ctx context.Context, userID uuid.UUID, ip net.IP) (locked bool, err error)
    RegisterSuccess(ctx context.Context, userID uuid.UUID) error
    RegisterIPFailure(ctx context.Context, ip net.IP) (locked bool, err error)
}

type Config struct {
    MaxPerUsername int
    MaxPerIP       int
    Window         time.Duration
}
```

### 2.4 SQL — sqlc additions

`shared/db/queries/users.sql` (additions):

```sql
-- name: IncrementFailedAttempts :one
UPDATE users
   SET failed_attempts = failed_attempts + 1
 WHERE id = $1
RETURNING failed_attempts;

-- name: LockUserUntil :exec
UPDATE users
   SET locked_until = $2
 WHERE id = $1;

-- name: ResetFailedAttempts :exec
UPDATE users
   SET failed_attempts = 0,
       locked_until    = NULL
 WHERE id = $1;
```

`shared/db/queries/auth_lockout.sql`:

```sql
-- name: RecordIPFailure :exec
INSERT INTO auth_ip_attempts (ip, attempted_at)
VALUES ($1, now());

-- name: CountIPFailuresInWindow :one
SELECT count(*)::int FROM auth_ip_attempts
 WHERE ip = $1 AND attempted_at > now() - $2::interval;

-- name: ReapIPFailures :execrows
DELETE FROM auth_ip_attempts
 WHERE attempted_at < now() - interval '24 hours';
```

## 3. Database migration — Postgres

`shared/db/migrations/0024_auth_ip_attempts.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE auth_ip_attempts (
    id           BIGSERIAL PRIMARY KEY,
    ip           INET NOT NULL,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hot path: COUNT(*) over the rolling window for a given IP.
CREATE INDEX auth_ip_attempts_ip_time
    ON auth_ip_attempts (ip, attempted_at DESC);

-- Reaper sweep: drop rows older than 24h.
CREATE INDEX auth_ip_attempts_reaper
    ON auth_ip_attempts (attempted_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS auth_ip_attempts;
-- +goose StatementEnd
```

The table is small in steady state (< 20 rows per IP per 15 min) and
the reaper keeps it bounded. We do not bother with a `failed_attempts`
counter table because per-username state lives on the `users` row
itself (Story 10.1).

### 3.1 SQLite variant

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE auth_ip_attempts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ip           TEXT NOT NULL,
    attempted_at TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);

CREATE INDEX auth_ip_attempts_ip_time
    ON auth_ip_attempts (ip, attempted_at DESC);

CREATE INDEX auth_ip_attempts_reaper
    ON auth_ip_attempts (attempted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS auth_ip_attempts;
-- +goose StatementEnd
```

## 4. Lockout logic

```go
// api/internal/auth/lockout.go
type lockout struct {
    db    *db.Queries
    cfg   Config
    audit AuditSink
}

func (l *lockout) IsLocked(ctx context.Context, username string, ip net.IP) (LockedState, error) {
    // Per-username lock — read from users row. The username may be
    // unknown; treat that as "no per-username lock".
    if username != "" {
        u, err := l.db.GetUserByUsername(ctx, username)
        if err == nil && u.LockedUntil.Valid && u.LockedUntil.Time.After(time.Now()) {
            return LockedState{
                Kind: LockedUsername, Until: u.LockedUntil.Time,
                Count: int(u.FailedAttempts),
            }, nil
        }
    }
    // Per-IP lock — count rows in the rolling window.
    n, err := l.db.CountIPFailuresInWindow(ctx, ip.String(), l.cfg.Window)
    if err == nil && n > l.cfg.MaxPerIP {
        // Locked until window passes for the oldest qualifying row.
        return LockedState{
            Kind:  LockedIP,
            Until: time.Now().Add(l.cfg.Window),  // upper bound; backoff calc in handler
            Count: n,
        }, nil
    }
    return LockedState{Kind: LockedNone}, nil
}

func (l *lockout) RegisterFailure(ctx context.Context, userID uuid.UUID, ip net.IP) (bool, error) {
    n, err := l.db.IncrementFailedAttempts(ctx, userID)
    if err != nil { return false, err }

    // Always log per-IP failure too (catches distributed stuffing
    // even when the username is known).
    _ = l.db.RecordIPFailure(ctx, ip.String())

    if int(n) > l.cfg.MaxPerUsername {
        until := time.Now().Add(l.cfg.Window)
        if err := l.db.LockUserUntil(ctx, userID, until); err != nil {
            return false, err
        }
        l.audit.Record(ctx, AuditLockoutUsername{
            UserID: userID, Count: int(n), Window: l.cfg.Window, IP: ip,
        })
        return true, nil
    }
    return false, nil
}

func (l *lockout) RegisterSuccess(ctx context.Context, userID uuid.UUID) error {
    return l.db.ResetFailedAttempts(ctx, userID)
}

func (l *lockout) RegisterIPFailure(ctx context.Context, ip net.IP) (bool, error) {
    if err := l.db.RecordIPFailure(ctx, ip.String()); err != nil {
        return false, err
    }
    n, err := l.db.CountIPFailuresInWindow(ctx, ip.String(), l.cfg.Window)
    if err != nil { return false, err }
    if int(n) > l.cfg.MaxPerIP {
        l.audit.Record(ctx, AuditLockoutIP{IP: ip, Count: int(n), Window: l.cfg.Window})
        return true, nil
    }
    return false, nil
}
```

The `IncrementFailedAttempts` query is a single UPDATE returning the
new value. We do not race the `failed_attempts` counter; concurrent
failed logins for the same user might both produce the same final
count, but the lock check (`> MaxPerUsername`) is monotone so the
eventual lock is guaranteed.

## 5. Login-handler integration

```go
// api/internal/http/auth_login.go (additions)
func loginHandler(...) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        var body req
        _ = json.NewDecoder(r.Body).Decode(&body)
        ip := clientIP(r)

        // Lock check FIRST so a locked account doesn't pay the argon2 cost.
        locked, _ := lockout.IsLocked(r.Context(), body.Username, ip)
        if locked.Kind != "" {
            auth.EnforceMinDelay(r.Context(), start, cfg.LoginMinDelay)
            switch locked.Kind {
            case auth.LockedUsername:
                problem(w, http.StatusLocked, "account-locked", fmt.Sprintf(
                    "try again after %s", locked.Until.UTC().Format(time.RFC3339),
                ))
            case auth.LockedIP:
                w.Header().Set("Retry-After", strconv.Itoa(int(retryAfterFor(locked.Count))))
                problem(w, http.StatusTooManyRequests, "ip-throttled", "")
            }
            return
        }

        user, hash, uerr := users.GetByUsername(r.Context(), body.Username)
        compareHash := hash
        if errors.Is(uerr, auth.ErrUserNotFound) {
            compareHash = dummyHash
        }
        ok, _, _ := auth.Verify(body.Password, compareHash)

        auth.EnforceMinDelay(r.Context(), start, cfg.LoginMinDelay)

        if !ok || errors.Is(uerr, auth.ErrUserNotFound) {
            if uerr == nil {
                _, _ = lockout.RegisterFailure(r.Context(), user.ID, ip)
            } else {
                _, _ = lockout.RegisterIPFailure(r.Context(), ip)
            }
            problem(w, http.StatusUnauthorized, "invalid-credentials", "")
            return
        }

        _ = lockout.RegisterSuccess(r.Context(), user.ID)
        // ... continue with session/JWT issuance
    }
}

// retryAfterFor: exponential — 1× window/N for the first overflow, 2× for the next, ...
// Capped at the window itself.
func retryAfterFor(count int) int {
    over := count - 20
    if over < 1 { over = 1 }
    base := 45 // 900 / 20
    backoff := base * (1 << min(over, 5))   // 45, 90, 180, 360, 720, 1440 → cap at window
    if backoff > 900 { backoff = 900 }
    return backoff
}
```

## 6. Reconciliation with NFR Story 23.6

The story's AC-1 flags a "5-min vs 15-min" reconciliation: this story
holds the canonical value at **15 min** (`failed_login_window_sec =
900`). NFR Story 23.6's plan **must** cite this story's value rather
than the historical 5-min draft. We pin the value via a config
validator that warns if the operator overrides to a value < 300s
(too short to defeat slow stuffing) or > 3600s (too punishing for
fat-fingers).

## 7. Test plan

### 7.1 Lockout unit tests (`lockout_test.go`)

| Test | What it pins |
|---|---|
| `TestRegisterFailureIncrementsCounter` | After N RegisterFailure calls, `users.failed_attempts == N`. |
| `TestRegisterFailureLocksAtThreshold` | Calls 1..5 return locked=false; call 6 returns locked=true and `users.locked_until > now()`. |
| `TestIsLockedReturnsLockedUsernameWhenLockedUntilFuture` | After lock, IsLocked(username, anyIP) → LockedUsername. |
| `TestIsLockedReturnsNoneAfterWindow` | After lock, set `locked_until = now() - 1s`; IsLocked → LockedNone. |
| `TestRegisterSuccessResetsCounter` | After 3 failures, RegisterSuccess; `failed_attempts=0, locked_until=NULL`. |
| `TestRegisterIPFailureLocksAtThreshold` | 21 failures from one IP across users → 21st returns locked=true; CountIPFailuresInWindow == 21. |
| `TestRegisterIPFailureRollingWindow` | 19 failures, then sleep window+1s, 1 more → not locked (the 19 expired). |
| `TestUnlockClearsState` | (Story 10.1 endpoint) After lock + unlock → IsLocked → LockedNone; failed_attempts=0. |

### 7.2 Login integration (`auth_login_lockout_test.go`)

| Test | What it pins |
|---|---|
| `TestLoginAfter5FailuresLocksUsername` | 5 wrong passwords for `alice`; 6th request → 423 `account-locked`; 7th valid-password request still 423. |
| `TestLoginLockedAlsoBlocksFromDifferentIP` | After lock from IP A, attempt from IP B with the same username → 423 (per-username, not per-IP). |
| `TestLoginAfterAdminUnlockSucceeds` | Lock; admin POST `/api/users/{id}/unlock`; next valid login → 200. |
| `TestLoginPerIPLockoutKicksAt21` | 20 wrong attempts across many usernames from one IP → 21st request 429 with `Retry-After`. |
| `TestLoginUnknownUserIncrementsIPCounter` | Unknown username from IP X → IP counter += 1; same username's per-username counter NOT incremented (it doesn't exist). |
| `TestLockedAccountDoesNotPayArgon2Cost` | Mock argon2 to fail-loud; locked account login → 423 returned without invoking argon2 (assert mock not called). |
| `TestLoginRetryAfterExponential` | 25 failures from one IP, observe `Retry-After` headers escalating: 90, 180, 360, ... capped at 900. |
| `TestLoginEmitsLockoutAuditWhenLockFires` | The 6th failure writes `audit_log` row `category='security', event='lockout-username', payload.target=username, payload.count=6, payload.window='15m'`. |
| `TestLoginEmitsIPLockoutAudit` | At 21st failure, one row `event='lockout-ip', payload.target=ip`. |
| `TestLoginTimingUnknownVsWrong` | Both paths take ≥ 500ms, mean within 50ms (story AC-3). |

### 7.3 Cross-dialect

Both PG and SQLite via the parametrized fixture; SQLite uses
`datetime('now', '-15 minutes')` for the rolling window.

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Legitimate user fat-fingers 5 times | 6th attempt 423; user can wait or admin unlocks. The 423 message includes the `Retry-After` ISO timestamp. | `TestLoginAfter5FailuresLocksUsername` |
| Distributed stuffing: 1 attempt per IP across 100 IPs | Per-IP lockout doesn't trip (each IP only has 1 failure); per-username lockout catches it after 5 attempts on the targeted account. | `TestLoginAfter5FailuresLocksUsername` |
| Concurrent failures racing the counter | Each UPDATE is atomic; both increments succeed; one of the two might be the one that crosses the threshold and writes the lock. The audit row may be written twice in a tight race (both saw `n > 5`); mitigated by an idempotency dedupe on `(target, event, minute)` in audit's plan (Story 10.16). | `TestRegisterFailureConcurrentLocks` |
| IP behind NAT (office) | Per-IP cap may pinch noisy offices; admin can raise via settings. The per-username cap is the real defense. | Configurable. |
| Unknown user (enumeration probe) | Per-username counter NOT incremented (no row to update); per-IP counter IS incremented (defense against enumeration). Timing matches wrong-password (story AC-3). | `TestLoginUnknownUserIncrementsIPCounter` |
| Lock expiring at exactly the request time | `locked_until > now()` uses strict greater-than; at equality the lock has expired. | `TestIsLockedReturnsNoneAfterWindow` |
| Admin's own account locks itself out | Same rules apply; the admin must wait or another admin unlocks. The sentinel admin token (Story 10.9) is a backdoor for true single-admin recovery. | n/a |
| Reaper backlog | The 24h reaper is run nightly by the existing `tasks/reaper` from Epic 6; no new scheduler needed. | n/a |
| Migration ran on a busy DB | `auth_ip_attempts` is a fresh table; no backfill. The first 15 min after deploy has under-counts; documented as the rollout cost. | n/a |
| Lock counter overflow at INT_MAX | The CHECK constraint on `failed_attempts >= 0` (Story 10.1 plan §3) prevents underflow; INT_MAX is unreachable in the 15-min window in practice. | n/a |

## 9. Dependencies

No new dependencies.

## 10. Acceptance checklist

**Per-username**
- [ ] AC-1: 6th failure within 15 min → 423 `account-locked`; valid password from another IP still locked. Defaults: 5 attempts / 900s window.
- [ ] Successful login resets `failed_attempts` and clears `locked_until`.

**Per-IP**
- [ ] AC-2: 21st failure within 15 min → 429 with `Retry-After`. Default: 20 attempts / 900s window.
- [ ] Backoff escalates exponentially, capped at the window (900s).

**Enumeration**
- [ ] AC-3: unknown-user and wrong-password timings within 50ms; same response shape.
- [ ] Unknown-user attempts increment IP counter only.

**Audit**
- [ ] AC-4: lockout fires write `event='lockout-username'` or `event='lockout-ip'` with payload {target, count, window}.

**Boot**
- [ ] Config validator warns when `failed_login_window_sec` is set outside 300–3600 seconds.

**Tests**
- [ ] All §7 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.11.
- [ ] NFR Story 23.6 cites this story's 15-min canonical value.
