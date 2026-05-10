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
| `0000` | `0000_schema_version.sql` | [plan-22-04](../../specs/epics/22-devops/plan-22-04-database-migrations.md) | — | `maktaba_schema_version` smoke-test table that proves the goose runner is wired and the dir is reachable. |
| `0001` | `0001_init_libraries_and_videos.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | 0000 | `libraries` + `videos` base tables (architecture §8.1). |
| `0002` | `0002_processing_jobs.sql` | [plan-06-01](../../specs/epics/06-job-queue/plan-06-01-schema-indexes.md) | 0001 | Canonical `processing_jobs` table + CHECK constraints + partial indexes (architecture §7.1). |
| `0003` | `0003_videos_content_hash.sql` | [plan-01-02](../../specs/epics/01-scanner/plan-01-02-content-identity.md) | 0001 | Add `videos.content_hash` + `UNIQUE (library_id, content_hash)` (architecture §3.1). |
| `0004` | `0004_video_states_and_stages.sql` | [plan-01-06](../../specs/epics/01-scanner/plan-01-06-video-state-machine.md) | 0001, 0002 | 12-state `videos.state` CHECK + stages enum (story-01-06). |
| `0005` | `0005_videos_new_notify.sql` | [plan-01-01](../../specs/epics/01-scanner/plan-01-01-file-discovery.md) | 0001, 0002 | `pg_notify('videos.new', …)` trigger for newly inserted rows. |
| `0006` | `0006_library_scan_state.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | 0001 | `library_scan_state` table (incremental scan cursors). |
| `0007` | `0007_videos_last_seen_at.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | 0001 | `videos.last_seen_at` + `videos.deleted_at` (soft delete). |
| `0008` | `0008_scan_control.sql` | [plan-01-04](../../specs/epics/01-scanner/plan-01-04-manual-control.md) | 0006 | Scan control trigger/cancel columns (`progress_pct`, `cancel_requested`). |
| `0009` | `0009_audio_tracks_extensions.sql` | [plan-02-02](../../specs/epics/02-audio-extraction/plan-02-02-track-selection.md) | 0001 | Base `media_info` + `audio_tracks` tables (architecture §8.1) plus `disposition` / `detected_language` / `detected_language_confidence`. |
| `0010` | `0010_extract_error_envelope.sql` | [plan-02-03](../../specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md) | 0002, 0009 | `processing_jobs.error TEXT → JSONB` + `audio_cache` table + `audio_tracks.last_extracted_at`. |
| `0011` | `0011_stt_usage.sql` | [plan-03-04](../../specs/epics/03-transcription/plan-03-04-openai-api-backend.md) | 0001 | `stt_usage` ledger for OpenAI API minutes/spend. |
| `0012` | `0012_transcripts_is_active.sql` | [plan-03-05](../../specs/epics/03-transcription/plan-03-05-backend-registry.md) | 0001, 0009 | Base `transcripts` + `transcript_segments` + `transcript_words` tables with `is_active` + `metadata`; partial unique index (REVIEW §1.1.b). |
| `0013` | `0013_segment_commit_function.sql` | [plan-03-06](../../specs/epics/03-transcription/plan-03-06-segment-commit.md) | 0002, 0012 | `commit_segment(...)` PL/pgSQL function + `AFTER INSERT` NOTIFY trigger. |
| `0014` | `0014_transcript_segments_speaker_index.sql` | [plan-03-09](../../specs/epics/03-transcription/plan-03-09-diarization.md) | 0012 | Index on `transcript_segments(transcript_id, speaker)` for diarization. |
| `0015` | `0015_subtitle_files.sql` | [plan-04-03](../../specs/epics/04-subtitles/plan-04-03-external-discovery.md) | 0001, 0012 | Canonical `subtitle_files` table with `is_embedded`, `is_external`, partial unique index, NOTIFY trigger. |
| `0016` | `0016_transcript_segments_fts.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0012 | Generated `transcript_segments.search_tsv` (Postgres) + GIN index, with SQLite FTS5 virtual-table mirror. Uses `maktaba_search` text-search configuration (`unaccent`-aware, mixed-language safe). |
| `0028` | `0028_jobs_progress_notify.sql` | [plan-06-03](../../specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md) | 0002, 0013 | `pg_notify('jobs.progress', …)` trigger. |
| `0029` | `0029_users.sql` | [plan-10-01](../../specs/epics/10-auth-security/plan-10-01-user-store.md) | — | `users` table (Epic 10 README §"users") + sentinel admin row for single-user mode. |
| `0030` | `0030_library_acl.sql` | [plan-10-13](../../specs/epics/10-auth-security/plan-10-13-permission-model.md) | 0001, 0029 | `library_acl` table (Story 10.13 — per-user library read scope). |
| `0031` | `0031_search_history.sql` | [plan-05-06](../../specs/epics/05-search-indexing/plan-05-06-query-suggestions.md) | 0029 | `search_history` table — typeahead/recents corpus keyed on `query_norm`. |
| `0032` | `0032_chapters.sql` | [plan-07-07](../../specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md) | 0001 | `chapters` table (per-video chapter list w/ `{embedded, inferred, manual}` source). |
| `0033` | `0033_collections.sql` | [plan-07-14](../../specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md) | 0001, 0029 | `collections` + `collection_items` (manual + smart). |
| `0034` | `0034_tags.sql` | [plan-07-14](../../specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md) | 0001 | `tags` + `video_tags` (case-fold + NFC name_norm uniqueness). |
| `0035` | `0035_speakers.sql` | [plan-07-14](../../specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md) | 0001, 0012 | `speakers` + `segment_speakers` (rename + merge). |
| `0036` | `0036_audit_log.sql` | [plan-07-04](../../specs/epics/07-api-server/plan-07-04-video-crud.md) | 0029 | Append-only `audit_log` (purge, settings, speaker merge). |
| `0037` | `0037_saved_searches.sql` | [plan-07-09](../../specs/epics/07-api-server/plan-07-09-saved-searches.md) | 0029 | `saved_searches` per-user (also feeds smart collections). |
| `0038` | `0038_playback_state.sql` | [plan-07-11](../../specs/epics/07-api-server/plan-07-11-watch-progress-sync.md) | 0001, 0029 | `playback_state` (user_id, video_id) resume table + `(user_id, updated_at)` index for Continue Watching. |
| `0039` | `0039_streaming_sessions.sql` | [plan-07-10](../../specs/epics/07-api-server/plan-07-10-streaming-session-lifecycle.md) | 0001, 0029 | `streaming_sessions` row per opened HLS/direct session. |
| `0040` | `0040_devices.sql` | [plan-07-22](../../specs/epics/07-api-server/plan-07-22-devices-register.md) | 0029 | `devices` (push notification token registry; soft-revocation). |
| `0041` | `0041_user_recs.sql` | [plan-07-21](../../specs/epics/07-api-server/plan-07-21-recommendations.md) | 0001, 0029 | `user_recs` (For-You rail; nightly Pipeline aggregation). |
| `0042` | `0042_app_settings.sql` | [plan-07-15](../../specs/epics/07-api-server/plan-07-15-settings-system.md) | 0029 | `app_settings` + `settings_changed` NOTIFY trigger. |
| `0043` | `0043_library_roots.sql` | [plan-09-16](../../specs/epics/09-library-management/plan-09-16-multi-root-overlap.md) | 0001 | `library_roots` canonical normalized store + back-fill from transitional `libraries.roots TEXT[]`. |
| `0044` | `0044_library_sweeps.sql` | [plan-09-03](../../specs/epics/09-library-management/plan-09-03-periodic-sweep.md) | 0001 | `library_sweeps` telemetry rows (one per periodic sweep run). |
| `0045` | `0045_media_features.sql` | [plan-09-10](../../specs/epics/09-library-management/plan-09-10-content-type-classifier.md) | 0001 | `media_features` (probe-stage feature blob) + `videos.content_type` column + index. |
| `0046` | `0046_library_topics.sql` | [plan-09-09](../../specs/epics/09-library-management/plan-09-09-topic-tag.md) | 0001 | `library_topics` (k-means centroids) + `video_topics` (per-video topic assignment). |
| `0047` | `0047_library_stats_cache.sql` | [plan-09-07](../../specs/epics/09-library-management/plan-09-07-library-stats.md) | 0001 | `library_stats_cache` (denormalized counts; backs <50 ms `/stats`). |
| `0048` | `0048_speakers_voiceprint.sql` | [plan-09-11](../../specs/epics/09-library-management/plan-09-11-speakers.md) | 0001, 0035 | `speakers.library_id` + `speakers.voiceprint` + `speakers.unknown_index` (per-library voiceprint matching). |
| `0049` | `0049_chapter_infer_stage.sql` | [plan-09-18](../../specs/epics/09-library-management/plan-09-18-chapter-inference.md) | 0002 | Extend `processing_jobs.stage` CHECK with `topic_recluster`, `topic_assign`, `categorize`, `chapter_infer`. |
| `0050` | `0050_web_sessions.sql` | [plan-10-02](../../specs/epics/10-auth-security/plan-10-02-web-login.md) | 0029 | `web_sessions` (cookie-backed SPA session store with CSRF token + active/reaper indexes). |
| `0051` | `0051_refresh_tokens.sql` | [plan-10-03](../../specs/epics/10-auth-security/plan-10-03-native-login.md) | 0029, 0040 | `refresh_tokens` (opaque token + argon2id hash, family rotation, device link). |

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
