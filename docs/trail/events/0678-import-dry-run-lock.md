---
schema: 1
kind: pr
pr: 678
title: The MediaIntel dry run takes no summary lock; detect is queued after the commit
date: 2026-11-06
session: b705488f-6ddd-4a03-8d08-60545e9b7ffa
prs: [662]
---

## What happened
- A review of #662 found that the import's `LOCK TABLE processing_done_subjects` was held in dry runs too (the default), stalling every finishing job for the whole import. The detect `enqueue_job` also ran under it, and could deadlock with a running detect job whose finish waited on the lock.
- Slot 0186 lets a transaction that sets `maktaba.done_subjects_dry_run` skip the summary touch. Only the import's always-rolled-back dry run sets it. Apply keeps the lock and queues detect in a second transaction after its commit.

## What we learned
- The import's done jobs cannot be written in one statement or in key order: each stage's fingerprint reads the stages before it, and detect's reads the video stages by `library_id` (`detect.py`). Writing them with a NULL library first, so the trigger skips them, would hide them from detect.
- A deadlock needs the import to wait on a job row while holding something the job's finish needs. Holding a summary row is enough, not only the table lock, so the enqueue has to come after the commit, not merely after the `LOCK TABLE`.
- A new slot also raises `schemaguard.SchemaRev` in `api/internal/cluster/schemaguard/guard.go`, besides the two that README rule 8 names. `TestSchemaRevIsTheSameInEveryService` says so.
