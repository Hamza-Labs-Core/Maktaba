# Story 50.29 — The broadcast rehearsal and fan-out load test in CI

> Epic 50 · Phase B · Depends on: 50.06, 50.21, 50.22, 50.25 · Needed by: the broadcasting gate

## Why

Epic 50's rule (goal 7) is that every failure path is proven in CI before it reaches production,
as the update rehearsal does. Broadcasting adds new paths: fan-out, the home going offline, the
kill switch, takedowns, plan modes, the Hetzner pair being down, and an edge deploy. It also adds
one number that must hold, **one upload from the home per segment per rung, whatever the number
of viewers**. This story adds those scenarios to 50.06's cloud rehearsal harness.

## Description

### 1. The stack in CI

- **A home:** the streaming service and the API from the PR's sha, with a test-pattern channel
  (a burned clock and counter), VideoToolbox off, and a software encoder for the lower rungs in
  CI only.
- **The edge:** `web/packages/broadcast-edge` in **Miniflare** with local R2, DOs and a fake
  Analytics Engine. No live Cloudflare call.
- **The cloud:** 50.06's compose stack (API, worker, Postgres). Payment is the fake provider;
  Turnstile and app attestation are fakes.
- **Viewers:** a Go load generator (`tools/broadcast-viewers/`) that simulates N HLS players. Each
  plays, heartbeats, fetches playlists every target duration and fetches each new segment. It
  can simulate several "locations" (separate cache namespaces in Miniflare) to model edge
  locations.
- **Counters:** bytes and requests out of the home (the uploader's own metrics and a proxy
  counter), R2 reads, DO requests, and viewer-side latency and errors.

### 2. Scenarios

Each scenario is listed as an expected failure until its story turns it on (50.06's rule).

| Scenario | Story | Steps | Pass when |
|---|---|---|---|
| `broadcast-fanout` | 50.21 | 1 channel × 3 rungs; viewers 1 → 10 → 100 → 1,000 across 5 locations, 5 minutes each | **home PUTs = segments × rungs at every step** (±0); R2 reads per segment ≤ locations × rungs; viewer segment p95 ≤ 1.5 s after the home finishes it; 0 viewer errors |
| `broadcast-home` | 50.22 | publish; kill the encoder; wait 30 s; restart it | slate ≤ 15 s; live again after restart; no player error |
| `broadcast-home-offline-24h` | 50.21 | stop the home; fast clock to 24 h | slate, then `off_air` at 24 h |
| `broadcast-plans` | 50.20 | drive a publisher through `full`, `lowest`, `wait`; add pay-as-you-go | modes in order; no playing session stopped |
| `broadcast-publishable` | 50.23 | mixed sources; publish; withdraw one source | the guide holds only publishable items; slate on withdrawal; replaced at top-up |
| `broadcast-access` | 50.26 | invite uses, short codes, Turnstile, limits | as 50.26's criteria |
| `broadcast-kill` | 50.25 | 200 viewers; `off_air`; lift; cloud API down + break-glass | stop ≤ 60 s on both paths; live again after the lift |
| `broadcast-takedown` | 50.24 | notice → takedown (airing now) → counter → restore; 3 strikes | the timings and states in 50.24 |
| `broadcast-hetzner-down` | 14.8 | stop the cloud stack for 2 h (fast clock) | channels keep playing; metering arrives exact afterwards |
| `broadcast-edge-deploy` | 50.21 | deploy a new edge version with gradual rollout mid-stream | no viewer error; DO state kept |
| `broadcast-observe` | 50.28 | break ingest; stale the revocation list; stop the canary | the alerts fire; the status component changes |

### 3. Reports

Each run writes a report into the CI artifacts, keeping the home's, the edge's and the cloud's
logs (the "tests keep logs" rule):

- per scenario: pass or fail and the timings;
- the fan-out table (viewers vs home bytes vs R2 reads);
- latency percentiles.

### 4. When it runs

- **The PR job** runs only for its code: `broadcast-edge`, `cloud/internal/broadcast`,
  `streaming/internal/broadcast` or `api/internal/broadcast` changes. It runs the short versions:
  `broadcast-fanout` to 100 viewers, plus every functional scenario.
- **Nightly** on main: the full fan-out to 1,000 viewers and the 24 h fast-clock scenarios.
- **Before a tag:** the tag gate needs the last nightly green on the tagged sha's broadcast code.

## Acceptance criteria

- **Given** a PR touching `web/packages/broadcast-edge`,
  **when** CI runs,
  **then** the broadcast rehearsal runs its PR set and blocks the merge on failure, and unrelated
  PRs don't run it.
- **Given** the fan-out scenario at 1,000 viewers,
  **when** it runs,
  **then** the home's upload equals segments × rungs exactly, and the report shows it next to the
  viewer count.
- **Given** a regression that makes the edge fetch from the home or R2 once per viewer,
  **when** the scenario runs,
  **then** it fails, naming the counter.
- **Given** a scenario not yet turned on,
  **when** it runs,
  **then** it is reported as an expected failure, not as a pass.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | load generator | 10 simulated players against a fixture server | requests and heartbeats as a real player's |
| T02 | integration | harness | a scenario set to expected-failure | reported XFAIL |
| T03 | self-test | fan-out with a planted per-viewer fetch | run | fails on the counter |
| T04 | CI only | the scenarios in §2 | PR and nightly sets | as listed |

## Edge cases

- **CI runner network limits.** The 1,000-viewer run is nightly on a larger runner. The PR set
  stays at 100 viewers.
- **Miniflare doesn't model Cloudflare's cache lock exactly.** The fan-out bound on R2 reads is
  per location with a tolerance (≤ 2 per object per location). The home-upload bound is exact,
  because it doesn't depend on Cloudflare.
- **Software encoding in CI is slower than hardware.** Rung timings are relaxed in CI, and the
  latency targets are checked only on the `copy` rung.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `tests/cloud-rehearsal/broadcast/` (new, in 50.06's harness) | Scenarios, fixtures, report. |
| `tools/broadcast-viewers/` (new, Go) | The load generator. |
| `.github/workflows/broadcast-edge.yml` | PR set (path-filtered); nightly set; artifacts with logs. |
| `docs/cloud-rehearsal.md` (50.06's doc) | The broadcast scenarios. |

### Tests

This story is the test. Its own checks are T01–T03 in the harness's unit job.

### Rollout

The harness and `broadcast-fanout` land with 50.21. Every other scenario is turned on by its own
story's PR.

### Rollback

A flaky scenario is never skipped silently. It is fixed, or marked expected-failure with an issue
link, and the broadcasting gate (README §14.11) counts it as not passed.

### Dependencies

50.06 (the harness), 50.21 (the edge), 50.22 (the home), 50.25 (the kill switch).
