# Story 15.7 — API: federation endpoints + crypto

**Status:** **NEW** — added in response to
[REVIEW §3.2 and §5.4](../../REVIEW.md): Story 15.3 references
federation token issuance, exchange, and revocation but the AC didn't
specify how the token is delivered, what cryptographic primitives bind
it, or what stops a man-in-the-middle from substituting public keys
during pairing. This story owns all of that.

**Anchors:** [`architecture.md` §6](../../architecture.md), §9.4. Touches
Epic 10 (Auth) for cross-instance JWT validation.

## Crypto threat model

We assume an active network attacker between two Maktaba instances A
and B during initial pairing. Without channel binding, an attacker
could substitute their own Ed25519 public keys, terminate TLS, and
intercept everything afterwards.

The protocol below binds the federation token to a TLS-bound
Diffie-Hellman handshake plus a human-verifiable short authentication
string (SAS), modeled on the SAFE-comparison pattern from MIT
Magic-Wormhole / TextSecure.

## Pairing protocol

1. **A initiates.** Admin on A clicks "Pair with another instance".
   A generates an ephemeral X25519 keypair `(epk_A, esk_A)` and stores
   it in `federation_pending` with a 10-minute TTL. A returns to the
   admin a base32-encoded `pair_token = base32(epk_A || hmac(epk_A, k))`
   where `k` is a per-A admin secret. The admin copies/shares the token
   to the operator of B by an out-of-band channel (Signal, in person,
   etc.).
2. **B receives.** Admin on B pastes the token into Settings →
   Federation → Pair. B verifies the HMAC against its own `k` (mismatch
   → reject; this also prevents copy-paste typos), generates its own
   X25519 ephemeral `(epk_B, esk_B)`, computes the shared secret
   `s = X25519(esk_B, epk_A)`, and POSTs to A.
3. **B → A handshake.** `POST /api/federation/pair {epk_B,
   sig_B = Ed25519_sign(LongTermKey_B, epk_A || epk_B || tls_spki_A)}`.
   The signature binds `epk_B` to A's TLS SPKI hash, so a MITM
   terminating TLS would have to forge B's long-term Ed25519 signature.
4. **A verifies.** A computes `s = X25519(esk_A, epk_B)`, derives
   `sas = SHA-256(s)[0..32]` rendered as 4 short words from a
   PGP-style word list. A returns `{epk_A, sig_A, sas, partner_id}`.
5. **Out-of-band SAS confirmation.** Both admins read the 4-word SAS to
   each other (phone call, video chat, in person). If they match,
   each clicks "Confirm" in their UI. If they don't, both abort.
   This is the only way to defeat a MITM that has compromised both
   transports.
6. **Persisted.** After mutual confirmation, both A and B persist a
   `federation_partner` row containing the partner's long-term Ed25519
   public key, ACL scope (`libraries: [...]`, `read_only: true`), and
   `confirmed_at`.

## Schema

- `federation_pending` (TTL'd staging during handshake):
  - `id UUID PRIMARY KEY`
  - `role TEXT NOT NULL CHECK (role IN ('initiator','responder'))`
  - `epk_self BYTEA NOT NULL`, `esk_self BYTEA NOT NULL`
    (stored encrypted at rest with the server's data-encryption key)
  - `epk_peer BYTEA`, `peer_origin_url TEXT`
  - `sas TEXT`
  - `expires_at TIMESTAMPTZ NOT NULL`
- `federation_partners`:
  - `partner_id UUID PRIMARY KEY`
  - `display_name TEXT NOT NULL`
  - `peer_origin_url TEXT NOT NULL`
  - `peer_long_term_pubkey BYTEA NOT NULL` (Ed25519)
  - `acl JSONB NOT NULL` (`{libraries: [uuid, ...], read_only: bool}`)
  - `created_at TIMESTAMPTZ NOT NULL`
  - `confirmed_at TIMESTAMPTZ NOT NULL`
  - `revoked_at TIMESTAMPTZ`
- Migration owner: this story.

## Endpoints

- `POST /api/federation/pair {epk_b, sig_b}` (B → A) → `200 {epk_a,
  sig_a, sas, partner_id}` or `400 invalid-signature |
  pair-window-expired | already-paired`.
- `POST /api/federation/{partner_id}/confirm` (admin, both sides) → `204`
  flips `confirmed_at`. Federation only takes effect after both sides
  confirm.
- `GET /api/federation` → list of partners (admin only): id, name,
  scope, last seen, status.
- `PATCH /api/federation/{partner_id} {acl}` → modify scope (admin only).
- `DELETE /api/federation/{partner_id}` → `204` revokes immediately;
  in-flight session JWTs survive until expiry (≤ 15 min).
- `POST /api/federation/{partner_id}/token` (admin only) → mints a
  short-lived JWT signed by A's long-term key for browsing/streaming
  scope on the partner. The peer validates the signature against the
  pinned long-term Ed25519 key.

## Security

- All `/api/federation/*` endpoints require `is_admin = true`.
- Long-term keys never leave the server; ephemeral keys live only
  in `federation_pending` and are wiped on success or expiry.
- Audit-log entries on every pair / confirm / revoke / token action
  (`category = 'federation'`).
- Revocation is immediate at the API; in-flight streaming JWTs survive
  until natural expiry (≤ 15 min) — same trade-off as
  [REVIEW §1.5.c](../../REVIEW.md).

## TC

- Admin on A creates a token; admin on B pastes it; SAS appears on both
  ends; admins confirm; subsequent `GET /api/federation` lists the new
  partner with `confirmed_at` set on both sides.
- MITM substitutes their own `epk_M` and `sig_M`: A's signature
  verification fails because the MITM doesn't know B's long-term
  private key. Pairing aborts.
- Network attacker forwards both handshakes intact but presents a
  different TLS cert to B: the SPKI hash in `sig_b` does not match B's
  view of A's cert; signature verification fails.
- SAS mismatch on the phone call: both admins click "Abort" and the
  partner row is never persisted; `federation_pending` rows expire in
  10 min.
- Revoke partner: the partner's next federated request returns 401
  `partner-revoked`.

## EC

- Both admins simultaneously revoke each other: idempotent, both rows
  end up in `revoked_at`.
- Long-term key rotation (Epic 10 Story 10.6): the federation handshake
  uses a snapshot of the key at pair-time; rotation requires re-pairing.
  Documented as a known limitation.
- Pair token leaked but never used: `federation_pending` row TTLs out
  in 10 min; no impact.
- Pair token leaked and used by an unauthorized party: the SAS
  comparison surface defeats it (the legitimate operator of B will see
  a SAS that doesn't match the legitimate operator of A's SAS, because
  the eavesdropper substituted their own ephemeral key).
