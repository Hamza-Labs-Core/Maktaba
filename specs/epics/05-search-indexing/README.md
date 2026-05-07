# Epic 05 — Search & Indexing

**Goal.** Make every transcribed second searchable in two complementary
ways — exact-phrase / proximity (Postgres `tsvector` or SQLite FTS5) and
semantic (ChromaDB) — and fuse them into one ranked result set with
language filters, deep-linkable timestamps, and snippet highlighting that
gets Arabic right.

**Owner.** Pipeline Service writes to both indexes
(`pipeline/src/maktaba_pipeline/search/`); the API Service reads (and
proxies semantic queries to Pipeline via gRPC `Embed`).

**Out of scope.** Saved searches and search analytics (API surface only,
covered in [`02-api-streaming.md`](../../02-api-streaming.md));
cross-language *translation* of queries (deferred per architecture
Appendix B; cross-language *retrieval* is in scope through the
multilingual embedding).

## Stories

| # | Title | File |
|---|-------|------|
| 5.1 | Search-unit chunking & schema | [story-05-01-unit-chunking.md](story-05-01-unit-chunking.md) |
| 5.2 | FTS5 / `tsvector` exact-phrase index (unit-backed) | [story-05-02-fts-tsvector.md](story-05-02-fts-tsvector.md) |
| 5.3 | ChromaDB vector index | [story-05-03-chroma-vector.md](story-05-03-chroma-vector.md) |
| 5.4 | Hybrid retrieval with Reciprocal Rank Fusion | [story-05-04-hybrid-rrf.md](story-05-04-hybrid-rrf.md) |
| 5.5 | Incremental indexing | [story-05-05-incremental-indexing.md](story-05-05-incremental-indexing.md) |
| 5.6 | Search query suggestions | [story-05-06-query-suggestions.md](story-05-06-query-suggestions.md) |
| 5.7 | Chapter inference from transcripts | [story-05-07-chapter-inference.md](story-05-07-chapter-inference.md) |

## Resolved cross-doc issues

- **REVIEW §1.1.d** (FTS source of truth). Both engines now index
  `transcript_units`. Story 5.1 owns the table; Story 5.2 owns the
  FTS layer attached to it.
- **REVIEW §1.1.h** (`transcript_units` table not defined). Story 5.1
  is the single migration owner.
- **REVIEW §1.4.d** (search latency budget mismatch). Story 5.4 sets
  the canonical budget and aligns with the NFR document.
- **REVIEW §2.5.a** (results expressed in segment coordinates). Story
  5.4 documents the unit→segment resolution.
- **REVIEW §2.7.a** (chapter inference has no story). Story 5.7 is new
  and owns the stage.
- **REVIEW §6.3** (missing index for `transcript_units(language)`).
  Story 5.1 includes it.

## Dependency notes

- Stories 5.1, 5.2, 5.3 depend on Epic 3
  ([Story 3.5 active transcript](../03-transcription/story-03-05-backend-registry.md),
  [Story 3.6 segment commit](../03-transcription/story-03-06-segment-commit.md)).
- Story 5.5 (incremental) depends on the `segments.committed` notify
  channel from
  [Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md).
- Story 5.7 (chapter inference) depends on Story 5.3 (vector index).
