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

- On CI the Python logger costs ~50 µs at p50 and 80 µs at p99, against 4.6 and 10 µs for Go. The bench warns instead of failing (`LOGBENCH_PY_STRICT=1` makes it fail).
- A first version also skipped scrubbing the `event` field, on the grounds that `make lint-log` keeps it a literal. `test_log.py` logs a token inside an event and caught it. Lint covers the pipeline's sources, not every caller, so the event is still scrubbed.
- The integration run failed `TestControllerGivesItsLockBackWhenItStops`. Run's ping of its lock connection failed *because* the context was cancelled, so it discarded the connection. Postgres frees the lock only when that backend exits, after `stop()` had returned. A ping that fails while stopping now keeps the lead, so the deferred unlock runs.

## Open
- Bring the Python logger under its budget, then make the bench strict (issue #744).
- The `logs` role's CPU share and memory on a live home (53.13 §3) are not measured yet.
