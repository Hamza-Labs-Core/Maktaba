# Plan 10.15 — Transport security — implementation

> Implementation plan for [story-10-15-transport-security.md](story-10-15-transport-security.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the cookie helper here is consumed by
> Story 10.2 (web login) cookie set; the CORS allowlist comes from the
> server config in Plan 10.14; HSTS is preserved end-to-end so Streaming
> (Epic 8) inherits the same posture.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Caddy in front, Go services on plain HTTP behind it.** Caddy terminates TLS, sets `Strict-Transport-Security` for browser-trusted cert paths, and proxies `/api`, `/graphql`, `/ws`, `/stream`, `/` to API/Streaming/Web. The Go binaries listen on `127.0.0.1:80xx` and trust `X-Forwarded-*` from Caddy only. | Story AC-1. | Letting Caddy handle ACME, OCSP, and HTTP/3 is much cheaper than re-implementing in Go. The plain-HTTP backend avoids cert-rotation rituals inside Go. |
| D2 | **Security headers are set by the Go middleware AND by Caddy** (defence-in-depth). If Caddy is bypassed (debug curl on the loopback port), the API still sets `X-Content-Type-Options`, `Referrer-Policy`, `COOP`, and CSP. Caddy adds HSTS only for HTTPS-served responses. | Story AC-5. | Header drift between Caddy and Go is the most common posture regression in homelab deployments. Setting both is one extra header per response (~80 bytes) — negligible. |
| D3 | **Cookie-attribute helper enforces `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/` by default.** `Secure` may be stripped only when `os.Getenv("MAKTABA_DEV") == "1"`, with a `slog.Warn("INSECURE-COOKIE-MODE-ENABLED")` line per minute (rate-limited so the log isn't spammed). | Story AC-3. | A central helper avoids per-handler drift. The loud log makes accidental dev-mode-in-prod immediately visible to operators. |
| D4 | **CORS is a bespoke middleware reading the runtime allowlist.** Origin matching is exact string compare (no regex, no wildcards in v1). Preflight returns 204 with `Access-Control-Allow-{Origin,Methods,Headers,Credentials}` and `Vary: Origin`. Unknown origins → no CORS headers (browser blocks). | Story AC-4. | Wildcards interact badly with `credentials: include`. Exact match keeps the policy obvious. |
| D5 | **HSTS posture is environment-aware.** Caddy sends HSTS only for non-`.local` HTTPS responses (config flag `enable_hsts_local: false` by default). The Go middleware never emits HSTS — Caddy is the authority for transport-layer headers. | Story AC-2. | Browsers cache HSTS aggressively; pinning a short-lived `.local` cert into HSTS strands users when the cert rotates. |
| D6 | **CSP baseline is strict for the SPA shell**: `default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' wss: ws:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`. The API itself emits a more restrictive policy (`default-src 'none'; frame-ancestors 'none'`). The CSP nonce mechanism is deferred to Epic 14 (web). | Story AC-5. | A baseline strict-CSP catches obvious script-injection regressions; the SPA team can relax via `Content-Security-Policy-Report-Only` during migration. |

If D2 is rejected (Go-only headers): a dev who curls the loopback port directly sees no headers and writes a "missing CSP" bug. If D2 is rejected (Caddy-only headers): a Caddy misconfiguration silently drops CSP and we don't notice until a CSP scanner runs. Both is correct.

---

## 1. Architecture diagram

```
                                  ┌─────────────────────────────────┐
   public TLS ──443──> Caddy ───►│ X-Forwarded-Proto: https        │
                                  │ HSTS, TLS, CSP (baseline)       │
                                  │ proxy_pass:                     │
                                  │   /api      → 127.0.0.1:8080    │
                                  │   /graphql  → 127.0.0.1:8080    │
                                  │   /ws       → 127.0.0.1:8080    │
                                  │   /stream   → 127.0.0.1:8081    │
                                  │   /         → 127.0.0.1:8090    │
                                  └─────────────────────────────────┘
                                         │
                                         ▼
   ┌────────────────────────────────────────────────────────────────┐
   │ chi mux                                                         │
   │   securityheaders.Middleware                                    │
   │     X-Content-Type-Options nosniff                              │
   │     Referrer-Policy strict-origin-when-cross-origin             │
   │     Cross-Origin-Opener-Policy same-origin                      │
   │     Content-Security-Policy <api or spa baseline> (D6)          │
   │   cors.Middleware (config-driven allowlist) (D4)                │
   │   redactlog.Middleware (Plan 10.14)                             │
   │   ... auth/Authz ...                                            │
   └────────────────────────────────────────────────────────────────┘
                                         │
                                         ▼
   handler that sets cookies → cookies.Set(w, "maktaba_session", val)
       Secure  = true   (always, except MAKTABA_DEV=1, with loud log)
       HttpOnly = true
       SameSite = Lax
       Path     = /
```

---

## 2. Detailed implementation

### 2.1 Package layout

```
api/
├── internal/
│   ├── auth/
│   │   ├── securityheaders/
│   │   │   └── middleware.go    // CSP, COOP, etc. (D2, D6)
│   │   ├── cookies/
│   │   │   └── cookies.go       // Set/Clear helpers (D3)
│   │   └── corsmw/
│   │       └── corsmw.go        // CORS middleware (D4)
└── cmd/api/main.go              // wires the chain
deploy/
└── caddy/
    ├── Caddyfile                // production
    └── Caddyfile.local          // .local CA
```

### 2.2 `Caddyfile` (D1, D2, D5)

```caddy
# deploy/caddy/Caddyfile
# Production deployment with public TLS via Let's Encrypt.
# Variables (env-substituted at boot):
#   MAKTABA_HOSTNAME   - public hostname, e.g. maktaba.example.com
#   MAKTABA_API_PORT   - 8080 by default
#   MAKTABA_STREAM_PORT - 8081 by default
#   MAKTABA_WEB_PORT   - 8090 by default

{
    # Global options.
    email ops@{$MAKTABA_HOSTNAME}
    servers {
        protocols h1 h2 h3
    }
}

{$MAKTABA_HOSTNAME} {
    encode zstd gzip

    # Security headers (D2): defence in depth alongside the Go middleware.
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options    "nosniff"
        Referrer-Policy           "strict-origin-when-cross-origin"
        Cross-Origin-Opener-Policy "same-origin"
        # CSP for the SPA shell (D6) — the API also sets a stricter one
        # for non-HTML responses.
        Content-Security-Policy   "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' wss: ws:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
        -Server
    }

    # Streaming Service (range requests, signed URLs).
    @stream path /stream/* /stream
    handle @stream {
        reverse_proxy 127.0.0.1:{$MAKTABA_STREAM_PORT} {
            transport http {
                read_buffer 64KB
            }
            header_up X-Forwarded-Proto https
        }
    }

    # API + GraphQL + WebSocket all hit the same Go binary.
    @api path /api/* /graphql /ws
    handle @api {
        reverse_proxy 127.0.0.1:{$MAKTABA_API_PORT} {
            header_up X-Forwarded-Proto https
            header_up X-Forwarded-For {client_ip}
            # WebSocket upgrade is automatic in Caddy v2.
        }
    }

    # SPA shell (everything else).
    handle {
        reverse_proxy 127.0.0.1:{$MAKTABA_WEB_PORT} {
            header_up X-Forwarded-Proto https
        }
    }

    # Hide sensitive paths from public access.
    @denied path /metrics /debug/* /pprof/*
    respond @denied 404
}
```

```caddy
# deploy/caddy/Caddyfile.local
# .local deployment with Caddy's internal CA. HSTS is OFF (D5) because
# the cert is not browser-trusted by default.

{
    local_certs
}

maktaba.local {
    tls internal

    encode zstd gzip

    header {
        X-Content-Type-Options    "nosniff"
        Referrer-Policy           "strict-origin-when-cross-origin"
        Cross-Origin-Opener-Policy "same-origin"
        # No HSTS for .local (D5).
        -Server
    }

    @stream path /stream/* /stream
    reverse_proxy @stream 127.0.0.1:8081

    @api path /api/* /graphql /ws
    reverse_proxy @api 127.0.0.1:8080

    handle {
        reverse_proxy 127.0.0.1:8090
    }
}
```

### 2.3 `securityheaders/middleware.go` (D2, D6)

```go
package securityheaders

import "net/http"

// Profile selects the CSP policy. APIs serve JSON; the SPA shell serves HTML.
type Profile int

const (
	ProfileAPI Profile = iota
	ProfileSPA
)

const (
	cspAPI = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'"
	cspSPA = "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; " +
		"connect-src 'self' wss: ws:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
)

// Middleware sets the security-header set described in AC-5.
// HSTS is deliberately NOT set here — Caddy owns that (D5).
func Middleware(profile Profile) func(http.Handler) http.Handler {
	csp := cspAPI
	if profile == ProfileSPA {
		csp = cspSPA
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Content-Security-Policy", csp)
			next.ServeHTTP(w, r)
		})
	}
}
```

### 2.4 `cookies/cookies.go` (D3)

```go
package cookies

import (
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	devMode      = os.Getenv("MAKTABA_DEV") == "1"
	devLogMu     sync.Mutex
	devLogLast   time.Time
	devLogMinGap = time.Minute
)

// Options for setting a cookie. Defaults satisfy AC-3.
type Options struct {
	Name     string
	Value    string
	MaxAge   time.Duration
	Path     string
	HTTPOnly bool
	SameSite http.SameSite
	// SecureForce overrides MAKTABA_DEV stripping (rarely needed).
	SecureForce bool
}

// Set writes the cookie with the platform's safe defaults.
//
//   Secure   = true   (stripped only when MAKTABA_DEV=1, logged loudly)
//   HttpOnly = caller's choice (default true via the helpers below)
//   SameSite = Lax    (caller may override)
//   Path     = "/"
func Set(w http.ResponseWriter, log *slog.Logger, o Options) {
	secure := true
	if devMode && !o.SecureForce {
		secure = false
		warnDevModeOnce(log)
	}
	path := o.Path
	if path == "" {
		path = "/"
	}
	sameSite := o.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	c := &http.Cookie{
		Name:     o.Name,
		Value:    o.Value,
		Path:     path,
		MaxAge:   int(o.MaxAge.Seconds()),
		Secure:   secure,
		HttpOnly: o.HTTPOnly,
		SameSite: sameSite,
	}
	http.SetCookie(w, c)
}

// Clear writes a deletion cookie matching Set's attributes.
func Clear(w http.ResponseWriter, log *slog.Logger, name string) {
	Set(w, log, Options{Name: name, Value: "", MaxAge: -1, HTTPOnly: true})
}

func warnDevModeOnce(log *slog.Logger) {
	devLogMu.Lock()
	defer devLogMu.Unlock()
	if time.Since(devLogLast) < devLogMinGap {
		return
	}
	devLogLast = time.Now()
	log.Warn("INSECURE-COOKIE-MODE-ENABLED",
		"reason", "MAKTABA_DEV=1",
		"impact", "Set-Cookie omits Secure attribute; only safe on localhost dev")
}
```

### 2.5 `corsmw/corsmw.go` (D4)

```go
package corsmw

import (
	"net/http"
	"strings"
)

// Config carries the runtime allowlist. Origins are matched by exact
// string equality; v1 supports no wildcards.
type Config struct {
	AllowedOrigins []string
	AllowedMethods []string // default: GET, POST, PUT, PATCH, DELETE, OPTIONS
	AllowedHeaders []string // default: Authorization, Content-Type, X-Maktaba-CSRF
	AllowCreds     bool     // default: true (cookies need this)
	MaxAgeSec      int      // default: 600
}

// Middleware returns a chi-compatible middleware that handles CORS.
// Unknown origins → no CORS headers (browser denies the request); we do
// NOT 403 because that would expose the allowlist via timing.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = struct{}{}
	}
	if len(cfg.AllowedMethods) == 0 {
		cfg.AllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	}
	if len(cfg.AllowedHeaders) == 0 {
		cfg.AllowedHeaders = []string{"Authorization", "Content-Type", "X-Maktaba-CSRF"}
	}
	if cfg.MaxAgeSec == 0 {
		cfg.MaxAgeSec = 600
	}
	methodsHdr := strings.Join(cfg.AllowedMethods, ", ")
	headersHdr := strings.Join(cfg.AllowedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
					if cfg.AllowCreds {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
				}
			}
			if r.Method == http.MethodOptions && origin != "" {
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Methods", methodsHdr)
					w.Header().Set("Access-Control-Allow-Headers", headersHdr)
					w.Header().Set("Access-Control-Max-Age", itoa(cfg.MaxAgeSec))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [11]byte
	bp := len(b)
	for n > 0 {
		bp--
		b[bp] = byte('0' + n%10)
		n /= 10
	}
	return string(b[bp:])
}
```

### 2.6 Wiring in `cmd/api/main.go`

```go
func buildRouter(cfg *secrets.Config, log *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(securityheaders.Middleware(securityheaders.ProfileAPI))
	r.Use(corsmw.Middleware(corsmw.Config{
		AllowedOrigins: cfg.Server.CORSAllowedOrigins,
		AllowCreds:     true,
	}))
	r.Use(redactlog.Middleware(log))
	// ... auth, Authz, handler routes ...
	return r
}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `deploy/caddy/Caddyfile` | production reverse-proxy config | `caddy validate` in CI |
| 2 | `deploy/caddy/Caddyfile.local` | local CA + maktaba.local | `caddy validate` |
| 3 | `api/internal/auth/securityheaders/middleware.go` | `Profile`, `Middleware` | `TestSecurityHeaders` |
| 4 | `api/internal/auth/cookies/cookies.go` | `Options`, `Set`, `Clear`, dev-mode warn | `TestCookieDefaults`, `TestCookieDevMode` |
| 5 | `api/internal/auth/corsmw/corsmw.go` | `Config`, `Middleware` | `TestCORSAllowedOrigin`, `TestCORSPreflight`, `TestCORSUnknownOrigin` |
| 6 | `cmd/api/main.go` | wires all three | smoke |

---

## 4. Test cases (keyed to ACs)

### AC-1 — Caddy front by default
- CI: `caddy validate --config deploy/caddy/Caddyfile` exits 0 (with required env vars stubbed).
- E2E (docker-compose): `curl --cacert <local-ca> https://maktaba.local/api/system/health` returns 200 + JSON body; the same call without `--cacert` fails with cert verification error.
- E2E: `/api/v1/auth/login` → reaches API; `/stream/<id>` → reaches Streaming.

### AC-2 — HSTS
- E2E: Caddy on a real domain sets `Strict-Transport-Security: max-age=31536000; includeSubDomains` on every response.
- E2E: Caddy on `.local` does NOT set HSTS (D5).

### AC-3 — Cookie attributes
- `TestCookieDefaults`: `cookies.Set(w, log, Options{Name:"x", Value:"y"})` → `Set-Cookie` header contains `Secure`, `HttpOnly` (when `HTTPOnly: true` is passed; default helpers set it), `SameSite=Lax`, `Path=/`.
- `TestCookieDevMode`: with `MAKTABA_DEV=1`, the cookie omits `Secure` and the logger receives a `INSECURE-COOKIE-MODE-ENABLED` warn line. Repeated calls within 1 minute log only once.
- E2E: web login (Story 10.2) sets `maktaba_session` with all four attributes correct in TLS mode.

### AC-4 — CORS
- `TestCORSAllowedOrigin`: GET with `Origin: https://app.example.com` (in allowlist) → response has `Access-Control-Allow-Origin: https://app.example.com`, `Vary: Origin`, `Access-Control-Allow-Credentials: true`.
- `TestCORSPreflight`: OPTIONS with `Origin: https://app.example.com` → 204 + Allow-Methods + Allow-Headers + Max-Age.
- `TestCORSUnknownOrigin`: Origin not in allowlist → no `Access-Control-Allow-Origin` header (browser will block); request still reaches the handler, but the response is unusable from the unknown origin.
- `TestCORSNoOrigin`: no Origin header (curl) → no CORS headers added; handler runs.

### AC-5 — Security response headers
- `TestSecurityHeaders`: every response from a chi mux wrapped with `Middleware(ProfileAPI)` carries `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Cross-Origin-Opener-Policy: same-origin`, and `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'; base-uri 'none'`.
- `TestSecurityHeadersSPA`: `ProfileSPA` switches the CSP to the SPA baseline.
- E2E: `curl https://maktaba.example.com/api/system/health` returns all the headers above plus HSTS (Caddy adds it).

---

## 5. Edge cases

| #   | Case | Handled by |
|-----|------|------------|
| E1  | A reverse proxy in front of Caddy that rewrites `Host`. Caddy's automatic ACME breaks. We document `--trust-fc` style flags and a `trusted_proxies` block in operations notes; not a code change. | Documented in `deploy/caddy/README.md`. |
| E2  | A WebSocket on a non-TLS dev origin. The SPA tries `wss://` first; if it fails AND `MAKTABA_DEV=1` is detected via the API's `/api/system/info` endpoint, falls back to `ws://`. | SPA-side; we document the contract. |
| E3  | A config-loaded CORS origin with trailing slash (`https://app.example.com/`). Browsers send `Origin: https://app.example.com` with no trailing slash → exact-match misses. We trim the trailing `/` at config-load time and log a warning. | Add a trim+warn in `corsmw.New` (test: `TestCORSConfigTrimSlash`). |
| E4  | A handler that sets a cookie via `http.SetCookie` directly bypasses `cookies.Set`. We add a `golangci-lint` rule forbidding `http.SetCookie` outside `internal/auth/cookies`. | CI lint. |
| E5  | An attacker sends `Origin: null` (e.g., from a sandboxed iframe). `null` is never in the allowlist; CORS headers are not added. | Falls out of D4. |
| E6  | An OPTIONS preflight arrives without any `Origin` header (some odd intermediaries strip it). We treat it as a normal request — no preflight response — and forward to the handler. | D4. |
| E7  | A `Set-Cookie: maktaba_session=…` line accidentally lacks `HttpOnly` because a handler passes `HTTPOnly: false` (e.g., Web's `csrf_token` cookie that the SPA reads). The helper still applies `Secure` and `SameSite=Lax`; the handler is responsible for the HTTPOnly choice. We document which cookies are HttpOnly. | Documented in cookies/README.md; tested per cookie. |
| E8  | Caddy ACME fails (rate limit). The site is unreachable; we surface this via Caddy's `acme.error` log + a healthcheck endpoint. Not in scope for v1; documented as an operations runbook entry. | Operations doc. |
| E9  | Cert renewal under load. Caddy renews ~30 days before expiry; the renewal is non-blocking. No application code change. | Caddy default. |
| E10 | A non-browser client (mobile app) hitting `/api/*` with no `Origin` header. CORS middleware is a no-op for these requests; the response carries no CORS headers; the auth header still works. | Falls out of D4. |
| E11 | An admin sets `MAKTABA_DEV=1` in production by mistake. The cookies log line `INSECURE-COOKIE-MODE-ENABLED` fires every minute and shows up in operations dashboards. Documented as an "alert on this line" rule. | D3 + operations doc. |

---

## 6. Acceptance checklist

- [ ] **A1** `deploy/caddy/Caddyfile` validates and proxies `/api`, `/graphql`, `/ws`, `/stream`, `/` to the right backend ports; `Caddyfile.local` uses Caddy's internal CA.
- [ ] **A2** Caddy emits `Strict-Transport-Security: max-age=31536000; includeSubDomains` on real domains; not on `.local`.
- [ ] **A3** `cookies.Set` enforces `Secure`, `HttpOnly` (caller-chosen, defaulting to true via per-cookie helpers), `SameSite=Lax`, `Path=/`. `MAKTABA_DEV=1` strips `Secure` and emits a rate-limited WARN log.
- [ ] **A4** `corsmw.Middleware` matches origins by exact string, sends `Access-Control-Allow-{Origin,Methods,Headers,Credentials}` on allowed origins, returns 204 on preflight, adds nothing on unknown origins.
- [ ] **A5** `securityheaders.Middleware` sets `X-Content-Type-Options`, `Referrer-Policy`, `Cross-Origin-Opener-Policy`, and a profile-appropriate `Content-Security-Policy`. HSTS is owned by Caddy.
- [ ] **A6** Documentation: `MAKTABA_DEV=1` is documented as the local non-TLS dev opt-out; `deploy/caddy/README.md` covers proxy-in-front-of-Caddy and `.local` cert trust.
- [ ] **A7** Integration: a curl to `https://maktaba.local` succeeds with `--cacert <local-ca>`; without the flag, only when the operator has trusted Caddy's local CA in the OS keychain.
- [ ] **A8** Integration: an unknown CORS origin is silently denied (no `Access-Control-Allow-Origin` header); the response status is unaffected.

---

## 7. Operations notes

- HSTS preload list: out of scope for v1. Self-hosted operators usually do not want preloading because it requires HTTPS forever on that hostname.
- HTTP/3: enabled in Caddy by default. No action.
- Cert rotation: Caddy handles it. Operators are alerted via `caddy renew_failed` logs.
- Local CA distribution: `deploy/caddy/README.md` includes `caddy trust` instructions for macOS/Linux/Windows operators who want browser-trusted dev certs.
