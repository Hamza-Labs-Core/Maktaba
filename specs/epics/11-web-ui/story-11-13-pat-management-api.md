# Story 11.13 — Personal Access Token (PAT) management

**Status:** **NEW** — added in response to
[REVIEW §3.4](../../REVIEW.md): Story 11.6 ("PAT management for clients")
referenced an API surface that no story owned. This story owns it
end-to-end.

A Personal Access Token is a long-lived bearer credential that a user
issues from Settings → Account → Tokens for use by automation, third-party
clients, or scripts. PATs are scoped, named, and revocable. They are
never auto-issued; they always represent an explicit user action.

**Anchors:** [`architecture.md` §9.8 (auth)](../../architecture.md), §11.5
(secrets). Touches Epic 10 (auth foundation): the Bearer-token
verification path is reused; PAT issuance and revocation are net-new.

## AC

### Schema

- New table `personal_access_tokens`:
  - `id UUID PRIMARY KEY`
  - `user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE`
  - `name TEXT NOT NULL` (user-supplied label; unique per user)
  - `prefix TEXT NOT NULL` (first 8 chars of the token, displayed in UI)
  - `hash BYTEA NOT NULL` (Argon2id hash of the full token)
  - `scopes TEXT[] NOT NULL DEFAULT '{}'` (`read`, `write`, `admin`)
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `last_used_at TIMESTAMPTZ`
  - `expires_at TIMESTAMPTZ` (nullable; default 1 year from issuance)
  - `revoked_at TIMESTAMPTZ`
  - `UNIQUE (user_id, name)`
- Index `(user_id, revoked_at)` for fast active-list lookups.
- Migration story owner: this story (carries the migration).

### Endpoints

- `POST /api/me/tokens {name, scopes[], expires_at?}` →
  `201 {id, name, scopes, expires_at, token}`. The plaintext `token`
  field is returned **once and only once**; subsequent reads return only
  metadata. Token format: `mkt_pat_<32 base32 random chars>`.
- `GET /api/me/tokens` → `200 {items: [{id, name, prefix, scopes,
  created_at, last_used_at, expires_at, revoked_at}]}`. Excludes the
  hash; includes both active and revoked tokens within the last 30 days.
- `DELETE /api/me/tokens/{id}` → `204` (sets `revoked_at = now()`); idempotent.
- Admin endpoint `GET /api/users/{id}/tokens` → list any user's PATs
  (`admin` scope or `is_admin = true` required).

### Validation & verification

- Bearer requests with a token starting `mkt_pat_` are routed to the PAT
  verifier (not the JWT verifier).
- Verifier resolves prefix → row → Argon2id verify → checks
  `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`.
- On verify success, set `last_used_at = now()` (debounced to ≤ 1 write per
  minute per token).
- A revoked or expired token returns `401 token-revoked|token-expired`.
- Scope enforcement: if the endpoint requires a scope the token doesn't
  hold, return `403 insufficient-scope`.

### Security

- Tokens are never logged; the redaction filter in
  Epic 21 Story 21.1 must mask anything starting `mkt_pat_`.
- The plaintext token is returned over TLS only; an HSTS header is required.
- Rate limits per user on `POST /api/me/tokens`: 10/hour (prevents
  enumeration / spam).
- Admin PAT enumeration writes an `audit_log` row
  (canonical audit table per [REVIEW §1.1.f](../../REVIEW.md)) with
  `category = 'pat'`.

## TC

- Issue a PAT named "ci-runner" with scopes `["read"]`: response carries
  the plaintext; subsequent `GET /api/me/tokens` lists it without the
  plaintext.
- Use the PAT in `Authorization: Bearer mkt_pat_xxx` against
  `GET /api/videos`: succeeds.
- Use the same PAT against `POST /api/libraries`: 403 (lacks `admin`).
- Revoke the PAT: subsequent use returns 401 `token-revoked`.
- Issue 11 PATs in an hour: the 11th returns 429.
- Force-expire a PAT (`UPDATE … SET expires_at = now()`): next use returns
  401 `token-expired`.

## EC

- Two PATs with the same `prefix` (1-in-2^40 collision): verifier still
  iterates all matching rows for Argon2id and picks the one that matches
  hash; documented but vanishingly rare.
- A PAT with no `expires_at` and `last_used_at` older than 90 days: a
  background job sends an email reminder (Epic 22; out of v1 scope).
- Leaked PAT replayed from a different IP: no automatic block in v1, but
  Story 21.6 logs the unusual IP for admin review.
- User deleted while PAT active: cascade delete removes all PATs in the
  same transaction.
