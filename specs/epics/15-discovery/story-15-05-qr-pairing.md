# Story 15.5 — QR code pairing for mobile → server

A pairing flow that lets the user point a phone at a TV / desktop's QR
code and bind the mobile app to the same server with the same login.

**Anchors:** [`architecture.md` §9.8](../../architecture.md). Depends on
[Story 15.6](story-15-06-pairing-api.md) for `POST /api/auth/pair`.

## AC

- The TV / desktop generates a one-time pairing code via
  [`POST /api/auth/pair`](story-15-06-pairing-api.md), receiving a
  6-digit human code + a QR-encoded URL.
- The QR URL has form
  `https://{server}/pair?code=ABC123&mid={mdns_id}&spki={hash}` and
  embeds the server's mDNS ID + LAN address + TLS SPKI hash (used for
  TOFU pinning per [Story 15.2](story-15-02-cloud-relay.md)).
- The mobile app's "Add device" flow scans the QR; if the encoded server
  is reachable on LAN it pairs directly, else it falls back to the relay.
- Pairing exchanges a refresh token tied to the device (Epic 10
  Story 10.3); valid for 30 d.
- Pairing code TTL: 5 min; one-time use; expires immediately on
  successful pair.

## TC

- TV shows QR; phone scans; phone is logged in within 3 s.
- Re-scan an expired QR: surfaces "Pairing code expired — generate a
  new one on TV".
- Pair across cellular (relay path): same flow, slower; the SPKI hash
  in the QR is what the client pins.

## EC

- QR contains a server the phone has never seen: surface a confirmation
  "Pair with `maktaba.local`?" before committing.
- Camera permission denied: fall back to manual code entry (6 digits).
- Phishing-style fake QR: pairing checks the server's TLS cert SPKI
  against the QR-supplied hash; mismatch → refuse and surface
  "Server identity mismatch — do not approve".
- The `code` is correct but the SPKI hash in the QR doesn't match the
  server's actual cert (e.g., MITM): refuse pairing (per
  [REVIEW §5.5](../../REVIEW.md)).
