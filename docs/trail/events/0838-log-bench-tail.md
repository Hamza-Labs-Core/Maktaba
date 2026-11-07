---
schema: 1
kind: pr
pr: 838
title: The log bench's budget follows the runner's tail; the logger's context merge and chain cost less
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 744
---

## What happened
- After #828 the strict Python bench still failed #824 on a runner of factor 2.70 (p99 54.6 µs against 54.0). There, logging was 2.8 times slower at p50 and 3.3 times at p99, while the factor was computed from totals.
- `calibrate()` now also measures the p99 of small chunks of the same independent work against the reference's, so the budget scales with the runner's tail. The logger's own speed stays out of it.
- The logger got cheaper: `merge_contextvars` reads structlog's own variables (2.26 to 0.43 µs with 30 others present), and eight processors fold into two.

## What we learned
- Scale a percentile budget by the same percentile of independent work. A factor from totals under-counts a busy runner's tail.
- Best-of-three rounds fixes one noisy round, not a slow kind of runner. Check whether a failure repeats on the same runner type before calling it noise.

## Open
- The margin at the reference is still about 20%. If the strict bench fails again, look at the scrub of values that carry a secret (about 2.6 µs).
