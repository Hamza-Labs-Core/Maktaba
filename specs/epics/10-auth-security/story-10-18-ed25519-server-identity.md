# Story 10.18 — Ed25519 long-term server identity keys

Every Maktaba Server has a single long-lived Ed25519 keypair that
identifies it across all federation, cloud-link, and inter-server
interactions. The key is generated on first boot, sealed at rest,
exposed by `kid` over a local JWKS-style endpoint, and rotatable
under explicit operator command.

The server-identity key is **distinct** from the Epic 10.6 RS256
JWT-signing key (which signs short-lived access tokens) and the
Epic 16.4 license-validation key (verify-only). 10.18 is owned by
each on-prem server and signs claims *about* the server itself —
its claim of identity to a federated peer, its claim to be the
server holding a particular cloud claim-token, and the keypair the
cloud entitlement verifier pins on (`Sub=server_id`).

This story is referenced by:

- Plan 15.7 (federation API) for federation request signatures.
- Plan 16.4 (license validation) for verifying that an inbound
  signed entitlement matches the *server* it claims to bind to.
- Plan 16.6 / 16.8 (cross-server licensing flows).
- Plan 25.6 (server claim flow): server posts its `server_pubkey`
  to the cloud at `/api/servers/claim/init`.
- Plan 25.26 (cloud entitlement): the cloud's signed payload binds
  `Sub=server_id`; the local server cross-checks against this key.

**AC-1 — First-boot generation.**
- **Given** an empty install with no server-identity material on
  disk and no `MAKTABA_SERVER_IDENTITY_PRIVATE_PEM` env var,
- **When** the API boots,
- **Then** it generates a fresh Ed25519 keypair, seals the private
  half at rest using the Epic 10.14 sealing helper (`auth/keys`
  `SealedBox`), persists both halves under
  `${MAKTABA_STATE_DIR}/identity/v<n>.pem.sealed` (private) and
  `${MAKTABA_STATE_DIR}/identity/v<n>.pub.pem` (public), and writes
  an audit row `category='keys', action='identity.generated',
  payload={kid:<sha256-of-pub>}`.

**AC-2 — Bootstrap from env var.**
- **Given** `MAKTABA_SERVER_IDENTITY_PRIVATE_PEM` is set,
- **When** the API boots,
- **Then** the env-var value is parsed as a PKCS-8 Ed25519 private
  key and is preferred over any on-disk material; the loader logs
  `identity.source=env`; mismatched algorithm refuses to start with
  a clear error message naming the env var.

**AC-3 — `kid` derivation and stability.**
- **Given** an active identity keypair,
- **When** consumers ask for its `kid`,
- **Then** the value is the lowercase hex SHA-256 of the public-key
  bytes (32-byte raw, not PEM), truncated to 16 chars. The same
  public key always produces the same `kid`.

**AC-4 — Local JWKS-style publication.**
- **Given** the API is running,
- **When** `GET /api/.well-known/server-identity.json` is called,
- **Then** the response is JSON `{kid, alg:"EdDSA",
  public_key_b64, created_at, rotation_overlap_until?}` for the
  active key plus any rotation-overlap predecessor. Endpoint is
  unauthenticated, cacheable for 300 s (`Cache-Control: public,
  max-age=300`).

**AC-5 — Sign / verify primitives.**
- **Given** the keypair is loaded,
- **When** callers invoke `Signer.Sign(ctx, payload)`,
- **Then** the signer returns the Ed25519 signature and the current
  `kid`. `Verifier.Verify(ctx, payload, sig, kid)` looks up the
  public key by `kid` (current + overlap predecessor) and returns
  a typed error for unknown `kid` vs invalid signature so callers
  can distinguish "I don't know that server" from "I do know it
  and the message is forged."

**AC-6 — Operator rotation.**
- **Given** an admin runs `maktaba-api identity rotate
  --reason "<text>"`,
- **When** processed,
- **Then** a new keypair is generated, the previous key enters a
  72-hour overlap window during which signatures from either key
  verify, and after the overlap the predecessor is purged from
  disk. An audit row `category='keys', action='identity.rotated',
  is_admin=true, reason='<text>', payload={old_kid, new_kid,
  overlap_seconds: 259200}` is written. An immediate-rotate flag
  collapses overlap to 0 and prompts for confirmation
  (`yes-invalidate-server-identity`).

**AC-7 — Federation & cloud cross-references.**
- The cloud claim flow (plan-25-06) reads the public key from
  `serverkeys.Active().PublicKey` when sending
  `{token_hash, server_pubkey}`.
- The cloud entitlement verifier (plan-25-26) checks
  `payload.Sub == serverkeys.Active().Kid` before honoring an
  entitlement — proves the entitlement was issued to *this*
  server.
- Federation outbound calls (plan-15-07) sign their request body
  with this key; inbound calls verify against the peer's JWKS at
  the same path.

**Test cases:**
- Unit: fresh install generates a new keypair sealed in place;
  reload returns the same `kid`.
- Unit: env-var-loaded key beats on-disk material; logged source
  reflects `env`.
- Unit: `kid` is stable across processes for the same key.
- Integration: rotation produces overlap during which both old
  and new signatures verify; after overlap, only new verifies.
- Integration: `/api/.well-known/server-identity.json` lists both
  keys during overlap.
- Negative: bad PEM in env var refuses to start.
- Negative: wrong-algorithm key (e.g., P-256) refuses to start.
- Negative: `--immediate` rotation without confirmation aborts;
  with confirmation, predecessor is purged within 1 s.

**Edge cases:**
- A leaked private key — operator rotates with `--immediate`. The
  cloud claim must be re-redeemed; entitlements bound to the old
  `kid` stop verifying because the new claim re-issues a fresh
  cloud entitlement bound to the new `kid`. Documented as
  identity-compromise IR.
- State-dir wiped between boots without env var → AC-1 fires;
  this is a *new* server identity (different `kid`); the cloud,
  federation peers, and any pinned license must re-pair. We do
  not silently regenerate over a missing key when one was
  expected: if `${MAKTABA_STATE_DIR}/identity/` contains a
  `v<n>.expected.kid` sentinel, boot refuses unless `--allow-new-
  identity` is passed.
- Two API processes racing first-boot generation → file-lock on
  `${MAKTABA_STATE_DIR}/identity/.lock`; the loser reads the
  winner's file.
