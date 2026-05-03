# Story 15.2 — Global discovery (optional cloud relay)

For users who want remote access without opening ports, an opt-in cloud
relay tunnels traffic to the home server.

**Anchors:** [`architecture.md` §6](../../architecture.md). Depends on
Epic 16 Story 16.2 (tier gating).

## TOFU certificate pinning (resolves [REVIEW §5.5](../../REVIEW.md))

The "end-to-end encrypted" claim depends on clients pinning the home
server's TLS leaf or SPKI hash. The flow is:

1. **First connect (LAN):** client connects to the server over LAN, the
   user authenticates, and the server returns its current
   `tls_spki_sha256` in the auth response. The client stores it
   alongside the server's `mdns_id` as the trust anchor.
2. **Relay connect (subsequent):** the client connects to the relay,
   negotiates TLS to the server *through* the relay (the relay sees only
   ciphertext), and verifies the leaf's SPKI hash equals the pinned
   value. Mismatch → connection aborts with a "server identity changed"
   warning that requires explicit user re-pairing.
3. **Cert rotation:** the server publishes upcoming SPKI hashes via
   `GET /api/system/cert-rotation` (signed with the current cert);
   clients prefetch and accept either the current or the next hash for
   a 7-day overlap window.
4. **First connect ever via relay** (no LAN bootstrap): the client must
   complete pairing via [Story 15.5](story-15-05-qr-pairing.md), where
   the QR carries the SPKI hash; the user implicitly trusts the QR.

This makes the "relay sees only ciphertext" claim verifiable rather
than aspirational.

## AC

- Relay protocol: outbound long-lived QUIC connection from server to
  relay; clients connect to relay and are routed.
- Strictly opt-in; off by default. Settings → Remote Access toggles it.
- Relay is end-to-end encrypted: the server holds the TLS cert,
  relay sees only ciphertext. Clients enforce SPKI pinning per the
  TOFU flow above.
- Relay user identity is bound to the Maktaba account; no separate
  login.
- Quota: free tier 50 GB/month, premium tier higher
  ([Epic 16 Story 16.2](../16-subscriptions/story-16-02-premium-features.md)).
- Latency overhead documented; "Direct" / "Relayed" badge in the
  client's connection status.

## TC

- Enable remote access on the server, open the mobile app on cellular:
  app reaches the server via the relay; video plays.
- Toggle off: subsequent connection attempts on cellular fail with
  "Server unreachable — enable remote access?".
- Quota exhausted: reads continue but new sessions block until next
  cycle, with a clear error.
- Tamper test: a malicious relay substitutes its own TLS cert. Clients
  with a pinned SPKI refuse to connect; first-time relay clients (no
  pin yet) refuse unless the QR-supplied pin matches.

## EC

- Relay node failover: server reconnects to a healthy node within 30 s;
  client sessions continue.
- Server's outbound is firewalled: relay connection fails; UI surfaces
  the diagnostic.
- Relay outage: clients fall back to LAN-only.
- Some jurisdictions require data residency: relays in `eu`, `us`,
  `ap` regions; user picks at opt-in time.
- Cert rotation overlap window expired and the client missed both
  refreshes: pin verification fails; user sees "Server identity changed
  — re-pair via QR".
