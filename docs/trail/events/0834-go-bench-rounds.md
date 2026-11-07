---
schema: 1
kind: pr
pr: 834
title: The Go logger bench is judged on the best of three rounds too
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 807
---

## What happened
- On the nightly macOS runner a p99 was 6 to 10 times its p50 for both loggers. Go's `TestLoggerOverhead` failed one night and passed the next.
- It now runs three rounds and judges the lowest p99, as #828 does for Python. Every round is reported.
- The nightly, dispatched on the branch, passed. Go's rounds were 4.4, 4.9 and 4.6 µs (budget 22.3); Python's were 25.7, 24.3 and 25.3 µs (budget 27.1).

## What we learned
- A `workflow_dispatch` of the nightly on a branch measures a fix on the real runner before it merges. Do that before calling a bench fix done.
- One night's numbers swing a lot. The Python margin on the Mac is about 10%, so it stays a warning there.

## Open
- Nothing.
