# Database Migration Manifest

> Single source of truth for migration slot numbering across all
> implementation plans. Any plan that ships SQL DDL **must** claim its slot
> here before the plan can be reviewed.
>
> The slots are sequential and topologically ordered by dependency: a
> migration may only reference tables, columns, indexes, or functions that
> were introduced by a smaller-numbered slot. CI enforces that every
> `NNNN_*.sql` file in the repository corresponds to one row below; any
> drift fails the build.

**Format.** `NNNN | filename | owning plan | depends on | one-line summary`.
The `depends on` column lists the slots that must already be applied for
the migration to succeed. Slots are reserved in groups per epic; new plans
land at the next free integer (no gaps, no reservations).

---

## Manifest

| Slot | Filename | Owning plan | Depends on | Summary |
|------|----------|-------------|------------|---------|
| `0001` | `0001_init_libraries_and_videos.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | — | `libraries` + `videos` base tables (architecture §8.1). |
| `0002` | `0002_processing_jobs.sql` | [plan-06-01](../../specs/epics/06-job-queue/plan-06-01-schema-indexes.md) | 0001 | Canonical `processing_jobs` table + CHECK constraints + partial indexes (architecture §7.1). |
| `0003` | `0003_videos_content_hash.sql` | [plan-01-02](../../specs/epics/01-scanner/plan-01-02-content-identity.md) | 0001 | Add `videos.content_hash` + `UNIQUE (library_id, content_hash)` (architecture §3.1). |
| `0004` | `0004_video_states_and_stages.sql` | [plan-01-06](../../specs/epics/01-scanner/plan-01-06-video-state-machine.md) | 0001, 0002 | 12-state `videos.state` CHECK + stages enum (story-01-06). |
| `0005` | `0005_videos_new_notify.sql` | [plan-01-01](../../specs/epics/01-scanner/plan-01-01-file-discovery.md) | 0001, 0002 | `pg_notify('videos.new', …)` trigger for newly inserted rows. |
| `0006` | `0006_library_scan_state.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | 0001 | `library_scan_state` table (incremental scan cursors). |
| `0007` | `0007_videos_last_seen_at.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | 0001 | `videos.last_seen_at` + `videos.deleted_at` (soft delete). |
| `0008` | `0008_scan_control.sql` | [plan-01-04](../../specs/epics/01-scanner/plan-01-04-manual-control.md) | 0006 | Scan control trigger/cancel columns (`progress_pct`, `cancel_requested`). |
| `0009` | `0009_audio_tracks_extensions.sql` | [plan-02-02](../../specs/epics/02-audio-extraction/plan-02-02-track-selection.md) | 0001 | `audio_tracks.disposition` + `detected_language` + `detected_language_confidence`. |
| `0010` | `0010_extract_error_envelope.sql` | [plan-02-03](../../specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md) | 0002 | `processing_jobs.error TEXT → JSONB` + `audio_cache` table. |
| `0011` | `0011_stt_usage.sql` | [plan-03-04](../../specs/epics/03-transcription/plan-03-04-openai-api-backend.md) | 0001 | `stt_usage` ledger for OpenAI API minutes/spend. |
| `0012` | `0012_transcripts_is_active.sql` | [plan-03-05](../../specs/epics/03-transcription/plan-03-05-backend-registry.md) | 0001 | `transcripts.is_active` + `metadata`; partial unique index (REVIEW §1.1.b). |
| `0013` | `0013_segment_commit_function.sql` | [plan-03-06](../../specs/epics/03-transcription/plan-03-06-segment-commit.md) | 0002, 0012 | `commit_segment(...)` PL/pgSQL function + `AFTER INSERT` NOTIFY trigger. |
| `0014` | `0014_transcript_segments_speaker_index.sql` | [plan-03-09](../../specs/epics/03-transcription/plan-03-09-diarization.md) | 0012 | Index on `transcript_segments(transcript_id, speaker)` for diarization. |
| `0015` | `0015_subtitle_files.sql` | [plan-04-03](../../specs/epics/04-subtitles/plan-04-03-external-discovery.md) | 0001, 0012 | Canonical `subtitle_files` table with `is_embedded`, `is_external`, partial unique index, NOTIFY trigger. |
| `0016` | `0016_transcript_segments_view.sql` | [plan-04-05](../../specs/epics/04-subtitles/plan-04-05-live-vtt-contract.md) | 0012 | `transcript_segments_v` view for live-VTT rendering. |
| `0017` | `0017_transcript_units.sql` | [plan-05-01](../../specs/epics/05-search-indexing/plan-05-01-unit-chunking.md) | 0012 | `transcript_units` table (search chunks). |
| `0018` | `0018_transcript_units_notify.sql` | [plan-05-03](../../specs/epics/05-search-indexing/plan-05-03-chroma-vector.md) | 0017 | `pg_notify('transcript_units.committed', …)` trigger. |
| `0019` | `0019_fts_tsvector_arabic_config.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0017 | Arabic-aware text-search configuration + `maktaba_normalize` function. |
| `0020` | `0020_transcripts_fts_virtual_table.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0017 | SQLite FTS5 virtual table (architecture §8.3). |
| `0021` | `0021_transcript_units_tsv_column.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0017, 0019 | Generated `transcript_units.tsv` column. |
| `0022` | `0022_transcripts_fts_triggers_with_normalize.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0020 | SQLite FTS5 sync triggers using `maktaba_normalize`. |
| `0023` | `0023_transcript_units_tsv_indexes.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0021 | GIN index on `transcript_units.tsv`. |
| `0024` | `0024_transcripts_fts_view_postgres.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0021 | Compatibility view (Postgres) for clients expecting `transcripts_fts`. |
| `0025` | `0025_incremental_indexing.sql` | [plan-05-05](../../specs/epics/05-search-indexing/plan-05-05-incremental-indexing.md) | 0017 | `transcripts.last_indexed_segment_seq`, `transcript_units.indexed_at_in_chroma`, `vector_index_dead_letter`. |
| `0026` | `0026_chapters.sql` | [plan-05-07](../../specs/epics/05-search-indexing/plan-05-07-chapter-inference.md) | 0001 | `chapters` table (architecture §8.1; supports `source` discriminator). |
| `0027` | `0027_search_suggestion_terms.sql` | [plan-05-06](../../specs/epics/05-search-indexing/plan-05-06-query-suggestions.md) | 0017 | `search_suggestion_terms` table (typeahead corpus). |
| `0028` | `0028_jobs_progress_notify.sql` | [plan-06-03](../../specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md) | 0002, 0013 | `pg_notify('jobs.progress', …)` trigger. |

---

## Reservation discipline

1. New migrations land at the next free integer. No gaps.
2. SQLite-only variants reuse the same slot with a `.sqlite.sql` suffix
   (e.g. `0002_processing_jobs.sqlite.sql`). They **do not** consume an
   independent slot.
3. Down-migrations reuse the same slot with a `.down.sql` suffix.
4. Post-migrations (`.post.sql`) are part of the same logical slot — used
   for `CREATE INDEX CONCURRENTLY` and similar non-transactional steps.
5. If two epics need to land migrations concurrently, they coordinate slot
   assignment by editing this file in the same PR.

## Cross-epic ownership

Three foundation tables span multiple epics:

- `processing_jobs` — owned by **plan-06-01** (slot 0002). Earlier plans
  (notably plan-01-01's enqueue path) declare a hard dependency on
  slot 0002 landing first.
- `subtitle_files` — owned by **plan-04-03** (slot 0015). All shape
  decisions (`is_embedded`, `is_external`, partial unique by language)
  ship in this single migration; plan-04-01 and plan-04-04 declare a
  dependency on it.
- `chapters` — owned by **plan-05-07** (slot 0026). The `source`
  discriminator (`embedded` | `inferred` | `manual`) is part of the base
  shape; downstream plans (e.g. plan-09-18) extend it via separate slots.

## See also

- [`specs/architecture.md`](../../specs/architecture.md) §8.1 (canonical schema)
- [`specs/PLAN_REVIEW.md`](../../specs/PLAN_REVIEW.md) §1 (history of the migration-collision audit)
