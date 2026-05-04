# Implementation Plan Review — Epics 07-13

> **STATUS: RESOLVED (2026-05-04).** All blockers and majors itemized in
> this review have been fixed across `specs/architecture.md` and the 105
> plan files. The architecture document was updated first as the single
> source of truth, then each epic was swept against it. See
> §"Resolution log" at the bottom for the per-issue cross-reference, or
> `git log` on `claude/cool-lichterman-f978aa` for the per-file changes.
> The findings below are preserved verbatim for historical traceability.

**Scope.** All 105 implementation plans paired with their stories across seven
epics on `main`:

| Epic | Title | Plans |
|------|-------|-------|
| 07 | API Server | 22 |
| 08 | Streaming | 15 |
| 09 | Library Management | 18 |
| 10 | Auth & Security | 17 |
| 11 | Web UI (PWA) | 14 |
| 12 | Mobile (Capacitor) | 11 |
| 13 | Desktop (Tauri) | 8 |

**Method.** Each epic reviewed against `specs/architecture.md` (canonical
schema §8, API surface §9, gRPC §9.9, auth §9.8, job FSM §7, streaming §4,
client topology §6) and against the matching story acceptance criteria.

**Verdict at a glance.**

| Epic | Overall | Blocking | Major | Minor |
|------|---------|----------|-------|-------|
| 07 | drift-heavy | 7 | 12 | 8 |
| 08 | substantially solid | 3 | 4 | 8 |
| 09 | drift-heavy | 5 | 6 | 6 |
| 10 | mostly clean | 1 | 4 | 8 |
| 11 | strong | 0 | 5 | 4 |
| 12 | mostly clean | 4 | 3 | 6 |
| 13 | mixed | 2 | 5 | 6 |

Eleven plans across the seven epics could ship as-is. The remainder need
edits — often only one or two lines — but a number of recurring patterns
indicate the architecture document and the plans drifted in opposite
directions during the planning sweep, and those should be reconciled before
implementation.

---

## 1. Top-priority cross-cutting issues

Issues below appear in multiple epics. Fixing them in one place avoids
fixing them N times in plan-by-plan edits.

### 1.1 Schema drift from `architecture.md §8` — affects Epics 07, 08, 09

`architecture.md §8` defines a fixed set of tables and columns. Multiple
plans silently reference columns and tables that aren't there. Either the
architecture is incomplete (most likely, given how many plans assume the
same missing columns) or the plans must be rewritten.

The recurring drift items, with the plans referencing them:

| Drifted reference | Canonical (architecture) | Referenced by |
|-------------------|--------------------------|---------------|
| `media_info.duration_sec` | `videos.duration_sec` (line 1318) | [plan-07-03](specs/epics/07-api-server/plan-07-03-library-crud.md), [07-04](specs/epics/07-api-server/plan-07-04-video-crud.md), [07-06](specs/epics/07-api-server/plan-07-06-transcript-window.md), [07-08](specs/epics/07-api-server/plan-07-08-search-api.md), [07-11](specs/epics/07-api-server/plan-07-11-watch-progress-sync.md), [07-21](specs/epics/07-api-server/plan-07-21-recommendations.md) |
| `transcripts.superseded_at` | not defined | [07-04](specs/epics/07-api-server/plan-07-04-video-crud.md), [07-05](specs/epics/07-api-server/plan-07-05-video-processing-control.md), [07-06](specs/epics/07-api-server/plan-07-06-transcript-window.md), [07-08](specs/epics/07-api-server/plan-07-08-search-api.md) |
| `videos.content_type` | not defined | [07-04](specs/epics/07-api-server/plan-07-04-video-crud.md), [07-17](specs/epics/07-api-server/plan-07-17-graphql-schema.md), [09-10](specs/epics/09-library-management/plan-09-10-content-type-classifier.md) |
| `videos.poster_url` | `videos.poster_path` (line 1316) | [07-04](specs/epics/07-api-server/plan-07-04-video-crud.md), [07-21](specs/epics/07-api-server/plan-07-21-recommendations.md) |
| `videos.mime` | not defined; `media_info.container` exists | [plan-08-15:343-392](specs/epics/08-streaming/plan-08-15-probe-cache.md), [08-03:140](specs/epics/08-streaming/plan-08-03-direct-play.md), [08-13:240](specs/epics/08-streaming/plan-08-13-posters-sprites.md) |
| `videos.size` | `videos.size_bytes` (line 1310) | [plan-09-04:273](specs/epics/09-library-management/plan-09-04-content-hash-dedup.md), [09-06:374-377](specs/epics/09-library-management/plan-09-06-manual-scan.md), [09-03:152](specs/epics/09-library-management/plan-09-03-periodic-sweep.md) |
| `videos.deleted_at` / `libraries.deleted_at` | not defined | [09-03:261](specs/epics/09-library-management/plan-09-03-periodic-sweep.md), [09-15:200](specs/epics/09-library-management/plan-09-15-library-deletion.md) |
| `transcript_segments.embedding` BYTEA | embeddings live in ChromaDB only (§8.4) | [09-09:354](specs/epics/09-library-management/plan-09-09-topic-tag.md), [09-18:213](specs/epics/09-library-management/plan-09-18-chapter-inference.md) |
| `transcripts.detected_language`, `transcripts.language_confidence` | only `transcripts.language` (line 1357) | [09-08](specs/epics/09-library-management/plan-09-08-language-tag.md) |
| `subtitle_tracks` | only `subtitle_files` exists | [plan-08-15:379-388](specs/epics/08-streaming/plan-08-15-probe-cache.md) |
| `audit_log` | not defined; introduced by Epic 09/10 | referenced by [07-04:380-388](specs/epics/07-api-server/plan-07-04-video-crud.md) before owner epic lands |
| `events` table | not defined; introduced by [07-16](specs/epics/07-api-server/plan-07-16-websocket-fanout.md) | also written by [07-11:48-51](specs/epics/07-api-server/plan-07-11-watch-progress-sync.md) |
| `collections.library_id` | not defined | [plan-09-14](specs/epics/09-library-management/plan-09-14-smart-collections.md) lines 134, 187, 217, 235; [09-15:211](specs/epics/09-library-management/plan-09-15-library-deletion.md) |
| `videos_fts` | only `transcripts_fts` (line 1464) | [plan-07-04:133](specs/epics/07-api-server/plan-07-04-video-crud.md) |
| `transcript_units` | not defined | [plan-07-08](specs/epics/07-api-server/plan-07-08-search-api.md) creates and reads it; nothing populates |

Beyond these, [plan-07-14](specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md)
silently extends `tags` (`name_fold`, `created_at`), `collections`
(`updated_at`), `collection_items` (`added_at`), and `speakers`
(`updated_at`) without any `ALTER TABLE` migrations — these CREATE-as-is
schemas will fail when sqlc compiles.

**Recommendation.** Decide table-by-table whether the architecture or the
plans are authoritative, then make a single sweeping reconciliation pass.
The most probable resolution is: add the columns to architecture (they're
operationally needed) and rename `videos.poster_url`→`poster_path`,
`videos.size`→`size_bytes`, `media_info.duration_sec`→`videos.duration_sec`
in the plans.

### 1.2 ID type drift (BIGSERIAL vs UUID) — affects Epics 07, 08, 09

`architecture.md §8` uses `BIGSERIAL` for `tags.id`, `chapters.id`,
`speakers.id`, `transcript_segments.id`, `transcript_words.id`,
`audio_tracks.id`, `subtitle_files.id`, `processing_jobs.id`. Multiple
plans treat these as `UUID` in their Go DTOs and SQL parameters.

| Plan | ID drift |
|------|----------|
| [plan-07-04](specs/epics/07-api-server/plan-07-04-video-crud.md) | treats `transcript_segments.id` as UUID in detail response |
| [plan-07-06](specs/epics/07-api-server/plan-07-06-transcript-window.md) | `Segment.ID uuid.UUID`; `SelectWordsForSegments` takes `$1::uuid[]` |
| [plan-07-07](specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md) | `subtitle_files.id` as `uuid.UUID` |
| [plan-07-08](specs/epics/07-api-server/plan-07-08-search-api.md) | `Match.SegmentID uuid.UUID` |
| [plan-07-14](specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md) | `RemoveVideoTag` passes `$2::uuid[]` for `tags.id` |
| [plan-09-11](specs/epics/09-library-management/plan-09-11-speakers.md) | migration creates `speakers.id UUID` and `segment_speakers.{segment,speaker}_id UUID`; will fail to FK against `transcript_segments.id BIGSERIAL` |
| [plan-09-12](specs/epics/09-library-management/plan-09-12-tag-crud.md) | `INSERT INTO tags (id, ...) VALUES ($1, ...)` implies UUID PK |
| [plan-09-18](specs/epics/09-library-management/plan-09-18-chapter-inference.md) | inserts `chapters.id` as `uuid4()` |
| [plan-08-11](specs/epics/08-streaming/plan-08-11-live-subtitle.md) | `transcript_segments.id` in DTO untyped but consumed as bigint elsewhere |

**[plan-09-11](specs/epics/09-library-management/plan-09-11-speakers.md) is
the most affected** — its migration is incompatible with `transcript_segments.id BIGSERIAL`
and will not apply.

**Recommendation.** Pin one type per table in architecture and rewrite
mismatching plans. UUIDs everywhere is operationally cleaner; BIGSERIAL is
storage-cheaper. Pick once.

### 1.3 Table name drift — affects Epic 07

Architecture uses `transcript_segments`, `transcript_words`,
`subtitle_files`, `transcripts_fts`. Several Epic 07 plans use the shorter
forms `segments`, `words`, `subtitles`, `videos_fts` throughout SQL,
migrations, and Go code.

| Drift | Canonical | Plan |
|-------|-----------|------|
| `segments` | `transcript_segments` | [plan-07-06](specs/epics/07-api-server/plan-07-06-transcript-window.md), [plan-07-08:471-479](specs/epics/07-api-server/plan-07-08-search-api.md) |
| `words` | `transcript_words` | [plan-07-06](specs/epics/07-api-server/plan-07-06-transcript-window.md) |
| `subtitles` | `subtitle_files` | [plan-07-07](specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md) |
| `videos_fts` | `transcripts_fts` | [plan-07-04:133](specs/epics/07-api-server/plan-07-04-video-crud.md) |

Mechanical search-and-replace.

### 1.4 Job FSM state-name casing — affects Epic 09

Architecture (line 1312, §3 FSM diagram lines 313-315) uses lowercase:
`'discovered', 'probed', 'audio_extracted', 'transcribed', 'indexed',
'thumbnailed', 'ready'`. Epic 09 plans use uppercase: `'DISCOVERED'`,
`'PROBED'`, etc. — and additionally introduce four new states (`MISSING`,
`SUPERSEDED`, `READY_NO_AUDIO`, `CORRUPTED`) ad-hoc across multiple plans.

| Plan | Uses uppercase / new states |
|------|------------------------------|
| [plan-09-02:478](specs/epics/09-library-management/plan-09-02-filesystem-watcher.md) | `state='MISSING'` |
| [plan-09-03:48,89,353](specs/epics/09-library-management/plan-09-03-periodic-sweep.md) | uppercase + `'DELETED'` |
| [plan-09-04:274,320](specs/epics/09-library-management/plan-09-04-content-hash-dedup.md) | `'DISCOVERED'` |
| [plan-09-06:156-169](specs/epics/09-library-management/plan-09-06-manual-scan.md) | full uppercase enum + four new states |
| [plan-09-09:237](specs/epics/09-library-management/plan-09-09-topic-tag.md) | `state IN ('INDEXED','READY','READY_NO_AUDIO')` |

**Recommendation.** Pick lowercase per architecture and update. Land a
single migration that owns the FSM extension (`MISSING`, `SUPERSEDED`,
`READY_NO_AUDIO`, `CORRUPTED`) — currently they appear scattered across
plans without a clear owner.

### 1.5 gRPC contract drift from `architecture.md §9.9` — affects Epics 07, 08

Architecture §9.9 defines exactly four RPCs per service:

```
service Pipeline   { Embed, Transcribe (stream), ListBackends, HealthCheck }
service Streaming  { OpenSession, CloseSession, EvictHashCache, HealthCheck }
```

Epic 07 invents Pipeline RPCs that don't exist:

| Invented RPC | Used by |
|--------------|---------|
| `Pipeline.Enqueue(library_id, stage, priority)` | [plan-07-03:62](specs/epics/07-api-server/plan-07-03-library-crud.md), [plan-07-05](specs/epics/07-api-server/plan-07-05-video-processing-control.md) |
| `Pipeline.EnqueueChain(video_id, from_stage, priority)` | [plan-07-05:264](specs/epics/07-api-server/plan-07-05-video-processing-control.md) |
| `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)` | [plan-07-18:127](specs/epics/07-api-server/plan-07-18-grpc-clients.md) |
| `Pipeline.RunSyntheticTranscribe(backend, config)` | [plan-07-15:323](specs/epics/07-api-server/plan-07-15-settings-system.md) |
| `Streaming.GetCapabilities()` | [plan-07-10:347](specs/epics/07-api-server/plan-07-10-streaming-session-lifecycle.md), [plan-07-18:145](specs/epics/07-api-server/plan-07-18-grpc-clients.md) |

Critically, [plan-07-18](specs/epics/07-api-server/plan-07-18-grpc-clients.md) — the canonical client
wrapper — does not include the two `Enqueue*` methods that
[plan-07-03](specs/epics/07-api-server/plan-07-03-library-crud.md) and
[plan-07-05](specs/epics/07-api-server/plan-07-05-video-processing-control.md) depend on.
The scan and process/reprocess flows have **no transport** as currently
specified.

Epic 08 is the converse: its proto in
[plan-08-08:100](specs/epics/08-streaming/plan-08-08-grpc-server.md) and
[plan-08-10:354](specs/epics/08-streaming/plan-08-10-concurrency-caps.md) is
a *superset* of architecture §9.9 (adds `GetCapabilities`, `WatchQueue`,
returns rich `OpenSessionResponse` instead of `Session`, returns
`EvictHashCacheResponse{entries_removed, artifacts}` instead of `Empty`).
The additions are sensible; just need to land in architecture.

**Recommendation.** Update architecture §9.9 to:
1. Add the Streaming extensions (`GetCapabilities`, `WatchQueue`, richer responses).
2. Decide whether bulk job control flows through gRPC (then add `Pipeline.Enqueue*`)
   or through direct DB writes to `processing_jobs` (then rewrite [plan-07-03](specs/epics/07-api-server/plan-07-03-library-crud.md)
   and [plan-07-05](specs/epics/07-api-server/plan-07-05-video-processing-control.md) to insert rows
   instead of calling gRPC). Architecture lines 138-142 lean toward the latter
   ("bulk job control flows through Postgres, not gRPC"); the plans assume the former.
3. Drop `Pipeline.RunSyntheticTranscribe` (use `Pipeline.Transcribe` with a
   fixture audio source) and `Pipeline.ExtractEmbeddedSubtitle` (Pipeline can
   ship a thin add-on RPC if architecture endorses it).

### 1.6 Stage list drift — `subtitle_gen` — affects Epics 07, 09

Architecture references the canonical six stages: `scan, probe, extract,
transcribe, index, thumbnail`. Epic 07
([plan-07-05](specs/epics/07-api-server/plan-07-05-video-processing-control.md),
[plan-07-12](specs/epics/07-api-server/plan-07-12-job-control.md),
[plan-07-13](specs/epics/07-api-server/plan-07-13-queue-stats.md)) and
Epic 11 ([plan-11-02](specs/epics/11-web-ui/plan-11-02-video-detail-page.md))
include a seventh stage `subtitle_gen`. Architecture §3.5 describes
subtitle generation behavior but no separate pipeline stage.

**Recommendation.** Either canonicalize `subtitle_gen` in §3 (likely;
generation is a real pipeline step) or fold it into the `transcribe` stage
output (less invasive but loses progress visibility).

### 1.7 `devices` table double-owned — affects Epics 07, 12

| Plan | Migration | Schema | Unique key |
|------|-----------|--------|------------|
| [plan-07-22](specs/epics/07-api-server/plan-07-22-devices-register.md):73-95 | `0022_devices.sql` | `(id, user_id, platform, push_token, bundle_id, app_version, locale, registered_at, last_seen_at, revoked_at)` | `(user_id, platform, push_token)` |
| [plan-12-10](specs/epics/12-mobile/plan-12-10-device-registration-api.md):23-41 | `0040_devices.sql` | `(id, user_id, platform, token, token_hash GENERATED, app_version, os_version, locale, categories JSONB, created_at, last_seen_at, revoked_at)` | `(user_id, token_hash)` |

Both migrations create the same table with different shapes. Field name
disagrees: `push_token` (07-22) vs `token` (12-10).
[plan-12-04](specs/epics/12-mobile/plan-12-04-push-notifications.md):67-74
uses `token`; the validator on
[plan-07-22:127-133](specs/epics/07-api-server/plan-07-22-devices-register.md) returns 422 if
`bundle_id` is missing — Plan 12-10 doesn't send it.

**Recommendation.** Keep the more featureful 12-10 design but **add back
`bundle_id`** (genuinely needed for APNs topic routing — one APNs cert per
bundle). Mark 07-22 as superseded; align migration number to a single value.

### 1.8 `audit_log.category` enum constraint conflict — affects Epics 09, 12

[plan-09-17:148](specs/epics/09-library-management/plan-09-17-library-audit.md)
declares `CHECK (category IN ('library','security'))`. Then
[plan-12-10](specs/epics/12-mobile/plan-12-10-device-registration-api.md):17,100
writes `category='device'` — INSERT will fail.

[plan-07-04:380-388](specs/epics/07-api-server/plan-07-04-video-crud.md)
also writes audit rows but the table itself isn't owned in Epic 07.

**Recommendation.** Either expand the CHECK enum (and reconcile with Epic 9's
partition strategy) or route device events under `category='security'`
(device registration is genuinely a security event).

### 1.9 `refresh_tokens.device_id` referenced but never defined — affects Epics 10, 12

[plan-12-11:52](specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md)
says "`refresh_tokens.device_id` is added by Epic 10 Story 10.3 — we depend
on it being present." Reading
[plan-10-03](specs/epics/10-auth-security/plan-10-03-native-login.md) and
[plan-10-04](specs/epics/10-auth-security/plan-10-04-token-refresh.md), the column
does not exist; `device_id` only appears on `pairing_codes`
([epic 10 README:108-119](specs/epics/10-auth-security/README.md)).

**Recommendation.** Either add the column to plan-10-03 explicitly, or
land an explicit `ALTER TABLE refresh_tokens ADD COLUMN device_id UUID
REFERENCES devices(id)` in plan-12-11's migration with backfill notes.
Currently neither plan owns it.

### 1.10 `device-pat` auth source referenced but never defined — affects Epics 10, 11, 12

[plan-12-11:38-49](specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md)
accepts auth from `ident.Source ∈ {'refresh', 'device-pat'}`. No plan in
Epic 10 or Epic 11 defines a `device-pat` source.
[plan-11-13](specs/epics/11-web-ui/plan-11-13-pat-management-api.md) defines PATs
as user-owned, not device-owned.

**Recommendation.** Either drop `device-pat` from
[plan-12-11](specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md) (rely on
refresh-token-bound sessions, matching Epic 10's model) or add an
explicit story to Epic 10/11 introducing device-scoped PATs.

### 1.11 Web UI calls endpoints not in Epic 07 — affects Epics 07, 11

The PWA (Epic 11) calls several endpoints that no Epic 07 plan defines:

| Endpoint | Web caller | Status in Epic 07 |
|----------|-----------|-------------------|
| `GET /api/jobs?state&stage&video&cursor` | [plan-11-05](specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md) | promised by architecture §9 line 1658, not in [plan-07-12](specs/epics/07-api-server/plan-07-12-job-control.md) |
| `POST /api/jobs:bulk-pause` (& variants) | [plan-11-05:139](specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md) | absent |
| `POST /api/jobs/{id}/priority` | [plan-11-05:32](specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md) | absent |
| `GET /api/videos/{id}/jobs` | [plan-11-02:49](specs/epics/11-web-ui/plan-11-02-video-detail-page.md) | absent (could be folded into GraphQL `videoDetail`) |
| `PATCH /api/me/playback-state` | [plan-11-02](specs/epics/11-web-ui/plan-11-02-video-detail-page.md), [plan-11-10](specs/epics/11-web-ui/plan-11-10-offline-pwa.md) | absent (currently only the session/progress route writes the table) |
| `POST /api/me/password` | [plan-11-06:107](specs/epics/11-web-ui/plan-11-06-settings-page.md) | absent in both Epic 07 and Epic 10 |

PAT management
([plan-11-13](specs/epics/11-web-ui/plan-11-13-pat-management-api.md)) and
sessions ([plan-11-14](specs/epics/11-web-ui/plan-11-14-active-sessions-api.md)) are
correctly fully owned within Epic 11 — that part is intentional.

The Web UI also has endpoint-name mismatches with existing Epic 07 plans:

| Plan-11 caller | Plan-07 owner | Mismatch |
|----------------|---------------|----------|
| [plan-11-02:113](specs/epics/11-web-ui/plan-11-02-video-detail-page.md) `GET /api/videos/{id}/transcript` | [plan-07-06:10](specs/epics/07-api-server/plan-07-06-transcript-window.md) `GET /api/videos/{id}/segments` | endpoint name |
| [plan-11-02:128](specs/epics/11-web-ui/plan-11-02-video-detail-page.md) `{ stage: 'transcribe' }` | [plan-07-05:55](specs/epics/07-api-server/plan-07-05-video-processing-control.md) `{ from_stage: 'transcribe' }` | request-body key |
| [plan-11-05:135](specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md) `POST /api/jobs/{id}/force-pause` | [plan-07-12:37](specs/epics/07-api-server/plan-07-12-job-control.md) `POST /api/jobs/{id}/pause?force=true` | route shape |
| [plan-11-01:14](specs/epics/11-web-ui/plan-11-01-library-browser.md) `GET /api/libraries/{id}/videos` | architecture §9 line 1658 `GET /api/videos?library_id=…` | route shape; both Epic 07 and Epic 11 may have drifted |

### 1.12 Architecture `lib[]` claim semantics: plan ↔ story conflict — affects Epic 10

[story-10-08-signed-url-minter.md:AC-1](specs/epics/10-auth-security/story-10-08-signed-url-minter.md)
says `lib=[library_id]` (singleton — just the resource's library).
[plan-10-08:144,163,181,301](specs/epics/10-auth-security/plan-10-08-signed-url-minter.md) deliberately
emits the user's *full* library set (the test
`TestMintIncludesUserLibsNotJustResourceLib` pins this).

A leaked direct-play URL with `lib=[L1, L2]` discloses that the user has
access to L2 — a real disclosure surface. The plan's reasoning (one mint
covers many resources) is operationally simpler but security-weaker.

**Recommendation.** Adopt the story's singleton; the access token
([plan-10-03](specs/epics/10-auth-security/plan-10-03-native-login.md)) is
the right place for the full snapshot.

### 1.13 Capabilities (Tauri 2 ACL) absent across Epic 13

Tauri 2 enforces ACL via `src-tauri/capabilities/*.json` — without these
files, every `invoke()`, plugin call, fs op, global-shortcut registration,
and updater call fails at runtime. **None of the eight Epic 13 plans
mention this directory.** This is the single biggest gap in Epic 13.

**Recommendation.** Add a shared "Capabilities" appendix to
[plan-13-01](specs/epics/13-desktop/plan-13-01-macos.md) (the de-facto base
plan) listing required capability JSONs, and cross-reference it from
[13-04](specs/epics/13-desktop/plan-13-04-system-tray.md), [13-05](specs/epics/13-desktop/plan-13-05-mdns-discovery.md),
[13-06](specs/epics/13-desktop/plan-13-06-drag-drop.md), [13-07](specs/epics/13-desktop/plan-13-07-keyboard-shortcuts.md),
[13-08](specs/epics/13-desktop/plan-13-08-auto-update.md).

### 1.14 Migration ownership and ordering

Several plans assume migrations from earlier epics that don't exist:

- [plan-09-09:§2.2](specs/epics/09-library-management/plan-09-09-topic-tag.md),
  [plan-09-10:§2.2](specs/epics/09-library-management/plan-09-10-content-type-classifier.md),
  [plan-09-18:§2.2](specs/epics/09-library-management/plan-09-18-chapter-inference.md) imply
  non-idempotent re-edits to `0010_processing_jobs.sql` (Epic 6). Migrations
  should be immutable; ship `ALTER`s instead.
- [plan-10-03:§6](specs/epics/10-auth-security/plan-10-03-native-login.md) calls
  `libACL.LibrariesForUser` — but `library_acl` is owned by
  [plan-10-13](specs/epics/10-auth-security/plan-10-13-permission-model.md), which
  lands later in the README sequence. Either stub `LibrariesForUser` or
  move the migration earlier.
- [plan-09-10](specs/epics/09-library-management/plan-09-10-content-type-classifier.md)
  is supposed to own `media_features` per the epic README, but the
  migration only adds `videos.content_type`. `media_features` migration is
  missing.

---

## 2. Epic 07 — API Server (22 plans)

**Migration numbers:** `0011..0022` are unique within the epic. **No route
conflicts** within the epic.

The recurring drift items in §1.1, §1.2, §1.3, §1.5, §1.6, §1.11 dominate.
Plan-specific findings beyond those:

### 2.1 Go-level bugs

| Plan | Issue |
|------|-------|
| [plan-07-01:144,213](specs/epics/07-api-server/plan-07-01-http-server-skeleton.md) | `RequestIDFromContext` referenced in `httperror` package but defined in `middleware` — circular import. Move the request-id key to `internal/reqid`. |
| [plan-07-02:104](specs/epics/07-api-server/plan-07-02-cursor-pagination.md) | Calls `httperror.BadRequest(...).With(...)` but the `httperror.Error` type from [plan-07-01](specs/epics/07-api-server/plan-07-01-http-server-skeleton.md) has no `With` method. Won't compile. |
| [plan-07-02:193-203](specs/epics/07-api-server/plan-07-02-cursor-pagination.md) | `paginate.Where` references `uuid.UUID` without importing `github.com/google/uuid`. |
| [plan-07-16:120,206,274](specs/epics/07-api-server/plan-07-16-websocket-fanout.md) | `Envelope.Payload` uses `json:",inline"` — not a real `encoding/json` tag (it's a YAML/`goccy` convention). Marshalling will produce a `{"Payload":...}` field literally, breaking subscribers. Also imports `nhooyr.io/websocket` instead of the renamed `github.com/coder/websocket` per architecture §1.2. |
| [plan-07-20:89,191](specs/epics/07-api-server/plan-07-20-health-version-metrics.md) | Declares both `var Version = "dev"` and `type Version struct {...}` in the same package — Go won't compile. Function `versionInfo` returns `Version` ambiguously. |

### 2.2 Cursor type mismatch

[plan-07-02](specs/epics/07-api-server/plan-07-02-cursor-pagination.md)'s
`Cursor.ID` is `uuid.UUID` only.
[plan-07-06](specs/epics/07-api-server/plan-07-06-transcript-window.md)
([for `transcript_segments` paging](specs/epics/07-api-server/plan-07-06-transcript-window.md:259-265))
and [plan-07-08](specs/epics/07-api-server/plan-07-08-search-api.md):569 (for
search results) need `(score, unit_id)` and `(transcript_id, seq)` cursor
variants that don't exist. Either generalize the cursor primitive or
specify per-resource cursor variants.

### 2.3 Inconsistent confirmation semantics

[plan-07-03](specs/epics/07-api-server/plan-07-03-library-crud.md) (libraries) requires `?purge=true&confirm=<name>`.
[plan-07-04](specs/epics/07-api-server/plan-07-04-video-crud.md) (videos) requires `?purge=true&confirm=<id>`.
Pick one for the epic.

### 2.4 Architecture `saved_searches` redefined

[plan-07-09:65-78](specs/epics/07-api-server/plan-07-09-saved-searches.md)
ships `0015_saved_searches.sql` re-creating the table with extra columns
`kind` and `updated_at`, conflicting with architecture §8.5
(lines 1521-1527) which already defines the table. Either ship an `ALTER`
or update architecture.

### 2.5 Problem-type constant registry not centralized

[plan-07-01](specs/epics/07-api-server/plan-07-01-http-server-skeleton.md)
declares `httperror/types.go` as the "single source of truth" for problem-type
URIs but enumerates only a fixed list. Plans 07-04, 07-05, 07-12, 07-15, 07-19
reference at least 10 additional constants
(`TypeBodyTooLarge`, `TypeRateLimited`, `TypeStreamingUnavailable`,
`TypeStageNotPerVideo`, `TypeJobTerminalOrMissing`, `TypeNotRuntime`,
`TypeAdminOnly`, `TypeCircuitOpen`, `TypeUnsupportedMediaType`, `TypeInvalidCursor`)
that aren't declared. Either expand 07-01's central list or relax the
"single source of truth" rule.

### 2.6 New routes outside architecture §9

- [plan-07-21](specs/epics/07-api-server/plan-07-21-recommendations.md):
  `/api/recommendations` not in §9.
- [plan-07-22](specs/epics/07-api-server/plan-07-22-devices-register.md):
  `/api/devices/*` not in §9.

Both are legitimate extensions. Update architecture §9.

### 2.7 Plans with no issues

[plan-07-19](specs/epics/07-api-server/plan-07-19-validation-rate-limiting.md)
is the cleanest. [plan-07-17](specs/epics/07-api-server/plan-07-17-graphql-schema.md)
is well-structured (only minor: claims a `playable: Boolean!` field that
would require an `fs.Stat` per video, contradicting [plan-07-04](specs/epics/07-api-server/plan-07-04-video-crud.md)'s
explicit guidance not to stat in list paths).

**No plan is too thin.** Every Epic 07 plan is ≥250 lines.

---

## 3. Epic 08 — Streaming (15 plans)

**Substantially solid.** Story → plan AC traceability is unusually
disciplined; FFmpeg invocations match architecture §4.4 reference command
lines almost verbatim; JWT/JWKS verification surface is consistent across
all 15 plans; cache layout matches §4.8.

### 3.1 Three blocking schema bugs

1. **[plan-08-15:343-392](specs/epics/08-streaming/plan-08-15-probe-cache.md)** references
   `videos.container` and `videos.mime`. Architecture §8.1 puts container
   on `media_info`; `mime` is undefined. Propagates to
   [plan-08-03:140](specs/epics/08-streaming/plan-08-03-direct-play.md) and
   [plan-08-13:240](specs/epics/08-streaming/plan-08-13-posters-sprites.md)
   which read `probe.Row.MIME`.
2. **[plan-08-15:379-388](specs/epics/08-streaming/plan-08-15-probe-cache.md)** SQL joins
   on a `subtitle_tracks` table; architecture defines only `subtitle_files`
   with a different shape (no `stream_index`, no `is_forced`).
3. **[plan-08-11:215-221](specs/epics/08-streaming/plan-08-11-live-subtitle.md)**
   query: `SELECT ... FROM transcript_segments WHERE video_id = $1`. The
   `video_id` column lives on `transcripts`, not `transcript_segments`.
   Needs JOIN through `transcripts`.

### 3.2 gRPC contract deviates from architecture §9.9

See §1.5. [plan-08-08:100](specs/epics/08-streaming/plan-08-08-grpc-server.md)
returns `OpenSessionResponse` (architecture says `Session`); adds
`GetCapabilities`; returns `EvictHashCacheResponse{entries_removed,
artifacts}` (architecture says `Empty`).
[plan-08-10:354](specs/epics/08-streaming/plan-08-10-concurrency-caps.md)
adds `WatchQueue` (server-streaming). All sensible extensions; just need
to land in architecture.

### 3.3 README schema drift

[epic 08 README](specs/epics/08-streaming/README.md):65 lists `closed_reason`
values `'api'|'idle'|'crash'|'evicted'`. [plan-08-09:117-121](specs/epics/08-streaming/plan-08-09-session-store.md)
CHECK constraint is wider: adds `'user-stop','admin-evict','hw_failed_software_failed','store-insert-failed'`.
Cross-plan use confirms the wider set is needed.

### 3.4 Architecture §9.4 audio-segment routes unimplemented

Architecture §9.4 line 1643 lists `GET /stream/{session_id}/audio/{lang}/seg-{n}.aac`
as a separate route, but no plan implements separate audio segments — the
HLS plan ([plan-08-05](specs/epics/08-streaming/plan-08-05-hls-transcode.md)) muxes audio into
the variant streams via `var_stream_map`, which matches the architecture's
own §4.4 reference command. Drop the separate route from §9.4.

### 3.5 `subtitle_files` table read by no plan

Architecture §4 lists `subtitle_files` as a streaming-touched table.
[plan-08-11:482](specs/epics/08-streaming/plan-08-11-live-subtitle.md) reads sidecar SRT
paths from `sess.SidecarSRT` (where does the path come from?) — probably
intended to flow from `subtitle_files`. Add a fetch step or an
explicit dependency on a Pipeline-side helper.

### 3.6 Smaller Go-level issues

| Plan | Issue |
|------|-------|
| [plan-08-05:336-344](specs/epics/08-streaming/plan-08-05-hls-transcode.md) | `%q` formatting in HLS master playlist for NAME/LANGUAGE attributes will escape Arabic/Hebrew to `\uXXXX`, breaking display. Use the `quoteEscape` helper from [plan-08-12](specs/epics/08-streaming/plan-08-12-chapter-delivery.md). |
| [plan-08-05:654](specs/epics/08-streaming/plan-08-05-hls-transcode.md) | Test asserts `var_stream_map` shape via joined-with-spaces string — quoting won't appear; test will fail. |
| [plan-08-08:322-331](specs/epics/08-streaming/plan-08-08-grpc-server.md) | `errors.As(err, &session.ErrResourceExhausted{})` with struct literal — won't compile; needs `var ere ...; if errors.As(err, &ere)`. |
| [plan-08-08:461](specs/epics/08-streaming/plan-08-08-grpc-server.md) | `m.Manager.Close(...)` self-reference; `Manager` doesn't have a `Manager` field. Should be `m.Close(...)`. |
| [plan-08-04:396](specs/epics/08-streaming/plan-08-04-direct-stream-remux.md) | hardcodes `ffprobe` binary in `validateRemuxOutput`; should read from `cfg.FFmpeg.ProbeBinary`. |
| [plan-08-09](specs/epics/08-streaming/plan-08-09-session-store.md) | SQLite migration variant for `streaming_sessions` not provided; reaper uses `FOR UPDATE SKIP LOCKED` (Postgres-only). SQLite uses `BEGIN IMMEDIATE`. |
| [plan-08-11:140-209](specs/epics/08-streaming/plan-08-11-live-subtitle.md) | Story 8.11 AC-5 says "lines wrap at the source language's natural break points"; plan only does bidi-isolation. Either soften the AC or add a wrap-rule pass. |

### 3.7 Type duplication

[plan-08-02](specs/epics/08-streaming/plan-08-02-capability-matrix.md) defines `caps.MediaInfo`;
[plan-08-15](specs/epics/08-streaming/plan-08-15-probe-cache.md) defines `probe.MediaInfo`.
[plan-08-08:365](specs/epics/08-streaming/plan-08-08-grpc-server.md) passes
`row.MediaInfo` to `caps.Decide`. Either merge the types or document the
conversion.

**No plan is too thin.** All 15 plans are 385-794 lines.

---

## 4. Epic 09 — Library Management (18 plans)

Many plans are individually well-thought-through but the epic has
**systemic schema drift from architecture.md**. The recurring items in
§1.1 (size, deleted_at, content_type, embedding, collections.library_id),
§1.2 (PK types — UUID vs BIGSERIAL), §1.4 (state casing) dominate.

### 4.1 Library roots: array vs table

Architecture §8.1 line 1297 has `libraries.roots TEXT[] NOT NULL`.

[plan-09-16:§3](specs/epics/09-library-management/plan-09-16-multi-root-overlap.md)
introduces a separate `library_roots` table (`0044_library_roots.sql`) but
**does not drop or migrate the existing `roots TEXT[]`**.
[plan-09-03:336](specs/epics/09-library-management/plan-09-03-periodic-sweep.md) and
[plan-09-15:184,207,259,322](specs/epics/09-library-management/plan-09-15-library-deletion.md) read
from `library_roots`; [plan-09-02:405](specs/epics/09-library-management/plan-09-02-filesystem-watcher.md)
still iterates `library.roots` (the TEXT[]).

Two sources of truth, never reconciled. Pick one.

### 4.2 [plan-09-11](specs/epics/09-library-management/plan-09-11-speakers.md) is schema-incompatible

Migration creates `speakers.id UUID PRIMARY KEY` and FK references
`segment_speakers.segment_id UUID REFERENCES transcript_segments(id)`.
Since architecture defines `transcript_segments.id` and `speakers.id` as
`BIGSERIAL`, the migration cannot apply (UUID FK can't reference BIGSERIAL
PK). This is the worst-affected plan in the epic.

### 4.3 [plan-09-04](specs/epics/09-library-management/plan-09-04-content-hash-dedup.md)
content_hash type changes silently

Architecture line 1307: `content_hash TEXT NOT NULL UNIQUE`. Plan migration
(lines 175,180,183,447) declares `content_hash BYTEA` with `octet_length=32`
CHECK and a *partial* unique index `WHERE content_hash IS NOT NULL` —
changes the type, drops `NOT NULL`, replaces the index. The plan calls
this "defensive" but it's not idempotent against the architecture column.

The plan's `INSERT … ON CONFLICT (content_hash) WHERE content_hash IS NOT NULL DO UPDATE`
also has a subtle bug: `(xmax = 0)` cannot distinguish "updated" from
"no-op skipped" reliably; the audit-write logic at lines 291-310 then
re-reads the row, which is OK but the early `inserted` flag is
misleading.

### 4.4 Channel name proliferation

Architecture §5.1 specifies `channel = "videos.new"`. Plans add nine more:
`library.settings_changed` (09-01), `library.sweep_done` (09-03),
`library.deleted` (09-15), `library.topics_updated` (09-09),
`video.topics_updated`, `video.speakers_updated`, `speaker.renamed`,
`library.speakers_merged` (09-11), `video.chapters_updated` (09-18). All
follow the dotted convention and are coherent. **Recommendation:**
consolidate the canonical list in
[`pipeline/db/pubsub.py`](specs/epics/09-library-management/plan-09-01-library-config-schema.md)
per [plan-09-01:§2.2](specs/epics/09-library-management/plan-09-01-library-config-schema.md).

### 4.5 Audit-write semantics inconsistent

[plan-09-17:§2.3](specs/epics/09-library-management/plan-09-17-library-audit.md) says
`Writer.Write` "never blocks; never propagates."
[plan-09-04:§4.3](specs/epics/09-library-management/plan-09-04-content-hash-dedup.md) and
[plan-09-15:§4.1](specs/epics/09-library-management/plan-09-15-library-deletion.md) `await
audit.write(...)` inline. Pick one and align.

### 4.6 Plans by quality

- **Clean:** [09-01](specs/epics/09-library-management/plan-09-01-library-config-schema.md), [09-05](specs/epics/09-library-management/plan-09-05-ignore-rules.md), [09-13](specs/epics/09-library-management/plan-09-13-collections-manual.md), [09-17](specs/epics/09-library-management/plan-09-17-library-audit.md).
- **Good design, external references needed:** [09-07](specs/epics/09-library-management/plan-09-07-library-stats.md), [09-08](specs/epics/09-library-management/plan-09-08-language-tag.md), [09-12](specs/epics/09-library-management/plan-09-12-tag-crud.md), [09-14](specs/epics/09-library-management/plan-09-14-smart-collections.md).
- **Significant fixes needed:** [09-02](specs/epics/09-library-management/plan-09-02-filesystem-watcher.md), [09-03](specs/epics/09-library-management/plan-09-03-periodic-sweep.md), [09-04](specs/epics/09-library-management/plan-09-04-content-hash-dedup.md), [09-06](specs/epics/09-library-management/plan-09-06-manual-scan.md), [09-09](specs/epics/09-library-management/plan-09-09-topic-tag.md), [09-10](specs/epics/09-library-management/plan-09-10-content-type-classifier.md), [09-15](specs/epics/09-library-management/plan-09-15-library-deletion.md), [09-16](specs/epics/09-library-management/plan-09-16-multi-root-overlap.md), [09-18](specs/epics/09-library-management/plan-09-18-chapter-inference.md).
- **Schema-incompatible:** [09-11](specs/epics/09-library-management/plan-09-11-speakers.md).

---

## 5. Epic 10 — Auth & Security (17 plans)

The plans are unusually thick and internally consistent. Beyond the
cross-cutting items (§1.9 `refresh_tokens.device_id`, §1.12 `lib[]`
semantics), the issues are mostly cross-plan friction.

### 5.1 [plan-10-08](specs/epics/10-auth-security/plan-10-08-signed-url-minter.md) `lib[]` story↔plan contradiction

See §1.12. Plan deliberately puts the user's full library set in every
signed URL ([test `TestMintIncludesUserLibsNotJustResourceLib`](specs/epics/10-auth-security/plan-10-08-signed-url-minter.md:301)
pins the deviation); story AC-1 says singleton. Decide before implementation.

### 5.2 [plan-10-16](specs/epics/10-auth-security/plan-10-16-security-audit.md) dedupe index on partitioned table

[plan-10-16:187-194](specs/epics/10-auth-security/plan-10-16-security-audit.md):

```sql
CREATE UNIQUE INDEX IF NOT EXISTS audit_log_security_dedupe
    ON audit_log (dedupe_key)
    WHERE category = 'security' AND dedupe_key IS NOT NULL;
```

PostgreSQL 12+ permits unique indexes on partitioned tables only when the
partition key is included. If `audit_log` is partitioned by `created_at`
monthly per [plan-09-17:364](specs/epics/09-library-management/plan-09-17-library-audit.md), this
index needs `(created_at, dedupe_key)` to apply to the parent.

### 5.3 [plan-10-06](specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md) stores private keys in DB

[plan-10-06:156-159](specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md) stores rotated
private keys in a `jwt_keys` table. Architecture §11.5 explicitly says
"secrets only in env or config file" — this is a deliberate deviation, the
plan acknowledges, with "encryption-at-rest" as mitigation. Architecture
decision needed before merge.

### 5.4 README schema stale

[epic 10 README](specs/epics/10-auth-security/README.md):

- Lines 50-60 (`users` table): missing `failed_attempts` and `locked_until`
  columns added by [plan-10-01:§3](specs/epics/10-auth-security/plan-10-01-user-store.md).
- Lines 88-102 (`refresh_tokens.hash`): says "argon2id(token)";
  [plan-10-03:§5](specs/epics/10-auth-security/plan-10-03-native-login.md) hashes only the secret half.
- Lines 107-119 (`pairing_codes.code`): plaintext PK.
  [plan-10-17:§3](specs/epics/10-auth-security/plan-10-17-auth-pair.md) replaces with `code_hash`.

Update README to match plans.

### 5.5 Audit emitters missing for vocabulary entries

[plan-10-16:§2.4](specs/epics/10-auth-security/plan-10-16-security-audit.md) enumerates 14
event types. Three lack emitters:

- `password.changed` — natural emitter is [plan-10-01:468-470](specs/epics/10-auth-security/plan-10-01-user-store.md).
- `streaming.direct.access` — story-10-08 mentions; should be emitted by [plan-10-07](specs/epics/10-auth-security/plan-10-07-streaming-jwt-verify.md) middleware.
- `permission.denied` — natural emitter is [plan-10-13:RequireAdmin](specs/epics/10-auth-security/plan-10-13-permission-model.md) on 403.

### 5.6 Reaper inconsistency

[plan-10-02](specs/epics/10-auth-security/plan-10-02-web-login.md) and
[plan-10-03](specs/epics/10-auth-security/plan-10-03-native-login.md) add reaper indexes
(`web_sessions`, `refresh_tokens`) but no actual reaper code lands.
[plan-10-17](specs/epics/10-auth-security/plan-10-17-auth-pair.md) ships a Python reaper for
pairing codes. Either drop the unused reaper indexes or add a unified
pipeline reaper task that handles all three tables.

### 5.7 Sequencing bug

[epic 10 README:149-151](specs/epics/10-auth-security/README.md) sequences `10.1 → 10.6 → 10.15 → 10.2/10.10 → 10.3/10.4 → 10.5 → 10.7/10.8 → 10.9 → 10.11/10.12 → 10.13 → 10.14 → 10.16 → 10.17`. But
[plan-10-03:§6,414-422](specs/epics/10-auth-security/plan-10-03-native-login.md) calls
`libACL.LibrariesForUser`, owned by [plan-10-13](specs/epics/10-auth-security/plan-10-13-permission-model.md). Either stub `LibrariesForUser` until 10.13 lands, or move
`library_acl` migration earlier.

### 5.8 [plan-10-17](specs/epics/10-auth-security/plan-10-17-auth-pair.md):527-529 changes story EC

Plan changes "404 on concurrent claim race" to "409 pair-code-already-claimed".
Update story to match (the 409 is more correct).

### 5.9 Smaller polish

- [plan-10-02:263-266](specs/epics/10-auth-security/plan-10-02-web-login.md) dummy hash comment
  says "generated from 'x'" — actually any valid PHC string works for
  timing parity since argon2 cost dominates.
- [plan-10-08:217-222](specs/epics/10-auth-security/plan-10-08-signed-url-minter.md) maps `want=0`
  TTL to `max=86400`. A zero or negative request is a bug; mapping to
  24 h hides it for 24 h. Return a small safe default instead.
- [plan-10-09:321-324](specs/epics/10-auth-security/plan-10-09-single-user-mode.md)
  `X-Maktaba-Admin-Token` header in the redact list isn't used anywhere
  else; either drop or add as third surface.
- [plan-10-12:244](specs/epics/10-auth-security/plan-10-12-rate-limiting-auth.md) test name
  `TestLockoutBeats RateLimitOrder` has a stray space.
- [plan-10-07:440](specs/epics/10-auth-security/plan-10-07-streaming-jwt-verify.md) test name
  `TestRotationPickedUpVia LISTEN` has a stray space.

### 5.10 Audiences not in architecture

Architecture line 1645 lists only `/stream/direct/{video_id}`. Epic 10
introduces `streaming-static` (sprites/posters) and `streaming` (manifest/
segments) audiences. Add to architecture §9.4 / §9.7.

**No plan is too thin.** [plan-10-10](specs/epics/10-auth-security/plan-10-10-csrf-protection.md)
and [plan-10-12](specs/epics/10-auth-security/plan-10-12-rate-limiting-auth.md) could be
slightly trimmed but the depth is reasonable for security-critical
primitives.

---

## 6. Epic 11 — Web UI / PWA (14 plans)

**Strongest epic.** Internally consistent, story → plan AC mapping is
tight, RTL/i18n/accessibility taken seriously, PAT
([plan-11-13](specs/epics/11-web-ui/plan-11-13-pat-management-api.md)) and Sessions
([plan-11-14](specs/epics/11-web-ui/plan-11-14-active-sessions-api.md)) correctly own their
backends end-to-end (Epic 10 deliberately doesn't have phantom plans for
them).

The cross-cutting issues in §1.11 (web calls endpoints not in Epic 07)
dominate the findings. Plan-specific items beyond those:

### 6.1 Routing library reconciliation

Architecture §2.1 names **TanStack Router**.
[plan-11-01:12](specs/epics/11-web-ui/plan-11-01-library-browser.md) and
[plan-11-12:67](specs/epics/11-web-ui/plan-11-12-i18n-rtl.md) use React Router v6
shape. Pick one — likely TanStack per architecture — and update Epic 11
routing snippets.

### 6.2 TanStack Query v5 dropped `keepPreviousData`

[plan-11-04:105](specs/epics/11-web-ui/plan-11-04-search-interface.md) uses
`keepPreviousData: true`; in v5 it's `placeholderData: keepPreviousData`
(imported helper).

### 6.3 WebSocket multiplexing description vs reality

[plan-11-02:58](specs/epics/11-web-ui/plan-11-02-video-detail-page.md) describes
`lib/ws.ts` as "one socket, fan-in by topic" — but the server has three
separate `/ws/*` routes, requiring three sockets. Clarify the description
to "one socket per channel, fanned-in by `useWsTopic` hook."

### 6.4 If-Match with timestamp

[plan-11-06:81](specs/epics/11-web-ui/plan-11-06-settings-page.md) uses `If-Match: <updated_at>`
where the standard idiom is an ETag or `If-Unmodified-Since: <updated_at>`.
Either is fine if documented; current shape is mid-way.

### 6.5 Manifest fields not enumerated

[plan-11-10:50](specs/epics/11-web-ui/plan-11-10-offline-pwa.md) declares
`manifest.webmanifest` but doesn't list contents (icons 192/512,
theme_color, background_color, display: 'standalone', start_url, id, lang,
dir). Add a sub-section.

### 6.6 PWA cache budget exceeds Safari

[plan-11-10](specs/epics/11-web-ui/plan-11-10-offline-pwa.md) budget = 50 MB images +
5 MB API + 10 MB shell = 65 MB. Safari ITP cap is around 50 MB per origin.
Add a Safari-specific budget (≤ 45 MB total) and an LRU-trim path keyed on
`navigator.storage.estimate()`.

### 6.7 Plans with no issues

Seven of 14 plans are clean:
[11-07](specs/epics/11-web-ui/plan-11-07-responsive-design.md), [11-08](specs/epics/11-web-ui/plan-11-08-dark-light-theme.md),
[11-09](specs/epics/11-web-ui/plan-11-09-keyboard-shortcuts.md), [11-11](specs/epics/11-web-ui/plan-11-11-accessibility.md),
[11-12](specs/epics/11-web-ui/plan-11-12-i18n-rtl.md), [11-13](specs/epics/11-web-ui/plan-11-13-pat-management-api.md), [11-14](specs/epics/11-web-ui/plan-11-14-active-sessions-api.md).

---

## 7. Epic 12 — Mobile / Capacitor (11 plans)

The cross-cutting items §1.7 (`devices` table double-owned), §1.8
(`audit_log.category='device'`), §1.9 (`refresh_tokens.device_id`),
§1.10 (`device-pat`) cover the four blocking conflicts. Plan-specific:

### 7.1 HLS offline downloads broken

[plan-12-06:68](specs/epics/12-mobile/plan-12-06-offline-downloads.md) downloads HLS via
`URLSessionDownloadTask` (iOS) and `DownloadManager` (Android) against the
manifest URL. Both download only the manifest text — segments are never
fetched. Real offline HLS uses
[`AVAssetDownloadTask`](https://developer.apple.com/documentation/avfoundation/avassetdownloadtask)
on iOS and ExoPlayer's `DownloadHelper` + `DownloadService` on Android.

**Fix options:**
- (a) Replace with the platform-native HLS download APIs.
- (b) Scope offline to direct-play files only and document the HLS gap.

### 7.2 Background `POST` claim on iOS overstates the OS

[plan-12-05:97](specs/epics/12-mobile/plan-12-05-background-playback.md) implies
periodic 10 s progress `POST`s while the app is backgrounded.
`URLSessionConfiguration.background` is for downloads/uploads only, and
recurring background execution requires `BGTaskScheduler`. While audio is
playing the app is awake and the `POST`s work; the plan should say so
explicitly.

### 7.3 Manifest `MediaPlaybackService` missing

[plan-12-05:59-83](specs/epics/12-mobile/plan-12-05-background-playback.md) introduces
`MediaPlaybackService` (Android `mediaPlayback` foreground type), but
[plan-12-02:64](specs/epics/12-mobile/plan-12-02-android-app.md)'s `AndroidManifest.xml`
example only declares `DownloadForegroundService` (`dataSync`). Add the
second `<service ... foregroundServiceType="mediaPlayback"/>`.

### 7.4 Server `.well-known` route has no owner

[plan-12-09:18](specs/epics/12-mobile/plan-12-09-deep-linking.md) declares
`apple-app-site-association` and Android `assetlinks.json` as static JSON
files, but no Epic 7 plan owns the static-file route. Add a static-file
handler to [plan-07-01](specs/epics/07-api-server/plan-07-01-http-server-skeleton.md) or open
a thin Epic 7 plan.

### 7.5 Hard-coded `maktaba.local` hostnames

[plan-12-01:55](specs/epics/12-mobile/plan-12-01-ios-app.md), [plan-12-02](specs/epics/12-mobile/plan-12-02-android-app.md), [plan-12-09:57,84](specs/epics/12-mobile/plan-12-09-deep-linking.md) hard-code
`maktaba.local`. Self-hosters use arbitrary hostnames. No plan owns
"first-launch server URL onboarding for the Capacitor app." Add a
sentence to [plan-12-01:§0](specs/epics/12-mobile/plan-12-01-ios-app.md) deferring to
[plan-10-17](specs/epics/10-auth-security/plan-10-17-auth-pair.md) (auth-pair) or open a
config story.

### 7.6 Bundle-size budget mismatch

[epic 12 README:51-52](specs/epics/12-mobile/README.md) `≤ 500 KB gzipped` vs
[plan-12-01:144](specs/epics/12-mobile/plan-12-01-ios-app.md) `≤ 750 KB`. Pick one.

### 7.7 [plan-12-03](specs/epics/12-mobile/plan-12-03-native-player.md) defects

- `intent.getStringExtra("sessionId")!!` (line 167) force-unwraps;
  crashes if web ever omits it.
- `Data(contentsOf: url)` in `configureNowPlaying` (lines 110-113) is
  synchronous network read on the main `open` path — can hang on cellular.
- Race: `players[handle]` assigned after async `present` completes;
  `close({handle})` arriving before insertion fails.

### 7.8 Inline AirPlay button bridging unspecified

[plan-12-07](specs/epics/12-mobile/plan-12-07-share-cast.md) promises an
`AVRoutePickerView` next to the inline web player but doesn't specify the
UIKit→WebView bridge plugin. Either drop the inline button (rely on
`AVPlayerViewController`'s built-in routes button in fullscreen) or add a
small plugin spec.

### 7.9 Plans with no issues

[plan-12-08](specs/epics/12-mobile/plan-12-08-haptics.md) is appropriately concise and clean.

---

## 8. Epic 13 — Desktop / Tauri (8 plans)

The cross-cutting items §1.13 (capabilities absent) and the missing
single-instance lock dominate. Plan-specific:

### 8.1 [plan-13-06](specs/epics/13-desktop/plan-13-06-drag-drop.md) — Tauri 1 API + security gaps

Most affected plan in the epic.

**Compile bugs:**
- Line 27: uses `WindowEvent::FileDrop(FileDropEvent::Dropped {...})`.
  Tauri 2 renamed this to `WindowEvent::DragDrop(DragDropEvent::Drop)`.
- Line 10: `WebviewWindowBuilder::on_drop_event` — actual Tauri 2 API is
  `on_drag_drop_event`.

**Security gaps:**
- No path-traversal validation. Dropped paths forwarded to
  `POST /api/libraries/{id}/files` without checking they're under
  user-allowed roots; a tampered Finder window could submit `/etc/shadow`.
- `fs.copyFile` / `fs.renameFile` used (line 3) without configuring the
  `tauri-plugin-fs` allowlist or capability JSON; will fail at runtime.
- No mention of the per-window `dragDropEnabled` flag in `tauri.conf.json`.

**Other:**
- Modifier detection via window keydown/keyup is fragile; OS captures
  keyboard during native drag. `DragDropEvent` doesn't carry modifiers; use
  HTML5 `dragover` event instead.
- Pool helper `pool(concurrency, items, fn)` invented; not defined or
  imported.

### 8.2 [plan-13-08](specs/epics/13-desktop/plan-13-08-auto-update.md) — bad Tauri APIs

- `{{channel}}` is **not** a built-in Tauri-updater substitution (only
  `{{target}}`, `{{arch}}`, `{{current_version}}` are). Plan needs a
  custom `set_endpoints` call after reading the channel file at boot.
- `tauri::Manager::config_mut` (referenced in §4) is **not a real Tauri 2
  API**. The proper approach is templated endpoint substitution via a
  startup-injected variable.
- Signing-key management is one line; should expand to cover key
  generation (`tauri signer generate`), rotation playbook, public-key
  storage in source control, HSM/CI secrets.

### 8.3 Universal binary vs per-arch updates

[plan-13-01:§7](specs/epics/13-desktop/plan-13-01-macos.md) builds `universal-apple-darwin`.
[plan-13-08:§2](specs/epics/13-desktop/plan-13-08-auto-update.md) manifest publishes
`darwin-aarch64` and `darwin-x86_64` separately. Pick one: either build
per-arch DMGs or use a single universal manifest entry.

### 8.4 [plan-13-04](specs/epics/13-desktop/plan-13-04-system-tray.md) prose error

Header references `tauri-plugin-system-tray` — that plugin doesn't exist;
the tray is a built-in Tauri 2 core feature (`tauri::tray::*`). The code
itself is correct; just fix the prose.

### 8.5 [plan-13-04:§2](specs/epics/13-desktop/plan-13-04-system-tray.md) lifetime bug

`build_menu` passes `&MenuItemBuilder::with_id(...).build(app).unwrap()`
inside `iter().map()` — the `MenuItemBuilder` temporary is dropped before
the `Vec<&MenuItem>` is used. Won't compile.

### 8.6 [plan-13-02:62](specs/epics/13-desktop/plan-13-02-windows.md) `MainActivity` typo

`MainActivity` is Android nomenclature; on Windows this should be the Rust
`main.rs` argv parser. Also the `app://open-shortcut` URI scheme is
invented; no plan registers it as a Tauri custom protocol.

### 8.7 [plan-13-03](specs/epics/13-desktop/plan-13-03-linux.md) gaps

- §2 `.desktop` file registers `application/x-mpegurl` (HLS) but
  [plan-13-06](specs/epics/13-desktop/plan-13-06-drag-drop.md) doesn't handle dropping `.m3u8`.
- §5 AppImage portability bundles gstreamer plugins (≤ 90 MB), conflicting
  with architecture §6.4 line 835 promise of "~10 MB binary." Note the
  contradiction.
- No `avahi-daemon` probe; mDNS depends on it.

### 8.8 [plan-13-07:§6](specs/epics/13-desktop/plan-13-07-keyboard-shortcuts.md) "private window"

Passes `?private=1` query string, but Tauri 2 supports per-window data
dirs via `WebviewWindowBuilder::data_directory(...)` — stronger isolation.
Sharing the OS-level data dir means private mode leaks.

### 8.9 Cross-plan: no shared scaffolding plan

Story-by-platform structure means [plan-13-01](specs/epics/13-desktop/plan-13-01-macos.md) doubles as
"the base scaffolding" but isn't labeled that way. [Epic 13 README](specs/epics/13-desktop/README.md)
should explicitly call it the base.

---

## 9. Plans by quality

**Clean / minor notes only (11 plans):**
- 07: [07-19](specs/epics/07-api-server/plan-07-19-validation-rate-limiting.md), [07-17](specs/epics/07-api-server/plan-07-17-graphql-schema.md) (lightly noted)
- 08: [08-02](specs/epics/08-streaming/plan-08-02-capability-matrix.md), [08-07](specs/epics/08-streaming/plan-08-07-hwaccel-detect.md), [08-12](specs/epics/08-streaming/plan-08-12-chapter-delivery.md), [08-13](specs/epics/08-streaming/plan-08-13-posters-sprites.md), [08-14](specs/epics/08-streaming/plan-08-14-cache-gc.md)
- 09: [09-01](specs/epics/09-library-management/plan-09-01-library-config-schema.md), [09-05](specs/epics/09-library-management/plan-09-05-ignore-rules.md), [09-13](specs/epics/09-library-management/plan-09-13-collections-manual.md), [09-17](specs/epics/09-library-management/plan-09-17-library-audit.md)
- 11: [11-07](specs/epics/11-web-ui/plan-11-07-responsive-design.md), [11-08](specs/epics/11-web-ui/plan-11-08-dark-light-theme.md), [11-09](specs/epics/11-web-ui/plan-11-09-keyboard-shortcuts.md), [11-11](specs/epics/11-web-ui/plan-11-11-accessibility.md), [11-12](specs/epics/11-web-ui/plan-11-12-i18n-rtl.md), [11-13](specs/epics/11-web-ui/plan-11-13-pat-management-api.md), [11-14](specs/epics/11-web-ui/plan-11-14-active-sessions-api.md)
- 12: [12-08](specs/epics/12-mobile/plan-12-08-haptics.md)
- 13: [13-04](specs/epics/13-desktop/plan-13-04-system-tray.md), [13-05](specs/epics/13-desktop/plan-13-05-mdns-discovery.md)

**Plans that are too thin:**

None of the 105 plans are too thin. The shortest plans
([plan-09-05](specs/epics/09-library-management/plan-09-05-ignore-rules.md), [plan-08-12](specs/epics/08-streaming/plan-08-12-chapter-delivery.md),
[plan-13-03](specs/epics/13-desktop/plan-13-03-linux.md), [plan-12-08](specs/epics/12-mobile/plan-12-08-haptics.md))
are concise relative to scope, not under-specified. Two sub-sections are
thin and should be expanded:

- [plan-13-08 §"Signing & key management"](specs/epics/13-desktop/plan-13-08-auto-update.md) — one line; expand to its own subsection.
- [plan-11-10 manifest fields](specs/epics/11-web-ui/plan-11-10-offline-pwa.md) — enumerate icons / theme_color / etc.

---

## 10. Recommended action order

Roughly in priority order:

### 10.1 Architecture decisions (block everything else)

1. Reconcile `architecture.md §8` (canonical schema) with the
   plans in §1.1 — pick architecture or pick plans for each drift item;
   land a single sweeping update.
2. Reconcile `architecture.md §9.9` (gRPC contract) with §1.5 —
   either canonicalize the additional Streaming RPCs and Pipeline
   `Enqueue*` (and their response shapes), or rewrite Epic 07 to enqueue
   via direct DB writes.
3. Resolve §1.12 ([plan-10-08](specs/epics/10-auth-security/plan-10-08-signed-url-minter.md)
   `lib[]` semantics): singleton vs full set.
4. Decide on `subtitle_gen` stage canonicalization (§1.6).
5. Decide on `transcript_segments.embedding` source: Postgres BYTEA
   (extra column + index) vs ChromaDB (rewrite plans 09-09, 09-18) (§1.1).
6. Pin ID types per table (UUID vs BIGSERIAL) — §1.2.
7. Pick library-roots model: `libraries.roots TEXT[]` vs `library_roots`
   table (§4.1).
8. Pick `audit_log.category` enum: extend to include `'device'` or
   reroute device events under `'security'` (§1.8).

### 10.2 Single-owner schema sweeps

9. Owner for FSM extension (`MISSING`, `SUPERSEDED`, `READY_NO_AUDIO`,
   `CORRUPTED`) — currently scattered across plans 09-02/03/06.
10. Owner for `media_features` table — referenced by plan-09-10 but no migration.
11. Owner for `refresh_tokens.device_id` — plan-10-03 or plan-12-11.
12. Owner for `apple-app-site-association` static-file route — plan-07-01 or new plan.

### 10.3 Critical compile bugs

13. [plan-09-11](specs/epics/09-library-management/plan-09-11-speakers.md) migration (UUID FK against BIGSERIAL).
14. [plan-07-16:120](specs/epics/07-api-server/plan-07-16-websocket-fanout.md) `json:",inline"`.
15. [plan-07-20](specs/epics/07-api-server/plan-07-20-health-version-metrics.md) `Version` symbol clash.
16. [plan-07-02:104](specs/epics/07-api-server/plan-07-02-cursor-pagination.md) `httperror.With()` missing.
17. [plan-08-08:322,461](specs/epics/08-streaming/plan-08-08-grpc-server.md) `errors.As` literal and `m.Manager.Close` typo.
18. [plan-08-15](specs/epics/08-streaming/plan-08-15-probe-cache.md) and
    [plan-08-11](specs/epics/08-streaming/plan-08-11-live-subtitle.md) SQL: missing JOIN through
    `transcripts` and references to nonexistent columns/tables.
19. [plan-13-06](specs/epics/13-desktop/plan-13-06-drag-drop.md) `FileDrop` → `DragDrop` API rename.
20. [plan-13-08](specs/epics/13-desktop/plan-13-08-auto-update.md) `{{channel}}` substitution and `config_mut` removal.

### 10.4 Cross-plan coordination

21. Resolve `devices` table conflict: 07-22 vs 12-10 (§1.7); reintroduce
    `bundle_id`.
22. Web→API endpoint additions in Epic 07 (§1.11): `GET /api/jobs`,
    `POST /api/jobs:bulk-pause`, `POST /api/jobs/{id}/priority`,
    `GET /api/videos/{id}/jobs`, `PATCH /api/me/playback-state`,
    `POST /api/me/password`. Either add to Epic 07 plans or fold into
    GraphQL schema explicitly.
23. Web UI endpoint name fixes (§1.11): plan-11-02 `/transcript`→`/segments`,
    `stage`→`from_stage`, plan-11-05 force-pause query-string form.
24. Capabilities appendix shared across Epic 13 (§1.13).
25. Single-instance lock for desktop apps (Epic 13 cross-cutting).
26. README schema updates: epic 08 `closed_reason`, epic 10
    `users`/`refresh_tokens`/`pairing_codes`.

### 10.5 Smaller polish

27. Stage list canonicalization across Epic 07.
28. Confirmation flow consistency (07-03 name vs 07-04 id).
29. Channel-name registry consolidation in `pipeline/db/pubsub.py`.
30. Audit-write semantics consistency (sync vs async, swallow vs propagate).
31. HLS offline downloads in plan-12-06 (use platform-native APIs).
32. iOS background-`POST` language softening in plan-12-05.
33. Manifest service declarations in plan-12-02.
34. Bundle-size budget reconciliation across Epic 12.
35. Audit emitters for `password.changed`, `streaming.direct.access`,
    `permission.denied`.
36. Reaper unification (Epic 10 logout/refresh/pair).
37. plan-13-08 signing-key playbook expansion.
38. plan-13-01 vs plan-13-08 universal-binary vs per-arch reconciliation.

---

## Appendix A — Files reviewed

- `specs/architecture.md` (2,292 lines).
- `specs/epics/07-api-server/`: 22 plans + 22 stories + README.
- `specs/epics/08-streaming/`: 15 plans + 15 stories + README.
- `specs/epics/09-library-management/`: 18 plans + 18 stories + README.
- `specs/epics/10-auth-security/`: 17 plans + 17 stories + README.
- `specs/epics/11-web-ui/`: 14 plans + 14 stories + README.
- `specs/epics/12-mobile/`: 11 plans + 11 stories + README.
- `specs/epics/13-desktop/`: 8 plans + 8 stories + README.

Total: 213 specification files plus the architecture document.

## Appendix B — Reviewer breakdown by issue type

| Issue type | Total instances | Most-affected epics |
|------------|-----------------|---------------------|
| Schema column drift | 22+ | 07, 08, 09 |
| Schema table drift | 6 | 07, 08, 09 |
| ID type drift | 9 | 07, 09 |
| gRPC contract drift | 5 invented + 4 deviated | 07, 08 |
| Job FSM casing | 5 | 09 |
| Endpoint name mismatch | 4 | 07/11 |
| Missing endpoints | 7 | 07/11 |
| Compile-bug-grade Go errors | 6 | 07, 08 |
| Tauri 2 API errors | 4 | 13 |
| Capability/ACL omissions | 8 | 13 |
| Story↔plan deliberate deviations | 5 | 08, 10, 12 |
| README schema staleness | 4 | 08, 10 |

---

## Resolution log (2026-05-04)

All cross-cutting and per-epic issues from §1–§8 have been addressed.
Resolution strategy: update `specs/architecture.md` once with every
canonical decision, then sweep each epic's plans to align. 91 files
changed (architecture + 90 plan/story/README files).

### Architecture decisions landed in `specs/architecture.md`

- **§3 FSM**: lowercase state strings canonical (`discovered`, `probed`,
  `audio_extracted`, `transcribed`, `subtitle_gen`, `indexed`,
  `thumbnailed`, `ready`, `failed`); auxiliary terminal states canonical
  (`missing`, `superseded`, `ready_no_audio`, `corrupted`); seven
  pipeline stages canonical including `subtitle_gen`.
- **§8 Schema**: `videos` gains `content_type`, `deleted_at`;
  `libraries` gains `deleted_at`; `transcripts` gains
  `detected_language`, `language_confidence`, `superseded_at`;
  `collections` gains `library_id`, `updated_at`; `collection_items`
  gains `added_at`; `tags` gains `name_fold`, `created_at`; `speakers`
  gains `updated_at`; `saved_searches` gains `kind`, `updated_at`.
- **§8 New tables**: `library_roots` (canonical roots store);
  `media_features` (classifier features); `transcript_units` (indexer
  units); `audit_log` (partitioned by `created_at`, `category IN
  ('library','security','device','admin')`); `events` (replay log);
  `devices` (canonically owned by Epic 12 plan-12-10, with
  `bundle_id` and generated `token_hash`).
- **§8.6 Auth cross-references**: `refresh_tokens.device_id` owned by
  plan-10-03; `pairing_codes.code_hash` (no plaintext); `library_acl`
  with `LibrariesForUser` accessor.
- **§9 API**: `GET /api/jobs?…`, `GET /api/videos/{id}/jobs`,
  `POST /api/jobs/{id}/priority`, `POST /api/jobs:bulk-{pause,resume,cancel,retry}`,
  `PATCH /api/me/playback-state`, `POST /api/me/password`,
  `GET/POST/DELETE /api/me/{sessions,pats}`, `GET /api/recommendations`,
  `POST/PATCH/DELETE /api/devices`, `GET /.well-known/{aasa,assetlinks}`,
  `GET /api/system/metrics` all documented.
- **§9.4 Streaming**: separate `/audio/{lang}/seg-{n}.aac` route
  removed (audio muxes via `var_stream_map`). JWT audiences canonical:
  `api`, `streaming`, `streaming-direct`, `streaming-static`.
- **§9.4 lib[] semantics**: signed URLs emit `lib=[resource.library_id]`
  as a singleton (privacy: do not disclose other library memberships).
- **§9.9 gRPC**: `Streaming.OpenSession` returns `OpenSessionResponse`
  (Session + Capabilities); `EvictHashCache` returns
  `EvictHashCacheResponse{entries_removed, artifacts}`;
  `GetCapabilities` and `WatchQueue` canonical. Pipeline keeps the
  four canonical RPCs only — bulk job control flows through Postgres
  per §1.4. `Pipeline.Enqueue*`, `RunSyntheticTranscribe`, and
  `ExtractEmbeddedSubtitle` removed from plans.

### Per-epic resolution

| § | Issue | Resolved in |
|---|-------|-------------|
| 1.1 | Schema column drift (22+ items) | architecture §8 + sweeps in epics 07/08/09 |
| 1.2 | ID-type drift (9 plans) | architecture pinned BIGSERIAL; plans 07-04/06/07/08/14, 08-11, 09-11/12/18 updated to int64 |
| 1.3 | Table-name drift (segments/words/subtitles/videos_fts) | epics 07/08 sweeps |
| 1.4 | FSM casing | epic 09 sweep + plan-09-06 owns FSM-extension migration |
| 1.5 | gRPC contract drift | architecture §9.9 + plans 07-03/05/15/18, 08-08/10 |
| 1.6 | `subtitle_gen` stage canonicalized | architecture §3 + plans 07-05/12/13/15, 11-02 |
| 1.7 | `devices` double-ownership | plan-07-22 marked superseded; plan-12-10 owns canonical schema with `bundle_id` |
| 1.8 | `audit_log.category` enum | architecture §8.2.1 extended to `('library','security','device','admin')` |
| 1.9 | `refresh_tokens.device_id` ownership | plan-10-03 adds the column explicitly |
| 1.10 | `device-pat` removed | plan-12-11 drops it; only `Source='refresh'` accepted |
| 1.11 | Web→API endpoint additions and renames | architecture §9 additions; epic 11 endpoint renames |
| 1.12 | `lib[]` singleton | plan-10-08 rewritten + test renamed |
| 1.13 | Tauri 2 capabilities/ACL | plan-13-01 §13 capabilities appendix; cross-referenced from 13-04..08 |
| 1.14 | Migration ownership/ordering | plans 09-09/10/18 ship ALTERs as new files; plan-10-03 stub for `LibrariesForUser`; plan-09-10 owns `media_features` migration; plan-07-01 owns `.well-known` route |
| 2.1 | Epic 07 Go bugs | plans 07-01 (reqid + With + types registry), 07-02 (cursor type, uuid import), 07-16 (coder/websocket + MarshalJSON), 07-20 (BuildVersion / VersionInfo rename) |
| 2.2 | Cursor type mismatch | plan-07-02 makes `Cursor.ID string`; `paginate.IDKind` enum |
| 2.3 | Confirm semantics | both 07-03 and 07-04 use `?confirm=<id>` |
| 2.4 | `saved_searches` re-creation | plan-07-09 converted to ALTER-only |
| 2.5 | Problem-type registry | plan-07-01 expanded to 13 canonical constants |
| 2.6 | New routes (recommendations, devices) | architecture §9.7.2 / §9.7.3 |
| 3.1 | Epic 08 schema bugs (videos.mime, subtitle_tracks, JOIN) | plans 08-15, 08-03, 08-13, 08-11 |
| 3.2 | gRPC deviation in plan-08-08 | aligned with canonical §9.9 |
| 3.3 | `closed_reason` enum | epic 08 README expanded to canonical list |
| 3.4 | §9.4 audio segments | route removed from architecture §9.4 |
| 3.5 | `subtitle_files` consumption | plan-08-11 fetches from `subtitle_files WHERE is_external=true` |
| 3.6 | Epic 08 Go bugs | plans 08-08 (errors.As literal, m.Manager.Close), 08-05 (quoting + var_stream_map), 08-04 (ffprobe binary), 08-09 (SQLite reaper variant) |
| 3.7 | MediaInfo type duplication | plan-08-15 re-exports `caps.MediaInfo` |
| 4.1 | Library roots dual-store | plan-09-16 backfills `library_roots`; deprecates `libraries.roots TEXT[]` |
| 4.2 | plan-09-11 UUID vs BIGSERIAL | rewrote to BIGSERIAL throughout |
| 4.3 | content_hash type silent change | plan-09-04 keeps TEXT NOT NULL UNIQUE; uses `RETURNING (xmax=0)` |
| 4.4 | Channel-name registry | plan-09-01 §2.5 hosts the 10 canonical channel constants |
| 4.5 | Audit-write semantics | plans 09-04/15 use non-blocking `audit.Write` per 09-17 |
| 5.1 | plan-10-08 `lib[]` | singleton; test renamed; admin bypass keeps singleton |
| 5.2 | plan-10-16 partition index | partition key `created_at` included |
| 5.3 | RS256 key DB storage | plan-10-06 §0.1 deviation note + encryption-at-rest constraint |
| 5.4 | Epic 10 README staleness | users/refresh_tokens/pairing_codes schemas updated; device_id added |
| 5.5 | Audit emitters missing | plan-10-01 (password.changed), 10-07 (streaming.direct.access), 10-13 (permission.denied) |
| 5.6 | Reaper unification | plan-10-17 unified reaper covers `web_sessions`, `refresh_tokens`, `pairing_codes` |
| 5.7 | Sequencing bug | plan-10-03 ships stub `LibrariesForUser`; plan-10-13 replaces |
| 5.8 | plan-10-17 EC change | story-10-17 updated to 409 |
| 5.9 | Smaller polish | plan-10-08 `clampTTL` 5-min default + WARN; plan-10-09 dropped unused header; test names cleaned |
| 5.10 | Audiences in arch | architecture §9.4 lists all four canonical audiences |
| 6.1 | TanStack Router | plans 11-01, 11-12 migrated from react-router-dom |
| 6.2 | TanStack Query v5 | plan-11-04 uses `placeholderData: keepPreviousData` |
| 6.3 | WebSocket multiplexing | plan-11-02 description corrected |
| 6.4 | If-Match | plan-11-06 switched to `If-Unmodified-Since` |
| 6.5 | Manifest fields | plan-11-10 §2.1 enumerates manifest |
| 6.6 | Safari ITP budget | plan-11-10 §10 Safari-specific cap + LRU trim |
| 7.1 | HLS offline broken | plan-12-06 scoped to direct-play; v2 follow-up notes AVAssetDownloadTask/DownloadHelper |
| 7.2 | iOS background-POST claim | plan-12-05 softened |
| 7.3 | MediaPlaybackService missing | plan-12-02 manifest entry added |
| 7.4 | `.well-known` ownership | plan-12-09 cross-references plan-07-01 |
| 7.5 | Hard-coded `maktaba.local` | replaced with `{server_host}`; plan-12-01 §0 onboarding via plan-10-17 |
| 7.6 | Bundle-size budget | epic 12 README updated to 750 KB |
| 7.7 | plan-12-03 defects | force-unwrap, sync artwork load, race condition all fixed |
| 7.8 | AirPlay button | plan-12-07 drops inline button |
| 8.1 | plan-13-06 Tauri 1 API + security | DragDrop API names + path-traversal validator + fs capability |
| 8.2 | plan-13-08 channel/config_mut | runtime `build_updater` + signing-key playbook |
| 8.3 | universal vs per-arch | plan-13-08 publishes `darwin-universal` |
| 8.4 | plan-13-04 prose | tray header references built-in `tauri::tray::*` |
| 8.5 | plan-13-04 lifetime | owned `Vec<MenuItem>` then `Vec<&dyn IsMenuItem>` |
| 8.6 | plan-13-02 typos | `MainActivity` replaced with Rust argv parser; `app://` registered explicitly |
| 8.7 | plan-13-03 gaps | HLS MIME removed; AppImage trade-off note; avahi-daemon probe |
| 8.8 | plan-13-07 private window | `WebviewWindowBuilder::data_directory(...)` per-private-window |
| 8.9 | Shared scaffolding plan | plan-13-01 marked as base; epic 13 README updated |

### Verification

`git diff --stat` on `claude/cool-lichterman-f978aa`:

```
91 files changed, 3307 insertions(+), 927 deletions(-)
```

Per-epic file counts:

- Epic 07: 21 plans modified (plan-07-19 needed no changes)
- Epic 08: 12 files modified (10 plans + README + 1 story softened)
- Epic 09: 14 files modified (13 plans + README)
- Epic 10: 13 files modified (12 plans + README + 1 story EC update)
- Epic 11: 9 plans modified
- Epic 12: 11 files modified (10 plans + README)
- Epic 13: 9 files modified (8 plans + README)
- `specs/architecture.md`: 1 file (288 insertions / 24 deletions)
