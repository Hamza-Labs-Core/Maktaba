# Story 5.1 — Search-unit chunking & schema

## Description

Whisper segments are typically 5–30 s and may be a fragment of a sentence;
embeddings work better on small coherent units (~200 chars). The indexer
re-chunks segments into "search units" before writing.

> **Resolves REVIEW §1.1.h.** This story is the single migration owner
> of the `transcript_units` table that other stories reference but
> `architecture.md §8` does not define.

## Acceptance criteria

- A migration `shared/db/migrations/000X_transcript_units.sql` creates
  the table:

  ```sql
  CREATE TABLE transcript_units (
      id            BIGSERIAL PRIMARY KEY,
      transcript_id BIGINT NOT NULL REFERENCES transcripts(id)
                                ON DELETE CASCADE,
      seq           INTEGER NOT NULL,
      start_sec     REAL NOT NULL,
      end_sec       REAL NOT NULL,
      text          TEXT NOT NULL,
      language      TEXT NOT NULL,         -- ISO 639-1 from the parent transcript
      segment_ids   JSONB NOT NULL,        -- ordered list of source segment ids
      indexed_at    TIMESTAMPTZ,
      metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
      created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
      UNIQUE (transcript_id, seq)
  );
  CREATE INDEX transcript_units_lang
    ON transcript_units (language);
  CREATE INDEX transcript_units_indexed_at_null
    ON transcript_units (transcript_id)
    WHERE indexed_at IS NULL;
  ```

  The `transcript_units_lang` index resolves REVIEW §6.3 (filter
  pushdown for `language`); the partial `_indexed_at_null` index
  supports the live indexer's incremental claim
  ([Story 5.5](story-05-05-incremental-indexing.md)).
- **Given** a transcript's segments,
  **when** the indexer runs,
  **then** it produces "search units" each containing 1–3 consecutive
  sentences with target ~200 characters and hard cap 400. A unit's
  `start_sec` is the first segment's `start`, its `end_sec` is the last
  segment's `end`.
- **Given** a segment that itself contains multiple sentences,
  **when** chunked,
  **then** each sentence becomes its own unit; segment boundaries are
  not load-bearing — the indexer reads `text` and re-segments by
  punctuation, then maps each new unit back to the segment(s) it
  derived from.
- **Given** an Arabic transcript,
  **when** chunking,
  **then** sentence boundaries are detected on `[.!?؟।]` plus
  newline; trailing whitespace stripped; combining marks preserved.
- The mapping `unit → list[segment_id]` is stored in
  `transcript_units.segment_ids` (JSONB) so a search hit always resolves
  back to a precise segment timestamp. The list is **ordered by
  segment seq**; consumers that need a single representative segment
  use `segment_ids[0]` (see
  [Story 5.4](story-05-04-hybrid-rrf.md) for the resolution rule).

## Test cases

- `test_migration_creates_table_and_indexes` — apply migration; assert
  table exists, both named indexes present, `UNIQUE (transcript_id,
  seq)` enforced.
- `test_chunking_target_length` — long fixture → distribution of unit
  lengths has median in `[150, 250]` chars and 99p ≤ 400.
- `test_chunking_arabic_punctuation` — fixture using `؟` →
  sentence break occurs there.
- `test_unit_to_segment_mapping_recovers_timestamps` — pick any unit;
  resolve its `segment_ids[0]` → that segment's `start_sec` equals the
  unit's `start_sec`.
- `test_chunking_does_not_drop_text` — concatenation of all unit texts
  (after de-NFC, preserving sentence joins) equals the concatenation of
  segment texts byte-for-byte.

## Edge cases

- **A single "sentence" longer than the cap.** Split at the nearest
  word boundary ≤ cap; record `metadata.split_method = "word"`.
- **No punctuation at all** (rare; bad STT output). The whole transcript
  is chunked by character count along word boundaries with target 200;
  `metadata.no_punctuation = true` for triage.
- **Cascade on transcript delete.** `ON DELETE CASCADE` removes all
  units for a deleted transcript; both FTS layer
  ([Story 5.2](story-05-02-fts-tsvector.md)) and Chroma
  ([Story 5.3](story-05-03-chroma-vector.md)) react to the deletion
  via their own incremental sync paths.
