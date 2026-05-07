# Story 18.1 — Define and codify latency budgets

Establish per-endpoint p50 / p95 / p99 budgets and encode them as test
assertions so a regression fails CI.

These budgets are the canonical user-facing numbers. Internal stage budgets
(e.g., the 200 ms p95 internal database-side budget for the search query
in [Story 5.4](../01-pipeline/) of the pipeline epics) are decomposed
inside these end-to-end budgets and not duplicated here.

## Acceptance criteria

- AC1. A `perf_budgets.yaml` file under `shared/` lists every user-facing
  endpoint (REST, GraphQL, WS, HLS manifest, HLS segment first byte, search
  first result) with `p50_ms`, `p95_ms`, `p99_ms`, a hardware profile tag
  (`mac-m2-8gb`, `linux-x86-16gb`), and a cache-state tag (`warm` or
  `cold`).
- AC2. Initial v1 budgets, all measured at a 1,000-video / 10 k-segment
  fixture with the cache state noted:
  - `GET /api/libraries` (warm) — p50 ≤ 30 ms, p95 ≤ 80 ms.
  - `GET /api/videos/{id}` (warm) — p50 ≤ 25 ms, p95 ≤ 60 ms.
  - `POST /api/search` warm cache (FTS+vector fusion, top 20) —
    p50 ≤ 250 ms, p95 ≤ 500 ms, p99 ≤ 800 ms.
  - `POST /api/search` cold (no embedding-cache hit) — p95 ≤ 1.5 s.
  - HLS manifest first byte (warm) — p50 ≤ 50 ms, p95 ≤ 120 ms.
  - HLS segment first byte (warm cache hit) — p50 ≤ 40 ms, p95 ≤ 100 ms.
  - HLS segment first byte (cold transcode) — p50 ≤ 2.5 s, p95 ≤ 6 s.
  - WebSocket job-progress event end-to-end (Postgres NOTIFY → client) —
    p95 ≤ 250 ms.
  - Time-to-first-frame in the web player on a warm session — p95 ≤ 1.5 s.
- AC3. The perf-budget file is loaded by the perf test harness; assertions
  read budgets from it (no magic numbers in tests).
- AC4. The `make perf` target runs all budgets against a seeded dev
  instance and exits non-zero on any breach. The harness reports each
  breach with the offending endpoint name, the measured value, and the
  budget value.

## Test cases

- TC1. Unit: budget loader rejects malformed YAML (missing key, negative
  ms, p99 < p95) with a clear error.
- TC2. Integration: against a fresh dev compose stack with the seed
  fixture, every endpoint listed reports < budget.
- TC3. Regression: artificially slow a single endpoint's primary query by
  200 ms (via a per-route middleware injected only for the test); the
  harness must fail and the failure message must name that specific
  endpoint and report the per-endpoint delta.

## Edge cases

- EC1. Cold-cache run: budgets carry an explicit `warm` or `cold` tag;
  running the warm suite against a cold instance must explicitly skip
  with a clear reason, not silently pass.
- EC2. CI runner variance: each measurement is the median of 5 trials
  after 1 warm-up; outliers > 3σ are reported but don't fail the build,
  unless the median itself breaches.
- EC3. Hardware profile mismatch: running `make perf` on a host whose
  detected profile isn't in the budget file fails fast with an actionable
  message ("add a profile tag for `linux-arm64-32gb` and re-run").
