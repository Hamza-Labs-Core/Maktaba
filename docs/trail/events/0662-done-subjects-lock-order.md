---
schema: 1
kind: pr
pr: 662
title: processing_done_subjects checks its lock order across a transaction
date: 2026-11-06
session: f0e36dc6-64c3-44f2-980b-fac44d831b70
issue: 642
---

## What happened
- Slot 0184 makes `processing_done_subjects_touch` remember the highest summary key its transaction has locked (transaction-local `maktaba.done_subjects_key`). A later statement locking a lower key raises under `maktaba.lock_order_strict = on` (set on the pipeline's test databases and the integration job's role) and only warns otherwise. A transaction holding the table (SHARE ROW EXCLUSIVE or stronger) may take any order; only an out-of-order touch reads `pg_locks`.
- Run under strict mode, the Go integration suite found nothing; the pipeline's Postgres suite found one real caller the issue had missed: the MediaIntel import writes its synthetic done jobs stage after stage in one long transaction (14 import tests failed). The import now locks the summary table for its run.

## What we learned
- "Nothing does this today" (the issue) was not true: making the rule fail in the tests was what found the importer. A guard that only warns would have kept it hidden.
- The comparison must be a row comparison `(library_id, stage, subject_id) < (...)`, the same as the triggers' `ORDER BY`, not a concatenated string: the en_US collation ignores punctuation, so a joined key sorts differently.
- `ALTER DATABASE ... SET` on a template does not reach databases cloned from it: the pipeline fixture sets the strict mode on each test database, and CI sets it on the role.

## Keep doing
- Turn a "should never happen" rule into a test-time error, and run the whole suite under it before trusting the claim.
