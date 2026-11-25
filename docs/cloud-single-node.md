# Maktaba Cloud on one node

> The cloud as it runs today: one Hetzner machine per environment, QA and production, each
> the whole cloud (relay, api, worker, database, web app). It is the smallest real setup for
> one household. The Phase 1 pair ([`deploy/cloud-ha/`](../deploy/cloud-ha/README.md), Epic 50)
> stays the design for more: on one node, a reboot or a deploy stops remote play for up to
> a minute or two, and the machine's disk is the only copy of the database between backups.

## The machines

| | QA | Production |
|---|---|---|
| Host (Hetzner, Helsinki) | environment `qa`'s secret `NODE_HOST` | environment `prod`'s secret `NODE_HOST` |
| Size | 1 vCPU, 2 GB | 2 vCPU, 4 GB |
| Names | `staging.relay.maktaba.dev`, `*.staging.relay.maktaba.dev`, `staging-api.maktaba.dev`, `staging-app.maktaba.dev` | `relay.maktaba.tv`, `*.relay.maktaba.tv`, `api.maktaba.tv`, `app.maktaba.tv` |
| Who uses it | the deploy's tests, never people | the household |

Both run Ubuntu 26.04 and the same files (`deploy/cloud-relay`), with each target's names in
`deploy/cloud-relay/targets/<target>.env`. SSH is root with the `RELAY_SSH_KEY` secret, and the
host key is pinned (each environment's secret `NODE_KNOWN_HOSTS`). Nothing reaches a node by
hand; every change goes through the workflow below.

## What runs on a node

One Docker Compose stack in `/opt/maktaba-relay`, started at boot by `maktaba-relay.service`:

| Service | What | Reached by |
|---|---|---|
| `caddy` | TLS for every name, and the cloud web app's files | the internet, on 80 and 443 only |
| `maktaba-cloud` | the relay (`--role relay`): the homes' tunnels, `<slug>.<relay name>` | Caddy |
| `api` | accounts, sign-in, claims, plans, the homes list, assertions (`--role api`), on `172.30.77.10:8090`; the operator console's listener on `:8091` | Caddy (`api` and `app` names); the operator tool, from the node only |
| `worker` | bandwidth roll-ups, expiries, account purges (`--role worker`) | nobody |
| `postgres` 16 | every account, home and meter | the stack's network only |
| `redis` 7 | a cache, nothing kept | the stack's network only |

The images are pinned by digest and verified with cosign before each deploy: `maktaba-cloud`
and `maktaba-server` (the test home) as `release.yml` signed them at the release's tag,
`maktaba-caddy` as `relay.yml` signed it on main.

### The licence signing key

Every release embeds the public half of one Ed25519 key (`MAKTABA_LICENSE_PUBKEY`,
[`docs/licensing.md`](licensing.md)), and a home applies only licences signed with its private
half. The cloud signs licences and home assertions with its entitlement key, so a node must hold
that same key: the deploy installs it from the `MAKTABA_LICENSE_SIGNING_KEY` secret
(`setup.sh`, on stdin, `secrets/entitlement.key`, the api restarted on a change). A node
without it generates its own key, which no released home trusts: such a home stays unlicensed,
its `cloud.relay` component off, and it never reports its linked accounts, so signing in through
the cloud fails (the e2e's `link`).

### The bot check

With the environment's `TURNSTILE_SECRET_KEY` and `TURNSTILE_SITE_KEY` secrets the web app's sign-up,
reset, resend and sign-in pages ask for Cloudflare Turnstile's check ([`cloud-mail.md`](cloud-mail.md)).
Without both it is off, and the deploy says so.

### Certificates

`RELAY_TLS` in `.env`. With the `CLOUDFLARE_DNS_TOKEN` secret (a token with Zone → DNS → Edit on
maktaba.tv and maktaba.dev only), it is `dns`: the deploy writes the node's four A records (DNS
only, TTL 300, `ops/dns-records.py`) and Caddy holds one wildcard certificate for every home,
plus the apex, the api's and the app's, by DNS-01. Without the token, `http` (records made by
hand; HTTP-01, and each home's name on its first use) or, on QA only, `internal` (Caddy's own
CA, which the tests trust).

### Differences from the Phase 1 design

- **The api is behind Caddy, not the Cloudflare Tunnel.** Its name is DNS only, like the relay's.
  The api still listens on a private address only and believes `CF-Connecting-IP` from Caddy's
  address alone; Caddy writes it from the connection and drops any a client sent
  (`server.trusted_proxies`, story 50.31's rule, kept).
- **The web app is Caddy's, not a Worker.** Caddy serves `web/cloud`'s bundle on the app name
  with the Worker's headers, and `/v1/*` to the api on the same origin.
- **No broadcasting.** `broadcast.enabled` is false and no broadcast edge is deployed. A release
  whose broadcast or moderation routes answer anything but 404 fails QA's test, so it can't reach
  production.
- **No Floating IP, no standby, no witness.** The relay's drain is off
  (there is no second relay to move homes to): a restart drops the tunnels, and homes dial back
  within seconds (the test fails past a minute).
- **Mail through Resend, with the node's key.** Each GitHub environment's `RESEND_API_KEY` secret
  goes to the node's `secrets/resend.key`; production writes to anyone, QA only to its own list
  ([`cloud-mail.md`](cloud-mail.md)). Without the secret the mail driver is `log`: no sign-up can
  confirm its email, and the operator confirms an account by hand (below).

## Deploying

Actions → **Cloud deploy** (`.github/workflows/cloud-deploy.yml`): a target (`qa` or `prod`) and
a release tag (blank: the latest release).

1. **resolve**: the release's images by digest, each verified with cosign, and the Postgres and
   Redis the compose file pins; the cloud rehearsal passed on the release's commit
   (`tools/release/cloud-guard.sh`). For `prod`: the run is main's (and the `prod` environment
   deploys from main only), main still has the run's deploy files, and a successful QA run of a
   commit of main recorded the same digests and the same files (this workflow, the guard,
   `deploy/cloud-relay` with its tests), or nothing deploys. A run started again asks all of it
   again.
2. **web** (QA): the cloud web app built from the release's source. Production gets QA's
   build, by the SHA-256 QA recorded.
3. **deploy**:
   - `ops/rollout.sh begin`: no release older than the node's, room on the disk (and some kept for
     going back), every image pulled by digest, a backup, and the node's present set saved. A
     failure leaves the node as it was.
   - the DNS records (a name with an AAAA or another record that sends clients elsewhere is
     refused before any is changed);
   - `setup.sh`, under the rollout's lock (idempotent: Docker from Ubuntu's packages, the `maktaba` user, the secrets it
     generates once, `cloud.toml` from its template, Docker's log caps, UFW, the backup timer,
     the unit);
   - the web bundle, unpacked beside the one being served;
   - `deploy.sh apply`: `maktaba-rollout apply` on the node (migrations in a throwaway container,
     the stack, its health and readiness watched for 45 s, then the web app switched in one
     rename). A changed `cloud.toml` or Caddyfile recreates the containers that read it.
4. **test**: `deploy/cloud-relay/tests/e2e.py` (below). Then `deploy.sh commit`, which also
   removes the images no container uses, and QA records what it passed.

### When a deploy fails

Anything that fails or is cancelled after `begin`, the tests included, ends in `deploy.sh rollback`.
`apply`, `commit` and `rollback` are systemd units of the node's own, started once and followed over
SSH: a cut session, a cancelled run or a lost runner does not stop them, and a session is picked up
again where it was. Every command names its deploy, and does nothing to another's.

**A lost runner.** A deploy has a lease of 20 minutes, renewed while the workflow works (each minute
during the tests). `maktaba-rollout-watch.timer` looks each minute: when the lease is over, or a
command was stopped part way (killed, out of memory), it restores the saved set. The node comes to
a known state, the previous release or held, with nobody there.

A rollback, on the node:

1. The migrations this deploy applied (its `apply`, or a role that started on its own) go back by
   their Down scripts (`maktaba-cloud migrate undo`, with the new release's image), newest first,
   every one judged before the first runs, the gravest refusal winning.
   - A Down script that would delete rows written since is not run: the schema is **kept**, and the
     previous release runs on it (a release's migrations only expand the schema, story 50.08).
   - A migration whose Up lost data (or is a contract), or a Down script that fails: the node is
     **held**.
2. The saved compose file, Caddyfile, `cloud.toml`, `.env`, secrets, scripts and web app come back,
   and the previous images with them, recreated and watched. A pass that could not (Docker was
   restarted under it) is done once more when Docker answers, before the node is held.

A **held** node has its relay, api and worker stopped, refuses deploys and does not start at boot;
`maktaba-rollout status` says why and where the saved set and the backups are. When Docker did not
answer, it says that not all of them could be stopped, and the node goes on trying each minute and at
boot. The stopped containers are removed (their last lines are in
`/var/lib/maktaba-cloud-deploy/last-logs`), so that Docker can't start one again. A dump is never put
back by itself. A person sets the node right, then `maktaba-rollout unhold`; a first release that was refused still starts
for nobody after it, unless a person has started it. A node that reboots
during a deploy restores first (the stack's unit asks before it starts, and never starts anything
while a command is at work), and a deploy that finds an earlier one cut off undoes it before it
begins. A failed first deploy has nothing to go back to: the stack stops, stays stopped at every boot
until a release is committed, and what its database holds is backed up before the next deploy.
A power cut can leave a container half removed, which Docker lists and compose then fails on: boot, a
restore and the next deploy take it out first.

**A database that does not come up.** `begin` starts the database to back it up, and refuses when it can't,
with nothing changed. On a node that never committed a release, a first start of Postgres that was cut or
failed leaves a folder Postgres does not finish by itself: `begin` makes it afresh when it can show that
nothing is in it (no backup was ever taken, and Postgres has no database but its own, or, when it can't
start, its folder holds only what a new cluster's does), and keeps a copy in
`/var/backups/maktaba-cloud`. When it can't show that, it says why and refuses. Then, on the node:

- nobody needs what is in it: `docker ps -aq --filter volume=maktaba-relay_pg_data | xargs -r docker rm -f`,
  then `docker volume rm maktaba-relay_pg_data`, and deploy again;
- otherwise look first at `docker logs` of the Postgres container, and keep a copy of the volume before
  anything else. The newest backup (`/var/backups/maktaba-cloud`) is what a new volume is filled from:
  start Postgres alone (`docker compose --env-file .env up -d --wait postgres` in `/opt/maktaba-relay`) on it
  and read the dump in (`docker compose --env-file .env exec -T postgres pg_restore -U maktaba -d maktaba
  --no-owner < db-….dump`).

What does not go back: the DNS records, the firewall, Docker's settings and the systemd units.
Each failure is rehearsed in CI (`cloud-rehearsal.yml`, the `single-node` jobs:
`deploy/cloud-relay/tests/rollout_rehearsal.py`; `reboot_rehearsal.py` on a virtual machine that is
really rebooted and whose power is really cut), and nightly the rehearsal itself is
(`cloud-rehearsal-mutations.yml`, which no deploy waits for): the node's scripts with one guarantee
taken out at a time, which the case that names it must notice.

One deploy per node at a time; one under way is never cancelled by another (of the ones waiting,
GitHub keeps the newest). The GitHub environments `qa` and `prod` hold the deploy jobs.

## Tests

`e2e.py` runs on the workflow's runner against the node just deployed. Full on QA, smoke on
production (no relay restart, no load):

| Check | What it proves |
|---|---|
| tls | the relay's apex, the api and the app answer HTTPS with a valid certificate; later a home's name (the wildcard) |
| health | `/healthz` and `/readyz` on the relay and the api, and how mails go (QA that sends with no recipients' list fails); the web app's page |
| firewall | from outside, of 1,040 TCP ports only 22, 80 and 443 answer; UFW is active and denies incoming; only Caddy publishes a port |
| backup | the timer is on; a backup taken now restores into a scratch database, every table's rows equal to those recorded in the dump's own snapshot, and its keys into a scratch folder, every file whole and the password good for the database |
| account | a new account is refused sign-in until confirmed; the operator tool confirms it and gives it Basic |
| broadcast-off | broadcasting is off: every public broadcast and moderation route answers 404, with and without an account; the providers and plans lists offer none; no home is a test publisher; `broadcast.enabled` is false; only the node's six services run (no broadcast edge) |
| home | the release's `maktaba-server` image on the runner, set up headlessly with a test video; its claim code minted by the account; online in the account's homes |
| licence | the home holds the cloud's licence for its owner's plan, signed with the key its release embeds, and serves the relay |
| link | the home's admin linked to the account (the Linked accounts flow, the web app's step by API), and the cloud lists the home as linked |
| relay-play | the home's own sign-in through its relay name, and the video played through the relay, its bytes equal to the file |
| plans | on Free the relay refuses the home (402 `relay_needs_plan`); on Basic it serves it again |
| cloud-play | sign-in through the cloud (an assertion, `POST /api/auth/cloud` on the home's relay name), then play again |
| restart | (QA) the relay restarted under a linked home: how long until it answers again, and it plays |
| load | (QA) players through the relay for a minute while the node's CPU, memory and network are sampled: `sizing.json` |

The run's artifact `e2e-<target>` holds `results.json`, `summary.md`, `sizing.json` and the logs.
Whatever failed, the run ends with `ended`: the test account deleted (the cloud's 30-day hold), its
home deleted from the cloud, its container and volumes removed from the runner, and no test
account of any run left active or with a home; a leftover fails the run.

## Operator actions

On the node, as root (the deploy's tests run them over SSH). The email is read from stdin and
nothing secret is printed:

```bash
echo 'someone@example.com' | /opt/maktaba-relay/ops/cloud-ops.py verify-email   # confirm the account's email
echo 'someone@example.com' | /opt/maktaba-relay/ops/cloud-ops.py set-plan pro    # free, basic or pro
echo 'someone@example.com' | /opt/maktaba-relay/ops/cloud-ops.py account         # id, plan, confirmed
echo 'someone@example.com' | /opt/maktaba-relay/ops/cloud-ops.py reset-link       # a password reset link, once, on a terminal only
/opt/maktaba-relay/ops/cloud-ops.py mail-status                                  # how mails go, and what became of them
echo 'someone@example.com' | /opt/maktaba-relay/ops/cloud-ops.py mail-test ar     # a test mail (docs/cloud-mail.md)
/opt/maktaba-relay/ops/cloud-ops.py end-test-accounts                            # the deploy tests' own (ci-e2e-*)
maktaba-rollout status                                                          # committed, pending or held
```

`reset-link` is for when no mail can arrive: it prints a link like a mailed one (an hour, once),
keeps only its SHA-256, and writes its audit row (`cli`, `auth.password_reset_link`) in the same
transaction. It prints on a terminal only, so over SSH it needs `ssh -t`, and never in a CI run.

`verify-email` changes the account and writes its audit row (`cli`, `auth.email_verified`) in
one transaction. `set-plan` goes through the operator console's API on the private listener,
as the node's own staff account (`cloud-ops@maktaba.tv`, `service_admin`), made on first use;
its password and TOTP secret stay in `/opt/maktaba-relay/ops-state/` (root, 0600), which no
container mounts.

## Backups

`maktaba-cloud-backup.timer`, nightly at 03:17 UTC, and before each deploy to a node that has a database: `pg_dump` (its copy equal
to the last byte and read to its end with `pg_restore` before it is kept) with every table's rows counted in the same
snapshot, and the node's keys (`.env`, `secrets/`) with each file's SHA-256, into
`/var/backups/maktaba-cloud`, 14 days kept. `ops/backup.sh --check` restores the newest one and
compares it with what was recorded, exactly.

**They stay on the node.** Losing the machine loses the cloud's accounts and homes since the
last copy taken elsewhere. Hetzner's own backups (20 % of the server's price) or a Storage Box
would keep a copy off the machine; neither is set up.

## Monitoring

The deploy's tests are the checks today. GitHub mails the owner when a run fails. The outside
probe (`cloud-probe.yml`, story 50.10) is built for the pair's witness and stays off. Nothing
watches the certificates or the nightly backup between deploys yet ([#1665](https://github.com/Hamza-Labs-Core/Maktaba/issues/1665)).

## Sizing

Measured on QA (1 vCPU, 2 GB) on 2026-11-24, by the `load` check's players on one CI runner, each
reading an 8 Mbit/s file as fast as the relay gives it:

| Players | Relayed | CPU of one vCPU | Memory used, the whole node | Failed streams |
|---|---|---|---|---|
| 8 (4 MB a session) | 71 Mbit/s (94 Mbit/s out of the node) | 24 % | 751 MB | 0 |
| 24 (whole files) | 238 Mbit/s (249 in, 244 out) | 45 % | 748 MB | 0 |

About 19 % of one vCPU per 100 Mbit/s relayed; the runner, not the node, was the limit. The
smallest server is therefore 1 vCPU and 2 GB: 238 Mbit/s is about 29 such streams at once, with
half the CPU left. Traffic counts twice, in from the home and out to the player, so one 8 Mbit/s
stream watched for an hour is 3.6 GB in and 3.6 GB out. Each QA run's `sizing.json` has its own
figures.
