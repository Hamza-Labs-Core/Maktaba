# The cloud rehearsal

> Requirement: failover, deploy, rollback and restore paths are proven in CI before they
> reach a machine with paying users on it, the same rule as the update rehearsal
> ([docs/update-rehearsal.md](update-rehearsal.md)). Epic 50, story 50.06.

The cloud rehearsal (`.github/workflows/cloud-rehearsal.yml`, its verdict job
**cloud-rehearsal**) runs Maktaba Cloud's Phase 1 cluster in Docker on a CI runner and breaks
it on purpose, one way per scenario. Each scenario ends with remote play working, measures
the downtime and the failed requests, and fails above its budget. It keeps every container's
log and its JSON result as an artifact, whether it passed or not.

## When it runs

- Every pull request that touches `cloud/**`, `shared/log/go/**`, `deploy/cloud-ha/**`,
  `deploy/cloud-relay/**`, `deploy/caddy/**`, `tools/rehearsal-cloud/**` or the deploy guard.
- Every push to main (and `release/*`), and nightly at 05:47 UTC.
- **Before every cloud deploy.** `relay.yml`'s deploy job first runs
  `tools/release/cloud-guard.sh`: it waits for the rehearsal's run on the exact commit being
  deployed and refuses the deploy unless it passed, naming the failed scenarios.
- The tag guard learns the rehearsal once the two-node scenarios are live (story 50.07).

## What it reproduces

| On production | In the rehearsal |
|---|---|
| nodes A and B, the relay as blue and green processes | `relay-blue` and `relay-green`: this commit's binary (`tools/rehearsal-cloud/Dockerfile`), each with its relay id and peer address |
| Caddy in front, the private network | `edge` (Caddy, plain HTTP, the processes in order, a process out of rotation while its `/readyz` fails); a Docker network, `10.50.0.0/24` |
| Postgres | `postgres` (Postgres 16) for C01, C08; for C02–C04 the pair itself: node A, node B and the witness W, each a container of `deploy/cloud-ha`'s files (`Dockerfile.node`: Postgres 16, Patroni 4.1, etcd 3.6 with TLS, Caddy, the relay while leader), and `front`, the internet's side of the Floating IP |
| homes | simulated homes (`cloud/internal/relay/simhome`): real cloudlink agents, each a real tunnel serving a fake home API, run by the test |
| apps and the outside probe | the stream generator and the probe (`cloud/rehearsal/streamgen`): clients playing a segment loop through `<slug>.relay.test`, and a probe every 250 ms |
| the Hetzner Floating IP API | `cloud/rehearsal/fakehcloud` (used from 50.07): moves a Docker network alias |
| Let's Encrypt, ZeroSSL and Cloudflare's DNS API | an unreachable CA, then Pebble (Let's Encrypt's test CA), and `fakeacmedns`: the acme-dns API Caddy's `acmedns` module calls, and the DNS server Pebble resolves the challenge through |

Nothing outside the runner is contacted except the image registries: no Hetzner,
Cloudflare, Let's Encrypt or payment provider host. Homes are seeded with cheap argon2id
hashes (`cloud/rehearsal/seed`); the relay's check is the real one.

## The scenarios

| # | Scenario | Live since | Budget |
|---|---|---|---|
| C01 | `drain-1k`: 1,000 homes and 100 streams on blue; blue gets SIGTERM | 50.06 | every home on green ≤ 15 s, 0 failed requests, blue exits ≤ 150 s |
| C02 | `failover-kill-leader`: the pair (`compose-ha.yml`), 300 homes, 50 streams, a writer committing every 100 ms; the leader killed | 50.07 | the other node leads ≤ 45 s; remote play back ≤ 3 min; no acknowledged commit lost |
| C03 | `failover-network-partition`: the leader cut from the other node and the witness, still running | 50.07 | it is read-only and its relay stopped ≤ 40 s; never two writable primaries (checked every 250 ms); no commit lost; the IP on the new leader; the old one back as a replica |
| C04 | `switchover`: `deploy/cloud-ha/bin/switchover` under traffic | 50.07 | remote play pauses ≤ 15 s; no commit lost |
| C05 | `deploy-blue-green`: `deploy/cloud-ha/bin/bluegreen` deploys a new release (`maktaba-cloud:rehearsal-new`) beside blue under 1,000 homes and 100 streams | 50.08 | 0 failed requests; every home on green; no tunnel force-closed; `/version` says new |
| C06 | `deploy-bad-release-rollback`: a release built with `-tags rehearsalbad` (its readiness fails 20 s after it starts) | 50.08 | the deploy exits non-zero before blue is touched; green removed; 0 failed requests |
| C07 | `migration-n-minus-1` | 50.08 | — |
| C08 | `db-blip`: 300 homes streaming; the database container killed for 15 s | 50.06 | 0 failed requests, metering within 1 % of what the clients received |
| C09 | `restore-pitr`: the pair archives WAL and backs up (`deploy/cloud-ha/bin/backup`) to two S3 repositories (SeaweedFS with TLS, standing in for Object Storage and R2); rows written for 80 s with repo1 down for 20 s of it; both nodes destroyed; `bin/restore` on a fresh machine to a time T, from repo1 and from repo2 | 50.09 | exactly the rows committed up to T from both; a wrong passphrase reads nothing; the bucket holds ciphertext; restore time against the 4 h RTO (7 s locally) |
| C10 | `cert-on-standby`: two fresh node edges (this repository's Caddy), Pebble and a DNS-01 provider (`cloud/rehearsal/fakeacmedns`); the first CA unreachable | 50.02 | each node holds its own wildcard and apex certificate within 2 min, from the second CA; a home's name gets the wildcard; no per-home certificate |
| C11 | `status-feed`: the outside probe (`deploy/cloud-ha/monitoring/probe.sh`) against a canary home through the edge; its rounds into a local repository standing in for GitHub (`status/publish-data.sh`); every relay process killed, then started | 50.11 | `summary.json` says the relay is in outage within 2 probe rounds, and operational again after 2 passing rounds; the outage reached the stand-in |
| C12 | `load-small`: 1,000 homes holding their tunnels and 50 players at 5 Mbit/s through the one-node cluster, the relay's CPU time read from its cgroups' counters by round, its memory from its Go live heap (`maktaba_go_heap_live_bytes` on its `/metrics`, sampled every 5 s) | 50.14 | runs twice on one runner, with the base commit's relay and this commit's (three one-minute rounds each), and writes each one's median CPU-seconds per Mbit relayed and live heap per tunnel, each the median of its rounds (`load-small-baseline.json`); the job fails when this commit's is more than 20 % worse than the base's on either |
| C13 | `direct-remote` (spec C14): two homes behind a router speaking NAT-PMP (`cloud/rehearsal/fakegateway`, `compose-direct.yml`), the cloud's relay, worker and name server, and two Pebbles (Let's Encrypt, and ZeroSSL as the overflow CA) on "the internet" (198.51.100.0/24, Docker's masquerade off on it); a weekly budget of one | 50.15 | each home maps its port and gets a certificate by DNS-01, validated against the cloud's own name server; the second from the overflow CA; no private key in the cloud's database or logs; the outside check passes; the name resolves to the router and an outside client gets `/api/health` and a 5 MB file over HTTPS straight from each home, none of it through the relay |
| C14 | `origin-only-via-edge` (50.31 T06, T07): the pair with production's Caddyfile and UFW (`compose-origin.yml`, `node/origin.sh`, `node/firewall.sh`), the api on the loopback, a stand-in connector (socat) on the leader only, a stand-in edge writing `CF-Connecting-IP`, and an attacker on the Floating IP's network (Docker's masquerade off on it); then the leader killed and started again | 50.31 | the Floating IP answers 80 and 443 only; 443 refuses SNI `api.maktaba.tv`, plain HTTP for it gets 421; a forged `CF-Connecting-IP` is replaced by the edge (the api logs the attacker's address) and never reaches the relay; the connector moves with the lead, the api is back through the edge within 3 min, and the old leader comes back as a replica without a connector |
| C15 | `origin-aop` (50.31 T08): the same pair with `origin_mode = "aop"`, a test AOP CA and the client certificate it signed; the test "Cloudflare ranges" are the edge's address | 50.31 | 200 with the certificate from the ranges; the TLS handshake refused without it (Caddy's `require_and_verify`, before any HTTP); 403 with the certificate from outside the ranges; the api logs the edge's `CF-Connecting-IP` |
| C16 | `report-symbolicated` (53.16 T07): the one-node cluster with this commit's api role (profile `api`, `api-rehearsal.toml`) and a symbols token made in the job; the canary home online on the relay sends an Apple, an Android and an Android native problem report through its agent's reports route (`cloudlink.ReportsHandler`); the symbol tables are made in the job (the Apple fixture's dSYM, `cloud/internal/symbols/testdata/mapping.txt`, the unstripped `native.so` fixture) and uploaded | 53.16 | before the upload the service admin reads raw frames and each missing table (the native library named with its build id); a wrong token is refused (401); after it, the Apple crash reads as function, file and line from its dSYM, the Android one from its R8 mapping and the native one from its library's table (its libc frame as it came) |
| C17 | `api-pool` (50.17 T06): three api nodes, each its api and its worker (`compose-pool.yml`), behind HAProxy standing in for LB11 (`haproxy-pool.cfg`: `GET /readyz` every 5 s, out after 2 failures, least connections, no retries), two relays with their home caches; the worker holding the leader lock killed; 90 malformed sign-ins round robin over the nodes; a plan written to a home; `deploy/cloud-ha/bin/api-rollout` puts `maktaba-cloud:rehearsal-new` on the three nodes one at a time (`pool.sh`) under load through the balancer | 50.17 | exactly one session holds the lock, and another node holds it ≤ 15 s after the kill; the first `429` right after the shared burst of 30, not after 90; both relays drop the home ≤ 1 s after the change; 0 failed requests during the rollout; each stopping node out of the balancer ≤ 10 s after its SIGTERM; every node says new |
| C18 | `relay-pool-kill-one` (50.16 T08): three relays (`compose-relay-pool.yml`) behind a round-robin edge, 1,000 homes each holding a primary and a standby tunnel, 100 streams through the edge, `relay-dns` against a fake Cloudflare (`cloud/rehearsal/fakecloudflare`); r3 killed | 50.16 | no request fails 10 s after the kill (p99 of each home's last failure ≤ 10 s); every home holds a new standby ≤ 60 s; r3's address leaves the round robin ≤ 30 s, the others stay; the three shares before the kill within 4 σ (±18 % of a third); the survivors hold their shares within 4 σ of the binomial count (±13 % of a half); a request counts from when it failed, so a stream cut by the kill counts after it |
| C19 | `rebalance-add-relay` (50.16 T09): two relays and 1,000 homes under streams; r3 started | 50.16 | only homes whose primary changed move, each to r3; no relay drains more than 20 homes a second; 0 failed requests; the outside probe never down; final shares within 4 σ (±18 % of a third) |
| C20 | `broadcast-plans` (50.20 T10): the one-node cluster with this commit's api role in the plans build (profile `api`, `compose-broadcast.yml`, `broadcast-plans.toml`: broadcasting on and enforced, the rehearsal's edge key); the scenario plays the edge (`fakeEdge`), signing its usage posts, for a Broadcaster home that publishes once its owner accepts the agreement | 50.20 | the modes come in order (full; lowest at the allowance; wait at twice it; off when the owner is suspended); a new viewer gets the mode of its moment and nobody playing stops until off; a 2-hour API outage leaves 24 windows queued, all taken after with the month's total exact; a window posted twice counts once |
| C21 | `db-nodes-failover` (50.18 T03, T05): three database nodes (`compose-db.yml`: Patroni, etcd and PgBouncer on each), the database's Floating IP (a second stand-in Hetzner moving `db.internal`), and the app tier (relay, two workers, an api) on PgBouncer's transaction pool and `cloud_session`; the synchronous replica stopped for maintenance, then the primary killed under a writer and remote play; a full backup from the new leader and a point-in-time restore from the second repository | 50.18 | the other replica synchronous with writes held ≤ 15 s; the synchronous replica leads ≤ 45 s and holds the Floating IP ≤ 15 s after; 0 acknowledged commits lost; relay and api back ≤ 2 min; the restore exact to the row |
| C22 | `db-nodes-migrate` (50.18 T04): the Phase 1 pair serving 100 homes with a writer, the three database nodes beside it (`JOIN=later`); `deploy/cloud-ha/bin/migrate-db-nodes all --announced` (join, the pair's cloud off the leader, the database's Floating IP, repoint with a blue/green of the relay, switchover to db-1, db-3, etcd onto the three, retire, backup) | 50.18 | writes held ≤ 15 s; remote play paused ≤ 15 s; 0 acknowledged commits lost; db-1 leads with one synchronous replica of two streaming; the pair gone from Patroni and etcd; a full backup from db-1 |

A scenario a later story builds is listed in `tools/rehearsal-cloud/expected-failures.txt`
with that story: it reports itself as not built and is not counted. The story that builds it
removes its line in the same pull request.

A timing failure is fixed at the root, never re-run to green.

## Run one scenario locally

Never against a live machine: everything a scenario touches is in its own compose project
(`mr-<scenario>`), on `127.0.0.1` ports.

```bash
docker build -f tools/rehearsal-cloud/Dockerfile -t maktaba-cloud:rehearsal .
cd cloud
REHEARSAL_OUT=/tmp/rehearsal go test -tags cloudrehearsal -run 'TestScenario/drain-1k$' -timeout 30m -v ./rehearsal/
```

`REHEARSAL_HOMES` and `REHEARSAL_STREAMS` scale a scenario down on a small machine;
`REHEARSAL_KEEP=1` leaves the cluster up afterwards (`docker compose -p mr-drain-1k down -v`).

## What it found while it was being built

The first runs, before any of 50.03–50.05 merged, found five bugs the unit and in-process
tests had passed: tunnel seqs that ordered tunnels wrongly across processes, a request lost
when a drained tunnel closed, a 15 s budget that a fixed reconnect window could not meet, and
two ways a request or the readiness probe waited on a database that had vanished. Each is
fixed in its story's pull request (#705, #714).

## The broadcast rehearsal

Story 50.29 adds broadcasting's failure paths, and the one number that must hold: **one upload
from the home per segment per rung, whatever the number of viewers**. It has its own workflow,
`.github/workflows/broadcast-rehearsal.yml`, and its own stack, `tools/rehearsal-cloud/broadcast/`.

- **The edge:** this commit's `web/packages/broadcast-edge` in Miniflare (`Dockerfile.edge`,
  `edge/server.mjs`): five simulated locations, each a copy of the Worker with its own cache,
  sharing the Durable Objects and R2. A router picks the location from `X-Rehearsal-Location`.
  `edge/entry.ts` wraps the real Worker with counters (R2 gets by key, ingest PUTs by rung,
  Durable Object stubs, Analytics Engine points) at `/__rehearsal/counters`, and a fast clock at
  `/__rehearsal/clock`. A control port (`:8788`) redeploys every location with new variables
  (break-glass), and a second version of the Workers serves a gradual rollout. Two fakes keep
  every call inside the container: Turnstile's siteverify (the edge's `TURNSTILE_API`; a token
  `pass-…` is good once) and the cloud's API (every Worker's outbound fetch): it takes the edge's
  signed usage posts as the cloud does, and `/__rehearsal/cloud/down`, `/up` and `/usage` stop it,
  start it and list what it took.
- **The home:** the streaming service's broadcaster (`streaming/internal/broadcast`) on a test
  pattern with a burned clock and frame counter, encoded in software
  (`streaming/cmd/rehearsal-broadcast-home`, `Dockerfile.home`). It counts every PUT at its HTTP
  transport (the proxy counter), beside the uploader's own report. `POST /uplink {"every": N,
  "body_ms": D}` congests its upload: every Nth segment PUT's body takes D ms to send.
- **The cloud's part:** the ingest tokens, the revocation lists, the plan pushes, the access
  posts and the invites are signed in the test with the cloud's TEST key, byte for byte as the
  cloud signs them (`tools/broadcast-viewers/tokens.go`, tested against
  `cloud/internal/broadcast/testdata/vectors.json` and `invite_vectors.json`), and viewer tokens
  with the rehearsal's viewer key. The edge signs its usage posts with a rehearsal key, checked as
  the cloud checks it. None of these keys is used in production.
- **The monitoring (`broadcast-observe` only, `compose.observe.yml`):** the witness's Prometheus,
  Alertmanager and Pushgateway, at the images of `deploy/cloud-ha/monitoring/compose.yml`, with
  the production alert rules (`alerts.yml`, mounted unchanged) and routes (`alertmanager.yml`,
  rendered with stand-in receivers: the phone receiver pages the monitor's ntfy stand-in). The
  cloud's `maktaba_broadcast_*` series come from `cloud/cmd/rehearsal-broadcast-monitor`
  (`Dockerfile.monitor`): the cloud's own fold, cost model and series writer
  (`broadcast.FoldMinute`, `CostEstimate`, `WriteSeries`) over the edge's Analytics Engine points
  (`/__rehearsal/ae`), with the worker's timing (the minute before last). On the runner, every
  60 s: `broadcast-probe.sh` plays the canary as the witness, `status-export` writes the witness's
  view, `probe.sh` plays it as GitHub's runners and records the status page's round, and
  `status/update.mjs` folds it in.
- **Viewers:** `tools/broadcast-viewers`, a Go load generator. Each player fetches the master
  and its rung's playlist every target duration, fetches each new segment, sends heartbeats, and
  records the slate and `off_air`.

| Scenario | Story | Pass when |
|---|---|---|
| `broadcast-fanout` | 50.21 | 1 → 10 → 100 viewers (1,000 nightly) across 5 locations: home PUTs = segments × rungs at every step, exactly; R2 reads ≤ 2 per object per location; 0 viewer errors; copy-rung fetch p95 ≤ 1.5 s and ≤ 6.5 s after the home's PUT; at the end, the uploader's count, the proxy's and the edge's agree on every rung |
| `broadcast-fanout-planted` | 50.29 T03 | the fan-out with every location's cache planted off (`REHEARSAL_PLANT=per-viewer-fetch`) **fails**, naming `r2_reads`, while the home's upload still holds |
| `broadcast-home` | 50.22 | the encoder killed for 30 s: the slate ≤ 15 s; live again after the restart; every player saw both; no player error |
| `broadcast-home-offline-24h` | 50.21 | the home stopped: the slate, still the slate at 23 h 50 on the fast clock, `off_air` after 24 h |
| `broadcast-kill` | 50.25 | 200 viewers: a revocation list stops every one ≤ 60 s and the home stops uploading; a new list lifts it; break-glass (`BROADCAST_OFF_CHANNELS`) does the same with the cloud out of the loop |
| `broadcast-edge-deploy` | 50.21 | a new edge version rolled out 10 → 50 → 100 % mid-stream, then a redeploy: no viewer error, every viewer live, the channel's Durable Object kept its playlist |
| `broadcast-plans` | 50.20 | plan pushes full → lowest → wait → full (pay-as-you-go): each new viewer gets the plan's mode in that order (a lowest master lists one rung, wait admits no one); every playing session keeps its mode, heartbeats and segments; an older plan is refused |
| `broadcast-publishable` | 50.23 | the home's guide from mixed sources holds only publishable items and the edge serves it as sent, attribution kept; a withdrawn permission on the item airing now: the slate ≤ 15 s; the top-up's guide replaces it and every viewer is live again |
| `broadcast-access` | 50.26 | an invite's uses (a device again reuses its pass), a short code and its 10 tries a minute, Turnstile good once, open sessions per network, viewers per channel, daily minutes, a revoked pass and invite; the limits refuse new sessions only |
| `broadcast-takedown` | 50.24 | a takedown of the item airing now (a programme entry): every viewer has the slate ≤ 60 s while the home uploads on; the counter-notice restores it; a second strike's slate ends with its slot; the third strike takes every viewer off air ≤ 60 s, stops the home and lets no one new in |
| `broadcast-hetzner-down` | 14.8 | nightly: the cloud down for 15 minutes mid-broadcast: no viewer error, the home uploads on, the edge's usage posts are refused and kept; once it is back every window arrives once, its viewer-minutes exactly the heartbeats the edge counted, every post signed with the edge's key |
| `broadcast-observe` | 50.28 | the canary's ingest events carry glass-to-glass; while the canary plays, the status page's Broadcasting is operational, the probe records glass-to-glass from both networks at ≤ 20 s and the canary and glass-to-glass alerts are quiet; each alert fires on the production rules and Alertmanager routes it by the production routes: `broadcast_cost_80pct` (email), `broadcast_glass_to_glass_high` when the burned clock reads 45 s late (email), `broadcast_revocation_list_stale` after 10 minutes without a list (phone), `broadcast_ingest_lag_high` with a congested uplink (every 4th segment's body takes 4.5 s; phone); the encoder killed: ingest events stop, the slate, `broadcast_canary_down` pages the phone ≤ 90 s after the second failed probe round and Broadcasting goes to outage; a revoked channel's viewers are written as blocked, and the stale alert clears once the edge takes a list |

A scenario is listed in `tools/rehearsal-cloud/broadcast/expected-failures.txt` while it has no
body, or while some of its checks need what this stack lacks, and is reported XFAIL (none is
listed now). A listed scenario that passes fails the run (XPASS) until its line goes, and an
unlisted scenario without a body fails.
`tools/broadcast-viewers/rehearsal/catalog.go` is the list of scenarios; the CI matrix is built
from it.

A pull request runs the short set when it touches the edge, the broadcast packages of the
cloud, the streaming service or the API, or the rehearsal itself. The nightly run (and a manual
run with `set: nightly`) runs the full set. The verdict job writes one report (each scenario's
verdict and timings, the fan-out table, the latency percentiles) to the run's summary and to the
`broadcast-report` artifact. Every scenario's stack log is kept in its own artifact.

Under the fan-out table, a second table says where a slow fetch's time went at each step: the
copy rung's time to first byte, the edge Worker's own answer time (the edge counts each segment
answer in buckets, `/__rehearsal/counters`), how late the viewers' playlist reloads started (the
load generator's own delay), the CPU the edge, the home and the viewers used (in cores, from the
containers' cgroups and the test process), and the runner's busy and stolen share. The edge,
every location and the Durable Objects share one workerd thread in the rehearsal, so anything
that holds a Durable Object's thread (a heartbeat that rewrote a channel's whole state did, at
1,000 viewers) shows as slow segment fetches there.

A release tag needs the last nightly run on the tagged commit's broadcast code green
([updates.md](updates.md#how-a-release-is-made), "The broadcast rehearsal gates the tag too"). A
nightly-set run is named `Broadcast rehearsal nightly of <sha>` so the release session can find
its commit.

### Run one broadcast scenario locally

```bash
docker build -f tools/rehearsal-cloud/broadcast/Dockerfile.edge -t maktaba-broadcast-edge:rehearsal .
docker build -f tools/rehearsal-cloud/broadcast/Dockerfile.home -t maktaba-broadcast-home:rehearsal .
# broadcast-observe only: the cloud's monitor (it also needs bash, curl, jq, python3 and node)
docker build -f tools/rehearsal-cloud/broadcast/Dockerfile.monitor -t maktaba-broadcast-monitor:rehearsal .
cd tools/broadcast-viewers
REHEARSAL_OUT=/tmp/broadcast go test -tags broadcastrehearsal -run 'TestBroadcast/broadcast-fanout$' -timeout 40m -v ./rehearsal/
```

`BROADCAST_VIEWERS` and `BROADCAST_STEP_SECONDS` scale the fan-out (`BROADCAST_KILL_VIEWERS`,
`BROADCAST_DEPLOY_VIEWERS`, `BROADCAST_TAKEDOWN_VIEWERS`, `BROADCAST_PUBLISHABLE_VIEWERS`,
`BROADCAST_HETZNER_VIEWERS` the others); `BROADCAST_PLAN_STEP_SECONDS` is each plan's spell and
`BROADCAST_OUTAGE_SECONDS` the cloud's outage (900). `REHEARSAL_KEEP=1` leaves the stack up
(`docker compose -p bc-fanout down -v`). Rebuild the images after changing the edge or the home:
the compose file runs the tagged images and never builds them.

### What it found while it was being built

- Miniflare 5 ignores the per-Worker `cache: false` option, so the first planted run passed. The
  plant now lives in `edge/entry.ts`, where the rehearsal controls it.
- The broadcaster's own report comes every 10 s, so it lags the proxy's count while the home
  uploads. The three counts are compared after the test pattern stops.
- A day on the fast clock expires a viewer token minted on real time. The scenarios mint tokens
  on the edge's clock.
- The edge refuses a home clock more than 60 s off its own, and the home does not follow the
  edge's clock, so the fast clock can't run under a live home: the Hetzner outage runs in real
  time.
- The canary's ingest events carried glass-to-glass 0 for most segments: the home's
  PROGRAM-DATE-TIME (ffmpeg's `program_date_time`) was not earlier than the moment the edge
  received the segment, so the edge's difference clamped to 0. Fixed in #1382 (the home sends
  each segment's start time); `broadcast-observe` checks it.
