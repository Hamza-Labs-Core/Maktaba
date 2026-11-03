# Story 50.06 — The cloud rehearsal in CI

> Epic 50 · Phase 1 · Depends on: 50.01 · Needed by: 50.07, 50.14

## Why

The rule since 2026-10-28: update, rollback, restore and migration paths are **proven
in CI first** (`docs/update-rehearsal.md`). The same goes for the cloud.
Failover, deploys and restores are exactly the paths that run rarely and break silently; the
first time a Patroni failover runs must not be on the VPS with paying users on it.

This story builds the **cloud rehearsal**: the whole Phase 1 cluster in Docker on a CI runner,
with no real Hetzner, Cloudflare or Let's Encrypt, broken on purpose in each way it can break.
Each scenario ends with remote play working, measures the downtime, and fails above its budget.
Its verdict gates every cloud deploy and every tag.

## Description

### 1. What runs

`.github/workflows/cloud-rehearsal.yml` (new), verdict job **`cloud-rehearsal`**. One Docker
Compose project per scenario (`tools/rehearsal-cloud/compose.yml`), on one runner:

| On production | In the rehearsal |
|---|---|
| node A, node B (Caddy, `maktaba-cloud` blue/green, Postgres 16 + Patroni, etcd, Redis) | two containers built from the same `deploy/cloud-ha/` files, on a Docker network standing in for the private network |
| witness W (etcd, checks) | a third etcd container + the witness scripts |
| Hetzner Floating IP | **fake Hetzner API** (`tools/rehearsal-cloud/fakehcloud`, Go): implements `POST /v1/floating_ips/{id}/actions/assign` and `GET /v1/floating_ips/{id}`; "moving the IP" moves a Docker network alias (`relay.test`) to the assigned container. Configurable delay (default 5 s, a scenario uses 60 s to match the worst report) |
| Cloudflare DNS + Let's Encrypt | **Pebble** (Let's Encrypt's test CA) + **acme-dns** as the DNS-01 provider; names under `relay.test` |
| Object Storage | **MinIO** |
| homes | **simhome** (`cloud/internal/relay/simhome`, 50.03): N simulated homes, each a real cloudlink tunnel serving a fake home API |
| apps | **stream generator** (`tools/rehearsal-cloud/streamgen`, Go): M clients, each playing an HLS-like loop (a 2 MB segment every 4 s) through `<slug>.relay.test`, logging every failed or late request |
| the probe | a poller hitting `canary.relay.test/api/health` and `api.relay.test/readyz` every 250 ms |

Images are this commit's `maktaba-cloud` and `maktaba-caddy`, plus the **previous published
release** of `maktaba-cloud` for the scenarios that need N−1 (pulled from GHCR by tag).

### 2. Scenarios

Downtime = the longest stretch the probe failed. "Failed requests" = stream generator requests
that errored or took longer than 10 s.

| # | Scenario | What it does | Asserts | Budget |
|---|---|---|---|---|
| C01 | `drain-1k` | 1,000 homes, 100 streams; `SIGTERM` one relay process | every home moved ≤ 15 s; 0 failed requests; old process exits ≤ 150 s | 0 s |
| C02 | `failover-kill-leader` | 300 homes, 50 streams; `docker kill` node A | B is leader; fake IP on B; no committed row lost (a writer inserts a row every 100 ms and records acknowledged ids); homes back p99 ≤ 45 s; held requests counted | **3 min** |
| C03 | `failover-network-partition` | cut A from B and W (Docker network disconnect), keep A alive | A demotes itself (Postgres read-only, cloud processes stopped) within 30 s; B leads; **never two leaders** (every 250 ms the test asks both for `pg_is_in_recovery()` and the fake API for the IP holder; any instant with two writable primaries fails) | 3 min |
| C04 | `switchover` | `patronictl switchover` with traffic | the planned path in 50.07's runbook works; impact ≤ 15 s | 15 s |
| C05 | `deploy-blue-green` | deploy this commit over the previous release with 1,000 homes and 100 streams, by `deploy/cloud-ha/deploy.sh` (50.08) | 0 failed requests; 0 dropped tunnels without a `DRAIN`; the new version answers `/version` | 0 s |
| C06 | `deploy-bad-release-rollback` | deploy an image built with `-tags rehearsalbad` (fails readiness after 20 s) | the deploy stops, green is removed, blue keeps serving; 0 failed requests; the deploy exits non-zero | 0 s |
| C07 | `migration-n-minus-1` | migrate to this commit's schema, then run the **previous release's** binary on it for its test suite subset (`go test -tags rehearsal ./internal/...` against the DB) | the old binary works on the new schema (expand/contract rule, 50.08) | — |
| C08 | `db-blip` | 300 homes streaming; Patroni switchover only (cloud processes stay) | 0 failed requests for known homes; metering totals equal what the stream generator counted (± 1 %) | 0 s |
| C09 | `restore-pitr` | write rows for 3 min with pgBackRest archiving to two MinIO buckets (standing in for Object Storage and R2); destroy both nodes' data; restore to a timestamp 30 s before the end on a fresh node by `deploy/cloud-ha/restore.sh` (50.09) | every row before the target time is there, none after; restore time recorded against the 4 h RTO | 4 h (expected minutes) |
| C10 | `cert-on-standby` | fresh cluster | both nodes hold the wildcard before any failover; Pebble saw no per-home certificate (50.02) | — |

Later stories add their own scenarios to the same harness, under these names:

| # | Scenario | Added by |
|---|---|---|
| C11 | `status-feed` | 50.11: the prober's `summary.json` flips to outage and back within its budget |
| C12 | `runbook-switchover`, `runbook-forced-failover`, `runbook-dns-swap` | 50.12: each runbook's script runs as written |
| C13 | `apps-cloud-down` | 50.13: client libraries with the cloud unreachable; local play works, cached direct entry is used |
| C14 | `direct-remote` | 50.15: UPnP mapping on a fake gateway, outside check, IP-encoded name answers on each configured direct-name domain; overflow CA used when the weekly budget is spent |
| C15 | `relay-pool-kill-one`, `rebalance-add-relay` | 50.16: three relays; one killed, one added |
| C16 | `logs-survive-node` | Epic 53.17: during C02, the killed node's last `warn`+ lines (≤ 1 s before the kill) are on W and found by the cloud log search; and a 53.16 problem report sent through the canary home arrives |
| C17 | `relay-plans` | 50.19: a home stack behind the relay and a fake payment provider; Free refused with the tunnel kept, Basic capped and transcoded with a third stream refused, Pro past its allowance falls back to capped quality; no stream stops; meter events sent once |

Each scenario writes a JSON result (`downtime_ms`, `failed_requests`, `reconnect_p50/p95/p99`,
`budget`), shown in the run summary, and keeps every container's log, Patroni's history and the
stream generator's request log as an artifact **whether it passes or not** (the project rule:
tests keep their logs).

### 3. When it runs, and what it gates

- Every PR that touches `cloud/**`, `deploy/cloud-ha/**`, `deploy/cloud-relay/**`,
  `deploy/caddy/**` or `tools/rehearsal-cloud/**`, through CI's "run what changed" paths
  (trail 2026-11-02).
- Every push to `main`, and nightly (05:47 UTC).
- **Before any cloud deploy:** `relay.yml`'s `deploy` job first runs
  `tools/release/cloud-guard.sh` (new, modelled on `tools/release/tag-guard.sh`): it waits for
  `cloud-rehearsal` on the exact sha and refuses the deploy unless it passed.
- **Before any tag:** `tools/release/tag-guard.sh` learns a third workflow
  (`CLOUD_REHEARSAL_WORKFLOW=cloud-rehearsal.yml`), required only when the tag's diff from the
  last release touches the paths above.
- `tools/rehearsal-cloud/expected-failures.txt` works like the update rehearsal's: a scenario
  whose fix isn't merged yet is listed with its PR and reported but not counted.

### 4. Runner size

The scenarios with 1,000 homes need about 4 vCPU and 8 GB; GitHub's standard Linux runner
(4 vCPU, 16 GB) fits. Scenarios run as separate jobs in parallel (matrix), each ≤ 20 min; C09
≤ 30 min.

## Acceptance criteria

- **Given** a PR that touches `cloud/internal/relay/`,
  **when** CI runs,
  **then** all ten scenarios run and `cloud-rehearsal` reports pass or fail with each scenario's
  downtime against its budget.
- **Given** a change that makes Patroni promote B while A still accepts writes,
  **when** C03 runs,
  **then** it fails and names the instant with two writable primaries.
- **Given** a scenario fails,
  **when** the run ends,
  **then** the artifact holds every container's log and the scenario's JSON result.
- **Given** `cloud-rehearsal` failed on sha S,
  **when** `relay.yml`'s deploy runs for S,
  **then** `cloud-guard.sh` refuses it with the failed scenarios' names.
- **Given** a tag whose diff touches `cloud/**`,
  **when** the tag guard runs,
  **then** it requires `cloud-rehearsal` passed on that sha.
- **Given** the rehearsal runs,
  **when** it makes any network call,
  **then** the only hosts outside the runner are GHCR (images) and the Go/Docker caches; no
  Hetzner, Cloudflare, Let's Encrypt or payment provider host is contacted (the job sets
  `HCLOUD_ENDPOINT` to the fake, and a test asserts the compose networks have no route to those
  hosts' names).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `fakehcloud` | assign to B | `GET` shows B; alias moved; delay honoured |
| T02 | unit | `streamgen` | a server that answers 500 once | counted as a failed request |
| T03 | unit (shell) | `cloud-guard-test.sh` with a fake GitHub API | rehearsal failed / passed / running | refuse / allow / wait |
| T04 | unit (shell) | `tag-guard-test.sh` | tag touching `cloud/` without rehearsal | refused |
| T05 | CI | the workflow itself, on its own PR | all scenarios | green, results in the summary |

## Edge cases

- **Flaky timings on shared runners.** Budgets are checked on the scenario's own measurements,
  with generous margins (C02's 3 min against an expected ~90 s). A timing failure is fixed at
  the root, never re-run to green (project rule).
- **The previous release doesn't exist yet** (first cloud release): C05–C07 use the merge base
  of `main` built in the job as "previous".
- **Image pulls rate-limited.** Images come from GHCR with the workflow token.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `.github/workflows/cloud-rehearsal.yml` (new) | Matrix of scenarios; verdict job `cloud-rehearsal`; artifacts. |
| `tools/rehearsal-cloud/compose.yml` (new) | The cluster: A, B, etcd W, fakehcloud, Pebble, acme-dns, MinIO, simhome, streamgen, probe. |
| `tools/rehearsal-cloud/fakehcloud/` (new, Go) | Fake Floating IP API. |
| `tools/rehearsal-cloud/streamgen/` (new, Go) | Stream generator and probe. |
| `tools/rehearsal-cloud/scenarios_test.go` (new) | C01–C10 as Go tests (`-tags cloudrehearsal`), driving Docker through the compose CLI. |
| `tools/rehearsal-cloud/expected-failures.txt` (new) | Empty. |
| `tools/release/cloud-guard.sh`, `tools/release/cloud-guard-test.sh` (new) | Deploy guard. |
| `tools/release/tag-guard.sh`, `tools/release/tag-guard-test.sh` | Require the cloud rehearsal for tags touching cloud paths. |
| `.github/workflows/relay.yml` | `deploy` job runs `cloud-guard.sh` first. |
| `docs/cloud-rehearsal.md` (new) | What it reproduces and how to run one scenario locally (never against a live VPS). |

Scenarios C02–C10 need the pieces built by 50.02, 50.07, 50.08 and 50.09. This story builds the
harness, C01 and C08 (which need only 50.03–50.05), and the empty slots; each later story adds
its scenarios and turns them on.

### Tests

T01–T05 above; the scenarios themselves.

### Rollout

1. Merge with C01 and C08 live, the rest listed in `expected-failures.txt` with the story that
   enables each.
2. Turn on the deploy guard at once; the tag guard once 50.07 lands.

### Rollback

Remove the guard step from `relay.yml` (a one-line revert). The rehearsal itself changes
nothing outside CI.

### Dependencies

50.01 (the test zone mirrors the production names).
