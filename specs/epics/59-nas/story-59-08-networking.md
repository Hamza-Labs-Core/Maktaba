# Story 59.08 — Networking on a NAS: host or bridge, mDNS beside the NAS's responder, HTTPS, reverse proxies

> Epic 59 · Phase 3 · Depends on: 59.01 · Needed by: 59.09, 59.10, 59.11

## Why

Maktaba's apps and TVs find a home by mDNS (`_maktaba._tcp`), and the TV front door also answers
SSDP. Both need the host network. A NAS already runs its own services that Maktaba must sit
beside:
- an mDNS responder (Avahi on TrueNAS and Unraid, Bonjour on DSM);
- nginx on 80/443;
- its own UI;
- on DSM, its own Postgres on 5432.

Some households run every container on a bridge network for tidiness, or must use one. Behind a
reverse proxy, uploads, WebSocket and the HLS paths need the right settings.

## Description

**Host network by default.**
- Every compose file and template uses `network_mode: host`.
- Maktaba's ports (8088 web, 8080 cluster API, 8081 live TV, 54329 Postgres) do not collide with
  DSM's (80, 443, 5000, 5001, 5432) or TrueNAS's (80, 443).
- At start, Maktaba checks each port it binds. If one is taken, it exits 78 naming the port and
  the setting that moves it (`[server] web_listen`, etc.), instead of crash-looping.

**Postgres stays private.**
- On a NAS platform the bundled Postgres listens on loopback until a node pairs. Pairing switches
  it to LAN with TLS, as today, and back when the last node leaves.

**mDNS beside the NAS's responder.**
- **The NAS's Avahi** (TrueNAS, Unraid, QNAP and others): when `/var/run/dbus/system_bus_socket`
  is mounted and Avahi answers on it, Maktaba publishes `_maktaba._tcp` through Avahi's D-Bus
  API. The compose files mount the socket read-only where the platform has it.
- **Otherwise** (DSM's Bonjour, or no socket): the in-process responder binds 5353 with
  `SO_REUSEADDR`/`SO_REUSEPORT` and answers only for Maktaba's own names and service. It never
  answers for the NAS's hostname.
- **Verification:** the device checklists confirm that the Maktaba apps discover the home on DSM,
  TrueNAS and Unraid, and that the NAS's own Bonjour/Avahi names still resolve.

**Bridge network.**
- With bridge networking (detected when the container's default route is a Docker bridge and
  multicast to 224.0.0.251 does not loop back), Maktaba turns mDNS and SSDP off.
- Setup says: "This container is on a bridge network, so apps can't find Maktaba by themselves.
  They can still connect by its address ({url}), by its HTTPS name, or with a code from Maktaba
  Cloud."
- `public_url` defaults to the host's address when `MAKTABA_HOST_IP` is set; the compose files
  set it. Otherwise setup asks for it.
- The bridge variants of the compose files publish 8088, 8080 and 8081, but never 54329, since a
  bridge home has no nodes.

**HTTPS.**
- Maktaba's per-home HTTPS name (story 50.15) is the default on a NAS, as elsewhere. It needs
  nothing from the NAS.
- A reverse proxy in front (DSM's Login Portal, TrueNAS with a proxy app, Nginx Proxy Manager on
  Unraid) is documented in the guides:
  - WebSocket upgrade on `/api/ws`;
  - `client_max_body_size` / upload limit 0 or ≥ 10 GB for uploads;
  - no buffering on `/stream/`;
  - `X-Forwarded-For` and `X-Forwarded-Proto`, honoured only from trusted proxies through
    `WithTrustedProxies`, with the proxy's address added in Settings.
- The DSM firewall note follows Immich's pattern: when the firewall is on, allow 8088 (and 8080
  and 8081 for nodes and TVs) from the LAN.

## Acceptance criteria

- **Given** host networking on a box where Avahi owns 5353 with D-Bus mounted, **then** the
  Apple Player discovers the home, and `avahi-browse -a` shows both the NAS and Maktaba.
- **Given** no D-Bus socket, **then** the in-process responder runs beside the host's responder,
  and both answer.
- **Given** port 8088 taken, **then** exit 78 naming the port and the setting.
- **Given** bridge networking, **then** mDNS is off, setup shows the bridge text, and an app
  connects by address.
- **Given** a reverse proxy at a trusted address, **then** client IPs in logs are the real
  clients'. From an untrusted address, the header is ignored.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | fake D-Bus Avahi | announce | entry group published through D-Bus |
| T02 | CI (59.15) | host network, avahi-daemon in a sibling container | announce | both visible to `avahi-browse` |
| T03 | Go unit | 8088 bound by another process | start | exit 78, message |
| T04 | CI `bridge` cell | bridge | setup | mDNS off, text shown, `public_url` from `MAKTABA_HOST_IP` |
| T05 | Go handler | `X-Forwarded-For` from trusted / untrusted | request | used / ignored |
| T06 | Go unit | NAS platform, no nodes | pgbundle | listens on loopback |
| T07 | Device checklist | DSM, TrueNAS, Unraid | apps discover home; NAS names still resolve | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `setup.network.bridge` | This container is on a bridge network, so apps can't find Maktaba by themselves. They can still connect by its address ({url}), by its HTTPS name, or with a code from Maktaba Cloud. | هذه الحاوية على شبكة جسرية، لذا لا تستطيع التطبيقات العثور على Maktaba بنفسها. لكنها تستطيع الاتصال به عبر عنوانه ({url})، أو اسمه الآمن HTTPS، أو برمز من Maktaba Cloud. |
| `server.port_taken` | Port {port} is already in use on this NAS. Change {setting} in server.toml, or stop what uses it. | المنفذ {port} مستخدم بالفعل على هذا الخادم. غيّر {setting} في server.toml، أو أوقف ما يستخدمه. |

## Edge cases

- **IPv6-only LANs:** the responder announces AAAA records as today. Avahi does it itself.
- **Several NICs** (bonded, VLANs on a NAS): announce on the interface holding `MAKTABA_HOST_IP`,
  else on all non-Docker interfaces. Docker bridges (`docker0`, `br-*`) are never announced.
- **macvlan networks** (Unraid's `br0` custom): treated like host for mDNS when multicast loops.
  The template offers it as an option.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/mdns/` | Avahi D-Bus publisher; coexisting responder; bridge detection |
| `cmd/maktaba-server/internal/netcheck/` (new) | port pre-check, exit 78 |
| `cmd/maktaba-server/internal/pgbundle/` | loopback until a node pairs |
| compose files and templates (59.09–59.11) | host network, D-Bus mount, bridge variant |
| guides (59.14) | reverse proxy and firewall sections |
