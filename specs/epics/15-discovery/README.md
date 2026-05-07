# Epic 15 — Discovery & Networking

**Goal.** Make Maktaba easy to find on the LAN, optionally reachable
from the open internet, and pair-able across devices in seconds.
mDNS / Bonjour for LAN, an opt-in cloud relay for remote access,
optional federation between instances, UPnP/DLNA for legacy clients,
and QR-based pairing.

**Anchors:** [`architecture.md` §6](../../architecture.md) (clients),
§9.4 (Streaming auth).

---

## Stories

| # | Story | Status |
|---|-------|--------|
| 15.1 | [Local network discovery (mDNS / Bonjour)](story-15-01-mdns.md) | spec |
| 15.2 | [Global discovery (cloud relay)](story-15-02-cloud-relay.md) | spec |
| 15.3 | [Server-to-server federation](story-15-03-federation.md) | spec |
| 15.4 | [UPnP / DLNA compatibility](story-15-04-dlna-upnp.md) | spec |
| 15.5 | [QR code pairing](story-15-05-qr-pairing.md) | spec |
| 15.6 | [API: pairing endpoints](story-15-06-pairing-api.md) | spec (added per REVIEW §3.2) |
| 15.7 | [API: federation endpoints](story-15-07-federation-api.md) | spec (added per REVIEW §3.2 and §5.4) |

---

## Dependencies

- **Epic 10** (Auth) Stories 10.3 (refresh tokens), 10.6 (RS256 keys).
- **Epic 7** (API) Story 7.1 (HTTP skeleton).
- **Epic 16** (Subscriptions) Story 16.2 — relay quota and federation
  are gated to `home`/`pro` tiers.

## Cross-cutting checklist

- **Opt-in by default:** every cross-instance feature
  (relay, federation, DLNA, telemetry) ships **off** and is enabled by
  explicit user action.
- **Privacy:** mDNS records publish `name` (configurable), never
  filenames or library titles.
- **Trust on first use (TOFU):** clients pin server certificates on
  first successful authenticated connection; subsequent connections
  verify against the pin (resolves [REVIEW §5.5](../../REVIEW.md)).

## Out of scope

- BitTorrent / IPFS-style content distribution.
- WebRTC peer-to-peer streaming (federation v2).
- TURN / STUN ICE coordination (relay v2).
