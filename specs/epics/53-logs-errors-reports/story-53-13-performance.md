# Story 53.13 — Performance budget and load test

> Epic 53 · Phase 2 · Depends on: 53.09, 53.12 · Needed by: —

## Why

Logging that slows playback or fills the disk is worse than no logging. README §4.2 and §5.3 set
numbers; this story measures them in CI on every change to the log path, and fails the build
above budget, as `test-budget` does for test times.

## Description

### 1. Benchmarks (`cmd/maktaba-server/internal/logbench/`)

| Bench | Load | Target |
|---|---|---|
| `capture_sustained` | 4 stand-in children writing 125 lines/s each (500/s) for 10 min | supervisor capture ≤ 1 % of a core; 0 lines lost |
| `capture_burst` | 5,000 lines/s for 60 s (mixed levels, 20 % ≥ warn) | 0 `warn`+ lost; segments complete; drop counts reported |
| `index_lag` | 500 lines/s | p95 write→indexed ≤ 2 s; `logs` role ≤ 3 % core, ≤ 120 MB RSS |
| `index_catchup` | after the burst | caught up ≤ 2 min |
| `tail_latency` | 500 lines/s, 3 tails | p95 ≤ 2 s |
| `search_filters` | 15 M lines (7 d synthetic, realistic mix incl. Arabic) | p95 ≤ 300 ms (24 h, service + level) |
| `search_fulltext` | same | p95 ≤ 1.5 s |
| `search_request` | same | p95 ≤ 300 ms |
| `dashboard_30d` | 30 d of rollups | p95 ≤ 500 ms |
| `logger_overhead` (Go, Python) | 1 M log calls | ≤ 20 µs p99 per call |
| `disk` | a simulated day at 500/s (~2 GB written) | the janitors hold segments ≤ 550 MB, `logs.db` ≤ 350 MB and the total ≤ 1 GB at every sample; `warn`+ of the last 24 h kept |

The 15 M-line corpus is generated (seeded, `tools/logbench/corpus.go`) and cached per seed, like
the rehearsal seeds.

### 2. Where they run

- A CI job `log-bench` on Linux runners, on PRs touching `shared/log/**`, `cmd/maktaba-server/internal/{logkeeper,logindex,supervisor}/**`
  or the pipeline's `log/`. Thresholds are relative to the runner (a calibration loop first, as
  `test-budget` normalises) with the absolute targets above for the Mac class recorded in
  `docs/performance/logs.md`.
- Nightly on a macOS arm64 runner for the absolute numbers; a regression of more than 20 % opens
  an issue.

### 3. On the live home

The Overview dashboard (53.12) shows the log pipeline's own numbers (lines/s, dropped, index lag,
disk), and System health warns when index lag stays above 30 s for 10 minutes.

## Acceptance criteria

- **Given** a PR that makes capture 2× slower,
  **then** `log-bench` fails with the bench and the numbers.
- **Given** the nightly Mac run,
  **then** every target in §1 is met, and the results are kept as an artifact for 90 days.
- **Given** the live home at its normal rate,
  **then** the Overview shows the log pipeline using ≤ 5 % of a core and ≤ 200 MB in total.

## Test cases

The benchmarks in §1 are the tests.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/logbench/` (new), `tools/logbench/corpus.go` (new) | §1. |
| `.github/workflows/_unit.yml` (a `log-bench` job), `.github/workflows/nightly-perf.yml` (new or existing nightly) | §2. |
| `docs/performance/logs.md` (new) | Targets and how to run. |
| `api/internal/handlers/systemstatus/rules.go` | Index lag warning. |

### Rollout

Lands with 53.09; thresholds start as warnings for one week of nightly data, then fail.

### Rollback

Turn the job back to warn-only.

### Dependencies

53.09, 53.12.
