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
