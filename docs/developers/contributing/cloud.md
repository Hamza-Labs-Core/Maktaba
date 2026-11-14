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
each laid out the same way: **When**, **Check**, **Do**, **Done when**, **Tell users**. A
runbook's **Check** step links to a saved search in the cloud's log view, never to SSH.

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
| The relay pool (off until turned on) | Several relay machines in one pool. Each home keeps its tunnel on one relay and an idle standby tunnel on another, chosen by weighted rendezvous hashing over the healthy relays, so losing a relay costs its homes seconds and adding one moves only the homes it wins. Apps get a `relay_hint` that goes straight to the home's relay, and the witness keeps `relay.maktaba.tv` round robin over the healthy ones. |
| One wildcard certificate per node | `*.relay.maktaba.tv`, issued by a DNS challenge, so the standby node already has it before it is needed. |
| Backups | The database's log and a nightly backup go to Hetzner Object Storage and to Cloudflare R2, encrypted, with 30 days of point-in-time restore. A restore is tried every week. |
| Monitoring | The witness probes a canary home through the relay every 30 seconds; GitHub's runners probe it from outside Hetzner too. Alerts page the owner and link to a runbook, and the same probes set the status page. |
| Logs | Each process writes the same log lines as a home, and the node keeps them for 14 days. Warnings reach the witness within a second and the rest every 10 seconds, so a node that dies leaves its last lines there. The witness indexes every node's lines: the service admin searches the whole cloud and sees its error groups beside the homes' anonymous ones, even while a node is down. A line names a home only by a hash. Each process also serves its error, request and log metrics (`shared/metrics/cloud-series.md`). |

The files are in
[deploy/cloud-ha](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/deploy/cloud-ha)
(the machines' setup, Patroni, etcd, the callback, the deploy and backup scripts, the
monitoring) and [cloud/](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/cloud) (the relay
and the API). The public names and what they point at are in
[docs/cloud-dns.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/cloud-dns.md).

## Signing a TV in with a cloud code

A TV that cannot find its home (a Samsung or LG web app has no mDNS; a TV at a relative's house
is on another network) signs in with a code from Maktaba Cloud. A person signed in on the home
approves it on their phone or the web; they need no cloud account, only a home with remote
access turned on.

| Step | Who | Call |
|---|---|---|
| Ask for a code | the TV, no account | `POST /v1/tv/codes {platform, app_version}` on the cloud: `{code, poll_secret, expires_in: 600, interval: 5, verify_url}`; at most 10 codes per address |
| Read it | the person, on the home | `GET /api/pairing/cloud-code/{code}`: the TV's platform and its address's country, for "Is this your TV?" |
| Approve it | the person, on the home | `POST /api/pairing/cloud-approve {code, as_user?}`: the home makes one of its own device codes for the approver, approves it, and claims the cloud's code with it through its relay agent (`/v1/homes/{id}/tv-codes/{code}/claim`) |
| Or approve it on maktaba.tv | the person, signed in to Maktaba Cloud | `GET /v1/tv/codes/{code}` (what asked), then `POST /v1/tv/codes/{code}/approve {server_id}` for a home that linked their cloud account: the cloud signs a one-use assertion for that account, home and code (purpose `tvcode`) and sends it through the relay to the home's `POST /api/pairing/cloud-assertion`, which approves the code as the linked user |
| Poll | the TV | `POST /v1/tv/codes/poll {code, poll_secret}`: `202` pending, `429` `slow_down`, `200` the home's connections and the grant (once), then `410` |
| Sign in | the TV | `POST /api/pairing/device/token {device_code: grant}` on the first of the home's connections that answers (LAN first, the relay last) |

The cloud keeps a code's poll secret as a hash and its payload sealed, deletes the payload on the
poll that delivers it, and sweeps the rows an hour after they expire. The TV is signed in as the
approver, with their libraries and parental policy, and shows in their list of app sign-ins
(`GET /api/me/app-sign-ins`) as a TV via a cloud code, where it can be signed out. It never acts
as an admin, even when an admin approved it: a cloud code can be read out to anyone anywhere, so
an admin's TV reads every library, named one by one, and its tokens say no admin, also after a
refresh. Whoever approved it, its tokens carry `rst` (restricted): the sign-in mints no
credential of its own (no API token, no approval of another device), the same refusal a
sign-in through the cloud gets. The home keeps the approval under its own ticket prefix, so a
restart between approving and signing in still gives a restricted sign-in. The cloud caps each home's lookups and claims (30, then one every 6 seconds), counted
after its secret is checked.

The QR code a TV shows opens `https://maktaba.tv/tv?c=<code>`, which forwards to Maktaba Cloud's
approve page (`https://app.maktaba.tv/tv`). A person signed in there sees the TV's platform and
country, picks one of the homes that linked their account, and approves. The cloud approves
nothing itself: only a home that linked the account has a user to approve as, and owning a home's
registration is not enough. A home that is offline (`409` `home_offline`) or that the relay cannot
reach (`503` `home_unreachable`) approves nothing, and the TV keeps waiting. The home checks the
assertion against the cloud key it pinned at claim, uses it once, and gives the TV the same
restricted sign-in as any cloud code's TV. The cloud caps each account's lookups and approvals (20,
then one every 3 seconds).

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
| `load-small` | 1,000 homes and 50 players at 5 Mbit/s, with the base commit's relay and then this commit's, on one runner | the relay's CPU per Mbit (its own CPU time, the median of three rounds) and memory per tunnel (its Go live heap, the median of three rounds) no more than 20 % worse than the base's |
| `relay-pool-kill-one` | kills one of three relays under 1,000 homes and 100 streams | no request fails 10 s after; every home holds a new standby within 60 s; the dead relay leaves the DNS round robin within 30 s |
| `rebalance-add-relay` | starts a third relay beside two under 1,000 homes | only the homes it wins move, at most 20 a second; no request fails |
| `relay-plans` | homes on Free, Basic and Pro behind the relay with the plans enforced; a test build sets the plans' numbers not chosen yet, and the payment provider is a fake | Free is refused with its tunnel kept; Basic is asked to fit 3 Mbit/s, paced to it, and a third stream is refused while two play on; Pro past its allowance plays at Basic's quality; pay-as-you-go plays in full and its gigabyte reaches the provider once, through an outage |
| `tv-code` | a TV away from home asks the api role for a code; the canary home claims it through its relay agent | the TV's next poll gets the payload once (then `410`, with no secret in it) and signs in on the home through the relay |

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

What one node carries is measured apart from the rehearsal: `cloud/cmd/maktaba-loadtest` ramps
simulated homes and players against a staging pair until the CPU, the added latency, the stalls,
a dropped tunnel or the memory crosses its limit for 2 minutes, and the last good step becomes a
row of [docs/cloud-sizing.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/cloud-sizing.md).
The monitoring compares the live traffic with it: at 60 % for a week, the next phase starts.
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
