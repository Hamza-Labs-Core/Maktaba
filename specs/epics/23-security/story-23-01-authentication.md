# Story 23.1 — Authentication

Two surfaces (web cookies, mobile/TV bearer tokens), one identity
table, modern password hashing, and rotation-friendly JWT signing.

## Acceptance criteria

- AC1. Passwords hashed with `argon2id`, parameters configurable but
  default to RFC 9106 second recommendation (`memory=65536KiB,
  iterations=3, parallelism=1`); rehash on login when params change.
- AC2. Web flow: login sets `httpOnly Secure SameSite=lax` session
  cookie; CSRF tokens required for any state-changing request and
  validated against the session.
- AC3. Native flow: login returns short-lived bearer JWT (RS256, 15
  min) + opaque refresh token (30 d). Refresh tokens are stored
  hashed in DB; rotation revokes the previous refresh.
- AC4. JWKS published at `/api/.well-known/jwks.json`; key rotation
  rolls every 90 days with a 30-day overlap; the streaming service
  caches JWKS for ≤ 5 min.
- AC5. Single-user mode: `MAKTABA_ADMIN_TOKEN` env-supplied bearer
  bypasses the user table entirely; the synthetic admin's `user_id`
  equals the sentinel UUID
  (`00000000-0000-0000-0000-000000000001`) defined in
  [Story 19.8](../19-scalability/story-19-08-multi-tenant-readiness.md);
  the UI stores the admin token after first boot. This path is
  feature-flagged off when `auth.multi_user = true`.

## Test cases

- TC1. Hashing: a known password produces a hash that verifies; an
  argon2id param bump on login transparently re-hashes and stores.
- TC2. Token rotation: refresh once, the previous refresh is invalid;
  reusing it returns 401 and the family is revoked (refresh-token
  reuse detection).
- TC3. JWKS rollover: rotate the signing key; existing access tokens
  continue to validate until expiry; new tokens are signed by the
  new key; streaming validates both during the overlap.

## Edge cases

- EC1. Clock skew between API and Streaming — JWT validation has a 30
  s `nbf` / `exp` skew tolerance; documented.
- EC2. Lost refresh token — user logs in fresh; the lost token's
  family is revoked on next attempted use.
- EC3. Admin token leaked — there is exactly one admin token; rotating
  it requires an env change and a service restart; documented in the
  ops guide.
