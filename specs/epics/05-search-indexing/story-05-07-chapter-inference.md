# Story 5.7 — Chapter inference from transcripts

## Description

> **Resolves REVIEW §2.7.a and §3.1.** Architecture §4.6 promises
> "inferred chapters from transcript-level topic shifts (cosine drop
> between adjacent segment embeddings > threshold)" — but no story
> previously owned the inference. The Streaming Service's chapter
> *delivery* is covered in `02-api-streaming.md` Story 8.12; this story
> owns the *production* of the data Streaming serves.

Generate chapter boundaries by detecting topic shifts in the embedding
stream. Output is written to a `chapters` table that the Streaming
Service reads when assembling HLS DATERANGE tags and the
`chapters.json` resource (architecture §4.6).

## Stage placement

`chapter_infer` is a sub-stage that runs at the tail of `index`
([Story 5.5](story-05-05-incremental-indexing.md)) once the full
transcript's units are present in Chroma. It does **not** introduce a
new top-level stage in `processing_jobs.stage` (we keep the canonical
seven-stage enum from
[Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md));
it is part of `index`. This keeps the per-video stage count stable and
avoids a new state-transition.

## Schema

A migration `shared/db/migrations/000X_chapters.sql` creates:

```sql
CREATE TABLE chapters (
    id            BIGSERIAL PRIMARY KEY,
    video_id      BIGINT NOT NULL REFERENCES videos(id)
                              ON DELETE CASCADE,
    transcript_id BIGINT NOT NULL REFERENCES transcripts(id)
                              ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    start_sec     REAL NOT NULL,
    end_sec       REAL NOT NULL,
    title         TEXT,                         -- optional, see AC
    confidence    REAL NOT NULL,                -- 0..1, the strength of the topic shift
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (transcript_id, seq)
);
CREATE INDEX chapters_video_start
    ON chapters (video_id, start_sec);
```

Only chapters tied to the **active** transcript
([Epic 3 Story 3.5](../03-transcription/story-03-05-backend-registry.md))
are surfaced by the API; reprocessing a transcript replaces the chapters
in the same transaction that flips `is_active`.

## Acceptance criteria

- After `index` finishes for a transcript, the inference computes
  cosine distance between adjacent unit embeddings (already cached in
  Chroma, no re-embedding required) and emits a chapter boundary
  wherever the distance exceeds `topic_shift_threshold` (default
  `0.35`, configurable per library).
- Each detected chapter is recorded with `seq`, `start_sec` (the start
  of the unit at the boundary), `end_sec` (just before the next
  boundary or end-of-video), and `confidence` (the cosine distance at
  the boundary, clamped to `[0, 1]`).
- `title` is left `NULL` in v1; an offline batch job (deferred) may
  later fill it from a summarization pass. The serving path tolerates
  `NULL` titles by falling back to "Chapter N".
- A configurable minimum chapter length `min_chapter_sec` (default
  `60`) suppresses noisy chapters: if two boundaries are closer than
  this, only the higher-confidence one is kept.
- Chapter inference is **opt-in per library** via
  `library.settings.infer_chapters` (default `true` for libraries with
  `content_type ∈ {lecture, sermon, podcast}`, `false` otherwise).
- Failure of chapter inference is logged but does **not** fail the
  parent `index` job; the video proceeds to `INDEXED` regardless.

## Test cases

- `test_migration_creates_chapters_table` — apply migration; assert
  table and indexes present, FKs correct.
- `test_inference_detects_known_topic_shift` — fixture transcript with
  three known topics → inference produces ≥3 chapters whose
  `start_sec` is within 5 s of the expected boundaries.
- `test_min_chapter_sec_suppresses_close_boundaries` — fixture where
  the embedding distance spikes within 30 s twice → only one chapter
  emitted.
- `test_threshold_respected` — fixture with all distances below
  threshold → zero chapters; the `index` job still finishes.
- `test_reindex_replaces_chapters` — re-process a video with a new
  transcript → old chapters are deleted in the same transaction that
  flips `is_active = false` on the previous transcript; new chapters
  are inserted.
- `test_disabled_per_library_skips_inference` —
  `infer_chapters = false` → no chapters table writes; the inference
  function is not even called.
- `test_inference_failure_does_not_fail_index_job` — inject a fault in
  the cosine-distance computation; the parent `index` job still
  reaches `done`; the failure is logged with `kind=chapter_infer_failed`.

## Edge cases

- **Very short videos** (< `min_chapter_sec`). Zero chapters are
  produced; that's correct, and the serving path renders a single
  "no chapters" entry.
- **Highly fragmented transcripts** (many short units). The threshold
  is computed against per-unit embeddings; a denser unit count means
  more candidate boundaries, but `min_chapter_sec` still applies.
- **Transcript reprocess while chapters are being inferred.** The
  transaction that flips `is_active` first deletes the in-progress
  chapter rows for the old transcript. The new transcript's
  `chapter_infer` runs after its `index` reaches `done`.
- **Library deletion.** `ON DELETE CASCADE` on `video_id` and
  `transcript_id` ensures rows clean up alongside their parents.
