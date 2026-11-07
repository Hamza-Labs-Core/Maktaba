---
schema: 1
kind: pr
pr: 904
title: The log bench's compare test checks both rounds ran, not their speed
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [866, 891]
---

## What happened
- `test_compare_runs_the_base_logger_round_for_round` (added in #866) asserted `0.2 < ratio < 5` between two runs of the same logger. #891's integration job read 7.579: a p99 of 2,000 calls in a fresh process on a shared runner. #891 doesn't touch the Python logger.
- The test now checks that each side ran its rounds, that each side's p99 is its fastest round, and that the ratio is finite and positive. `verdict`'s parametrized tests cover what the ratio decides.

## What we learned
- A p99 from a few thousand calls on a shared runner can't be bounded, even against the same code. A test of the mechanism shouldn't also check speed. Keep speed checks in the bench's own verdict, compared on one runner.

## Open
- None.
