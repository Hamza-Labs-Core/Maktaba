# Story 5.5 — Incremental indexing

## Description

The indexer must keep up with live transcription, not run as a giant
batch at the end.

## Acceptance criteria

- The `index` stage runs on a video as soon as `transcribe` reaches
  `done`. It indexes only the units whose `transcript_id` matches the
  newly-completed transcript and whose `unit.indexed_at IS NULL`. The
  partial index `transcript_units_indexed_at_null`
  ([Story 5.1](story-05-01-unit-chunking.md)) supports the claim query.
- Indexing also runs **incrementally during** transcription: a
  background "live indexer" task in the same worker subscribes to
  `LISTEN segments.committed` (Postgres) or polls every 5 s (SQLite),
  re-chunks any new segments into units, and writes them to FTS only
  (Chroma is deferred to the post-transcribe stage to amortize
  embedding cost). This makes search return live partial results
  while a long video is still transcribing.
- **Pause-aware chunking.** The live indexer **stops chunking** as soon
  as it observes `processing_jobs.state ∈ {paused, paused-pending,
  cancelled}` for the parent transcribe job. It only re-chunks once
  the job returns to `running`. This avoids producing a partial unit
  that grows across a resume seam (resolves REVIEW §4.7).
- Re-processing a video (model upgrade, settings change) creates a new
  active transcript and indexes it in place; the old transcript's units
  are deleted from FTS and Chroma in the same transaction that flips
  `is_active = false` on the old transcript
  ([Epic 3 Story 3.5](../03-transcription/story-03-05-backend-registry.md)).

## Test cases

- `test_live_indexer_updates_fts_during_transcribe` — start a long
  fixture; query for a phrase that appears 1 minute in → after the
  segment containing it commits, the FTS layer returns it within 10 s.
- `test_chroma_added_only_at_index_stage` — during live transcribe,
  Chroma collection size does not grow; after `index` stage, it
  contains all units.
- `test_live_indexer_halts_on_pause` — start a transcribe; mid-flight,
  set `pause_requested = true`; assert the live indexer's unit-count
  for that transcript stops advancing within one poll interval and
  does **not** emit a partial unit that straddles the pause boundary.
- `test_live_indexer_resumes_on_unpause` — continuing the above:
  resume the transcribe; the live indexer picks up at the next
  `last_segment_end_sec` and chunks forward.
- `test_reindex_replaces_old_transcript` — re-process; old units are
  removed from both indexes; new units present.

## Edge cases

- **Transcribe paused mid-video.** Live FTS indexing pauses naturally
  per the AC above; no special handling beyond the state observation.
- **Crash during live indexing.** `unit.indexed_at IS NULL` is the
  resume key; the indexer is idempotent.
- **A unit straddling a paused→resumed seam.** The pause-aware AC
  above prevents the live indexer from emitting a partial unit; on
  resume, the chunker re-reads the committed segments past
  `last_segment_end_sec` and produces well-formed units.
