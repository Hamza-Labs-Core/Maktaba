# Story 25.6 — Server claim token flow

> Epic 25 · Cloud relay · Phase 2 (linking)

## Description

A user binds their self-hosted Maktaba Server to their cloud account
through a one-time claim-token exchange. The server-side half mints a
short-lived token; the cloud-side half consumes it; the result is a
durable bearer credential the server uses to maintain a tunnel
(25.7) and dispatch push (25.17). The flow is the cloud's gating
event — without a valid linkage, no relay or push runs.

The protocol:

1. **Generate (server side).** From the local admin UI ("Connect to
   Maktaba Cloud"), the local API service:
   - Generates a 40-bit random token (5 bytes), RFC-4648 base32-
     encoded as 8 chars (e.g., `K3F9-MZ7P`). Token TTL is 10
     minutes; the 40-bit entropy is paired with a per-IP rate
     limit + single-use redemption (see plan-25-06 §5 brute-force
     math). Collisions in the 10-min window are vanishingly
     improbable but still checked on insert.
   - Computes its own Ed25519 fingerprint (Epic 10 Story 10.18)
     and includes it in the registration request.
   - `POST`s to `https://api.maktaba.app/api/servers/claim/init`
     with `{token_hash: HMAC-SHA256(token, server_secret),
     server_pubkey, server_version, server_locale}`. The cloud
     responds with `{claim_id}` and stores a row in
     `cloud_claim_tokens` with `expires_at = now()+10min`.
   - Displays the token to the user as `K3F9-MZ7P`. The local
     UI shows a QR code that encodes
     `maktaba-claim://K3F9-MZ7P/{server_pubkey_b64}`.

2. **Redeem (cloud side).** The user, signed in to
   `app.maktaba.app`, enters the token (or scans the QR). The
   client `POST`s `/api/servers/claim` with `{token: "K3F9-MZ7P",
   server_pubkey: "..."}`. The cloud:
   - Hashes the token, finds the matching `cloud_claim_tokens`
     row (where `redeemed_at IS NULL` and `expires_at > now()`).
     If no row: `404 claim_not_found`. If `expires_at`: `410
     claim_expired`. If `redeemed_at`: `409 claim_already_used`.
   - Verifies the `server_pubkey` field matches the one the
     server posted in init (defends against token-without-key
     replay).
   - In one transaction: inserts `cloud_servers` (user_id,
     server_pubkey, version, subdomain=NULL initially),
     inserts `cloud_server_tokens` (random 256-bit bearer,
     stored as bcrypt-of-bearer), marks the claim row redeemed.
   - Responds with `{server_id, server_token, cloud_endpoint:
     "wss://relay.maktaba.app/tunnel/v1/connect", entitlement: ...}`.

3. **Persist (server side).** The server stores `server_id` and
   `server_token` (encrypted-at-rest using its data key) and
   immediately opens the tunnel (25.7). The token never appears
   in any subsequent claim flow.

## Acceptance criteria

- **Given** a user runs "Connect to Maktaba Cloud" on a fresh
  server,
  **when** the server contacts cloud `/claim/init`,
  **then** a `cloud_claim_tokens` row is created with TTL 10
  min and the operator sees a token like `K3F9-MZ7P`.
- **Given** the user enters the token within TTL,
  **when** the cloud redeems it,
  **then** `cloud_servers` and `cloud_server_tokens` rows are
  created in one transaction and the response includes
  `server_token`.
- **Given** the user enters the token after 10 minutes,
  **when** the cloud looks it up,
  **then** the response is `410 claim_expired` and
  `cloud_audit` records the failed redemption.
- **Given** an attacker guesses or scans tokens at high rate,
  **when** they submit > 10 invalid tokens / minute / IP,
  **then** the IP is rate-limited (`429`) and a
  `cloud_abuse_events` row records `kind=claim_token_brute`.
- **Given** the user redeems the token but the
  `server_pubkey` in their `POST` doesn't match the one the
  server posted in `init`,
  **when** the cloud verifies,
  **then** the response is `400 claim_pubkey_mismatch`.
- **Given** the user accidentally redeems on Account A then
  re-runs the flow and redeems on Account B,
  **when** the second redemption arrives with a *new* token,
  **then** Account B gets a *second* server row; the first
  link is unaffected. (Each token is a single-use linkage.)
- **Given** an entitlement was issued at claim time,
  **when** the server reads its TOML config,
  **then** the entitlement blob's signature verifies against
  the bundled cloud public key.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | random 12-byte token | base32 + group hyphen | matches `^[A-Z2-7]{4}-[A-Z2-7]{4}$` |
| T02 | integration | full happy path with mock server | claim/init → claim | tokens issued, audit row written |
| T03 | integration | clock advanced 11 min between init and claim | claim | 410 |
| T04 | integration | redeem twice | second attempt | 409 |
| T05 | integration | submit token with wrong pubkey | claim | 400 |
| T06 | integration | 11 invalid tokens from same IP | check `/api/servers/claim` | 429 from 11th |
| T07 | unit        | token grouping/case (`k3f9-mz7p` lowercase) | normalize | matches uppercase row |
| T08 | regression  | TLS pin: server posts to a man-in-the-middle | claim/init | TLS handshake fails (Cloudflare cert pin) |
| T09 | integration | concurrent claim of same token from two users | both POST | one 200, the other 409 |
| T10 | regression  | server pubkey rotation between init and claim | claim | 400 (mismatch) |
| T11 | integration | server token survives cloud restart | restart cloud, server reuses token | tunnel establishes |

## Edge cases

- **Operator copies token wrong.** Users see typo-friendly
  output: 4-char groups, base32 (no I/O/0/1 ambiguity).
- **Server has no internet at init time.** `POST /claim/init`
  fails; the local UI surfaces the error and offers retry.
  Without `init`, the cloud has no record and cannot redeem;
  this is intentional (defends against unknown-server
  redemption).
- **User is on a corporate network blocking `*.maktaba.app`.**
  The init call fails; we offer manual key import (out of
  scope for this story; future enhancement: signed-CSR-by-mail).
- **Server reset / re-claim.** Operator clicks "Reset cloud
  link" → server deletes its token, posts
  `DELETE /api/servers/{id}` with the token, then re-claims.
  The previous server row is deleted (cascades to bandwidth,
  streams, push devices that were registered against it).
- **TLS pinning for init.** The server pins Cloudflare's
  intermediate CA. If the cloud's public cert chain rotates
  (uncommon), the server requires a software update first.
  Acceptable v1 trade-off.
- **Replay of init.** If two `init` calls arrive with the
  same `token_hash`, the second is rejected `409`. Tokens
  are unique by hash.
- **TTL skew.** Cloud and server may disagree on time by
  small margins (NTP). 10-minute TTL absorbs ±60s skew
  safely.
- **Subdomain not assigned at claim.** Users pick a subdomain
  in 25.22 *after* claiming; this story leaves
  `cloud_servers.subdomain = NULL`.
- **One-cloud, many-servers.** A user can claim multiple
  servers; each gets its own row, token, and (later)
  subdomain. v1 caps at 5 servers/user.
- **Token format collisions with profanity / reservations.**
  Base32 alphabet excludes vowels-ish that produce English
  words; the alphabet `A-Z2-7` rarely produces words.
  Document; do not filter (would leak info).

## Files / packages

- `cloud/internal/server/claim.go` — both endpoints.
- `cloud/internal/server/token.go` — bcrypt-of-bearer.
- `cloud/internal/entitlement/sign.go` — Ed25519 sign helper
  (consumed by 25.26).
- Server-side: `internal/cloudlink/claim.go` (in the local
  Go API service repo); UI: `web/src/pages/admin/cloud-link.tsx`.

## Open questions

- **Cross-account move.** Should we let user A transfer a
  server to user B? Out for v1 (delete + re-claim suffices).
- **Audit visibility to user.** A failed redemption should
  appear in *whose* audit log? We log it under the actor of
  the failed call (or the IP if anonymous); the rightful
  owner sees nothing. Consider surfacing to the rightful
  owner (linked-server "suspicious activity" feed) in v2.
