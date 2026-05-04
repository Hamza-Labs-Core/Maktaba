# Plan 10.10 — CSRF protection (web only) — implementation

> Implementation plan for [story-10-10-csrf-protection.md](story-10-10-csrf-protection.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on the `mkt_csrf` cookie issued
> at web login by [Story 10.2](story-10-02-web-login.md), the
> `mkt_sess` session cookie also from Story 10.2, and the bearer-JWT
> path detection logic shared with the JWT verifier from
> [Story 10.7](story-10-07-streaming-jwt-verify.md). The admin-token
> middleware from [Plan 10.9](plan-10-09-single-user-mode.md) runs
> *before* this middleware in the chi chain — admin-token requests are
> treated as bearer-equivalent and skip CSRF.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Double-submit cookie pattern, not synchronizer-token.** The `mkt_csrf` cookie value is sent by the SPA in the `X-Maktaba-CSRF` header on every state-changing request. The middleware compares cookie to header in constant time. | Story description: "double-submit-cookie pattern" + AC-2 ("`X-Maktaba-CSRF: <token>` whose value matches the `mkt_csrf` cookie") | Synchronizer-token requires server-side state per session and a way to look up the token by session id. Double-submit is stateless on the read path: the cookie is the source of truth and the header is the proof-of-intent. The browser same-origin policy guarantees that only same-origin JS can read the cookie, so a malicious cross-origin form POST can submit the cookie but cannot read it to set the header. |
| D2 | **Skip on `GET`, `HEAD`, `OPTIONS`.** All other methods (`POST`, `PUT`, `PATCH`, `DELETE`) are subject to the check. | Story AC-4: "Given GET/HEAD/OPTIONS, … CSRF is not enforced." | Safe methods per RFC 7231 are nullipotent — CSRF on a GET is a different attack surface (tag injection, image-src) and is mitigated by output sanitization, not by CSRF tokens. |
| D3 | **Skip when `Authorization: Bearer ` header is present.** The bearer-JWT path is its own proof-of-intent (browsers don't set custom auth headers cross-origin without a preflight, and a successful preflight requires CORS approval). | Story AC-3: "request authenticated via `Authorization: Bearer …`, … CSRF is not enforced (the bearer header itself is the proof-of-intent — CSRF can't set custom headers on cross-origin requests)." | Mobile/desktop/TV clients use bearer JWTs and don't have cookies. Forcing them to also carry CSRF tokens adds friction and surface area for no benefit. The Origin/Referer check is *additionally* applied (D4) to belt-and-suspender the bearer path. |
| D4 | **Origin/Referer check as defense-in-depth on cookie-auth state-changing requests.** When the request has the `mkt_sess` cookie and a state-changing method, in addition to the CSRF token check, we verify `Origin` (or `Referer` fallback) matches the configured `allowed_origins`. Mismatch → 403 `type: csrf-failed-origin`. | Refines the story (which doesn't specify) | Belt-and-suspenders: on browsers that mishandle cookie isolation (legacy/buggy), the Origin header is enforced by the browser and cannot be spoofed by CSRF. We piggyback on the existing CORS allowed-origins config. The check costs ~50 ns. |
| D5 | **Constant-time compare via `crypto/subtle.ConstantTimeCompare` over equal-length 32-byte tokens.** Tokens are issued as `base64.RawURLEncoding(32 bytes)` = 43 chars; both sides have the same length. Mismatch length → 403 immediately (no timing information leaked because length is structural, not per-token-content). | Refines the story (which says "matches"); standard Go pattern | The token is operator-issued at login and not user-modifiable; if the lengths differ, the request is malformed and 403 is correct. We still use `ConstantTimeCompare` over the equal-length payloads to avoid leaking byte-position-of-first-difference. |
| D6 | **403 with structured error body** `{"type": "csrf-failed", "title": "CSRF token missing or invalid", "detail": "...", "status": 403}` per Problem Details (RFC 7807). | Story AC-2: "403 `type: csrf-failed`" | Maktaba already uses Problem Details for error responses (Epic 7); CSRF errors should match that shape. The `detail` field carries diagnostic info ("missing header", "header/cookie mismatch", "no session cookie") that helps developers debug without leaking the cookie value itself. |

If D3 is rejected (enforce CSRF on bearer paths too): the SPA is the
only browser-cookie consumer; mobile/desktop never have cookies. Forcing
bearer paths to also carry CSRF tokens adds zero security value (the
bearer itself is the proof-of-intent) and creates token-management
overhead on native clients.

If D4 is rejected (skip Origin/Referer check): the double-submit cookie
alone is sufficient under correct browser cookie isolation, but the
Origin check is cheap and catches misconfigured CORS or buggy browsers
without any cost on the happy path.

---

## 1. Architecture diagram — CSRF middleware in the chi chain

```
   Incoming HTTP request
              │
              ▼
   ┌──────────────────────────────────────────────────────┐
   │  chi root middleware chain                           │
   │   ...                                                │
   │   r.Use(admintoken.Middleware(...))   // Plan 10.9   │
   │   r.Use(csrf.Middleware(cfg))         // THIS PLAN   │
   │   r.Use(session.Middleware(...))      // Story 10.2  │
   │   ...                                                │
   └──────────────────────────────┬───────────────────────┘
                                  │
        ┌─────────────────────────┴──────────────────────┐
        │ csrf.Middleware decision tree                   │
        │                                                 │
        │  method == GET/HEAD/OPTIONS?    yes ──► next()  │
        │     │ no                                        │
        │  has Authorization: Bearer?     yes ──► next()  │
        │     │ no                                        │
        │  has admin-token sentinel ctx?  yes ──► next()  │
        │     │ no                                        │
        │  has mkt_sess cookie?           no  ──► next()  │ (no session;
        │     │ yes                                       │  downstream 401)
        │     ▼                                           │
        │  Origin/Referer matches cfg.AllowedOrigins?     │
        │     │ no  ──► 403 type=csrf-failed-origin       │
        │     │ yes                                       │
        │     ▼                                           │
        │  read mkt_csrf cookie  (missing → 403)          │
        │  read X-Maktaba-CSRF header  (missing → 403)    │
        │  ConstantTimeCompare(cookie, header)            │
        │     │ false ──► 403 type=csrf-failed            │
        │     │ true                                      │
        │     ▼                                           │
        │  next.ServeHTTP(w, r)                           │
        └─────────────────────────────────────────────────┘
                                  │
                                  ▼
                          Handler runs.
```

The middleware is a **read-only** consumer of the request. It does not
touch the DB or any external store. The `mkt_csrf` cookie is issued at
login by Story 10.2's handler (which writes a fresh 32-byte random
value); this middleware only validates, never issues.

---

## 2. Detailed implementation

### 2.1 Package layout — Go (API Service)

```
api/
├── internal/
│   ├── auth/
│   │   ├── csrf/
│   │   │   ├── middleware.go        # public: Middleware(cfg) func(http.Handler) http.Handler
│   │   │   ├── config.go            # Config struct (allowed_origins, cookie/header names)
│   │   │   ├── compare.go           # ctEqualBytes via subtle.ConstantTimeCompare
│   │   │   ├── origin.go            # Origin/Referer parser + matcher (D4)
│   │   │   ├── error.go             # writeProblem(w, status, type, detail)
│   │   │   └── middleware_test.go
│   │   ├── ctxuser/                 # Source enum extended for SourceAdminToken
│   │   └── ...                      # admintoken/ from Plan 10.9
│   └── server/
│       └── routes.go                # wires r.Use(csrf.Middleware(...))
└── ...
```

### 2.2 Config

```go
// api/internal/auth/csrf/config.go
package csrf

const (
    CookieName = "mkt_csrf"
    HeaderName = "X-Maktaba-CSRF"
    SessName   = "mkt_sess"

    ProblemTypeMismatch = "csrf-failed"
    ProblemTypeOrigin   = "csrf-failed-origin"
)

// Config carries the runtime knobs for the CSRF middleware. The cookie
// name and header name are constants (interop with the SPA), so the
// only configurable item is the allowed-origin list.
type Config struct {
    // AllowedOrigins is the set of origins (scheme://host[:port]) that
    // are permitted to perform state-changing operations under cookie
    // auth. Reuses the CORS allowed-origin list from Story 10.15.
    AllowedOrigins []string
}
```

### 2.3 Constant-time compare (D5)

```go
// api/internal/auth/csrf/compare.go
package csrf

import "crypto/subtle"

// ctEqualBytes returns true iff a == b. Length mismatch returns false
// without invoking ConstantTimeCompare (length is structural). Same
// length → ConstantTimeCompare avoids the byte-position-of-first-diff
// timing leak.
func ctEqualBytes(a, b []byte) bool {
    if len(a) != len(b) {
        return false
    }
    return subtle.ConstantTimeCompare(a, b) == 1
}
```

### 2.4 Origin matcher (D4)

```go
// api/internal/auth/csrf/origin.go
package csrf

import (
    "net/url"
    "strings"
)

// originMatches returns true if origin (scheme://host[:port]) is in
// allowed. If origin is empty, fall back to Referer's scheme+host.
// Returns false on parse error.
func originMatches(originHeader, refererHeader string, allowed []string) bool {
    candidate := strings.TrimSpace(originHeader)
    if candidate == "" || candidate == "null" {
        // null Origin (e.g. data: URIs, sandboxed iframes) is never allowed.
        if candidate == "null" { return false }
        if refererHeader == "" { return false }
        u, err := url.Parse(refererHeader)
        if err != nil || u.Scheme == "" || u.Host == "" { return false }
        candidate = u.Scheme + "://" + u.Host
    }
    for _, ok := range allowed {
        if strings.EqualFold(candidate, ok) {
            return true
        }
    }
    return false
}
```

### 2.5 Problem-details writer (D6)

```go
// api/internal/auth/csrf/error.go
package csrf

import (
    "encoding/json"
    "net/http"
)

type problem struct {
    Type   string `json:"type"`
    Title  string `json:"title"`
    Detail string `json:"detail"`
    Status int    `json:"status"`
}

func writeProblem(w http.ResponseWriter, status int, ptype, detail string) {
    w.Header().Set("Content-Type", "application/problem+json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(problem{
        Type:   ptype,
        Title:  "CSRF token missing or invalid",
        Detail: detail,
        Status: status,
    })
}
```

### 2.6 Middleware

```go
// api/internal/auth/csrf/middleware.go
package csrf

import (
    "log/slog"
    "net/http"
    "strings"

    "github.com/maktaba/api/internal/auth/ctxuser"
)

// Middleware enforces double-submit-cookie CSRF on state-changing
// requests authenticated by the mkt_sess cookie. Bearer-JWT and
// admin-token requests bypass; safe methods bypass.
func Middleware(cfg Config) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // D2: safe methods bypass.
            switch r.Method {
            case http.MethodGet, http.MethodHead, http.MethodOptions:
                next.ServeHTTP(w, r)
                return
            }

            // D3: bearer-JWT bypass — the Authorization header itself is the
            // proof-of-intent.
            if hasBearer(r) {
                next.ServeHTTP(w, r)
                return
            }

            // Plan 10.9: if the admin-token middleware already attached the
            // sentinel admin to context, treat that as bearer-equivalent.
            if u, ok := ctxuser.FromContext(r.Context()); ok && u.Source == ctxuser.SourceAdminToken {
                next.ServeHTTP(w, r)
                return
            }

            // No cookie session → downstream auth will 401; CSRF doesn't
            // apply (there's no session to forge).
            sess, err := r.Cookie(SessName)
            if err != nil || sess.Value == "" {
                next.ServeHTTP(w, r)
                return
            }

            // D4: Origin/Referer check.
            if !originMatches(r.Header.Get("Origin"), r.Header.Get("Referer"), cfg.AllowedOrigins) {
                slog.Info("csrf_origin_mismatch",
                    "origin", r.Header.Get("Origin"),
                    "referer", r.Header.Get("Referer"))
                writeProblem(w, http.StatusForbidden, ProblemTypeOrigin,
                    "Origin or Referer does not match an allowed origin")
                return
            }

            // Token check.
            csrfCookie, err := r.Cookie(CookieName)
            if err != nil || csrfCookie.Value == "" {
                writeProblem(w, http.StatusForbidden, ProblemTypeMismatch,
                    "missing CSRF cookie")
                return
            }
            csrfHeader := r.Header.Get(HeaderName)
            if csrfHeader == "" {
                writeProblem(w, http.StatusForbidden, ProblemTypeMismatch,
                    "missing CSRF header")
                return
            }
            if !ctEqualBytes([]byte(csrfCookie.Value), []byte(csrfHeader)) {
                writeProblem(w, http.StatusForbidden, ProblemTypeMismatch,
                    "CSRF header does not match CSRF cookie")
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

func hasBearer(r *http.Request) bool {
    h := r.Header.Get("Authorization")
    return strings.HasPrefix(h, "Bearer ")
}
```

### 2.7 Integration with Story 10.2 login handler (token issuance)

The `mkt_csrf` cookie is *issued* by Story 10.2's login handler (32-byte
random value, base64.RawURLEncoded, lifetime equal to session, NOT
HttpOnly so the SPA can read it). This plan only validates. The login
handler is owned by Story 10.2; this plan documents the contract:

```go
// Story 10.2 login handler (excerpt — for reference, NOT shipped here):
func issueCSRFCookie(w http.ResponseWriter) string {
    var b [32]byte
    if _, err := rand.Read(b[:]); err != nil { panic(err) }
    val := base64.RawURLEncoding.EncodeToString(b[:])
    http.SetCookie(w, &http.Cookie{
        Name: csrf.CookieName, Value: val,
        Path: "/", Secure: true, SameSite: http.SameSiteStrictMode,
        HttpOnly: false, // SPA must read it to set X-Maktaba-CSRF header
        MaxAge: int((24 * time.Hour).Seconds()),
    })
    return val
}
```

The `mkt_sess` cookie is HttpOnly (Story 10.2 owns); only `mkt_csrf` is
SPA-readable.

### 2.8 Wire-up in `cmd/api/main.go`

```go
// api/cmd/api/main.go (excerpt)
csrfCfg := csrf.Config{AllowedOrigins: serverCfg.AllowedOrigins}
r.Use(admintoken.Middleware(adminCfg))   // Plan 10.9
r.Use(csrf.Middleware(csrfCfg))           // THIS PLAN
r.Use(session.Middleware(sessCfg))        // Story 10.2
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `api/internal/auth/csrf/config.go` | `CookieName`, `HeaderName`, `SessName`, `ProblemType*`, `Config` | (smoke import) |
| 2 | `api/internal/auth/csrf/compare.go` | `ctEqualBytes` | `TestCtEqualBytes_*` |
| 3 | `api/internal/auth/csrf/origin.go` | `originMatches` | `TestOriginMatches_*` |
| 4 | `api/internal/auth/csrf/error.go` | `writeProblem`, `problem` | (covered by middleware tests) |
| 5 | `api/internal/auth/csrf/middleware.go` | `Middleware`, `hasBearer` | `TestMiddleware_*` |
| 6 | `api/internal/server/routes.go` (extend) | `r.Use(csrf.Middleware(csrfCfg))` | integration `TestRoutes_CSRFEnforcedOnPOST` |

No new migrations; this story is 100% middleware. The cookie is issued
by Story 10.2 (existing).

---

## 4. Test cases keyed to acceptance criteria

### 4.1 `TestMiddleware_PostWithCookieAndMatchingHeader_OK` (AC-2)

```go
func TestMiddleware_PostWithCookieAndMatchingHeader_OK(t *testing.T) {
    h := buildHandler(csrf.Config{AllowedOrigins: []string{"https://app.example"}})
    tok := "ZGVhZGJlZWY..." // 43 chars, base64.RawURLEncoded(32 bytes)
    req := httptest.NewRequest("POST", "/api/something", nil)
    req.AddCookie(&http.Cookie{Name: "mkt_sess", Value: "sid"})
    req.AddCookie(&http.Cookie{Name: "mkt_csrf", Value: tok})
    req.Header.Set("X-Maktaba-CSRF", tok)
    req.Header.Set("Origin", "https://app.example")
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 200, rr.Code)
}
```

### 4.2 `TestMiddleware_PostWithCookieNoHeader_403` (AC-2)

```go
func TestMiddleware_PostWithCookieNoHeader_403(t *testing.T) {
    h := buildHandler(csrf.Config{AllowedOrigins: []string{"https://app.example"}})
    req := httptest.NewRequest("POST", "/api/x", nil)
    req.AddCookie(&http.Cookie{Name: "mkt_sess", Value: "sid"})
    req.AddCookie(&http.Cookie{Name: "mkt_csrf", Value: "tok"})
    req.Header.Set("Origin", "https://app.example")
    // No X-Maktaba-CSRF header.
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 403, rr.Code)
    var p map[string]any
    require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))
    require.Equal(t, "csrf-failed", p["type"])
    require.Contains(t, p["detail"], "missing CSRF header")
}
```

### 4.3 `TestMiddleware_PostBearerToken_BypassesCSRF` (AC-3)

```go
func TestMiddleware_PostBearerToken_BypassesCSRF(t *testing.T) {
    h := buildHandler(csrf.Config{AllowedOrigins: []string{"https://app.example"}})
    req := httptest.NewRequest("POST", "/api/x", nil)
    req.Header.Set("Authorization", "Bearer eyJ...")
    // No CSRF cookie or header — bearer wins.
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 200, rr.Code)
}
```

### 4.4 `TestMiddleware_GetIsExempt` (AC-4)

```go
func TestMiddleware_GetIsExempt(t *testing.T) {
    h := buildHandler(csrf.Config{AllowedOrigins: []string{"https://app.example"}})
    for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
        req := httptest.NewRequest(m, "/api/x", nil)
        req.AddCookie(&http.Cookie{Name: "mkt_sess", Value: "sid"})
        // No CSRF token at all.
        rr := httptest.NewRecorder()
        h.ServeHTTP(rr, req)
        require.Equal(t, 200, rr.Code, "method=%s", m)
    }
}
```

### 4.5 `TestMiddleware_FormPostFromAttacker_BlockedByOrigin` (security test)

```go
func TestMiddleware_FormPostFromAttacker_BlockedByOrigin(t *testing.T) {
    // Simulates: a malicious site at https://evil.example uses an HTML
    // form to POST to https://app.example/api/x. The browser sends the
    // mkt_sess cookie, but the Origin header reflects evil.example.
    h := buildHandler(csrf.Config{AllowedOrigins: []string{"https://app.example"}})
    req := httptest.NewRequest("POST", "/api/x", nil)
    req.AddCookie(&http.Cookie{Name: "mkt_sess", Value: "sid"})
    req.AddCookie(&http.Cookie{Name: "mkt_csrf", Value: "tok"})
    // Browser-supplied Origin (cannot be spoofed by JS):
    req.Header.Set("Origin", "https://evil.example")
    // Attacker did not (and cannot from a simple form post) set the
    // X-Maktaba-CSRF header.
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 403, rr.Code)
    var p map[string]any
    require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))
    require.Equal(t, "csrf-failed-origin", p["type"])
}
```

### 4.6 `TestMiddleware_HeaderCookieMismatch_403` (AC-2 negative)

```go
func TestMiddleware_HeaderCookieMismatch_403(t *testing.T) {
    h := buildHandler(csrf.Config{AllowedOrigins: []string{"https://app.example"}})
    req := httptest.NewRequest("POST", "/api/x", nil)
    req.AddCookie(&http.Cookie{Name: "mkt_sess", Value: "sid"})
    req.AddCookie(&http.Cookie{Name: "mkt_csrf", Value: "AAA..."})
    req.Header.Set("X-Maktaba-CSRF", "BBB...") // different
    req.Header.Set("Origin", "https://app.example")
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 403, rr.Code)
}
```

### 4.7 `TestMiddleware_NoSessionCookie_PassesThrough` (edge: cleared cookies)

```go
func TestMiddleware_NoSessionCookie_PassesThrough(t *testing.T) {
    // Cleared cookies mid-session: no mkt_sess at all → CSRF middleware
    // is a no-op; downstream auth returns 401 (not 403).
    h := buildHandler(csrf.Config{AllowedOrigins: []string{"https://app.example"}})
    req := httptest.NewRequest("POST", "/api/x", nil)
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    // Asserting handler returned 200; downstream auth (not part of this
    // test rig) is what would 401.
    require.Equal(t, 200, rr.Code)
}
```

### 4.8 `TestMiddleware_AdminTokenSentinel_BypassesCSRF` (cross-ref Plan 10.9)

```go
func TestMiddleware_AdminTokenSentinel_BypassesCSRF(t *testing.T) {
    h := buildHandlerWithCtxUser(csrf.Config{AllowedOrigins: []string{"https://app.example"}},
        ctxuser.User{Source: ctxuser.SourceAdminToken, IsAdmin: true})
    req := httptest.NewRequest("POST", "/api/x", nil)
    // No CSRF token; no bearer; just the sentinel context from admin-token middleware.
    rr := httptest.NewRecorder()
    h.ServeHTTP(rr, req)
    require.Equal(t, 200, rr.Code)
}
```

### 4.9 `TestOriginMatches_*`

```go
func TestOriginMatches_AllowedOK(t *testing.T) {
    require.True(t, originMatchesExp("https://app.example", "", []string{"https://app.example"}))
}
func TestOriginMatches_FallbackToReferer(t *testing.T) {
    require.True(t, originMatchesExp("", "https://app.example/foo", []string{"https://app.example"}))
}
func TestOriginMatches_NullOriginRejected(t *testing.T) {
    require.False(t, originMatchesExp("null", "", []string{"https://app.example"}))
}
func TestOriginMatches_PortMatters(t *testing.T) {
    require.False(t, originMatchesExp("https://app.example:444", "", []string{"https://app.example"}))
}
```

### 4.10 `TestCtEqualBytes_*`

```go
func TestCtEqualBytes(t *testing.T) {
    a := []byte("ABCDEFGHIJKL")
    require.True(t, ctEqualBytes(a, a))
    require.False(t, ctEqualBytes(a, []byte("ABCDEFGHIJK")))   // length differs
    require.False(t, ctEqualBytes(a, []byte("BBCDEFGHIJKL")))  // first byte differs
    require.False(t, ctEqualBytes(a, []byte("ABCDEFGHIJKM")))  // last byte differs
}
```

---

## 5. Edge cases and how the plan handles each

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **CSRF token rotation across logins.** The token rotates on each login (Story 10.2 issues a fresh value). The SPA reads `mkt_csrf` from `document.cookie` once at boot AND after each login. Documented behavior. | Story 10.2 owns issuance; this plan validates only. Documented in §2.7. |
| E2 | **User clears cookies mid-session.** Next request has no `mkt_sess` and no `mkt_csrf`. CSRF middleware passes through (E1 in the ASCII matrix); downstream session middleware returns 401 (not 403). | `TestMiddleware_NoSessionCookie_PassesThrough` |
| E3 | **CSRF cookie present but session cookie missing.** Treated as no-session: CSRF middleware passes through; downstream session middleware 401s. | Same as E2. |
| E4 | **CSRF cookie missing, session cookie present.** This means the SPA never received a valid login response (or a deploy bug stripped the cookie). Returns 403 `type=csrf-failed, detail="missing CSRF cookie"`. | `TestMiddleware_PostWithCookieNoHeader_403` variant |
| E5 | **Bearer header AND cookies present** (a hybrid client confused about auth modes). Bearer wins (D3): CSRF skipped. The bearer JWT is the proof-of-intent. | `TestMiddleware_PostBearerToken_BypassesCSRF` (extend with cookies) |
| E6 | **`Origin: null`** (sandboxed iframe, file:// URI). Always rejected — `null` is never in `AllowedOrigins`. | `TestOriginMatches_NullOriginRejected` |
| E7 | **Trailing slash or path on Origin** (some browsers send `https://app.example/`). The check uses scheme+host only; a trailing slash doesn't appear on `Origin` (RFC 6454). The Referer fallback strips path. | `originMatches` parses Referer with `url.Parse` and reconstructs `scheme://host`. |
| E8 | **Port mismatch.** `https://app.example` and `https://app.example:444` are different origins. The check is byte-exact on `scheme://host[:port]`. | `TestOriginMatches_PortMatters` |
| E9 | **Header case sensitivity.** Go's `http.Header.Get` is case-insensitive on the key; the value comparison is byte-exact. SPAs typically send `X-Maktaba-CSRF` or `x-maktaba-csrf` — both are accepted. | Standard `net/http` behavior; no special code path. |
| E10 | **Replay across sessions.** A captured `mkt_csrf` from session A used against session B's `mkt_sess` would not match because each login issues a fresh `mkt_csrf` cookie. The browser carries only the latest. | Login flow (Story 10.2) re-issues. |
| E11 | **Cross-site form POST without JS** (the classic CSRF). Browser sends cookies but cannot set custom headers like `X-Maktaba-CSRF` on simple form POSTs (only `application/x-www-form-urlencoded`, `multipart/form-data`, `text/plain` are allowed without preflight). 403 fires. | `TestMiddleware_FormPostFromAttacker_BlockedByOrigin` (Origin check fires first; even if Origin were spoofable the header check fires next). |
| E12 | **Preflight OPTIONS request.** `OPTIONS` is a safe method; passes through without CSRF check. The CORS handler downstream returns the appropriate `Access-Control-*` headers. | D2; `TestMiddleware_GetIsExempt` covers OPTIONS. |

---

## 6. Acceptance checklist

- [ ] **A1** A successful web login (Story 10.2) issues `mkt_csrf=<32-byte random base64-url>` as a non-HttpOnly cookie. (Owned by Story 10.2; documented contract in §2.7. Smoke test: login and inspect `Set-Cookie` for `mkt_csrf`.)
- [ ] **A2** A state-changing request (POST/PUT/PATCH/DELETE) with the `mkt_sess` cookie must carry `X-Maktaba-CSRF: <token>` matching the `mkt_csrf` cookie; mismatch or missing → 403 `type: csrf-failed`. (`TestMiddleware_PostWithCookieNoHeader_403`, `TestMiddleware_HeaderCookieMismatch_403`)
- [ ] **A3** A request authenticated via `Authorization: Bearer …` skips CSRF entirely. (`TestMiddleware_PostBearerToken_BypassesCSRF`)
- [ ] **A4** GET/HEAD/OPTIONS bypass CSRF unconditionally. (`TestMiddleware_GetIsExempt`)
- [ ] **A5** A malicious cross-origin form POST (browser-controlled `Origin: https://evil.example`) is blocked with 403 `type: csrf-failed-origin` (D4 belt-and-suspenders). (`TestMiddleware_FormPostFromAttacker_BlockedByOrigin`)
- [ ] **A6** Constant-time compare via `subtle.ConstantTimeCompare` on equal-length payloads; length mismatch is structural and short-circuited. (`TestCtEqualBytes`)
- [ ] **A7** Admin-token sentinel (Plan 10.9) bypasses CSRF — the admin-token middleware runs first and the CSRF middleware checks `ctxuser.SourceAdminToken`. (`TestMiddleware_AdminTokenSentinel_BypassesCSRF`)
- [ ] **A8** Cleared-cookie / no-session requests pass through CSRF (no false-403); downstream session middleware returns 401. (`TestMiddleware_NoSessionCookie_PassesThrough`)
- [ ] **A9** Error responses follow Problem Details (RFC 7807) with `type`, `title`, `detail`, `status` fields. Content-Type is `application/problem+json`. (Inspection in `TestMiddleware_PostWithCookieNoHeader_403`)
- [ ] **A10** No DB or external-store reads on the middleware hot path; the entire check is request-local. (Code review on `middleware.go`.)
- [ ] **A11** Origin check uses `Origin` header first, falls back to `Referer`'s scheme+host, rejects `null`, requires byte-exact match against `AllowedOrigins` (incl. port). (`TestOriginMatches_*`)
- [ ] **A12** Cookie name `mkt_csrf`, header name `X-Maktaba-CSRF`, session name `mkt_sess` are constants in `config.go`; SPA contract is single-sourced. (`config.go` review.)
