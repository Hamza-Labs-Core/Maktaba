# DB Entity catalog

Every database table in Maktaba — base schema (architecture §8.1–§8.6) and plan-
introduced extensions (§8.7). Owning migration is from
[`shared/db/migrations/MANIFEST.md`](../../shared/db/migrations/MANIFEST.md);
stories and plans are derived by scanning their text for backticked table names.

| Table | Source | Migration | Owning plan | Stories | Plans (count) |
|-------|--------|-----------|-------------|---------|---------------|
| `audio_cache` | architecture.md §8.7 | 0010 | plan-02-03 | — | 1 |
| `audio_tracks` | architecture.md §8 | — | — | [2.1](stories-map.md#21), [2.2](stories-map.md#22), [7.4](stories-map.md#74), [7.17](stories-map.md#717), [9.15](stories-map.md#915) | 8 |
| `audit_log` | architecture.md §8 | — | — | [9.17](stories-map.md#917), [10.4](stories-map.md#104), [10.16](stories-map.md#1016), [10.17](stories-map.md#1017), [11.13](stories-map.md#1113), [12.10](stories-map.md#1210) *(+3)* | 19 |
| `chapters` | architecture.md §8 | — | — | [5.7](stories-map.md#57), [7.4](stories-map.md#74), [9.15](stories-map.md#915), [9.18](stories-map.md#918) | 9 |
| `collection_items` | architecture.md §8 | — | — | [7.14](stories-map.md#714), [9.13](stories-map.md#913), [9.14](stories-map.md#914), [9.15](stories-map.md#915) | 3 |
| `collections` | architecture.md §8 | — | — | — | 1 |
| `devices` | architecture.md §8 | — | — | [7.17](stories-map.md#717), [7.22](stories-map.md#722), [12.10](stories-map.md#1210) | 3 |
| `events` | architecture.md §8 | — | — | [7.16](stories-map.md#716), [19.2](stories-map.md#192) | 3 |
| `libraries` | architecture.md §8 | — | — | [7.3](stories-map.md#73) | 5 |
| `library_roots` | architecture.md §8 | — | — | — | 1 |
| `library_scan_state` | architecture.md §8.7 | 0006 | plan-01-05 | — | 2 |
| `media_features` | architecture.md §8 | — | — | [9.10](stories-map.md#910), [9.15](stories-map.md#915), [14.7](stories-map.md#147) | 3 |
| `media_info` | architecture.md §8 | — | — | [1.6](stories-map.md#16), [2.1](stories-map.md#21), [7.4](stories-map.md#74), [7.17](stories-map.md#717), [8.15](stories-map.md#815), [9.15](stories-map.md#915) | 7 |
| `playback_state` | architecture.md §8 | — | — | [7.4](stories-map.md#74), [7.11](stories-map.md#711), [7.21](stories-map.md#721), [9.15](stories-map.md#915), [10.1](stories-map.md#101), [10.13](stories-map.md#1013) *(+2)* | 6 |
| `processing_jobs` | architecture.md §8 | — | — | [1.1](stories-map.md#11), [1.4](stories-map.md#14), [1.6](stories-map.md#16), [3.6](stories-map.md#36), [6.1](stories-map.md#61), [6.10](stories-map.md#610) *(+6)* | 30 |
| `purge_log` | architecture.md §8.7 | 0006 | plan-01-05 | — | 1 |
| `saved_searches` | architecture.md §8 | — | — | [7.9](stories-map.md#79), [10.1](stories-map.md#101), [10.13](stories-map.md#1013) | 4 |
| `search_suggestion_terms` | architecture.md §8.7 | 0027 | plan-05-06 | — | 1 |
| `segment_speakers` | architecture.md §8 | — | — | [9.11](stories-map.md#911) | 3 |
| `speakers` | architecture.md §8 | — | — | [3.9](stories-map.md#39), [9.11](stories-map.md#911) | 3 |
| `stt_usage` | architecture.md §8.7 | 0011 | plan-03-04 | — | 1 |
| `subtitle_files` | architecture.md §8 | — | — | [4.1](stories-map.md#41), [4.3](stories-map.md#43), [4.4](stories-map.md#44), [9.15](stories-map.md#915) | 9 |
| `tags` | architecture.md §8 | — | — | [7.4](stories-map.md#74), [9.12](stories-map.md#912) | 2 |
| `track_selection_decisions` | architecture.md §8.7 | 0009 | plan-02-02 | — | 1 |
| `transcript_segments` | architecture.md §8 | — | — | [3.6](stories-map.md#36), [3.9](stories-map.md#39), [4.1](stories-map.md#41), [4.5](stories-map.md#45), [5.2](stories-map.md#52), [8.11](stories-map.md#811) *(+1)* | 11 |
| `transcript_units` | architecture.md §8 | 0017 | plan-05-01 | [5.1](stories-map.md#51), [5.2](stories-map.md#52), [5.4](stories-map.md#54), [5.6](stories-map.md#56), [7.8](stories-map.md#78) | 7 |
| `transcript_words` | architecture.md §8 | — | — | [3.6](stories-map.md#36) | 3 |
| `transcripts` | architecture.md §8 | — | — | [1.6](stories-map.md#16), [3.5](stories-map.md#35), [3.7](stories-map.md#37), [5.3](stories-map.md#53), [7.5](stories-map.md#75), [9.15](stories-map.md#915) | 20 |
| `users` | architecture.md §8 | — | — | [10.1](stories-map.md#101), [10.9](stories-map.md#109) | 4 |
| `vector_index_dead_letter` | architecture.md §8.7 | 0025 | plan-05-05 | — | 1 |
| `video_tags` | architecture.md §8 | — | — | [9.12](stories-map.md#912), [9.15](stories-map.md#915) | 1 |
| `videos` | architecture.md §8 | — | — | [1.1](stories-map.md#11), [1.2](stories-map.md#12), [1.3](stories-map.md#13), [1.5](stories-map.md#15), [1.6](stories-map.md#16), [2.3](stories-map.md#23) *(+8)* | 32 |

## Migration manifest (full)

| Slot | File | Plan | Depends on | Summary |
|------|------|------|-----------|---------|
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
