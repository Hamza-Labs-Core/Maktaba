# Story 25.2 — Email + password registration

> Epic 25 · Cloud relay · Phase 1 (identity)

## Description

A user can create a Maktaba Cloud account with an email address and a
password, verify ownership of the email, and sign in. This is the
identity baseline that OAuth (25.3, 25.4), server claim (25.6), and
billing (25.13) all build on.

Concrete behaviors:

- **Sign up.** `POST /api/auth/register` with `{email, password,
  display_name, locale, accept_tos: true}`. Email is normalized
  (lowercase, trim, NFKC; reject invalid per RFC 5321 length and
  RFC 5322 syntax). Password is hashed with argon2id (`m=64MB,
  t=3, p=1`) and stored in `cloud_users.password_hash`. Account
  starts in state `pending_verification`; sessions are not yet
  issued.
- **Email verification.** A signed verification token (HMAC, 24h
  TTL) is emailed; clicking the link `POST`s
  `/api/auth/verify-email` with the token and the account moves to
  `active`. Re-sends are rate-limited to 1 per minute / 5 per
  hour per `(user, ip)`.
- **Login.** `POST /api/auth/login` with `{email, password}`.
  Returns `{access_token, refresh_token, expires_in, user}` on
  success. `access_token` is a 1h RS256 JWT with claims `{sub,
  email, plan, kid}`; `refresh_token` is a 30-day opaque token
  whose hash sits in `cloud_sessions`.
- **Refresh.** `POST /api/auth/refresh` with the refresh token in
  `Authorization: Bearer` rotates the refresh token (single-use;
  the old hash is marked revoked) and issues a fresh access
  token. This thwarts replay and detects token theft (if a stolen
  refresh token is used after rotation, the legitimate user's
  next refresh fails and we force re-auth).
- **Logout.** `POST /api/auth/logout` revokes the calling
  refresh token; `?everywhere=1` revokes all sessions for the
  user.
- **Lockout.** 10 failed logins from any IP for the same email
  in a 15-minute window pause login attempts for 30 minutes
  (constant 401 with `Retry-After`); successful login resets the
  counter. Lockout never reveals whether the email exists
  (timing-safe response).
- **Forgot password.** `POST /api/auth/forgot-password` with
  `{email}` always returns 200 (never reveals existence). If the
  account exists, an email goes out with a 1-hour reset link.
  `POST /api/auth/reset-password` validates the token and sets a
  new hash; all existing sessions revoked.
- **Audit.** Every state change writes to `cloud_audit` with
  `actor=user_id`, `action=auth.register|verify|login|reset|logout`,
  `ip`, `ua`.

## Acceptance criteria

- **Given** a fresh email and a 12-character password,
  **when** the client `POST`s `/api/auth/register`,
  **then** the response is `202 Accepted` with body
  `{"status":"pending_verification"}` and a verification email is
  enqueued.
- **Given** a registered user clicks the email link within 24 h,
  **when** the cloud receives `POST /api/auth/verify-email`,
  **then** the user moves to `active`, the response is `200 OK`,
  and an audit row is written.
- **Given** a verified user with valid credentials,
  **when** they `POST` `/api/auth/login`,
  **then** the response includes a 1h JWT access token and a
  30-day refresh token; the JWT validates against the JWKS at
  `/.well-known/jwks.json`.
- **Given** an attacker submits a wrong password,
  **when** they `POST` `/api/auth/login`,
  **then** the response is `401 Unauthorized` with body
  `{"error":"invalid_credentials"}`; response time is constant
  ±5ms (timing-safe).
- **Given** 10 failed logins for `victim@example.com` in 15 min,
  **when** the 11th attempt arrives,
  **then** the response is `429 Too Many Requests` with header
  `Retry-After: 1800`.
- **Given** a refresh token has been rotated,
  **when** the *original* token is presented again,
  **then** the response is `401`, all sessions for the user
  are revoked, and a `cloud_abuse_events` row is written with
  `kind=refresh_token_replay`.
- **Given** the email format is invalid,
  **when** the client `POST`s `/api/auth/register`,
  **then** the response is `400` with `{"error":"invalid_email"}`.
- **Given** a password is shorter than 10 chars or matches a
  top-1000 leaked-password Bloom filter,
  **when** the client `POST`s `/api/auth/register`,
  **then** the response is `400 weak_password`.
- **Given** an unverified account tries to log in,
  **when** the client `POST`s `/api/auth/login`,
  **then** the response is `403` with
  `{"error":"email_not_verified","resend_url":"/api/auth/verify-email/resend"}`.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | argon2id with ref params | hash a known password | hash matches expected pattern, verify returns true |
| T02 | unit        | leaked-password Bloom filter loaded | check `password123` | `weak=true` |
| T03 | integration | empty DB | register, verify, login | end-to-end happy path produces tokens |
| T04 | integration | clock advanced 25h | verify with old token | 410 `verification_token_expired` |
| T05 | integration | 10 wrong passwords for same email | 11th attempt | 429 with Retry-After |
| T06 | integration | refresh once, then retry old | retry | 401 + all sessions revoked + audit row |
| T07 | unit        | timing | 1000 wrong-pw + 1000 unknown-email | mean response time delta < 1ms |
| T08 | integration | resend verification 6 times in 1h | 6th call | 429 |
| T09 | integration | reset password | old session → API call | 401 (sessions revoked) |
| T10 | integration | register with email containing unicode (`mö@x.com`) | post | normalized lowercased, NFKC-folded, stored as `mö@x.com` |
| T11 | regression  | concurrent register with same email | two POSTs | one succeeds, the other gets 409 `email_taken` |
| T12 | unit        | JWT signed by old `kid` | verify after rotation | still valid until expiry; new tokens use new `kid` |

## Edge cases

- **Email enumeration.** Registration, login, forgot-password, and
  resend-verification all return identical responses regardless of
  whether the email exists. Cookies / tokens differ only when the
  flow legitimately produces a new resource.
- **Plus-tagged emails.** `user+test@example.com` is treated as a
  distinct address from `user@example.com` (RFC 5233). Document
  this — it's intentional, not a bug.
- **Password too long.** Argon2 has no practical max but we cap at
  256 chars to bound memory; longer → 400.
- **Account already verified, reverify clicked.** Token still
  valid → 200, no-op; expired → 410. Never an error in user-facing
  copy.
- **Unicode display name.** NFKC-normalize and reject control
  characters; cap at 80 graphemes.
- **JWKS rotation.** Two `kid`s active at once (current + previous);
  previous kept until max access-token lifetime (1h) elapses then
  removed. JWKS endpoint cached 5 minutes by Cloudflare.
- **Refresh token theft window.** A stolen refresh token is good
  until either (a) it's used and rotation happens, or (b) the
  legitimate user does a `logout?everywhere=1`. We do *not*
  implement device fingerprinting for v1.
- **Email provider bounces.** Hard bounces flag the account
  `email_bounced=true`; account is *not* suspended but verify-resend
  paths show "your provider rejected our email — switch email or
  contact support". Bounce data fed by Postmark webhooks (out of
  scope for this story; lands in 25.X observability).
- **Time skew.** JWT clock skew tolerance is 60s. Tokens issued
  inside that window survive replicas with skewed clocks.
- **TOS / privacy.** `accept_tos: true` is required; we record
  `tos_version_accepted` so a future TOS change can re-prompt.

## Files / packages

- `cloud/internal/auth/{registration,login,refresh,reset}.go`
- `cloud/internal/auth/argon2.go` — wrapper around
  `github.com/alexedwards/argon2id`.
- `cloud/internal/auth/jwks.go` — RS256 keypair loader, rotation,
  `/.well-known/jwks.json` handler.
- `cloud/internal/email/templates/{verify,reset}.html.tmpl` — i18n
  via `golang.org/x/text/message`.
- `cloud/internal/email/send.go` — Postmark adapter (swappable).
- `cloud/migrations/00020002_email_verification.sql` — adds
  `email_verified_at`, `password_changed_at`.

## Open questions

- **2FA / TOTP.** Out of scope for v1. Track as a v2 risk: paid
  users should get TOTP at minimum.
- **Magic-link login.** Defer; OAuth (25.3, 25.4) covers the
  no-password use case.
- **Email provider.** Postmark is assumed; SES and Mailgun work too.
  The interface allows swap without code changes.
