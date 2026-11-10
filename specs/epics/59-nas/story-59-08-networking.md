# Story 59.08 — Networking on a NAS: host or bridge, mDNS beside the NAS's responder, HTTPS, reverse proxies

> Epic 59 · Phase 3 · Depends on: 59.01 · Needed by: 59.09, 59.10, 59.11

## Why

Maktaba's apps and TVs find a home by mDNS (`_maktaba._tcp`), and the TV front door also answers
SSDP. Both need the host network. A NAS already runs its own services that Maktaba must sit
beside:
- an mDNS responder (avahi-daemon on DSM and Unraid; whether TrueNAS 25.04+ still runs one is
  checked on the device checklist);
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
- QNAP's QTS uses 8080 for its own UI, which collides with Maktaba's cluster API.
- A taken port is already handled: `ReconcilePorts` (`cmd/maktaba-server/ports.go`) moves
  Maktaba's port, and `supervisor/portcheck.go` with `portholder` names what holds it. This story
  makes the move visible on a NAS: setup and Settings → This machine show `server.port_moved`
  with the new port and the holder's name. The web port (8088) is the one a household types, so
  when it moves, the container log prints the new address in both languages too.

**Postgres stays private.**
- On a NAS platform the bundled Postgres listens on loopback until a node pairs. Pairing switches
  it to LAN with TLS, as today, and back when the last node leaves.

**mDNS beside the NAS's responder.** `internal/mdns/announce.go` already has both paths:
publishing through Avahi with `avahi-publish-service` (which talks to avahi-daemon over D-Bus),
and a zeroconf responder of its own under `<host>-maktaba` when Avahi is not reachable.
- **Default in a container: Maktaba's own responder.** No D-Bus socket is mounted, so
  `announce.go` takes its zeroconf path. The responder binds 5353 with `SO_REUSEADDR`, which on
  Linux lets it sit beside the host's avahi-daemon: every socket bound that way receives the
  multicast queries. (`SO_REUSEPORT` would not help, since Linux shares a port that way only
  between sockets of the same uid.) It answers only for `<host>-maktaba` and `_maktaba._tcp`,
  never for the NAS's own hostname. Unicast and QU queries reach only one socket on the host;
  apps already ask by multicast, and the checklist confirms it.
- **Optional: the host's Avahi.** Mounting `/var/run/dbus/system_bus_socket` makes `announce.go`
  publish through the host's avahi-daemon. The guides offer it as an option with its cost: the
  socket reaches every system service D-Bus policy allows the container's uid, and a `:ro` bind
  does not stop `connect()`. It is never in the default compose files.
- **Verification:** the device checklists confirm that the Maktaba apps discover the home on DSM,
  TrueNAS and Unraid with the default, and that the NAS's own names still resolve.

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
  - `X-Forwarded-For` and `X-Forwarded-Proto`, honoured only from the proxy. The home has no
    trusted-proxy setting today (`WithTrustedProxies` is the cloud server's). This story adds
    `[server] trusted_proxies` (addresses or CIDRs, empty by default) to the home's API and
    streaming, set in Settings, with the cloud's helper moved to `shared/` and reused.
- The DSM firewall note follows Immich's pattern: when the firewall is on, allow 8088 (and 8080
  and 8081 for nodes and TVs) from the LAN.

## Acceptance criteria

- **Given** host networking on a box where avahi-daemon owns 5353 and no D-Bus socket is
  mounted, **then** Maktaba's responder runs beside it, the Apple Player discovers the home, and
  `avahi-browse -a` shows both the NAS and Maktaba.
- **Given** the D-Bus socket mounted, **then** Maktaba publishes through the host's Avahi.
- **Given** port 8080 taken (QNAP), **then** `ReconcilePorts` moves the cluster port, and setup
  shows the new port and the holder.
- **Given** bridge networking, **then** mDNS is off, setup shows the bridge text, and an app
  connects by address.
- **Given** a reverse proxy at a trusted address, **then** client IPs in logs are the real
  clients'. From an untrusted address, the header is ignored.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | no D-Bus socket | announce | zeroconf path; socket bound with `SO_REUSEADDR` |
| T02 | CI (59.15) | host network, avahi-daemon in a sibling container as another uid, no D-Bus mount | announce | both visible to `avahi-browse` |
| T03 | Go unit | 8080 bound by another process | start | port moved; `server.port_moved` with the holder |
| T08 | CI | D-Bus socket mounted | announce | published through the host's Avahi |
| T04 | CI `bridge` cell | bridge | setup | mDNS off, text shown, `public_url` from `MAKTABA_HOST_IP` |
| T05 | Go handler | `X-Forwarded-For` from trusted / untrusted | request | used / ignored |
| T06 | Go unit | NAS platform, no nodes | pgbundle | listens on loopback |
| T07 | Device checklist | DSM, TrueNAS, Unraid | apps discover home; NAS names still resolve | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `setup.network.bridge` | This container is on a bridge network, so apps can't find Maktaba by themselves. They can still connect by its address ({url}), by its HTTPS name, or with a code from Maktaba Cloud. | هذه الحاوية على شبكة جسرية، لذا لا تستطيع التطبيقات العثور على Maktaba بنفسها. لكنها تستطيع الاتصال به عبر عنوانه ({url})، أو اسمه الآمن HTTPS، أو برمز من Maktaba Cloud. |
| `server.port_moved` | Port {old} is used by {holder} on this NAS, so Maktaba uses port {new} instead. | المنفذ {old} يستخدمه {holder} على خادم التخزين الشبكي هذا، لذا يستخدم Maktaba المنفذ {new} بدلًا منه. |
| `settings.trusted_proxies` | Trusted proxies: forwarded client addresses are believed only from these | الوكلاء الموثوقون: لا تُصدَّق عناوين العملاء المُمرَّرة إلا منهم |

## Edge cases

- **IPv6-only LANs:** the responder announces AAAA records as today. Avahi does it itself on the
  optional path.
- **Several NICs** (bonded, VLANs on a NAS): announce on the interface holding `MAKTABA_HOST_IP`,
  else on all non-Docker interfaces. Docker bridges (`docker0`, `br-*`) are never announced.
- **macvlan networks** (Unraid's `br0` custom): treated like host for mDNS when multicast loops.
  The template offers it as an option.

## Build plan

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/mdns/` | `SO_REUSEADDR` beside the host's responder; bridge detection |
| `cmd/maktaba-server/ports.go`, `internal/supervisor/portcheck.go` | the moved-port notice with the holder's name |
| `shared/` (trusted proxies helper), `api/`, `streaming/` | `[server] trusted_proxies` |
| `cmd/maktaba-server/internal/pgbundle/` | loopback until a node pairs |
| compose files and templates (59.09–59.11) | host network, the D-Bus mount as a commented option, bridge variant |
| guides (59.14) | reverse proxy and firewall sections |
