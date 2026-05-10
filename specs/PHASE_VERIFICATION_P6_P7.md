# Phase 6 & 7 QA Certification

**Date:** 2026-05-10
**Branch verified:** `origin/main` @ `937a15e`
**Phase 6 commit:** `301da2f` — "Add Phase 6 API CRUD/search/sessions surfaces (Epic 7 slot P6)"
**Phase 7 commit:** `974556d` — "Add Phase 7 streaming service (Epic 8 stories 8.1-8.15)"

## Test results

| Module | Result |
|---|---|
| `cd api && go test ./...` | **PASS** — 27 packages OK, 6 no-test packages, 0 failures |
| `cd streaming && go test ./...` | **PASS** — 13 packages OK, 1 no-test package (`config`), 0 failures |

Full output captured at run time; no FAIL or panic in either tree.

---

## Phase 6 — Epic 7 (API server, stories 7.3–7.18, 7.21, 7.22)

### Structural inventory

All 13 handler packages exist under `api/internal/handlers/`:
`libraries`, `videos`, `search`, `streaming`, `jobs`, `collections`, `tags`, `speakers`, `settings`, `recommendations`, `devices`, `ws`, `common`.

Router wiring at `api/internal/router/p6.go` mounts every handler plus GraphQL endpoints. `api/internal/router/adapters.go` provides the gRPC service adapters. `api/internal/graphql/schema.go` carries the SDL. `api/internal/grpcclients/{pipeline,streaming}/` carries the typed wrappers.

Migrations 0032–0042 (Postgres + SQLite duals) all present in `shared/db/migrations/`.

### Per-story verification

| Story | Status | Evidence |
|---|---|---|
| 7.3 Library CRUD | **PASS** | `api/internal/handlers/libraries/libraries.go` (673 LOC) + `libraries_test.go`; deep-merge settings, root validation, scan, stats. |
| 7.4 Video listing/detail/patch/delete | **PASS** | `api/internal/handlers/videos/videos.go` + `videos_test.go`; eager-join detail, tag-replacing PATCH, purge with id confirm. |
| 7.5 Video processing control | **PASS** | `api/internal/handlers/videos/videos.go` JobControl interface; `/process`, `/reprocess`, scan-stage rejection. |
| 7.6 Transcript window | **PASS** | `api/internal/handlers/videos/segments.go`; window overlap query, from>to → 400, duration clamp, bidi-isolation. |
| 7.7 Subtitles & chapters read | **PASS** | `api/internal/handlers/videos/videos.go` enumerations with Accept-Language ordering. |
| 7.8 Search API (FTS/semantic/hybrid) | **PASS** | `api/internal/handlers/search/search.go` (552 LOC) + `search_test.go`; RRF fusion k=60, push-down filters, `<mark>` highlights. |
| 7.9 Saved searches | **PASS** | `api/internal/handlers/search/search.go` SaveSearch / list / delete; per-user namespace + 409 conflict; migration 0037. |
| 7.10 Streaming session lifecycle | **PASS** | `api/internal/handlers/streaming/streaming.go` (466 LOC) + `streaming_test.go`; OpenSession/CloseSession/GetCapabilities with 60s cache. |
| 7.11 Watch progress sync | **PASS** | `api/internal/handlers/streaming/streaming.go` PostProgress; per-session 1/s debouncer, ≥95% auto-complete, migration 0038. |
| 7.12 Job control endpoints | **PASS** | `api/internal/handlers/jobs/jobs.go` (533 LOC) + `jobs_test.go`; pause / force-pause / resume / cancel / retry; per-video aggregates. |
| 7.13 Queue stats | **PASS** | `api/internal/handlers/jobs/jobs.go` stage × state matrix + done_24h + workers. |
| 7.14 Collections / tags / speakers | **PASS** | `collections/collections.go` (422 LOC), `tags/tags.go` (226 LOC) + tests, `speakers/speakers.go` (173 LOC); migrations 0033–0035; manual+smart collections, NFC tags, speaker merge tx. |
| 7.15 Settings & system | **PASS** | `api/internal/handlers/settings/settings.go` + `settings_test.go`; secret redaction with `*_present`, runtime-key allowlist, gRPC stt-backends/stt-test passthroughs; migration 0042. |
| 7.16 WebSocket fan-out | **PASS** | `api/internal/handlers/ws/ws.go` + `ws_test.go`; Hub with bounded 1000-deep per-sub queue; SSE handlers for jobs/library/playback. |
| 7.17 GraphQL schema (SDL) | **PASS** | `api/internal/graphql/schema.go` + `schema_test.go`; all required types present (Library, Video, MediaInfo, AudioTrack, Transcript, Segment, Word, Subtitle, Chapter, Tag, Collection, Speaker, Job, StreamingSession, User, PlaybackState, SearchResult, SearchHit, SearchMatch, Recommendation, Device); resolver wiring deferred per spec. |
| 7.18 gRPC clients (Pipeline + Streaming) | **PASS** | `api/internal/grpcclients/pipeline/pipeline.go` (211 LOC) + tests, `api/internal/grpcclients/streaming/streaming.go` (99 LOC); sliding-window breaker, retry+jittered backoff, UNAVAILABLE/DEADLINE_EXCEEDED retry. |
| 7.21 Recommendations | **PASS** | `api/internal/handlers/recommendations/recommendations.go` (236 LOC); migration 0041 `user_recs`. |
| 7.22 Device registration | **PASS** | `api/internal/handlers/devices/devices.go` (216 LOC); migration 0040 `devices`. |

**Migrations 0032–0042 present:** chapters, collections, tags, speakers, audit_log, saved_searches, playback_state, streaming_sessions, devices, user_recs, app_settings — both `.sql` (Postgres) and `.sqlite.sql` duals.

**Common helpers:** `api/internal/handlers/common/common.go` + tests.

---

## Phase 7 — Epic 8 (Streaming service, stories 8.1–8.15)

### Structural inventory

All required streaming packages present under `streaming/internal/`:
`auth` (signed-URL middleware + JWKS + JWT), `capability` (matrix), `config`, `ffmpeg` (ffmpeg + remux + hwaccel), `grpcsrv` (gRPC server), `handlers` (direct + manifest + subtitle + chapter + static), `httpx` (problem+json), `probe`, `server` (chi router), `session` (store + reaper), `slots`, `cache`, `version`.

Router wired in `streaming/main.go` + `streaming/internal/server/server.go`. Tests in 13 packages all pass.

### Per-story verification

| Story | Status | Evidence |
|---|---|---|
| 8.1 Server skeleton + signed-URL middleware | **PASS** | `streaming/internal/auth/{claims,jwt,jwks,middleware}.go` + `middleware_test.go` (284 LOC); `streaming/internal/httpx/problem.go` problem+json; `streaming/internal/server/server.go` chi mux. |
| 8.2 Capability matrix + client profile registry | **PASS** | `streaming/internal/capability/matrix.go` (270 LOC) + `matrix_test.go`; can-direct-play / can-remux decision logic. |
| 8.3 Direct play (range-served 206) | **PASS** | `streaming/internal/handlers/direct.go` (268 LOC) + `direct_test.go` (309 LOC); range-served partial content. |
| 8.4 Direct stream remux (FFmpeg `-c copy`) | **PASS** | `streaming/internal/ffmpeg/remux.go` (66 LOC) Remuxer; `streaming/internal/ffmpeg/ffmpeg.go` RemuxArgs. |
| 8.5 HLS adaptive transcode | **PASS** | `streaming/internal/handlers/manifest.go` (188 LOC) ServeMaster/ServeRenditionIndex/ServeSegment; `ffmpeg.go` HLSArgs + BuildMasterPlaylist. |
| 8.6 DASH manifest (opt-in) | **PASS** | `streaming/internal/handlers/manifest.go` mpd path; `ffmpeg.go` DASHArgs (per §4.3 invocation). |
| 8.7 Hardware acceleration auto-detect | **PASS** | `streaming/internal/ffmpeg/hwaccel.go` (133 LOC) probe-based selection + per-session fallback. |
| 8.8 gRPC server (OpenSession/CloseSession/EvictHashCache/GetCapabilities) | **PASS** | `streaming/internal/grpcsrv/server.go` (338 LOC) + `server_test.go` (202 LOC); all four RPC methods implemented. |
| 8.9 Session store + sticky transcoder + reaper | **PASS** | `streaming/internal/session/session.go` (266 LOC) MemoryStore; `streaming/internal/session/reaper.go` IdleAfter=90s default. |
| 8.10 Concurrency caps + slot accounting | **PASS** | `streaming/internal/slots/slots.go` (203 LOC) Allocator + `slots_test.go`; direct-degraded fallback path. |
| 8.11 Live subtitle rendering | **PASS** | `streaming/internal/handlers/subtitle.go` (202 LOC) live VTT, sidecar, embedded paths. |
| 8.12 Chapter delivery | **PASS** | `streaming/internal/handlers/chapter.go` (177 LOC) chapters.json + HLS DATERANGE. |
| 8.13 Posters / sprite sheets / chapter thumbs | **PASS** | `streaming/internal/handlers/static.go` (112 LOC) ServePoster + ServeSprite; `server.go` mounts `/stream/posters` + `/stream/sprites`. |
| 8.14 Cache layout + LRU GC + cap enforcement | **PASS** | `streaming/internal/cache/layout.go` (259 LOC) + `layout_test.go` (164 LOC); shard layout, cap enforcement, posters/sprites floor. |
| 8.15 Probe cache (LRU + Postgres) | **PASS** | `streaming/internal/probe/probe.go` (200 LOC) + `probe_test.go` (112 LOC); EvictHash, in-place modification handling. |

---

## Summary

**Phase 6:** All 18 verified Epic 7 stories (7.3–7.18, 7.21, 7.22) PASS. All 11 migrations (0032–0042) present with Postgres + SQLite duals. All 13 handler packages mounted. GraphQL SDL covers all 21 required types. gRPC clients wired for both Pipeline and Streaming.

**Phase 7:** All 15 Epic 8 stories (8.1–8.15) PASS. Signed-URL middleware, capability matrix, direct play, remux, HLS, DASH, hwaccel, gRPC server, session store, slots, subtitles, chapters, posters/sprites, cache, probe — every required surface is present and exercised by tests.

**Verdict:** Phases 6 and 7 are CERTIFIED. Both `go test ./...` runs pass cleanly.
