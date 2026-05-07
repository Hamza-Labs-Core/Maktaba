# Story 3.8 — Crash recovery & graceful shutdown

## Description

Make the worker survive `kill -9`, OOM-killer, host reboot, and `SIGTERM`
on a 4-hour job without losing more than the in-flight ≤30 s segment.

## Acceptance criteria

- On `SIGTERM` / `SIGINT`, the worker treats it as `pause_requested`
  for every job it holds, with `paused_reason = 'shutdown'`. Each
  affected job commits the current segment (if any), flips to `paused`,
  and the process exits within `shutdown_grace_sec` (default 120 s).
- A second `SIGTERM` / second Ctrl-C aborts immediately with the same
  correctness guarantee (the in-flight segment was uncommitted, so the
  DB is consistent).
- On crash (`SIGKILL`, panic, host reboot), the reaper
  ([Epic 6 Story 6.6](../06-job-queue/story-06-06-reaper.md)) finds
  jobs whose `last_heartbeat_at < now() - stale_claim_sec` (default
  90 s) and flips them to `paused` with `paused_reason = 'crash'`,
  `paused_at_sec = last_segment_end_sec`. They are then claimable as
  resumes by any worker.
- A "chaos" pytest fixture randomly `SIGKILL`s the worker mid-job;
  after restart, the resulting transcript matches the
  no-crash baseline byte-for-byte (or within ε for non-deterministic
  backends).

## Test cases

- `test_sigterm_pauses_all_jobs` — start two transcribe jobs; SIGTERM
  → both rows in `paused` with reason `shutdown` within
  `shutdown_grace_sec`.
- `test_double_sigterm_aborts_fast` — second SIGTERM forces exit < 5 s.
- `test_reaper_pauses_stale_claim` — claim a job; freeze its
  heartbeats; advance simulated clock past `stale_claim_sec` → reaper
  flips it to `paused` with reason `crash`.
- `test_chaos_kill_yields_consistent_resume` — kill -9 the worker N
  times during a fixture transcribe; final segment count matches the
  no-kill run; no duplicate `seq`s.

## Edge cases

- **Reaper races a recovering worker.** Both attempt to mutate the same
  job. The reaper's UPDATE includes `WHERE last_heartbeat_at < now() -
  stale_claim_sec`; a heartbeating worker invalidates the predicate, so
  the UPDATE matches zero rows and the worker keeps the claim.
- **Wall-clock skew.** All times are server-side `now()`; workers never
  send wall-clock timestamps for the heartbeat. A workstation whose
  clock jumped backward cannot fool the reaper into thinking heartbeats
  are still fresh.
