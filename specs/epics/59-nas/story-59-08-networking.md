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

**Ports the household chooses.** Other apps on a NAS (Plex, another package, the NAS's own UI)
may already use Maktaba's ports, so each one has a setting in the container's environment, the
place a compose file or a NAS template sets things, beside its `server.toml` key:

| Port | Variable | `server.toml` |
|---|---|---|
| 8088 web app | `MAKTABA_WEB_PORT` | `[server] web_listen` |
| 8080 API (apps, the home's machines) | `MAKTABA_API_PORT` | `[server] listen` |
| 8081 live TV | `MAKTABA_STREAM_PORT` | `[server] stream_addr` |
| 54329 Postgres (the home's machines) | `MAKTABA_POSTGRES_PORT` | `[postgres] port` |

- Every load of the config applies the variables over `server.toml` (`config/portenv.go`), so
  every program that reads it uses the same port: serve and its roles' environment, setup, the
  health check, the internal callers (each builds its address from the config), mDNS, the
  launcher's can't-start page (`MAKTABA_WEB_PORT`). A variable is an override, never written to
  `server.toml`: a save keeps the file's own value, so removing the variable goes back to it.
  An empty variable is not set (Unraid passes a template's empty field as `""`); a value that
  is not a port (1 to 65535) stops the start, naming the variable in both languages.
- **Moved or not.** The automatic move stays the behaviour for a default port: one another
  program holds at start moves into 8780–8790 and setup, the log and Settings → This machine
  show it (above). A port a variable sets is never moved, since the household chose it and a
  published port or a firewall rule may name it: setup's check and serve's start stop on it with
  `setup.check.note.port_setting_taken` (the holder named, or `_other`), naming the port and the
  variable, in the log in both languages.
- **The database port.** A bundled Postgres port another program holds (no `postmaster.pid` of
  this home's, which the supervisor's stale-lock step handles) is said in both languages,
  naming `MAKTABA_POSTGRES_PORT` and `[postgres] port`; in a container, or when the variable set
  the port, the start stops there. Elsewhere the supervisor holds the role until the port is
  free (`port_taken`), as before. The machines that join are told the port the bundle runs on
  (`ClusterDB.Port` in their database URL).
- **Settings → Storage & discovery** shows an address whose port a variable sets as set there
  (`storage.field.pinned`), its field locked; a change to it is refused.
- **A home's machines** learn the home's live TV port from `GET /api/discovery/home`'s
  `stream_port` when they start, so the home's TV front door is found on a port that is not 8081
  (`MAKTABA_HOME_STREAM_URL` still wins).
- **Fixed ports, not settings:** the optional browser HTTPS port (8443, off unless the cluster
  rule turns it on), direct access (8444), the TV slot ports (8791–8793) and the intra-host role
  ports, which `server.toml`'s `[server]` keys already move.

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
- With bridge networking (detected when the interface of the container's default route is a
  veth, the container's end of a Docker bridge; macvlan and ipvlan count as the host's network;
  `MAKTABA_NETWORK=host|bridge` overrides), Maktaba turns mDNS and SSDP off.
- Setup says: "This container is on a bridge network, so apps can't find Maktaba by themselves.
  They can still connect by its address ({url}), by its HTTPS name, or with a code from Maktaba
  Cloud."
- `public_url` defaults to the host's address when `MAKTABA_HOST_IP` is set; the compose files
  set it. Otherwise setup asks for it.
- The bridge variants of the compose files publish 8088, 8080 and 8081, but never 54329, since a
  bridge home has no nodes.
- **A published port that differs from the container's** ("19088:8088", TrueNAS's 30088): the
  server does not see it, so the household names it with `MAKTABA_PUBLIC_WEB_PORT`,
  `MAKTABA_PUBLIC_API_PORT` and `MAKTABA_PUBLIC_STREAM_PORT` (`[server] public_web_port`,
  `public_api_port`, `public_stream_port`). Every address Maktaba hands out uses them: setup's
  text, the Machines page and the cluster sign-in (`PublicWebURL`, `PublicAPIURL`), mDNS's
  advertised ports, the TV tuner's URLs and the discovery answer (`PublicStreamURL`).
  `web_public_url` and `public_url`, when set, still win. The TrueNAS app sets them from its
  published port numbers; the compose file's bridge block carries them beside its `ports:`.

**HTTPS.**
- Maktaba's per-home HTTPS name (story 50.15) is the default on a NAS, as elsewhere. It needs
  nothing from the NAS.
- A reverse proxy in front (DSM's Login Portal, TrueNAS with a proxy app, Nginx Proxy Manager on
  Unraid) is documented in the guides:
  - WebSocket upgrade on `/api/` (the live updates are `/api/cluster/ws`);
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
- **Given** `MAKTABA_WEB_PORT`, `MAKTABA_API_PORT`, `MAKTABA_STREAM_PORT` and
  `MAKTABA_POSTGRES_PORT` set, **then** the web app loads, the API and live TV answer, and
  Postgres listens on those ports, nothing on the defaults, and the addresses handed out name
  them.
- **Given** a port set by a variable and held by another program, **then** it is not moved and
  the start stops with a message in English and Arabic naming the port and the variable.
- **Given** a bridge that publishes other ports and names them (`MAKTABA_PUBLIC_*_PORT`),
  **then** setup and the server hand out the published address, and an app connects there.
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
| T09 | Go unit | port variables set, empty, bad | load, save | applied over the file, never saved; a bad one named in both languages (`portenv_test.go`) |
| T10 | Go unit | a variable's port held | setup check, serve | not moved; `port_setting_taken` in both languages (`ports_pinned_test.go`, `serve_network_test.go`) |
| T11 | Go unit | the bundled Postgres's port held | serve | both languages; stops in a container (`TestPostgresPortCheck`) |
| T12 | CI `custom-ports` cell | every port set | start | web app, API, live TV and Postgres on them, nothing on the defaults |
| T13 | CI `bridge-published-ports` cell | 19088:8088 and the others published and named | setup, start | the published address handed out and answering |
| T14 | CI `port-taken` cell | `MAKTABA_WEB_PORT` held by another program | start | the log names the port and the variable in both languages |
| T15 | Go handler, Go unit | a home's live TV on 18081 | a node starts | `stream_port` in `/api/discovery/home`; the node's TV front door on 18081 |

## Strings

| Key | English | Arabic |
|---|---|---|
| `setup.check.note.network_bridge` | This container is on a bridge network, so apps can't find Maktaba by themselves. They can still connect by its address ({url}), by its HTTPS name, or with a code from Maktaba Cloud. | هذه الحاوية على شبكة جسرية، لذا لا تستطيع التطبيقات العثور على Maktaba بنفسها. لكنها تستطيع الاتصال به عبر عنوانه ({url})، أو اسمه الآمن HTTPS، أو برمز من Maktaba Cloud. |
| `setup.check.note.port_moved` | Port {old} is used by {holder} on this machine, so Maktaba uses port {new} instead. | المنفذ {old} يستخدمه {holder} على هذا الجهاز، لذا تستخدم مكتبة المنفذ {new} بدلًا منه. |
| `setup.check.note.port_moved_other` | Port {old} is used by another program on this machine, so Maktaba uses port {new} instead. | المنفذ {old} يستخدمه برنامج آخر على هذا الجهاز، لذا تستخدم مكتبة المنفذ {new} بدلًا منه. |
| `setup.check.note.port_setting_taken` | Port {port} is used by {holder} on this machine, so Maktaba cannot use it. Set {setting} to a free port, then start Maktaba again. | المنفذ {port} يستخدمه {holder} على هذا الجهاز، فلا تستطيع مكتبة استخدامه. اضبط {setting} على منفذ متاح، ثم شغّل مكتبة من جديد. |
| `setup.check.note.port_setting_taken_other` | Port {port} is used by another program on this machine, so Maktaba cannot use it. Set {setting} to a free port, then start Maktaba again. | المنفذ {port} يستخدمه برنامج آخر على هذا الجهاز، فلا تستطيع مكتبة استخدامه. اضبط {setting} على منفذ متاح، ثم شغّل مكتبة من جديد. |
| `storage.field.pinned` | Set by {env} in the container's settings: change the port there. | يضبطه {env} في إعدادات الحاوية: غيّر المنفذ من هناك. |
| `storage.field.trusted_proxies` | Trusted proxies: forwarded client addresses are believed only from these | الوكلاء الموثوقون: لا تُصدَّق عناوين العملاء المُمرَّرة إلا منهم |

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
| compose files and templates (59.09–59.11) | host network, the D-Bus mount as a commented option, bridge variant; the `[ports]` block (each port commented, with a line on why), the published ports in the bridge block, the Unraid template's port variables, the TrueNAS app's published ports |
| `cmd/maktaba-server/internal/config/portenv.go`, `ports.go`, `serve_postgres.go`, `serve_config.go` | the port variables, the pinned port never moved, the database port check, Settings' locked field |
| `api/internal/handlers/discovery/home.go`, `cmd/maktaba-server/serve_node.go` | `stream_port`, and a node learning it |
| guides (59.14) | reverse proxy and firewall sections |
