# Epic 50 — Maktaba Cloud with no downtime

> **Status:** spec (2026-11-03). **Source:** [`specs/epics/50-cloud-no-downtime/`](../../../specs/epics/50-cloud-no-downtime/README.md).
> **Anchors:** [`architecture.md` §13](../../../specs/architecture.md#13-cloud-relay-architecture) · [Epic 25](epic-25-cloud-relay.md) · [`deploy/cloud-relay/`](../../../deploy/cloud-relay/README.md) · [research](../../../specs/epics/50-cloud-no-downtime/research.md).

## Goal

The owner, 2026-11-03: *"if we are letting people pay then there should be no downtime"*.

Maktaba Cloud's relay is built (Epic 25) but deployed as one VPS running relay, API, Postgres,
Redis and Caddy together. That's fine for a demo but not for paying users. This epic takes it
to **two nodes that cover for each other** before paid plans open (Phase 1), then to a **pool
of relays** (Phase 2).

## Targets

| | Phase 1 | Phase 2 |
|---|---|---|
| Availability | 99.9 % a month (43.8 min) | 99.95 % (21.9 min) |
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
  Object Storage, a canary home probed from outside, a status page off Hetzner.
- **Phase 1b:** direct remote access like Plex: UPnP / NAT-PMP, an outside reachability check,
  per-home HTTPS names.
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

The dependency graph and launch gate are in the [epic README](../../../specs/epics/50-cloud-no-downtime/README.md#10-stories).

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
  a cached connection list. **Avoid:** local play depending on the cloud, a status page updated
  by hand.
- **Names:** users get `maktaba.tv`, dev gets `maktaba.dev`. `maktaba.app` is not ours; older
  specs that say it are fixed by story 50.01.

## Costs (as of 2026-11-03)

Hetzner raised prices twice in 2026, and its CX and CAX plans say "currently not available".
Phase 1 is about **€79 a month** with plans that can be ordered today (2 × CPX22 + a CPX22
witness, backups, Floating IP, Object Storage), or about **€39** if CX/CAX come back. Phase 2
at about 10,000 homes is about **€481**. Details and sources: epic README §5 and research §4.
