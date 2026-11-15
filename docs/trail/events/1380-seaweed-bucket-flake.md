---
schema: 1
kind: pr
pr: 1380
title: "Cloud rehearsal: recover a SeaweedFS bucket cached as missing"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1380 merged as 1837f77e2. The cloud rehearsal's bucket scenarios (db-nodes-migrate,
  db-nodes-failover, restore-pitr) intermittently timed out waiting for a backup bucket.
  SeaweedFS 3.97's gateway reads the bucket directory when a bucket is made; if that read comes
  first, it caches the bucket as missing for 15 minutes and every write fails. Main's passing
  runs logged the same error and only got out when a later delete-and-remake won a race with the
  storage heartbeat; the delete could also wedge the bucket's volumes.
- `bucketReady` now makes the bucket once (409 counts as made), retries only the probe object,
  and after 15 s of failure restarts that repository's SeaweedFS container (at most twice),
  never deleting the bucket. Each changed reason is logged. CI run 43669089486 exercised it:
  two scenarios needed one restart per bucket, which cleared it.

## What we learned
- The first fix (stop deleting) passed its unit fake but failed three scenarios in CI: a fake
  only proves what it models. Log the real failure reason in the readiness loop before fixing.
- Raising a timeout would not have helped: the cached-missing state lasts 15 minutes.
