# Maktaba — Migration Catalog

> Source of truth: [`shared/db/migrations/MANIFEST.md`](../../shared/db/migrations/MANIFEST.md).
> CI fails the build if any `NNNN_*.sql` in the repo doesn't correspond to a manifest row. Plans that ship SQL DDL must claim their slot in the manifest before review.

This page is a wiki view derived from the canonical manifest plus all `plan-*.md` files. It surfaces:

1. **§1 — Manifest (canonical, slots 0001–0028).** What's locked in.
2. **§2 — Per-epic claims.** Slots referenced by plans for epics 07–24 (some not yet folded into the manifest).
3. **§3 — Reservation discipline.**
4. **§4 — Foundation tables crossing multiple epics.**
5. **§5 — Known slot collisions** that the manifest must reconcile during review.

---

## §1 Canonical manifest (slots 0001–0028)

Verbatim from [`shared/db/migrations/MANIFEST.md`](../../shared/db/migrations/MANIFEST.md). Format: `slot | filename | owning plan | depends on | summary`.

| Slot | Filename | Owning plan | Depends on | Summary |
|---|---|---|---|---|
| `0001` | `0001_init_libraries_and_videos.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | — | `libraries` + `videos` base tables (architecture §8.1). |
| `0002` | `0002_processing_jobs.sql` | [plan-06-01](../../specs/epics/06-job-queue/plan-06-01-schema-indexes.md) | 0001 | Canonical `processing_jobs` table + CHECK constraints + partial indexes. |
| `0003` | `0003_videos_content_hash.sql` | [plan-01-02](../../specs/epics/01-scanner/plan-01-02-content-identity.md) | 0001 | `videos.content_hash` + `UNIQUE(library_id, content_hash)`. |
| `0004` | `0004_video_states_and_stages.sql` | [plan-01-06](../../specs/epics/01-scanner/plan-01-06-video-state-machine.md) | 0001, 0002 | 12-state `videos.state` CHECK + stages enum. |
| `0005` | `0005_videos_new_notify.sql` | [plan-01-01](../../specs/epics/01-scanner/plan-01-01-file-discovery.md) | 0001, 0002 | `pg_notify('videos.new', …)` trigger. |
| `0006` | `0006_library_scan_state.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | 0001 | `library_scan_state` (incremental scan cursors). |
| `0007` | `0007_videos_last_seen_at.sql` | [plan-01-05](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) | 0001 | `videos.last_seen_at` + `videos.deleted_at`. |
| `0008` | `0008_scan_control.sql` | [plan-01-04](../../specs/epics/01-scanner/plan-01-04-manual-control.md) | 0006 | Scan control trigger/cancel columns. |
| `0009` | `0009_audio_tracks_extensions.sql` | [plan-02-02](../../specs/epics/02-audio-extraction/plan-02-02-track-selection.md) | 0001 | `audio_tracks.disposition` + `detected_language` + confidence. |
| `0010` | `0010_extract_error_envelope.sql` | [plan-02-03](../../specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md) | 0002 | `processing_jobs.error TEXT → JSONB` + `audio_cache`. |
| `0011` | `0011_stt_usage.sql` | [plan-03-04](../../specs/epics/03-transcription/plan-03-04-openai-api-backend.md) | 0001 | `stt_usage` ledger for OpenAI minutes/spend. |
| `0012` | `0012_transcripts_is_active.sql` | [plan-03-05](../../specs/epics/03-transcription/plan-03-05-backend-registry.md) | 0001 | `transcripts.is_active` + `metadata`; partial unique index. |
| `0013` | `0013_segment_commit_function.sql` | [plan-03-06](../../specs/epics/03-transcription/plan-03-06-segment-commit.md) | 0002, 0012 | `commit_segment(...)` PL/pgSQL function + `AFTER INSERT` NOTIFY trigger. |
| `0014` | `0014_transcript_segments_speaker_index.sql` | [plan-03-09](../../specs/epics/03-transcription/plan-03-09-diarization.md) | 0012 | Index on `transcript_segments(transcript_id, speaker)`. |
| `0015` | `0015_subtitle_files.sql` | [plan-04-03](../../specs/epics/04-subtitles/plan-04-03-external-discovery.md) | 0001, 0012 | Canonical `subtitle_files` with `is_embedded`, `is_external`, partial unique, NOTIFY. |
| `0016` | `0016_transcript_segments_view.sql` | [plan-04-05](../../specs/epics/04-subtitles/plan-04-05-live-vtt-contract.md) | 0012 | `transcript_segments_v` view. |
| `0017` | `0017_transcript_units.sql` | [plan-05-01](../../specs/epics/05-search-indexing/plan-05-01-unit-chunking.md) | 0012 | `transcript_units` (search chunks). |
| `0018` | `0018_transcript_units_notify.sql` | [plan-05-03](../../specs/epics/05-search-indexing/plan-05-03-chroma-vector.md) | 0017 | `pg_notify('transcript_units.committed', …)` trigger. |
| `0019` | `0019_fts_tsvector_arabic_config.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0017 | Arabic-aware text-search config + `maktaba_normalize` function. |
| `0020` | `0020_transcripts_fts_virtual_table.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0017 | SQLite FTS5 virtual table. |
| `0021` | `0021_transcript_units_tsv_column.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0017, 0019 | Generated `transcript_units.tsv` column. |
| `0022` | `0022_transcripts_fts_triggers_with_normalize.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0020 | SQLite FTS5 sync triggers using `maktaba_normalize`. |
| `0023` | `0023_transcript_units_tsv_indexes.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0021 | GIN index on `transcript_units.tsv`. |
| `0024` | `0024_transcripts_fts_view_postgres.sql` | [plan-05-02](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) | 0021 | Compatibility view (Postgres) for clients expecting `transcripts_fts`. |
| `0025` | `0025_incremental_indexing.sql` | [plan-05-05](../../specs/epics/05-search-indexing/plan-05-05-incremental-indexing.md) | 0017 | `transcripts.last_indexed_segment_seq`, `transcript_units.indexed_at_in_chroma`, `vector_index_dead_letter`. |
| `0026` | `0026_chapters.sql` | [plan-05-07](../../specs/epics/05-search-indexing/plan-05-07-chapter-inference.md) | 0001 | `chapters` with `source` discriminator. |
| `0027` | `0027_search_suggestion_terms.sql` | [plan-05-06](../../specs/epics/05-search-indexing/plan-05-06-query-suggestions.md) | 0017 | `search_suggestion_terms` (typeahead corpus). |
| `0028` | `0028_jobs_progress_notify.sql` | [plan-06-03](../../specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md) | 0002, 0013 | `pg_notify('jobs.progress', …)` trigger. |

---

## §2 Per-epic claims (slots beyond the manifest)

These slot numbers / filenames appear in `plan-*.md` files for epics 07–24. **They are awaiting canonical assignment in [`MANIFEST.md`](../../shared/db/migrations/MANIFEST.md).** Where two plans reference the same slot with different filenames (see §5), the manifest pass must reconcile.

### Epic 07 — API Server

| Slot | Filename (claimed) | Owning plan | Notes |
|---|---|---|---|
| `0017` | `0017_playback_state_indexes.sql` | [plan-07-11](../../specs/epics/07-api-server/plan-07-11-watch-progress-sync.md) | **Collides with `0017_transcript_units.sql` (Epic 05).** Reassign on manifest. |
| `0018` | `0018_processing_jobs_stats_indexes.sql` | plan-07 (queue stats) | Collides with `0018_transcript_units_notify.sql` (Epic 05). |
| `0019` | `0019_app_settings.sql` | plan-07 (settings API) | Collides with `0019_fts_tsvector_arabic_config.sql` (Epic 05). |
| `0020` | `0020_events.sql` | plan-07/19 (event bus) | Collides with `0020_transcripts_fts_virtual_table.sql` and `0020_users.sql` (Epic 10). |

### Epic 08 — Streaming

| Slot | Filename (claimed) | Owning plan | Notes |
|---|---|---|---|
| `0016` | `0016_streaming_sessions.sql` | plan-08 (session store) | Collides with `0016_transcript_segments_view.sql` (Epic 04). |
| `0020` | `0020_streaming_sessions.sql` | (alt slot) | Manifest pass to pick one. |

### Epic 09 — Library Management

| Slot | Filename (claimed) | Owning plan |
|---|---|---|
| `0030` | `0030_libraries_settings.sql` | [plan-09-01](../../specs/epics/09-library-management/plan-09-01-library-config-schema.md) |
| `0031` | `0031_videos_path_history.sql` | plan-09 (filesystem watcher) |
| `0032` | `0032_library_sweeps.sql` | plan-09 (periodic sweep) |
| `0033` | `0033_videos_content_hash.sql` | plan-09 (dedup; **note: duplicates filename of `0003`** — content of slot 33 is an additional dedup index/unique, not the column) |
| `0034` | `0034_videos_state_aux.sql` | plan-09 (state aux columns) |
| `0035` | `0035_library_stats_cache.sql` | plan-09 (stats) |
| `0036` | `0036_videos_language_source.sql` | plan-09 (language tag) |
| `0037` | `0037_topics.sql` | plan-09 (topic tag) |
| `0038` | `0038_videos_content_type.sql` | plan-09 (content-type classifier) |
| `0039` | `0039_speakers.sql` | plan-09 (speakers) |
| `0040` | `0040_tags_normalize.sql` | plan-09 (tag CRUD) |
| `0041` | `0041_collection_items.sql` | plan-09 (collections manual) |
| `0042` | `0042_collections_smart.sql` | plan-09 (smart collections) |
| `0043` | `0043_libraries_cascade_fixups.sql` | plan-09 (deletion semantics) |
| `0044` | `0044_library_roots.sql` | plan-09 (multi-root) |
| `0046` | `0046_chapters_source.sql` | plan-09-18 (chapters extension) |

### Epic 10 — Auth & Security

| Slot | Filename (claimed) | Notes |
|---|---|---|
| `0020` | `0020_users.sql` | User store. Collides with Epic 05/07 claims. |
| `0021` | `0021_web_sessions.sql` | Login sessions. |
| `0022` | `0022_refresh_tokens.sql` | Refresh-token rotation. |
| `0023` | `0023_jwt_keys.sql` | (early form of `signing_keys`). |
| `0024` | `0024_auth_ip_attempts.sql` | Failed-login lockout. |
| `0025` | `0025_library_acl.sql` | Per-library role table. |
| `0026` | `0026_audit_security_dedupe.sql` | Auth-side audit log (later subsumed by Epic 21 `audit_log`). |
| `0027` | `0027_pairing_codes.sql` | Auth-pair canonical (extended by `0053` for QR nonce). |
| `0030` | `0030_personal_access_tokens.sql` | PAT (Story 10.13 / 11.13). |
| `0040` | `0040_signing_keys.sql` | RS256 signing keys (Story 23.1, plan-23-01). |

### Epic 11 — Web UI / Personal data surfaces

| Slot | Filename (claimed) | Notes |
|---|---|---|
| `0015` | `0015_saved_searches.sql` | Collides with Epic 04 `0015_subtitle_files.sql`. |
| `0021` | `0021_user_recs.sql` | (alt slot for Epic 11). |

### Epic 12 — Mobile

| Slot | Filename (claimed) | Notes |
|---|---|---|
| `0022` | `0022_devices.sql` | Capacitor device registration. |
| `0040` | `0040_devices.sql` | (alt slot if 22 is taken). |
| `0041` | `0041_device_downloads.sql` | Offline downloads. |

### Epic 14 — TV apps

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0046` | `0046_playback_state_continue_idx.sql` | [plan-14-05](../../specs/epics/14-tv-apps/plan-14-05-continue-watching.md) — Continue Watching index. |
| `0047` | `0047_recommendation_dismissals.sql` | [plan-14-07](../../specs/epics/14-tv-apps/plan-14-07-recommendations-api.md) — "Not interested" persistence. |

### Epic 15 — Discovery & Networking

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0050` | `0050_server_identity.sql` | plan-15-01 — `server_identity(id, mdns_id)`. |
| `0051` | `0051_relay.sql` | plan-15-02 — `relay_settings`, `relay_usage`. |
| `0052` | `0052_dlna.sql` | plan-15-04 — `dlna_settings`, `videos_dlna_compatible` view. |
| `0053` | `0053_pairing_codes_qr.sql` | plan-15-06 — `pairing_codes.nonce`, `created_by_user_id`. |
| `0054` | `0054_federation.sql` | plan-15-07 — `federation_pending`, `federation_partners`. |

### Epic 16 — Subscriptions

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0060` | `0060_seat_counter.sql` | plan-16-02. |
| `0061` | `0061_tier_grace.sql` | plan-16-02 — downgrade grace tracking. |
| `0062` | `0062_billing.sql` | plan-16-03 — Stripe webhook + reconciliation. |
| `0063` | `0063_licenses.sql` | plan-16-04 — `licenses(raw_sealed, ...)`, `license_revocations`. |
| `0064` | `0064_telemetry.sql` | plan-16-07 — `telemetry_events`, `telemetry_web_vitals`. |
| `0065` | `0065_feature_flags.sql` | plan-16-08 — `feature_flag_overrides`, `beta_cohorts`. |

### Epic 19 — Scalability

| Slot range | Filename (claimed) | Plan |
|---|---|---|
| `0020` | `0020_events.sql` | plan-19-02 — durable WS event log. |
| (TBA) | streaming-replicas | plan-19-03. |
| (TBA) | pipeline scale-out columns | plan-19-04. |
| (TBA) | library budgets | plan-19-07. |
| (TBA) | multi-tenant readiness | plan-19-08. |

### Epic 21 — Observability

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0045` | `0045_audit_log.sql` | [plan-21-06](../../specs/epics/21-observability/plan-21-06-audit-log.md) — partitioned append-only `audit_log`. |

### Epic 23 — Security

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0040` | `0040_signing_keys.sql` | [plan-23-01](../../specs/epics/23-security/plan-23-01-authentication.md). Replaces / canonicalizes Epic 10's `0023_jwt_keys.sql`. |
| (TBA) | `failed_login_attempts`, `rate_limit_bucket` | story 23.6. |

### Epic 24 — Data Integrity

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0011` | `0011_idempotency_keys.sql` | plan-24-02 — collides with `0011_stt_usage.sql`; expected to be subsumed into `processing_jobs` extension at a later free slot. |
| `0050` | `0050_constraints.sql` | plan-24-03 — system-wide CHECK / FK / soft-delete inventory. |
| `0060` | `0060_videos_supersede.sql` | plan-24-08 — `videos.superseded_by`. |
| (TBA) | `advisory_locks` metadata | plan-24-04. |
| (TBA) | `backups`, `recovery_events`, `integrity_reports` | plan-24-05/06/07. |

### Epic 17 — UX

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0070` | `0070_onboarding.sql` | plan-17-06 — optional `onboarding_state` (may be deferred). |

### Reserved

| Slot | Filename (claimed) | Plan |
|---|---|---|
| `0099` | `0099_widgets.sql` | (reserved) |

---

## §3 Reservation discipline

(From [`MANIFEST.md`](../../shared/db/migrations/MANIFEST.md).)

1. New migrations land at the next free integer. **No gaps.**
2. SQLite-only variants reuse the same slot with a `.sqlite.sql` suffix (e.g. `0002_processing_jobs.sqlite.sql`). They **do not** consume an independent slot.
3. Down-migrations reuse the same slot with a `.down.sql` suffix.
4. Post-migrations (`.post.sql`) are part of the same logical slot — used for `CREATE INDEX CONCURRENTLY` and similar non-transactional steps.
5. If two epics need to land migrations concurrently, they coordinate slot assignment by editing `MANIFEST.md` in the same PR.
6. Migration safety lint (Story 19.5) blocks `CREATE INDEX` without `CONCURRENTLY` on hot tables (`videos`, `segments`, `processing_jobs`, `events`).

---

## §4 Cross-epic foundation tables

Three tables span multiple epics:

- **`processing_jobs`** — owned by **plan-06-01** (slot `0002`). Earlier plans (notably plan-01-01's enqueue path) declare a hard dependency on slot 0002 landing first.
- **`subtitle_files`** — owned by **plan-04-03** (slot `0015`). All shape decisions (`is_embedded`, `is_external`, partial unique by language) ship in this single migration; plans 04-01 and 04-04 declare a dependency on it.
- **`chapters`** — owned by **plan-05-07** (slot `0026`). The `source` discriminator (`embedded | inferred | manual`) is part of the base shape; downstream plans (e.g. plan-09-18) extend it via separate slots.

---

## §5 Known slot collisions (manifest reconciliation needed)

| Slot | Conflicting filenames | Owning plans |
|---|---|---|
| `0011` | `0011_stt_usage.sql` ✓ | Epic 03 (canonical) vs `0011_idempotency_keys.sql` (Epic 24). |
| `0015` | `0015_subtitle_files.sql` ✓ | Epic 04 (canonical) vs `0015_saved_searches.sql` (Epic 11). |
| `0016` | `0016_transcript_segments_view.sql` ✓ | Epic 04 (canonical) vs `0016_streaming_sessions.sql` (Epic 08). |
| `0017` | `0017_transcript_units.sql` ✓ | Epic 05 (canonical) vs `0017_playback_state_indexes.sql` (Epic 07). |
| `0018` | `0018_transcript_units_notify.sql` ✓ | Epic 05 (canonical) vs `0018_processing_jobs_stats_indexes.sql` (Epic 07). |
| `0019` | `0019_fts_tsvector_arabic_config.sql` ✓ | Epic 05 (canonical) vs `0019_app_settings.sql` (Epic 07). |
| `0020` | `0020_transcripts_fts_virtual_table.sql` ✓ | Epic 05 (canonical) vs `0020_events.sql` (Epic 19) vs `0020_users.sql` (Epic 10) vs `0020_streaming_sessions.sql` (Epic 08). |
| `0021` | `0021_transcript_units_tsv_column.sql` ✓ | Epic 05 (canonical) vs `0021_user_recs.sql` (Epic 11) vs `0021_web_sessions.sql` (Epic 10). |
| `0022` | `0022_transcripts_fts_triggers_with_normalize.sql` ✓ | Epic 05 (canonical) vs `0022_devices.sql` (Epic 12) vs `0022_refresh_tokens.sql` (Epic 10). |
| `0024` | `0024_transcripts_fts_view_postgres.sql` ✓ | Epic 05 (canonical) vs `0024_auth_ip_attempts.sql` (Epic 10). |
| `0025` | `0025_incremental_indexing.sql` ✓ | Epic 05 (canonical) vs `0025_library_acl.sql` (Epic 10). |
| `0026` | `0026_chapters.sql` ✓ | Epic 05 (canonical) vs `0026_audit_security_dedupe.sql` (Epic 10). |
| `0027` | `0027_search_suggestion_terms.sql` ✓ | Epic 05 (canonical) vs `0027_pairing_codes.sql` (Epic 10). |
| `0030` | (free) | `0030_libraries_settings.sql` (Epic 09) vs `0030_personal_access_tokens.sql` (Epic 10). |
| `0040` | (free) | `0040_signing_keys.sql` (Epic 23) vs `0040_devices.sql` (Epic 12) vs `0040_tags_normalize.sql` (Epic 09). |
| `0041` | (free) | `0041_collection_items.sql` (Epic 09) vs `0041_device_downloads.sql` (Epic 12). |
| `0046` | (free) | `0046_playback_state_continue_idx.sql` (Epic 14) vs `0046_chapters_source.sql` (Epic 09). |
| `0050` | (free) | `0050_server_identity.sql` (Epic 15) vs `0050_constraints.sql` (Epic 24). |
| `0060` | (free) | `0060_seat_counter.sql` (Epic 16) vs `0060_videos_supersede.sql` (Epic 24). |

✓ = canonical assignment in `MANIFEST.md`. Epics 07+ slot claims past 0028 are awaiting manifest folding; the table above is the audit list.

---

## See also

- [`shared/db/migrations/MANIFEST.md`](../../shared/db/migrations/MANIFEST.md) — canonical source.
- [`specs/architecture.md`](../../specs/architecture.md) §8.1 (canonical schema), §8.3 (FTS), §10.3 (caches and single-writer).
- [`specs/PLAN_REVIEW.md`](../../specs/PLAN_REVIEW.md) §1 — history of the migration-collision audit.
- [Epic 22 — DevOps](epics/epic-22-devops.md) Story 22.4 (migration runner, lint, doctor).
- [Epic 19 — Scalability](epics/epic-19-scalability.md) Story 19.5 (migration safety lint, long-running guard).
- [Epic 24 — Data Integrity](epics/epic-24-data-integrity.md) Story 24.3 (constraint inventory).
