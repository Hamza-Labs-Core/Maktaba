---
schema: 1
kind: pr
pr: 524
title: Extract paused by nobody, thumbnail starved: pauses need an owner, pools share fairly
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- A read-only look at the live home: extract (`nas_read`) had `paused = true` with `paused_at`/`paused_by` NULL since 2026-10-22 22:31. Thumbnail (70k pending at priority 900) had not run because thumb_small and chapter_thumbs (about 42k at 100) always filled `nas_read`'s 2 slots under `ORDER BY priority, id`.
- Writer: no code; every pause writer in git history sets both columns. It was a Claude session (`30850266`) on 2026-10-23 02:37 UTC, after a request to pause it: a raw `psql` `update job_stages set paused = true where stage = 'extract'` on the live Docker Postgres. Later that night the same session did the same for `index` and `chapter_infer` on the bundled Postgres.
- Fix (migration 0172, `0172_stage_pause_owner_fair_claim`): orphaned pauses are resumed, with `audit_log` `admin` / `queue.stage_orphan_pause_resumed`; a trigger refuses a pause without `paused_at` and `paused_by` (a trigger, not a CHECK, because `ADD CONSTRAINT` would make the release unsafe to roll back on its schema); the Queues API adds `paused_by_name`, and the web card and queue page say who paused a stage and when (English and Arabic).
- Fairness: stride scheduling per pool (`claim_shares`, `engine/claim.py`) between priority classes, not stages. Each stage's head is due at `GREATEST(class pass + priority, pool clock)`, and the claim takes the head due first. Equal priorities stay in id order across stages. Priority 50 or less (Process now) still goes first, strictly. 900 vs 100 gets 1 claim in 9. The new partial index `processing_jobs_claimable_idx` took a claim with 203k claimable rows from 64 ms to 0.3-0.8 ms. Legacy `claim_one` (non-pool mode) is unchanged: it has a semaphore per stage, so no cross-stage starvation.
- Live state was not changed by this session. Extract resumes when a release with 0172 is installed; `index` and `chapter_infer` are resumed too if still hand-paused.

## What we learned
- A first cut kept a share per stage. It round-robined equal stages, split fp_gate's batch and ran detect 4 times (`test_fingerprint_flow` seed 2, CI integration). Shares belong to priority classes.
- A CHECK constraint added by `ADD CONSTRAINT` makes a release unsafe to roll back on its schema; a trigger does not.

## Stop doing
- Writing operator columns by hand with raw SQL on the live database. Pause through `POST /api/queues/{stage}/pause` (the DB now refuses anything else).
