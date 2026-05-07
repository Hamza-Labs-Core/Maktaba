# Implementation Plan — Story 10.10 CSRF protection (web only)

> Companion to [story-10-10-csrf-protection.md](story-10-10-csrf-protection.md).
> The `mkt_csrf` cookie is set by [Story 10.2](plan-10-02-web-login.md);
> this story owns the *enforcement* middleware. The bearer-JWT path
> (Story 10.3) skips this middleware.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Middleware | `api/internal/http/middleware/csrf.go` — runs *after* the session/bearer middleware so it can branch on whether the request is bearer-authenticated. |
| Compare | `subtle.ConstantTimeCompare(headerToken, cookieToken)`. |
| Header name | `X-Maktaba-CSRF`. |
| Methods enforced | `POST`, `PUT`, `PATCH`, `DELETE`. `GET`/`HEAD`/`OPTIONS` skip. |
| Bearer-path skip | If `auth.JWTClaimsFromContext(ctx)` is set (i.e., bearer middleware authenticated), CSRF is not enforced. |
| Out of scope | Issuance (Story 10.2). Token rotation policy (also 10.2 — token rotates on each login). |

## 1. Architecture diagram

```
incoming request
   ▼
session/bearer middleware (auth set in ctx)
   ▼
┌──────────────────────────────────────────────────────────────┐
│ middleware/csrf.go                                            │
│   if method ∈ {GET, HEAD, OPTIONS}        → next               │
│   if claims, _ := JWTClaimsFromContext(ctx); claims != nil    │
│       → next  (bearer auth — header itself is the proof)      │
│   if no auth at all (anonymous)            → next             │
│   if !cookie("mkt_csrf")                  → 403 csrf-missing-cookie│
│   if !header("X-Maktaba-CSRF")            → 403 csrf-missing-header│
│   if !ConstantTimeCompare(header, cookie) → 403 csrf-mismatch  │
│   → next                                                       │
└──────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/http/middleware/csrf.go` | Middleware. |
| `api/internal/http/middleware/csrf_test.go` | Integration tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/http/router.go` | Install CSRF middleware after session/bearer; before handlers. |
| `web/src/lib/api.ts` | (Web client) Read `mkt_csrf` from `document.cookie` on boot; attach `X-Maktaba-CSRF` to every state-changing request. |

### 2.3 Function signatures

```go
// api/internal/http/middleware/csrf.go
func CSRF(opts CSRFOptions) func(http.Handler) http.Handler

type CSRFOptions struct {
    HeaderName string   // "X-Maktaba-CSRF"
    CookieName string   // "mkt_csrf"
}
```

## 3. Middleware

```go
// api/internal/http/middleware/csrf.go
package middleware

import (
    "crypto/subtle"
    "net/http"

    "maktaba/api/internal/auth"
)

var safeMethods = map[string]struct{}{
    http.MethodGet: {}, http.MethodHead: {}, http.MethodOptions: {},
}

func CSRF(opts CSRFOptions) func(http.Handler) http.Handler {
    if opts.HeaderName == "" { opts.HeaderName = "X-Maktaba-CSRF" }
    if opts.CookieName == "" { opts.CookieName = "mkt_csrf" }

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if _, safe := safeMethods[r.Method]; safe {
                next.ServeHTTP(w, r); return
            }

            // Bearer-authenticated → skip. Even custom headers cross-origin
            // are blocked by the browser without a successful CORS preflight.
            if _, ok := auth.JWTClaimsFromContext(r.Context()); ok {
                next.ServeHTTP(w, r); return
            }

            // Anonymous request (no session, no bearer) → no CSRF risk to
            // worry about because there's nothing to ride; let it pass to
            // the route, which will 401 if it requires auth.
            if _, ok := auth.SessionFromContext(r.Context()); !ok {
                next.ServeHTTP(w, r); return
            }

            cookie, err := r.Cookie(opts.CookieName)
            if err != nil {
                problem(w, http.StatusForbidden, "csrf-missing-cookie", "")
                return
            }
            header := r.Header.Get(opts.HeaderName)
            if header == "" {
                problem(w, http.StatusForbidden, "csrf-missing-header", "")
                return
            }
            if subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
                problem(w, http.StatusForbidden, "csrf-mismatch", "")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

The single classified type per failure mode (`csrf-missing-cookie`,
`csrf-missing-header`, `csrf-mismatch`) helps developers debug client
issues. The story's AC-2 lists `type: csrf-failed`; we expose the
sub-classification in `Maktaba-Hint` header for diagnostics while
keeping the `type` consistent across all three:

```go
func problem(w http.ResponseWriter, status int, hint, detail string) {
    w.Header().Set("Maktaba-Hint", hint)
    writeProblem(w, status, "csrf-failed", detail)
}
```

(The `Maktaba-Hint` is already used by Story 10.2 for cookies-missing
diagnostics.)

## 4. Web client

```ts
// web/src/lib/api.ts
function csrfToken(): string | null {
  const m = document.cookie.match(/(?:^|;\s*)mkt_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : null;
}

export async function api(path: string, init: RequestInit = {}) {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    const t = csrfToken();
    if (t) headers.set("X-Maktaba-CSRF", t);
  }
  return fetch(path, { ...init, headers, credentials: "same-origin" });
}
```

The token is read once per request from `document.cookie` (cheap; no
caching needed). On login, the new `mkt_csrf` cookie replaces the old
one and the next state-change picks it up automatically.

## 5. Test plan

### 5.1 Middleware (`csrf_test.go`)

| Test | What it pins |
|---|---|
| `TestCSRFGetSkipsCheck` | GET protected route with cookie but no header → 200. |
| `TestCSRFOptionsSkipsCheck` | OPTIONS preflight → 200; never blocks. |
| `TestCSRFPostMissingCookieReturns403` | POST with no `mkt_csrf` cookie → 403, `Maktaba-Hint: csrf-missing-cookie`, `type: csrf-failed`. |
| `TestCSRFPostMissingHeaderReturns403` | POST with cookie but no `X-Maktaba-CSRF` → 403, `Maktaba-Hint: csrf-missing-header`. |
| `TestCSRFPostMismatchReturns403` | POST with header value differing in last char from cookie → 403, `Maktaba-Hint: csrf-mismatch`. |
| `TestCSRFPostMatchReturns200` | POST with matching header and cookie → 200. |
| `TestCSRFBearerSkipsCheck` | Authorization: Bearer <jwt>; POST with no CSRF artifact → 200 (bearer auth proves intent). |
| `TestCSRFAnonSkipsCheck` | No cookie, no bearer; POST to a route that requires auth → 401 from the route, NOT 403 from CSRF (the CSRF middleware passes through the anon request to let the route decide). |
| `TestCSRFConstantTimeCompareTimingLastVsFirst` | 1000 trials each: header differs from cookie in the first byte vs the last byte; timing within 5 %. |
| `TestCSRFTokenRotatesOnLogin` | Login A → POST works with token A; logout, login B → POST with token A returns 403; with token B returns 200. |

### 5.2 Cross-site scenario (synthetic)

| Test | What it pins |
|---|---|
| `TestSimplePOSTWithoutHeaderBlocked` | Simulate browser form POST cross-site (no custom header set) → 403 csrf-failed. |
| `TestPreflightOPTIONSAllowed` | A custom-header POST cross-origin triggers an OPTIONS preflight; preflight returns 204 without CSRF check; the actual POST is then expected to be from a same-origin context (or CORS-allowed) and carries the header. |

### 5.3 Bearer / cookie composition

| Test | What it pins |
|---|---|
| `TestCookieAndBearerBothPresentBearerWins` | Request carries both `Authorization: Bearer …` (valid JWT) and `mkt_sess` cookie → CSRF middleware skips (bearer wins). |
| `TestCookieValidBearerInvalid` | Bearer header malformed → bearer middleware did not authenticate; cookie is valid; CSRF middleware enforces. |

## 6. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| User clears cookies mid-session | `mkt_sess` is gone → request is anonymous → CSRF passes through → route returns 401 (not 403). | `TestCSRFAnonSkipsCheck` |
| CSRF token rotation | Each login issues a new `mkt_csrf` (Story 10.2). The SPA reads the new value on its next state-changing request. Tests pin this. | `TestCSRFTokenRotatesOnLogin` |
| Browser doesn't send `X-Maktaba-CSRF` for a simple form POST | Browsers cannot set custom headers on form POSTs cross-origin without a preflight (which would reveal the request to the API's CORS policy first). The header check therefore protects against the standard CSRF vector. | `TestSimplePOSTWithoutHeaderBlocked` |
| GraphQL endpoint at `/graphql` | Mounted under the same router; CSRF middleware applies. GraphQL clients (graphql-request) attach the header via `api.ts`. | covered by general POST tests |
| WebSocket `Upgrade` request | The Upgrade is a GET; CSRF skips. WebSockets have their own origin-check (Story 10.15 plan). | n/a |
| User opens DevTools and removes `mkt_csrf` cookie | Next state-change → 403 csrf-missing-cookie. The user re-logs-in to recover. | `TestCSRFPostMissingCookieReturns403` |
| Misbehaving SPA pulls old token from cache | After logout + new login, the cached token doesn't match → 403 csrf-mismatch. The SPA's interceptor refreshes by re-reading `document.cookie` on each call (per §4). | `TestCSRFTokenRotatesOnLogin` |
| Header name inconsistency | The middleware's `HeaderName` defaults to `X-Maktaba-CSRF`; the SPA hardcodes the same. Operators changing this constant must coordinate both. | n/a (config doc) |
| Cookie value contains `=` or `;` | The cookie value is URL-decoded by `r.Cookie`; the SPA's match is on the cookie's raw URL-encoded form, so it must `decodeURIComponent` before sending in the header. | covered by `TestCSRFPostMatchReturns200` |

## 7. Dependencies

No new dependencies.

## 8. Acceptance checklist

**Issuance**
- [ ] (Owned by Story 10.2) Login sets `mkt_csrf` cookie with documented attributes.

**Enforcement**
- [ ] AC-1: token issued on login (Story 10.2); this story consumes it.
- [ ] AC-2: state-changing requests with `mkt_sess` and missing/mismatched `X-Maktaba-CSRF` → 403 `csrf-failed`.
- [ ] AC-3: bearer-authenticated requests skip CSRF.
- [ ] AC-4: `GET`, `HEAD`, `OPTIONS` skip.

**Compare**
- [ ] `subtle.ConstantTimeCompare` used; timing test pinned.

**Tests**
- [ ] All §5 tests pass.

**Docs**
- [ ] README.md ticks story 10.10.
- [ ] Web client README documents the CSRF interceptor pattern.
