# Story 50.07 — Two nodes, one address

> Epic 50 · Phase 1 · Depends on: 50.02, 50.04, 50.05, 50.06 · Needed by: 50.08, 50.09, 50.10

## Why

Today the whole cloud runs on one VPS (`deploy/cloud-relay/`). If that machine reboots, loses
its disk or its Hetzner host has a fault, remote play stops for every paying home until it is
back, and a lost disk loses every account. This story adds a second machine that takes over by
itself, and a small third machine (the witness) that settles who is in charge, so the two can
never both think they are.

## Description

### 1. The machines

| Node | Location | Runs |
|---|---|---|
| **A** | FSN1 | Caddy, Postgres + Patroni, etcd, Redis, the cloud processes (when leader) |
| **B** | NBG1 | the same; the cloud processes are stopped while B is the standby |
| **W** (witness) | HEL1 | etcd only for this story (50.10 adds monitoring and the canary home, 50.09 the restore drill) |

**The witness machine (owner decision, 2026-11-03; README §13 decision 3):** a **Hetzner
CPX22** (€19.99 a month), ordered now rather than waiting for CX23 to come back. Its witness
job is **etcd only**, the third vote, which needs about **1–2 vCPU, 1–2 GB RAM and ~20 GB
disk**; a CPX22 covers that with room. The monitoring, canary home and restore drill that later
stories put on the same machine are **guests, not part of the witness role**: they run under
systemd with lower `CPUWeight` / `IOWeight` than etcd and their own disk quota, so they can
never starve etcd's heartbeats; if they outgrow it they move to their own machine and W stays
etcd only. **Cloudflare can't host the witness**: Cloudflare sells no VMs, and Cloudflare
Tunnel is only a connector into a machine that already exists, so W must be a server we run
(on Hetzner, in a third data centre).

All three sit on one **Hetzner private network** in the eu-central zone. A Floating IP can only
move inside one network zone, which is why A and B are in two data centres of the same zone.

### 2. The rule: everything follows the database leader

**Patroni** runs Postgres on A and B. It keeps a leader lease in **etcd** (three members: A, B,
W). Whoever holds the lease is the Postgres primary; the other is a streaming replica.

Patroni settings (`deploy/cloud-ha/patroni/patroni.yml`):

| Setting | Value | Why |
|---|---|---|
| `ttl` | 30 | the lease lasts 30 s: a dead leader is noticed within 30 s |
| `loop_wait` | 10 | Patroni checks every 10 s |
| `retry_timeout` | 10 | how long a leader tries etcd/Postgres before it gives up the lease |
| `synchronous_mode` | true | a commit waits for B: no committed write is lost while both run (RPO 0) |
| `synchronous_mode_strict` | false | if B is gone, A keeps taking writes alone instead of stopping |
| `maximum_lag_on_failover` | 1 MB | a replica far behind is never promoted (with sync mode it isn't behind) |
| `watchdog.mode` | required, `softdog` | a leader that hangs is rebooted by the kernel before its lease runs out, so a frozen old primary can't come back and write |
| `postgresql.parameters` | `wal_level=replica`, `max_wal_senders=10`, `hot_standby=on`, `archive_mode=on` (archive command from 50.09) | |

etcd: three members, peer and client traffic with TLS on the private network only, certificates
made by `setup.sh` from a small CA kept on the owner's machine and in the password manager.

### 3. The role-change callback

Patroni calls `deploy/cloud-ha/bin/on-role-change` with the new role. On `master`/`primary`:

1. **Move the Floating IP** to this node: `POST /v1/floating_ips/{id}/actions/assign` with
   `{"server": <this server's id>}`, using the production project token. Retry with backoff
   (1, 2, 4, 8 s) up to 60 s; then raise the `fip_assign_failed` alert (50.10) and keep trying
   every 30 s.
2. Make sure the Floating IP is configured on the OS (`ip addr add <fip>/32 dev eth0`; it is
   also in the netplan config on both nodes, so it is always present on the interface. Hetzner
   routes it only to the node it is assigned to).
3. `systemctl start maktaba-cloud-active.target` (api, relay and worker, the colour from 50.08).

On `replica` or `demoted`: `systemctl stop maktaba-cloud-active.target`. Postgres is
already read-only. The callback never moves the Floating IP away (the new leader takes it).

Caddy runs on **both** nodes all the time, with its own wildcard certificate (50.02), so the
standby needs no TLS work when it takes over. Redis runs on each node as a cache only (50.05),
not replicated.

### 4. Timing (RTO)

| Step | Time |
|---|---|
| Leader dies; its lease runs out | ≤ 30 s |
| B takes the lease and promotes | ~5 s |
| Floating IP moves | not published by Hetzner; one user reports up to 60 s (**unverified**), expect a few seconds |
| Cloud processes start on B | ~2 s |
| Homes reconnect with a resume ticket (50.03), jittered | p95 ≤ 20 s |
| **Total** | **expected ~90 s; target ≤ 3 min** (README §4.2) |

While homes reconnect, requests for a home seen in the last 2 min are held up to 15 s (50.04).

### 5. Planned switchover

`deploy/cloud-ha/bin/switchover` wraps `patronictl switchover --leader <current> --candidate <other> --force`
after checks: replica streaming with zero lag, the other node's Caddy healthy, a note posted to
the status page 48 h before (50.11). Expected pause in remote streams: ≤ 15 s (README §4.2).
It drops the tunnels on the old node (their TCP connections were to the Floating IP), and homes
reconnect with tickets.

### 6. Firewall

- **Hetzner Cloud Firewall** on all three: public in = 22 (owner IPs and the GitHub deploy
  runner, see 50.08), 80, 443 on A and B; 22 only on W. Everything else from the private
  network only.
- **UFW** on each node says the same, so a mistake in one layer isn't enough.
- Postgres (5432), Patroni REST (8008), etcd (2379/2380), Redis (6379) and the cloud processes
  listen only on the private address or loopback.

### 7. What the owner creates (we call no API)

1. A Hetzner project **maktaba-prod** with only these resources.
2. Private network `10.50.0.0/16`, subnet eu-central.
3. Servers A (FSN1), B (NBG1), W (HEL1), Ubuntu 24.04, SSH key only, backups on for A and B.
   Plans per README §5; W is a CPX22.
4. One Floating IPv4 in eu-central, first assigned to A.
5. A project API token (read/write), stored on A and B only in
   `/etc/maktaba/hcloud-token` (0600, root) and as a GitHub `production` secret.
6. DNS (50.01): `relay`, `*.relay` and the origin of `api` pointed at the Floating IP.
   When 50.15 lands, nodes A and B also run `--role=dns` on port 53 (UDP and TCP, opened in the
   Hetzner firewall and UFW) for the direct-name domains' delegation, on their own addresses so
   it answers from both nodes at once. The delegation is for **each separate direct-name
   domain** (50.15 §4), never for a name under `maktaba.tv`.

### 8. Alternatives weighed

| Option | Why not |
|---|---|
| keepalived (VRRP) + a script that moves the IP | Two nodes can't break a tie: a network split makes both primary (split brain). No database awareness. |
| pg_auto_failover | Its monitor node is itself a single point of failure: lose it, lose failover. |
| repmgr | Failover works, but fencing and the IP move are left to us; the witness logic is weaker than an etcd lease. |
| A home-made controller in Go | More code to get wrong in the one place that must never be wrong. Patroni is used widely and does exactly this. |
| DNS failover (change the A record) | Waits on DNS caches that ignore short TTLs; minutes, not seconds. Kept as the manual path when Hetzner's API is down (§9). |

### 9. Failure modes

| What fails | What happens |
|---|---|
| W down | A and B still have 2 of 3 etcd votes. Nothing changes. Alert. |
| B down | A keeps the lease; sync mode falls back to async (not strict); writes go on. Alert; RPO is no longer 0 until B is back. |
| A down | B takes the lease after ≤ 30 s, promotes, moves the IP. §4. |
| Network split: A alone, B + W together | A can't renew its lease, so it demotes itself within `retry_timeout` (and the watchdog fences it if it hangs). B + W have the majority; B promotes and takes the IP. A comes back as a replica (Patroni rewinds it with `pg_rewind`). |
| Hetzner API down when the IP must move | B is primary but traffic still goes to A's (dead) address. The callback keeps retrying and pages. Runbook (50.12): point the DNS names at B's own IP through Cloudflare (TTL 60 s). |
| etcd loses 2 of 3 | No lease can be renewed: Patroni turns the primary read-only (by design). The cloud stays up for the relay (50.05 caches) but writes fail. Page. |

## Acceptance criteria

- **Given** A is leader and both nodes are healthy,
  **when** a row is committed on A,
  **then** it is on B before the commit returns (synchronous).
- **Given** A is leader,
  **when** A is powered off,
  **then** within 3 min B is primary, holds the Floating IP, runs the cloud processes, and the
  canary home (50.10) answers through `relay.maktaba.tv`, with no one touching anything.
- **Given** A is cut off from B and W but still running,
  **when** 30 s pass,
  **then** A's Postgres is read-only and A's cloud processes are stopped, B is primary, and at no
  moment do both accept writes.
- **Given** B is down,
  **when** writes arrive on A,
  **then** they succeed (async fallback) and the `replica_missing` alert fires.
- **Given** the owner runs `switchover`,
  **when** it completes,
  **then** B is primary with zero lost commits, and remote streams pause for ≤ 15 s.
- **Given** the Hetzner API refuses the IP move,
  **when** the callback has retried for 60 s,
  **then** `fip_assign_failed` pages the owner with the DNS-swap runbook link.
- **Given** the old leader A comes back after a failover,
  **when** Patroni starts,
  **then** A rejoins as a replica (rewound if needed) and does not take the IP.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (shell, `bats`) | `on-role-change` with a fake Hetzner API | role `primary` | one assign call; retries on 5xx; target started |
| T02 | unit | `on-role-change` | role `replica` | target stopped; no assign call |
| T03 | unit | `switchover` | lag > 0 / Caddy unhealthy | refuses with a reason |
| T04 | rehearsal (CI) | 50.06 `failover-kill-leader`: A, B, W containers, fake Hetzner API, 1,000 simulated homes | `docker kill` A | B primary ≤ 45 s, canary OK ≤ 3 min, 0 lost commits |
| T05 | rehearsal (CI) | `failover-network-partition` | cut A from B and W | A read-only ≤ 40 s; never two writers (checked by a writer that alternates nodes) |
| T06 | rehearsal (CI) | `switchover` | run the script | 0 lost commits; stream pause ≤ 15 s |
| T07 | rehearsal (CI) | `cert-on-standby` | fail over | first TLS handshake on B succeeds with no ACME call |
| T08 | rehearsal (CI) | fake Hetzner API returns 503 | kill A | `fip_assign_failed` raised after 60 s |

## Edge cases

- **Both A and B lose the private network but keep the internet.** Neither reaches W and the
  other; both lose the lease; both go read-only. Remote play continues on A for homes in the
  relay cache (50.05) until the network is back. This is safer than two writers.
- **The watchdog reboots A in a loop.** Patroni's `nofailover` tag can be set on A by the owner
  from the runbook while it is investigated.
- **Clock drift.** etcd leases use the etcd leader's clock; chrony runs on every node.
- **Disk full on A.** Postgres stops; Patroni fails its health check and gives up the lease; B
  takes over. Disk alerts at 80 % (50.10) should come first.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/cloud-ha/README.md` (new) | What the setup is, the owner's resource list, day-to-day commands. |
| `deploy/cloud-ha/setup.sh` (new) | Idempotent; `--role a|b|witness`. Installs Postgres 16, Patroni, etcd, Caddy, Redis, chrony, softdog, the units, UFW. |
| `deploy/cloud-ha/patroni/patroni.yml` (new) | Settings in §2, templated per node. |
| `deploy/cloud-ha/etcd/etcd.conf.yml` (new) | Three members, TLS. |
| `deploy/cloud-ha/bin/on-role-change` (new) | §3. |
| `deploy/cloud-ha/bin/switchover` (new) | §5. |
| `deploy/cloud-ha/systemd/maktaba-cloud-active.target` and the per-role units (new) | Started and stopped by the callback. |
| `deploy/cloud-ha/netplan/60-floating-ip.yaml` (new) | The Floating IP on `eth0`. |
| `deploy/cloud-ha/caddy/Caddyfile` (new) | From `deploy/cloud-relay/Caddyfile`, with the wildcard site from 50.02. |
| `deploy/cloud-relay/README.md` | Say it is the single-VPS / dev setup, and point at `cloud-ha` for production. |
| `tools/rehearsal/cloud/` (new, owned by 50.06) | The A/B/W compose and the fake Hetzner API this story's scenarios use. |

### Tests

- `bats` tests for the scripts (T01–T03), run by `relay.yml` when `deploy/cloud-ha/**` changes.
- **CI only:** 50.06 scenarios `failover-kill-leader`, `failover-network-partition`,
  `switchover`, `cert-on-standby`.

### Rollout

1. Merge with the rehearsals green. Nothing runs anywhere yet.
2. The owner creates the resources in §7.
3. `setup.sh --role witness` on W, then `--role a` on A (bootstraps the cluster), then
   `--role b` on B (joins as replica).
4. If the single VPS is already live: B joins it as a replica first (Patroni can take over an
   existing data directory), then switchover to the new pair. Announced on the status page.
5. Run one forced failover and one switchover before the launch gate (README §11).

### Rollback

- Before launch: delete the pair; `deploy/cloud-relay/` still works on one VPS.
- After launch: `patronictl pause` stops automatic failover (the cluster then behaves like one
  primary with a replica), if Patroni itself misbehaves.

### Dependencies

50.02 (certificate on both nodes), 50.04 (holding requests during the reconnect), 50.05 (the
relay rides out the database switching), 50.06 (the rehearsal harness). Needed by 50.08, 50.09,
50.10.
