---
schema: 1
kind: pr
pr: 740
title: Epic 53.13 — the Python logger bench and the nightly Mac log benches
date: 2026-11-06
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- `pipeline/tools/log_bench.py` times the pipeline's logger per call. CI's `log-bench` job runs it.
- A nightly macOS arm64 workflow runs the Go and Python benches, keeps the numbers 90 days and opens an issue on a cost more than 20 % over the last night's (`tools/logbench/compare.py`, with runner factors taken out).
- Writing the bench found two costs in the Python redaction, both fixed.
  - `Rules.key_sensitive` used an `lru_cache` keyed on the frozen `Rules` dataclass. Every field of every line hashed the whole rule set: 1.8 M hash calls for 20,000 lines.
  - Every string field ran all eight value rules, including the logger's own fields.

## What we learned
- An `lru_cache` on a function that takes a large frozen dataclass hashes that dataclass on every call. Its fields are not cached. Keep per-key answers on the instance instead.
- During this session the Mac's load average was ~135 (other sessions building), and a bare `print` to `/dev/null` took 28 µs. Local timings were unusable as absolutes. The profile's call counts still showed where the time went, and the budget comes from the CI and nightly runners.

## Open
- The `logs` role's CPU share and memory on a live home (53.13 §3) are not measured yet.
