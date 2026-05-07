# Story 6.6 — Reaper for crashed claims

## Description

A worker that died holding a `claimed`/`running`/`resuming` row must
release it.

> **Resolves REVIEW §1.4.c.** The original story's comment said
> `90 = 3× the 30 s heartbeat`. The actual heartbeat across all stories
> in this epic and Epic 3 is **5 s**, so the ratio is 18×, not 3×. The
> comment is corrected here.

## Acceptance criteria

- A reaper task runs every `reaper_interval_sec` (default 30 s) and
  executes:

  ```sql
  UPDATE processing_jobs
     SET state = 'paused',
         paused_at = now(),
         paused_at_sec = last_segment_end_sec,
         paused_reason = 'crash',
         claimed_by = NULL,
         pause_requested = false
   WHERE state IN ('claimed', 'running', 'resuming')
     AND last_heartbeat_at < now() - $stale_claim_sec
   RETURNING id;
  ```

  Default `stale_claim_sec = 90` (90 s = **18× the 5 s heartbeat**;
  see [Story 6.3](story-06-03-heartbeat-progress.md) for the heartbeat
  cadence). Eighteen missed heartbeats is the threshold for "the worker
  is definitely dead, not just slow."
- For each reaped id, the reaper emits `NOTIFY jobs.reaped` with
  `{id, prev_state, paused_at_sec}`. (Channel name standardized in
  [README](README.md).) The API surfaces this in the job's history.
- The reaper is **per-instance** but uses a `pg_advisory_lock` to
  prevent multiple Pipeline workers from running it simultaneously
  (only one runs at a time per DB).
- The reaper never reaps `done`, `failed`, `paused`, `cancelled`, or
  `pending` rows — only the live-claim states.

## Test cases

- `test_reaper_pauses_stale_claim` — covered in
  [Epic 3 Story 3.8](../03-transcription/story-03-08-crash-recovery.md)
  from the worker side; here, assert the SQL pattern with synthetic
  clock.
- `test_reaper_skips_fresh_heartbeats` — claim with `last_heartbeat_at
  = now()` → reaper's UPDATE matches zero rows.
- `test_reaper_advisory_lock` — start two reaper tasks; only one
  acquires the lock; the other returns immediately.
- `test_reaper_emits_jobs_reaped_notify` — listener on `jobs.reaped`
  catches one notify per reaped row with the documented payload.
- `test_stale_claim_sec_default_matches_heartbeat_ratio` — assert
  `stale_claim_sec == 18 * heartbeat_sec` (5 s × 18 = 90 s); a
  config change to one without the other fails the assertion.

## Edge cases

- **Clock skew between client and server.** Reaper compares server-
  side `now()` against server-side `last_heartbeat_at`; client clocks
  are irrelevant.
- **A worker that revives just as the reaper runs.** The UPDATE's
  WHERE filters by `last_heartbeat_at < now() - stale_claim_sec`; a
  recent heartbeat invalidates the predicate and the row is left alone.
  If both happened (heartbeat and reap) in the same millisecond, only
  one wins per row-level lock; the worker, seeing its row mutated to
  `paused`, exits cleanly.
