# Maktaba Cloud: how it stays up

Maktaba Cloud is the service a home links to for remote access: the relay that carries the
home's traffic when it is away from the home's network, and the API behind accounts and
plans (Epic 50). Remote access people pay for must not stop for a reboot, a deploy or a lost
machine. This page says how the cloud is built for that, what a home owner sees when
something fails, and how to work on it without a server.

## What an owner sees

- **A deploy is invisible.** The relay process that is replaced asks each home to move to the
  new one before it stops (a `DRAIN` frame). The home opens a new tunnel first, then lets the
  old one finish the videos it is playing. A home reconnects with a short-lived resume ticket,
  so a thousand homes reconnecting cost no password checks.
- **A machine that fails is replaced by itself.** Two machines cover for each other, and a third
  small one breaks ties. Remote play comes back within about a minute, with no one touching
  anything; at most 3 minutes is the target.
- **A home that is reconnecting is waited for.** A request for a home seen in the last 2
  minutes waits up to 15 seconds for it, then answers `503 home_reconnecting` with
  `Retry-After: 5`. A browser gets a page that retries by itself, in English or Arabic.
- **The database failing over does not stop play.** The relay keeps serving the homes it knows
  from memory for up to 5 minutes without the database, and counts the bytes it carries
  until it can write them.
- **Play at home never depends on the cloud.** Signing in on the home and playing on its own
  network work with the cloud unreachable.

## How it is built

| Part | What it does |
|---|---|
| Two nodes, A and B, and a witness W | Postgres 16 under Patroni on A and B, with synchronous replication: a commit returns once both have it. etcd on all three holds a 30-second leader lease, so the two can never both lead. |
| The Floating IP | The relay's address. When a node becomes the leader, Patroni's callback moves the address to it and starts the cloud there. DNS never changes on a failover. |
| Blue and green relay processes | A deploy starts the new release beside the old one, waits for it to stay healthy for 30 seconds, then drains the old one. A watch rolls back on its own. |
| The tunnel directory | Which relay process holds each home's tunnel, so a process without it forwards the request to the one with it. |
| One wildcard certificate per node | `*.relay.maktaba.tv`, issued by a DNS challenge, so the standby node already has it before it is needed. |
| Backups | The database's log and a nightly backup go to Hetzner Object Storage and to Cloudflare R2, encrypted, with 30 days of point-in-time restore. A restore is tried every week. |
| Monitoring | The witness probes a canary home through the relay every 30 seconds; GitHub's runners probe it from outside Hetzner too. Alerts page the owner and link to a runbook. |

The files are in
[deploy/cloud-ha](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/deploy/cloud-ha)
(the machines' setup, Patroni, etcd, the callback, the deploy and backup scripts, the
monitoring) and [cloud/](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/cloud) (the relay
and the API). The public names and what they point at are in
[docs/cloud-dns.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/cloud-dns.md).

## The cloud rehearsal

Every failure path is proven in CI before it reaches a machine with users on it. The cloud
rehearsal
([.github/workflows/cloud-rehearsal.yml](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/.github/workflows/cloud-rehearsal.yml))
runs the real components in Docker on the runner, with simulated homes and players, and
breaks them on purpose:

| Scenario | What it breaks | What must hold |
|---|---|---|
| `drain-1k` | stops a relay process under 1,000 homes | every home moved within 15 s; no request fails |
| `db-blip` | kills the database for 15 s | no request fails; the bytes are counted |
| `failover-kill-leader` | kills the leading node | the other leads within 45 s; play back within 3 min; no commit lost |
| `failover-network-partition` | cuts the leader off, still running | never two writers; it stops its relay within 40 s |
| `switchover` | a planned switch under traffic | play pauses at most 15 s |
| `deploy-blue-green` | a new release under 1,000 homes | no request fails |
| `deploy-bad-release-rollback` | a release that breaks after 20 s | stopped before users see it |
| `restore-pitr` | destroys both nodes | the database comes back with exactly the rows up to the chosen time |
| `cert-on-standby` | fresh nodes | each holds its own wildcard certificate before any traffic |

A cloud deploy is refused unless the rehearsal passed on that exact commit. No Hetzner,
Cloudflare or Let's Encrypt host is ever contacted: a fake Hetzner API moves the address,
Pebble (Let's Encrypt's test CA) issues the certificates, and S3 stand-ins hold the backups.

Run one scenario on your machine (it needs Docker):

```bash
docker build -f tools/rehearsal-cloud/Dockerfile -t maktaba-cloud:rehearsal .
cd cloud
go test -tags cloudrehearsal -run 'TestScenario/drain-1k$' -timeout 30m -v ./rehearsal/
```

`REHEARSAL_HOMES` and `REHEARSAL_STREAMS` make a scenario smaller.
[docs/cloud-rehearsal.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/cloud-rehearsal.md)
lists the images the other scenarios need.

## Rules for changes to the cloud

- **A cloud migration only adds.** A release runs beside the previous one during a deploy, so
  a migration may add a table, a nullable column or an index, but not drop, rename, change a
  type or set `NOT NULL`. That is a later release's "contract" migration, marked on its first
  line (`-- contract: …`). `tools/lint/cloud-expand-only.sh` checks it.
- **Nothing on a request's path waits on the database** for a home the relay already knows.
- **A health check answers from memory.** A check that does the work it reports on takes its
  node out of rotation when that work is slow.
- **Redis is a cache.** Anything in it may vanish; durable state lives in Postgres.
