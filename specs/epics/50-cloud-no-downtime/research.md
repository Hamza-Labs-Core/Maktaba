# Epic 50 research: how others keep remote access up, and what Maktaba Cloud has today

Read with the [README](README.md). Everything under *Maktaba Cloud today* was checked in the
code on 2026-11-03 (main at `f45276258`). Prices and outside facts were looked up on
2026-11-03; each one carries its source. Facts we could not confirm are marked **unverified**.

## 1. Maktaba Cloud today

### What exists

| Piece | Where | What it does now |
|---|---|---|
| One binary, three roles | `cloud/cmd/maktaba-cloud/` (`role_api.go`, `role_relay.go`, `role_worker.go`) | `api`, `relay` and `worker` share Postgres. |
| Single-VPS deploy | `deploy/cloud-relay/` | Docker Compose under systemd: relay + Postgres 16 + Redis 7 + Caddy, all on one machine. |
| Relay pipeline | `.github/workflows/relay.yml` | lint, test, multi-arch image, cosign, then `deploy.sh` over SSH to **one** host (`RELAY_SSH_HOST`). |
| Deploy | `deploy/cloud-relay/deploy.sh` | pull → migrate → `docker compose up -d --wait` → health check → roll back to the previous tag on failure. |
| Tunnel registry | `cloud/internal/relay/registry.go` | In-memory map slug → tunnel, per process. Newest connection wins (`seq`). |
| Home reconnect | `cloud/internal/cloudlink/health.go` | Exponential backoff, base 1 s, cap 30 s, **no jitter**. |
| Relay proxy | `cloud/internal/handlers/relay/proxy.go` | For **every** request: `Servers.BySlug` (a Postgres query) and `Meter.Allow` (a Postgres query), then the tunnel. |
| Metering | `cloud/internal/billing/meter.go` | Bytes counted in memory, flushed to Postgres every 30 s in 5-minute buckets. |
| Rate limits | `cloud/internal/ratelimit/ratelimit.go` | In memory, per process ("We do not back this with Redis in v1"). |
| Redis | `cloud/internal/config/config.go` | The URL is read from config, but **no code connects to Redis**. (A comment in `00050001_bandwidth.sql` claims monthly totals "hit Redis"; they don't.) |
| TLS | `deploy/cloud-relay/Caddyfile` | Caddy **on-demand** certificates: one Let's Encrypt certificate per home subdomain, issued the first time it is visited, kept in the `caddy_data` volume on that machine. |
| Direct address | `cloud/internal/handlers/homes/homes.go` | The home list gives `direct_url` = `http://<ip>:<port>` when the home reported one. Plain HTTP, no router setup (no UPnP or NAT-PMP anywhere in the repo). |
| Entitlements | story 25.26, `cloud/internal/entitlement/` | Ed25519-signed, 24 h life, **7-day offline grace** on the home. |

### What breaks, and how badly

| # | Gap | What a paying user sees |
|---|---|---|
| G1 | **One machine.** Relay, API, Postgres, Redis and Caddy share one VPS. | Any reboot, kernel patch, disk fault or Hetzner host problem = remote access down until it is back. |
| G2 | **Every deploy drops every tunnel.** `compose up` replaces the relay container; `server.Run` calls `http.Server.Shutdown`, which does not close hijacked WebSockets, so tunnels die with the process. | Each release: every remote stream stops, every home reconnects. |
| G3 | **Reconnect storm.** No jitter: after a restart all homes retry at 1 s, 2 s, 4 s … together. | The relay is hit by every home at the same instants just as it comes up. |
| G4 | **Certificates live on one disk.** Per-subdomain on-demand certificates sit in `caddy_data`. Let's Encrypt allows 50 new certificates per registered domain per week ([rate limits](https://letsencrypt.org/docs/rate-limits/)). | A fresh standby would need a new certificate for every home: past ~50 homes it cannot get them in time, so failover fails on TLS. |
| G5 | **The relay needs Postgres on every request.** | A 30-second database failover = 30 seconds of failed requests, even for homes whose tunnel is fine. |
| G6 | **Tunnels are per process; no multi-relay routing.** Story 25.8 planned "consistent hash on `server_id` at the L4 LB" with a bearer-parsing sidecar; it was never built, and Hetzner's load balancer can't do it (round robin or least connections only, see §4). | A second live relay can't be added: a client request may land on a relay that doesn't hold its home's tunnel. |
| G7 | **No managed Postgres on Hetzner.** Story 25.1 and architecture §13.2 say "Postgres (managed)". Hetzner has none ([Ubicloud runs one on Hetzner](https://www.ubicloud.com/use-cases/hetzner); Hetzner itself doesn't). | Replication, failover and backups are ours to run. |
| G8 | **Backups are a README line** (`pg_dump > backup.sql`). Nothing scheduled, nothing off the machine, no restore ever tested. | A lost disk loses accounts, links and billing state. |
| G9 | **No outside monitoring, no status page.** | We learn about an outage from users. |
| G10 | **The direct path is plain HTTP and only works if the user forwarded a port by hand.** | Most homes depend on the relay for remote play, so a relay outage is total for them. |
| G11 | **A stale name.** A comment in `cloud/migrations/00040001_servers.sql` says `skylark-12.relay.maktaba.app`. **maktaba.app is not ours**: users get `maktaba.tv`, dev gets `maktaba.dev`. (The `com.maktaba.app` / `io.maktaba.app` strings in Epic 12 and 22 plans are app identifiers, not hostnames.) | A copied example could point users at a domain someone else owns. |

### What already keeps working when the cloud is down

- **Local playback.** The home signs in its own users and streams on the LAN without the cloud
  (architecture §13: "Self-hosted features never depend on the cloud").
- **Cloud features on the home** (relay entitlement flags, editions) keep their last signed
  entitlement for **7 days** (story 25.26).
- **Payments.** The payment provider is TBD. A hosted
  checkout and webhooks the provider retries for days mean a cloud outage delays a plan change
  but doesn't lose it, so multi-day webhook retries are a selection rule for the provider
  (50.19 §6).

What does **not** keep working today: a **new** sign-in with a Maktaba Cloud account (the home
checks the cloud's assertion), the home list in the apps (fetched from `api.maktaba.tv`, not
cached), and remote play for anyone without a hand-forwarded port.

## 2. Plex

### Remote Access (direct)

- Plex Media Server first tries to open a port on the router itself with **UPnP or NAT-PMP**. If
  that fails, the owner ticks "Manually specify public port" and forwards it by hand. When it
  works the settings page says "Fully accessible outside your network".
  ([Remote Access](https://support.plex.tv/articles/200289506-remote-access/),
  [forum: manual port](https://forums.plex.tv/t/manually-specify-public-port-fails/224535))
- plex.tv checks reachability **from outside** the home; the green status flips to "Not
  available outside your network" within seconds when the forward is wrong.
  ([forum](https://forums.plex.tv/t/not-available-outside-your-network/754311)). Where the
  checks run from is **unverified** (one forum user says AWS in the EU:
  [forum](https://forums.plex.tv/t/full-list-of-ips-hostnames-plex-uses-for-remote-access/236678)).

### Plex Relay (the fallback)

- Used only when no direct connection works. Server and app both connect out to a Plex relay;
  the stream stays encrypted end to end; the dashboard shows it as "Indirect".
  ([Accessing a server through Relay](https://support.plex.tv/articles/216766168-accessing-a-server-through-relay/))
- **Capped at 2 Mbps** for Plex Pass and Remote Watch Pass users (same article). It used to be
  **1 Mbps free / 2 Mbps Plex Pass**
  ([forum](https://forums.plex.tv/t/remote-access-bandwidth-limit/586227)).
- Since **2025-04-29**, remote playback of personal video needs a Plex Pass or Remote Watch
  Pass on the viewer's account, or a Plex Pass on the server owner's account
  ([requirements](https://support.plex.tv/articles/requirements-for-remote-playback-of-personal-media/)).
  Prices then: Plex Pass $6.99/mo, $69.99/yr; Remote Watch Pass $1.99/mo
  ([PCWorld](https://www.pcworld.com/article/2642674/plexs-lifetime-subscription-plan-is-getting-a-massive-price-hike.html)).
  Lifetime went to $749.99 on 2026-07-01
  ([9to5Mac](https://9to5mac.com/2026/05/19/plex-increasing-lifetime-plex-pass-cost-to-whopping-750/)).
- Where Plex runs its relays and how it scales them: **unverified** (forum users see regional
  host tags such as "lhr": [forum](https://forums.plex.tv/t/plex-relay-hosting/819839)).

### `plex.direct`: HTTPS for every server

- Each server gets its own wildcard certificate for `*.<hash>.plex.direct`. A name like
  `1-2-3-4.<hash>.plex.direct` resolves to `1.2.3.4`, so one certificate works for every
  address of that server (LAN or public) but proves only that one server.
  ([Filippo Valsorda, 2015](https://words.filippo.io/how-plex-is-doing-https-for-all-its-users/))
- Because those names resolve to LAN addresses, routers with DNS-rebinding protection break
  them ([forum](https://forums.plex.tv/t/documentation-around-the-warn-cert-tls-errors-plex-direct-sni-dns-overlay-etc/801991)).

### How clients find and race connections

- Apps ask plex.tv's resources API (`/api/v2/resources?includeHttps=1&includeRelay=1&includeIPv6=1`)
  for the account's servers. Each server comes with a list of connections (local, remote,
  relay, flagged `relay=1`), and the client picks the best one that answers.
  ([forum](https://forums.plex.tv/t/plex-relay-is-broken/937329/2),
  [dart_plex docs](https://pub.dev/documentation/dart_plex/latest/dart_plex/PlexResource-class.html)).
  The exact racing rules are **unverified** (no official spec).

### When plex.tv is down

- Local servers need plex.tv to sign users in. The workaround, "List of IP addresses and networks
  that are allowed without auth", is unreliable: some apps also need their own setting, and it
  fails behind a reverse proxy.
  ([forum 1](https://forums.plex.tv/t/local-plex-media-server-stops-working-because-of-plex-tv-issues/766982),
  [forum 2](https://forums.plex.tv/t/bug-during-internet-outage-local-server-is-inaccessible/800153))
- Outages (from the IsDown aggregator, not Plex post-mortems):
  - **2026-07-14/15**, about 2.2 h: people couldn't sign in or reach remote **or local** servers,
    while Plex's status page listed only Live TV guide data
    ([FingerLakes1](https://www.fingerlakes1.com/2026/07/15/plex-streaming-outage-prevents-thousands-from-watching-movies-and-tv-shows-across-multiple-regions/),
    [IsDown](https://isdown.app/status/plex/outage-history)).
  - **April 2026**: plex.tv API down about 19 h (sign-in and PIN), and a 2.1 h DNS fault on
    `plex.direct` that cut off access from outside the home
    ([IsDown](https://isdown.app/status/plex/incidents/575686-issues-with-plex-tv-api)).
  - **September 2026**: a relay incident of about 2.4 h
    ([IsDown](https://isdown.app/status/plex/incidents/650034-issues-with-plex-relay-service)).

## 3. Jellyfin, Emby, Tailscale, Cloudflare Tunnel

- **Jellyfin**: no relay and no cloud. Remote access is a reverse proxy (also on a VPS), a VPN,
  or a forwarded port (which its docs mark "not recommended!"). HTTPS ends at the proxy.
  ([Jellyfin networking](https://jellyfin.org/docs/general/post-install/networking/))
- **Emby Connect**: account linking and server discovery only. "All connections are direct from
  your devices to your Emby Server"; the server must already be reachable.
  ([Emby Connect](https://emby.media/support/articles/Emby-Connect.html))
- **Tailscale DERP**: relays that carry already-encrypted WireGuard packets, used only when no
  direct or peer-relay path works. Each client picks a **home DERP region by latency**; regions
  have at least 3 servers; if a server fails clients move within the region, and if the region
  fails, to the next closest. ([DERP servers](https://tailscale.com/kb/1232/derp-servers))
  Needs the Tailscale client on every device, which rules it out for TVs, browsers and family
  members.
- **Cloudflare Tunnel**: `cloudflared` holds **4 outbound connections to at least 2 data
  centres**; each extra replica adds 4 more. Replicas get no traffic steering (no hashing,
  no round robin).
  ([tunnel availability](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-availability/))
  Cloudflare's service terms say non-Enterprise customers must use paid products (Stream,
  Images, R2…) to serve video and large files, or Cloudflare may limit or disable access
  ([service-specific terms](https://www.cloudflare.com/service-specific-terms-application-services/),
  updated 2026-09-28; the section number we were given, "2.8", isn't visible on the page). So
  relayed video can't go through the Cloudflare proxy on our plan, which is why the relay's names
  are DNS-only today.

## 4. Hetzner Cloud facts (as of 2026-11-03, EUR, excluding VAT, Germany/Finland)

| Item | Price / fact | Source |
|---|---|---|
| CX23 / CX33 / CX43 / CX53 (shared x86) | €5.99 / €8.99 / €16.49 / €29.99 a month. **Listed "currently not available" to order.** | [cost-optimized](https://www.hetzner.com/cloud/cost-optimized/) |
| CAX11 / CAX21 / CAX31 / CAX41 (shared Arm) | €6.49 / €10.99 / €21.49 / €41.49. **Also "currently not available".** | same |
| CPX22 / CPX32 (regular performance) | €19.99 / €35.99 | [regular-performance](https://www.hetzner.com/cloud/regular-performance/) |
| CCX13 / CCX23 / CCX33 (dedicated vCPU) | €43.49 / €86.49 / €138.99 | [general-purpose](https://www.hetzner.com/cloud/general-purpose/) |
| Included traffic | 20 TB a month per server in the EU (CCX33: 30 TB); extra **€1.00/TB** | plan pages above |
| Price rises | 2026-04-01 (all servers, existing included) and 2026-06-15 (new orders and rescales). CCX23 was €24.49 before. | [heise 1](https://heise.de/-11185981), [heise 2](https://heise.de/-11333037), [whtop](https://www.whtop.com/plans/hetzner.com/128291) |
| Primary IPv4 / Floating IPv4 | €0.50 / €3.00 a month | [IPv4 pricing](https://docs.hetzner.com/general/others/ipv4-pricing/) |
| Floating IP | Configured inside the OS (`ip addr add <ip>/32 dev eth0`); moves to another server **in the same network zone** with `POST /v1/floating_ips/{id}/actions/assign`. Failover time is not published; one user reports "up to 1 minute" (**unverified**). | [FAQ](https://docs.hetzner.com/cloud/floating-ips/faq/), [keepalived tutorial](https://community.hetzner.com/tutorials/configure-cloud-ha-keepalived), [keepalived-hcloud](https://github.com/psi-4ward/keepalived-hcloud) |
| Load Balancer | LB11 €7.49 (25 targets, **1 TB traffic**), LB21 €21.49, LB31 €42.99. TCP, HTTP, HTTPS, health checks, Proxy Protocol. Algorithms: **round robin, least connections**. No documented header routing or consistent hashing. | [LB](https://www.hetzner.com/cloud/load-balancer/), [overview](https://docs.hetzner.com/cloud/load-balancers/overview/), [FAQ](https://docs.hetzner.com/cloud/load-balancers/faq/) |
| Backups | 20 % of the server price, 7 slots | [backups](https://docs.hetzner.com/cloud/servers/backups-snapshots/overview/) |
| Object Storage | €6.49 a month incl. 1 TB stored + 1 TB egress; FSN1, NBG1, HEL1 | [object storage](https://www.hetzner.com/storage/object-storage/) |
| Managed Postgres | None from Hetzner | [Ubicloud on Hetzner](https://www.ubicloud.com/use-cases/hetzner) |
| Network speed per server | Not published. Reported 300–500 Mbit/s typical, not guaranteed (**unverified**); older runs measured 1–2.3 Gbit/s. | [comparedge](https://comparedge.com/tools/hetzner/performance), [vpsbenchmarks](https://www.vpsbenchmarks.com/hosters/hetzner/plans/cax11) |

What follows for us:

1. **The €20–25 a month figure from the first conversation is stale.** It assumed 2025 prices
   for CX/CAX machines, which can't currently be ordered and cost more now. The sizing in the
   README uses what can be ordered today and shows the cheaper plan where it comes back.
2. **The Hetzner LB can't route by home.** Fine for the stateless API; not usable for picking
   the relay that holds a tunnel. LB11's 1 TB traffic also rules it out for video.
3. **Floating IPs stay inside one network zone** (eu-central: FSN1, NBG1, HEL1). That covers a
   machine or a data-centre fault; a whole-zone or whole-Hetzner fault needs the manual restore
   path (story 50.07) and a DNS change.
4. **Plan relay capacity at 300 Mbit/s per node** until story 50.13 measures it.

## 5. Uptime arithmetic

An average month is 730 h = 43,830 min.

| Target | Downtime allowed a month | a year |
|---|---|---|
| 99.5 % | 3 h 39 min | 43.8 h |
| 99.9 % | **43.8 min** | 8.8 h |
| 99.95 % | **21.9 min** | 4.4 h |
| 99.99 % | 4.4 min | 53 min |

## 6. What to copy, what to avoid

| From | Copy | Avoid |
|---|---|---|
| Plex | Direct first, relay as fallback (we have it). A **connection list** per home from the cloud, with each entry flagged local / direct / relay, **cached by the app**. Router setup with **UPnP / NAT-PMP**, a manual port, and an **outside reachability check**. **Per-home certificates with IP-encoded names** so the direct path is HTTPS (ours is plain HTTP). | Local playback that needs the cloud to sign in (Plex's July 2026 outage blocked local servers). A status page that misses real impact (same outage). One DNS name as a single point of failure for direct access (April 2026 `plex.direct` fault): keep the raw-IP and relay entries in the list. |
| Tailscale | Clients choose a relay **region by latency** and move to the next one when a region fails (Phase 3). | Needing a client install on every device. |
| Cloudflare Tunnel | The home holds **more than one tunnel, to different relays**, so losing a relay doesn't wait for a reconnect (Phase 2, story 50.14). | Putting relayed video through a CDN whose terms forbid it. |
| Jellyfin / Emby | Nothing about the cloud depends on them: a self-hosted server must keep working with no cloud at all (we already promise this). | Making users expose ports or run a proxy as the only remote option. |
