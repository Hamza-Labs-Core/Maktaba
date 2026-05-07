# Story 15.1 — Local network discovery (mDNS / Bonjour)

The Maktaba server advertises itself on the LAN; every client discovers
it without manual entry.

**Anchors:** [`architecture.md` §6.1](../../architecture.md).

## AC

- Server advertises `_maktaba._tcp.local.` with TXT records:
  `version=`, `name=`, `tls=`, `auth_required=`, `mdns_id=` (a
  per-server stable UUID).
- Client (web is exempt; mobile and desktop included) queries on launch
  and on network-change events.
- Web client cannot browse mDNS directly; it relies on a captive-portal
  style "Open Maktaba" link in the discovery agent app, or manual URL.
- Server registers under both `local.` and any configured search domains.

## TC

- Server on LAN, mobile app cold-launch: discovered within 2 s; no
  manual entry needed.
- Server restart: TXT records re-published; clients re-resolve within
  10 s.
- Two servers on the same LAN: client picker
  ([Story 13.5](../13-desktop/story-13-05-mdns-discovery.md)) shows both.

## EC

- LAN with mDNS reflectors / VLAN segmentation: server advertises on the
  bound NIC only; document multi-VLAN setups.
- Server changes hostname: clients see two entries until the old one TTLs
  out; we treat `mdns_id` as the canonical identity.
- IPv6-only LAN: mDNS works over LL-multicast; `AAAA` records published.
