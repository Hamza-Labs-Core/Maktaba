# Epic 15 — Discovery & Networking

> **Status:** spec + plans complete. **Source:** `specs/epics/15-discovery/`.
> **Anchors:** [`architecture.md` §6](../../../specs/architecture.md), §9.4 (streaming auth).

## Goal

Make Maktaba easy to find on the LAN, optionally reachable from the open internet, and pair-able across devices in seconds. mDNS/Bonjour for LAN discovery, an opt-in cloud relay for remote access (end-to-end encrypted, SPKI-pinned), optional server-to-server federation for sharing libraries between instances, UPnP/DLNA for legacy clients, and QR-based pairing. Every cross-instance feature ships **off** by default and is enabled by explicit user action.

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 15.1 | [mDNS / Bonjour](../../../specs/epics/15-discovery/story-15-01-mdns.md) | [plan-15-01](../../../specs/epics/15-discovery/plan-15-01-mdns.md) | Server advertises `_maktaba._tcp.local.` with TXT records `(version, name, tls, auth_required, mdns_id)`; clients browse on launch + on network change. |
| 15.2 | [Global discovery (cloud relay)](../../../specs/epics/15-discovery/story-15-02-cloud-relay.md) | [plan-15-02](../../../specs/epics/15-discovery/plan-15-02-cloud-relay.md) | Opt-in QUIC tunnel from server to relay edge; HTTPS ingress from clients; end-to-end TLS with SPKI pinning; per-region routing (us/eu/ap); quota enforcement. |
| 15.3 | [Server-to-server federation](../../../specs/epics/15-discovery/story-15-03-federation.md) | [plan-15-03](../../../specs/epics/15-discovery/plan-15-03-federation.md) | Two instances pair for asymmetric library sharing; federated browsing via GraphQL; conflict resolution (local always wins unless explicitly browsing remote). |
| 15.4 | [UPnP / DLNA compatibility](../../../specs/epics/15-discovery/story-15-04-dlna-upnp.md) | [plan-15-04](../../../specs/epics/15-discovery/plan-15-04-dlna-upnp.md) | Opt-in MediaServer (direct-play files only, no transcoded HLS); browse tree Library/Genre/Speaker/Recently Added; SSDP+SOAP+byte server. |
| 15.5 | [QR code pairing](../../../specs/epics/15-discovery/story-15-05-qr-pairing.md) | [plan-15-05](../../../specs/epics/15-discovery/plan-15-05-qr-pairing.md) | TV/desktop generates QR (`code` + `mid` + `spki` + `nonce`); mobile scans, parses, verifies; manual 6-digit fallback. |
| 15.6 | [API: pairing endpoints](../../../specs/epics/15-discovery/story-15-06-pairing-api.md) | [plan-15-06](../../../specs/epics/15-discovery/plan-15-06-pairing-api.md) | Pairing-code lifecycle: issue (5 min TTL), single-use claim with nonce verification, revoke, list-mine; rate-limited 6/min/IP. |
| 15.7 | [API: federation endpoints + crypto](../../../specs/epics/15-discovery/story-15-07-federation-api.md) | [plan-15-07](../../../specs/epics/15-discovery/plan-15-07-federation-api.md) | Crypto SAS pairing (X25519 ECDH + Ed25519 signatures + 4-word PGP SAS); federation-token mint/verify; ACL scoping; immediate revocation. |

## Key technical decisions

- **Opt-in by default.** Relay, federation, DLNA, and telemetry all ship off and require explicit user action.
- **Privacy-first mDNS.** Records publish only the configurable server `name`; never filenames, library titles, or user data.
- **TOFU (Trust On First Use).** Clients pin the server's TLS SPKI hash on first authenticated connection; subsequent connections (LAN or relay) verify against the pin. The QR code embeds the SPKI hash so mobile pairing bootstraps without prior LAN contact.
- **Cert rotation overlap window:** 7 days both old and new SPKI hashes are accepted via JWS-signed `GET /api/system/cert-rotation`.
- **Relay end-to-end encryption.** Server holds the TLS cert; the relay sees only ciphertext. QUIC outbound from server → relay edge; ingress is HTTPS routed by `mdns_id`.
- **Federation asymmetry.** A → B can read A's `Lectures`; B → A can read B's `Films`. Permissions scoped per-library via ACL.
- **SAS comparison for federation pairing.** 4-word PGP word list (phonetically distinct), read over an out-of-band channel (phone call) to defeat MITM that compromises a single transport.
- **DLNA codec filtering.** Only direct-play files exposed; HEVC and transcoded HLS excluded.
- **QR security layers.** `code` (6 alphanumeric, ≤5 min TTL, one-time) + `nonce` (32 random bytes bound into QR — second factor against shoulder-surfing) + `spki` (TLS binding for TOFU).

## API endpoints

- `POST /api/auth/pair` (issue, owned by [plan-10-17](../../../specs/epics/10-auth-security/plan-10-17-auth-pair.md))
- `POST /api/auth/pair/claim`, `DELETE /api/auth/pair/{code}`, `GET /api/auth/pair` (Story 15.6)
- `GET /api/system/cert-rotation` JWS-signed (Story 15.2)
- `POST /api/federation/init`, `POST /api/federation/pair`, `POST /api/federation/{partner_id}/confirm`, `POST /api/federation/{partner_id}/token`, `GET /api/federation`, `PATCH /api/federation/{partner_id}`, `DELETE /api/federation/{partner_id}` (Story 15.7)
- DLNA: SSDP multicast `239.255.255.250:1900`, SOAP ContentDirectory, HTTP byte server (Range-aware sendfile) (Story 15.4)

## Migrations claimed by this epic

| Slot | Plan | Tables / changes |
|------|------|------------------|
| `0050` | plan-15-01 | `server_identity(id, mdns_id, created_at)`. |
| `0051` | plan-15-02 | `relay_settings(enabled, region, next_spki_sha256, next_spki_until)`, `relay_usage(period_start, bytes_in, bytes_out, sessions)`. |
| `0052` | plan-15-04 | `dlna_settings(enabled, bind_iface, advertise_uuid)`, `VIEW videos_dlna_compatible`. |
| `0053` | plan-15-06 | `pairing_codes` adds `nonce BYTEA CHECK (octet_length IN (1,32))`, `created_by_user_id UUID`. |
| `0054` | plan-15-07 | `federation_pending(...)`, `federation_partners(partner_id, peer_long_term_pubkey, acl, confirmed_at, revoked_at, ...)`. |

## Dependencies

- **Epic 10** Stories 10.3 (refresh tokens), 10.6 (RS256 keys), 10.14 (data-encryption key for ephemeral key sealing), 10.16 (security audit on pairing-code brute force), 10.17 (canonical pair/claim/poll endpoints), 10.18 (Ed25519 long-term server identity keys).
- **Epic 7** Story 7.1 (HTTP skeleton).
- **Epic 9** for `library_roots.path` canonical store (architecture §8.1).
- **Epic 16** Story 16.2 — relay quota and federation gated to `home`/`pro` tiers.
- **Epic 23** rate-limiting and supply-chain checks apply to relay-agent binary.

## Related mockups

`web/mockups/admin/` settings tabs for pairing & federation (Epic 15 admin mockups landed in commit `0253e38`).

## Out of scope

- BitTorrent / IPFS-style content distribution.
- WebRTC peer-to-peer streaming (federation v2).
- TURN / STUN ICE coordination (relay v2).
- Per-client DLNA codec negotiation (v1).
- Push notifications for federation revocation (v2).
- Automated cert rotation trigger on relay (manual for v1).

## See also

- [Epic 14 — TV Apps](epic-14-tv-apps.md) (consumer of QR pairing).
- [Epic 10 (auth)](#) — auth-pair endpoints, Ed25519 server identity, rate limits.
- [Security architecture summary](../security.md) — TOFU, SPKI pinning, federation crypto.
- [Glossary](../glossary.md) — mDNS, TOFU, SPKI, QUIC tunnel, SAS, X25519, Ed25519, federation JWT.
