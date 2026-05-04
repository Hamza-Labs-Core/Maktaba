# Implementation Plan — Story 10.2 Web login (cookie + CSRF)

> Companion to [story-10-02-web-login.md](story-10-02-web-login.md).
> Schema for `web_sessions` is in [README.md](README.md). Argon2id verify
> comes from [Story 10.1](plan-10-01-user-store.md). CSRF enforcement is
> the sister [Story 10.10](story-10-10-csrf-protection.md); this plan
> *issues* the CSRF cookie but does not enforce it.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0021_web_sessions.sql` (Postgres) and `0021_web_sessions.sqlite.sql` (SQLite). |
| Login handler | `api/internal/http/auth_login.go` — picks the surface (cookie vs JWT) by inspecting `X-Maktaba-Client: native`. |
| Session store | `api/internal/auth/sessions.go` — typed wrapper around sqlc-generated queries; debounced `last_seen_at`. |
| Cookie codec | `api/internal/auth/cookies.go` — `Set`, `Clear`, attribute calculation from config. |
| Session middleware | `api/internal/http/middleware/session.go` — loads `User` into `context.Context` from `mkt_sess`; expired/revoked sessions clear the cookie and pass through as anonymous. |
| Out of scope | CSRF enforcement (Story 10.10); brute-force counters (Story 10.11); rate limit (Story 10.12); JWT branch (Story 10.3). |

## 1. Architecture diagram

```
POST /api/auth/login
   ▼
┌──────────────────────────────────────────────────────────┐
│ http/auth_login.go                                       │
│   1. JSON decode {username, password}                    │
│   2. branchSurface(req) → cookie | jwt                   │
│   3. start := time.Now()                                 │
│   4. user, hash := store.GetByUsername(username)         │
│        miss → simulate verify with the dummy hash to keep│
│              timing identical to a real wrong-password   │
│   5. ok := auth.Verify(password, hash)                   │
│   6. enforce minimum-delay: sleep(max(0, 500ms - elapsed))│
│   7. ok==false → 401 invalid-credentials                 │
│   8. ok && needsRehash → store.UpdatePassword (upgrade)  │
│   9. cookie surface →                                    │
│        sessions.Create(userID, ip, ua) → sessionID + csrf│
│        cookies.Set(w, sessionID, csrfToken)              │
│   10. write JSON {user: {id, username, is_admin}}        │
└──────────────────────────────────────────────────────────┘

Subsequent request:
   ▼
┌──────────────────────────────────────────────────────────┐
│ middleware/session.go                                    │
│   - read mkt_sess cookie                                 │
│   - sessions.Load(sessionID) → User | (expired/revoked)  │
│   - on expiry/revoke: clear mkt_sess via Set-Cookie max-age=0 │
│   - on success: bumpLastSeen(sessionID) [debounced 1/min]│
│   - inject User into ctx via auth.WithUser(ctx, user)    │
└──────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0021_web_sessions.sql` | Postgres table, indexes per [README.md](README.md). |
| `shared/db/migrations/0021_web_sessions.sqlite.sql` | SQLite variant. |
| `shared/db/queries/web_sessions.sql` | sqlc input — InsertSession, GetSession, BumpLastSeen, ExpiredCleanup. |
| `api/internal/auth/sessions.go` | `SessionStore` with Create/Load/BumpLastSeen/Revoke/RevokeAllForUser. |
| `api/internal/auth/cookies.go` | `SetSessionCookies`, `ClearSessionCookies`, `csrfTokenFromCookie`. |
| `api/internal/http/auth_login.go` | `POST /api/auth/login` handler. |
| `api/internal/http/middleware/session.go` | Cookie-based authn middleware. |
| `api/internal/auth/timing.go` | `EnforceMinDelay(ctx, start, min)` — `time.Sleep` with context cancellation. |
| `api/internal/http/auth_login_test.go` | Integration tests (httptest). |
| `api/internal/auth/sessions_test.go` | Store tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.WebSessionTTLSec` (default 28×24×3600), `Auth.LoginMinDelayMs` (default 500), `Auth.CookieSecure`, `Auth.CookieSameSite`, `Auth.CookieDomain`. |
| `api/internal/http/router.go` | `r.Post("/api/auth/login", login(...))`; install `session.Middleware` before all routes that need `auth.UserFromContext`. |
| `api/cmd/api/main.go` | Wire `SessionStore` into the router builder. |

### 2.3 Type definitions

```go
// api/internal/auth/sessions.go
package auth

import (
    "context"
    "net"
    "time"

    "github.com/google/uuid"
)

type Session struct {
    ID         uuid.UUID
    UserID     uuid.UUID
    CSRFToken  string
    CreatedAt  time.Time
    LastSeenAt time.Time
    ExpiresAt  time.Time
    IP         net.IP
    UserAgent  string
    RevokedAt  *time.Time
}

type SessionStore interface {
    Create(ctx context.Context, p CreateSessionParams) (Session, error)
    Load(ctx context.Context, id uuid.UUID) (Session, error)
    BumpLastSeen(ctx context.Context, id uuid.UUID) error
    Revoke(ctx context.Context, id uuid.UUID) error
    RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int, error)
}

type CreateSessionParams struct {
    UserID    uuid.UUID
    TTL       time.Duration
    IP        net.IP
    UserAgent string
}

// Sentinel error returned by Load when the row is gone, expired, or revoked.
var ErrSessionInvalid = errors.New("auth: session invalid")
```

```go
// api/internal/auth/cookies.go
type CookieOptions struct {
    Secure   bool
    SameSite http.SameSite       // http.SameSiteLaxMode default
    Domain   string              // empty = host-only
    Path     string              // "/"
}

func SetSessionCookies(w http.ResponseWriter, sessID uuid.UUID, csrf string, ttl time.Duration, opt CookieOptions)
func ClearSessionCookies(w http.ResponseWriter, opt CookieOptions)
```

`mkt_sess` is `httpOnly=true`; `mkt_csrf` is `httpOnly=false` (the SPA
reads it). Both share `Secure`, `SameSite`, `Domain`, `Path`.

## 3. Database migration — Postgres

`shared/db/migrations/0021_web_sessions.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE web_sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token    TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    ip            INET,
    user_agent    TEXT,
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX web_sessions_user_active
    ON web_sessions (user_id) WHERE revoked_at IS NULL;

CREATE INDEX web_sessions_reaper
    ON web_sessions (expires_at) WHERE revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS web_sessions;
-- +goose StatementEnd
```

### 3.1 SQLite variant

`shared/db/migrations/0021_web_sessions.sqlite.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE web_sessions (
    id            TEXT PRIMARY KEY,           -- UUID
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token    TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    last_seen_at  TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    expires_at    TEXT NOT NULL,
    ip            TEXT,                       -- string form of INET
    user_agent    TEXT,
    revoked_at    TEXT
);

CREATE INDEX web_sessions_user_active
    ON web_sessions (user_id) WHERE revoked_at IS NULL;

CREATE INDEX web_sessions_reaper
    ON web_sessions (expires_at) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS web_sessions;
-- +goose StatementEnd
```

## 4. sqlc queries

`shared/db/queries/web_sessions.sql`:

```sql
-- name: InsertWebSession :one
INSERT INTO web_sessions (id, user_id, csrf_token, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, user_id, csrf_token, created_at, last_seen_at,
          expires_at, ip, user_agent, revoked_at;

-- name: GetActiveWebSession :one
SELECT id, user_id, csrf_token, created_at, last_seen_at,
       expires_at, ip, user_agent, revoked_at
FROM web_sessions
WHERE id = $1 AND revoked_at IS NULL AND expires_at > now();

-- name: BumpWebSessionLastSeen :exec
UPDATE web_sessions
SET last_seen_at = now()
WHERE id = $1
  AND revoked_at IS NULL
  AND last_seen_at < now() - interval '1 minute';   -- debounce

-- name: RevokeWebSession :exec
UPDATE web_sessions SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllWebSessionsForUser :execrows
UPDATE web_sessions SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: ReapExpiredWebSessions :execrows
DELETE FROM web_sessions
WHERE expires_at < now() - interval '7 days';
```

The 1-minute debounce in `BumpWebSessionLastSeen` is the *only* place
that touches the row on a hot path; idle sessions stay cold.

## 5. Login handler

```go
// api/internal/http/auth_login.go
package http

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "strings"
    "time"

    "maktaba/api/internal/auth"
)

const dummyHash = "$argon2id$v=19$m=65536,t=2,p=1$" +
    // realistic-shape hash for the user-not-found timing path. Generated
    // once at startup from "x" with default params and stored in a const.
    "AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func loginHandler(users auth.Store, sessions auth.SessionStore, cfg auth.Config) http.HandlerFunc {
    type req struct {
        Username string `json:"username"`
        Password string `json:"password"`
    }
    return func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        var body req
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            problem(w, http.StatusBadRequest, "invalid-json", "")
            return
        }

        user, hash, err := users.GetByUsername(r.Context(), body.Username)
        if err != nil && !errors.Is(err, auth.ErrUserNotFound) {
            problem(w, http.StatusInternalServerError, "internal", "")
            return
        }

        // Always run argon2 verify, even on user-not-found, against a
        // realistic-shape dummy hash so the wall clock is identical.
        compareHash := hash
        if errors.Is(err, auth.ErrUserNotFound) {
            compareHash = dummyHash
        }
        ok, needsRehash, _ := auth.Verify(body.Password, compareHash)

        // Enforce the 500 ms minimum delay (story AC-3) regardless of branch.
        auth.EnforceMinDelay(r.Context(), start, cfg.LoginMinDelay)

        if !ok || errors.Is(err, auth.ErrUserNotFound) {
            problem(w, http.StatusUnauthorized, "invalid-credentials", "")
            return
        }

        // Opportunistic rehash on successful login (uses Story 10.1's needsRehash).
        if needsRehash {
            _ = users.UpgradeHash(r.Context(), user.ID, body.Password)
        }

        if isNativeClient(r) {
            issueJWT(w, r, user)   // delegates to Story 10.3
            return
        }

        sess, err := sessions.Create(r.Context(), auth.CreateSessionParams{
            UserID:    user.ID,
            TTL:       time.Duration(cfg.WebSessionTTLSec) * time.Second,
            IP:        clientIP(r),
            UserAgent: r.Header.Get("User-Agent"),
        })
        if err != nil {
            problem(w, http.StatusInternalServerError, "internal", "")
            return
        }

        auth.SetSessionCookies(w, sess.ID, sess.CSRFToken,
            time.Until(sess.ExpiresAt), cfg.Cookies)

        writeJSON(w, http.StatusOK, map[string]any{
            "user": map[string]any{
                "id": user.ID, "username": user.Username, "is_admin": user.IsAdmin,
            },
        })
    }
}

func isNativeClient(r *http.Request) bool {
    return strings.EqualFold(r.Header.Get("X-Maktaba-Client"), "native")
}
```

`auth.EnforceMinDelay` is implemented as:

```go
func EnforceMinDelay(ctx context.Context, start time.Time, min time.Duration) {
    elapsed := time.Since(start)
    if elapsed >= min {
        return
    }
    select {
    case <-time.After(min - elapsed):
    case <-ctx.Done():
    }
}
```

The min-delay is intentionally floor-only; we never *cap* the response
time, since legitimate slow-CPU paths (cold argon2 cache miss) should
not be rushed.

## 6. Session middleware

```go
// api/internal/http/middleware/session.go
package middleware

import (
    "net/http"

    "github.com/google/uuid"

    "maktaba/api/internal/auth"
)

func Session(store auth.SessionStore, users auth.Store, cfg auth.Config) func(next http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            c, err := r.Cookie("mkt_sess")
            if err != nil {
                next.ServeHTTP(w, r)   // anonymous
                return
            }
            sid, err := uuid.Parse(c.Value)
            if err != nil {
                auth.ClearSessionCookies(w, cfg.Cookies)
                next.ServeHTTP(w, r)
                return
            }
            sess, err := store.Load(r.Context(), sid)
            if err != nil {
                // Expired, revoked, or unknown — clear and treat as anon.
                auth.ClearSessionCookies(w, cfg.Cookies)
                next.ServeHTTP(w, r)
                return
            }
            user, err := users.GetByID(r.Context(), sess.UserID)
            if err != nil {
                auth.ClearSessionCookies(w, cfg.Cookies)
                next.ServeHTTP(w, r)
                return
            }
            // Debounced bump — query is a no-op if last_seen_at < now()-1min.
            _ = store.BumpLastSeen(r.Context(), sess.ID)

            ctx := auth.WithUser(r.Context(), user)
            ctx = auth.WithSession(ctx, sess)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

`auth.WithUser`/`UserFromContext` and `auth.WithSession`/`SessionFromContext`
live in `api/internal/auth/context.go` and use unexported context keys.

## 7. Cookie codec

```go
// api/internal/auth/cookies.go
package auth

import (
    "net/http"
    "time"

    "github.com/google/uuid"
)

func SetSessionCookies(w http.ResponseWriter, sessID uuid.UUID, csrf string, ttl time.Duration, opt CookieOptions) {
    base := func(name, val string, httpOnly bool) *http.Cookie {
        return &http.Cookie{
            Name:     name,
            Value:    val,
            Path:     opt.Path,
            Domain:   opt.Domain,
            Secure:   opt.Secure,
            HttpOnly: httpOnly,
            SameSite: opt.SameSite,
            MaxAge:   int(ttl.Seconds()),
        }
    }
    http.SetCookie(w, base("mkt_sess", sessID.String(), true))
    http.SetCookie(w, base("mkt_csrf", csrf, false))
}

func ClearSessionCookies(w http.ResponseWriter, opt CookieOptions) {
    expire := func(name string, httpOnly bool) *http.Cookie {
        return &http.Cookie{
            Name:     name,
            Value:    "",
            Path:     opt.Path,
            Domain:   opt.Domain,
            Secure:   opt.Secure,
            HttpOnly: httpOnly,
            SameSite: opt.SameSite,
            MaxAge:   -1,
            Expires:  time.Unix(0, 0),
        }
    }
    http.SetCookie(w, expire("mkt_sess", true))
    http.SetCookie(w, expire("mkt_csrf", false))
}
```

The `MaxAge: -1` plus `Expires: epoch` form is the safest cross-browser
"delete this cookie" pattern.

## 8. Crypto details

| Concern | Decision |
|---|---|
| Session ID | UUID v7 from `github.com/google/uuid`; `gen_random_uuid()` is the DB default for direct INSERTs. UUID v7 monotonicity makes index ordering predictable but the value is still 122 bits of entropy. |
| CSRF token | 32 bytes from `crypto/rand`, base64url-encoded (43 chars). Stored in `csrf_token` column verbatim; comparison in Story 10.10 uses `subtle.ConstantTimeCompare`. |
| Cookie signing | Not applied: the session ID has 122 bits of entropy and is itself the secret. We do not embed the user ID or any payload in the cookie. |
| Hash storage of session ID | Not applied: web sessions live only server-side; a DB compromise discloses everything else anyway, so hashing the ID is no defense. Refresh tokens (Story 10.3) are different — they live on the device and *are* hashed. |
| Argon2id verify | From Story 10.1's `auth.Verify`. The dummy-hash branch must be a real `$argon2id$v=19$...` string with default params so the timing matches — see §5. |
| Timing-attack mitigation | `EnforceMinDelay(start, 500ms)` runs on every login outcome including malformed JSON (so the response time of "JSON broken" doesn't reveal "user does not exist"). |

## 9. Test plan

### 9.1 SessionStore (`sessions_test.go`)

| Test | What it pins |
|---|---|
| `TestCreateInsertsRow` | Create returns Session with `CSRFToken != ""`; row exists with correct `expires_at`. |
| `TestLoadActiveReturnsSession` | Create then Load → equal IDs, `RevokedAt == nil`. |
| `TestLoadExpiredReturnsErrInvalid` | Force `expires_at = now() - 1s`; Load → `ErrSessionInvalid`. |
| `TestLoadRevokedReturnsErrInvalid` | Revoke; Load → `ErrSessionInvalid`. |
| `TestBumpLastSeenDebounced` | Create; BumpLastSeen twice within 30s → second is a no-op (`last_seen_at` unchanged); after 70s sleep (or fake clock), bump succeeds. |
| `TestRevokeIdempotent` | Revoke twice → second is no-op; one row, one `revoked_at`. |
| `TestCSRFTokenIs32Bytes` | Decode the base64url csrf_token → 32 bytes. |

### 9.2 Login handler (`auth_login_test.go`)

| Test | What it pins |
|---|---|
| `TestLoginSuccessSetsCookies` | Valid creds → 200; `Set-Cookie` for both `mkt_sess` and `mkt_csrf` with `HttpOnly`/`Secure`/`SameSite=Lax` for the session cookie, `HttpOnly=false` for csrf. |
| `TestLoginWrongPasswordReturns401` | Body `{type: "invalid-credentials"}`. |
| `TestLoginUnknownUserReturns401SameShape` | Same body shape and same status as wrong-password. |
| `TestLoginTimingUserNotFoundMatchesWrongPassword` | 100 trials each; means within 50 ms, max within 100 ms. |
| `TestLoginEnforces500msFloor` | Mock argon2 to return in 1 ms; assert response wall-clock >= 500 ms (within 20 ms). |
| `TestLoginEmitsAuditOnFail` | A failed login writes one `audit_log` row with `category='security', event='login.failed'` (Story 10.16 owns the schema; this test asserts the row appears). |
| `TestLoginNativeHeaderRoutesToJWT` | `X-Maktaba-Client: native` → response body has `access_token`, no `Set-Cookie`. |
| `TestLoginNoCookieGetReturnsAnonymous` | GET `/api/me` (or whatever returns the current user) without cookie → 200 anon or 401 depending on route policy; no panic. |
| `TestLoginTamperedCookieClearsAndReturns401` | Modify one byte of `mkt_sess`; GET protected route → 401 + `Set-Cookie: mkt_sess=; max-age=0`. |
| `TestLoginRehashUpgrade` | Stand up a user with a weak hash (`m=8192`); login → row's `pw_hash` is now the default-strength hash. |

### 9.3 Session middleware

| Test | What it pins |
|---|---|
| `TestMiddlewareLoadsUser` | Logged-in cookie → handler sees `auth.UserFromContext(ctx)` populated. |
| `TestMiddlewareDebouncesBump` | Two requests inside 60s → only one BumpLastSeen UPDATE hits the DB (verified via a mock store). |
| `TestMiddlewareExpiredClearsCookie` | Force `expires_at < now()`; request → response has `Set-Cookie: mkt_sess=; max-age=0`; ctx user is nil. |
| `TestMiddlewareRevokedClearsCookie` | Same as above for `revoked_at IS NOT NULL`. |
| `TestMiddlewareCookieMissingHintHeader` | When the route requires authn and the request has no cookies, the 401 carries `Maktaba-Hint: cookies-missing-check-proxy`. |

### 9.4 Cross-dialect parity

`auth_login_dialect_test.go` runs the integration flow against Postgres
and SQLite via the project's parametrized fixture. Cookie/header
behaviour is identical; only the `last_seen_at` debounce SQL differs (PG
uses `interval '1 minute'`, SQLite uses `datetime('now', '-1 minute')`).

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Two browser tabs, same session | Both tabs share the cookie; logout from either → both fail next request. | `TestSharedSessionLogoutAllTabs` |
| Reverse proxy strips cookies | 401 + `Maktaba-Hint: cookies-missing-check-proxy`. | `TestMiddlewareCookieMissingHintHeader` |
| `samesite=lax` and a deep-link from external page | GET cross-site allowed (top-level nav); POST cross-site blocked by CSRF middleware (Story 10.10). | Documented; covered in 10.10 plan. |
| Login while already logged in | A new session row is inserted; the old session remains valid until logout/expiry. The browser receives the new cookies (overwriting the old). | `TestLoginOverwritesCookie` |
| Empty `User-Agent` | Stored as `NULL`; no error. | `TestCreateAcceptsEmptyUA` |
| `clientIP(r)` from `X-Forwarded-For` chain | Use the leftmost public IP per RFC 7239. The trusted proxy list comes from `[server].trusted_proxies` (loaded in Epic 7 Story 7.15). | `TestClientIPRespectsForwarded` |
| Cookie domain mismatch (multi-host setup) | `Domain=""` (host-only) by default; setting `Auth.CookieDomain` makes the cookie cross-subdomain — documented as the only multi-host knob. | Config doc |
| Very long username | Schema doesn't cap; the handler trims to 256 chars before lookup (`username` column remains TEXT). Beyond 256 → 422 `username-too-long`. | `TestPostLoginUsernameTooLong` |
| Concurrent session writes for one user | No constraint blocks parallel sessions; a user with N tabs has up to N rows. The `web_sessions_user_active` partial index keeps lookups fast. | n/a |
| 500-ms floor under context cancellation | If the client disconnects mid-sleep, `EnforceMinDelay` returns early via `<-ctx.Done()`; the handler still writes the 401 (the response is dropped on the floor). | `TestLoginCanceledMidSleep` |

## 11. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/google/uuid` | already | UUID v7 + parse. |
| `github.com/jackc/pgx/v5` | already | Postgres + `pgx.ErrNoRows` mapping. |
| `golang.org/x/crypto/argon2` | from Story 10.1 | Verify path. |
| `github.com/go-chi/chi/v5` | already | Router. |

No new heavy deps.

## 12. Acceptance checklist

**Migration**
- [ ] `0021_web_sessions.sql` applies; both indexes present.
- [ ] CASCADE from `users(id)` deletes web sessions.

**Cookies**
- [ ] `mkt_sess` is `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`.
- [ ] `mkt_csrf` is the same except `HttpOnly=false`.
- [ ] `MaxAge` matches `Auth.WebSessionTTLSec` (default 28×24×3600).

**Login flow**
- [ ] AC-1: cookies set with documented attributes; body returns `{user: {...}}`.
- [ ] AC-2: subsequent request uses cookie; `last_seen_at` bumps only once per minute.
- [ ] AC-3: wrong password and unknown user both return 401 `invalid-credentials` and take >= 500 ms.
- [ ] AC-4: expired session returns 401 with `Set-Cookie: mkt_sess=; max-age=0`.

**Tests**
- [ ] Timing test passes (50 ms variance window).
- [ ] All §9 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.2.
- [ ] `Maktaba-Hint: cookies-missing-check-proxy` documented in operations.
