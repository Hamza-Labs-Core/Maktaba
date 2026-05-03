# Story 6.4 — Pause, resume, cancel via request flags

## Description

Control plane: API only sets request flags, never mutates live state.

## Acceptance criteria

- `POST /api/jobs/{id}/pause` (handled by API but the contract is here)
  sets `pause_requested = true` and emits `NOTIFY jobs.flag_set` with
  payload `{id, flag: 'pause'}`. Returns 200 with the job's current
  state. The endpoint is idempotent.
- `POST /api/jobs/{id}/pause?force=true` additionally executes
  `UPDATE … SET state = 'paused', paused_at = now(), paused_at_sec =
  last_segment_end_sec, pause_requested = false, claimed_by = NULL
  WHERE state IN ('claimed','running','resuming')` and signals the
  worker via `NOTIFY jobs.force_pause` with payload `{id}` to abort
  its subprocess. (Channel name standardized in [README](README.md).)
- `POST /api/jobs/{id}/resume` sets `pause_requested = false` and, if
  the row was in `paused`, leaves it claimable. Emits `NOTIFY
  jobs.flag_set` with payload `{id, flag: 'resume'}`. (No state change
  here; the next claim does the transition.)
- `POST /api/jobs/{id}/cancel` sets `cancel_requested = true` and emits
  `NOTIFY jobs.flag_set` with payload `{id, flag: 'cancel'}`. The
  worker observes this on the next per-segment check
  ([Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md))
  and flips state to `cancelled`.
- The worker's per-segment check uses one cheap query: `SELECT
  pause_requested, cancel_requested FROM processing_jobs WHERE id =
  $1` (uses PK index, < 1 ms).

## Test cases

- `test_pause_request_is_idempotent` — call pause twice → same
  response, single state transition, two `jobs.flag_set` notifies (one
  per call).
- `test_force_pause_drops_inflight` — covered in
  [Epic 3 Story 3.7](../03-transcription/story-03-07-pause-resume.md).
- `test_force_pause_emits_jobs_force_pause` — call `?force=true` →
  exactly one `NOTIFY jobs.force_pause` payload received with the
  job id.
- `test_resume_does_not_mutate_state_directly` — resume on a paused
  job → `state` remains `paused` until a worker claims it.
- `test_cancel_after_pause_is_consistent` — cancel a paused job → state
  becomes `cancelled`, no orphaned worker references.
- `test_pause_observed_within_one_segment_window` — synthetic 1 s
  segments; set pause; assert ≤ 2 segments commit before the worker
  exits (timing tolerance for race after the check).

## Edge cases

- **Pause requested before claim.** A `pending` row with
  `pause_requested = true` is excluded from the claim WHERE; effectively
  it's "shelved" and only resume clears the flag.
- **Cancel requested mid-resume context-rebuild.** The `resuming`
  state's setup phase periodically polls cancel and aborts cleanly,
  flipping to `cancelled` from `resuming`.
