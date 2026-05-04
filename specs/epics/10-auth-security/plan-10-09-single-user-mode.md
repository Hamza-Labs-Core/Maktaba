# Plan 10.9 — Single-user mode (admin token bypass) — implementation

> Implementation plan for [story-10-09-single-user-mode.md](story-10-09-single-user-mode.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on the sentinel UUID seeded by
> [Story 10.1](story-10-01-user-store.md) (`00000000-0000-0000-0000-000000000001`),
> the JWT minter from [Story 10.8](story-10-08-signed-url-minter.md), and
> the audit-log writer from [Story 10.16](story-10-16-security-audit.md).
> Web cookie semantics align with [Story 10.2](story-10-02-web-login.md).
> The UI bootstrap dialog (AC-3) is **deferred to Epic 11** — this plan
> ships only the API-side middleware and the contract the SPA will use.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Header AND cookie are accepted; header wins on conflict.** The middleware reads `Authorization: Bearer <tok>` first; if absent, falls back to cookie `mkt_admin_token`. Both populated with different values → header wins, cookie ignored. | Story AC-1: "carries `Authorization: Bearer <that-token>` (or cookie `mkt_admin_token=<that-token>`)" | Header is the explicit, native-client path; cookie is the SPA fallback (browsers can't add `Authorization` cross-origin without preflight). Header-first matches OAuth-bearer convention and removes ambiguity when both happen to be set. |
| D2 | **No-op when `MAKTABA_ADMIN_TOKEN` is empty or unset.** The middleware short-circuits to `next.ServeHTTP` with no context mutation; the regular auth path (cookie-session or bearer-JWT) handles the request. | Story AC-1 ("Given `MAKTABA_ADMIN_TOKEN` is set") + test: "empty env var → no bypass" | A blank-token bypass would be catastrophic (an attacker submitting an empty Bearer header could match). The empty-string check is **before** any compare, and the constant-time compare is **only** called when both sides are non-empty. |
| D3 | **Boot-time length check refuses startup on `< 32` chars.** The API process exits 1 with `error: admin-token-too-short (got N chars, need ≥32)` written to stderr/stdout. | Story edge case: "A weak admin token (e.g. 8 chars) — refused at boot with `error: admin-token-too-short` (require ≥32 chars)" | Failing-loud at boot is far better than silently accepting weak tokens. 32 chars is the minimum to make brute-force impractical (`base64.RawURLEncoding(24 random bytes)` produces 32 chars; that's the documented operator workflow). |
| D4 | **Constant-time compare via `crypto/subtle.ConstantTimeCompare` after a length-equality short-circuit using a constant-time mask.** `subtle.ConstantTimeCompare` itself returns 0 on length mismatch in O(1), but the `len()` check leaks whether the candidate has a different length. We mitigate by using `subtle.ConstantTimeEq(int32(len(a)), int32(len(b)))` AND `subtle.ConstantTimeCompare(padded_a, padded_b)` with HMAC-padded buffers. | Story AC-2: "constant-time equality (no early exit on length or content)" | Strict reading of "no early exit on length" requires hashing or padding both sides. We use `hmac.Equal` on the SHA-256 of both tokens — the expected and the candidate — which is the standard Go pattern: same-length, content-uniform, no leaks. Implementation uses `hmac.Equal(sha256(expected), sha256(candidate))`. |
| D5 | **Synthetic admin user is the sentinel UUID `00000000-0000-0000-0000-000000000001`** seeded by Epic 10 README schema. The middleware sets `ctx.User = AdminSentinel{}` which carries this UUID; downstream handlers and the audit logger see a real user record (`is_admin=true`) so foreign-key references in `audit_log.actor_user_id` resolve. | Story AC-1, README "Sentinel for the single-user/admin-token bypass path" | Reusing a seeded row instead of a NULL-actor pattern keeps the audit and FK invariants simple. The row's password hash is `<unsalted-disabled>` so the regular login path can never produce a session for the sentinel — only the admin-token middleware can. |
| D6 | **JWT minted via this path carries `lib = ALL_LIBRARY_IDS`.** When an internal background task (e.g., re-index, signed-URL minter) needs a JWT under the admin-token identity, the minter calls `LibraryRepo.AllIDs(ctx)` and stuffs the result into `lib[]` and sets `is_admin=true`. The `usr` claim is the sentinel UUID. | Story AC-5 | The admin sentinel must have full read access by definition — it's the operator-of-last-resort. Materializing the full library list at mint time keeps the offline-verifying Streaming Service's authorization check cheap (Story 10.7 AC-1: it doesn't have to special-case admin). |

If D4 is rejected (use plain `subtle.ConstantTimeCompare` on raw bytes):
length leak via timing is real but extremely small (hundreds of nanoseconds);
the practical threat model (operator-set token in a self-hosted deploy)
makes this acceptable. We still recommend the HMAC-equal path because
it's cheap and standard.

If D3 is rejected (allow short tokens with a warning): operators will
ignore the warning; weak tokens become a security hole. Boot-failure
is the only enforcement that works.

---

## 1. Architecture diagram — admin-token middleware in the chi chain

```
   Incoming HTTP request
              │
              ▼
   ┌──────────────────────────────────────────────────────┐
   │  chi router root                                     │
   │   r.Use(middleware.RequestID)                        │
   │   r.Use(middleware.RealIP)                           │
   │   r.Use(middleware.Logger)                           │
   │   r.Use(middleware.Recoverer)                        │
   │   r.Use(transportsec.HSTS) // Story 10.15            │
   │   r.Use(admintoken.Middleware(cfg))  // THIS STORY   │
   │   r.Use(csrf.Middleware(cfg))        // Story 10.10  │
   │   r.Use(session.Middleware(cfg))     // Story 10.2/3 │
   └──────────────────────────────┬───────────────────────┘
                                  │
              ┌───────────────────┴───────────────────┐
              │                                       │
   admin token configured AND                  admin token absent OR
   matching credential present                 candidate token mismatch
              │                                       │
              ▼                                       ▼
   ┌──────────────────────────────┐    ┌─────────────────────────────┐
   │ ctx = WithUser(ctx,          │    │ next.ServeHTTP(w, r)         │
   │   AdminSentinel{             │    │ (cookie/JWT path runs next)  │
   │     ID: SentinelUUID,        │    └─────────────────────────────┘
   │     IsAdmin: true,           │
   │   })                         │
   │ next.ServeHTTP(w, r.WithCtx) │
   └──────────────────────────────┘
              │
              ▼
   Handler runs as the sentinel admin.
   Audit rows: actor_user_id = SentinelUUID.
   Internal JWT minter (Story 10.8):
     usr = SentinelUUID
     is_admin = true
     lib  = LibraryRepo.AllIDs(ctx)
```

The middleware is a **read-only** consumer of the env var (loaded once
at boot) and a **context writer**. It does not touch the DB on the hot
path. The DB is only touched at boot (validate token length and verify
the sentinel row exists) and at audit time (which goes through the
existing audit writer).

---

## 2. Detailed implementation

### 2.1 Package layout — Go (API Service)

```
api/
├── internal/
│   ├── auth/
│   │   ├── admintoken/
│   │   │   ├── middleware.go        # public: Middleware(cfg) func(http.Handler) http.Handler
│   │   │   ├── config.go            # Config struct loaded from env at boot
│   │   │   ├── sentinel.go          # SentinelUUID const + AdminSentinel struct
│   │   │   ├── compare.go           # ctEquals via hmac.Equal(sha256, sha256) — D4
│   │   │   └── middleware_test.go
│   │   ├── ctxuser/                 # context key + accessor; shared with cookie/JWT paths
│   │   │   └── ctxuser.go
│   │   └── ...                      # csrf/, session/, lockout/, ratelimit/ from sibling stories
│   ├── librepo/
│   │   └── all_ids.go               # LibraryRepo.AllIDs — used by the JWT minter (D6)
│   └── server/
│       └── routes.go                # chi router; r.Use(admintoken.Middleware(...))
└── cmd/api/
    └── main.go                      # boot: validate token length; refuse start on < 32
```

### 2.2 Config struct + boot validation (D2, D3)

```go
// api/internal/auth/admintoken/config.go
package admintoken

import (
    "errors"
    "fmt"
    "os"
)

const (
    EnvVar          = "MAKTABA_ADMIN_TOKEN"
    MinTokenLen     = 32
    HeaderName      = "Authorization"
    HeaderPrefix    = "Bearer "
    CookieName      = "mkt_admin_token"
)

// ErrTokenTooShort is returned by LoadConfig when the env var is set but
// shorter than MinTokenLen. The API process should exit on this error.
var ErrTokenTooShort = errors.New("admin-token-too-short")

// Config carries the loaded admin token (or empty string for "disabled").
// Loaded once at boot; never re-read.
type Config struct {
    Token string // empty = middleware is a no-op
}

// LoadConfig reads MAKTABA_ADMIN_TOKEN. Returns Config{Token: ""} for
// unset/empty, an ErrTokenTooShort wrapped error for too-short tokens,
// and Config{Token: <value>} for a valid token.
func LoadConfig() (Config, error) {
    raw := os.Getenv(EnvVar)
    if raw == "" {
        return Config{}, nil
    }
    if len(raw) < MinTokenLen {
        return Config{}, fmt.Errorf(
            "%w: got %d chars, need >=%d", ErrTokenTooShort, len(raw), MinTokenLen)
    }
    return Config{Token: raw}, nil
}
```

### 2.3 Constant-time compare (D4)

```go
// api/internal/auth/admintoken/compare.go
package admintoken

import (
    "crypto/hmac"
    "crypto/sha256"
)

// ctEquals returns true iff a == b without leaking length or content
// timing. We hash both sides under the same algorithm and compare the
// fixed-size digests with hmac.Equal (constant-time over equal-length
// inputs). The hashing eliminates the length-leak from a raw
// subtle.ConstantTimeCompare on different-length inputs.
//
// Note: SHA-256 is used as a constant-time-equal helper; we are not
// hashing for password storage. The expected token is operator-set and
// already trusted; this is a side-channel mitigation only.
func ctEquals(expected, candidate string) bool {
    if expected == "" {
        return false // never match an empty configured token
    }
    eh := sha256.Sum256([]byte(expected))
    ch := sha256.Sum256([]byte(candidate))
    return hmac.Equal(eh[:], ch[:])
}
```

### 2.4 Sentinel + context key

```go
// api/internal/auth/admintoken/sentinel.go
package admintoken

import "github.com/google/uuid"

// SentinelUUID matches the row seeded in the users table by the Epic 10
// README schema. Cross-referenced by Epic 4 NFR Story 19.8 and audit
// rows under the admin-token path.
var SentinelUUID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type AdminSentinel struct {
    ID       uuid.UUID
    Username string // "admin"
    IsAdmin  bool   // always true
}

func newSentinel() AdminSentinel {
    return AdminSentinel{ID: SentinelUUID, Username: "admin", IsAdmin: true}
}
```

### 2.5 Middleware — chi handler-of-handlers

```go
// api/internal/auth/admintoken/middleware.go
package admintoken

import (
    "log/slog"
    "net/http"
    "strings"

    "github.com/maktaba/api/internal/auth/ctxuser"
)

// Middleware returns a chi-compatible middleware that, when the env-var
// token is configured AND the request carries a matching credential
// (header or cookie), attaches the sentinel admin to the request
// context. Otherwise, it is a pass-through (the cookie/JWT middleware
// downstream handles auth).
//
// Behavior matrix:
//   cfg.Token == ""                   -> pass-through (no-op)
//   no Bearer header AND no cookie    -> pass-through
//   Bearer header present             -> use header value
//   else cookie present               -> use cookie value
//   ctEquals(cfg.Token, candidate)    -> attach AdminSentinel to ctx
//   mismatch                          -> pass-through (rely on downstream)
func Middleware(cfg Config) func(http.Handler) http.Handler {
    if cfg.Token == "" {
        // D2: no-op when unset/empty.
        return func(next http.Handler) http.Handler { return next }
    }
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            cand := candidateFromRequest(r)
            if cand == "" {
                next.ServeHTTP(w, r)
                return
            }
            if !ctEquals(cfg.Token, cand) {
                // Important: do NOT 401 here. A wrong admin token might
                // accompany a valid cookie session (e.g., mis-set cookie
                // from a previous deploy). Let downstream auth decide.
                slog.Debug("admintoken_candidate_mismatch")
                next.ServeHTTP(w, r)
                return
            }
            sent := newSentinel()
            ctx := ctxuser.WithUser(r.Context(), ctxuser.User{
                ID: sent.ID, Username: sent.Username, IsAdmin: sent.IsAdmin,
                Source: ctxuser.SourceAdminToken,
            })
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

func candidateFromRequest(r *http.Request) string {
    // D1: header wins.
    if h := r.Header.Get(HeaderName); strings.HasPrefix(h, HeaderPrefix) {
        return strings.TrimPrefix(h, HeaderPrefix)
    }
    if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
        return c.Value
    }
    return ""
}
```

### 2.6 Wire-up in `cmd/api/main.go`

```go
// api/cmd/api/main.go (excerpt)
adminCfg, err := admintoken.LoadConfig()
if err != nil {
    // D3: refuse to start on too-short.
    fmt.Fprintln(os.Stderr, "fatal:", err)
    os.Exit(1)
}
if adminCfg.Token != "" {
    slog.Info("admin_token_enabled", "len", len(adminCfg.Token))
} else {
    slog.Info("admin_token_disabled")
}

r := chi.NewRouter()
r.Use(chi_middleware.RequestID, chi_middleware.RealIP, chi_middleware.Logger, chi_middleware.Recoverer)
r.Use(transportsec.HSTS) // Story 10.15
r.Use(admintoken.Middleware(adminCfg))
r.Use(csrf.Middleware(csrfCfg))           // Story 10.10
r.Use(session.Middleware(sessCfg))        // Story 10.2/3
mountRoutes(r, deps)
```

### 2.7 JWT minter integration (D6)

```go
// api/internal/auth/jwtmint/mint.go (excerpt — the part that depends on this story)
func (m *Minter) MintAPIInternal(ctx context.Context, actor ctxuser.User) (string, error) {
    libs := []uuid.UUID(nil)
    if actor.Source == ctxuser.SourceAdminToken {
        // D6: admin sentinel gets ALL libraries.
        ids, err := m.libRepo.AllIDs(ctx)
        if err != nil { return "", err }
        libs = ids
    } else {
        ids, err := m.libRepo.IDsForUser(ctx, actor.ID)
        if err != nil { return "", err }
        libs = ids
    }
    return m.signClaims(Claims{
        Iss: "maktaba", Aud: "api", Sub: actor.ID.String(),
        Iat: time.Now().Unix(), Exp: time.Now().Add(15 * time.Minute).Unix(),
        Jti: uuid.NewString(), Kid: m.activeKid,
        Usr: actor.ID.String(), Lib: libs, IsAdmin: actor.IsAdmin,
    })
}
```

### 2.8 SQL — confirm sentinel row exists at boot

Already shipped by Story 10.1 README. This story adds a defensive
existence-check at API boot:

```sql
-- api/internal/auth/admintoken/queries.sql
-- name: SentinelExists :one
SELECT EXISTS(SELECT 1 FROM users WHERE id = '00000000-0000-0000-0000-000000000001');
```

Boot fails-loud with `error: admin-sentinel-row-missing` if FALSE,
guiding the operator to re-run migrations.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `api/internal/auth/admintoken/config.go` | `EnvVar`, `MinTokenLen`, `HeaderName/Prefix`, `CookieName`, `ErrTokenTooShort`, `Config`, `LoadConfig` | `TestLoadConfig_*` |
| 2 | `api/internal/auth/admintoken/sentinel.go` | `SentinelUUID`, `AdminSentinel`, `newSentinel` | (covered by middleware tests) |
| 3 | `api/internal/auth/admintoken/compare.go` | `ctEquals` | `TestCtEquals_*` |
| 4 | `api/internal/auth/admintoken/middleware.go` | `Middleware`, `candidateFromRequest` | `TestMiddleware_*` |
| 5 | `api/internal/auth/admintoken/queries.sql` + sqlc generation | `SentinelExists` | `TestSentinelExists` |
| 6 | `api/internal/auth/ctxuser/ctxuser.go` (extend) | `SourceAdminToken` constant added to existing `Source` enum | (smoke import) |
| 7 | `api/cmd/api/main.go` (extend) | call `admintoken.LoadConfig`, wire middleware, exit on `ErrTokenTooShort` | `TestBoot_RefusesShortToken` (integration) |
| 8 | `api/internal/auth/jwtmint/mint.go` (extend) | branch in `MintAPIInternal` for `SourceAdminToken` → `LibraryRepo.AllIDs` | `TestMintAPIInternal_AdminGetsAllLibs` |

No new migrations: the `users` table and the sentinel row are owned by
Story 10.1.

---

## 4. Test cases keyed to acceptance criteria

### 4.1 `TestMiddleware_HeaderBypass` (AC-1)

```go
func TestMiddleware_HeaderBypass(t *testing.T) {
    tok := strings.Repeat("a", 32)
    mw := admintoken.Middleware(admintoken.Config{Token: tok})
    next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        u, ok := ctxuser.FromContext(r.Context())
        require.True(t, ok)
        require.Equal(t, admintoken.SentinelUUID, u.ID)
        require.True(t, u.IsAdmin)
        w.WriteHeader(204)
    })
    h := mw(next)

    req := httptest.NewRequest("GET", "/x", nil)
    req.Header.Set("Authorization", "Bearer "+tok)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 204, rr.Code)
}
```

### 4.2 `TestMiddleware_CookieBypass` (AC-1)

```go
func TestMiddleware_CookieBypass(t *testing.T) {
    tok := strings.Repeat("b", 40)
    mw := admintoken.Middleware(admintoken.Config{Token: tok})
    h := mw(adminAttachedAssertingHandler(t))

    req := httptest.NewRequest("POST", "/api/jobs", nil)
    req.AddCookie(&http.Cookie{Name: "mkt_admin_token", Value: tok})
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 204, rr.Code)
}
```

### 4.3 `TestMiddleware_OneCharDifferent_IsRejected` (AC-2, security test)

```go
func TestMiddleware_OneCharDifferent_IsRejected(t *testing.T) {
    tok := strings.Repeat("a", 32)
    mw := admintoken.Middleware(admintoken.Config{Token: tok})
    h := mw(adminNotAttachedAssertingHandler(t))

    bad := strings.Repeat("a", 31) + "b"
    req := httptest.NewRequest("GET", "/x", nil)
    req.Header.Set("Authorization", "Bearer "+bad)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    // pass-through to next; downstream returns 200 for the asserting handler
    require.Equal(t, 200, rr.Code)
}
```

### 4.4 `TestMiddleware_EmptyEnv_IsNoop` (AC-1, security test)

```go
func TestMiddleware_EmptyEnv_IsNoop(t *testing.T) {
    mw := admintoken.Middleware(admintoken.Config{Token: ""})
    h := mw(adminNotAttachedAssertingHandler(t))

    // Even submitting an empty bearer or random bearer must not bypass.
    for _, cand := range []string{"", "anything", strings.Repeat("z", 64)} {
        req := httptest.NewRequest("GET", "/x", nil)
        req.Header.Set("Authorization", "Bearer "+cand)
        rr := httptest.NewRecorder()
        h.ServeHTTP(rr, req)
        require.Equal(t, 200, rr.Code, "candidate=%q must not bypass", cand)
    }
}
```

### 4.5 `TestLoadConfig_RejectsShort` (AC-edge "weak admin token")

```go
func TestLoadConfig_RejectsShort(t *testing.T) {
    t.Setenv("MAKTABA_ADMIN_TOKEN", "shorty")
    _, err := admintoken.LoadConfig()
    require.ErrorIs(t, err, admintoken.ErrTokenTooShort)
    require.Contains(t, err.Error(), "got 6")
}

func TestLoadConfig_AcceptsLongEnough(t *testing.T) {
    tok := strings.Repeat("k", 32)
    t.Setenv("MAKTABA_ADMIN_TOKEN", tok)
    cfg, err := admintoken.LoadConfig()
    require.NoError(t, err)
    require.Equal(t, tok, cfg.Token)
}
```

### 4.6 `TestCtEquals_*` (AC-2, constant-time)

```go
func TestCtEquals(t *testing.T) {
    ok := strings.Repeat("a", 32)
    require.True(t, ctEqualsExported(ok, ok))
    require.False(t, ctEqualsExported(ok, ok[:31]+"b"))
    require.False(t, ctEqualsExported("", ""))            // empty expected
    require.False(t, ctEqualsExported(ok, ""))            // empty candidate
    require.False(t, ctEqualsExported(ok, ok+"x"))        // different length
}
```

### 4.7 `TestAuditUnderAdminTokenPath` (AC-1, integration)

```go
func TestAuditRowUsesSentinelUUID(t *testing.T) {
    db := openTestDB(t)
    tok := strings.Repeat("c", 32)
    h := buildRouterWithAdminToken(t, db, tok)

    req := httptest.NewRequest("DELETE", "/api/libraries/L1", nil)
    req.Header.Set("Authorization", "Bearer "+tok)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 204, rr.Code)

    var actor uuid.UUID
    require.NoError(t, db.QueryRow(
        "SELECT actor_user_id FROM audit_log WHERE event='library-deleted' ORDER BY at DESC LIMIT 1",
    ).Scan(&actor))
    require.Equal(t, admintoken.SentinelUUID, actor)
}
```

### 4.8 `TestMintAPIInternal_AdminGetsAllLibs` (AC-5)

```go
func TestMintAPIInternal_AdminGetsAllLibs(t *testing.T) {
    libs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
    libRepo := &fakeLibRepo{all: libs}
    m := jwtmint.New(testKey, libRepo)

    sentinel := ctxuser.User{
        ID: admintoken.SentinelUUID, IsAdmin: true,
        Source: ctxuser.SourceAdminToken,
    }
    tok, err := m.MintAPIInternal(context.Background(), sentinel)
    require.NoError(t, err)
    claims := decodeJWT(t, tok)
    require.ElementsMatch(t, libs, claims.Lib)
    require.True(t, claims.IsAdmin)
}
```

### 4.9 `TestTokenRotation_OldRejected` (AC-4)

```go
func TestTokenRotation_OldRejected(t *testing.T) {
    old := strings.Repeat("o", 32)
    new := strings.Repeat("n", 32)
    h := admintoken.Middleware(admintoken.Config{Token: new})(adminNotAttachedAssertingHandler(t))

    req := httptest.NewRequest("GET", "/x", nil)
    req.Header.Set("Authorization", "Bearer "+old)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 200, rr.Code) // pass-through, NOT bypass
}
```

---

## 5. Edge cases and how the plan handles each

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **Both admin token and user table populated.** Both auth paths work; admin-token path always lands on the sentinel admin user. | The middleware short-circuits only when the candidate matches; if it doesn't match, the cookie/JWT middleware downstream runs normally. `TestMiddleware_OneCharDifferent_IsRejected` covers this. |
| E2 | **Empty env var.** `LoadConfig` returns `Config{Token: ""}`; `Middleware` returns a no-op pass-through (D2). Random tokens cannot accidentally match. | `TestMiddleware_EmptyEnv_IsNoop` |
| E3 | **Short token at boot.** `LoadConfig` returns `ErrTokenTooShort` and the API process exits 1 with a clear error message. | `TestLoadConfig_RejectsShort` + integration `TestBoot_RefusesShortToken`. |
| E4 | **Sentinel row missing from `users` table.** Boot calls `SentinelExists` and refuses to start with `error: admin-sentinel-row-missing` so the operator re-runs migrations. | §2.8 |
| E5 | **Header set with wrong prefix** (e.g., `Authorization: Token abc`). `candidateFromRequest` strictly requires `Bearer ` prefix; falls through to cookie or pass-through. | `TestMiddleware_BadHeaderPrefix_FallsThrough` |
| E6 | **Both header AND cookie present.** Header wins (D1). Cookie is ignored even if header is malformed (because the malformed header has no `Bearer ` prefix and is treated as absent — then the cookie is read). | Test: `TestMiddleware_HeaderWinsOverCookie`. |
| E7 | **Token rotation.** Operator changes `MAKTABA_ADMIN_TOKEN` and restarts. Old-token requests pass through (no bypass), and the cookie/JWT middleware returns 401 if no other auth is present. No grace period. | `TestTokenRotation_OldRejected` |
| E8 | **Constant-time leak via length difference.** Mitigated by hashing both sides under SHA-256 before `hmac.Equal` (D4); both inputs to `hmac.Equal` are 32 bytes. | `TestCtEquals_*` |
| E9 | **Replay across restarts.** The token is operator-set and persists across restarts; replay of a captured token is identical to legitimate use. Out of scope for this story; transport security (Story 10.15) prevents capture. | TLS + HSTS via Story 10.15. |
| E10 | **UI bootstrap dialog (AC-3).** Deferred to Epic 11. The SPA reads the user-pasted token from `localStorage` and sends it as the `mkt_admin_token` cookie. This plan ships only the API contract; the UI work is owned by Epic 11. | Documented out-of-scope at the top. |

---

## 6. Acceptance checklist

- [ ] **A1** `MAKTABA_ADMIN_TOKEN` set + `Authorization: Bearer <tok>` → request runs as the sentinel admin (UUID `00000000-0000-0000-0000-000000000001`, `is_admin=true`). (`TestMiddleware_HeaderBypass`)
- [ ] **A2** `MAKTABA_ADMIN_TOKEN` set + cookie `mkt_admin_token=<tok>` → request runs as the sentinel admin. (`TestMiddleware_CookieBypass`)
- [ ] **A3** Comparison uses `hmac.Equal(sha256(expected), sha256(candidate))` — no early exit on length or content. (`TestCtEquals_*`, code review on `compare.go`.)
- [ ] **A4** UI bootstrap dialog (AC-3) is deferred to Epic 11; this plan ships only the API contract. (Documented in §1; no API code path required for AC-3.)
- [ ] **A5** Token rotation: old-token requests after restart are not bypassed; downstream auth decides 401 vs. valid session. (`TestTokenRotation_OldRejected`)
- [ ] **A6** JWT minted under admin-token path has `lib = [all library_ids]` and `is_admin = true`. (`TestMintAPIInternal_AdminGetsAllLibs`)
- [ ] **A7** Audit row written under admin-token path uses the sentinel UUID as `actor_user_id`. (`TestAuditRowUsesSentinelUUID`)
- [ ] **A8** Empty env var → no bypass for any candidate token, including the empty string. (`TestMiddleware_EmptyEnv_IsNoop`)
- [ ] **A9** A 1-char-different token is rejected (no early exit, no bypass). (`TestMiddleware_OneCharDifferent_IsRejected`)
- [ ] **A10** API boot fails loudly with `admin-token-too-short` when env var is set but `< 32` chars. (`TestLoadConfig_RejectsShort` + integration `TestBoot_RefusesShortToken`)
- [ ] **A11** API boot fails loudly when the sentinel row is missing from `users`. (Integration `TestBoot_RefusesMissingSentinel`.)
- [ ] **A12** Header wins over cookie when both are present and differ (D1). (`TestMiddleware_HeaderWinsOverCookie`)
- [ ] **A13** Middleware does not 401 on candidate-mismatch — it passes through to downstream auth. (`TestMiddleware_OneCharDifferent_IsRejected`)
- [ ] **A14** No DB query on the hot path of the admin-token middleware (boot-only `SentinelExists`); confirmed by handler trace. (Code review.)
