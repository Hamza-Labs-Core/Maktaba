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

## The status page

[status.maktaba.tv](https://status.maktaba.tv) answers "is it me or them?". It is hosted on
GitHub Pages, off Hetzner, so it still loads when the cloud is down, and no one updates it by
hand: the probes set each part's state.

- **The parts:** Remote access (the relay), Sign-in and accounts (the API), Notifications,
  and a pointer to the payment provider's own status page. Each reads **Working**, **Slow**,
  **Down** or **Maintenance**, with a 90-day uptime bar, one cell a day.
- **Two networks decide.** The witness probes from inside Hetzner, and GitHub's runners from
  outside it. Both failing twice in a row (30 seconds apart) is **Down**; one alone is
  **Slow**, because it is probably that probe's own network. Recovering also takes two passing
  rounds, so one lost packet never flashes a banner.
- **The uptime target, with no credit.** The page shows the 99.9 % monthly target for remote
  access and last month's measured figure. It is a target, not a guarantee: no credit or
  refund is given for downtime.
- **English and Arabic.** The page follows the browser's language on a first visit, and has a
  switch. It reads fully without JavaScript.
- **Incidents and maintenance** are posts in the repository, each with an English and an
  Arabic title and text. Maintenance is posted at least 48 hours ahead.

### `summary.json` for apps

Apps read `https://status.maktaba.tv/summary.json` when they start, every 5 minutes while
open, and at once when a relayed request fails. They never wait on it: a failed fetch shows
nothing.

```json
{ "updated_at": "2026-11-03T10:15:00Z",
  "overall": "degraded",
  "components": { "relay": "degraded", "api": "operational", "push": "operational" },
  "incident": { "id": "2026-11-03-relay-slow", "state": "identified", "components": ["relay"],
                "title": { "en": "Remote access is slow", "ar": "الوصول عن بُعد بطيء" },
                "url": "https://status.maktaba.tv/i/2026-11-03-relay-slow" },
  "maintenance": [ { "id": "2026-11-04-db-upgrade", "starts_at": "2026-11-05T02:00:00Z",
                     "ends_at": "2026-11-05T02:30:00Z", "components": ["relay", "api"],
                     "title": { "en": "Database upgrade", "ar": "ترقية قاعدة البيانات" },
                     "url": "https://status.maktaba.tv/i/2026-11-04-db-upgrade" } ] }
```

A state is `operational`, `degraded`, `outage` or `maintenance`; `overall` is the worst of them.
Add the minute to the address (`summary.json?t=<unix minute>`): GitHub Pages lets caches keep
a copy for 10 minutes. The page's source, its tests and the posts are in
[status/](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/status); how to post is in
[docs/runbooks/cloud/status-page.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/runbooks/cloud/status-page.md).

## When something goes wrong

Failover and alerts handle the failures that were planned for; the rest need a person. Every
alert carries a severity and links to a runbook in
[docs/runbooks/cloud](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/docs/runbooks/cloud),
each laid out the same way: **When**, **Check**, **Do**, **Done when**, **Tell users**.

| Severity | Means | Response | Users hear |
|---|---|---|---|
| SEV1 | paying users can't play remotely, or data is at risk | within 15 minutes, any hour; the phone is paged every 5 minutes until acknowledged | the status page within 15 minutes, then every 30 minutes |
| SEV2 | degraded, or one safety net gone | within the hour by day | the status page if they notice |
| SEV3 | no user impact yet | the next working day | nothing |

Recovery is one click, never a terminal session: **Cloud switchover**, **Cloud failover** and
**Cloud DNS swap** are GitHub Actions workflows, approved by the owner, that run the same
scripts the cloud rehearsal runs first against its own pair (`runbook-switchover`,
`runbook-forced-failover`, `runbook-dns-swap`). Updates for users come from templates in
English and Arabic, and every SEV1 and every automatic failover gets a blameless postmortem
within 5 days. The 99.9 % target is a target, not a guarantee: no credit or refund is given
for downtime, and a check in CI keeps any page or template from offering one.

## How it is built

| Part | What it does |
|---|---|
| Two nodes, A and B, and a witness W | Postgres 16 under Patroni on A and B, with synchronous replication: a commit returns once both have it. etcd on all three holds a 30-second leader lease, so the two can never both lead. |
| The Floating IP | The relay's address. When a node becomes the leader, Patroni's callback moves the address to it and starts the cloud there. DNS never changes on a failover. |
| Blue and green relay processes | A deploy starts the new release beside the old one, waits for it to stay healthy for 30 seconds, then drains the old one. A watch rolls back on its own. |
| The tunnel directory | Which relay process holds each home's tunnel, so a process without it forwards the request to the one with it. |
| One wildcard certificate per node | `*.relay.maktaba.tv`, issued by a DNS challenge, so the standby node already has it before it is needed. |
| Backups | The database's log and a nightly backup go to Hetzner Object Storage and to Cloudflare R2, encrypted, with 30 days of point-in-time restore. A restore is tried every week. |
| Monitoring | The witness probes a canary home through the relay every 30 seconds; GitHub's runners probe it from outside Hetzner too. Alerts page the owner and link to a runbook, and the same probes set the status page. |

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
| `status-feed` | stops every relay process | the status page's `summary.json` says remote access is down within two probe rounds, and working again after two passing ones |
| `runbook-switchover`, `runbook-forced-failover`, `runbook-dns-swap` | runs the one-click recovery workflows' scripts; for the DNS swap, with the Hetzner API down | each does what its runbook says against the real pair: a new leader, the old one fenced, the relay's names on the new leader |

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
