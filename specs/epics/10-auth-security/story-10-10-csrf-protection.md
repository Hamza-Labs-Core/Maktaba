# Story 10.10 — CSRF protection (web only)

Cookie-based auth (Story 10.2) is vulnerable to CSRF; the bearer-JWT
path is not. This story implements the double-submit-cookie pattern.

**AC-1 — CSRF token issuance.**
- **Given** a successful web login,
- **When** processed,
- **Then** the response carries `mkt_csrf=<32-byte random>` (Story 10.2
  AC-1).

**AC-2 — CSRF token check.**
- **Given** a state-changing request (POST/PUT/PATCH/DELETE) with the
  `mkt_sess` cookie,
- **When** processed,
- **Then** the request must carry `X-Maktaba-CSRF: <token>` whose value
  matches the `mkt_csrf` cookie. Mismatch or missing → 403 `type:
  csrf-failed`.

**AC-3 — Bearer-JWT path skips CSRF.**
- **Given** a request authenticated via `Authorization: Bearer …`,
- **When** processed,
- **Then** CSRF is not enforced (the bearer header itself is the
  proof-of-intent — CSRF can't set custom headers on cross-origin
  requests).

**AC-4 — Safe methods exempt.**
- **Given** GET/HEAD/OPTIONS,
- **When** processed,
- **Then** CSRF is not enforced.

**Test cases:**
- Integration: POST with cookie but no CSRF header → 403.
- Integration: POST with cookie and matching CSRF header → 200.
- Integration: POST with bearer token → 200 regardless of CSRF.
- Integration: a malicious site triggering a form POST cannot include
  the `X-Maktaba-CSRF` header (browsers forbid setting custom headers
  on simple form POSTs).

**Edge cases:**
- CSRF token rotation — the token rotates on each login but persists
  through a session; documented behavior. The SPA reads `mkt_csrf` from
  `document.cookie` once at boot.
- A user who clears cookies mid-session — next request returns 401
  (session unauthenticated), not 403.
