# Story 3.6 — Real-time per-segment durable commit

## Description

The hot path: every segment is committed atomically with a job-progress
update before the worker advances. This is the core of pause/resume.

## Acceptance criteria

- For each segment `s` produced by the backend, the worker executes a
  single DB transaction that:
  1. Inserts a `transcript_segments` row with monotonic `seq`,
     `start_sec`, `end_sec`, `text`, optional `speaker`, `confidence`.
  2. Updates `processing_jobs` setting
     `last_segment_end_sec = s.end`,
     `processed_seconds = processed_seconds + (s.end - prev_end)` (where
     `prev_end` is the previous `last_segment_end_sec`),
     `segments_completed = segments_completed + 1`,
     `realtime_factor = ewma(prev, audio_sec_in_segment / wall_sec)`,
     `estimated_remaining_sec = (total - processed) /
     max(realtime_factor, ε)`,
     `progress_updated_at = now()`,
     `last_heartbeat_at = now()`.
  3. (Optional) inserts `transcript_words` rows when word timestamps
     are enabled.
- Both writes are **committed together**; if either fails, the
  transaction rolls back and the worker retries the same segment.
- After every committed segment, before pulling the next one off the
  backend, the worker checks `pause_requested` and `cancel_requested`
  in the same connection and exits cleanly if either is set
  (`architecture §7.6`).
- After every committed segment, the worker emits a `LISTEN
  segments.committed` notify with `{transcript_id, last_segment_end_sec,
  seq}` so the live indexer
  ([Epic 5 Story 5.5](../05-search-indexing/story-05-05-incremental-indexing.md))
  can advance.
- The post-commit invariant
  `last(transcript_segments.end_sec) == processing_jobs.last_segment_end_sec`
  holds at every consistent read.

## Test cases

- `test_segment_commit_atomic` — inject a failure on the
  `processing_jobs` UPDATE → `transcript_segments` row is also rolled
  back; on retry, the row appears exactly once.
- `test_progress_advances_with_audio_time_not_wall_time` — synthetic
  backend yields a 60 s segment instantly → `processed_seconds`
  increments by 60, not by the (sub-second) wall time.
- `test_realtime_factor_ewma` — feed alternating fast/slow segments;
  assert `realtime_factor` is smoothed (α = 0.2) and the visible
  series is monotonically tracking the input mean.
- `test_eta_uses_smoothed_factor` — eta is consistent with
  `(total - processed) / realtime_factor` to two decimal places.
- `test_pause_request_observed_after_commit` — set `pause_requested =
  true` mid-decode → exactly one more segment commits, then the worker
  exits to `paused` with `paused_at_sec == that segment's end_sec`.

## Edge cases

- **Segment shorter than the prior `last_segment_end_sec`.** The backend
  produced an out-of-order segment; the orchestrator's reorder buffer
  ([Story 3.1](story-03-01-backend-protocol.md)) suppresses the commit
  until earlier segments arrive. If buffering exceeds
  `reorder_window_sec` (default 30 s), the offending segment is dropped
  with a WARN.
- **Backend emits a "final" segment past the audio's true end.** The
  orchestrator clamps `end_sec` to `min(end_sec, audio_duration)` to
  keep `processed_seconds <= total_duration_seconds`.
- **DB write contention with the API's read traffic.** The progress
  UPDATE uses the `(id)` PK only and is therefore O(1); no risk of
  contention beyond a single row lock.
