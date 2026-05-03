# Story 4.5 — Live VTT serving (read-side, contract only)

## Description

The Streaming Service renders live VTT directly from `transcript_segments`
(architecture §4.5). This story owns the **contract** the Pipeline must
honor, not the Streaming code itself (which lives in
[`02-api-streaming.md`](../../02-api-streaming.md)).

## Acceptance criteria

- A read-only SQL view `transcript_segments_v` is created with columns
  `(video_id, transcript_id, seq, start_sec, end_sec, text, speaker,
  is_active)` and an index on `(video_id, start_sec)`.
- Only segments whose parent transcript has `is_active = true` are
  visible through the view (the column is owned by
  [Epic 3 Story 3.5](../03-transcription/story-03-05-backend-registry.md)).
- Pipeline-side write paths never lock the view's rows for more than the
  duration of a single-segment transaction
  ([Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md)).
  This is guaranteed by row-level locks in Postgres and by SQLite's WAL
  mode.

## Test cases

- `test_view_excludes_superseded_transcripts` — two transcripts for
  one video, only one `is_active` → view returns only the active one's
  segments.
- `test_view_index_supports_window_query` — `EXPLAIN` of a
  `(video_id, start_sec BETWEEN x AND y)` query uses the index.

## Edge cases

- **Live read during the per-segment commit.** Because the segment
  insert and progress UPDATE share one transaction
  ([Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md)),
  readers see all-or-nothing.
- **Reader during a transcript flip.** `is_active = true` is the
  filter; the transcript flip transaction
  ([Epic 3 Story 3.5](../03-transcription/story-03-05-backend-registry.md))
  is atomic, so readers see either the old transcript or the new
  transcript, never both.
