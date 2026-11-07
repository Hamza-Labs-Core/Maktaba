---
schema: 1
kind: pr
pr: 690
title: Epic 53 story 53.13 — a performance budget for the log path
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- `internal/logbench`: capture, index catch-up and lag, three kinds of search, the dashboards' 30 days, the Go logger and the disk caps. The benches are tests behind the `logbench` tag, with the story's budgets scaled by a calibration loop.
- CI's `log-bench` job runs them on a change to the log path and keeps the numbers.

## What we learned
- At a tenth of the load, the capture bench's fixed costs (file creation, the first flush) dominate the per-line figure, which reads 28 µs against 15.6 µs at the full CI load. A per-line budget needs a load large enough to amortise setup.
- Adding a path filter means updating `ci-paths.yml`, the outputs and the unit call in `ci.yml`, the input in `_unit.yml`, and the expectations in `tools/ci/tests/test_ci_paths.py`. That test pins which parts each change runs, so it catches a filter that runs more than it should.

## Open
- The nightly macOS run with a regression issue, the Python logger bench, and the log pipeline's own numbers on the live Overview with a System-health lag warning.
