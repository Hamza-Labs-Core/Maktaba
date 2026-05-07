# Story 20.7 — Performance regression tests in CI

[Epic 18](../18-performance/README.md) budgets need a CI lane; flakes
are managed, not silenced.

## Acceptance criteria

- AC1. A `make perf-ci` target runs a reduced perf suite (≤ 5 minutes)
  on every PR against a docker-compose stack on a known runner profile.
- AC2. The full perf suite (`make perf`, 30 minutes) runs nightly on a
  dedicated runner; results are pushed to a time-series store
  (Prometheus pushgateway or simple file).
- AC3. PR perf changes are reported as a comment with deltas vs. main;
  a > 10 % regression on any p95 budget blocks merge.
- AC4. Flake handling: a perf budget that fails 3× in 5 days on main
  is marked unstable and quarantined with an issue auto-filed.

## Test cases

- TC1. PR delta: artificially slow a query by 50 ms; the comment shows
  the regression and the merge gate fires.
- TC2. Nightly publish: results are queryable for 30 days; charts are
  rendered in a static dashboard.
- TC3. Quarantine: a flapping budget is automatically tagged and a
  triage issue is filed with logs attached.

## Edge cases

- EC1. Runner contention (CI host shared) — perf-ci runs on a tagged
  runner only; if no tagged runner is available, the job queues
  rather than running on a busy one.
- EC2. Cold-start vs. warm-cache — the perf-ci suite is explicitly
  warm-only; a separate weekly cold suite exists.
- EC3. Cross-OS variance — perf-ci reports per-OS budgets and never
  averages across runners.
