---
schema: 1
kind: pr
pr: 1768
title: Pipeline reliability follow-ups for failure healing, word timings, and database restarts
date: 2026-11-26
session: 4419ee9d-62d2-421a-9d13-992d2c6e9711
agent: antigravity
---

## What happened
- Closed #1764 (healer and propagation minors): tracked hung directory listings via `paths.track_unavailable()` so non-ASCII paths are treated as inconclusive rather than confirmed missing; rotated `_look` with `_look_after` so 7+ slow folders cannot starve other shares; excluded both full-folded and ASCII-folded down-root prefixes in SQL across SQLite and PostgreSQL; caught per-path `_exists` errors; wrapped `_count_relisten` so counting failures cannot abort a batch; clamped Epic 47 follow-up priority so normal triggers on healed `dedup_scan` jobs do not inherit 950; and added mutation-verified tests for all five guards.
- Closed #1765 (word timings and second-listen minors): stamped `words_by_sentence: true` whenever word timestamps are requested and handled rollback-then-reupdate recovery without re-transcribing `v0.3.166` transcripts; split sub-20 ms words proportionally across sentence boundaries in `align.py`; and clamped each stretch's quarter-second tail before the next spoken sentence starts in `refine.py`.
- Addressed #1751 item 2: recognized `DatabaseUnavailable`, `AdminShutdownError`, and `CrashShutdownError` as `connection_lost` (while excluding filesystem `OSError` subclasses), and queued unrecorded `_dispatch_job` failures in `ClaimLoop._pending_failures` to flush via `mark_failed_or_retry` as soon as the database answers again.
- Merged as `229c4dd95` with all 172 checks green.

## What we learned
- `claim_owner.running_for(worker_id)` prevents a stale task from clobbering a job reclaimed by another worker, but not a newer live attempt reclaimed by the same worker after a lease expiry. Tracking active `task -> job.id` in `ClaimLoop._task_job_id` guards both queueing and flushing against same-worker reclaim races.
- `schema_compat.connection_lost` treating any `OSError` as a lost database was safe only on bare DB calls; when called from `_dispatch_job`, filesystem `OSError` subclasses (`FileNotFoundError`, `PermissionError`, etc.) must be excluded explicitly so they are never retried as `DatabaseUnavailable`.
- Nested context managers that swap thread-local flags (`track_unavailable`) must propagate hits back to the outer flag on exit.

## Keep doing
- Mutation-test every SQL and Python guard in queue/healer code so each test is proven to fail when its guard is removed.
- Run `tools/affected-tests` gate locally before pushing so every changed source file has a changed mapped test.

## Open
- #1751 remains open for the upstream PostgreSQL `EINTR` on `open()` report.
