# Epic 50 — Maktaba Cloud with no downtime

> **Status:** spec (2026-11-03). **Source:** [`specs/epics/50-cloud-no-downtime/`](../../../specs/epics/50-cloud-no-downtime/README.md).
> **Anchors:** [`architecture.md` §13](../../../specs/architecture.md#13-cloud-relay-architecture) · [Epic 25](epic-25-cloud-relay.md) · [Epic 53](epic-53-logs-errors-reports.md) (the cloud's logs, errors and application metrics) · [Epic 51](epic-51-configurable-components.md) (the home's cloud parts are components) · [`deploy/cloud-relay/`](../../../deploy/cloud-relay/README.md) · [research](../../../specs/epics/50-cloud-no-downtime/research.md).

## Goal

Requirement: once people pay for remote access, there must be no downtime.

Maktaba Cloud's relay is built (Epic 25) but deployed as one VPS running relay, API, Postgres,
Redis and Caddy together. That's fine for a demo but not for paying users. This epic takes it
to **two nodes that cover for each other** before paid plans open (Phase 1), then to a **pool
of relays** (Phase 2).

## Targets

| | Phase 1 | Phase 2 |
|---|---|---|
| Availability (a target, no credit for downtime) | 99.9 % a month (43.8 min) | 99.95 % (21.9 min) |
| One node lost | back in ≤ 3 min, unattended | ≤ 30 s |
| Data lost on failover | none (synchronous replication) | none |
| Data lost in a disaster | ≤ 60 s (WAL archive), restore ≤ 4 h | ≤ 60 s, ≤ 1 h |
| Deploys | 0 dropped tunnels, 0 failed requests | same |
| Cost ceiling | €100 / €250 a month at 100 / 1,000 homes | €1,000 at 10,000 homes |

## Phases

- **Phase 1 (launch):** nodes A (FSN1) and B (NBG1) and a witness W (HEL1). Patroni with a
  three-member etcd decides the database leader; the leader holds the Floating IP and runs the
  cloud processes. Synchronous replication, a wildcard certificate on each node, blue-green
  deploys where homes move their tunnels before the old process stops, pgBackRest backups to
  Object Storage and Cloudflare R2, a canary home probed from outside, a status page off
  Hetzner, and the Free / Basic / Pro plans enforced by the relay and billed through a payment provider TBD.
- **Phase 1b:** direct remote access like Plex, **free on every plan**: UPnP / NAT-PMP, an
  outside reachability check, per-home HTTPS names on several separate direct-name domains
  (never under `maktaba.tv`).
- **Phase 2:** a relay pool with rendezvous-hash assignment and two tunnels per home, any relay
  forwarding to the one that holds a tunnel, a stateless API pool behind Hetzner LB11, the
  database on three nodes.

## Stories

| # | Story | Phase |
|---|---|---|
| 50.01 | [Names and the DNS plan](../../../specs/epics/50-cloud-no-downtime/story-50-01-names-and-dns.md) | 1 |
| 50.02 | [One wildcard certificate on every node](../../../specs/epics/50-cloud-no-downtime/story-50-02-wildcard-certificate.md) | 1 |
| 50.03 | [Homes move their tunnel without a gap](../../../specs/epics/50-cloud-no-downtime/story-50-03-tunnel-handover.md) | 1 |
| 50.04 | [Any relay process answers for any home](../../../specs/epics/50-cloud-no-downtime/story-50-04-tunnel-directory.md) | 1 |
| 50.05 | [The relay rides out a database blip](../../../specs/epics/50-cloud-no-downtime/story-50-05-database-blip.md) | 1 |
| 50.06 | [The cloud rehearsal in CI](../../../specs/epics/50-cloud-no-downtime/story-50-06-cloud-rehearsal.md) | 1 |
| 50.07 | [Two nodes, one address](../../../specs/epics/50-cloud-no-downtime/story-50-07-two-nodes.md) | 1 |
| 50.08 | [Deploys with no downtime](../../../specs/epics/50-cloud-no-downtime/story-50-08-zero-downtime-deploys.md) | 1 |
| 50.09 | [Backups and restore](../../../specs/epics/50-cloud-no-downtime/story-50-09-backups-restore.md) | 1 |
| 50.10 | [Monitoring, the canary home and alerts](../../../specs/epics/50-cloud-no-downtime/story-50-10-monitoring.md) | 1 |
| 50.11 | [Status page and in-app status](../../../specs/epics/50-cloud-no-downtime/story-50-11-status-page.md) | 1 |
| 50.12 | [Incident handling](../../../specs/epics/50-cloud-no-downtime/story-50-12-incident-handling.md) | 1 |
| 50.13 | [Apps ride out a cloud outage](../../../specs/epics/50-cloud-no-downtime/story-50-13-apps-ride-out-outage.md) | 1 |
| 50.14 | [Load test and sizing](../../../specs/epics/50-cloud-no-downtime/story-50-14-load-test.md) | 1 |
| 50.15 | [Direct remote access, like Plex](../../../specs/epics/50-cloud-no-downtime/story-50-15-direct-remote.md) | 1b |
| 50.16 | [A pool of relays](../../../specs/epics/50-cloud-no-downtime/story-50-16-relay-pool.md) | 2 |
| 50.17 | [A stateless API pool](../../../specs/epics/50-cloud-no-downtime/story-50-17-api-pool.md) | 2 |
| 50.18 | [The database on its own nodes](../../../specs/epics/50-cloud-no-downtime/story-50-18-database-nodes.md) | 2 |
| 50.19 | [Plans: Free, Basic and Pro, enforced by the relay and billed by usage](../../../specs/epics/50-cloud-no-downtime/story-50-19-plans-relay-billing.md) | 1 |
| 50.35 | [Remote access is on when any remote path is on](../../../specs/epics/50-cloud-no-downtime/story-50-35-remote-access-capability.md) | 1b |

The dependency graph and launch gate are in the [epic README](../../../specs/epics/50-cloud-no-downtime/README.md#10-stories).
50.10 depends on [Epic 53.17](../../../specs/epics/53-logs-errors-reports/story-53-17-cloud-observability.md)
(logs, error groups and application metrics of the cloud's processes) and 50.11 on
[Epic 53.06](../../../specs/epics/53-logs-errors-reports/story-53-06-status-strip.md) (the one
status strip every app draws).

## Decisions (2026-11-03)

The answers to the epic's open questions (epic README §13):

1. **No downtime credits.** The uptime target stays and is published; it is a target, not a
   promise, and no credit or refund is given (50.11, 50.12).
2. **Off-Hetzner backup copy on Cloudflare R2**, beside Hetzner Object Storage (50.09).
3. **Witness: a Hetzner CPX22 now.** Its witness job is etcd only (about 1–2 vCPU, 1–2 GB RAM,
   ~20 GB disk). Cloudflare can't host it: it sells no VMs, and Cloudflare Tunnel is only a
   connector (50.07).
4. **Plans: Free, Basic, Pro** (50.19), replacing the doc's Pro 2 / Family 5 and the code's
   1 / 4 / 10 "Home" / "Pro". **Free:** no relay. **Basic:** relay capped at about 3 Mbps (the
   home transcodes down to it), 2 relayed streams, 500 GB a month soft fair use (then slower,
   never stopped). **Pro:** full quality within a monthly allowance and N streams, optional
   pay-as-you-go GB (off by default, capped by the user), then Basic's quality, never cut off;
   extra streams are a paid add-on. Family is dropped; prices are TBD by the owner. The payment provider (TBD) bills
   a flat price per plan, a metered price for pay-as-you-go and a per-unit price for extra
   streams; old code names migrate `pro`→`basic`, `family`→`pro`.
5. **Direct certificates on several separate domains** (3–4 to start, never under
   `maktaba.tv`), homes spread across them by each domain's Let's Encrypt weekly room, more
   domains by configuration only, ZeroSSL / Google Trust Services for overflow. Paid per-home
   certificates rejected (50.15).
6. **Direct remote is free on every plan; only the relay is paid** (50.15, 50.13).

## Key decisions

- **Everything follows the database leader.** Patroni + etcd (with a witness so two nodes can't
  both win) decides; its callback moves the Floating IP and starts the cloud processes. DNS
  never changes on failover. Rejected: keepalived (no tie-breaker), pg_auto_failover (its
  monitor is a single point of failure), a home-made controller.
- **Hetzner has no managed Postgres**, so we run Patroni ourselves (story 25.1 assumed one).
- **One wildcard certificate per node by DNS-01**, replacing per-home on-demand certificates,
  which hit Let's Encrypt's 50-a-week limit on a fresh standby.
- **Make before break.** A `DRAIN` frame tells a home to open a new tunnel before the old one
  closes; resume tickets make reconnects cheap; jitter spreads them.
- **Redis holds nothing we can't lose.** Durable state is in Postgres.
- **Hetzner's load balancer can't route by home** (round robin and least connections only, and
  1 TB a month on LB11), so Phase 2 routes in the relay: a tunnel directory and forwarding.
- **Copy from Plex:** direct first, router setup with an outside check, per-home certificates,
  a cached connection list, a capped relay on the cheaper paid plan. **Avoid:** local play
  depending on the cloud, a status page updated by hand, charging for direct remote.
- **A paid plan never cuts relayed play off**: past its limits it steps down in quality. Only a
  lapsed plan (no plan) ends relayed play.
- **Names:** users get `maktaba.tv`, dev gets `maktaba.dev`. `maktaba.app` is not ours; older
  specs that say it are fixed by story 50.01. Direct names use their own separate domains.
- **The home's side is Epic 51 components** (README §4.6). `cloud.link` (the relay agent, never
  licence-gated) and `cloud.relay` (relayed play, licence flag `cloud_relay`, which Basic and Pro
  carry) exist. 50.15 adds `cloud.direct` (free, no flag) and 50.22 adds `livetv.broadcast`
  (flag `broadcast`, added by 50.20 for Pro and Broadcaster). On a Free home `cloud.relay` waits
  with `licence_missing` and keeps its wish.
- **Gap in Epic 51, closed by 50.35:** a capability is on only when every component listing it is
  on, so a Free home would read `remote_access: off` while direct remote works. 50.35 adds
  `feature_any` to the catalog: `cloud.relay` or `cloud.direct` is enough.

## Costs (as of 2026-11-03)

Hetzner raised prices twice in 2026, and its CX and CAX plans say "currently not available".
Phase 1 is about **€79 a month** with plans that can be ordered today (2 × CPX22 + a CPX22
witness, backups, Floating IP, Object Storage, an R2 copy), or about **€53** if CX/CAX come
back for A and B (the witness stays a CPX22), plus a few euros a month for the direct-name
domains. Phase 2
at about 10,000 homes is about **€481**. Details and sources: epic README §5 and research §4.

## Home broadcasting (spec, 2026-11-03)

Requirement: a way to stream TV from home servers to other people, as a tier of its own (an
entertainment or home streamer tier). Epic README
[§14](../../../specs/epics/50-cloud-no-downtime/README.md#14-home-broadcasting), research in
[research-broadcasting.md](../../../specs/epics/50-cloud-no-downtime/research-broadcasting.md).

A Maktaba home owner (the **publisher**) publishes chosen live TV channels to viewers outside the
home through Maktaba Cloud, on `watch.maktaba.tv/<publisher>/<channel>` and in every Maktaba
Player and TV app as a guest.

**Decisions (2026-11-03):**

- **Viewers never pay.** Access is open or by invite link only.
- **Maktaba charges for infrastructure, not content.** Maktaba Cloud is an infrastructure and hosting
  provider. It never selects, uploads, curates or recommends what airs. Channel building,
  scheduling and YouTube intake stay on the publisher's home, and the publisher is responsible for
  the rights.

**Design:**

- **Push to the edge.** The home pushes each finished HLS segment of each rendition **once** to
  Maktaba's Cloudflare edge (Worker + Durable Object + R2). Viewers never reach the home or the
  Hetzner relay.
- **One fetch, any audience.** Segments are cached under token-free keys, so the home's upload
  doesn't depend on the number of viewers.
- **The ladder at home** (top rung `copy` of the channel's own encode). Cloudflare Stream is the
  switchable fallback.
- **Standard HLS** with 4 s segments: ≤ 20 s glass to glass at p95.
- **Split planes.** The control plane (plans, ingest tokens, takedown queue, revocation signing,
  billing) rides on the Hetzner pair. The data plane survives the pair being down for 24 h.

**Plans:** Pro gains **guest sharing** (1 invite-only channel on demand, 5 viewers, 50
viewer-hours). **Broadcaster** is an add-on to Pro: 3 channels, 100 concurrent viewers, 5,000
viewer-hours, up to 1080p, then optional pay-as-you-go viewer-hours with a cap, then the lowest
rung, then new viewers wait. Prices are TBD; the payment provider is TBD.

**Estimated cost to Maktaba a month:**

| Path | Broadcaster at its allowance |
|---|---|
| R2 + Worker | ≈ $46 |
| Bunny fallback | ≈ $102 |
| Cloudflare Stream fallback | ≈ $302 |

Mux is ruled out: about $1,369 a month to encode one 24/7 channel.

**Rights (not legal advice; confirm with a lawyer before launch):**

- **Only publishable items air** on a published channel. This is a safety feature on the
  publisher's home. Publishable means the owner's own recordings, CC BY items detected at YouTube
  intake, or sources (a speaker, a channel, a reciter) with a recorded permission and proof link.
- The scheduler skips everything else while the channel is published.
- Quran recordings are treated like any other source.

**Hosting status** needs, and the stories build: a designated agent, notice and takedown, a
repeat-infringer policy (3 strikes in 12 months), acting on actual knowledge, and a short
Broadcaster agreement that puts rights on the publisher. On top of that: a Report button, and a
kill switch through a signed revocation list (the Epic 54 pattern). There is no Maktaba directory.

| # | Story | Phase |
|---|---|---|
| 50.20 | [The Broadcaster tier and guest sharing](../../../specs/epics/50-cloud-no-downtime/story-50-20-broadcaster-tier.md) | B |
| 50.21 | [The broadcast edge](../../../specs/epics/50-cloud-no-downtime/story-50-21-broadcast-edge.md) | B |
| 50.22 | [The home's broadcaster](../../../specs/epics/50-cloud-no-downtime/story-50-22-home-broadcaster.md) | B |
| 50.23 | [Publishable sources and geo](../../../specs/epics/50-cloud-no-downtime/story-50-23-publishable-sources.md) | B |
| 50.24 | [Takedown, reports and repeat infringers](../../../specs/epics/50-cloud-no-downtime/story-50-24-takedown-reports.md) | B |
| 50.25 | [Kill switch and the signed broadcast revocation list](../../../specs/epics/50-cloud-no-downtime/story-50-25-kill-switch.md) | B |
| 50.26 | [Viewer access: invite and open](../../../specs/epics/50-cloud-no-downtime/story-50-26-viewer-access.md) | B |
| 50.27 | [Guest viewers: watch pages and apps](../../../specs/epics/50-cloud-no-downtime/story-50-27-guest-viewers.md) | B |
| 50.28 | [Viewer analytics and broadcast monitoring](../../../specs/epics/50-cloud-no-downtime/story-50-28-analytics-monitoring.md) | B |
| 50.29 | [The broadcast rehearsal and fan-out load test](../../../specs/epics/50-cloud-no-downtime/story-50-29-broadcast-rehearsal.md) | B |

The open questions, each with a recommended default, are in README §14.10. The
broadcasting gate is in §14.11.

## Cloudflare in front (README §15)

The user portal (`app.maktaba.tv`, `api.maktaba.tv`) and the operator
console (`admin.maktaba.tv`, its own hostname) sit behind the Cloudflare proxy: TLS, WAF
managed rules, rate limits on sign-in, sign-up and the API, and Turnstile on the auth pages.
The origin is reached only through a Cloudflare Tunnel from the database leader, and the API
trusts `CF-Connecting-IP` only from it. Relay names stay DNS only (video). The console uses
Maktaba's own staff accounts with mandatory MFA (passkey or TOTP), 15-minute idle timeout,
8-hour cap, re-authentication for sensitive actions, lockout and full audit; admin routes are
404 on every user name. Cloudflare pieces deploy from CI only. Estimated cost: about $25–30 a
month on the Pro plan (about $5 on Free).

| # | Story | Phase |
|---|---|---|
| 50.30 | [Cloudflare in front: TLS, WAF, rate limits and bot protection](../../../specs/epics/50-cloud-no-downtime/story-50-30-cloudflare-edge-rules.md) | 1 |
| 50.31 | [The origin only through Cloudflare](../../../specs/epics/50-cloud-no-downtime/story-50-31-origin-through-cloudflare.md) | 1 |
| 50.32 | [The operator console on admin.maktaba.tv](../../../specs/epics/50-cloud-no-downtime/story-50-32-admin-hostname.md) | 1 |
| 50.33 | [Staff sign-in for the operator console](../../../specs/epics/50-cloud-no-downtime/story-50-33-staff-auth.md) | 1 |
| 50.34 | [Cloudflare deploys from CI](../../../specs/epics/50-cloud-no-downtime/story-50-34-cloudflare-deploys.md) | 1 |
