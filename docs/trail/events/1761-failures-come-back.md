---
schema: 1
kind: pr
pr: 1761
title: Failures whose cause has passed come back by themselves
date: 2026-11-25
session: 5aab7d5e-37cb-4588-bf7a-d1e3676d6cd9
agent: claude-code
---

## What happened
- On a home of v0.3.161, 54,540 thumbnails stayed failed as `file_missing` (item_permanent, 1 try of 3) sixteen days after #1062 fixed their cause. #1062's event expected the next completed scan to bring them back; that home scans nothing on a schedule, so it never came.
- The queue-health leader round now heals without a scan (`engine/healer.py`): it looks at 250 open failures every 10 minutes, on the share probes it just made, and queues a stage again at priority 950 when the file is there. It covers `file_missing`, the NAS causes of class transient, a lost database or transaction under a plugin's job, and a foreign key a row went away from. At most three times an item and stage since its last success.
- `DatabaseUnavailable` and `plugin_tx` are transient from now on; `plugin_tx` was a refusal that was never retried.

## What we learned
- A failure that only a scan heals is never healed on a home that does not scan: `sweep_interval_sec` is 0 by design (story 9.3, Epic 59 for disk sleep).
- A job's dependents are queued at the default priority 100, not at the job's: a low-priority sweep of 54k thumbnails would have made 54k `thumb_small` jobs among the normal work. What follows a job at 950 keeps it.
- A terminal failure leaves `not_before` NULL and nothing claims a failed row, so a failed row's `not_before` can hold when it is looked at again: no table, no process memory, and rows not due are skipped in SQL.
- With `LIMIT`, every row a sweep selects must get an outcome (queued, or put off); a row skipped in Python stays at the head and starves the rest.
- `asyncio.wait_for(to_thread(...))` with no free thread cancels the call before it starts: a flag the thread clears must be cleared by the caller then.

- A job queued in a failed one's place must carry its payload and `force_cascade`: `payload_seasons` edges, relisten and backfill marks live there. The first version dropped both, as `requeue_failed` did; the review found it. `run_now` is the exception (it passes the quiet hours).
- A new class of jobs counts everywhere jobs are counted: the quiet hours took a healed backlog for new work, so a new file would have waited for the night.
- Putting a row off by a fixed hour starves the others once more rows than a day of rounds take are in that state: leave out in SQL what cannot be answered now (a root that is down).
- `+column` steers SQLite's planner off an index; on PostgreSQL unary plus on text is an error, so it is per dialect. Read the plan in a test.
- A new heading anchor in the help changes maktaba.tv's `dist-manifest.json`: `npm run build` in `web/packages/website`, and commit it. Its reference pictures change with the page's length: take the `-actual` ones of the CI run's `website-visual-diff-N` artifact, look at each, and list them under `Reference images:` in the body.

## Keep doing
- Take each guard out once and see a test fail.

## Open
- The re-review's minors are in #1764.
- Only videos' stages are healed; a library's or the catalog's failed stage waits for its next trigger.
- The failures page's `retryTargets` and the queue page's bulk retry (`enqueueTargets`) drop the payload; `jobs.go`'s Retry carries it.
- A scan's own heal (`confirm_missing`) still queues at priority 100 with no cap.
- Failures older than `error_cause` (NULL there) are not matched; they leave with the 90 day prune.
