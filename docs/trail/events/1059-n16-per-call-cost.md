---
schema: 1
kind: pr
pr: 1059
title: N16 states the plugin path's per-call cost and judges CPU and paired wall time
date: 2026-11-08
session: b7095d77-a4dd-4b22-9468-dcb6f082ae2b
prs: [1021, 1049]
---

## What happened
- N16, the first-party parity timing check, failed `plugins / first-party` on `detect` in #1021 (plugin 51.4 ms > 1.10 × 25.2 ms) and #1049 (53.3 > 1.10 × 26.5): a steady 2×, not noise.
- #1049's report broke the extra ~27 ms down: nearly all of it is the run's 29 plugin API calls, about 0.9 ms each over the in-process query (≈0.58 ms of the API's CPU, ≈0.15 ms of SDK encode/decode, the rest socket wait). That is within N18's 2 ms a round trip; the cost is real. The old allowance was a fixed 25 ms with no per-call term: it held at `detect`'s usual 19 calls and failed at 29.
- `maktaba_plugin_sdk.parity.perf_check` now judges `JobTiming(wall, cpu, calls, hop)` per job and round. CPU (worker, runner, stage code and the plugin API through `/proc/<pid>/schedstat`, not Postgres): p50 ≤ 1.10×, p95 ≤ 1.15×, allowance the larger of the percentage and 25 ms plus `calls × 1 ms` (`STATEMENT_CPU_S`). Wall: the median of paired `plugin − in-process` differences within the larger of 10% and 25 ms plus `calls × hop` (the job's cheapest call, capped at 2 ms). The retry-once rule for a timing-only failure is unchanged. The Epic 54 README's N16 decision was rewritten.
- Injected regressions through the real bench: twice the CPU fails on CPU p50/p95; a 0.3 s sleep (no CPU) fails on paired wall; equal work passes. `test_perf_check.py` replays #1021's numbers, stalls and a slow stretch (all pass) and twice the CPU, twice the stated per-call CPU and a waiting plugin (all fail).
- A temporary `n16-repeat` workflow (6 shards × 6 reps, removed in the PR) gave the old measure 1 real failure in 36 full-suite reps and the new one 0 of 36 plus 0 of 36 classify-alone reps. Merged as f0c280cc3 on head adf285b08.

## What we learned
- The runner runs inside the test process as an asyncio task, so `time.process_time()` covers the worker, the runner and the plugin code.
- A fixed allowance over a cost that scales with call count looks flaky; it fails whenever the count moves. State the per-call cost and let CPU (which ignores stalls) and paired wall (which cancels a slow machine) judge it.
- Tighter per-call budgets failed historical runs (5 ms a job with 0.8 ms a call fails 11 p50s on `plex_livetv`, whose calls go to Plex), so the budgets were set from 1,824 CI stage benches, not tightened.

## Keep doing
- Before changing a timing gate, judge the old and the new rule offline on stored CI samples and say how many runs each would have failed.

## Open
- `detect`'s workload depends on test order: 2 seasons and 15 episodes when classify runs alone, 1 season and 8 episodes in the full suite. Parity holds (both sides see the same state), but the suite proves less than it could. Neither half of `tests/plugins/firstparty` alone reproduces it; it is not the hash seed or elapsed time.
- CPU p95 over 11 runs is their maximum; a rare job costing ~4× CPU in both processes came within 0.8 ms of failing `topic_assign` once.
- The plugin API's ~0.58 ms of CPU per call is the bulk of the per-call cost.
