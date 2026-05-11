# Story 25.26 — Cloud→server entitlement signing

> Epic 25 · Cloud relay · Phase 5 (operations)

## Description

The cloud is the system-of-record for billing, but the local API
service must answer "is this feature on?" without calling the cloud
on every request. We solve it by signing a small JSON entitlement
blob (Ed25519, 24h TTL) and shipping it to the linked server.
This is the same pattern as Epic 16 (license-key validation), now
delivered automatically over the tunnel rather than entered by hand.

Blob shape:

```json
{
  "iss": "cloud.maktaba.app",
  "sub": "<server_id>",
  "user_id": "<user_id>",
  "tier": "pro",
  "interval": "yearly",
  "suspended": false,
  "issued_at": "2026-05-06T00:00:00Z",
  "expires_at": "2026-05-07T00:00:00Z",
  "features": {
    "cloud_relay": true,
    "cloud_push": true,
    "concurrent_streams": 2,
    "monthly_bandwidth_gb": 100,
    "family_invites": 0
  },
  "kid": "ent-2026-05",
  "v": 1
}
```

`tier` is `"free" | "pro" | "family"` (matches
[`architecture.md` §13.10](../../architecture.md#1310-billing--entitlements)).
`interval` is `"monthly" | "yearly"` and is omitted for `tier=free`.
A suspended subscription is signalled by `suspended=true` (which
zeroes out `features`) rather than a fifth tier value.

The blob is canonicalized (RFC 8785 JCS), signed with the cloud's
Ed25519 entitlement key, and the signature is embedded as
`...entitlement.<base64-payload>.<base64-sig>` (compact JWS-style).

Distribution paths:

1. **At claim time** (25.6): the `claim` response includes the
   first entitlement.
2. **Refresh on tunnel handshake** (25.8): the cloud sends
   `0x21 ENT_REFRESH` after handshake completes.
3. **Daily rotation**: a cron pushes a fresh entitlement to every
   connected server before the previous expires.
4. **Pull**: server can request `GET /api/servers/{id}/entitlement`
   over the tunnel HTTP path if it suspects staleness.

Local server behavior:

- Server caches the latest entitlement on disk (encrypted with
  the local data key).
- On every cloud-only feature gate, the server checks
  `expires_at > now() + 5m` and signature valid.
- **Offline grace = 7 days.** If the server hasn't received a
  fresh entitlement in 7 days (e.g., the cloud is down),
  cloud-only features remain on. After 7 days, they degrade to
  "free tier" until refresh.
- A revoked entitlement (`kid` in revocation list) is honored
  immediately (server fetches the revocation list daily).

Key management:

- Cloud holds an Ed25519 keypair `kid=ent-YYYY-MM`. Rotated
  monthly. Old `kid`s remain published in the JWKS-style
  endpoint for 90 days so older entitlements still verify until
  they expire.
- Public key bundled in server build at `keys/cloud-ent.pub.pem`;
  also fetchable via cloud `GET /.well-known/maktaba-ent-jwks.json`.
  Server prefers bundled key but accepts new keys from JWKS if
  signed by the older key (chain-of-trust rotation).

## Acceptance criteria

- **Given** a freshly claimed server,
  **when** the claim response is processed,
  **then** the server has a valid entitlement on disk with
  `expires_at = now() + 24h` and signature verifies against
  the bundled public key.
- **Given** the server reconnects after 23h,
  **when** the cloud sends `0x21 ENT_REFRESH`,
  **then** the server replaces its cached entitlement with
  the new one without re-claim.
- **Given** the cloud has been unreachable for 6 days,
  **when** a user issues a remote-stream request via LAN,
  **then** the server still permits the cloud-relay feature
  (within 7-day grace).
- **Given** the cloud has been unreachable for 8 days,
  **when** the user issues the same request,
  **then** cloud-only features are off; LAN-only features
  unaffected.
- **Given** the cloud's `kid` rotates,
  **when** the server receives a new entitlement signed by
  the new `kid`,
  **then** the server fetches the new public key from JWKS
  (signed by the previous key) and verifies.
- **Given** an entitlement signature is tampered,
  **when** the server verifies,
  **then** verification fails; local cache is wiped and
  the server requests a fresh one.
- **Given** the entitlement is in the revocation list,
  **when** the server checks,
  **then** the entitlement is invalid; cloud features off
  until the next refresh produces an unrevoked one.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | sign + verify roundtrip | known key | passes |
| T02 | unit        | flip a bit in signature | verify | fails |
| T03 | integration | full claim → cache | inspect | file present, mode 0600 |
| T04 | integration | offline 6 days | feature gate check | on |
| T05 | integration | offline 8 days | check | off |
| T06 | integration | key rotation | issue with new kid | accepted via JWKS |
| T07 | regression  | revoked kid | check | off until next refresh |
| T08 | unit        | clock skew ±60s | verify | within tolerance |
| T09 | regression  | server boots without entitlement | observe | cloud features off, LAN unchanged |
| T10 | integration | cron pushes refresh nightly | observe over 5 days | always 24h ahead |

## Edge cases

- **Server's data key lost.** The encrypted entitlement
  cache is unrecoverable; a re-claim drops a fresh copy.
- **Time tampering on server.** A server with a wildly
  wrong clock could think the entitlement is still valid
  forever; we set a check on `now() < issued_at + 8d` and
  reject ridiculous offsets.
- **Down-scope entitlement.** A user downgrades from Pro
  to Free; the next entitlement push is "Free tier"; the
  current one (still valid for up to 24h) honors Pro
  until expiry. Acceptable: 24h transition window.
- **Family member servers.** Each family member has their
  own server, their own entitlement, all signed by the
  same cloud key. Members inherit the payer's tier
  (`tier=family`, `interval=...`) — the family role is a
  membership relationship in `family_members`, not a tier
  value.
- **Suspended user.** Cloud sets `suspended=true` on the
  entitlement (tier still reflects the last paid plan).
  The verifier zeros `features` and lets the local UI show
  a "subscription action required" banner.
- **Compromised cloud-private key.** Worst case;
  rotation procedure: mint a new key signed by the
  build-time bundled key; revoke the compromised `kid`
  in the JWKS. Servers refuse the compromised `kid`
  and fall back to the bundled key + new key chain.
- **Replay attack.** Entitlements are short-lived (24h)
  and bound to `sub=server_id`; an entitlement for
  server A can't be presented at server B.
- **JCS canonicalization bugs.** Use a vetted library
  (`github.com/cyberphone/json-canonicalization`); pin
  version; test against vector cases.
- **Consensus on `tier` strings.** Catalog of allowed
  `tier` values lives in a shared schema between cloud
  and server (Epic 16 already defines it).

## Files / packages

- `cloud/internal/entitlement/sign.go` — Ed25519 signer,
  JCS canonicalization.
- `cloud/internal/entitlement/keys.go` — keypair store +
  JWKS publisher.
- `cloud/internal/entitlement/refresh.go` — daily cron +
  on-handshake push.
- Server-side: `internal/cloudlink/entitlement.go` (in
  the local Go API repo); `internal/cloudlink/grace.go`.
- `cloud/migrations/00100010_entitlement_keys.sql`.

## Open questions

- **Mutual authentication of the cloud.** The bundled
  public key is the trust anchor; if the binary is
  tampered, all bets off. Out of v1.
- **Revocation list size.** A few keys per year, modest
  growth. Keep it inline in the JWKS endpoint.
