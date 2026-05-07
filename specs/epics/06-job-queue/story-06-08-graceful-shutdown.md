# Story 6.8 — Graceful shutdown semantics

## Description

The whole queue layer must shut down cleanly on `SIGTERM` so that no
running job is forgotten in `claimed`/`running` state.

## Acceptance criteria

- On signal, the worker:
  1. Sets a global `shutdown_requested` event.
  2. Stops the claim loop (no new claims).
  3. Sets `pause_requested = true` for every job it currently holds, in
     a single UPDATE keyed on `claimed_by = $worker_id`.
  4. Waits up to `shutdown_grace_sec` (default 120 s) for those jobs to
     reach `paused` (the per-segment check in
     [Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md)
     sees the flag and transitions cleanly).
  5. If any job is still not paused after the grace period, force-pauses
     it (the same effect as `?force=true`, see
     [Story 6.4](story-06-04-pause-resume-cancel.md)) and exits.
- The reaper's existence ([Story 6.6](story-06-06-reaper.md)) guarantees
  safety even if force-pause fails: the next reaper interval sweeps any
  orphaned claims to `paused` with `paused_reason = 'crash'`.
- Tests use a real `SIGTERM` (subprocess) plus a synthetic backend so
  that the assertion is end-to-end.

## Test cases

- `test_shutdown_pauses_all_claims` — start workers with two running
  jobs; SIGTERM → both become `paused` with reason `shutdown` within
  grace.
- `test_shutdown_force_pauses_after_grace` — synthetic backend that
  ignores pause; SIGTERM with grace 5 s → after 5 s, jobs forced to
  `paused`.
- `test_no_orphan_after_kill_minus_nine` — `kill -9` the worker; assert
  reaper sweeps within `stale_claim_sec`.

## Edge cases

- **Two `SIGTERM`s in quick succession.** The second forces an
  immediate exit (architecture §7.8); reaper is responsible for
  cleanup. Verified.
- **Container orchestrator's TERM-then-KILL window shorter than
  `shutdown_grace_sec`.** Document that operators should set the
  Compose `stop_grace_period` to ≥ `shutdown_grace_sec + 30s` or
  accept the reaper-driven path.
