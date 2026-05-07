# Story 10.11 — Brute-force / credential-stuffing protection

Login and refresh endpoints need throttling beyond plain rate-limit
(Epic 7 Story 7.19).

**AC-1 — Per-username lockout.**
- **Given** N failed logins for `username = X` in a window,
- **When** N exceeds `max_failed_logins_per_username` (default **5**)
  within `failed_login_window_sec` (default **900 s** — 15 minutes),
- **Then** subsequent login attempts for that username — *with any
  password* — are rejected with 423 `type: account-locked` until the
  window passes. Successful logins reset the counter.
- The 15-minute window catches slow credential-stuffing attacks; the
  shorter 5-minute window suggested by NFR Story 23.6 was reconciled in
  favor of 15 minutes here. NFR Story 23.6 must align to this same
  value.

**AC-2 — Per-IP lockout.**
- **Given** N failed logins from `ip = Y` against any username in a
  window,
- **When** N exceeds `max_failed_logins_per_ip` (default 20) within
  `failed_login_window_sec` (default 900 s),
- **Then** further logins from that IP are throttled with 429 +
  exponentially-increasing `Retry-After`.

**AC-3 — No user enumeration.**
- **Given** a login attempt for an unknown username,
- **When** processed,
- **Then** the timing matches the wrong-password path (Story 10.2 AC-3),
  the response shape is identical (`type: invalid-credentials`), and the
  per-IP counter is incremented (per-username counter is not, since the
  username does not exist).

**AC-4 — Audit on lockout.**
- **Given** a lockout fires,
- **When** the response is returned,
- **Then** an audit row `category='security',
  event='lockout-username'|'lockout-ip', payload={target, count, window}`
  is written.

**Test cases:**
- Integration: 5 failed logins for `alice` in <15 min → 6th request 423;
  valid login from a different IP for `alice` is also locked
  (per-username, not per-IP).
- Integration: per-IP lockout from 20 wrong attempts across users in 15
  min.
- Security: timing of unknown-user vs wrong-password is within 50 ms.

**Edge cases:**
- A legitimate user fat-fingers 5 times — locked out for 15 min.
  Document the "wait it out" path and the admin-reset path
  (`POST /api/users/{id}/unlock` from Story 10.1 AC-3).
- Distributed credential stuffing across many IPs — per-IP lockout helps
  but isn't enough; the per-username lockout catches it.
