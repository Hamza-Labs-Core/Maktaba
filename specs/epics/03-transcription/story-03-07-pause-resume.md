# Story 3.7 — Pause and resume to the exact second

## Description

The user's most-demanded feature: pause a 4-hour transcribe, walk away,
resume exactly where it stopped — across process restarts and host
reboots.

## Acceptance criteria

- **Given** a running transcribe at `last_segment_end_sec = 1234.5`,
  **when** the user calls `POST /api/jobs/{id}/pause`,
  **then** the API sets `pause_requested = true`; within one segment
  boundary the worker commits the current segment and flips state to
  `paused`, recording `paused_at_sec = last_segment_end_sec`,
  `paused_reason = 'user'`, and releasing the GPU lock.
- **Given** the same paused job,
  **when** the user calls `POST /api/jobs/{id}/resume`,
  **then** the job becomes claimable; the next worker that picks it up
  flips state to `resuming`, opens the audio decoder seeked to
  `last_segment_end_sec` ([Story 2.3](../02-audio-extraction/story-02-03-stream-extraction.md)),
  rebuilds the Whisper prompt from the last K segments' text (default
  K=3), and flips to `running`. The next emitted segment's `start_sec
  >= last_segment_end_sec`.
- **Given** a paused job whose original backend is no longer available,
  **when** the user resumes,
  **then** resume succeeds with the fallback backend; the
  `transcripts` row records `metrics.resumed_with_different_backend =
  {from, to, at_sec}`.
- **Given** force-pause via `POST /api/jobs/{id}/pause?force=true`,
  **when** the worker is stuck on a single long segment,
  **then** the job flips to `paused` immediately with `paused_at_sec =
  last_segment_end_sec` (the in-flight segment is discarded; no commit
  was attempted yet) and the worker is signalled to abort its
  subprocess.

## Test cases

- `test_resume_starts_from_last_segment_end_sec` — pause at 600.0; resume
  → first new segment's start is ≥ 600.0 and within 0.5 s of it.
- `test_resume_across_process_restart` — pause; kill the worker process;
  start a new worker; the job is reclaimed and resumes with no rework.
- `test_resume_after_backend_change` — pause on `whisper-mlx`; change
  library setting to `whisper-cuda`; resume → succeeds, transcript
  metadata captures the change.
- `test_force_pause_drops_inflight` — start a synthetic backend that
  hangs in a single segment; force-pause → state becomes `paused`
  within 1 s, no segment was committed for the in-flight chunk.
- `test_double_resume_is_idempotent` — resume on a paused job twice
  rapidly → exactly one worker claim succeeds; the second returns 200
  with the unchanged state.

## Edge cases

- **Audio file moved between pause and resume.** The video's
  `content_hash` resolves the new path before extraction reopens the
  file; if the file is gone, the resume claim fails the job back to
  `pending` with `error.kind = "audio_missing"` and `not_before = +5m`.
- **Whisper prompt seam glitch.** Some Arabic recitations re-detect a
  different language at the resume boundary because the first 30 s
  after the seek are in mid-sentence. The orchestrator reuses the
  pre-pause `transcripts.language` and disables auto-detect on resume.
- **Crash mid-segment commit.** No segment row is partially committed
  (transaction atomicity); on resume, the worker rebuilds from the
  same `last_segment_end_sec` it saw before. Verified by the chaos
  test in [Story 3.8](story-03-08-crash-recovery.md).
