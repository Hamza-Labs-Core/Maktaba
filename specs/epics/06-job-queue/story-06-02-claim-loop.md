# Story 6.2 — Claim loop

## Description

The atomic primitive every worker is built on.

## Acceptance criteria

- The Postgres claim is exactly the SQL in architecture §7.3 (single
  UPDATE with `SELECT … FOR UPDATE SKIP LOCKED`).
- The SQLite claim uses an asyncio lock + `BEGIN IMMEDIATE` to emulate
  `SKIP LOCKED`, with the same semantics: at most one worker holds any
  given job at any time.
- The claim returns either a fully-populated `Job` or `None` (no work).
- The claim accepts both `pending` and `paused` rows whose
  `pause_requested = false` — i.e., a "resume" is just a claim against a
  paused row. The worker disambiguates from `state` and walks into
  `resuming` accordingly.
- The claim respects `cancel_requested = false`, `not_before <= now()`,
  and `stage = ANY($supported_stages)`.
- A worker that just claimed sees `state='claimed'`, `claimed_by =
  worker_id`, `claimed_at = now()`, `attempts = attempts + 1`.
- The claim never blocks indefinitely; the worker's claim cadence is
  driven by either `LISTEN jobs.new` (Postgres; channel name
  standardized in [README](README.md)) or polling at `claim_poll_sec`
  (default 1 s; SQLite).

## Test cases

- `test_claim_atomic_under_contention` — start N=10 in-process workers
  against the same DB; enqueue 100 jobs → each job is claimed by
  exactly one worker; sum of claims = 100.
- `test_claim_respects_priority` — enqueue jobs at priority 100 then
  one at priority 50 → the 50 is claimed first.
- `test_claim_skips_not_before` — job with `not_before = now() + 60s`
  → not claimable until time has advanced.
- `test_claim_picks_up_paused_resume` — a paused row with
  `pause_requested = false` is returned by `claim()` and the worker
  transitions it to `resuming`.
- `test_claim_returns_none_when_empty` — no eligible rows → returns
  `None` without raising.
- `test_listen_jobs_new_wakes_claim_loop` — block the claim loop on
  `LISTEN jobs.new`; an `enqueue` from another connection wakes it
  within the test's deadline.

## Edge cases

- **A worker dies between `SELECT` and `UPDATE`.** Cannot happen: the
  claim is one atomic UPDATE; the SELECT is in its sub-query.
- **A row whose `cancel_requested = true` arrives at the front.** It
  is skipped by the WHERE; cancellation is enacted by the cancel
  responder ([Story 6.4](story-06-04-pause-resume-cancel.md)), not by
  the claim loop.
- **Stage filter mismatch.** A worker that supports only `transcribe`
  never claims `index` jobs even if priority would otherwise win.
