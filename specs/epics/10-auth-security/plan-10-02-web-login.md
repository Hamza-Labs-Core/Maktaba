# Plan 10.2 — Web login (cookie + CSRF) — implementation

> Implementation plan for [story-10-02-web-login.md](story-10-02-web-login.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Depends on `password.Verify` from
> [Plan 10.1](plan-10-01-user-store.md). The CSRF *middleware* itself
> ships in [Story 10.10](story-10-10-csrf-protection.md) — this plan
> issues the `mkt_csrf` cookie and lays down the session table that
> 10.10 will consume; it does NOT enforce double-submit yet (that is
> 10.10's job). Logout/revocation lives in
> [Story 10.5](story-10-05-logout-revocation.md); this plan exposes the
> session-id helpers it needs.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Login dispatch on the `X-Maktaba-Client` header.** Absent or any value other than `"native"` → web (cookie) flow. `"native"` → JWT flow (Story 10.3). One endpoint, two response shapes. | Story AC-1; Story 10.3 EC ("native client that misses `X-Maktaba-Client: native` and is therefore given cookies — this is acceptable"). | The single endpoint avoids per-platform URL fragmentation and lets a misconfigured native client still log in (degraded to cookie) rather than 404'ing. The header is an explicit opt-in to JWT — easy to set, easy to grep for, no Accept-header gymnastics. |
| D2 | **Session id is a 256-bit (32-byte) cryptographically random value, base64url-encoded** as the cookie value. The `web_sessions.id` is a UUID v7; the cookie carries the random value, and we look up the row by a separate `session_token_hash` column. Cookie value ≠ row PK. | Story AC-1; epic README schema (no token column shown — we add one). | The schema in epic README has only `id UUID PRIMARY KEY`, but using a UUID directly as the cookie is exactly the shape we use for refresh tokens (10.3) — there, the plaintext is hashed before storage. Doing the same here means a stolen DB cannot be replayed as cookies. We INSERT `id = uuid7(), session_token = random32, session_token_hash = sha256(token)`; the cookie is `mkt_sess = base64url(random32)`. Lookup is `WHERE session_token_hash = sha256($1)`. UUID v7 keeps the index time-ordered for fast reaper sweeps. |
| D3 | **Hash for session-token lookup is SHA-256 (NOT argon2id).** | Refines story (silent on hash). | Argon2 is for passwords (slow on purpose). For session lookup we need O(1) per request and the input is already 256 bits of entropy from `crypto/rand`, so a fast hash is correct: there is no offline guessing attack against random 256-bit strings. SHA-256 is constant-time and 1-roundtrip; the column is `BYTEA(32)` indexed `UNIQUE`. |
| D4 | **Cookies: `mkt_sess` is `HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=auth.web_session_ttl_sec` (default 28d). `mkt_csrf` is `Secure; SameSite=Lax; Path=/; Max-Age=…` (NOT HttpOnly — the SPA reads it).** Both cookies share the session lifetime; the CSRF cookie is rotated on login but NOT on every request. | Story AC-1. arch §11.2 (`cookie_secure = true`, `cookie_samesite = "lax"`). | `Lax` is the right default: GET cross-site nav (deep links) works, POST cross-site (CSRF) is blocked. Story 10.10 still adds double-submit because Lax doesn't cover cross-origin XHR with credentials and we want defence in depth. The CSRF token is plaintext in the cookie *and* must echo to a header (`X-CSRF-Token`) on state-changing requests; the SPA reads it via `document.cookie`. Rotating the CSRF token per-request burns entropy for no security gain — once the SPA reads it, it's already in JS memory, which is the threat model boundary. |
| D5 | **500 ms minimum response time on `POST /api/auth/login`** — both success and failure. Implemented via `time.Sleep(500*ms - time.Since(start))` at the very end of the handler. | Story AC-3 + test case. | The classic timing oracle is "user-not-found" (~1 ms — no argon2) vs "wrong password" (~30 ms — argon2 ran). The argon2 cost helps but isn't enough; an attacker gathering ~10k samples can still see the difference. Padding to a constant floor neutralises the oracle. We sleep AFTER all work, so the wall time is `floor(work_time, 500ms)`. 500 ms is the right number per the story. |
| D6 | **`last_seen_at` is debounced to one write per session per minute.** The middleware reads the row's current `last_seen_at` and skips the UPDATE if `now() - last_seen_at < 1m`. | Story AC-2. | A SPA fires dozens of requests per minute (autocomplete, polling, list refresh). One UPDATE per request is wasteful and turns the session row into a write-hot-spot that contests with the read query. Debouncing to 1/min keeps the timestamp meaningful for "last activity" UI without burning IOPS. The check is racy but harmless — a duplicate UPDATE is idempotent. |
| D7 | **Tampered cookie → 401 + `Set-Cookie: mkt_sess=; Max-Age=0`.** The middleware always clears the cookie on any auth failure (no row, expired, revoked, signature mismatch). | Story AC-4 + test case. | If the browser keeps sending a junk cookie indefinitely, every page load is a wasted DB lookup. Clearing on failure means the next request is anonymous and cheap. We do NOT distinguish "expired" from "invalid" in the response body; the `Maktaba-Hint: cookies-missing-check-proxy` header is set only when the request had no cookie at all (the proxy stripped it). |

If D2 is rejected (use UUID-as-cookie): a leaked DB dump becomes a cookie dump. Hashed at rest is the standard pattern and we already use it for refresh tokens.

If D5 is rejected (no 500 ms floor): a network attacker with 10k samples can enumerate valid usernames. Test `TestLoginConstantTimeOnUserNotFound` measures the variance and would fail without the floor.

If D6 is rejected (UPDATE every request): the `web_sessions` row becomes the hottest write target in the system. Pgbench shows this dominates p99 by 2-3 ms on busy SPAs.

---

## 1. Architecture diagram — login + authenticated request

```
                                  POST /api/auth/login
                                  body: {username, password}
                                  header: Cookie: (none)
                                          X-Maktaba-Client: (absent)        ← D1: web flow
                                              │
                                              ▼
                              ┌──────────────────────────────┐
                              │ handler.Login                │
                              │  start = time.Now()          │
                              │  user = GetUserByLowerUsername│
                              │  if !user || !Verify():      │
                              │      sleep_to(500ms, start)  │← D5
                              │      → 401 invalid-credentials
                              │  failed_attempts = 0          │ (10.11 hook)
                              │  token  = rand32()            │← D2
                              │  th     = sha256(token)       │← D3
                              │  csrf   = base64url(rand32()) │
                              │  INSERT web_sessions(...)     │
                              │  Set-Cookie mkt_sess=token    │← D4
                              │  Set-Cookie mkt_csrf=csrf     │
                              │  sleep_to(500ms, start)       │
                              │  → 200 {user}                 │
                              └──────────────────────────────┘

  ─────────── any subsequent request to a /api/* endpoint ───────────

  GET /api/libraries                 Cookie: mkt_sess=<token>
                                              │
                                              ▼
                              ┌──────────────────────────────┐
                              │ middleware.WebSession        │
                              │  raw = cookie("mkt_sess")    │
                              │  if raw == "":               │
                              │     hint cookies-missing,    │
                              │     →401 (no clear)          │
                              │  th = sha256(raw)            │
                              │  row = SELECT … WHERE        │
                              │        session_token_hash=th │
                              │        AND revoked_at IS NULL│
                              │        AND expires_at > now()│
                              │  if !row:                    │
                              │     clear cookie, → 401      │← D7
                              │  ctx = WithUser(ctx, row.uid)│
                              │  if now()-last_seen >= 1min: │← D6
                              │     UPDATE last_seen_at=now()│
                              │  next.ServeHTTP(w, r.With(ctx))
                              └──────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout (Go)

```
apps/api/internal/auth/
├── web/
│   ├── handler.go              # POST /api/auth/login, dispatch on X-Maktaba-Client
│   ├── handler_test.go
│   ├── middleware.go           # WebSession middleware (D6, D7)
│   ├── repo.go                 # sqlc-generated wrappers
│   ├── repo_extra.go           # tx helpers
│   ├── token.go                # rand32, base64url, sha256 helpers (D2, D3)
│   ├── token_test.go
│   ├── routes.go
│   └── queries/
│       └── web_sessions.sql
└── authctx/
    ├── ctx.go                  # WithUser, MustUser, FromContext  (already exists if not, add here)
    └── ctx_test.go
```

### 2.2 Schema migration — `web_sessions`

```sql
-- shared/db/migrations/0041_web_sessions.sql
BEGIN;

CREATE TABLE web_sessions (
    id                  UUID PRIMARY KEY,                        -- uuid v7
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_token_hash  BYTEA NOT NULL,                          -- sha256(cookie)  (D3)
    csrf_token          TEXT NOT NULL,                           -- base64url, plaintext
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    ip                  INET,
    user_agent          TEXT,
    revoked_at          TIMESTAMPTZ,
    CONSTRAINT web_sessions_token_hash_unique UNIQUE (session_token_hash),
    CONSTRAINT web_sessions_token_hash_len    CHECK (octet_length(session_token_hash) = 32)
);

-- O(1) lookup on every authenticated request (the dominant query).
-- (Already covered by the UNIQUE constraint's index.)

-- Active-sessions list per user — supports DELETE /api/users/{id}/sessions/{sid}
CREATE INDEX web_sessions_user_active
    ON web_sessions (user_id) WHERE revoked_at IS NULL;

-- Reaper sweep ("delete expired & revoked older than N days").
CREATE INDEX web_sessions_reaper
    ON web_sessions (expires_at) WHERE revoked_at IS NULL;

COMMIT;
```

### 2.3 Token helpers (`internal/auth/web/token.go`)

```go
// Package web — session-token primitives.
//
// The cookie carries a 32-byte random value, base64url-encoded (no padding).
// Postgres stores the SHA-256 of the value, never the plaintext (D2, D3).
package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const tokenBytes = 32

var ErrEmptyToken = errors.New("web: empty session token")

func newRawToken() ([]byte, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func encodeCookie(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCookie(s string) ([]byte, error) {
	if s == "" {
		return nil, ErrEmptyToken
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// hashToken — for the BYTEA(32) column. Constant-time for our purposes
// because we look up by exact equality, not compare; the index handles it.
func hashToken(raw []byte) []byte {
	h := sha256.Sum256(raw)
	return h[:]
}

// newCSRFToken returns a fresh base64url string for the mkt_csrf cookie.
func newCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
```

### 2.4 sqlc queries (`web_sessions.sql`)

```sql
-- name: InsertWebSession :one
INSERT INTO web_sessions (id, user_id, session_token_hash, csrf_token,
                          expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at, expires_at;

-- name: GetActiveWebSessionByTokenHash :one
SELECT s.id, s.user_id, s.csrf_token, s.last_seen_at, s.expires_at,
       u.username, u.is_admin
  FROM web_sessions s
  JOIN users u ON u.id = s.user_id
 WHERE s.session_token_hash = $1
   AND s.revoked_at IS NULL
   AND s.expires_at > now();

-- name: BumpWebSessionLastSeen :exec
UPDATE web_sessions SET last_seen_at = now()
 WHERE id = $1 AND last_seen_at < now() - interval '1 minute';

-- name: RevokeWebSessionByTokenHash :exec
UPDATE web_sessions SET revoked_at = now()
 WHERE session_token_hash = $1 AND revoked_at IS NULL;
```

### 2.5 Login handler (`internal/auth/web/handler.go`)

```go
package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"maktaba/apps/api/internal/auth/password"
	"maktaba/apps/api/internal/auth/user/repo"
	"maktaba/apps/api/internal/problem"
)

type Config struct {
	WebSessionTTL    time.Duration // 28d default
	MinResponseDelay time.Duration // 500ms (D5)
	CookieDomain     string        // empty for host-only
	CookieSecure     bool          // true in prod
	PasswordParams   password.Params
}

type Handler struct {
	pool   PoolBegin
	users  *repo.Queries
	web    *Queries // package-local
	cfg    Config
	native NativeIssuer // Plan 10.3
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer h.padTo(start) // D5

	var body loginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem.Write(w, 400, "invalid-body", "request body is not valid JSON")
		return
	}

	// D1 dispatch: native flow handed off to JWT issuer (Plan 10.3).
	if r.Header.Get("X-Maktaba-Client") == "native" {
		h.native.IssueLogin(w, r, body.Username, body.Password, start)
		return
	}

	// 1. Look up user; collapse "no such user" and "wrong password" into one path (AC-3).
	user, err := h.users.GetUserByLowerUsername(r.Context(), body.Username)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		problem.Write(w, 500, "internal", "")
		return
	}
	verifyErr := password.ErrInvalidCredentials
	if err == nil {
		verifyErr = password.Verify(body.Password, user.PwHash)
	}
	if verifyErr != nil {
		problem.Write(w, 401, "invalid-credentials", "username or password is incorrect")
		return
	}

	// 2. Mint a session.
	rawToken, err := newRawToken()
	if err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	csrf, err := newCSRFToken()
	if err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	sid := uuid.Must(uuid.NewV7())
	exp := time.Now().Add(h.cfg.WebSessionTTL)
	_, err = h.web.InsertWebSession(r.Context(), InsertWebSessionParams{
		ID: sid, UserID: user.ID,
		SessionTokenHash: hashToken(rawToken),
		CsrfToken:        csrf,
		ExpiresAt:        exp,
		Ip:               clientIP(r),
		UserAgent:        truncate(r.UserAgent(), 512),
	})
	if err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}

	// 3. Set cookies (D4).
	setSessionCookie(w, encodeCookie(rawToken), h.cfg, exp)
	setCSRFCookie(w, csrf, h.cfg, exp)

	// 4. Body.
	writeJSON(w, 200, map[string]any{
		"user": map[string]any{
			"id": user.ID, "username": user.Username, "is_admin": user.IsAdmin,
		},
	})
}

// padTo sleeps until at least cfg.MinResponseDelay has elapsed since start (D5).
func (h *Handler) padTo(start time.Time) {
	left := h.cfg.MinResponseDelay - time.Since(start)
	if left > 0 {
		time.Sleep(left)
	}
}

func setSessionCookie(w http.ResponseWriter, value string, cfg Config, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     "mkt_sess",
		Value:    value,
		Path:     "/",
		Domain:   cfg.CookieDomain,
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
		Secure:   cfg.CookieSecure,
		HttpOnly: true,                // D4
		SameSite: http.SameSiteLaxMode,
	})
}

func setCSRFCookie(w http.ResponseWriter, value string, cfg Config, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     "mkt_csrf",
		Value:    value,
		Path:     "/",
		Domain:   cfg.CookieDomain,
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
		Secure:   cfg.CookieSecure,
		HttpOnly: false,               // D4 — SPA reads it
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, cfg Config) {
	http.SetCookie(w, &http.Cookie{
		Name: "mkt_sess", Value: "", Path: "/",
		MaxAge: -1, Secure: cfg.CookieSecure, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
```

### 2.6 Session middleware (`internal/auth/web/middleware.go`)

```go
package web

import (
	"net/http"

	"maktaba/apps/api/internal/auth/authctx"
	"maktaba/apps/api/internal/problem"
)

// WebSession loads the user from mkt_sess and bumps last_seen_at (debounced, D6).
// Failures clear the cookie (D7) and return 401.
func (h *Handler) WebSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("mkt_sess")
		if err != nil || c.Value == "" {
			w.Header().Set("Maktaba-Hint", "cookies-missing-check-proxy")
			problem.Write(w, 401, "unauthenticated", "")
			return
		}
		raw, err := decodeCookie(c.Value)
		if err != nil {
			clearSessionCookie(w, h.cfg)
			problem.Write(w, 401, "unauthenticated", "")
			return
		}
		row, err := h.web.GetActiveWebSessionByTokenHash(r.Context(), hashToken(raw))
		if err != nil {
			clearSessionCookie(w, h.cfg)
			problem.Write(w, 401, "unauthenticated", "")
			return
		}
		// D6 — debounced bump; UPDATE no-ops if last_seen_at < 1 minute old.
		_ = h.web.BumpWebSessionLastSeen(r.Context(), row.ID)

		ctx := authctx.WithUser(r.Context(), authctx.User{
			ID: row.UserID, Username: row.Username, IsAdmin: row.IsAdmin,
			SessionID: row.ID, CSRFToken: row.CsrfToken, // 10.10 reads CSRFToken
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

### 2.7 Routing

```go
// internal/auth/web/routes.go
func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/auth/login", h.Login)
}
```

The `Mount` for protected routes uses `r.Use(h.WebSession)` higher in the
tree (`internal/server/server.go` adds it before the user/library
routers). Story 10.10 will sandwich `r.Use(h.CSRFGuard)` under `WebSession`.

---

## 3. File-by-file scaffolding

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0041_web_sessions.sql` | `web_sessions` table + `*_token_hash_unique`, `*_user_active`, `*_reaper` indexes | `TestMigrationCreatesWebSessions` |
| 2 | `apps/api/internal/auth/authctx/ctx.go` | `User` struct, `WithUser`, `MustUser`, `FromContext` | `TestAuthCtxRoundtrip` |
| 3 | `apps/api/internal/auth/web/token.go` | `newRawToken`, `encodeCookie`, `decodeCookie`, `hashToken`, `newCSRFToken` | `TestRoundTripCookieEncoding`, `TestHashIsDeterministic` |
| 4 | `apps/api/internal/auth/web/queries/web_sessions.sql` | sqlc inputs (4 queries) | (n/a) |
| 5 | `apps/api/internal/auth/web/repo.go` | `Queries.InsertWebSession`, `.GetActiveWebSessionByTokenHash`, `.BumpWebSessionLastSeen`, `.RevokeWebSessionByTokenHash` | `TestRepoInsertAndLookup` |
| 6 | `apps/api/internal/auth/web/handler.go` | `Handler`, `Config`, `Login`, `padTo`, `setSessionCookie`, `setCSRFCookie`, `clearSessionCookie` | `TestLogin*` |
| 7 | `apps/api/internal/auth/web/middleware.go` | `Handler.WebSession` | `TestWebSession*` |
| 8 | `apps/api/internal/auth/web/routes.go` | `Handler.Mount` | wired in `cmd/maktaba-api/serve.go` |

---

## 4. Test cases — keyed to ACs

```go
// AC-1: full login flow sets both cookies with expected attributes.
func TestLoginSetsCookiesAndBody(t *testing.T) {
	srv := newTestServer(t)
	srv.seedUser(t, "alice", "hunter2", false)

	resp := srv.do(t, "POST", "/api/auth/login",
		`{"username":"alice","password":"hunter2"}`, nil)
	require.Equal(t, 200, resp.StatusCode)
	cs := resp.Cookies()
	sess := findCookie(cs, "mkt_sess")
	require.NotNil(t, sess)
	require.True(t, sess.HttpOnly)
	require.True(t, sess.Secure)
	require.Equal(t, http.SameSiteLaxMode, sess.SameSite)
	csrf := findCookie(cs, "mkt_csrf")
	require.NotNil(t, csrf)
	require.False(t, csrf.HttpOnly) // SPA reads it (D4)
	var body struct{ User struct{ ID, Username string; IsAdmin bool } `json:"user"` }
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "alice", body.User.Username)
}

// AC-2: authenticated request loads user & bumps last_seen_at (debounced).
func TestSessionMiddlewareLoadsUserAndDebouncesLastSeen(t *testing.T) {
	srv := newTestServer(t)
	cookie := srv.loginAs(t, "alice", "hunter2")

	resp := srv.do(t, "GET", "/api/whoami", "", cookie)
	require.Equal(t, 200, resp.StatusCode)
	first := srv.queryLastSeen(t, cookie)

	srv.do(t, "GET", "/api/whoami", "", cookie) // immediate replay
	second := srv.queryLastSeen(t, cookie)
	require.True(t, second.Equal(first), "debounced: no bump within 1 minute (D6)")

	// Move time forward and try again.
	srv.advanceClock(t, 90*time.Second)
	srv.do(t, "GET", "/api/whoami", "", cookie)
	third := srv.queryLastSeen(t, cookie)
	require.True(t, third.After(first))
}

// AC-3: invalid credentials → 401 + 500ms minimum response time.
func TestLoginConstantTimeOnUserNotFound(t *testing.T) {
	srv := newTestServer(t)
	srv.seedUser(t, "alice", "hunter2", false)

	tStart := time.Now()
	resp := srv.do(t, "POST", "/api/auth/login",
		`{"username":"nope","password":"x"}`, nil)
	wallNo := time.Since(tStart)
	require.Equal(t, 401, resp.StatusCode)

	tStart = time.Now()
	resp = srv.do(t, "POST", "/api/auth/login",
		`{"username":"alice","password":"wrong"}`, nil)
	wallWrong := time.Since(tStart)
	require.Equal(t, 401, resp.StatusCode)

	// Both within (500ms, 600ms]; variance ≤ 50ms (D5).
	require.GreaterOrEqual(t, wallNo, 500*time.Millisecond)
	require.GreaterOrEqual(t, wallWrong, 500*time.Millisecond)
	delta := wallWrong - wallNo
	if delta < 0 { delta = -delta }
	require.LessOrEqual(t, delta, 50*time.Millisecond)
}

// AC-4: tampered cookie → 401 + Set-Cookie clears the cookie.
func TestSessionMiddlewareClearsTamperedCookie(t *testing.T) {
	srv := newTestServer(t)
	cookie := srv.loginAs(t, "alice", "hunter2")
	cookie.Value = cookie.Value[:len(cookie.Value)-1] + "Z" // flip last char

	resp := srv.do(t, "GET", "/api/whoami", "", cookie)
	require.Equal(t, 401, resp.StatusCode)
	cs := resp.Cookies()
	cleared := findCookie(cs, "mkt_sess")
	require.NotNil(t, cleared)
	require.Equal(t, -1, cleared.MaxAge) // cleared (D7)
	require.Equal(t, "", cleared.Value)
}

// AC-4: expired session row → 401 + cookie cleared.
func TestSessionMiddlewareExpiredSession(t *testing.T) {
	srv := newTestServer(t)
	cookie := srv.loginAs(t, "alice", "hunter2")
	srv.expireSessionNow(t, cookie)
	resp := srv.do(t, "GET", "/api/whoami", "", cookie)
	require.Equal(t, 401, resp.StatusCode)
	require.NotNil(t, findCookie(resp.Cookies(), "mkt_sess"))
}

// EC: cookies stripped by reverse proxy → 401 + hint header.
func TestLoginNoCookieHasHint(t *testing.T) {
	srv := newTestServer(t)
	resp := srv.do(t, "GET", "/api/whoami", "", nil)
	require.Equal(t, 401, resp.StatusCode)
	require.Equal(t, "cookies-missing-check-proxy", resp.Header.Get("Maktaba-Hint"))
}

// D1: the same endpoint dispatches to native flow when header is set.
func TestLoginNativeHeaderRoutesToJWTFlow(t *testing.T) {
	srv := newTestServer(t)
	srv.seedUser(t, "alice", "hunter2", false)
	resp := srv.do(t, "POST", "/api/auth/login",
		`{"username":"alice","password":"hunter2"}`, nil,
		http.Header{"X-Maktaba-Client": []string{"native"}})
	require.Equal(t, 200, resp.StatusCode)
	require.Empty(t, resp.Cookies())            // no cookies for native
	require.Contains(t, bodyString(resp), `"access_token"`)
	require.Contains(t, bodyString(resp), `"refresh_token"`)
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **No cookie at all** (proxy stripped). | Middleware emits `Maktaba-Hint: cookies-missing-check-proxy` and 401. (`TestLoginNoCookieHasHint`) |
| E2  | **Tampered cookie value** (1-char flip). | `decodeCookie` fails OR `GetActiveWebSessionByTokenHash` returns 0 rows; middleware clears cookie + 401. (`TestSessionMiddlewareClearsTamperedCookie`) |
| E3  | **Multiple browser tabs share one cookie**. | Single row in `web_sessions`; logout in one tab calls `RevokeWebSessionByTokenHash` (Story 10.5) and the other tab fails its next request → 401 + cookie cleared. |
| E4  | **GET cross-site nav from a deep link.** | `SameSite=Lax` allows GET top-level navigation; the request gets the cookie and the server treats it as an authenticated GET. (Browser-level, no code.) |
| E5  | **POST cross-site request from another origin.** | `SameSite=Lax` strips the cookie; the API sees an unauthenticated POST → 401. Story 10.10 layers double-submit on top. |
| E6  | **Session race: two parallel requests, one logging out.** | Logout (10.5) runs `UPDATE … SET revoked_at` in one statement; the other request's `GetActiveWebSessionByTokenHash` filter `revoked_at IS NULL` excludes the row at most one request later. No torn state. |
| E7  | **`last_seen_at` UPDATE races within 1 min** between two replicas. | Idempotent UPDATE — both writes set the same value within milliseconds; the WHERE clause `last_seen_at < now() - interval '1 minute'` ensures one of the two no-ops. (D6) |
| E8  | **User is deleted while their session is alive.** | FK cascade `users → web_sessions` deletes the row; next request → 401 + cookie cleared. |
| E9  | **Native client missing the header.** | Falls into web flow (D1); the client gets cookies it ignores. Story 10.3 EC documents this is acceptable. |
| E10 | **Login body missing fields**. | `json.Decoder` accepts; missing `password` then makes `Verify` fail with `ErrInvalidCredentials`; 401 after the 500 ms padding (no enumeration leak). |
| E11 | **Two concurrent logins for the same user**. | Each insert generates a fresh UUID v7 and a fresh random token; both rows coexist. The user can have N active sessions; admin can revoke individually via Plan 10.1's `DELETE /api/users/{id}/sessions/{sid}`. |
| E12 | **Reverse proxy rewrites `Set-Cookie` `Domain`.** | We default to host-only (no `Domain` attribute). If an operator must set a domain, `[server] cookie_domain` in `api.toml` plumbs through `Config.CookieDomain`; the middleware doesn't care because it reads from `Cookie:` not `Set-Cookie:`. |
| E13 | **Clock skew between API replicas**. | `expires_at > now()` uses Postgres time; both replicas agree because Postgres is the only clock that matters for session validity. |
| E14 | **Session cookie collision (sha256 of two distinct random 32-byte strings)**. | UNIQUE constraint causes INSERT to fail; handler returns 500 and the operator's monitoring catches it. Probability is `2^-128` — never observed in practice. |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `shared/db/migrations/0041_web_sessions.sql` creates `web_sessions(id UUID PK, user_id, session_token_hash BYTEA UNIQUE, csrf_token TEXT, created_at, last_seen_at, expires_at, ip, user_agent, revoked_at)` plus the `_user_active` and `_reaper` partial indexes. (`TestMigrationCreatesWebSessions`)
- [ ] **A2** `POST /api/auth/login` with valid credentials returns 200, sets `mkt_sess` (HttpOnly+Secure+Lax) and `mkt_csrf` (Secure+Lax, NOT HttpOnly), and the body is `{user: {id, username, is_admin}}`. (`TestLoginSetsCookiesAndBody`)
- [ ] **A3** The `mkt_sess` cookie value is the base64url of a fresh 32-byte random; the DB column `session_token_hash` is the SHA-256 of that value. (`TestRoundTripCookieEncoding`, `TestHashIsDeterministic`, `TestRepoInsertAndLookup`)
- [ ] **A4** A subsequent request with `Cookie: mkt_sess=...` is authenticated as the same user; `MustUser(ctx)` returns the right id. (`TestSessionMiddlewareLoadsUserAndDebouncesLastSeen`)
- [ ] **A5** `last_seen_at` is bumped at most once per minute per session; rapid-fire requests do not amplify writes. (`TestSessionMiddlewareLoadsUserAndDebouncesLastSeen`)
- [ ] **A6** `POST /api/auth/login` with wrong password OR unknown username takes ≥ 500 ms wall time; the two paths differ by ≤ 50 ms. (`TestLoginConstantTimeOnUserNotFound`)
- [ ] **A7** Both failure modes return `401 Unauthorized` problem+json with `type: invalid-credentials`. (`TestLoginConstantTimeOnUserNotFound`)
- [ ] **A8** Tampered or unknown `mkt_sess` → 401 and `Set-Cookie: mkt_sess=; Max-Age=0`. (`TestSessionMiddlewareClearsTamperedCookie`)
- [ ] **A9** Expired session row → 401 + cookie cleared. (`TestSessionMiddlewareExpiredSession`)
- [ ] **A10** Missing `Cookie:` header sets `Maktaba-Hint: cookies-missing-check-proxy` on the 401 response. (`TestLoginNoCookieHasHint`)
- [ ] **A11** `X-Maktaba-Client: native` on `POST /api/auth/login` dispatches to the JWT flow (Plan 10.3) and does NOT set cookies. (`TestLoginNativeHeaderRoutesToJWTFlow`)
- [ ] **A12** `csrf_token` is stored in the row and returned to the SPA via the `mkt_csrf` cookie; the middleware places it on `authctx.User.CSRFToken` for Story 10.10's guard to consume. (Smoke test on `MustUser(ctx).CSRFToken != ""` in `TestSessionMiddlewareLoadsUserAndDebouncesLastSeen`)
- [ ] **A13** Cookie attributes default to `Secure: cfg.CookieSecure (true in prod), SameSite=Lax, Path=/, MaxAge=auth.web_session_ttl_sec`. (`TestLoginSetsCookiesAndBody` covers attributes; config-loading test covers TTL.)
- [ ] **A14** Logout (Story 10.5) calls `RevokeWebSessionByTokenHash` and the very next request from the same browser is 401 + cookie cleared. (Cross-story: tested when 10.5 lands.)
- [ ] **A15** No code path logs the raw cookie value, the SHA-256 hash, or the password. (`TestLoginNeverLogsSecrets`, log-output assertion across success + failure paths.)
