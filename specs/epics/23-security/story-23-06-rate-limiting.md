# Story 23.6 — Rate limiting, lockout, and destructive-action confirmation

Self-host doesn't mean "no abuse." A misbehaving client (or a
compromised account on a shared LAN) shouldn't take the box down. And
destructive admin actions need a typed confirmation, not just a single
click.

This story owns the canonical numbers for rate limits and the failed-
login lockout window. Earlier drafts that named different values
(`auth_rate_per_min=30`, `failed_login_window_sec=900`,
"5 failures in 15 minutes") are superseded by this story.

## Acceptance criteria

- AC1. Per-IP and per-route rate limits on the auth surface:
  - `POST /api/auth/login` — **10/min per IP** (the strictest path).
  - `POST /api/auth/refresh` — 60/min per IP.
  - Other `/api/auth/*` endpoints — 30/min per IP.
  All return structured `429` with `Retry-After`. The `auth_rate_per_min`
  config key in earlier drafts is replaced by this per-route table; a
  single value across the auth surface is no longer used.
- AC2. Per-user rate limits on expensive endpoints (search 60/min,
  bulk job submit 10/min).
- AC3. **Failed-login lockout.** Failed login attempts are tracked per
  `(user, ip)`; ≥ **5 failures within a 15-minute sliding window**
  lock the user for **15 minutes**. (The "5 minutes / 15 minutes"
  formulation in earlier drafts is replaced by the single 15-minute
  window so the window matches the lockout for clarity; the
  `failed_login_window_sec=900` value used by the code stays the
  same.) An audit row of category `auth` is written; an admin
  override (`POST /api/users/{id}/unlock`) clears the lock and writes
  a category-`admin` audit row.
- AC4. Limits are configurable; in single-user mode, defaults are
  relaxed (since one user owns the box) but never disabled.
- AC5. **Destructive-action confirmation token.** Endpoints that
  perform irreversible destructive operations require an explicit
  `confirm` field in the request body equal to a deterministic
  function of the resource:
  - `DELETE /api/libraries/{id}?purge=true` requires
    `confirm = library.name`.
  - `DELETE /api/users/{id}` requires `confirm = user.username`.
  - `POST /api/keys/rotate?immediate=true` requires
    `confirm = "rotate-immediate"`.
  Mismatch returns `412 Precondition Failed`. Successful execution
  writes an audit row of category `data` (or `admin` for users,
  `keys` for key rotation).

## Test cases

- TC1. Login burst: 11 failed login attempts from the same IP within
  60 s; the 11th is `429`; subsequent attempts continue to be `429`
  for the configured window.
- TC2. User lockout: 5 failed logins for one user within 15 minutes;
  the 6th attempt even with the correct password returns `423 Locked`;
  admin unlock clears it and emits the documented audit row.
- TC3. Search burst: 100 requests in 30 s from one user; the limiter
  responds with `429` after the threshold, never crashes.
- TC4. Confirm-token mismatch: `DELETE /api/libraries/{id}?purge=true`
  with the wrong `confirm` value returns `412` and writes nothing;
  with the correct value it succeeds and writes a `data` audit row.
- TC5. Per-route ceiling: a single client hits `/api/auth/login` 12×
  in 60 s and `/api/auth/refresh` 70× in 60 s from the same IP; the
  login limit fires at 11, refresh at 61, independently.

## Edge cases

- EC1. Behind a reverse proxy that strips `X-Forwarded-For` — the
  limiter falls back to the connecting IP; an admin warning is
  emitted on startup if proxy headers are required but absent.
- EC2. Legitimate burst from a multi-device household — per-user
  limits dominate over per-IP; documented.
- EC3. Distributed admin operations (bulk re-process) — exempt from
  user limits with explicit `admin: true` flag; audited.
- EC4. Confirm-token races: two admins concurrently submit identical
  confirm tokens for the same library — the second sees `404` after
  the first commits; both attempts are audited.
