# Story 10.3 — Native login (JWT access + refresh)

Mobile/desktop/TV clients log in once, then carry a short-lived JWT and
refresh it (§9.8). Schema for `refresh_tokens` is in
[README.md](README.md).

**AC-1 — Login.**
- **Given** valid credentials with `Accept: application/json` and
  `X-Maktaba-Client: native`,
- **When** `POST /api/auth/login` is processed,
- **Then** the response is `{access_token: <JWT>, access_expires_in,
  refresh_token: <opaque>, refresh_expires_in, user}`. No cookies set.

**AC-2 — JWT shape.**
- **Given** an issued access token,
- **When** decoded,
- **Then** the claims include `iss="maktaba"`, `aud="api"`, `sub=user_id`,
  `iat`, `exp = iat + 900` (15 min default), `jti=<uuid v7>`, `is_admin`,
  `kid=<key id>`, **`lib=[<library_id>, ...]`** (the set of libraries
  the user has read access to at issue time, used by Streaming for
  offline authorization per Epic 8 Story 8.1 AC-1 — see also Story
  10.8 for signed-URL minting). RS256 signed.

**AC-3 — Bearer auth.**
- **Given** a request with `Authorization: Bearer <jwt>`,
- **When** an authenticated handler runs,
- **Then** the JWT is verified (signature + exp + aud), the `sub` is the
  user, the `jti` is recorded for audit, and the request proceeds.

**AC-4 — Opaque refresh tokens.**
- **Given** a refresh token,
- **When** stored,
- **Then** the token value is a 32-byte url-safe random string; only its
  argon2id hash is persisted in `refresh_tokens` (schema in
  [README.md](README.md)). The plaintext is returned only at issue time.

**Test cases:**
- Integration: login → access token decodes to expected claims,
  including a non-empty `lib` array for a user with library access.
- Integration: a tampered JWT signature → 401.
- Integration: an expired access token → 401 `type: token-expired`; the
  client is expected to refresh.
- Integration: a user with no library access has `lib: []`; Streaming
  rejects all signed URLs for them.

**Edge cases:**
- Skewed device clock — the JWT's `iat` may be slightly future to the
  server. Acceptance leeway `clock_skew_leeway_sec` (default 60) on `nbf`
  and `exp`.
- A native client that misses `X-Maktaba-Client: native` and is therefore
  given cookies — this is acceptable; the API supports both flows on
  one endpoint based on the header.
- A user's library access changes after issue — the `lib` claim is a
  snapshot; revocation has up to `access_ttl_sec` (15 min) lag for
  in-flight signed URLs. Documented in Story 10.5.
