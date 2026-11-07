---
schema: 1
kind: pr
pr: 802
title: A Python log call from 19.5 to 11.5 µs, and the bench's runner factor measured
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 744
---

## What happened
- Taking the processors out one at a time found where the pipeline logger's ~19.5 µs a call went: the redaction 8.7 µs, the ring's second JSON encoding 2.5 µs, the time stamp 0.9 µs.
- The redaction checked each value against the rules' needs with a case-insensitive regex alternation (about 1 µs on a 12-character value). It is now `in` on the lowered text, in a loop. Each key's kind is kept, and the logger's own `user_h` is no longer scrubbed.
- The ring keeps the console's line instead of encoding it again (`make_json_ring_renderer`), and `ts` is formatted once a second.
- `log_bench.py`'s calibration reference was 0.20 s, about 3.6 times the reference machine's 0.055 s, so CI came out at factor 1 although it is 2.8 times slower at logging. The factor is now the slower of two loops, as Go's `Calibrate` does, and the report shows both loops' times.
- The nightly workflow skipped the Python bench whenever a Go bench failed. It now runs unless the run is cancelled.

## What we learned
- In CPython's `re`, an alternation of literals with `IGNORECASE` is slow; `any()` over a generator costs twice a plain loop. Measure with `timeit` before reaching for a regex in a hot path.
- A calibration constant must be measured, and its raw times reported: a clamp at 1 hides a wrong reference.
- On the nightly macOS runner, the Go logger's p99 was 40 µs against a 22.5 µs budget (p50 3.8 µs). That is its own problem, not this PR's.

## Open
- The Go logger's p99 on the macOS nightly runner (an issue of its own).
