# Story 10.15 — Transport security

TLS, HSTS, secure cookies, CORS, security headers.

**AC-1 — Caddy front by default.**
- **Given** the docker-compose deployment,
- **When** the stack boots,
- **Then** Caddy terminates TLS (auto-issuing via internal CA on `.local`
  hostnames or Let's Encrypt on real domains) and proxies `/api`,
  `/graphql`, `/ws`, `/stream`, `/` to the appropriate backend. The Go
  binaries listen on plain HTTP behind Caddy.

**AC-2 — HSTS.**
- **Given** a TLS-served response,
- **When** the response is built,
- **Then** the header `Strict-Transport-Security: max-age=31536000;
  includeSubDomains` is set on every response. (Configurable; off for
  pure-`.local` setups where the cert isn't browser-trusted.)

**AC-3 — Cookie attributes.**
- **Given** any auth cookie set by Story 10.2,
- **When** inspected,
- **Then** `Secure`, `HttpOnly` (where appropriate), `SameSite=Lax`,
  `Path=/` are set. `Secure` may be unset only when `MAKTABA_DEV=1`
  (logged loudly).

**AC-4 — CORS.**
- **Given** a request from a known origin in `[server].cors_allowed_origins`,
- **When** received,
- **Then** the appropriate `Access-Control-Allow-*` headers are set;
  preflight `OPTIONS` returns 204 with the allow-list of methods and
  headers. Unknown origins → no CORS headers (request fails browser-side).

**AC-5 — Security response headers.**
- **Given** any response,
- **When** built,
- **Then** the API sets:
  - `X-Content-Type-Options: nosniff`,
  - `Referrer-Policy: strict-origin-when-cross-origin`,
  - `Cross-Origin-Opener-Policy: same-origin`,
  - `Content-Security-Policy: <strict baseline>` for the SPA shell.

**Test cases:**
- Integration: a request to `/` returns the CSP and HSTS headers.
- Integration: an unknown CORS origin is silently denied.
- Integration: cookie attributes correct in headers.
- Security scan: `curl --insecure https://maktaba.local/api/system/health`
  on the dev TLS cert succeeds; without `--insecure` only when the
  Caddy local CA cert is trusted.

**Edge cases:**
- A reverse proxy that rewrites `Host` and breaks Caddy's automatic
  cert — operations doc covers `--trust-fc` style flags and known fixes.
- A WebSocket on a non-TLS dev origin — the SPA tries `wss://` first,
  falls back to `ws://` if `MAKTABA_DEV=1`.
