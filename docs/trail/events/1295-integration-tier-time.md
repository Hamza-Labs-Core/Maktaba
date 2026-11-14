---
schema: 1
kind: pr
pr: 1295
title: Integration tier budget 6m30s; SQLite datetime warnings gone
date: 2026-11-14
session: 9bf8c97a-3cf8-4549-bf90-d22d611218d2
agent: claude-code
prs: [1240, 1267]
---

## What happened
- #1295 merged as 2072cd489. The integration tier had failed #1240 (5m0.17s) and #1267
  (5m5.6s) on time alone, every test passing. `INTEGRATION_TIER_BUDGET` went from 5m to 6m30s,
  the slowest passing run plus ~30%, with the numbers in the Makefile comment.
- The pipeline registers explicit SQLite adapters for `datetime` and `date` that store the same
  text as Python's deprecated defaults (`isoformat(" ")`, `isoformat()`). The tier's 41,834
  warnings (aiosqlite binding datetimes) dropped to 2. `pytest -m integration` now lists its 25
  slowest tests in the tier's log.
- #1295's own run: tier 4m15.6s (API module 254s, pytest 228s).

## What we learned
- The tier is as long as the API module's `go test`: wall is it plus ~15s. Its 132 packages
  add up to 310-464s of package time, mostly test run rather than compile. `pytest -m
  integration` runs beside it on the same 4 vCPUs, and both rise and fall together from run
  to run (main's last 15: 2m29s-4m29s).
- What grew is the pytest side: 127 tests passing on 2026-10-26, 271 on 2026-11-14, and its
  time roughly doubled. The two failing runs were ~20% slower in every module, which points to
  a slower runner, not one test.
- The pytest side takes 35s locally, but 124-278s in CI, and its 25 slowest CI tests add up to
  only ~30s. Most of its CI time is spent outside the tests themselves.
- 40k captured warnings are not free: they were a third of the tvrelease tests' time
  (4.76s -> 2.95s locally).
- Main's per-run tier numbers come from the job logs:
  `test-budget [integration] wall=`, each Go module's `(api, NNNs)` header, and pytest's
  `passed ... in NNNs` line.

## Keep doing
- Size a wall budget from measured runs (slowest passing run plus ~30%) and write the numbers
  next to it.

## Stop doing
- Don't guess a tier slowdown from one run. Compare module by module against main's runs:
  if everything is slower by the same factor, the runner is slow.

## Open
- pytest's CI session time is mostly outside test durations. Collection and imports of the
  whole tree (5,212 items) while four Go modules compile may be the cost. Profiling
  collection in CI (`--collect-only` timed in the job) would show whether narrowing
  collection to the integration-marked files is worth doing.
