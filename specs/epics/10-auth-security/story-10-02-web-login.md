# Story 10.2 — Web login (cookie + CSRF)

Web clients log in once and ride a short-lived session cookie. Schema
for `web_sessions` is in [README.md](README.md).

**AC-1 — Login flow.**
- **Given** valid `username + password`,
- **When** `POST /api/auth/login` is sent (JSON body),
- **Then** the server creates a `web_sessions` row, sets two cookies:
  - `mkt_sess` = opaque session id, `httpOnly`, `secure`, `samesite=lax`,
    `path=/`, `max-age=auth.web_session_ttl_sec` (default 28 days),
  - `mkt_csrf` = random 32-byte token, `secure`, `samesite=lax`, **not**
    `httpOnly` (the SPA reads it),
  and the response body is `{user: {id, username, is_admin}}`.

**AC-2 — Authenticated requests.**
- **Given** a request with a valid `mkt_sess` cookie,
- **When** an authenticated handler runs,
- **Then** the user identity is loaded from the session; the session's
  `last_seen_at` is bumped (debounced to once per minute per session).

**AC-3 — Wrong credentials.**
- **Given** an invalid login,
- **When** processed,
- **Then** the response is `401 Unauthorized` problem+json with a
  generic `type: invalid-credentials` message (don't differentiate
  unknown-user vs wrong-password) and an artificial 500 ms minimum
  delay (timing attack mitigation).

**AC-4 — Session expiry.**
- **Given** a session row whose `expires_at < now()`,
- **When** any request uses it,
- **Then** the request is treated as anonymous (401); the cookie is
  cleared via `Set-Cookie: mkt_sess=; max-age=0`.

**Test cases:**
- Integration: full login flow → cookies set with the correct attributes.
- Integration: tampered `mkt_sess` (changed by 1 char) → 401 + cookie
  cleared.
- Integration: timing attack — user-not-found and wrong-password both
  take ~500 ms (within 50 ms variance).

**Edge cases:**
- Multiple browser tabs: same session cookie shared. Logout in one tab
  invalidates the session for all.
- Cookie with `samesite=lax`: GET cross-site navigation works (deep
  links into Maktaba); POST cross-site does not (CSRF guard, Story
  10.10).
- Reverse proxy strips cookies: documented setup error; the API
  returns 401 with `Maktaba-Hint: cookies-missing-check-proxy`.
