# File Inventory

> Annotated tree of every project file: mockups, API spec, specs (architecture, epics, stories, plans), reviews, diagrams, infra, and the wiki itself.


**Total files indexed:** 581

## Documentation (2)

- [LICENSE](../../LICENSE) — AGPL-3.0 license.
- [README.md](../../README.md) — Project README.

## Infrastructure & build (3)

- [.gitignore](../../.gitignore) — Git ignore rules.
- [pyproject.toml](../../pyproject.toml) — Python project metadata (pipeline service).
- [shared/db/migrations/MANIFEST.md](../../shared/db/migrations/MANIFEST.md) — Canonical migration-slot manifest (resolved from PLAN_REVIEW.md §1).

## Code skeleton (2)

- [maktaba/__init__.py](../../maktaba/__init__.py) — Pipeline package init.
- [maktaba/cli.py](../../maktaba/cli.py) — Pipeline CLI entrypoint.

## API specification (2)

- [shared/api/openapi.json](../../shared/api/openapi.json) — OpenAPI 3.1 spec — JSON output of the same source.
- [shared/api/openapi.yaml](../../shared/api/openapi.yaml) — OpenAPI 3.1 spec for the Maktaba API (canonical, YAML).

## Architecture spec (1)

- [specs/architecture.md](../../specs/architecture.md) — System architecture — services, schema §8, API surface §9, gRPC §9.9, auth §9.8, FSM §3+§7, streaming §4.

## Reviews (5)

- [specs/PLAN_REVIEW.md](../../specs/PLAN_REVIEW.md) — Plan review for Epics 01–06 (49 plans). Status: RESOLVED.
- [specs/PLAN_REVIEW_07_13.md](../../specs/PLAN_REVIEW_07_13.md) — Plan review for Epics 07–13 (105 plans). Status: RESOLVED 2026-05-04.
- [specs/PLAN_REVIEW_14_17.md](../../specs/PLAN_REVIEW_14_17.md) — Plan review for Epics 14–17 (33 plans). Status: RESOLVED.
- [specs/PLAN_REVIEW_18_24.md](../../specs/PLAN_REVIEW_18_24.md) — Plan review for Epics 18–24 (57 plans). Status: RESOLVED.
- [specs/REVIEW.md](../../specs/REVIEW.md) — Independent spec review — cross-document conflicts, missing integrations, sequencing (§7 build order).

## Epic READMEs (24)

- [specs/epics/01-scanner/README.md](../../specs/epics/01-scanner/README.md) — Epic 01 — Scanner: Detect, identify, and track every video file in a library; assign stable content_hash; manage video state machine.
- [specs/epics/02-audio-extraction/README.md](../../specs/epics/02-audio-extraction/README.md) — Epic 02 — Audio Extraction: Probe video metadata, select audio tracks, extract PCM streams; resource accounting.
- [specs/epics/03-transcription/README.md](../../specs/epics/03-transcription/README.md) — Epic 03 — Transcription: Pluggable STT backends (Whisper.cpp, faster-whisper, OpenAI API); per-segment commit; pause/resume; crash recovery; diarization.
- [specs/epics/04-subtitles/README.md](../../specs/epics/04-subtitles/README.md) — Epic 04 — Subtitles: Generate VTT/SRT from transcript segments, format & wrap; discover external/embedded subs; live VTT contract for streaming.
- [specs/epics/05-search-indexing/README.md](../../specs/epics/05-search-indexing/README.md) — Epic 05 — Search & Indexing: Unit chunking; FTS (SQLite FTS5 / Postgres tsvector); vector store (ChromaDB); hybrid RRF; chapter inference.
- [specs/epics/06-job-queue/README.md](../../specs/epics/06-job-queue/README.md) — Epic 06 — Job Queue: processing_jobs schema; claim loop; heartbeat & progress; pause/resume/cancel; backoff retry; reaper; concurrency caps; graceful shutdown; observability.
- [specs/epics/07-api-server/README.md](../../specs/epics/07-api-server/README.md) — Epic 07 — API Server: REST surface (libraries, videos, transcripts, search, jobs, sessions, settings); cursor pagination; gRPC clients; WebSocket fan-out; GraphQL.
- [specs/epics/08-streaming/README.md](../../specs/epics/08-streaming/README.md) — Epic 08 — Streaming: Direct play / direct stream (remux) / HLS / DASH; capability matrix; hwaccel; session store; live subtitles & chapters; cache GC.
- [specs/epics/09-library-management/README.md](../../specs/epics/09-library-management/README.md) — Epic 09 — Library Management: Library config; filesystem watcher; periodic sweep; dedup; ignore rules; manual scan; tags & speakers; collections (manual + smart); audit; chapter inference.
- [specs/epics/10-auth-security/README.md](../../specs/epics/10-auth-security/README.md) — Epic 10 — Auth & Security: User store; web/native login; token refresh; logout/revocation; RS256 keys + JWKS; signed-URL minter; CSRF; brute-force protection; rate limiting.
- [specs/epics/11-web-ui/README.md](../../specs/epics/11-web-ui/README.md) — Epic 11 — Web UI (PWA): Library browser; video detail; player; search; processing-queue dashboard; settings; responsive; dark/light theme; keyboard shortcuts; offline PWA; a11y; i18n+RTL.
- [specs/epics/12-mobile/README.md](../../specs/epics/12-mobile/README.md) — Epic 12 — Mobile (Capacitor): iOS & Android apps; native player; push notifications; background playback; offline downloads; share/cast; haptics; deep linking.
- [specs/epics/13-desktop/README.md](../../specs/epics/13-desktop/README.md) — Epic 13 — Desktop (Tauri): macOS / Windows / Linux apps; system tray; mDNS discovery; drag-drop import; keyboard shortcuts; auto-update.
- [specs/epics/14-tv-apps/README.md](../../specs/epics/14-tv-apps/README.md) — Epic 14 — TV Apps: tvOS (Swift) and Android TV (Kotlin); 10-foot UI; voice search; continue watching; recommendations UI/API.
- [specs/epics/15-discovery/README.md](../../specs/epics/15-discovery/README.md) — Epic 15 — Discovery & Networking: mDNS service discovery; cloud relay; federation; DLNA/UPnP; QR pairing; pairing/federation REST APIs.
- [specs/epics/16-subscriptions/README.md](../../specs/epics/16-subscriptions/README.md) — Epic 16 — Subscriptions & Monetization: Free tier; premium features; subscription management; license validation; telemetry opt-in; feature flags + admin APIs.
- [specs/epics/17-ux-design-system/README.md](../../specs/epics/17-ux-design-system/README.md) — Epic 17 — UX Design System: Design tokens; component library; motion; loading/error/empty states; onboarding; RTL; player controls; search results; processing progress; transcript presentation.
- [specs/epics/18-performance/README.md](../../specs/epics/18-performance/README.md) — Epic 18 — Performance: Latency budgets; search/streaming/pipeline/database/cache hot-path performance; client-perceived performance.
- [specs/epics/19-scalability/README.md](../../specs/epics/19-scalability/README.md) — Epic 19 — Scalability: Single-host capacity; API/streaming/pipeline scale-out; database & storage scaling; concurrency caps; multi-tenant readiness.
- [specs/epics/20-testing/README.md](../../specs/epics/20-testing/README.md) — Epic 20 — Testing: Test pyramid; fixtures + seed data; unit/integration/E2E coverage; contract tests; perf regression CI; flaky-test policy.
- [specs/epics/21-observability/README.md](../../specs/epics/21-observability/README.md) — Epic 21 — Observability: Structured logging; metrics; distributed tracing; health/readiness probes; error reporting; audit log; job pipeline visibility; telemetry privacy.
- [specs/epics/22-devops/README.md](../../specs/epics/22-devops/README.md) — Epic 22 — DevOps & Delivery: CI pipeline; reproducible builds; container images; database migrations; release management; upgrade/rollback; multi-platform packaging; local dev workflow.
- [specs/epics/23-security/README.md](../../specs/epics/23-security/README.md) — Epic 23 — Security: Authentication; authorization & ACLs; transport security; secrets management; input validation; rate limiting; supply chain; coordinated disclosure.
- [specs/epics/24-data-integrity/README.md](../../specs/epics/24-data-integrity/README.md) — Epic 24 — Data Integrity: Atomic writes; idempotent jobs; DB constraints; concurrency locking; backup/restore; disaster recovery; integrity verification; identity stability; forward/back compat.

## User stories (236)

### 01-scanner

- [specs/epics/01-scanner/story-01-01-file-discovery.md](../../specs/epics/01-scanner/story-01-01-file-discovery.md) — Story 01.01 — Bootstrap a library and walk its roots.
- [specs/epics/01-scanner/story-01-02-content-identity.md](../../specs/epics/01-scanner/story-01-02-content-identity.md) — Story 01.02 — Content-addressable identity (BLAKE3).
- [specs/epics/01-scanner/story-01-03-filesystem-watcher.md](../../specs/epics/01-scanner/story-01-03-filesystem-watcher.md) — Story 01.03 — Watch for live filesystem changes.
- [specs/epics/01-scanner/story-01-04-manual-control.md](../../specs/epics/01-scanner/story-01-04-manual-control.md) — Story 01.04 — Manual control surface (start/pause/cancel scan).
- [specs/epics/01-scanner/story-01-05-schema-decisions.md](../../specs/epics/01-scanner/story-01-05-schema-decisions.md) — Story 01.05 — Schema & ownership decisions.
- [specs/epics/01-scanner/story-01-06-video-state-machine.md](../../specs/epics/01-scanner/story-01-06-video-state-machine.md) — Story 01.06 — Video state machine (DISCOVERED → READY).

### 02-audio-extraction

- [specs/epics/02-audio-extraction/story-02-01-audio-probe.md](../../specs/epics/02-audio-extraction/story-02-01-audio-probe.md) — Story 02.01 — Probe audio tracks via ffprobe.
- [specs/epics/02-audio-extraction/story-02-02-track-selection.md](../../specs/epics/02-audio-extraction/story-02-02-track-selection.md) — Story 02.02 — Select preferred audio track.
- [specs/epics/02-audio-extraction/story-02-03-stream-extraction.md](../../specs/epics/02-audio-extraction/story-02-03-stream-extraction.md) — Story 02.03 — Extract PCM via FFmpeg pipe.
- [specs/epics/02-audio-extraction/story-02-04-resource-accounting.md](../../specs/epics/02-audio-extraction/story-02-04-resource-accounting.md) — Story 02.04 — Resource accounting / concurrency caps.

### 03-transcription

- [specs/epics/03-transcription/story-03-01-backend-protocol.md](../../specs/epics/03-transcription/story-03-01-backend-protocol.md) — Story 03.01 — Backend protocol (Transcriber interface).
- [specs/epics/03-transcription/story-03-02-whisper-mlx-backend.md](../../specs/epics/03-transcription/story-03-02-whisper-mlx-backend.md) — Story 03.02 — whisper.cpp / MLX backend.
- [specs/epics/03-transcription/story-03-03-faster-whisper-backend.md](../../specs/epics/03-transcription/story-03-03-faster-whisper-backend.md) — Story 03.03 — faster-whisper backend.
- [specs/epics/03-transcription/story-03-04-openai-api-backend.md](../../specs/epics/03-transcription/story-03-04-openai-api-backend.md) — Story 03.04 — OpenAI Whisper API backend.
- [specs/epics/03-transcription/story-03-05-backend-registry.md](../../specs/epics/03-transcription/story-03-05-backend-registry.md) — Story 03.05 — Backend registry & selection.
- [specs/epics/03-transcription/story-03-06-segment-commit.md](../../specs/epics/03-transcription/story-03-06-segment-commit.md) — Story 03.06 — Real-time per-segment commit (correctness keystone).
- [specs/epics/03-transcription/story-03-07-pause-resume.md](../../specs/epics/03-transcription/story-03-07-pause-resume.md) — Story 03.07 — Pause / resume mid-transcribe.
- [specs/epics/03-transcription/story-03-08-crash-recovery.md](../../specs/epics/03-transcription/story-03-08-crash-recovery.md) — Story 03.08 — Crash recovery (resume from last segment).
- [specs/epics/03-transcription/story-03-09-diarization.md](../../specs/epics/03-transcription/story-03-09-diarization.md) — Story 03.09 — Diarization (speaker turns).

### 04-subtitles

- [specs/epics/04-subtitles/story-04-01-generate-from-segments.md](../../specs/epics/04-subtitles/story-04-01-generate-from-segments.md) — Story 04.01 — Generate VTT/SRT from transcript segments.
- [specs/epics/04-subtitles/story-04-02-formatting-wrapping.md](../../specs/epics/04-subtitles/story-04-02-formatting-wrapping.md) — Story 04.02 — Line formatting & wrapping.
- [specs/epics/04-subtitles/story-04-03-external-discovery.md](../../specs/epics/04-subtitles/story-04-03-external-discovery.md) — Story 04.03 — Discover external sidecar subtitles.
- [specs/epics/04-subtitles/story-04-04-embedded-extraction.md](../../specs/epics/04-subtitles/story-04-04-embedded-extraction.md) — Story 04.04 — Extract embedded subtitle tracks.
- [specs/epics/04-subtitles/story-04-05-live-vtt-contract.md](../../specs/epics/04-subtitles/story-04-05-live-vtt-contract.md) — Story 04.05 — Live VTT contract for streaming.

### 05-search-indexing

- [specs/epics/05-search-indexing/story-05-01-unit-chunking.md](../../specs/epics/05-search-indexing/story-05-01-unit-chunking.md) — Story 05.01 — Unit chunking (transcript_units table).
- [specs/epics/05-search-indexing/story-05-02-fts-tsvector.md](../../specs/epics/05-search-indexing/story-05-02-fts-tsvector.md) — Story 05.02 — FTS (SQLite FTS5 / Postgres tsvector).
- [specs/epics/05-search-indexing/story-05-03-chroma-vector.md](../../specs/epics/05-search-indexing/story-05-03-chroma-vector.md) — Story 05.03 — ChromaDB vector store.
- [specs/epics/05-search-indexing/story-05-04-hybrid-rrf.md](../../specs/epics/05-search-indexing/story-05-04-hybrid-rrf.md) — Story 05.04 — Hybrid search via reciprocal rank fusion.
- [specs/epics/05-search-indexing/story-05-05-incremental-indexing.md](../../specs/epics/05-search-indexing/story-05-05-incremental-indexing.md) — Story 05.05 — Incremental indexing.
- [specs/epics/05-search-indexing/story-05-06-query-suggestions.md](../../specs/epics/05-search-indexing/story-05-06-query-suggestions.md) — Story 05.06 — Query suggestions.
- [specs/epics/05-search-indexing/story-05-07-chapter-inference.md](../../specs/epics/05-search-indexing/story-05-07-chapter-inference.md) — Story 05.07 — Chapter inference.

### 06-job-queue

- [specs/epics/06-job-queue/story-06-01-schema-indexes.md](../../specs/epics/06-job-queue/story-06-01-schema-indexes.md) — Story 06.01 — processing_jobs schema + indexes.
- [specs/epics/06-job-queue/story-06-02-claim-loop.md](../../specs/epics/06-job-queue/story-06-02-claim-loop.md) — Story 06.02 — Worker claim loop.
- [specs/epics/06-job-queue/story-06-03-heartbeat-progress.md](../../specs/epics/06-job-queue/story-06-03-heartbeat-progress.md) — Story 06.03 — Heartbeat & progress reporting.
- [specs/epics/06-job-queue/story-06-04-pause-resume-cancel.md](../../specs/epics/06-job-queue/story-06-04-pause-resume-cancel.md) — Story 06.04 — Pause / resume / cancel.
- [specs/epics/06-job-queue/story-06-05-backoff-retry.md](../../specs/epics/06-job-queue/story-06-05-backoff-retry.md) — Story 06.05 — Backoff & retry policy.
- [specs/epics/06-job-queue/story-06-06-reaper.md](../../specs/epics/06-job-queue/story-06-06-reaper.md) — Story 06.06 — Reaper for dead workers.
- [specs/epics/06-job-queue/story-06-07-concurrency-caps.md](../../specs/epics/06-job-queue/story-06-07-concurrency-caps.md) — Story 06.07 — Concurrency caps.
- [specs/epics/06-job-queue/story-06-08-graceful-shutdown.md](../../specs/epics/06-job-queue/story-06-08-graceful-shutdown.md) — Story 06.08 — Graceful shutdown.
- [specs/epics/06-job-queue/story-06-09-observability.md](../../specs/epics/06-job-queue/story-06-09-observability.md) — Story 06.09 — Queue observability.
- [specs/epics/06-job-queue/story-06-10-resume-invariant.md](../../specs/epics/06-job-queue/story-06-10-resume-invariant.md) — Story 06.10 — Resume invariant guarantee.

### 07-api-server

- [specs/epics/07-api-server/story-07-01-http-server-skeleton.md](../../specs/epics/07-api-server/story-07-01-http-server-skeleton.md) — Story 07.01 — HTTP server skeleton.
- [specs/epics/07-api-server/story-07-02-cursor-pagination.md](../../specs/epics/07-api-server/story-07-02-cursor-pagination.md) — Story 07.02 — Cursor pagination middleware.
- [specs/epics/07-api-server/story-07-03-library-crud.md](../../specs/epics/07-api-server/story-07-03-library-crud.md) — Story 07.03 — Library CRUD endpoints.
- [specs/epics/07-api-server/story-07-04-video-crud.md](../../specs/epics/07-api-server/story-07-04-video-crud.md) — Story 07.04 — Video CRUD endpoints.
- [specs/epics/07-api-server/story-07-05-video-processing-control.md](../../specs/epics/07-api-server/story-07-05-video-processing-control.md) — Story 07.05 — Video processing control.
- [specs/epics/07-api-server/story-07-06-transcript-window.md](../../specs/epics/07-api-server/story-07-06-transcript-window.md) — Story 07.06 — Transcript window endpoint.
- [specs/epics/07-api-server/story-07-07-subtitles-chapters-read.md](../../specs/epics/07-api-server/story-07-07-subtitles-chapters-read.md) — Story 07.07 — Subtitles & chapters read.
- [specs/epics/07-api-server/story-07-08-search-api.md](../../specs/epics/07-api-server/story-07-08-search-api.md) — Story 07.08 — Search API.
- [specs/epics/07-api-server/story-07-09-saved-searches.md](../../specs/epics/07-api-server/story-07-09-saved-searches.md) — Story 07.09 — Saved searches.
- [specs/epics/07-api-server/story-07-10-streaming-session-lifecycle.md](../../specs/epics/07-api-server/story-07-10-streaming-session-lifecycle.md) — Story 07.10 — Streaming session lifecycle.
- [specs/epics/07-api-server/story-07-11-watch-progress-sync.md](../../specs/epics/07-api-server/story-07-11-watch-progress-sync.md) — Story 07.11 — Watch progress sync.
- [specs/epics/07-api-server/story-07-12-job-control.md](../../specs/epics/07-api-server/story-07-12-job-control.md) — Story 07.12 — Job control endpoints.
- [specs/epics/07-api-server/story-07-13-queue-stats.md](../../specs/epics/07-api-server/story-07-13-queue-stats.md) — Story 07.13 — Queue stats endpoint.
- [specs/epics/07-api-server/story-07-14-collections-tags-speakers.md](../../specs/epics/07-api-server/story-07-14-collections-tags-speakers.md) — Story 07.14 — Collections / tags / speakers REST.
- [specs/epics/07-api-server/story-07-15-settings-system.md](../../specs/epics/07-api-server/story-07-15-settings-system.md) — Story 07.15 — Settings & system endpoints.
- [specs/epics/07-api-server/story-07-16-websocket-fanout.md](../../specs/epics/07-api-server/story-07-16-websocket-fanout.md) — Story 07.16 — WebSocket event fan-out.
- [specs/epics/07-api-server/story-07-17-graphql-schema.md](../../specs/epics/07-api-server/story-07-17-graphql-schema.md) — Story 07.17 — GraphQL schema.
- [specs/epics/07-api-server/story-07-18-grpc-clients.md](../../specs/epics/07-api-server/story-07-18-grpc-clients.md) — Story 07.18 — gRPC clients (to Pipeline + Streaming).
- [specs/epics/07-api-server/story-07-19-validation-rate-limiting.md](../../specs/epics/07-api-server/story-07-19-validation-rate-limiting.md) — Story 07.19 — Validation + rate limiting middleware.
- [specs/epics/07-api-server/story-07-20-health-version-metrics.md](../../specs/epics/07-api-server/story-07-20-health-version-metrics.md) — Story 07.20 — Health / version / metrics.
- [specs/epics/07-api-server/story-07-21-recommendations.md](../../specs/epics/07-api-server/story-07-21-recommendations.md) — Story 07.21 — Recommendations endpoint.
- [specs/epics/07-api-server/story-07-22-devices-register.md](../../specs/epics/07-api-server/story-07-22-devices-register.md) — Story 07.22 — Devices register endpoint.

### 08-streaming

- [specs/epics/08-streaming/story-08-01-server-skeleton.md](../../specs/epics/08-streaming/story-08-01-server-skeleton.md) — Story 08.01 — Streaming server skeleton + signed-URL middleware.
- [specs/epics/08-streaming/story-08-02-capability-matrix.md](../../specs/epics/08-streaming/story-08-02-capability-matrix.md) — Story 08.02 — Capability matrix.
- [specs/epics/08-streaming/story-08-03-direct-play.md](../../specs/epics/08-streaming/story-08-03-direct-play.md) — Story 08.03 — Direct play.
- [specs/epics/08-streaming/story-08-04-direct-stream-remux.md](../../specs/epics/08-streaming/story-08-04-direct-stream-remux.md) — Story 08.04 — Direct stream (remux).
- [specs/epics/08-streaming/story-08-05-hls-transcode.md](../../specs/epics/08-streaming/story-08-05-hls-transcode.md) — Story 08.05 — HLS transcode.
- [specs/epics/08-streaming/story-08-06-dash-manifest.md](../../specs/epics/08-streaming/story-08-06-dash-manifest.md) — Story 08.06 — DASH manifest.
- [specs/epics/08-streaming/story-08-07-hwaccel-detect.md](../../specs/epics/08-streaming/story-08-07-hwaccel-detect.md) — Story 08.07 — Hardware accel detection.
- [specs/epics/08-streaming/story-08-08-grpc-server.md](../../specs/epics/08-streaming/story-08-08-grpc-server.md) — Story 08.08 — Streaming gRPC server.
- [specs/epics/08-streaming/story-08-09-session-store.md](../../specs/epics/08-streaming/story-08-09-session-store.md) — Story 08.09 — Session store + reaper.
- [specs/epics/08-streaming/story-08-10-concurrency-caps.md](../../specs/epics/08-streaming/story-08-10-concurrency-caps.md) — Story 08.10 — Concurrency caps.
- [specs/epics/08-streaming/story-08-11-live-subtitle.md](../../specs/epics/08-streaming/story-08-11-live-subtitle.md) — Story 08.11 — Live subtitle delivery.
- [specs/epics/08-streaming/story-08-12-chapter-delivery.md](../../specs/epics/08-streaming/story-08-12-chapter-delivery.md) — Story 08.12 — Chapter delivery.
- [specs/epics/08-streaming/story-08-13-posters-sprites.md](../../specs/epics/08-streaming/story-08-13-posters-sprites.md) — Story 08.13 — Posters & sprites.
- [specs/epics/08-streaming/story-08-14-cache-gc.md](../../specs/epics/08-streaming/story-08-14-cache-gc.md) — Story 08.14 — Cache GC.
- [specs/epics/08-streaming/story-08-15-probe-cache.md](../../specs/epics/08-streaming/story-08-15-probe-cache.md) — Story 08.15 — Probe cache.

### 09-library-management

- [specs/epics/09-library-management/story-09-01-library-config-schema.md](../../specs/epics/09-library-management/story-09-01-library-config-schema.md) — Story 09.01 — Library config schema.
- [specs/epics/09-library-management/story-09-02-filesystem-watcher.md](../../specs/epics/09-library-management/story-09-02-filesystem-watcher.md) — Story 09.02 — Library filesystem watcher.
- [specs/epics/09-library-management/story-09-03-periodic-sweep.md](../../specs/epics/09-library-management/story-09-03-periodic-sweep.md) — Story 09.03 — Periodic sweep.
- [specs/epics/09-library-management/story-09-04-content-hash-dedup.md](../../specs/epics/09-library-management/story-09-04-content-hash-dedup.md) — Story 09.04 — Content-hash dedup.
- [specs/epics/09-library-management/story-09-05-ignore-rules.md](../../specs/epics/09-library-management/story-09-05-ignore-rules.md) — Story 09.05 — Ignore rules.
- [specs/epics/09-library-management/story-09-06-manual-scan.md](../../specs/epics/09-library-management/story-09-06-manual-scan.md) — Story 09.06 — Manual scan trigger.
- [specs/epics/09-library-management/story-09-07-library-stats.md](../../specs/epics/09-library-management/story-09-07-library-stats.md) — Story 09.07 — Library stats.
- [specs/epics/09-library-management/story-09-08-language-tag.md](../../specs/epics/09-library-management/story-09-08-language-tag.md) — Story 09.08 — Language auto-tag.
- [specs/epics/09-library-management/story-09-09-topic-tag.md](../../specs/epics/09-library-management/story-09-09-topic-tag.md) — Story 09.09 — Topic auto-tag.
- [specs/epics/09-library-management/story-09-10-content-type-classifier.md](../../specs/epics/09-library-management/story-09-10-content-type-classifier.md) — Story 09.10 — Content-type classifier.
- [specs/epics/09-library-management/story-09-11-speakers.md](../../specs/epics/09-library-management/story-09-11-speakers.md) — Story 09.11 — Speaker management.
- [specs/epics/09-library-management/story-09-12-tag-crud.md](../../specs/epics/09-library-management/story-09-12-tag-crud.md) — Story 09.12 — Tag CRUD.
- [specs/epics/09-library-management/story-09-13-collections-manual.md](../../specs/epics/09-library-management/story-09-13-collections-manual.md) — Story 09.13 — Manual collections.
- [specs/epics/09-library-management/story-09-14-smart-collections.md](../../specs/epics/09-library-management/story-09-14-smart-collections.md) — Story 09.14 — Smart collections.
- [specs/epics/09-library-management/story-09-15-library-deletion.md](../../specs/epics/09-library-management/story-09-15-library-deletion.md) — Story 09.15 — Library deletion + cascades.
- [specs/epics/09-library-management/story-09-16-multi-root-overlap.md](../../specs/epics/09-library-management/story-09-16-multi-root-overlap.md) — Story 09.16 — Multi-root overlap detection.
- [specs/epics/09-library-management/story-09-17-library-audit.md](../../specs/epics/09-library-management/story-09-17-library-audit.md) — Story 09.17 — Library audit log.
- [specs/epics/09-library-management/story-09-18-chapter-inference.md](../../specs/epics/09-library-management/story-09-18-chapter-inference.md) — Story 09.18 — Chapter inference (library-side).

### 10-auth-security

- [specs/epics/10-auth-security/story-10-01-user-store.md](../../specs/epics/10-auth-security/story-10-01-user-store.md) — Story 10.01 — User store.
- [specs/epics/10-auth-security/story-10-02-web-login.md](../../specs/epics/10-auth-security/story-10-02-web-login.md) — Story 10.02 — Web login (cookie + CSRF).
- [specs/epics/10-auth-security/story-10-03-native-login.md](../../specs/epics/10-auth-security/story-10-03-native-login.md) — Story 10.03 — Native login (bearer JWT).
- [specs/epics/10-auth-security/story-10-04-token-refresh.md](../../specs/epics/10-auth-security/story-10-04-token-refresh.md) — Story 10.04 — Token refresh.
- [specs/epics/10-auth-security/story-10-05-logout-revocation.md](../../specs/epics/10-auth-security/story-10-05-logout-revocation.md) — Story 10.05 — Logout & revocation.
- [specs/epics/10-auth-security/story-10-06-rs256-keys-jwks.md](../../specs/epics/10-auth-security/story-10-06-rs256-keys-jwks.md) — Story 10.06 — RS256 keys + JWKS.
- [specs/epics/10-auth-security/story-10-07-streaming-jwt-verify.md](../../specs/epics/10-auth-security/story-10-07-streaming-jwt-verify.md) — Story 10.07 — Offline JWT verify (Streaming).
- [specs/epics/10-auth-security/story-10-08-signed-url-minter.md](../../specs/epics/10-auth-security/story-10-08-signed-url-minter.md) — Story 10.08 — Signed-URL minter.
- [specs/epics/10-auth-security/story-10-09-single-user-mode.md](../../specs/epics/10-auth-security/story-10-09-single-user-mode.md) — Story 10.09 — Single-user mode.
- [specs/epics/10-auth-security/story-10-10-csrf-protection.md](../../specs/epics/10-auth-security/story-10-10-csrf-protection.md) — Story 10.10 — CSRF protection.
- [specs/epics/10-auth-security/story-10-11-brute-force-protection.md](../../specs/epics/10-auth-security/story-10-11-brute-force-protection.md) — Story 10.11 — Brute-force protection (lockout).
- [specs/epics/10-auth-security/story-10-12-rate-limiting-auth.md](../../specs/epics/10-auth-security/story-10-12-rate-limiting-auth.md) — Story 10.12 — Auth rate limiting.
- [specs/epics/10-auth-security/story-10-13-permission-model.md](../../specs/epics/10-auth-security/story-10-13-permission-model.md) — Story 10.13 — Permission model.
- [specs/epics/10-auth-security/story-10-14-secret-loading.md](../../specs/epics/10-auth-security/story-10-14-secret-loading.md) — Story 10.14 — Secret loading.
- [specs/epics/10-auth-security/story-10-15-transport-security.md](../../specs/epics/10-auth-security/story-10-15-transport-security.md) — Story 10.15 — Transport security (TLS).
- [specs/epics/10-auth-security/story-10-16-security-audit.md](../../specs/epics/10-auth-security/story-10-16-security-audit.md) — Story 10.16 — Security audit log.
- [specs/epics/10-auth-security/story-10-17-auth-pair.md](../../specs/epics/10-auth-security/story-10-17-auth-pair.md) — Story 10.17 — Auth pair endpoint.

### 11-web-ui

- [specs/epics/11-web-ui/story-11-01-library-browser.md](../../specs/epics/11-web-ui/story-11-01-library-browser.md) — Story 11.01 — Library browser.
- [specs/epics/11-web-ui/story-11-02-video-detail-page.md](../../specs/epics/11-web-ui/story-11-02-video-detail-page.md) — Story 11.02 — Video detail page.
- [specs/epics/11-web-ui/story-11-03-video-player.md](../../specs/epics/11-web-ui/story-11-03-video-player.md) — Story 11.03 — Video player.
- [specs/epics/11-web-ui/story-11-04-search-interface.md](../../specs/epics/11-web-ui/story-11-04-search-interface.md) — Story 11.04 — Search interface.
- [specs/epics/11-web-ui/story-11-05-processing-queue-dashboard.md](../../specs/epics/11-web-ui/story-11-05-processing-queue-dashboard.md) — Story 11.05 — Processing-queue dashboard.
- [specs/epics/11-web-ui/story-11-06-settings-page.md](../../specs/epics/11-web-ui/story-11-06-settings-page.md) — Story 11.06 — Settings page.
- [specs/epics/11-web-ui/story-11-07-responsive-design.md](../../specs/epics/11-web-ui/story-11-07-responsive-design.md) — Story 11.07 — Responsive design.
- [specs/epics/11-web-ui/story-11-08-dark-light-theme.md](../../specs/epics/11-web-ui/story-11-08-dark-light-theme.md) — Story 11.08 — Dark / light theme.
- [specs/epics/11-web-ui/story-11-09-keyboard-shortcuts.md](../../specs/epics/11-web-ui/story-11-09-keyboard-shortcuts.md) — Story 11.09 — Keyboard shortcuts.
- [specs/epics/11-web-ui/story-11-10-offline-pwa.md](../../specs/epics/11-web-ui/story-11-10-offline-pwa.md) — Story 11.10 — Offline PWA.
- [specs/epics/11-web-ui/story-11-11-accessibility.md](../../specs/epics/11-web-ui/story-11-11-accessibility.md) — Story 11.11 — Accessibility.
- [specs/epics/11-web-ui/story-11-12-i18n-rtl.md](../../specs/epics/11-web-ui/story-11-12-i18n-rtl.md) — Story 11.12 — i18n + RTL.
- [specs/epics/11-web-ui/story-11-13-pat-management-api.md](../../specs/epics/11-web-ui/story-11-13-pat-management-api.md) — Story 11.13 — PAT management API.
- [specs/epics/11-web-ui/story-11-14-active-sessions-api.md](../../specs/epics/11-web-ui/story-11-14-active-sessions-api.md) — Story 11.14 — Active sessions API.

### 12-mobile

- [specs/epics/12-mobile/story-12-01-ios-app.md](../../specs/epics/12-mobile/story-12-01-ios-app.md) — Story 12.01 — iOS app shell.
- [specs/epics/12-mobile/story-12-02-android-app.md](../../specs/epics/12-mobile/story-12-02-android-app.md) — Story 12.02 — Android app shell.
- [specs/epics/12-mobile/story-12-03-native-player.md](../../specs/epics/12-mobile/story-12-03-native-player.md) — Story 12.03 — Native player.
- [specs/epics/12-mobile/story-12-04-push-notifications.md](../../specs/epics/12-mobile/story-12-04-push-notifications.md) — Story 12.04 — Push notifications.
- [specs/epics/12-mobile/story-12-05-background-playback.md](../../specs/epics/12-mobile/story-12-05-background-playback.md) — Story 12.05 — Background playback.
- [specs/epics/12-mobile/story-12-06-offline-downloads.md](../../specs/epics/12-mobile/story-12-06-offline-downloads.md) — Story 12.06 — Offline downloads.
- [specs/epics/12-mobile/story-12-07-share-cast.md](../../specs/epics/12-mobile/story-12-07-share-cast.md) — Story 12.07 — Share / cast.
- [specs/epics/12-mobile/story-12-08-haptics.md](../../specs/epics/12-mobile/story-12-08-haptics.md) — Story 12.08 — Haptics.
- [specs/epics/12-mobile/story-12-09-deep-linking.md](../../specs/epics/12-mobile/story-12-09-deep-linking.md) — Story 12.09 — Deep linking.
- [specs/epics/12-mobile/story-12-10-device-registration-api.md](../../specs/epics/12-mobile/story-12-10-device-registration-api.md) — Story 12.10 — Device registration API.
- [specs/epics/12-mobile/story-12-11-downloaded-flag-api.md](../../specs/epics/12-mobile/story-12-11-downloaded-flag-api.md) — Story 12.11 — Downloaded flag API.

### 13-desktop

- [specs/epics/13-desktop/story-13-01-macos.md](../../specs/epics/13-desktop/story-13-01-macos.md) — Story 13.01 — macOS app (Tauri).
- [specs/epics/13-desktop/story-13-02-windows.md](../../specs/epics/13-desktop/story-13-02-windows.md) — Story 13.02 — Windows app (Tauri).
- [specs/epics/13-desktop/story-13-03-linux.md](../../specs/epics/13-desktop/story-13-03-linux.md) — Story 13.03 — Linux app (Tauri).
- [specs/epics/13-desktop/story-13-04-system-tray.md](../../specs/epics/13-desktop/story-13-04-system-tray.md) — Story 13.04 — System tray.
- [specs/epics/13-desktop/story-13-05-mdns-discovery.md](../../specs/epics/13-desktop/story-13-05-mdns-discovery.md) — Story 13.05 — mDNS discovery.
- [specs/epics/13-desktop/story-13-06-drag-drop.md](../../specs/epics/13-desktop/story-13-06-drag-drop.md) — Story 13.06 — Drag-drop import.
- [specs/epics/13-desktop/story-13-07-keyboard-shortcuts.md](../../specs/epics/13-desktop/story-13-07-keyboard-shortcuts.md) — Story 13.07 — Keyboard shortcuts.
- [specs/epics/13-desktop/story-13-08-auto-update.md](../../specs/epics/13-desktop/story-13-08-auto-update.md) — Story 13.08 — Auto-update.

### 14-tv-apps

- [specs/epics/14-tv-apps/story-14-01-tvos.md](../../specs/epics/14-tv-apps/story-14-01-tvos.md) — Story 14.01 — tvOS app.
- [specs/epics/14-tv-apps/story-14-02-android-tv.md](../../specs/epics/14-tv-apps/story-14-02-android-tv.md) — Story 14.02 — Android TV app.
- [specs/epics/14-tv-apps/story-14-03-10-foot-ui.md](../../specs/epics/14-tv-apps/story-14-03-10-foot-ui.md) — Story 14.03 — 10-foot UI.
- [specs/epics/14-tv-apps/story-14-04-voice-search.md](../../specs/epics/14-tv-apps/story-14-04-voice-search.md) — Story 14.04 — Voice search.
- [specs/epics/14-tv-apps/story-14-05-continue-watching.md](../../specs/epics/14-tv-apps/story-14-05-continue-watching.md) — Story 14.05 — Continue watching.
- [specs/epics/14-tv-apps/story-14-06-recommendations-ui.md](../../specs/epics/14-tv-apps/story-14-06-recommendations-ui.md) — Story 14.06 — Recommendations UI.
- [specs/epics/14-tv-apps/story-14-07-recommendations-api.md](../../specs/epics/14-tv-apps/story-14-07-recommendations-api.md) — Story 14.07 — Recommendations API.

### 15-discovery

- [specs/epics/15-discovery/story-15-01-mdns.md](../../specs/epics/15-discovery/story-15-01-mdns.md) — Story 15.01 — mDNS server discovery.
- [specs/epics/15-discovery/story-15-02-cloud-relay.md](../../specs/epics/15-discovery/story-15-02-cloud-relay.md) — Story 15.02 — Cloud relay.
- [specs/epics/15-discovery/story-15-03-federation.md](../../specs/epics/15-discovery/story-15-03-federation.md) — Story 15.03 — Federation.
- [specs/epics/15-discovery/story-15-04-dlna-upnp.md](../../specs/epics/15-discovery/story-15-04-dlna-upnp.md) — Story 15.04 — DLNA / UPnP.
- [specs/epics/15-discovery/story-15-05-qr-pairing.md](../../specs/epics/15-discovery/story-15-05-qr-pairing.md) — Story 15.05 — QR pairing.
- [specs/epics/15-discovery/story-15-06-pairing-api.md](../../specs/epics/15-discovery/story-15-06-pairing-api.md) — Story 15.06 — Pairing API.
- [specs/epics/15-discovery/story-15-07-federation-api.md](../../specs/epics/15-discovery/story-15-07-federation-api.md) — Story 15.07 — Federation API.

### 16-subscriptions

- [specs/epics/16-subscriptions/story-16-01-free-tier.md](../../specs/epics/16-subscriptions/story-16-01-free-tier.md) — Story 16.01 — Free tier.
- [specs/epics/16-subscriptions/story-16-02-premium-features.md](../../specs/epics/16-subscriptions/story-16-02-premium-features.md) — Story 16.02 — Premium features.
- [specs/epics/16-subscriptions/story-16-03-subscription-management.md](../../specs/epics/16-subscriptions/story-16-03-subscription-management.md) — Story 16.03 — Subscription management.
- [specs/epics/16-subscriptions/story-16-04-license-validation.md](../../specs/epics/16-subscriptions/story-16-04-license-validation.md) — Story 16.04 — License validation.
- [specs/epics/16-subscriptions/story-16-05-telemetry-opt-in.md](../../specs/epics/16-subscriptions/story-16-05-telemetry-opt-in.md) — Story 16.05 — Telemetry opt-in.
- [specs/epics/16-subscriptions/story-16-06-feature-flags.md](../../specs/epics/16-subscriptions/story-16-06-feature-flags.md) — Story 16.06 — Feature flags.
- [specs/epics/16-subscriptions/story-16-07-telemetry-api.md](../../specs/epics/16-subscriptions/story-16-07-telemetry-api.md) — Story 16.07 — Telemetry API.
- [specs/epics/16-subscriptions/story-16-08-feature-flags-api.md](../../specs/epics/16-subscriptions/story-16-08-feature-flags-api.md) — Story 16.08 — Feature flags API.

### 17-ux-design-system

- [specs/epics/17-ux-design-system/story-17-01-design-tokens.md](../../specs/epics/17-ux-design-system/story-17-01-design-tokens.md) — Story 17.01 — Design tokens.
- [specs/epics/17-ux-design-system/story-17-02-component-library.md](../../specs/epics/17-ux-design-system/story-17-02-component-library.md) — Story 17.02 — Component library.
- [specs/epics/17-ux-design-system/story-17-03-motion.md](../../specs/epics/17-ux-design-system/story-17-03-motion.md) — Story 17.03 — Motion / animation.
- [specs/epics/17-ux-design-system/story-17-04-loading-states.md](../../specs/epics/17-ux-design-system/story-17-04-loading-states.md) — Story 17.04 — Loading states.
- [specs/epics/17-ux-design-system/story-17-05-error-empty-states.md](../../specs/epics/17-ux-design-system/story-17-05-error-empty-states.md) — Story 17.05 — Error & empty states.
- [specs/epics/17-ux-design-system/story-17-06-onboarding.md](../../specs/epics/17-ux-design-system/story-17-06-onboarding.md) — Story 17.06 — Onboarding.
- [specs/epics/17-ux-design-system/story-17-07-rtl-layout.md](../../specs/epics/17-ux-design-system/story-17-07-rtl-layout.md) — Story 17.07 — RTL layout.
- [specs/epics/17-ux-design-system/story-17-08-player-controls.md](../../specs/epics/17-ux-design-system/story-17-08-player-controls.md) — Story 17.08 — Player controls.
- [specs/epics/17-ux-design-system/story-17-09-search-results.md](../../specs/epics/17-ux-design-system/story-17-09-search-results.md) — Story 17.09 — Search results presentation.
- [specs/epics/17-ux-design-system/story-17-10-processing-progress.md](../../specs/epics/17-ux-design-system/story-17-10-processing-progress.md) — Story 17.10 — Processing progress presentation.
- [specs/epics/17-ux-design-system/story-17-11-transcript-presentation.md](../../specs/epics/17-ux-design-system/story-17-11-transcript-presentation.md) — Story 17.11 — Transcript presentation.

### 18-performance

- [specs/epics/18-performance/story-18-01-latency-budgets.md](../../specs/epics/18-performance/story-18-01-latency-budgets.md) — Story 18.01 — Latency budgets.
- [specs/epics/18-performance/story-18-02-search-performance.md](../../specs/epics/18-performance/story-18-02-search-performance.md) — Story 18.02 — Search performance.
- [specs/epics/18-performance/story-18-03-streaming-hot-path.md](../../specs/epics/18-performance/story-18-03-streaming-hot-path.md) — Story 18.03 — Streaming hot path.
- [specs/epics/18-performance/story-18-04-pipeline-throughput.md](../../specs/epics/18-performance/story-18-04-pipeline-throughput.md) — Story 18.04 — Pipeline throughput.
- [specs/epics/18-performance/story-18-05-memory-cpu-envelopes.md](../../specs/epics/18-performance/story-18-05-memory-cpu-envelopes.md) — Story 18.05 — Memory / CPU envelopes.
- [specs/epics/18-performance/story-18-06-client-perceived-performance.md](../../specs/epics/18-performance/story-18-06-client-perceived-performance.md) — Story 18.06 — Client-perceived performance.
- [specs/epics/18-performance/story-18-07-database-query-performance.md](../../specs/epics/18-performance/story-18-07-database-query-performance.md) — Story 18.07 — Database query performance.
- [specs/epics/18-performance/story-18-08-cache-layout-hit-rates.md](../../specs/epics/18-performance/story-18-08-cache-layout-hit-rates.md) — Story 18.08 — Cache layout / hit rates.

### 19-scalability

- [specs/epics/19-scalability/story-19-01-single-host-capacity.md](../../specs/epics/19-scalability/story-19-01-single-host-capacity.md) — Story 19.01 — Single-host capacity.
- [specs/epics/19-scalability/story-19-02-api-scale-out.md](../../specs/epics/19-scalability/story-19-02-api-scale-out.md) — Story 19.02 — API scale-out.
- [specs/epics/19-scalability/story-19-03-streaming-scale-out.md](../../specs/epics/19-scalability/story-19-03-streaming-scale-out.md) — Story 19.03 — Streaming scale-out.
- [specs/epics/19-scalability/story-19-04-pipeline-scale-out.md](../../specs/epics/19-scalability/story-19-04-pipeline-scale-out.md) — Story 19.04 — Pipeline scale-out.
- [specs/epics/19-scalability/story-19-05-database-scaling.md](../../specs/epics/19-scalability/story-19-05-database-scaling.md) — Story 19.05 — Database scaling.
- [specs/epics/19-scalability/story-19-06-storage-scaling.md](../../specs/epics/19-scalability/story-19-06-storage-scaling.md) — Story 19.06 — Storage scaling.
- [specs/epics/19-scalability/story-19-07-concurrency-caps.md](../../specs/epics/19-scalability/story-19-07-concurrency-caps.md) — Story 19.07 — Concurrency caps.
- [specs/epics/19-scalability/story-19-08-multi-tenant-readiness.md](../../specs/epics/19-scalability/story-19-08-multi-tenant-readiness.md) — Story 19.08 — Multi-tenant readiness.

### 20-testing

- [specs/epics/20-testing/story-20-01-test-pyramid.md](../../specs/epics/20-testing/story-20-01-test-pyramid.md) — Story 20.01 — Test pyramid.
- [specs/epics/20-testing/story-20-02-fixtures-seed-data.md](../../specs/epics/20-testing/story-20-02-fixtures-seed-data.md) — Story 20.02 — Fixtures & seed data.
- [specs/epics/20-testing/story-20-03-unit-test-coverage.md](../../specs/epics/20-testing/story-20-03-unit-test-coverage.md) — Story 20.03 — Unit test coverage.
- [specs/epics/20-testing/story-20-04-integration-tests.md](../../specs/epics/20-testing/story-20-04-integration-tests.md) — Story 20.04 — Integration tests.
- [specs/epics/20-testing/story-20-05-e2e-smoke-flows.md](../../specs/epics/20-testing/story-20-05-e2e-smoke-flows.md) — Story 20.05 — E2E smoke flows.
- [specs/epics/20-testing/story-20-06-contract-tests.md](../../specs/epics/20-testing/story-20-06-contract-tests.md) — Story 20.06 — Contract tests.
- [specs/epics/20-testing/story-20-07-perf-regression-ci.md](../../specs/epics/20-testing/story-20-07-perf-regression-ci.md) — Story 20.07 — Perf regression CI.
- [specs/epics/20-testing/story-20-08-flaky-test-policy.md](../../specs/epics/20-testing/story-20-08-flaky-test-policy.md) — Story 20.08 — Flaky test policy.

### 21-observability

- [specs/epics/21-observability/story-21-01-structured-logging.md](../../specs/epics/21-observability/story-21-01-structured-logging.md) — Story 21.01 — Structured logging.
- [specs/epics/21-observability/story-21-02-metrics-surface.md](../../specs/epics/21-observability/story-21-02-metrics-surface.md) — Story 21.02 — Metrics surface.
- [specs/epics/21-observability/story-21-03-distributed-tracing.md](../../specs/epics/21-observability/story-21-03-distributed-tracing.md) — Story 21.03 — Distributed tracing.
- [specs/epics/21-observability/story-21-04-health-readiness-probes.md](../../specs/epics/21-observability/story-21-04-health-readiness-probes.md) — Story 21.04 — Health / readiness probes.
- [specs/epics/21-observability/story-21-05-error-reporting.md](../../specs/epics/21-observability/story-21-05-error-reporting.md) — Story 21.05 — Error reporting.
- [specs/epics/21-observability/story-21-06-audit-log.md](../../specs/epics/21-observability/story-21-06-audit-log.md) — Story 21.06 — Audit log.
- [specs/epics/21-observability/story-21-07-job-pipeline-visibility.md](../../specs/epics/21-observability/story-21-07-job-pipeline-visibility.md) — Story 21.07 — Job pipeline visibility.
- [specs/epics/21-observability/story-21-08-telemetry-privacy.md](../../specs/epics/21-observability/story-21-08-telemetry-privacy.md) — Story 21.08 — Telemetry privacy.

### 22-devops

- [specs/epics/22-devops/story-22-01-ci-pipeline.md](../../specs/epics/22-devops/story-22-01-ci-pipeline.md) — Story 22.01 — CI pipeline.
- [specs/epics/22-devops/story-22-02-reproducible-builds.md](../../specs/epics/22-devops/story-22-02-reproducible-builds.md) — Story 22.02 — Reproducible builds.
- [specs/epics/22-devops/story-22-03-container-images.md](../../specs/epics/22-devops/story-22-03-container-images.md) — Story 22.03 — Container images.
- [specs/epics/22-devops/story-22-04-database-migrations.md](../../specs/epics/22-devops/story-22-04-database-migrations.md) — Story 22.04 — Database migrations.
- [specs/epics/22-devops/story-22-05-release-management.md](../../specs/epics/22-devops/story-22-05-release-management.md) — Story 22.05 — Release management.
- [specs/epics/22-devops/story-22-06-upgrade-rollback.md](../../specs/epics/22-devops/story-22-06-upgrade-rollback.md) — Story 22.06 — Upgrade / rollback.
- [specs/epics/22-devops/story-22-07-multi-platform-packaging.md](../../specs/epics/22-devops/story-22-07-multi-platform-packaging.md) — Story 22.07 — Multi-platform packaging.
- [specs/epics/22-devops/story-22-08-local-developer-workflow.md](../../specs/epics/22-devops/story-22-08-local-developer-workflow.md) — Story 22.08 — Local developer workflow.

### 23-security

- [specs/epics/23-security/story-23-01-authentication.md](../../specs/epics/23-security/story-23-01-authentication.md) — Story 23.01 — Authentication.
- [specs/epics/23-security/story-23-02-authorization-acls.md](../../specs/epics/23-security/story-23-02-authorization-acls.md) — Story 23.02 — Authorization & ACLs.
- [specs/epics/23-security/story-23-03-transport-security.md](../../specs/epics/23-security/story-23-03-transport-security.md) — Story 23.03 — Transport security.
- [specs/epics/23-security/story-23-04-secrets-management.md](../../specs/epics/23-security/story-23-04-secrets-management.md) — Story 23.04 — Secrets management.
- [specs/epics/23-security/story-23-05-input-validation.md](../../specs/epics/23-security/story-23-05-input-validation.md) — Story 23.05 — Input validation.
- [specs/epics/23-security/story-23-06-rate-limiting.md](../../specs/epics/23-security/story-23-06-rate-limiting.md) — Story 23.06 — Rate limiting.
- [specs/epics/23-security/story-23-07-supply-chain-security.md](../../specs/epics/23-security/story-23-07-supply-chain-security.md) — Story 23.07 — Supply-chain security.
- [specs/epics/23-security/story-23-08-coordinated-disclosure.md](../../specs/epics/23-security/story-23-08-coordinated-disclosure.md) — Story 23.08 — Coordinated disclosure.

### 24-data-integrity

- [specs/epics/24-data-integrity/story-24-01-atomic-writes.md](../../specs/epics/24-data-integrity/story-24-01-atomic-writes.md) — Story 24.01 — Atomic writes.
- [specs/epics/24-data-integrity/story-24-02-idempotent-jobs.md](../../specs/epics/24-data-integrity/story-24-02-idempotent-jobs.md) — Story 24.02 — Idempotent jobs.
- [specs/epics/24-data-integrity/story-24-03-database-constraints.md](../../specs/epics/24-data-integrity/story-24-03-database-constraints.md) — Story 24.03 — Database constraints.
- [specs/epics/24-data-integrity/story-24-04-concurrency-locking.md](../../specs/epics/24-data-integrity/story-24-04-concurrency-locking.md) — Story 24.04 — Concurrency locking.
- [specs/epics/24-data-integrity/story-24-05-backup-restore.md](../../specs/epics/24-data-integrity/story-24-05-backup-restore.md) — Story 24.05 — Backup / restore.
- [specs/epics/24-data-integrity/story-24-06-disaster-recovery.md](../../specs/epics/24-data-integrity/story-24-06-disaster-recovery.md) — Story 24.06 — Disaster recovery.
- [specs/epics/24-data-integrity/story-24-07-integrity-verification.md](../../specs/epics/24-data-integrity/story-24-07-integrity-verification.md) — Story 24.07 — Integrity verification.
- [specs/epics/24-data-integrity/story-24-08-identity-stability.md](../../specs/epics/24-data-integrity/story-24-08-identity-stability.md) — Story 24.08 — Identity stability.
- [specs/epics/24-data-integrity/story-24-09-forward-back-compat.md](../../specs/epics/24-data-integrity/story-24-09-forward-back-compat.md) — Story 24.09 — Forward / back compat.

## Implementation plans (237)

### 01-scanner

- [specs/epics/01-scanner/plan-01-01-file-discovery.md](../../specs/epics/01-scanner/plan-01-01-file-discovery.md) — Implementation plan for story 01.01 — Bootstrap a library and walk its roots.
- [specs/epics/01-scanner/plan-01-02-content-identity.md](../../specs/epics/01-scanner/plan-01-02-content-identity.md) — Implementation plan for story 01.02 — Content-addressable identity (BLAKE3).
- [specs/epics/01-scanner/plan-01-03-filesystem-watcher.md](../../specs/epics/01-scanner/plan-01-03-filesystem-watcher.md) — Implementation plan for story 01.03 — Watch for live filesystem changes.
- [specs/epics/01-scanner/plan-01-04-manual-control.md](../../specs/epics/01-scanner/plan-01-04-manual-control.md) — Implementation plan for story 01.04 — Manual control surface (start/pause/cancel scan).
- [specs/epics/01-scanner/plan-01-05-schema-decisions.md](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) — Implementation plan for story 01.05 — Schema & ownership decisions.
- [specs/epics/01-scanner/plan-01-06-video-state-machine.md](../../specs/epics/01-scanner/plan-01-06-video-state-machine.md) — Implementation plan for story 01.06 — Video state machine (DISCOVERED → READY).

### 02-audio-extraction

- [specs/epics/02-audio-extraction/plan-02-01-audio-probe.md](../../specs/epics/02-audio-extraction/plan-02-01-audio-probe.md) — Implementation plan for story 02.01 — Probe audio tracks via ffprobe.
- [specs/epics/02-audio-extraction/plan-02-01-ffprobe-binding.md](../../specs/epics/02-audio-extraction/plan-02-01-ffprobe-binding.md) — Implementation plan for story 02.01 — Probe audio tracks via ffprobe.
- [specs/epics/02-audio-extraction/plan-02-02-track-selection.md](../../specs/epics/02-audio-extraction/plan-02-02-track-selection.md) — Implementation plan for story 02.02 — Select preferred audio track.
- [specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md](../../specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md) — Implementation plan for story 02.03 — Extract PCM via FFmpeg pipe.
- [specs/epics/02-audio-extraction/plan-02-04-resource-accounting.md](../../specs/epics/02-audio-extraction/plan-02-04-resource-accounting.md) — Implementation plan for story 02.04 — Resource accounting / concurrency caps.

### 03-transcription

- [specs/epics/03-transcription/plan-03-01-backend-protocol.md](../../specs/epics/03-transcription/plan-03-01-backend-protocol.md) — Implementation plan for story 03.01 — Backend protocol (Transcriber interface).
- [specs/epics/03-transcription/plan-03-02-whisper-mlx-backend.md](../../specs/epics/03-transcription/plan-03-02-whisper-mlx-backend.md) — Implementation plan for story 03.02 — whisper.cpp / MLX backend.
- [specs/epics/03-transcription/plan-03-03-faster-whisper-backend.md](../../specs/epics/03-transcription/plan-03-03-faster-whisper-backend.md) — Implementation plan for story 03.03 — faster-whisper backend.
- [specs/epics/03-transcription/plan-03-04-openai-api-backend.md](../../specs/epics/03-transcription/plan-03-04-openai-api-backend.md) — Implementation plan for story 03.04 — OpenAI Whisper API backend.
- [specs/epics/03-transcription/plan-03-05-backend-registry.md](../../specs/epics/03-transcription/plan-03-05-backend-registry.md) — Implementation plan for story 03.05 — Backend registry & selection.
- [specs/epics/03-transcription/plan-03-06-segment-commit.md](../../specs/epics/03-transcription/plan-03-06-segment-commit.md) — Implementation plan for story 03.06 — Real-time per-segment commit (correctness keystone).
- [specs/epics/03-transcription/plan-03-07-pause-resume.md](../../specs/epics/03-transcription/plan-03-07-pause-resume.md) — Implementation plan for story 03.07 — Pause / resume mid-transcribe.
- [specs/epics/03-transcription/plan-03-08-crash-recovery.md](../../specs/epics/03-transcription/plan-03-08-crash-recovery.md) — Implementation plan for story 03.08 — Crash recovery (resume from last segment).
- [specs/epics/03-transcription/plan-03-09-diarization.md](../../specs/epics/03-transcription/plan-03-09-diarization.md) — Implementation plan for story 03.09 — Diarization (speaker turns).

### 04-subtitles

- [specs/epics/04-subtitles/plan-04-01-generate-from-segments.md](../../specs/epics/04-subtitles/plan-04-01-generate-from-segments.md) — Implementation plan for story 04.01 — Generate VTT/SRT from transcript segments.
- [specs/epics/04-subtitles/plan-04-02-formatting-wrapping.md](../../specs/epics/04-subtitles/plan-04-02-formatting-wrapping.md) — Implementation plan for story 04.02 — Line formatting & wrapping.
- [specs/epics/04-subtitles/plan-04-03-external-discovery.md](../../specs/epics/04-subtitles/plan-04-03-external-discovery.md) — Implementation plan for story 04.03 — Discover external sidecar subtitles.
- [specs/epics/04-subtitles/plan-04-04-embedded-extraction.md](../../specs/epics/04-subtitles/plan-04-04-embedded-extraction.md) — Implementation plan for story 04.04 — Extract embedded subtitle tracks.
- [specs/epics/04-subtitles/plan-04-05-live-vtt-contract.md](../../specs/epics/04-subtitles/plan-04-05-live-vtt-contract.md) — Implementation plan for story 04.05 — Live VTT contract for streaming.

### 05-search-indexing

- [specs/epics/05-search-indexing/plan-05-01-unit-chunking.md](../../specs/epics/05-search-indexing/plan-05-01-unit-chunking.md) — Implementation plan for story 05.01 — Unit chunking (transcript_units table).
- [specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) — Implementation plan for story 05.02 — FTS (SQLite FTS5 / Postgres tsvector).
- [specs/epics/05-search-indexing/plan-05-03-chroma-vector.md](../../specs/epics/05-search-indexing/plan-05-03-chroma-vector.md) — Implementation plan for story 05.03 — ChromaDB vector store.
- [specs/epics/05-search-indexing/plan-05-04-hybrid-rrf.md](../../specs/epics/05-search-indexing/plan-05-04-hybrid-rrf.md) — Implementation plan for story 05.04 — Hybrid search via reciprocal rank fusion.
- [specs/epics/05-search-indexing/plan-05-05-incremental-indexing.md](../../specs/epics/05-search-indexing/plan-05-05-incremental-indexing.md) — Implementation plan for story 05.05 — Incremental indexing.
- [specs/epics/05-search-indexing/plan-05-06-query-suggestions.md](../../specs/epics/05-search-indexing/plan-05-06-query-suggestions.md) — Implementation plan for story 05.06 — Query suggestions.
- [specs/epics/05-search-indexing/plan-05-07-chapter-inference.md](../../specs/epics/05-search-indexing/plan-05-07-chapter-inference.md) — Implementation plan for story 05.07 — Chapter inference.

### 06-job-queue

- [specs/epics/06-job-queue/plan-06-01-schema-indexes.md](../../specs/epics/06-job-queue/plan-06-01-schema-indexes.md) — Implementation plan for story 06.01 — processing_jobs schema + indexes.
- [specs/epics/06-job-queue/plan-06-02-claim-loop.md](../../specs/epics/06-job-queue/plan-06-02-claim-loop.md) — Implementation plan for story 06.02 — Worker claim loop.
- [specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md](../../specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md) — Implementation plan for story 06.03 — Heartbeat & progress reporting.
- [specs/epics/06-job-queue/plan-06-04-pause-resume-cancel.md](../../specs/epics/06-job-queue/plan-06-04-pause-resume-cancel.md) — Implementation plan for story 06.04 — Pause / resume / cancel.
- [specs/epics/06-job-queue/plan-06-05-backoff-retry.md](../../specs/epics/06-job-queue/plan-06-05-backoff-retry.md) — Implementation plan for story 06.05 — Backoff & retry policy.
- [specs/epics/06-job-queue/plan-06-06-reaper.md](../../specs/epics/06-job-queue/plan-06-06-reaper.md) — Implementation plan for story 06.06 — Reaper for dead workers.
- [specs/epics/06-job-queue/plan-06-07-concurrency-caps.md](../../specs/epics/06-job-queue/plan-06-07-concurrency-caps.md) — Implementation plan for story 06.07 — Concurrency caps.
- [specs/epics/06-job-queue/plan-06-08-graceful-shutdown.md](../../specs/epics/06-job-queue/plan-06-08-graceful-shutdown.md) — Implementation plan for story 06.08 — Graceful shutdown.
- [specs/epics/06-job-queue/plan-06-09-observability.md](../../specs/epics/06-job-queue/plan-06-09-observability.md) — Implementation plan for story 06.09 — Queue observability.
- [specs/epics/06-job-queue/plan-06-10-resume-invariant.md](../../specs/epics/06-job-queue/plan-06-10-resume-invariant.md) — Implementation plan for story 06.10 — Resume invariant guarantee.

### 07-api-server

- [specs/epics/07-api-server/plan-07-01-http-server-skeleton.md](../../specs/epics/07-api-server/plan-07-01-http-server-skeleton.md) — Implementation plan for story 07.01 — HTTP server skeleton.
- [specs/epics/07-api-server/plan-07-02-cursor-pagination.md](../../specs/epics/07-api-server/plan-07-02-cursor-pagination.md) — Implementation plan for story 07.02 — Cursor pagination middleware.
- [specs/epics/07-api-server/plan-07-03-library-crud.md](../../specs/epics/07-api-server/plan-07-03-library-crud.md) — Implementation plan for story 07.03 — Library CRUD endpoints.
- [specs/epics/07-api-server/plan-07-04-video-crud.md](../../specs/epics/07-api-server/plan-07-04-video-crud.md) — Implementation plan for story 07.04 — Video CRUD endpoints.
- [specs/epics/07-api-server/plan-07-05-video-processing-control.md](../../specs/epics/07-api-server/plan-07-05-video-processing-control.md) — Implementation plan for story 07.05 — Video processing control.
- [specs/epics/07-api-server/plan-07-06-transcript-window.md](../../specs/epics/07-api-server/plan-07-06-transcript-window.md) — Implementation plan for story 07.06 — Transcript window endpoint.
- [specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md](../../specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md) — Implementation plan for story 07.07 — Subtitles & chapters read.
- [specs/epics/07-api-server/plan-07-08-search-api.md](../../specs/epics/07-api-server/plan-07-08-search-api.md) — Implementation plan for story 07.08 — Search API.
- [specs/epics/07-api-server/plan-07-09-saved-searches.md](../../specs/epics/07-api-server/plan-07-09-saved-searches.md) — Implementation plan for story 07.09 — Saved searches.
- [specs/epics/07-api-server/plan-07-10-streaming-session-lifecycle.md](../../specs/epics/07-api-server/plan-07-10-streaming-session-lifecycle.md) — Implementation plan for story 07.10 — Streaming session lifecycle.
- [specs/epics/07-api-server/plan-07-11-watch-progress-sync.md](../../specs/epics/07-api-server/plan-07-11-watch-progress-sync.md) — Implementation plan for story 07.11 — Watch progress sync.
- [specs/epics/07-api-server/plan-07-12-job-control.md](../../specs/epics/07-api-server/plan-07-12-job-control.md) — Implementation plan for story 07.12 — Job control endpoints.
- [specs/epics/07-api-server/plan-07-13-queue-stats.md](../../specs/epics/07-api-server/plan-07-13-queue-stats.md) — Implementation plan for story 07.13 — Queue stats endpoint.
- [specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md](../../specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md) — Implementation plan for story 07.14 — Collections / tags / speakers REST.
- [specs/epics/07-api-server/plan-07-15-settings-system.md](../../specs/epics/07-api-server/plan-07-15-settings-system.md) — Implementation plan for story 07.15 — Settings & system endpoints.
- [specs/epics/07-api-server/plan-07-16-websocket-fanout.md](../../specs/epics/07-api-server/plan-07-16-websocket-fanout.md) — Implementation plan for story 07.16 — WebSocket event fan-out.
- [specs/epics/07-api-server/plan-07-17-graphql-schema.md](../../specs/epics/07-api-server/plan-07-17-graphql-schema.md) — Implementation plan for story 07.17 — GraphQL schema.
- [specs/epics/07-api-server/plan-07-18-grpc-clients.md](../../specs/epics/07-api-server/plan-07-18-grpc-clients.md) — Implementation plan for story 07.18 — gRPC clients (to Pipeline + Streaming).
- [specs/epics/07-api-server/plan-07-19-validation-rate-limiting.md](../../specs/epics/07-api-server/plan-07-19-validation-rate-limiting.md) — Implementation plan for story 07.19 — Validation + rate limiting middleware.
- [specs/epics/07-api-server/plan-07-20-health-version-metrics.md](../../specs/epics/07-api-server/plan-07-20-health-version-metrics.md) — Implementation plan for story 07.20 — Health / version / metrics.
- [specs/epics/07-api-server/plan-07-21-recommendations.md](../../specs/epics/07-api-server/plan-07-21-recommendations.md) — Implementation plan for story 07.21 — Recommendations endpoint.
- [specs/epics/07-api-server/plan-07-22-devices-register.md](../../specs/epics/07-api-server/plan-07-22-devices-register.md) — Implementation plan for story 07.22 — Devices register endpoint.

### 08-streaming

- [specs/epics/08-streaming/plan-08-01-server-skeleton.md](../../specs/epics/08-streaming/plan-08-01-server-skeleton.md) — Implementation plan for story 08.01 — Streaming server skeleton + signed-URL middleware.
- [specs/epics/08-streaming/plan-08-02-capability-matrix.md](../../specs/epics/08-streaming/plan-08-02-capability-matrix.md) — Implementation plan for story 08.02 — Capability matrix.
- [specs/epics/08-streaming/plan-08-03-direct-play.md](../../specs/epics/08-streaming/plan-08-03-direct-play.md) — Implementation plan for story 08.03 — Direct play.
- [specs/epics/08-streaming/plan-08-04-direct-stream-remux.md](../../specs/epics/08-streaming/plan-08-04-direct-stream-remux.md) — Implementation plan for story 08.04 — Direct stream (remux).
- [specs/epics/08-streaming/plan-08-05-hls-transcode.md](../../specs/epics/08-streaming/plan-08-05-hls-transcode.md) — Implementation plan for story 08.05 — HLS transcode.
- [specs/epics/08-streaming/plan-08-06-dash-manifest.md](../../specs/epics/08-streaming/plan-08-06-dash-manifest.md) — Implementation plan for story 08.06 — DASH manifest.
- [specs/epics/08-streaming/plan-08-07-hwaccel-detect.md](../../specs/epics/08-streaming/plan-08-07-hwaccel-detect.md) — Implementation plan for story 08.07 — Hardware accel detection.
- [specs/epics/08-streaming/plan-08-08-grpc-server.md](../../specs/epics/08-streaming/plan-08-08-grpc-server.md) — Implementation plan for story 08.08 — Streaming gRPC server.
- [specs/epics/08-streaming/plan-08-09-session-store.md](../../specs/epics/08-streaming/plan-08-09-session-store.md) — Implementation plan for story 08.09 — Session store + reaper.
- [specs/epics/08-streaming/plan-08-10-concurrency-caps.md](../../specs/epics/08-streaming/plan-08-10-concurrency-caps.md) — Implementation plan for story 08.10 — Concurrency caps.
- [specs/epics/08-streaming/plan-08-11-live-subtitle.md](../../specs/epics/08-streaming/plan-08-11-live-subtitle.md) — Implementation plan for story 08.11 — Live subtitle delivery.
- [specs/epics/08-streaming/plan-08-12-chapter-delivery.md](../../specs/epics/08-streaming/plan-08-12-chapter-delivery.md) — Implementation plan for story 08.12 — Chapter delivery.
- [specs/epics/08-streaming/plan-08-13-posters-sprites.md](../../specs/epics/08-streaming/plan-08-13-posters-sprites.md) — Implementation plan for story 08.13 — Posters & sprites.
- [specs/epics/08-streaming/plan-08-14-cache-gc.md](../../specs/epics/08-streaming/plan-08-14-cache-gc.md) — Implementation plan for story 08.14 — Cache GC.
- [specs/epics/08-streaming/plan-08-15-probe-cache.md](../../specs/epics/08-streaming/plan-08-15-probe-cache.md) — Implementation plan for story 08.15 — Probe cache.

### 09-library-management

- [specs/epics/09-library-management/plan-09-01-library-config-schema.md](../../specs/epics/09-library-management/plan-09-01-library-config-schema.md) — Implementation plan for story 09.01 — Library config schema.
- [specs/epics/09-library-management/plan-09-02-filesystem-watcher.md](../../specs/epics/09-library-management/plan-09-02-filesystem-watcher.md) — Implementation plan for story 09.02 — Library filesystem watcher.
- [specs/epics/09-library-management/plan-09-03-periodic-sweep.md](../../specs/epics/09-library-management/plan-09-03-periodic-sweep.md) — Implementation plan for story 09.03 — Periodic sweep.
- [specs/epics/09-library-management/plan-09-04-content-hash-dedup.md](../../specs/epics/09-library-management/plan-09-04-content-hash-dedup.md) — Implementation plan for story 09.04 — Content-hash dedup.
- [specs/epics/09-library-management/plan-09-05-ignore-rules.md](../../specs/epics/09-library-management/plan-09-05-ignore-rules.md) — Implementation plan for story 09.05 — Ignore rules.
- [specs/epics/09-library-management/plan-09-06-manual-scan.md](../../specs/epics/09-library-management/plan-09-06-manual-scan.md) — Implementation plan for story 09.06 — Manual scan trigger.
- [specs/epics/09-library-management/plan-09-07-library-stats.md](../../specs/epics/09-library-management/plan-09-07-library-stats.md) — Implementation plan for story 09.07 — Library stats.
- [specs/epics/09-library-management/plan-09-08-language-tag.md](../../specs/epics/09-library-management/plan-09-08-language-tag.md) — Implementation plan for story 09.08 — Language auto-tag.
- [specs/epics/09-library-management/plan-09-09-topic-tag.md](../../specs/epics/09-library-management/plan-09-09-topic-tag.md) — Implementation plan for story 09.09 — Topic auto-tag.
- [specs/epics/09-library-management/plan-09-10-content-type-classifier.md](../../specs/epics/09-library-management/plan-09-10-content-type-classifier.md) — Implementation plan for story 09.10 — Content-type classifier.
- [specs/epics/09-library-management/plan-09-11-speakers.md](../../specs/epics/09-library-management/plan-09-11-speakers.md) — Implementation plan for story 09.11 — Speaker management.
- [specs/epics/09-library-management/plan-09-12-tag-crud.md](../../specs/epics/09-library-management/plan-09-12-tag-crud.md) — Implementation plan for story 09.12 — Tag CRUD.
- [specs/epics/09-library-management/plan-09-13-collections-manual.md](../../specs/epics/09-library-management/plan-09-13-collections-manual.md) — Implementation plan for story 09.13 — Manual collections.
- [specs/epics/09-library-management/plan-09-14-smart-collections.md](../../specs/epics/09-library-management/plan-09-14-smart-collections.md) — Implementation plan for story 09.14 — Smart collections.
- [specs/epics/09-library-management/plan-09-15-library-deletion.md](../../specs/epics/09-library-management/plan-09-15-library-deletion.md) — Implementation plan for story 09.15 — Library deletion + cascades.
- [specs/epics/09-library-management/plan-09-16-multi-root-overlap.md](../../specs/epics/09-library-management/plan-09-16-multi-root-overlap.md) — Implementation plan for story 09.16 — Multi-root overlap detection.
- [specs/epics/09-library-management/plan-09-17-library-audit.md](../../specs/epics/09-library-management/plan-09-17-library-audit.md) — Implementation plan for story 09.17 — Library audit log.
- [specs/epics/09-library-management/plan-09-18-chapter-inference.md](../../specs/epics/09-library-management/plan-09-18-chapter-inference.md) — Implementation plan for story 09.18 — Chapter inference (library-side).

### 10-auth-security

- [specs/epics/10-auth-security/plan-10-01-user-store.md](../../specs/epics/10-auth-security/plan-10-01-user-store.md) — Implementation plan for story 10.01 — User store.
- [specs/epics/10-auth-security/plan-10-02-web-login.md](../../specs/epics/10-auth-security/plan-10-02-web-login.md) — Implementation plan for story 10.02 — Web login (cookie + CSRF).
- [specs/epics/10-auth-security/plan-10-03-native-login.md](../../specs/epics/10-auth-security/plan-10-03-native-login.md) — Implementation plan for story 10.03 — Native login (bearer JWT).
- [specs/epics/10-auth-security/plan-10-04-token-refresh.md](../../specs/epics/10-auth-security/plan-10-04-token-refresh.md) — Implementation plan for story 10.04 — Token refresh.
- [specs/epics/10-auth-security/plan-10-05-logout-revocation.md](../../specs/epics/10-auth-security/plan-10-05-logout-revocation.md) — Implementation plan for story 10.05 — Logout & revocation.
- [specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md](../../specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md) — Implementation plan for story 10.06 — RS256 keys + JWKS.
- [specs/epics/10-auth-security/plan-10-07-streaming-jwt-verify.md](../../specs/epics/10-auth-security/plan-10-07-streaming-jwt-verify.md) — Implementation plan for story 10.07 — Offline JWT verify (Streaming).
- [specs/epics/10-auth-security/plan-10-08-signed-url-minter.md](../../specs/epics/10-auth-security/plan-10-08-signed-url-minter.md) — Implementation plan for story 10.08 — Signed-URL minter.
- [specs/epics/10-auth-security/plan-10-09-single-user-mode.md](../../specs/epics/10-auth-security/plan-10-09-single-user-mode.md) — Implementation plan for story 10.09 — Single-user mode.
- [specs/epics/10-auth-security/plan-10-10-csrf-protection.md](../../specs/epics/10-auth-security/plan-10-10-csrf-protection.md) — Implementation plan for story 10.10 — CSRF protection.
- [specs/epics/10-auth-security/plan-10-11-brute-force-protection.md](../../specs/epics/10-auth-security/plan-10-11-brute-force-protection.md) — Implementation plan for story 10.11 — Brute-force protection (lockout).
- [specs/epics/10-auth-security/plan-10-12-rate-limiting-auth.md](../../specs/epics/10-auth-security/plan-10-12-rate-limiting-auth.md) — Implementation plan for story 10.12 — Auth rate limiting.
- [specs/epics/10-auth-security/plan-10-13-permission-model.md](../../specs/epics/10-auth-security/plan-10-13-permission-model.md) — Implementation plan for story 10.13 — Permission model.
- [specs/epics/10-auth-security/plan-10-14-secret-loading.md](../../specs/epics/10-auth-security/plan-10-14-secret-loading.md) — Implementation plan for story 10.14 — Secret loading.
- [specs/epics/10-auth-security/plan-10-15-transport-security.md](../../specs/epics/10-auth-security/plan-10-15-transport-security.md) — Implementation plan for story 10.15 — Transport security (TLS).
- [specs/epics/10-auth-security/plan-10-16-security-audit.md](../../specs/epics/10-auth-security/plan-10-16-security-audit.md) — Implementation plan for story 10.16 — Security audit log.
- [specs/epics/10-auth-security/plan-10-17-auth-pair.md](../../specs/epics/10-auth-security/plan-10-17-auth-pair.md) — Implementation plan for story 10.17 — Auth pair endpoint.

### 11-web-ui

- [specs/epics/11-web-ui/plan-11-01-library-browser.md](../../specs/epics/11-web-ui/plan-11-01-library-browser.md) — Implementation plan for story 11.01 — Library browser.
- [specs/epics/11-web-ui/plan-11-02-video-detail-page.md](../../specs/epics/11-web-ui/plan-11-02-video-detail-page.md) — Implementation plan for story 11.02 — Video detail page.
- [specs/epics/11-web-ui/plan-11-03-video-player.md](../../specs/epics/11-web-ui/plan-11-03-video-player.md) — Implementation plan for story 11.03 — Video player.
- [specs/epics/11-web-ui/plan-11-04-search-interface.md](../../specs/epics/11-web-ui/plan-11-04-search-interface.md) — Implementation plan for story 11.04 — Search interface.
- [specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md](../../specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md) — Implementation plan for story 11.05 — Processing-queue dashboard.
- [specs/epics/11-web-ui/plan-11-06-settings-page.md](../../specs/epics/11-web-ui/plan-11-06-settings-page.md) — Implementation plan for story 11.06 — Settings page.
- [specs/epics/11-web-ui/plan-11-07-responsive-design.md](../../specs/epics/11-web-ui/plan-11-07-responsive-design.md) — Implementation plan for story 11.07 — Responsive design.
- [specs/epics/11-web-ui/plan-11-08-dark-light-theme.md](../../specs/epics/11-web-ui/plan-11-08-dark-light-theme.md) — Implementation plan for story 11.08 — Dark / light theme.
- [specs/epics/11-web-ui/plan-11-09-keyboard-shortcuts.md](../../specs/epics/11-web-ui/plan-11-09-keyboard-shortcuts.md) — Implementation plan for story 11.09 — Keyboard shortcuts.
- [specs/epics/11-web-ui/plan-11-10-offline-pwa.md](../../specs/epics/11-web-ui/plan-11-10-offline-pwa.md) — Implementation plan for story 11.10 — Offline PWA.
- [specs/epics/11-web-ui/plan-11-11-accessibility.md](../../specs/epics/11-web-ui/plan-11-11-accessibility.md) — Implementation plan for story 11.11 — Accessibility.
- [specs/epics/11-web-ui/plan-11-12-i18n-rtl.md](../../specs/epics/11-web-ui/plan-11-12-i18n-rtl.md) — Implementation plan for story 11.12 — i18n + RTL.
- [specs/epics/11-web-ui/plan-11-13-pat-management-api.md](../../specs/epics/11-web-ui/plan-11-13-pat-management-api.md) — Implementation plan for story 11.13 — PAT management API.
- [specs/epics/11-web-ui/plan-11-14-active-sessions-api.md](../../specs/epics/11-web-ui/plan-11-14-active-sessions-api.md) — Implementation plan for story 11.14 — Active sessions API.

### 12-mobile

- [specs/epics/12-mobile/plan-12-01-ios-app.md](../../specs/epics/12-mobile/plan-12-01-ios-app.md) — Implementation plan for story 12.01 — iOS app shell.
- [specs/epics/12-mobile/plan-12-02-android-app.md](../../specs/epics/12-mobile/plan-12-02-android-app.md) — Implementation plan for story 12.02 — Android app shell.
- [specs/epics/12-mobile/plan-12-03-native-player.md](../../specs/epics/12-mobile/plan-12-03-native-player.md) — Implementation plan for story 12.03 — Native player.
- [specs/epics/12-mobile/plan-12-04-push-notifications.md](../../specs/epics/12-mobile/plan-12-04-push-notifications.md) — Implementation plan for story 12.04 — Push notifications.
- [specs/epics/12-mobile/plan-12-05-background-playback.md](../../specs/epics/12-mobile/plan-12-05-background-playback.md) — Implementation plan for story 12.05 — Background playback.
- [specs/epics/12-mobile/plan-12-06-offline-downloads.md](../../specs/epics/12-mobile/plan-12-06-offline-downloads.md) — Implementation plan for story 12.06 — Offline downloads.
- [specs/epics/12-mobile/plan-12-07-share-cast.md](../../specs/epics/12-mobile/plan-12-07-share-cast.md) — Implementation plan for story 12.07 — Share / cast.
- [specs/epics/12-mobile/plan-12-08-haptics.md](../../specs/epics/12-mobile/plan-12-08-haptics.md) — Implementation plan for story 12.08 — Haptics.
- [specs/epics/12-mobile/plan-12-09-deep-linking.md](../../specs/epics/12-mobile/plan-12-09-deep-linking.md) — Implementation plan for story 12.09 — Deep linking.
- [specs/epics/12-mobile/plan-12-10-device-registration-api.md](../../specs/epics/12-mobile/plan-12-10-device-registration-api.md) — Implementation plan for story 12.10 — Device registration API.
- [specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md](../../specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md) — Implementation plan for story 12.11 — Downloaded flag API.

### 13-desktop

- [specs/epics/13-desktop/plan-13-01-macos.md](../../specs/epics/13-desktop/plan-13-01-macos.md) — Implementation plan for story 13.01 — macOS app (Tauri).
- [specs/epics/13-desktop/plan-13-02-windows.md](../../specs/epics/13-desktop/plan-13-02-windows.md) — Implementation plan for story 13.02 — Windows app (Tauri).
- [specs/epics/13-desktop/plan-13-03-linux.md](../../specs/epics/13-desktop/plan-13-03-linux.md) — Implementation plan for story 13.03 — Linux app (Tauri).
- [specs/epics/13-desktop/plan-13-04-system-tray.md](../../specs/epics/13-desktop/plan-13-04-system-tray.md) — Implementation plan for story 13.04 — System tray.
- [specs/epics/13-desktop/plan-13-05-mdns-discovery.md](../../specs/epics/13-desktop/plan-13-05-mdns-discovery.md) — Implementation plan for story 13.05 — mDNS discovery.
- [specs/epics/13-desktop/plan-13-06-drag-drop.md](../../specs/epics/13-desktop/plan-13-06-drag-drop.md) — Implementation plan for story 13.06 — Drag-drop import.
- [specs/epics/13-desktop/plan-13-07-keyboard-shortcuts.md](../../specs/epics/13-desktop/plan-13-07-keyboard-shortcuts.md) — Implementation plan for story 13.07 — Keyboard shortcuts.
- [specs/epics/13-desktop/plan-13-08-auto-update.md](../../specs/epics/13-desktop/plan-13-08-auto-update.md) — Implementation plan for story 13.08 — Auto-update.

### 14-tv-apps

- [specs/epics/14-tv-apps/plan-14-01-tvos.md](../../specs/epics/14-tv-apps/plan-14-01-tvos.md) — Implementation plan for story 14.01 — tvOS app.
- [specs/epics/14-tv-apps/plan-14-02-android-tv.md](../../specs/epics/14-tv-apps/plan-14-02-android-tv.md) — Implementation plan for story 14.02 — Android TV app.
- [specs/epics/14-tv-apps/plan-14-03-10-foot-ui.md](../../specs/epics/14-tv-apps/plan-14-03-10-foot-ui.md) — Implementation plan for story 14.03 — 10-foot UI.
- [specs/epics/14-tv-apps/plan-14-04-voice-search.md](../../specs/epics/14-tv-apps/plan-14-04-voice-search.md) — Implementation plan for story 14.04 — Voice search.
- [specs/epics/14-tv-apps/plan-14-05-continue-watching.md](../../specs/epics/14-tv-apps/plan-14-05-continue-watching.md) — Implementation plan for story 14.05 — Continue watching.
- [specs/epics/14-tv-apps/plan-14-06-recommendations-ui.md](../../specs/epics/14-tv-apps/plan-14-06-recommendations-ui.md) — Implementation plan for story 14.06 — Recommendations UI.
- [specs/epics/14-tv-apps/plan-14-07-recommendations-api.md](../../specs/epics/14-tv-apps/plan-14-07-recommendations-api.md) — Implementation plan for story 14.07 — Recommendations API.

### 15-discovery

- [specs/epics/15-discovery/plan-15-01-mdns.md](../../specs/epics/15-discovery/plan-15-01-mdns.md) — Implementation plan for story 15.01 — mDNS server discovery.
- [specs/epics/15-discovery/plan-15-02-cloud-relay.md](../../specs/epics/15-discovery/plan-15-02-cloud-relay.md) — Implementation plan for story 15.02 — Cloud relay.
- [specs/epics/15-discovery/plan-15-03-federation.md](../../specs/epics/15-discovery/plan-15-03-federation.md) — Implementation plan for story 15.03 — Federation.
- [specs/epics/15-discovery/plan-15-04-dlna-upnp.md](../../specs/epics/15-discovery/plan-15-04-dlna-upnp.md) — Implementation plan for story 15.04 — DLNA / UPnP.
- [specs/epics/15-discovery/plan-15-05-qr-pairing.md](../../specs/epics/15-discovery/plan-15-05-qr-pairing.md) — Implementation plan for story 15.05 — QR pairing.
- [specs/epics/15-discovery/plan-15-06-pairing-api.md](../../specs/epics/15-discovery/plan-15-06-pairing-api.md) — Implementation plan for story 15.06 — Pairing API.
- [specs/epics/15-discovery/plan-15-07-federation-api.md](../../specs/epics/15-discovery/plan-15-07-federation-api.md) — Implementation plan for story 15.07 — Federation API.

### 16-subscriptions

- [specs/epics/16-subscriptions/plan-16-01-free-tier.md](../../specs/epics/16-subscriptions/plan-16-01-free-tier.md) — Implementation plan for story 16.01 — Free tier.
- [specs/epics/16-subscriptions/plan-16-02-premium-features.md](../../specs/epics/16-subscriptions/plan-16-02-premium-features.md) — Implementation plan for story 16.02 — Premium features.
- [specs/epics/16-subscriptions/plan-16-03-subscription-management.md](../../specs/epics/16-subscriptions/plan-16-03-subscription-management.md) — Implementation plan for story 16.03 — Subscription management.
- [specs/epics/16-subscriptions/plan-16-04-license-validation.md](../../specs/epics/16-subscriptions/plan-16-04-license-validation.md) — Implementation plan for story 16.04 — License validation.
- [specs/epics/16-subscriptions/plan-16-05-telemetry-opt-in.md](../../specs/epics/16-subscriptions/plan-16-05-telemetry-opt-in.md) — Implementation plan for story 16.05 — Telemetry opt-in.
- [specs/epics/16-subscriptions/plan-16-06-feature-flags.md](../../specs/epics/16-subscriptions/plan-16-06-feature-flags.md) — Implementation plan for story 16.06 — Feature flags.
- [specs/epics/16-subscriptions/plan-16-07-telemetry-api.md](../../specs/epics/16-subscriptions/plan-16-07-telemetry-api.md) — Implementation plan for story 16.07 — Telemetry API.
- [specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md](../../specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md) — Implementation plan for story 16.08 — Feature flags API.

### 17-ux-design-system

- [specs/epics/17-ux-design-system/plan-17-01-design-tokens.md](../../specs/epics/17-ux-design-system/plan-17-01-design-tokens.md) — Implementation plan for story 17.01 — Design tokens.
- [specs/epics/17-ux-design-system/plan-17-02-component-library.md](../../specs/epics/17-ux-design-system/plan-17-02-component-library.md) — Implementation plan for story 17.02 — Component library.
- [specs/epics/17-ux-design-system/plan-17-03-motion.md](../../specs/epics/17-ux-design-system/plan-17-03-motion.md) — Implementation plan for story 17.03 — Motion / animation.
- [specs/epics/17-ux-design-system/plan-17-04-loading-states.md](../../specs/epics/17-ux-design-system/plan-17-04-loading-states.md) — Implementation plan for story 17.04 — Loading states.
- [specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md](../../specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md) — Implementation plan for story 17.05 — Error & empty states.
- [specs/epics/17-ux-design-system/plan-17-06-onboarding.md](../../specs/epics/17-ux-design-system/plan-17-06-onboarding.md) — Implementation plan for story 17.06 — Onboarding.
- [specs/epics/17-ux-design-system/plan-17-07-rtl-layout.md](../../specs/epics/17-ux-design-system/plan-17-07-rtl-layout.md) — Implementation plan for story 17.07 — RTL layout.
- [specs/epics/17-ux-design-system/plan-17-08-player-controls.md](../../specs/epics/17-ux-design-system/plan-17-08-player-controls.md) — Implementation plan for story 17.08 — Player controls.
- [specs/epics/17-ux-design-system/plan-17-09-search-results.md](../../specs/epics/17-ux-design-system/plan-17-09-search-results.md) — Implementation plan for story 17.09 — Search results presentation.
- [specs/epics/17-ux-design-system/plan-17-10-processing-progress.md](../../specs/epics/17-ux-design-system/plan-17-10-processing-progress.md) — Implementation plan for story 17.10 — Processing progress presentation.
- [specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md](../../specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md) — Implementation plan for story 17.11 — Transcript presentation.

### 18-performance

- [specs/epics/18-performance/plan-18-01-latency-budgets.md](../../specs/epics/18-performance/plan-18-01-latency-budgets.md) — Implementation plan for story 18.01 — Latency budgets.
- [specs/epics/18-performance/plan-18-02-search-performance.md](../../specs/epics/18-performance/plan-18-02-search-performance.md) — Implementation plan for story 18.02 — Search performance.
- [specs/epics/18-performance/plan-18-03-streaming-hot-path.md](../../specs/epics/18-performance/plan-18-03-streaming-hot-path.md) — Implementation plan for story 18.03 — Streaming hot path.
- [specs/epics/18-performance/plan-18-04-pipeline-throughput.md](../../specs/epics/18-performance/plan-18-04-pipeline-throughput.md) — Implementation plan for story 18.04 — Pipeline throughput.
- [specs/epics/18-performance/plan-18-05-memory-cpu-envelopes.md](../../specs/epics/18-performance/plan-18-05-memory-cpu-envelopes.md) — Implementation plan for story 18.05 — Memory / CPU envelopes.
- [specs/epics/18-performance/plan-18-06-client-perceived-performance.md](../../specs/epics/18-performance/plan-18-06-client-perceived-performance.md) — Implementation plan for story 18.06 — Client-perceived performance.
- [specs/epics/18-performance/plan-18-07-database-query-performance.md](../../specs/epics/18-performance/plan-18-07-database-query-performance.md) — Implementation plan for story 18.07 — Database query performance.
- [specs/epics/18-performance/plan-18-08-cache-layout-hit-rates.md](../../specs/epics/18-performance/plan-18-08-cache-layout-hit-rates.md) — Implementation plan for story 18.08 — Cache layout / hit rates.

### 19-scalability

- [specs/epics/19-scalability/plan-19-01-single-host-capacity.md](../../specs/epics/19-scalability/plan-19-01-single-host-capacity.md) — Implementation plan for story 19.01 — Single-host capacity.
- [specs/epics/19-scalability/plan-19-02-api-scale-out.md](../../specs/epics/19-scalability/plan-19-02-api-scale-out.md) — Implementation plan for story 19.02 — API scale-out.
- [specs/epics/19-scalability/plan-19-03-streaming-scale-out.md](../../specs/epics/19-scalability/plan-19-03-streaming-scale-out.md) — Implementation plan for story 19.03 — Streaming scale-out.
- [specs/epics/19-scalability/plan-19-04-pipeline-scale-out.md](../../specs/epics/19-scalability/plan-19-04-pipeline-scale-out.md) — Implementation plan for story 19.04 — Pipeline scale-out.
- [specs/epics/19-scalability/plan-19-05-database-scaling.md](../../specs/epics/19-scalability/plan-19-05-database-scaling.md) — Implementation plan for story 19.05 — Database scaling.
- [specs/epics/19-scalability/plan-19-06-storage-scaling.md](../../specs/epics/19-scalability/plan-19-06-storage-scaling.md) — Implementation plan for story 19.06 — Storage scaling.
- [specs/epics/19-scalability/plan-19-07-concurrency-caps.md](../../specs/epics/19-scalability/plan-19-07-concurrency-caps.md) — Implementation plan for story 19.07 — Concurrency caps.
- [specs/epics/19-scalability/plan-19-08-multi-tenant-readiness.md](../../specs/epics/19-scalability/plan-19-08-multi-tenant-readiness.md) — Implementation plan for story 19.08 — Multi-tenant readiness.

### 20-testing

- [specs/epics/20-testing/plan-20-01-test-pyramid.md](../../specs/epics/20-testing/plan-20-01-test-pyramid.md) — Implementation plan for story 20.01 — Test pyramid.
- [specs/epics/20-testing/plan-20-02-fixtures-seed-data.md](../../specs/epics/20-testing/plan-20-02-fixtures-seed-data.md) — Implementation plan for story 20.02 — Fixtures & seed data.
- [specs/epics/20-testing/plan-20-03-unit-test-coverage.md](../../specs/epics/20-testing/plan-20-03-unit-test-coverage.md) — Implementation plan for story 20.03 — Unit test coverage.
- [specs/epics/20-testing/plan-20-04-integration-tests.md](../../specs/epics/20-testing/plan-20-04-integration-tests.md) — Implementation plan for story 20.04 — Integration tests.
- [specs/epics/20-testing/plan-20-05-e2e-smoke-flows.md](../../specs/epics/20-testing/plan-20-05-e2e-smoke-flows.md) — Implementation plan for story 20.05 — E2E smoke flows.
- [specs/epics/20-testing/plan-20-06-contract-tests.md](../../specs/epics/20-testing/plan-20-06-contract-tests.md) — Implementation plan for story 20.06 — Contract tests.
- [specs/epics/20-testing/plan-20-07-perf-regression-ci.md](../../specs/epics/20-testing/plan-20-07-perf-regression-ci.md) — Implementation plan for story 20.07 — Perf regression CI.
- [specs/epics/20-testing/plan-20-08-flaky-test-policy.md](../../specs/epics/20-testing/plan-20-08-flaky-test-policy.md) — Implementation plan for story 20.08 — Flaky test policy.

### 21-observability

- [specs/epics/21-observability/plan-21-01-structured-logging.md](../../specs/epics/21-observability/plan-21-01-structured-logging.md) — Implementation plan for story 21.01 — Structured logging.
- [specs/epics/21-observability/plan-21-02-metrics-surface.md](../../specs/epics/21-observability/plan-21-02-metrics-surface.md) — Implementation plan for story 21.02 — Metrics surface.
- [specs/epics/21-observability/plan-21-03-distributed-tracing.md](../../specs/epics/21-observability/plan-21-03-distributed-tracing.md) — Implementation plan for story 21.03 — Distributed tracing.
- [specs/epics/21-observability/plan-21-04-health-readiness-probes.md](../../specs/epics/21-observability/plan-21-04-health-readiness-probes.md) — Implementation plan for story 21.04 — Health / readiness probes.
- [specs/epics/21-observability/plan-21-05-error-reporting.md](../../specs/epics/21-observability/plan-21-05-error-reporting.md) — Implementation plan for story 21.05 — Error reporting.
- [specs/epics/21-observability/plan-21-06-audit-log.md](../../specs/epics/21-observability/plan-21-06-audit-log.md) — Implementation plan for story 21.06 — Audit log.
- [specs/epics/21-observability/plan-21-07-job-pipeline-visibility.md](../../specs/epics/21-observability/plan-21-07-job-pipeline-visibility.md) — Implementation plan for story 21.07 — Job pipeline visibility.
- [specs/epics/21-observability/plan-21-08-telemetry-privacy.md](../../specs/epics/21-observability/plan-21-08-telemetry-privacy.md) — Implementation plan for story 21.08 — Telemetry privacy.

### 22-devops

- [specs/epics/22-devops/plan-22-01-ci-pipeline.md](../../specs/epics/22-devops/plan-22-01-ci-pipeline.md) — Implementation plan for story 22.01 — CI pipeline.
- [specs/epics/22-devops/plan-22-02-reproducible-builds.md](../../specs/epics/22-devops/plan-22-02-reproducible-builds.md) — Implementation plan for story 22.02 — Reproducible builds.
- [specs/epics/22-devops/plan-22-03-container-images.md](../../specs/epics/22-devops/plan-22-03-container-images.md) — Implementation plan for story 22.03 — Container images.
- [specs/epics/22-devops/plan-22-04-database-migrations.md](../../specs/epics/22-devops/plan-22-04-database-migrations.md) — Implementation plan for story 22.04 — Database migrations.
- [specs/epics/22-devops/plan-22-05-release-management.md](../../specs/epics/22-devops/plan-22-05-release-management.md) — Implementation plan for story 22.05 — Release management.
- [specs/epics/22-devops/plan-22-06-upgrade-rollback.md](../../specs/epics/22-devops/plan-22-06-upgrade-rollback.md) — Implementation plan for story 22.06 — Upgrade / rollback.
- [specs/epics/22-devops/plan-22-07-multi-platform-packaging.md](../../specs/epics/22-devops/plan-22-07-multi-platform-packaging.md) — Implementation plan for story 22.07 — Multi-platform packaging.
- [specs/epics/22-devops/plan-22-08-local-developer-workflow.md](../../specs/epics/22-devops/plan-22-08-local-developer-workflow.md) — Implementation plan for story 22.08 — Local developer workflow.

### 23-security

- [specs/epics/23-security/plan-23-01-authentication.md](../../specs/epics/23-security/plan-23-01-authentication.md) — Implementation plan for story 23.01 — Authentication.
- [specs/epics/23-security/plan-23-02-authorization-acls.md](../../specs/epics/23-security/plan-23-02-authorization-acls.md) — Implementation plan for story 23.02 — Authorization & ACLs.
- [specs/epics/23-security/plan-23-03-transport-security.md](../../specs/epics/23-security/plan-23-03-transport-security.md) — Implementation plan for story 23.03 — Transport security.
- [specs/epics/23-security/plan-23-04-secrets-management.md](../../specs/epics/23-security/plan-23-04-secrets-management.md) — Implementation plan for story 23.04 — Secrets management.
- [specs/epics/23-security/plan-23-05-input-validation.md](../../specs/epics/23-security/plan-23-05-input-validation.md) — Implementation plan for story 23.05 — Input validation.
- [specs/epics/23-security/plan-23-06-rate-limiting.md](../../specs/epics/23-security/plan-23-06-rate-limiting.md) — Implementation plan for story 23.06 — Rate limiting.
- [specs/epics/23-security/plan-23-07-supply-chain-security.md](../../specs/epics/23-security/plan-23-07-supply-chain-security.md) — Implementation plan for story 23.07 — Supply-chain security.
- [specs/epics/23-security/plan-23-08-coordinated-disclosure.md](../../specs/epics/23-security/plan-23-08-coordinated-disclosure.md) — Implementation plan for story 23.08 — Coordinated disclosure.

### 24-data-integrity

- [specs/epics/24-data-integrity/plan-24-01-atomic-writes.md](../../specs/epics/24-data-integrity/plan-24-01-atomic-writes.md) — Implementation plan for story 24.01 — Atomic writes.
- [specs/epics/24-data-integrity/plan-24-02-idempotent-jobs.md](../../specs/epics/24-data-integrity/plan-24-02-idempotent-jobs.md) — Implementation plan for story 24.02 — Idempotent jobs.
- [specs/epics/24-data-integrity/plan-24-03-database-constraints.md](../../specs/epics/24-data-integrity/plan-24-03-database-constraints.md) — Implementation plan for story 24.03 — Database constraints.
- [specs/epics/24-data-integrity/plan-24-04-concurrency-locking.md](../../specs/epics/24-data-integrity/plan-24-04-concurrency-locking.md) — Implementation plan for story 24.04 — Concurrency locking.
- [specs/epics/24-data-integrity/plan-24-05-backup-restore.md](../../specs/epics/24-data-integrity/plan-24-05-backup-restore.md) — Implementation plan for story 24.05 — Backup / restore.
- [specs/epics/24-data-integrity/plan-24-06-disaster-recovery.md](../../specs/epics/24-data-integrity/plan-24-06-disaster-recovery.md) — Implementation plan for story 24.06 — Disaster recovery.
- [specs/epics/24-data-integrity/plan-24-07-integrity-verification.md](../../specs/epics/24-data-integrity/plan-24-07-integrity-verification.md) — Implementation plan for story 24.07 — Integrity verification.
- [specs/epics/24-data-integrity/plan-24-08-identity-stability.md](../../specs/epics/24-data-integrity/plan-24-08-identity-stability.md) — Implementation plan for story 24.08 — Identity stability.
- [specs/epics/24-data-integrity/plan-24-09-forward-back-compat.md](../../specs/epics/24-data-integrity/plan-24-09-forward-back-compat.md) — Implementation plan for story 24.09 — Forward / back compat.

## Diagrams (.drawio) (14)

- [specs/diagrams/api-streaming-stories.drawio](../../specs/diagrams/api-streaming-stories.drawio) — Per-story dependency diagram for Epics 07–08.
- [specs/diagrams/auth-flow.drawio](../../specs/diagrams/auth-flow.drawio) — Auth flows (web cookie + native JWT + refresh).
- [specs/diagrams/client-stories.drawio](../../specs/diagrams/client-stories.drawio) — Per-story dependency diagram for Epics 11–14.
- [specs/diagrams/data-flow.drawio](../../specs/diagrams/data-flow.drawio) — End-to-end data flow from file discovery to search.
- [specs/diagrams/entity-relationship.drawio](../../specs/diagrams/entity-relationship.drawio) — Database ER diagram (videos, transcripts, jobs, users…).
- [specs/diagrams/epic-dependencies.drawio](../../specs/diagrams/epic-dependencies.drawio) — Epic-to-epic dependency graph (24 nodes).
- [specs/diagrams/job-lifecycle.drawio](../../specs/diagrams/job-lifecycle.drawio) — processing_jobs FSM and worker claim loop.
- [specs/diagrams/nonfunctional-stories.drawio](../../specs/diagrams/nonfunctional-stories.drawio) — Per-story dependency diagram for Epics 18–24.
- [specs/diagrams/pipeline-stories.drawio](../../specs/diagrams/pipeline-stories.drawio) — Per-story dependency diagram for Epics 01–06.
- [specs/diagrams/search-architecture.drawio](../../specs/diagrams/search-architecture.drawio) — Search index topology (FTS + vector + RRF).
- [specs/diagrams/security-architecture.drawio](../../specs/diagrams/security-architecture.drawio) — Security boundaries (perimeter + signed URLs).
- [specs/diagrams/streaming-flow.drawio](../../specs/diagrams/streaming-flow.drawio) — Streaming session lifecycle (mint → play → close).
- [specs/diagrams/system-architecture.drawio](../../specs/diagrams/system-architecture.drawio) — System architecture overview (services + comms).
- [specs/diagrams/transcription-pipeline.drawio](../../specs/diagrams/transcription-pipeline.drawio) — Transcription pipeline (probe → extract → STT → segments).

## HTML mockups (49)

- [web/mockups/_shared.css](../../web/mockups/_shared.css) — Shared CSS for all mockups (theme tokens, layout helpers).
- [web/mockups/admin/admin-dashboard.html](../../web/mockups/admin/admin-dashboard.html) — Admin mockup — overview dashboard (story 21.07 visibility)
- [web/mockups/admin/cloud-relay.html](../../web/mockups/admin/cloud-relay.html) — Admin mockup — story 15.02 (cloud relay)
- [web/mockups/admin/duplicates.html](../../web/mockups/admin/duplicates.html) — Admin mockup — story 09.04 (content-hash duplicate review)
- [web/mockups/admin/feature-gate.html](../../web/mockups/admin/feature-gate.html) — Admin mockup — story 16.06 (feature flag gating)
- [web/mockups/admin/job-pipeline.html](../../web/mockups/admin/job-pipeline.html) — Admin mockup — story 21.07 (job pipeline visibility)
- [web/mockups/admin/library-config.html](../../web/mockups/admin/library-config.html) — Admin mockup — story 09.01 (library config)
- [web/mockups/admin/lockout.html](../../web/mockups/admin/lockout.html) — Admin mockup — story 10.11 (brute-force lockout)
- [web/mockups/admin/log-viewer.html](../../web/mockups/admin/log-viewer.html) — Admin mockup — story 21.01 (structured log viewer)
- [web/mockups/admin/login.html](../../web/mockups/admin/login.html) — Admin mockup — stories 10.02 / 10.03 (login)
- [web/mockups/admin/plans.html](../../web/mockups/admin/plans.html) — Admin mockup — story 16.03 (subscription plans)
- [web/mockups/admin/qr-pairing.html](../../web/mockups/admin/qr-pairing.html) — Admin mockup — story 15.05 (QR pairing)
- [web/mockups/admin/register.html](../../web/mockups/admin/register.html) — Admin mockup — story 10.01 (user registration)
- [web/mockups/admin/server-discovery.html](../../web/mockups/admin/server-discovery.html) — Admin mockup — story 15.01 (mDNS server discovery)
- [web/mockups/admin/sessions.html](../../web/mockups/admin/sessions.html) — Admin mockup — story 11.14 (active sessions)
- [web/mockups/admin/speaker-manager.html](../../web/mockups/admin/speaker-manager.html) — Admin mockup — story 09.11 (speaker management)
- [web/mockups/desktop/drag-drop.html](../../web/mockups/desktop/drag-drop.html) — Desktop mockup — story 13.06 (drag-drop import)
- [web/mockups/desktop/main-window.html](../../web/mockups/desktop/main-window.html) — Desktop mockup — stories 13.01–13.03 (Tauri main window)
- [web/mockups/desktop/tray-menu.html](../../web/mockups/desktop/tray-menu.html) — Desktop mockup — story 13.04 (system tray menu)
- [web/mockups/mobile/downloads.html](../../web/mockups/mobile/downloads.html) — Mobile mockup — story 12.06 (offline downloads)
- [web/mockups/mobile/home.html](../../web/mockups/mobile/home.html) — Mobile mockup — stories 12.01 / 12.02 (home)
- [web/mockups/mobile/player.html](../../web/mockups/mobile/player.html) — Mobile mockup — story 12.03 (native player)
- [web/mockups/mobile/push-notification.html](../../web/mockups/mobile/push-notification.html) — Mobile mockup — story 12.04 (push notifications)
- [web/mockups/mobile/search.html](../../web/mockups/mobile/search.html) — Mobile mockup — story 12.01 (search)
- [web/mockups/mobile/video-detail.html](../../web/mockups/mobile/video-detail.html) — Mobile mockup — story 12.01 (video detail)
- [web/mockups/mockup-11-01-library-browser.html](../../web/mockups/mockup-11-01-library-browser.html) — Web UI mockup — story 11.01 (library browser)
- [web/mockups/mockup-11-02-video-detail.html](../../web/mockups/mockup-11-02-video-detail.html) — Web UI mockup — story 11.02 (video detail page)
- [web/mockups/mockup-11-03-video-player.html](../../web/mockups/mockup-11-03-video-player.html) — Web UI mockup — story 11.03 (video player)
- [web/mockups/mockup-11-04-search-interface.html](../../web/mockups/mockup-11-04-search-interface.html) — Web UI mockup — story 11.04 (search interface)
- [web/mockups/mockup-11-05-processing-queue.html](../../web/mockups/mockup-11-05-processing-queue.html) — Web UI mockup — story 11.05 (processing-queue dashboard)
- [web/mockups/mockup-11-06-settings.html](../../web/mockups/mockup-11-06-settings.html) — Web UI mockup — story 11.06 (settings page)
- [web/mockups/mockup-11-07-theme.html](../../web/mockups/mockup-11-07-theme.html) — Web UI mockup — story 11.07 / 11.08 (responsive + dark/light theme)
- [web/mockups/mockup-11-10-offline-pwa.html](../../web/mockups/mockup-11-10-offline-pwa.html) — Web UI mockup — story 11.10 (offline PWA)
- [web/mockups/mockup-11-12-i18n.html](../../web/mockups/mockup-11-12-i18n.html) — Web UI mockup — story 11.12 (i18n + RTL)
- [web/mockups/mockup-17-06-onboarding.html](../../web/mockups/mockup-17-06-onboarding.html) — Web UI mockup — story 17.06 (onboarding)
- [web/mockups/theme-library/badges-tags.html](../../web/mockups/theme-library/badges-tags.html) — Theme library — badges & tags (story 17.02)
- [web/mockups/theme-library/buttons.html](../../web/mockups/theme-library/buttons.html) — Theme library — buttons (story 17.02)
- [web/mockups/theme-library/cards.html](../../web/mockups/theme-library/cards.html) — Theme library — cards (story 17.02)
- [web/mockups/theme-library/colors.html](../../web/mockups/theme-library/colors.html) — Theme library — color tokens (story 17.01)
- [web/mockups/theme-library/inputs.html](../../web/mockups/theme-library/inputs.html) — Theme library — inputs (story 17.02)
- [web/mockups/theme-library/modals.html](../../web/mockups/theme-library/modals.html) — Theme library — modals (story 17.02)
- [web/mockups/theme-library/navigation.html](../../web/mockups/theme-library/navigation.html) — Theme library — navigation (story 17.02)
- [web/mockups/theme-library/player-controls.html](../../web/mockups/theme-library/player-controls.html) — Theme library — player controls (story 17.08)
- [web/mockups/theme-library/tables.html](../../web/mockups/theme-library/tables.html) — Theme library — tables (story 17.02)
- [web/mockups/theme-library/typography.html](../../web/mockups/theme-library/typography.html) — Theme library — typography (story 17.01)
- [web/mockups/tv/detail-tv.html](../../web/mockups/tv/detail-tv.html) — TV mockup — story 14.03 (10-foot detail)
- [web/mockups/tv/home-row.html](../../web/mockups/tv/home-row.html) — TV mockup — stories 14.01 / 14.02 / 14.03 / 14.05 (home rows)
- [web/mockups/tv/player-tv.html](../../web/mockups/tv/player-tv.html) — TV mockup — story 14.03 (10-foot player)
- [web/mockups/tv/search-tv.html](../../web/mockups/tv/search-tv.html) — TV mockup — story 14.04 (voice search)

## Wiki pages (this directory) (6)

- [docs/wiki/README.md](README.md) — Wiki home page — table of contents + navigation.
- [docs/wiki/build-order.md](build-order.md) — Implementation phases (Phase 0–10), critical path, parallelizable tracks.
- [docs/wiki/db/wiki-cross-refs.json](db/wiki-cross-refs.json) — Machine-readable JSON with all cross-references (issues, files, build order, reviews).
- [docs/wiki/file-inventory.md](file-inventory.md) — Complete file tree with one-line descriptions.
- [docs/wiki/linear-map.md](linear-map.md) — HLB-5..HLB-224 → story / plan / mockup / diagram / API.
- [docs/wiki/review-status.md](review-status.md) — Status of REVIEW.md + four PLAN_REVIEW files.

---

## Summary by category

| Category | Count |
|----------|------:|
| Documentation | 2 |
| Infrastructure & build | 3 |
| Code skeleton | 2 |
| API specification | 2 |
| Architecture spec | 1 |
| Reviews | 5 |
| Epic READMEs | 24 |
| User stories | 236 |
| Implementation plans | 237 |
| Diagrams (.drawio) | 14 |
| HTML mockups | 49 |
| Wiki pages (this directory) | 6 |
| **Total** | **581** |
