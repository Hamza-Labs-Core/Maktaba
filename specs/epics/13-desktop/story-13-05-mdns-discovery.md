# Story 13.5 — Local server auto-discovery (Bonjour / mDNS)

The desktop app discovers Maktaba servers on the LAN automatically and
offers them in a server picker.

**Anchors:** [`architecture.md` §6.4](../../architecture.md). Depends on
[Epic 15 Story 15.1](../15-discovery/story-15-01-mdns.md) (server-side
mDNS advertisement).

## AC

- The desktop app advertises `_maktaba._tcp.local.` as a client and
  resolves servers advertising the same service.
- First-launch wizard: lists discovered servers with name + last-seen
  timestamp; user picks one; manual entry of `host:port` is also
  available.
- "Switch server" command in the menu re-opens the picker.
- Discovery is passive (no active scans beyond mDNS) so it consumes
  minimal bandwidth.
- Pairing across LAN uses QR code
  ([Story 15.5](../15-discovery/story-15-05-qr-pairing.md)) when manual
  auth is needed.

## TC

- LAN with one server running: the picker auto-fills it within 2 s.
- LAN with three servers: all listed; selection persists for next launch.
- LAN with zero servers: picker shows manual entry only.

## EC

- mDNS is blocked by VPN / corporate firewall: graceful fallback to
  manual entry; we surface "mDNS unavailable — enter your server
  manually".
- Server changes IP on the LAN: discovery re-resolves on next launch
  without user action.
- Multi-NIC machine with mDNS on every interface: dedupe by service name.
