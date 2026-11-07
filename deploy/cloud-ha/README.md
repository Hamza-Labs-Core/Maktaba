# Maktaba Cloud: the Phase 1 pair (no downtime)

Two machines that cover for each other, and a witness that settles who is in charge (Epic 50,
story 50.07). The single-VPS setup in [`deploy/cloud-relay/`](../cloud-relay/) stays for
development and a first demo; production runs this.

```
   relay.maktaba.tv, *.relay.maktaba.tv ──► Floating IP ──► the leader
        ┌──────── node A (FSN1) ────────┐   ┌──────── node B (NBG1) ────────┐
        │ Caddy (its own wildcard)       │   │ Caddy (its own wildcard)       │
        │ the cloud processes (if leader)│   │ (stopped while standby)        │
        │ Postgres 16 + Patroni ─ sync ──┼──►│ Postgres 16 + Patroni          │
        │ etcd · Redis (cache only)      │   │ etcd · Redis (cache only)      │
        └──────────────┬─────────────────┘   └──────────────┬─────────────────┘
                       └──── private network ──► witness W (HEL1): etcd ◄┘
```

**The rule: everything follows the database leader.** Patroni holds a 30 s leader lease in
etcd (three members, so two nodes can never both win). On the leader, Patroni's role-change
callback ([`bin/on-role-change`](bin/on-role-change)) moves the Floating IP to it (one Hetzner
API call, retried; `fip_assign_failed` pages after 60 s) and starts the cloud processes
(`maktaba-cloud-active.target`); on any other role it stops them. The leader guard
([`bin/leader-guard`](bin/leader-guard)) also stops them as soon as Patroni stops calling the
node the primary, without waiting for a demotion to finish. DNS never changes on failover.

Replication is synchronous with fallback (`synchronous_mode: true`, `strict: false`): a commit
returns once the standby has it (RPO 0 while both run), and a primary whose standby is gone
keeps taking writes alone.

## Proven in CI

The cloud rehearsal ([docs/cloud-rehearsal.md](../../docs/cloud-rehearsal.md)) runs this
directory's files on three containers (`tools/rehearsal-cloud/compose-ha.yml`), with a fake
Hetzner Floating IP API:

| Scenario | Measured locally |
|---|---|
| `failover-kill-leader` (300 homes, 50 streams, the leader killed) | the other node led after 38 s; every home back after 53 s; remote play down 47 s (budget 3 min); 0 of 243 commits lost |
| `failover-network-partition` (the leader cut from B and W, still running) | read-only after 19 s, its relay stopped after 20 s (budget 40 s); never two writable primaries; 0 commits lost; the IP on the new leader; the old one back as a streaming replica |
| `switchover` (`bin/switchover` under traffic) | done in 3.2 s; remote play paused 2 s (budget 15 s); 0 commits lost |

The scripts' own tests: [`tests/scripts-test.sh`](tests/scripts-test.sh) (the callback, the
guard, the switchover's checks).

## What the owner creates (nothing in the repo calls these APIs)

1. A Hetzner project **maktaba-prod** with only these resources.
2. A private network `10.50.0.0/16`, subnet eu-central.
3. Servers, Ubuntu 24.04, SSH key only: **A** in FSN1 and **B** in NBG1 (each at least 2 vCPU,
   4 GB; backups on), and the witness **W**, a CPX22, in HEL1.
4. One Floating IPv4 in eu-central, assigned to A.
5. A Hetzner Cloud Firewall on all three: public 22 from the owner's and the deploy runner's
   addresses, 80 and 443 on A and B; everything else from the private network.
6. A project API token (read/write) in `/etc/maktaba/hcloud-token` on A and B (0600) and as
   the GitHub `production` secret `HCLOUD_TOKEN`.
7. The Cloudflare DNS-edit token (Zone → DNS → Edit on `maktaba.tv` only) in
   `/etc/maktaba/cloudflare.token` on A and B, and the records of
   [docs/cloud-dns.md](../../docs/cloud-dns.md) pointed at the Floating IP.
8. The etcd certificates, on the owner's machine:
   `deploy/cloud-ha/bin/make-etcd-certs ~/maktaba-etcd node-a=<A's private IP> node-b=<B's> witness=<W's>`;
   copy `ca.crt` and each member's pair to its `/etc/etcd/pki/`. Keep the CA key in the
   password manager.
9. `/etc/maktaba/cloud-ha.env` on each machine, from [`cloud-ha.env.example`](cloud-ha.env.example).

Then, in this order:

```bash
sudo deploy/cloud-ha/setup.sh --role witness   # on W
sudo deploy/cloud-ha/setup.sh --role a         # on A: bootstraps the cluster
sudo deploy/cloud-ha/setup.sh --role b         # on B: joins as a replica
```

Before the launch gate (Epic 50 README §11): one planned switchover and one forced failover
(power off the leader in the Hetzner console), each within its budget.

## Deploys with no downtime (story 50.08)

`deploy/cloud-ha/deploy.sh <tag>` (relay.yml's deploy job when the repository variable
`CLOUD_HA` is `true`, or a laptop): Patroni says which node leads; the **standby** fetches the
release from its signed image and smoke-tests it against its own replica (a bad release stops
here); the **leader** runs the migrations (expand only: `tools/lint/cloud-expand-only.sh`
refuses a contract change in a release) and [`bin/bluegreen`](bin/bluegreen): the new colour
starts beside the old one and must stay ready for 30 s, then the old colour gets SIGTERM, its
`/readyz` turns `503 draining` so the edge sends new work to the new colour, and it drains its
homes' tunnels to it (DRAIN, 50.03); a 10-minute watch rolls back on its own. The colour and the
tag are recorded on both nodes, so a failover starts the same release.

The edge is never reloaded during a deploy: both colours are its upstreams, health-checked
every second, and readiness moves the traffic (a reload through Caddy's admin API cut streams
in flight in the rehearsal). The cloud rehearsal's `deploy-blue-green` (1,000 homes, 0 failed
requests) and `deploy-bad-release-rollback` prove it before any deploy.

GitHub `production` environment secrets for it: `RELAY_HOST_A`, `RELAY_HOST_B` (the nodes' SSH
addresses), `RELAY_SSH_USER`, `RELAY_SSH_KEY` (the `deploy` user's key, which works only for
that user).

## Day to day

| What | How |
|---|---|
| Who leads, how far behind the replica is | `patronictl -c /etc/patroni/patroni.yml list` |
| Planned node work | `sudo switchover --announced` (after the status page notice, 50.11): refuses unless the replica streams with zero lag and its Caddy answers |
| Keep a node from leading while it is looked at | `patronictl -c /etc/patroni/patroni.yml edit-config` → its `nofailover` tag; or `patronictl pause` to stop automatic failover altogether |
| The Hetzner API is down and the IP did not move | the DNS-swap runbook (50.12): point the relay names at the new leader's own IP through Cloudflare (TTL 60 s) |

## Known limits

- **A commit cancelled while it waits for the standby is done on the primary only.** Postgres
  reports it committed ("canceling wait for synchronous replication") although the standby
  never got it; if the primary is then lost, so is that commit. The rehearsal's writer found it
  during a partition. Every write path of the cloud with a short deadline (the meter's flush)
  can see it during a partition; the bytes of one flush are the most it costs today.
- The witness runs etcd only; 50.10 adds monitoring and the canary home as lower-priority
  guests (`CPUWeight`), 50.09 the restore drill.
