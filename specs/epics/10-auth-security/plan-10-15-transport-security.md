# Implementation Plan — Story 10.15 Transport security

> Companion to [story-10-15-transport-security.md](story-10-15-transport-security.md).
> The cookies themselves are set by [Story 10.2](plan-10-02-web-login.md);
> this story enforces the *attributes* and the surrounding HTTP layer
> (HSTS, CSP, CORS, security response headers).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| TLS termination | Caddy in `deploy/docker/caddy/Caddyfile`; auto-cert for real domains, `tls internal` for `.local`. |
| Backend listener | API and Streaming listen on plain HTTP behind Caddy; refusing direct TLS keeps backend simple. |
| Headers middleware | `api/internal/http/middleware/security_headers.go` — single middleware sets HSTS, X-Content-Type-Options, Referrer-Policy, COOP, CSP. Same package mirrored to `streaming/internal/http/middleware/security_headers.go`. |
| CORS middleware | `api/internal/http/middleware/cors.go` — allow-list driven; reject unknown origins silently. |
| Cookie attribute enforcement | Already in `api/internal/auth/cookies.go` (Story 10.2). This story adds a startup validator that refuses `Secure=false` unless `MAKTABA_DEV=1`. |
| WebSocket origin check | `api/internal/ws/upgrade.go` — origin must be in the allow-list for the upgrade to succeed. |
| Out of scope | Caddy install (deploy story), per-route caching headers (Epic 22). |

## 1. Architecture diagram

```
                          public internet
                                │
                                ▼
              ┌────────────────────────────────────┐
              │ Caddy (auto-TLS)                    │
              │   :443 → /api → api:8080            │
              │          /graphql → api             │
              │          /ws    → api               │
              │          /stream → streaming:8081   │
              │          /        → web (static)    │
              │   :80 → 308 to https                │
              └────────────────┬───────────────────┘
                                │ plain HTTP
                                ▼
   ┌────────────────────────────────────────────────────────────┐
   │ Backend chain (api or streaming):                           │
   │   middleware.SecurityHeaders                                │
   │   middleware.CORS                                           │
   │   middleware.LogRequests (Story 10.14)                      │
   │   middleware.AdminToken (Story 10.9)                        │
   │   middleware.Bearer     (Story 10.3)                        │
   │   middleware.Session    (Story 10.2)                        │
   │   middleware.CSRF       (Story 10.10)                       │
   │   middleware.AuthRateLimit (Story 10.12)                    │
   │   middleware.RateLimit  (Epic 7 Story 7.19)                 │
   │   handler                                                   │
   └────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `deploy/docker/caddy/Caddyfile` | Caddy config for the canonical compose. |
| `api/internal/http/middleware/security_headers.go` | HSTS, CSP, etc. |
| `api/internal/http/middleware/cors.go` | CORS allow-list middleware. |
| `streaming/internal/http/middleware/security_headers.go` | Mirror; same shape. |
| `api/internal/ws/upgrade.go` | (Updated) origin-check during upgrade. |
| `api/internal/http/middleware/security_headers_test.go` | Tests. |
| `api/internal/http/middleware/cors_test.go` | Tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Server.HSTS.Enabled` (true), `Server.HSTS.MaxAge` (31536000), `Server.HSTS.IncludeSubDomains` (true), `Server.CORSAllowedOrigins` ([]string), `Server.CSPDirectives` (string). |
| `api/internal/auth/cookies.go` | At startup, validate `cfg.Cookies.Secure || os.Getenv("MAKTABA_DEV") == "1"`. |
| `api/cmd/api/main.go` | Mount the new middlewares first in the chain. |
| `streaming/cmd/streaming/main.go` | Same on streaming side (no cookies, but HSTS + headers still apply). |
| `web/src/main.tsx` | WebSocket connect prefers `wss://`, falls back to `ws://` only when `import.meta.env.DEV`. |

## 3. Caddyfile

```caddyfile
# deploy/docker/caddy/Caddyfile

{
    # Internal CA for .local hostnames; Let's Encrypt for real domains.
    # The user toggles this implicitly by choosing a hostname.
    email admin@maktaba.local
    auto_https on
}

# Production / .local fallback
maktaba.local, *.maktaba.local {
    tls internal

    @api      path /api/* /graphql /.well-known/*
    @stream   path /stream/*
    @ws       path /ws
    @web      path /*

    handle @api      { reverse_proxy api:8080 }
    handle @stream   { reverse_proxy streaming:8081 }
    handle @ws       { reverse_proxy api:8080 }
    handle @web      { reverse_proxy web:5173 }

    encode gzip zstd

    # HSTS is set by the backend so it stays consistent across Caddy +
    # direct-to-backend dev paths. Caddy is a no-op for security headers.

    header /api/* {
        # Backend already sets these; Caddy doesn't override.
    }
}

# Dev profile (overlay docker-compose.dev.yml maps localhost):
:80 {
    @api    path /api/* /graphql /.well-known/*
    @stream path /stream/*
    @ws     path /ws
    handle @api    { reverse_proxy api:8080 }
    handle @stream { reverse_proxy streaming:8081 }
    handle @ws     { reverse_proxy api:8080 }
    reverse_proxy web:5173
}
```

For real domain deployments, a sibling Caddyfile snippet swaps `tls
internal` for `tls user@example.com` and removes the `:80` dev block.

## 4. Security headers middleware

```go
// api/internal/http/middleware/security_headers.go
package middleware

import (
    "fmt"
    "net/http"
)

type SecurityHeadersConfig struct {
    HSTSEnabled         bool
    HSTSMaxAge          int
    HSTSIncludeSubDomains bool
    CSP                 string
}

func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler {
    hsts := buildHSTS(cfg)
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            h := w.Header()
            // AC-2: HSTS only on TLS-served responses; Caddy sets X-Forwarded-Proto.
            if cfg.HSTSEnabled && r.Header.Get("X-Forwarded-Proto") == "https" {
                h.Set("Strict-Transport-Security", hsts)
            }
            h.Set("X-Content-Type-Options", "nosniff")
            h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
            h.Set("Cross-Origin-Opener-Policy", "same-origin")
            h.Set("X-Frame-Options", "DENY")
            if cfg.CSP != "" {
                h.Set("Content-Security-Policy", cfg.CSP)
            }
            next.ServeHTTP(w, r)
        })
    }
}

func buildHSTS(cfg SecurityHeadersConfig) string {
    s := fmt.Sprintf("max-age=%d", cfg.HSTSMaxAge)
    if cfg.HSTSIncludeSubDomains { s += "; includeSubDomains" }
    return s
}
```

The default CSP for the SPA shell:

```
default-src 'self';
script-src 'self';
style-src 'self' 'unsafe-inline';   /* Vidstack inline */
img-src 'self' data: blob:;
media-src 'self' blob:;
connect-src 'self' wss: https:;
font-src 'self';
frame-ancestors 'none';
form-action 'self';
base-uri 'self';
```

The full string lives in config so deployments with extra origins can
override it without recompiling.

## 5. CORS middleware

```go
// api/internal/http/middleware/cors.go
type CORSConfig struct {
    AllowedOrigins []string
    AllowedMethods []string
    AllowedHeaders []string
    MaxAgeSec      int
}

func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
    allowSet := make(map[string]struct{}, len(cfg.AllowedOrigins))
    for _, o := range cfg.AllowedOrigins { allowSet[o] = struct{}{} }
    methods := strings.Join(cfg.AllowedMethods, ", ")
    headers := strings.Join(cfg.AllowedHeaders, ", ")

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            origin := r.Header.Get("Origin")
            if origin != "" {
                if _, ok := allowSet[origin]; ok {
                    w.Header().Set("Access-Control-Allow-Origin", origin)
                    w.Header().Set("Access-Control-Allow-Credentials", "true")
                    w.Header().Set("Vary", "Origin")
                    if r.Method == http.MethodOptions {
                        w.Header().Set("Access-Control-Allow-Methods", methods)
                        w.Header().Set("Access-Control-Allow-Headers", headers)
                        w.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAgeSec))
                        w.WriteHeader(http.StatusNoContent)
                        return
                    }
                }
                // Unknown origin → no headers; the browser will reject.
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

Default config:

```toml
[server]
cors_allowed_origins = ["https://maktaba.local", "https://app.maktaba.local"]
cors_allowed_methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"]
cors_allowed_headers = ["Authorization", "Content-Type", "X-Maktaba-CSRF", "X-Maktaba-Client", "X-Maktaba-Device"]
cors_max_age_sec     = 600
```

## 6. WebSocket origin check

```go
// api/internal/ws/upgrade.go (additions)
var allowedOrigins map[string]struct{}

upgrader := websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        if origin == "" { return false }   // refuse origin-less upgrades
        _, ok := allowedOrigins[origin]
        return ok
    },
}
```

The origin allow-list reuses `Server.CORSAllowedOrigins`. Refusing
origin-less upgrades is intentional: a same-origin browser always sends
Origin; only non-browser tooling omits it, and we don't support that.

## 7. Cookie validator

```go
// api/internal/auth/cookies.go (additions)
func (c CookieOptions) ValidateAtBoot() error {
    if !c.Secure && os.Getenv("MAKTABA_DEV") != "1" {
        return fmt.Errorf("auth: cookie_secure=false requires MAKTABA_DEV=1")
    }
    if !c.Secure {
        slog.Warn("cookie Secure flag disabled (MAKTABA_DEV=1)")
    }
    return nil
}
```

Called from `main.go` immediately after config load.

## 8. Test plan

### 8.1 Security headers (`security_headers_test.go`)

| Test | What it pins |
|---|---|
| `TestHSTSPresentOnHTTPS` | `X-Forwarded-Proto: https` → `Strict-Transport-Security` set with documented max-age. |
| `TestHSTSAbsentOnPlainHTTP` | No proto header → no HSTS (avoids breaking dev). |
| `TestNosniffAlwaysSet` | Every response carries `X-Content-Type-Options: nosniff`. |
| `TestReferrerPolicySet` | `strict-origin-when-cross-origin`. |
| `TestCOOPSet` | `same-origin`. |
| `TestCSPSet` | The configured CSP string is set verbatim. |
| `TestXFrameOptionsDeny` | `DENY`. |

### 8.2 CORS (`cors_test.go`)

| Test | What it pins |
|---|---|
| `TestCORSKnownOriginAllowed` | Origin in allow-list → `Access-Control-Allow-Origin: <origin>`, `Access-Control-Allow-Credentials: true`, `Vary: Origin`. |
| `TestCORSUnknownOriginSilentDeny` | Origin not in list → no CORS headers; the request continues to the route (browser will block client-side). |
| `TestCORSPreflightReturns204` | OPTIONS with allowed origin → 204 with full allow-* headers. |
| `TestCORSPreflightUnknownOriginNoHeaders` | OPTIONS with unknown origin → 204 (we don't 4xx — silent denial), no allow-* headers. |
| `TestCORSAllowedHeadersIncludesCSRF` | The default config includes `X-Maktaba-CSRF`. |

### 8.3 Cookie attribute validation

| Test | What it pins |
|---|---|
| `TestCookieSecureFalseFailsBoot` | `cookie_secure=false`, `MAKTABA_DEV` unset → `ValidateAtBoot` returns error. |
| `TestCookieSecureFalseAllowedInDev` | `cookie_secure=false`, `MAKTABA_DEV=1` → no error; WARN log emitted. |
| `TestCookieSecureTrueAlwaysOK` | `cookie_secure=true`, any env → no error. |

### 8.4 WebSocket origin check

| Test | What it pins |
|---|---|
| `TestWSUpgradeAllowedOriginAccepted` | Origin in list → upgrade succeeds. |
| `TestWSUpgradeUnknownOriginRejected` | Origin not in list → 403. |
| `TestWSUpgradeMissingOriginRejected` | No Origin header → 403. |

### 8.5 Caddy smoke (manual / docker-compose)

| Test | What it pins |
|---|---|
| `TestCaddyServesAPIOver443` | `curl -k https://maktaba.local/api/system/health` → 200. |
| `TestCaddyRedirectsHTTPto443` | `curl -I http://maktaba.local/api/...` → 308 + `Location: https://...`. |
| `TestCaddyForwardsXForwardedProto` | The backend log line for the request shows `X-Forwarded-Proto: https`. |

### 8.6 Security scan integration

`make securityscan` runs:
- `securityheaders.com`-style checks via `headers` library locally.
- `nuclei` smoke (a pinned subset) against the live dev instance.

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Reverse proxy that doesn't set `X-Forwarded-Proto` | HSTS not emitted (we err on the safe side). Operations doc tells operators to set it. | `TestHSTSAbsentOnPlainHTTP` |
| Pure-`.local` setup with untrusted cert | Browser shows warning; HSTS pinning would harden the wrong cert. The doc tells operators to either accept the warning per-browser or trust Caddy's local CA cert (`caddy trust`). | n/a |
| Operator hosts on a real domain but disables HSTS | Allowed via config; we never force HSTS, only default it on. | `TestHSTSAbsentOnPlainHTTP` (force-disable variant) |
| WebSocket from Tauri / Electron desktop with custom origin | The desktop's origin (`tauri://localhost` or `app://...`) must be added to `cors_allowed_origins`. Documented in apps/desktop/README. | `TestWSUpgradeAllowedOriginAccepted` |
| CORS with credentialed cross-origin SPA | The `Access-Control-Allow-Credentials: true` requires a *specific* origin (not `*`). Our middleware echoes the matched origin, so this works. | `TestCORSKnownOriginAllowed` |
| CSP breaks Vidstack inline styles | The default CSP allows `'unsafe-inline'` for `style-src` (Vidstack requirement). Documented as the only inline allowance. | n/a |
| API exposed to the public internet without Caddy | The API listens on plain HTTP; `cookie_secure=true` would fail to set cookies cross-network. Operators are guided to put SOMETHING in front. The boot validator in §7 catches the misconfig that would silently break logins. | `TestCookieSecureFalseFailsBoot` |
| WebSocket auth via cookie cross-origin | Browsers do NOT send `mkt_sess` to a different host on `Upgrade` unless `SameSite=None; Secure`. We document that cross-origin WS requires bearer tokens, not cookies. | docs |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| Caddy | latest stable | TLS termination. |
| `github.com/gorilla/websocket` | already | WS upgrader. |

No new heavy deps.

## 11. Acceptance checklist

**TLS**
- [ ] AC-1: `make dev` brings up Caddy fronting api/streaming/web; `https://maktaba.local` serves the SPA.
- [ ] HTTP→HTTPS redirect on port 80.

**HSTS**
- [ ] AC-2: HSTS header present on TLS responses; max-age 31536000; includeSubDomains.

**Cookies**
- [ ] AC-3: `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/` on auth cookies (already covered by Story 10.2; this story adds the boot validator).
- [ ] `cookie_secure=false` outside `MAKTABA_DEV=1` refuses to start.

**CORS**
- [ ] AC-4: known origin → headers set; unknown origin → no headers; preflight → 204 with allow-list.

**Headers**
- [ ] AC-5: `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Cross-Origin-Opener-Policy: same-origin`, `Content-Security-Policy: <baseline>`.

**WebSocket**
- [ ] Upgrade rejects unknown / missing origins.

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] README.md ticks story 10.15.
- [ ] Operations doc covers Caddy local-CA trust and the production HSTS preload-ready domain caveat.
