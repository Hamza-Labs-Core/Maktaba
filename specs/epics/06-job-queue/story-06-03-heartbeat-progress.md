# Story 6.3 — Heartbeat & progress

## Description

The worker proves it's alive by writing to the same row it claimed. This
story owns the heartbeat cadence, the progress UPDATE, and the two
notify channels (`jobs.progress` and `jobs.heartbeat`) that consumers
read.

> **Channel naming.** Consistent with the standardization in
> [README](README.md), the channels are **plural**: `jobs.progress` and
> `jobs.heartbeat`. The retired singular names (`job.progress`,
> `job.heartbeat`) must not appear in code.

## Acceptance criteria

- While running, the worker calls `tick(job_id, processed_seconds_delta,
  segments_completed_delta, last_segment_end_sec, realtime_factor,
  estimated_remaining_sec)` after every committed segment
  ([Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md)).
  The function executes a single UPDATE that bumps progress and sets
  `last_heartbeat_at = now()`, `progress_updated_at = now()`.
- For non-transcribe stages (probe, index, thumbnail) that don't have
  natural segment cadence, the worker emits a pure heartbeat tick every
  `heartbeat_sec` (default **5 s**) that updates only
  `last_heartbeat_at`.
- A `LISTEN jobs.progress` channel fires on each progress UPDATE; the
  payload is `{id, video_id, stage, state, last_segment_end_sec,
  processed_seconds, total_duration_seconds, realtime_factor,
  estimated_remaining_sec}` exactly per architecture §7.10.
- Pure heartbeat ticks do **not** fire `jobs.progress` (that's reserved
  for actual progress); they fire `jobs.heartbeat` instead, consumed
  only by the reaper, not by the UI.

## Test cases

- `test_progress_updates_visible` — call `tick`; subscribe to
  `LISTEN jobs.progress` → exactly one notification with matching
  payload.
- `test_heartbeat_only_does_not_emit_progress` — call the heartbeat-
  only path → no `jobs.progress` notification, but
  `last_heartbeat_at` advanced and exactly one `jobs.heartbeat`
  notification fired.
- `test_progress_payload_shape` — payload JSON matches the schema
  in §7.10 byte-for-byte.
- `test_no_legacy_singular_channel_names_in_code` — grep
  `pipeline/` for `'job.progress'`, `'job.heartbeat'`, `'job.pending'`,
  `'job.reaped'`, `'job.force_pause'` → zero hits (use word
  boundaries to allow comments referencing the retired names).

## Edge cases

- **Stage that completes faster than the heartbeat interval.** No
  problem; the next tick is the completion UPDATE that flips state to
  `done`.
- **A long-running pure-CPU step inside one stage** (e.g., a 60 s
  ffmpeg decode for a single segment). The pure heartbeat path
  guarantees liveness even when the per-segment commit cadence
  exceeds `stale_claim_sec`.
