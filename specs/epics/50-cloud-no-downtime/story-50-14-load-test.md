# Story 50.14 — Load test and sizing

> Epic 50 · Phase 1 · Depends on: 50.06, 50.08 · Needed by: 50.16

## Why

Every capacity number in the README is a guess. Hetzner doesn't publish per-server network
speed (reports range from 300–500 Mbit/s to over 1 Gbit/s, research §4), and nobody has measured
how many tunnels or streams one `maktaba-cloud` relay process carries on a 2- or 4-vCPU machine.
Until we measure, the README plans at **300 Mbit/s and 5,000 idle tunnels per node**. This story
measures it, keeps measuring it on every release, and turns the result into the sizing table
and the Phase 2 trigger.

## Description

### 1. The harness

`cloud/loadtest/` and `cloud/cmd/maktaba-loadtest` (Go; no k6, so it reuses simhome and
streamgen from 50.03 / 50.06, inside the cloud module because simhome is one of its internal
packages):

- **Homes:** `simhome` with N tunnels. Each simulated home serves a fake media API: HLS
  playlists and segments of fixed sizes from memory (no disk), so the home side is never the
  bottleneck.
- **Clients:** `streamgen` with M players. A profile sets the bitrate (2.5, 5, 8 Mbit/s), the
  segment length (4 s) and how many players start per second. Each player measures time to
  first byte and whether each segment arrived before the next was due (a **stall**).
- **Idle load:** most homes have no stream; they just hold the tunnel and PING every 25 s, as in
  real life.
- **Collector:** scrapes the relay's `/metrics` and the node's CPU, memory, network and socket
  counts (node_exporter) every 5 s.

Run: `go run ./cmd/maktaba-loadtest run --target https://<host> --homes 1000 --homes-step 1000
--players 50 --players-step 50 --bitrate 5M --step 3m`. It ramps up until a **stop rule** trips, then reports the last good step.

### 2. Stop rules (what "full" means)

A step fails, and the previous step is the node's capacity, when any of these holds for 2 minutes:

- relay CPU > **70 %** of the machine;
- p95 added latency (time to first byte through the relay minus straight to the simulated home)
  > **20 ms**;
- stalls > **0.5 %** of segments;
- any tunnel dropped that wasn't drained;
- memory > 75 % of the machine.

### 3. Where it runs

| Where | Scale | When | Infra |
|---|---|---|---|
| CI, rehearsal job `load-small` (50.06's compose project, one node) | 1,000 homes, 50 players at 5 Mbit/s for 5 min | every PR touching `cloud/internal/relay/**`, `cloud/internal/handlers/relay/**`, nightly | none: the runner only |
| The owner's **staging pair** on `staging.relay.maktaba.dev` | ramp to failure | by hand (`workflow_dispatch` of `loadtest.yml`) before launch, after any relay change that moves CI's numbers > 10 %, and before every Phase 2 resize | the owner creates it (two nodes + a load generator machine, same plans as production) and deletes it after; ~€2–5 per run at hourly prices |

CI can't measure a Hetzner machine's real network, so `load-small` checks **regressions**: CPU
per Mbit/s relayed and memory per tunnel, against the last main run's numbers; it fails when
either gets 20 % worse. The staging run gives the **absolute** numbers.

The load generator must not run on the relay machine (it would measure itself), and staging
never uses user-facing names: only `maktaba.dev`.

### 4. Output: the sizing table

Each staging run writes `docs/cloud-sizing.md` (by PR, from the run's artifact):

| Plan | vCPU / RAM | Idle tunnels at stop | Relayed Mbit/s at stop | Concurrent 5 Mbit/s streams | CPU per 100 Mbit/s | Date, sha |
|---|---|---|---|---|---|---|
| (filled by the run) | | | | | | |

The README §5 numbers and §4.2's "300 Mbit/s planning figure" are replaced by these, and the
cost model in architecture §13.13 is updated from them.

### 5. The Phase 2 trigger

The monitoring (50.10) gets two recording rules from this table:

- `relay_capacity_used = peak relayed Mbit/s ÷ measured Mbit/s at stop`
- `relay_tunnels_used = tunnels_open ÷ measured idle tunnels at stop`

An alert fires when either passes **60 %** at the daily peak for **7 days in a row**: start
Phase 2 (50.16). At 80 % for one hour, a page: resize the active pair now (Hetzner rescale to
the next plan during a planned switchover, 50.07).

## Acceptance criteria

- **Given** the harness against a relay,
  **when** it ramps,
  **then** it stops at the first step that breaks a stop rule and reports the previous step's
  tunnels, Mbit/s, streams and CPU.
- **Given** a PR that makes the relay use 25 % more CPU per Mbit/s,
  **when** `load-small` runs,
  **then** it fails and shows both numbers.
- **Given** a staging run finishes,
  **when** its artifact is turned into `docs/cloud-sizing.md`,
  **then** the table has a row per plan tested with the date and sha.
- **Given** peak relayed traffic above 60 % of measured capacity for 7 days,
  **when** the rule evaluates,
  **then** the owner gets "start Phase 2" with the numbers.
- **Given** the load test,
  **when** it runs anywhere,
  **then** it targets only CI containers or `*.maktaba.dev` names (the tool refuses a target
  under `maktaba.tv`).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | stop rules | feed a series crossing CPU 70 % | step marked failed after 2 min |
| T02 | unit | stall detection | a segment 1 s late | counted as a stall |
| T03 | unit | target check | `--target https://x.relay.maktaba.tv` | refused |
| T04 | CI | `load-small` | main | baseline written as artifact |
| T05 | CI | `load-small` with a test build adding a 2 ms sleep per frame | PR | fails on regression |

## Edge cases

- **Hetzner noisy neighbours.** Shared-vCPU plans vary; staging runs three times and keeps the
  worst run.
- **IPv6.** Clients with IPv6 reach the relay over its IPv6 too; the harness tests both.
- **TLS cost.** Measured with real TLS through Caddy (it is part of the path), not plain HTTP.
- **Traffic bill.** A 20-minute run at 1 Gbit/s is ~150 GB per direction, well inside the
  staging machines' included traffic.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/loadtest/`, `cloud/cmd/maktaba-loadtest` (new, Go) | Ramp driver, stop rules, collector (node_exporter on staging, `docker stats` in CI), regression compare, sizing rows. |
| `cloud/internal/relay/simhome/` (from 50.03) | Its `/stream` serves sized segments from memory over a set time (the home is never the bottleneck); homes PING every 25 s. |
| `cloud/rehearsal/streamgen/` (from 50.06) | Bitrate profiles, stall and TTFB measurement. |
| `.github/workflows/cloud-rehearsal.yml` | Job `load-small` with the regression check. |
| `.github/workflows/loadtest.yml` (new) | `workflow_dispatch` against staging; secrets on a `staging` environment. |
| `docs/cloud-sizing.md` (new) | The table. |
| `deploy/cloud-ha/monitoring/alerts.yml`, `capacity-test.yml` (from 50.10) | `relay_capacity_used`, `relay_tunnels_used`, the 60 % and 80 % alerts, against the witness's `relay_measured_*` textfile (`RELAY_MEASURED_MBIT`, `RELAY_MEASURED_TUNNELS`); runbook `docs/runbooks/cloud/capacity.md`. |
| `specs/architecture.md` §13.13 | Cost model from measured numbers (after the first staging run). |

### Tests

T01–T05; `load-small` as the CI-only scenario of the cloud rehearsal.

### Rollout

1. Harness and `load-small` merge first (baseline from main).
2. Before launch (README §11), the owner creates the staging pair, runs `loadtest.yml`, and the
   sizing PR updates the README numbers.

### Rollback

Nothing runs in production; drop the job.

### Dependencies

50.06 (harness, simhome, streamgen), 50.08 (the deploy on the staging pair is the production
deploy).
