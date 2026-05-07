# Story 6.10 — Single source of truth for resume

## Description

Capture the central correctness invariant — `last_segment_end_sec` is
the resume offset, never wall clock, never file mtime, never a
JSON sidecar.

## Acceptance criteria

- The DB constraint
  `last_segment_end_sec >= 0 AND last_segment_end_sec <=
  COALESCE(total_duration_seconds, last_segment_end_sec)` is enforced
  by a CHECK constraint on `processing_jobs`.
- A migration test asserts no other table has a column named
  `*_resume_offset` or similar — an architectural smoke test that the
  invariant isn't accidentally duplicated.
- A property test runs across crash/resume cycles for synthetic
  workloads and asserts: at every consistent read,
  `last(transcript_segments WHERE transcript_id = T) .end_sec
  == processing_jobs.last_segment_end_sec`.
- The runner refuses to write to a sidecar file as a checkpoint;
  attempts to do so fail a unit test that lints `pipeline/`.

## Test cases

- `test_invariant_after_crash_resume` — chaos-kill loop
  ([Epic 3 Story 3.8](../03-transcription/story-03-08-crash-recovery.md));
  invariant holds at each restart.
- `test_no_sidecar_checkpoint_files` — grep `pipeline/` for
  `partial.json`, `checkpoint`, `_resume` patterns → none.

## Edge cases

- **A backend that emits an end past `total_duration_seconds`.** The
  per-segment commit clamps as described in
  [Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md)'s
  edge cases; the invariant still holds.
- **A migration that adds new columns.** The CHECK constraint stays
  pinned to `last_segment_end_sec`; new resume-related state must be
  proven derivable from it or rejected in code review.
