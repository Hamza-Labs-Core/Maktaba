# Story 15.6 — API: device pairing endpoints

**Status:** **NEW** — added in response to
[REVIEW §3.2](../../REVIEW.md): the QR-pairing flow
([Story 15.5](story-15-05-qr-pairing.md)) referenced
`POST /api/auth/pair` with no implementation owner. This story owns the
pairing API surface end-to-end, including code TTLs, single-use
semantics, and the SPKI handshake required for TOFU pinning
([Story 15.2](story-15-02-cloud-relay.md)).

**Anchors:** [`architecture.md` §9.8 (auth)](../../architecture.md);
ties to Epic 10 Stories 10.3, 10.6.

## AC

### Schema

- New table `pairing_codes`:
  - `code TEXT PRIMARY KEY` (6 alphanumerics, base32 alphabet excluding
    `IL01`)
  - `nonce BYTEA NOT NULL` (32 bytes random; bound into the QR URL as
    `n=<base64url>`)
  - `created_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE`
  - `device_label TEXT`
  - `device_kind TEXT NOT NULL` (`mobile`, `desktop`, `tv`)
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `expires_at TIMESTAMPTZ NOT NULL`
  - `claimed_at TIMESTAMPTZ`
  - `claimed_by_device_id UUID REFERENCES devices(id) ON DELETE SET NULL`
- Indexes:
  - `(expires_at)` for the sweeper
  - `(created_by_user_id, claimed_at)` for "show my outstanding pairs"
- Migration owner: this story.

### Endpoints

- `POST /api/auth/pair {device_label?, device_kind}` →
  `201 {code, qr_url, expires_at}`
  - Authenticated; the code is bound to the caller's user.
  - `qr_url` is `https://{server}/pair?code={code}&mid={mdns_id}&spki={hash}&n={nonce}`.
  - `expires_at` defaults to `now() + 5 minutes`.
  - Rate limit: 5 codes per user per minute.
- `POST /api/auth/pair/claim {code, nonce, device_token?, app_version,
  os_version, locale}` →
  - `200 {access_token, refresh_token, user, server: {mdns_id, spki}}`
    on success.
  - `400 invalid-code | code-expired | code-already-claimed |
    nonce-mismatch` per failure mode.
  - The endpoint is unauthenticated (the code itself is the credential)
    but the response binds the device to the user that created the code.
  - `device_token`, when provided, is registered with the device fan-out
    table per [Story 12.10](../12-mobile/story-12-10-device-registration-api.md).
- `DELETE /api/auth/pair/{code}` → `204` revokes a still-pending code;
  user can only revoke their own. Idempotent.
- `GET /api/auth/pair` → list of the caller's pending and recently
  claimed codes (last 24 h).

### Background sweep

- A 30-second cron sets `claimed_at = expires_at` for any expired,
  unclaimed code (so reads can distinguish "expired" from
  "still pending"). Rows older than 7 days are hard-deleted.

### Security

- Codes are random over a 32-symbol alphabet of length 6 → ~30 bits;
  the rate limit + 5-minute TTL keeps brute force impractical
  (~30 yr at 5/min).
- The `nonce` is the second factor: a code alone is not enough; the
  claimer must echo the QR-bound nonce (which a shoulder-surfer reading
  the human code would not have).
- Server returns its current TLS SPKI hash so the client can pin it
  immediately (TOFU per [REVIEW §5.5](../../REVIEW.md)).
- The pairing endpoint runs through the same rate limit / WAF middleware
  as login.
- Audit-log row written on issue, claim, and revoke
  (`category = 'pair'`).

## TC

- TV authenticated user POSTs `/api/auth/pair`: 201 with `code`,
  `qr_url` containing the server's `mdns_id` + SPKI hash.
- Phone POSTs `/api/auth/pair/claim` with the right code and nonce:
  200 with tokens; the user's device list now contains the phone.
- Same code re-claimed: 400 `code-already-claimed`.
- Code 6 minutes old: 400 `code-expired`.
- Code claimed with wrong nonce: 400 `nonce-mismatch`; code remains
  unclaimed for legitimate use within TTL.
- 6th code in a minute: 429 rate-limited.

## EC

- TLS cert rotation between issue and claim: the `spki` in the QR no
  longer matches the live cert. The claim still succeeds (the
  claim endpoint returns the *current* SPKI in its response); the
  client surfaces the rotation to the user via "Server certificate
  was renewed during pairing — confirm new identity?".
- The user revokes the code while the phone is mid-claim: the claim
  fails with `code-revoked`; the phone surfaces "Code was cancelled —
  try again".
- The created-by user is deleted between issue and claim: cascade
  removes the row; claim fails with `invalid-code`.
- Two phones race to claim the same code: only one succeeds (DB
  constraint on `claimed_at IS NULL`).
