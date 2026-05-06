# Feature catalog

Every feature in Maktaba, organized by epic. Each entry lists the stories that
implement it, the plans that detail it, the mockups that show it, and the API
endpoints that serve it.

## Epic 01 — Scanner

**Engine:** Pipeline (Python)  ·  **Phase:** 1

**Goal.** Detect every video file under a library's roots, assign it a

📖 [Epic README](../../specs/epics/01-scanner/README.md)

### Features

#### 1.1 — Bootstrap a library and walk its roots

- 📄 Story: [specs/epics/01-scanner/story-01-01-file-discovery.md](../../specs/epics/01-scanner/story-01-01-file-discovery.md)
- 🛠 Plan: [specs/epics/01-scanner/plan-01-01-file-discovery.md](../../specs/epics/01-scanner/plan-01-01-file-discovery.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}` *(+2)*
- 🗄 Tables: `processing_jobs`, `videos`

#### 1.2 — Content-addressable identity (BLAKE3)

- 📄 Story: [specs/epics/01-scanner/story-01-02-content-identity.md](../../specs/epics/01-scanner/story-01-02-content-identity.md)
- 🛠 Plan: [specs/epics/01-scanner/plan-01-02-content-identity.md](../../specs/epics/01-scanner/plan-01-02-content-identity.md)
- 🗄 Tables: `videos`

#### 1.3 — Watch for live filesystem changes

- 📄 Story: [specs/epics/01-scanner/story-01-03-filesystem-watcher.md](../../specs/epics/01-scanner/story-01-03-filesystem-watcher.md)
- 🛠 Plan: [specs/epics/01-scanner/plan-01-03-filesystem-watcher.md](../../specs/epics/01-scanner/plan-01-03-filesystem-watcher.md)
- 🗄 Tables: `videos`

#### 1.4 — Manual control surface

- 📄 Story: [specs/epics/01-scanner/story-01-04-manual-control.md](../../specs/epics/01-scanner/story-01-04-manual-control.md)
- 🛠 Plan: [specs/epics/01-scanner/plan-01-04-manual-control.md](../../specs/epics/01-scanner/plan-01-04-manual-control.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}` *(+1)*
- 🗄 Tables: `processing_jobs`

#### 1.5 — Schema & ownership decisions

- 📄 Story: [specs/epics/01-scanner/story-01-05-schema-decisions.md](../../specs/epics/01-scanner/story-01-05-schema-decisions.md)
- 🛠 Plan: [specs/epics/01-scanner/plan-01-05-schema-decisions.md](../../specs/epics/01-scanner/plan-01-05-schema-decisions.md)
- 🗄 Tables: `videos`

#### 1.6 — Video state machine

- 📄 Story: [specs/epics/01-scanner/story-01-06-video-state-machine.md](../../specs/epics/01-scanner/story-01-06-video-state-machine.md)
- 🛠 Plan: [specs/epics/01-scanner/plan-01-06-video-state-machine.md](../../specs/epics/01-scanner/plan-01-06-video-state-machine.md)
- 🗄 Tables: `processing_jobs`, `videos`, `media_info`, `transcripts`


## Epic 02 — Audio Extraction

**Engine:** Pipeline (Python)  ·  **Phase:** 1

**Goal.** From a probed video, pick the right audio track, extract it as

📖 [Epic README](../../specs/epics/02-audio-extraction/README.md)

### Features

#### 2.1 — Probe the audio tracks

- 📄 Story: [specs/epics/02-audio-extraction/story-02-01-audio-probe.md](../../specs/epics/02-audio-extraction/story-02-01-audio-probe.md)
- 🛠 Plan: [specs/epics/02-audio-extraction/plan-02-01-audio-probe.md](../../specs/epics/02-audio-extraction/plan-02-01-audio-probe.md)
- 🗄 Tables: `media_info`, `audio_tracks`

#### 2.2 — Track selection

- 📄 Story: [specs/epics/02-audio-extraction/story-02-02-track-selection.md](../../specs/epics/02-audio-extraction/story-02-02-track-selection.md)
- 🛠 Plan: [specs/epics/02-audio-extraction/plan-02-02-track-selection.md](../../specs/epics/02-audio-extraction/plan-02-02-track-selection.md)
- 🗄 Tables: `audio_tracks`

#### 2.3 — Stream extraction (no intermediate WAV by default)

- 📄 Story: [specs/epics/02-audio-extraction/story-02-03-stream-extraction.md](../../specs/epics/02-audio-extraction/story-02-03-stream-extraction.md)
- 🛠 Plan: [specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md](../../specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md)
- 🗄 Tables: `videos`

#### 2.4 — Audio resource accounting

- 📄 Story: [specs/epics/02-audio-extraction/story-02-04-resource-accounting.md](../../specs/epics/02-audio-extraction/story-02-04-resource-accounting.md)
- 🛠 Plan: [specs/epics/02-audio-extraction/plan-02-04-resource-accounting.md](../../specs/epics/02-audio-extraction/plan-02-04-resource-accounting.md)


## Epic 03 — Transcription

**Engine:** Pipeline (Python)  ·  **Phase:** 1

**Goal.** Convert extracted audio into a sequence of `transcript_segments`

📖 [Epic README](../../specs/epics/03-transcription/README.md)

### Features

#### 3.1 — STT backend protocol

- 📄 Story: [specs/epics/03-transcription/story-03-01-backend-protocol.md](../../specs/epics/03-transcription/story-03-01-backend-protocol.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-01-backend-protocol.md](../../specs/epics/03-transcription/plan-03-01-backend-protocol.md)
- 🔌 API: `GET /system/health`

#### 3.2 — Whisper MLX backend (default on Apple Silicon)

- 📄 Story: [specs/epics/03-transcription/story-03-02-whisper-mlx-backend.md](../../specs/epics/03-transcription/story-03-02-whisper-mlx-backend.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-02-whisper-mlx-backend.md](../../specs/epics/03-transcription/plan-03-02-whisper-mlx-backend.md)

#### 3.3 — Faster-Whisper (CUDA / CPU) backend

- 📄 Story: [specs/epics/03-transcription/story-03-03-faster-whisper-backend.md](../../specs/epics/03-transcription/story-03-03-faster-whisper-backend.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-03-faster-whisper-backend.md](../../specs/epics/03-transcription/plan-03-03-faster-whisper-backend.md)

#### 3.4 — OpenAI API backend

- 📄 Story: [specs/epics/03-transcription/story-03-04-openai-api-backend.md](../../specs/epics/03-transcription/story-03-04-openai-api-backend.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-04-openai-api-backend.md](../../specs/epics/03-transcription/plan-03-04-openai-api-backend.md)

#### 3.5 — Backend registry, transcript history & per-library selection

- 📄 Story: [specs/epics/03-transcription/story-03-05-backend-registry.md](../../specs/epics/03-transcription/story-03-05-backend-registry.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-05-backend-registry.md](../../specs/epics/03-transcription/plan-03-05-backend-registry.md)
- 🗄 Tables: `transcripts`

#### 3.6 — Real-time per-segment durable commit

- 📄 Story: [specs/epics/03-transcription/story-03-06-segment-commit.md](../../specs/epics/03-transcription/story-03-06-segment-commit.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-06-segment-commit.md](../../specs/epics/03-transcription/plan-03-06-segment-commit.md)
- 🗄 Tables: `processing_jobs`, `transcript_segments`, `transcript_words`

#### 3.7 — Pause and resume to the exact second

- 📄 Story: [specs/epics/03-transcription/story-03-07-pause-resume.md](../../specs/epics/03-transcription/story-03-07-pause-resume.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-07-pause-resume.md](../../specs/epics/03-transcription/plan-03-07-pause-resume.md)
- 🔌 API: `GET /jobs`, `GET /jobs/{id}`, `POST /jobs/{id}/pause`, `POST /jobs/{id}/resume`
- 🗄 Tables: `transcripts`

#### 3.8 — Crash recovery & graceful shutdown

- 📄 Story: [specs/epics/03-transcription/story-03-08-crash-recovery.md](../../specs/epics/03-transcription/story-03-08-crash-recovery.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-08-crash-recovery.md](../../specs/epics/03-transcription/plan-03-08-crash-recovery.md)

#### 3.9 — Diarization (opt-in, off by default)

- 📄 Story: [specs/epics/03-transcription/story-03-09-diarization.md](../../specs/epics/03-transcription/story-03-09-diarization.md)
- 🛠 Plan: [specs/epics/03-transcription/plan-03-09-diarization.md](../../specs/epics/03-transcription/plan-03-09-diarization.md)
- 🗄 Tables: `transcript_segments`, `speakers`


## Epic 04 — Subtitles

**Engine:** Pipeline (Python)  ·  **Phase:** 1

**Goal.** Convert finalized transcripts into well-formed `.srt` and `.vtt`

📖 [Epic README](../../specs/epics/04-subtitles/README.md)

### Features

#### 4.1 — Generate SRT and VTT from `transcript_segments`

- 📄 Story: [specs/epics/04-subtitles/story-04-01-generate-from-segments.md](../../specs/epics/04-subtitles/story-04-01-generate-from-segments.md)
- 🛠 Plan: [specs/epics/04-subtitles/plan-04-01-generate-from-segments.md](../../specs/epics/04-subtitles/plan-04-01-generate-from-segments.md)
- 🗄 Tables: `transcript_segments`, `subtitle_files`

#### 4.2 — SRT/VTT formatting & line wrapping

- 📄 Story: [specs/epics/04-subtitles/story-04-02-formatting-wrapping.md](../../specs/epics/04-subtitles/story-04-02-formatting-wrapping.md)
- 🛠 Plan: [specs/epics/04-subtitles/plan-04-02-formatting-wrapping.md](../../specs/epics/04-subtitles/plan-04-02-formatting-wrapping.md)

#### 4.3 — External subtitle auto-discovery

- 📄 Story: [specs/epics/04-subtitles/story-04-03-external-discovery.md](../../specs/epics/04-subtitles/story-04-03-external-discovery.md)
- 🛠 Plan: [specs/epics/04-subtitles/plan-04-03-external-discovery.md](../../specs/epics/04-subtitles/plan-04-03-external-discovery.md)
- 🗄 Tables: `subtitle_files`

#### 4.4 — Embedded subtitle extraction (with `is_embedded` schema)

- 📄 Story: [specs/epics/04-subtitles/story-04-04-embedded-extraction.md](../../specs/epics/04-subtitles/story-04-04-embedded-extraction.md)
- 🛠 Plan: [specs/epics/04-subtitles/plan-04-04-embedded-extraction.md](../../specs/epics/04-subtitles/plan-04-04-embedded-extraction.md)
- 🗄 Tables: `subtitle_files`

#### 4.5 — Live VTT serving (read-side, contract only)

- 📄 Story: [specs/epics/04-subtitles/story-04-05-live-vtt-contract.md](../../specs/epics/04-subtitles/story-04-05-live-vtt-contract.md)
- 🛠 Plan: [specs/epics/04-subtitles/plan-04-05-live-vtt-contract.md](../../specs/epics/04-subtitles/plan-04-05-live-vtt-contract.md)
- 🗄 Tables: `transcript_segments`


## Epic 05 — Search & Indexing

**Engine:** Pipeline (Python) + API (Go)  ·  **Phase:** 1

**Goal.** Make every transcribed second searchable in two complementary

📖 [Epic README](../../specs/epics/05-search-indexing/README.md)

### Features

#### 5.1 — Search-unit chunking & schema

- 📄 Story: [specs/epics/05-search-indexing/story-05-01-unit-chunking.md](../../specs/epics/05-search-indexing/story-05-01-unit-chunking.md)
- 🛠 Plan: [specs/epics/05-search-indexing/plan-05-01-unit-chunking.md](../../specs/epics/05-search-indexing/plan-05-01-unit-chunking.md)
- 🗄 Tables: `transcript_units`

#### 5.2 — FTS5 / `tsvector` exact-phrase index (unit-backed)

- 📄 Story: [specs/epics/05-search-indexing/story-05-02-fts-tsvector.md](../../specs/epics/05-search-indexing/story-05-02-fts-tsvector.md)
- 🛠 Plan: [specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md](../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md)
- 🗄 Tables: `transcript_segments`, `transcript_units`

#### 5.3 — ChromaDB vector index

- 📄 Story: [specs/epics/05-search-indexing/story-05-03-chroma-vector.md](../../specs/epics/05-search-indexing/story-05-03-chroma-vector.md)
- 🛠 Plan: [specs/epics/05-search-indexing/plan-05-03-chroma-vector.md](../../specs/epics/05-search-indexing/plan-05-03-chroma-vector.md)
- 🗄 Tables: `transcripts`

#### 5.4 — Hybrid retrieval with Reciprocal Rank Fusion

- 📄 Story: [specs/epics/05-search-indexing/story-05-04-hybrid-rrf.md](../../specs/epics/05-search-indexing/story-05-04-hybrid-rrf.md)
- 🛠 Plan: [specs/epics/05-search-indexing/plan-05-04-hybrid-rrf.md](../../specs/epics/05-search-indexing/plan-05-04-hybrid-rrf.md)
- 🗄 Tables: `transcript_units`

#### 5.5 — Incremental indexing

- 📄 Story: [specs/epics/05-search-indexing/story-05-05-incremental-indexing.md](../../specs/epics/05-search-indexing/story-05-05-incremental-indexing.md)
- 🛠 Plan: [specs/epics/05-search-indexing/plan-05-05-incremental-indexing.md](../../specs/epics/05-search-indexing/plan-05-05-incremental-indexing.md)

#### 5.6 — Search query suggestions

- 📄 Story: [specs/epics/05-search-indexing/story-05-06-query-suggestions.md](../../specs/epics/05-search-indexing/story-05-06-query-suggestions.md)
- 🛠 Plan: [specs/epics/05-search-indexing/plan-05-06-query-suggestions.md](../../specs/epics/05-search-indexing/plan-05-06-query-suggestions.md)
- 🔌 API: `GET /search`, `POST /search`, `GET /search/suggest`
- 🗄 Tables: `transcript_units`

#### 5.7 — Chapter inference from transcripts

- 📄 Story: [specs/epics/05-search-indexing/story-05-07-chapter-inference.md](../../specs/epics/05-search-indexing/story-05-07-chapter-inference.md)
- 🛠 Plan: [specs/epics/05-search-indexing/plan-05-07-chapter-inference.md](../../specs/epics/05-search-indexing/plan-05-07-chapter-inference.md)
- 🗄 Tables: `chapters`


## Epic 06 — Job Queue

**Engine:** Pipeline + API (cross-cutting)  ·  **Phase:** 1

**Goal.** Implement the durable, atomic, pause-aware job queue that every

📖 [Epic README](../../specs/epics/06-job-queue/README.md)

### Features

#### 6.1 — Schema, migration, indexes

- 📄 Story: [specs/epics/06-job-queue/story-06-01-schema-indexes.md](../../specs/epics/06-job-queue/story-06-01-schema-indexes.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-01-schema-indexes.md](../../specs/epics/06-job-queue/plan-06-01-schema-indexes.md)
- 🗄 Tables: `processing_jobs`

#### 6.2 — Claim loop

- 📄 Story: [specs/epics/06-job-queue/story-06-02-claim-loop.md](../../specs/epics/06-job-queue/story-06-02-claim-loop.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-02-claim-loop.md](../../specs/epics/06-job-queue/plan-06-02-claim-loop.md)

#### 6.3 — Heartbeat & progress

- 📄 Story: [specs/epics/06-job-queue/story-06-03-heartbeat-progress.md](../../specs/epics/06-job-queue/story-06-03-heartbeat-progress.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md](../../specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md)

#### 6.4 — Pause, resume, cancel via request flags

- 📄 Story: [specs/epics/06-job-queue/story-06-04-pause-resume-cancel.md](../../specs/epics/06-job-queue/story-06-04-pause-resume-cancel.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-04-pause-resume-cancel.md](../../specs/epics/06-job-queue/plan-06-04-pause-resume-cancel.md)
- 🔌 API: `GET /jobs`, `GET /jobs/{id}`, `POST /jobs/{id}/pause`, `POST /jobs/{id}/resume`, `POST /jobs/{id}/cancel`

#### 6.5 — Backoff and retry

- 📄 Story: [specs/epics/06-job-queue/story-06-05-backoff-retry.md](../../specs/epics/06-job-queue/story-06-05-backoff-retry.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-05-backoff-retry.md](../../specs/epics/06-job-queue/plan-06-05-backoff-retry.md)
- 🔌 API: `GET /jobs`, `GET /jobs/{id}`, `POST /jobs/{id}/retry`

#### 6.6 — Reaper for crashed claims

- 📄 Story: [specs/epics/06-job-queue/story-06-06-reaper.md](../../specs/epics/06-job-queue/story-06-06-reaper.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-06-reaper.md](../../specs/epics/06-job-queue/plan-06-06-reaper.md)

#### 6.7 — Concurrency model & per-host caps

- 📄 Story: [specs/epics/06-job-queue/story-06-07-concurrency-caps.md](../../specs/epics/06-job-queue/story-06-07-concurrency-caps.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-07-concurrency-caps.md](../../specs/epics/06-job-queue/plan-06-07-concurrency-caps.md)

#### 6.8 — Graceful shutdown semantics

- 📄 Story: [specs/epics/06-job-queue/story-06-08-graceful-shutdown.md](../../specs/epics/06-job-queue/story-06-08-graceful-shutdown.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-08-graceful-shutdown.md](../../specs/epics/06-job-queue/plan-06-08-graceful-shutdown.md)

#### 6.9 — Observability hooks

- 📄 Story: [specs/epics/06-job-queue/story-06-09-observability.md](../../specs/epics/06-job-queue/story-06-09-observability.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-09-observability.md](../../specs/epics/06-job-queue/plan-06-09-observability.md)
- 🔌 API: `GET /queue/stats`

#### 6.10 — Single source of truth for resume

- 📄 Story: [specs/epics/06-job-queue/story-06-10-resume-invariant.md](../../specs/epics/06-job-queue/story-06-10-resume-invariant.md)
- 🛠 Plan: [specs/epics/06-job-queue/plan-06-10-resume-invariant.md](../../specs/epics/06-job-queue/plan-06-10-resume-invariant.md)
- 🗄 Tables: `processing_jobs`


## Epic 07 — API Server

**Engine:** API (Go)  ·  **Phase:** 2

**Goal.** The Go API Service is every request that isn't a media byte: library CRUD,

📖 [Epic README](../../specs/epics/07-api-server/README.md)

### Features

#### 7.1 — HTTP server skeleton

- 📄 Story: [specs/epics/07-api-server/story-07-01-http-server-skeleton.md](../../specs/epics/07-api-server/story-07-01-http-server-skeleton.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-01-http-server-skeleton.md](../../specs/epics/07-api-server/plan-07-01-http-server-skeleton.md)

#### 7.2 — Cursor pagination primitive

- 📄 Story: [specs/epics/07-api-server/story-07-02-cursor-pagination.md](../../specs/epics/07-api-server/story-07-02-cursor-pagination.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-02-cursor-pagination.md](../../specs/epics/07-api-server/plan-07-02-cursor-pagination.md)

#### 7.3 — Library CRUD

- 📄 Story: [specs/epics/07-api-server/story-07-03-library-crud.md](../../specs/epics/07-api-server/story-07-03-library-crud.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-03-library-crud.md](../../specs/epics/07-api-server/plan-07-03-library-crud.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}` *(+3)*
- 🗄 Tables: `processing_jobs`, `videos`, `libraries`

#### 7.4 — Video list, detail, patch, delete

- 📄 Story: [specs/epics/07-api-server/story-07-04-video-crud.md](../../specs/epics/07-api-server/story-07-04-video-crud.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-04-video-crud.md](../../specs/epics/07-api-server/plan-07-04-video-crud.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`
- 🗄 Tables: `media_info`, `audio_tracks`, `chapters`, `tags`, `playback_state`

#### 7.5 — Video processing control

- 📄 Story: [specs/epics/07-api-server/story-07-05-video-processing-control.md](../../specs/epics/07-api-server/story-07-05-video-processing-control.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-05-video-processing-control.md](../../specs/epics/07-api-server/plan-07-05-video-processing-control.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`, `POST /videos/{id}/process` *(+1)*
- 🗄 Tables: `transcripts`

#### 7.6 — Transcript window endpoint

- 📄 Story: [specs/epics/07-api-server/story-07-06-transcript-window.md](../../specs/epics/07-api-server/story-07-06-transcript-window.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-06-transcript-window.md](../../specs/epics/07-api-server/plan-07-06-transcript-window.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`, `GET /videos/{id}/segments`

#### 7.7 — Subtitles & chapters read endpoints

- 📄 Story: [specs/epics/07-api-server/story-07-07-subtitles-chapters-read.md](../../specs/epics/07-api-server/story-07-07-subtitles-chapters-read.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md](../../specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`, `GET /videos/{id}/subtitles` *(+1)*

#### 7.8 — Search API (FTS, semantic, hybrid)

- 📄 Story: [specs/epics/07-api-server/story-07-08-search-api.md](../../specs/epics/07-api-server/story-07-08-search-api.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-08-search-api.md](../../specs/epics/07-api-server/plan-07-08-search-api.md)
- 🔌 API: `GET /search`, `POST /search`, `GET /search/suggest`
- 🗄 Tables: `transcript_units`

#### 7.9 — Saved searches

- 📄 Story: [specs/epics/07-api-server/story-07-09-saved-searches.md](../../specs/epics/07-api-server/story-07-09-saved-searches.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-09-saved-searches.md](../../specs/epics/07-api-server/plan-07-09-saved-searches.md)
- 🔌 API: `GET /search`, `POST /search`, `POST /search/save`, `GET /search/saved`
- 🗄 Tables: `saved_searches`

#### 7.10 — Streaming session lifecycle

- 📄 Story: [specs/epics/07-api-server/story-07-10-streaming-session-lifecycle.md](../../specs/epics/07-api-server/story-07-10-streaming-session-lifecycle.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-10-streaming-session-lifecycle.md](../../specs/epics/07-api-server/plan-07-10-streaming-session-lifecycle.md)
- 🔌 API: `POST /sessions`, `GET /sessions/{id}`, `DELETE /sessions/{id}`

#### 7.11 — Watch progress sync

- 📄 Story: [specs/epics/07-api-server/story-07-11-watch-progress-sync.md](../../specs/epics/07-api-server/story-07-11-watch-progress-sync.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-11-watch-progress-sync.md](../../specs/epics/07-api-server/plan-07-11-watch-progress-sync.md)
- 🔌 API: `POST /sessions`, `GET /sessions/{id}`, `DELETE /sessions/{id}`, `POST /sessions/{id}/progress`, `GET /ws`
- 🗄 Tables: `playback_state`

#### 7.12 — Job control endpoints

- 📄 Story: [specs/epics/07-api-server/story-07-12-job-control.md](../../specs/epics/07-api-server/story-07-12-job-control.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-12-job-control.md](../../specs/epics/07-api-server/plan-07-12-job-control.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`, `POST /videos/{id}/pause` *(+7)*

#### 7.13 — Queue stats endpoint

- 📄 Story: [specs/epics/07-api-server/story-07-13-queue-stats.md](../../specs/epics/07-api-server/story-07-13-queue-stats.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-13-queue-stats.md](../../specs/epics/07-api-server/plan-07-13-queue-stats.md)
- 🔌 API: `GET /queue/stats`
- 🗄 Tables: `processing_jobs`

#### 7.14 — Collections, tags, speakers

- 📄 Story: [specs/epics/07-api-server/story-07-14-collections-tags-speakers.md](../../specs/epics/07-api-server/story-07-14-collections-tags-speakers.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md](../../specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`, `PATCH /videos/{id}/tags` *(+4)*
- 🗄 Tables: `collection_items`

#### 7.15 — Settings & system endpoints

- 📄 Story: [specs/epics/07-api-server/story-07-15-settings-system.md](../../specs/epics/07-api-server/story-07-15-settings-system.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-15-settings-system.md](../../specs/epics/07-api-server/plan-07-15-settings-system.md)
- 🔌 API: `GET /settings`, `PUT /settings`, `GET /settings/stt-backends`, `POST /settings/stt-test`

#### 7.16 — WebSocket fan-out

- 📄 Story: [specs/epics/07-api-server/story-07-16-websocket-fanout.md](../../specs/epics/07-api-server/story-07-16-websocket-fanout.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-16-websocket-fanout.md](../../specs/epics/07-api-server/plan-07-16-websocket-fanout.md)
- 🔌 API: `GET /jobs`, `GET /ws`
- 🗄 Tables: `events`

#### 7.17 — GraphQL schema + resolvers

- 📄 Story: [specs/epics/07-api-server/story-07-17-graphql-schema.md](../../specs/epics/07-api-server/story-07-17-graphql-schema.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-17-graphql-schema.md](../../specs/epics/07-api-server/plan-07-17-graphql-schema.md)
- 🗄 Tables: `media_info`, `audio_tracks`, `devices`

#### 7.18 — gRPC clients to Pipeline and Streaming

- 📄 Story: [specs/epics/07-api-server/story-07-18-grpc-clients.md](../../specs/epics/07-api-server/story-07-18-grpc-clients.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-18-grpc-clients.md](../../specs/epics/07-api-server/plan-07-18-grpc-clients.md)

#### 7.19 — Validation, body limits, rate limiting

- 📄 Story: [specs/epics/07-api-server/story-07-19-validation-rate-limiting.md](../../specs/epics/07-api-server/story-07-19-validation-rate-limiting.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-19-validation-rate-limiting.md](../../specs/epics/07-api-server/plan-07-19-validation-rate-limiting.md)
- 🔌 API: `POST /auth/login`, `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}` *(+2)*

#### 7.20 — Health, version, metrics

- 📄 Story: [specs/epics/07-api-server/story-07-20-health-version-metrics.md](../../specs/epics/07-api-server/story-07-20-health-version-metrics.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-20-health-version-metrics.md](../../specs/epics/07-api-server/plan-07-20-health-version-metrics.md)
- 🔌 API: `GET /system/health`, `GET /system/version`

#### 7.21 — Recommendations endpoint

- 📄 Story: [specs/epics/07-api-server/story-07-21-recommendations.md](../../specs/epics/07-api-server/story-07-21-recommendations.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-21-recommendations.md](../../specs/epics/07-api-server/plan-07-21-recommendations.md)
- 🔌 API: `GET /recommendations`
- 🗄 Tables: `playback_state`

#### 7.22 — Device registration for push

- 📄 Story: [specs/epics/07-api-server/story-07-22-devices-register.md](../../specs/epics/07-api-server/story-07-22-devices-register.md)
- 🛠 Plan: [specs/epics/07-api-server/plan-07-22-devices-register.md](../../specs/epics/07-api-server/plan-07-22-devices-register.md)
- 🔌 API: `POST /devices/register`, `GET /devices`, `DELETE /devices/{id}`
- 🗄 Tables: `devices`


## Epic 08 — Streaming

**Engine:** Streaming (Go)  ·  **Phase:** 2

**Goal.** The Go Streaming Service is every media byte: HLS and DASH manifests,

📖 [Epic README](../../specs/epics/08-streaming/README.md)

### Features

#### 8.1 — Server skeleton, signed URL middleware

- 📄 Story: [specs/epics/08-streaming/story-08-01-server-skeleton.md](../../specs/epics/08-streaming/story-08-01-server-skeleton.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-01-server-skeleton.md](../../specs/epics/08-streaming/plan-08-01-server-skeleton.md)
- 🔌 API: `POST /sessions`

#### 8.2 — Capability matrix & client profile registry

- 📄 Story: [specs/epics/08-streaming/story-08-02-capability-matrix.md](../../specs/epics/08-streaming/story-08-02-capability-matrix.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-02-capability-matrix.md](../../specs/epics/08-streaming/plan-08-02-capability-matrix.md)

#### 8.3 — Direct play (range-served `206 Partial Content`)

- 📄 Story: [specs/epics/08-streaming/story-08-03-direct-play.md](../../specs/epics/08-streaming/story-08-03-direct-play.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-03-direct-play.md](../../specs/epics/08-streaming/plan-08-03-direct-play.md)
- 🔌 API: `POST /sessions`

#### 8.4 — Direct stream (remux only)

- 📄 Story: [specs/epics/08-streaming/story-08-04-direct-stream-remux.md](../../specs/epics/08-streaming/story-08-04-direct-stream-remux.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-04-direct-stream-remux.md](../../specs/epics/08-streaming/plan-08-04-direct-stream-remux.md)

#### 8.5 — HLS adaptive transcode pipeline

- 📄 Story: [specs/epics/08-streaming/story-08-05-hls-transcode.md](../../specs/epics/08-streaming/story-08-05-hls-transcode.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-05-hls-transcode.md](../../specs/epics/08-streaming/plan-08-05-hls-transcode.md)

#### 8.6 — DASH manifest (opt-in per session)

- 📄 Story: [specs/epics/08-streaming/story-08-06-dash-manifest.md](../../specs/epics/08-streaming/story-08-06-dash-manifest.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-06-dash-manifest.md](../../specs/epics/08-streaming/plan-08-06-dash-manifest.md)

#### 8.7 — Hardware acceleration auto-detect

- 📄 Story: [specs/epics/08-streaming/story-08-07-hwaccel-detect.md](../../specs/epics/08-streaming/story-08-07-hwaccel-detect.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-07-hwaccel-detect.md](../../specs/epics/08-streaming/plan-08-07-hwaccel-detect.md)

#### 8.8 — gRPC server (Open/Close/EvictHashCache/GetCapabilities)

- 📄 Story: [specs/epics/08-streaming/story-08-08-grpc-server.md](../../specs/epics/08-streaming/story-08-08-grpc-server.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-08-grpc-server.md](../../specs/epics/08-streaming/plan-08-08-grpc-server.md)

#### 8.9 — Session store, sticky transcoder, reaper

- 📄 Story: [specs/epics/08-streaming/story-08-09-session-store.md](../../specs/epics/08-streaming/story-08-09-session-store.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-09-session-store.md](../../specs/epics/08-streaming/plan-08-09-session-store.md)
- 🔌 API: `POST /sessions`

#### 8.10 — Concurrency caps and backpressure

- 📄 Story: [specs/epics/08-streaming/story-08-10-concurrency-caps.md](../../specs/epics/08-streaming/story-08-10-concurrency-caps.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-10-concurrency-caps.md](../../specs/epics/08-streaming/plan-08-10-concurrency-caps.md)

#### 8.11 — Live subtitle rendering

- 📄 Story: [specs/epics/08-streaming/story-08-11-live-subtitle.md](../../specs/epics/08-streaming/story-08-11-live-subtitle.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-11-live-subtitle.md](../../specs/epics/08-streaming/plan-08-11-live-subtitle.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`, `GET /videos/{id}/subtitles`
- 🗄 Tables: `transcript_segments`

#### 8.12 — Chapter delivery

- 📄 Story: [specs/epics/08-streaming/story-08-12-chapter-delivery.md](../../specs/epics/08-streaming/story-08-12-chapter-delivery.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-12-chapter-delivery.md](../../specs/epics/08-streaming/plan-08-12-chapter-delivery.md)

#### 8.13 — Posters, sprite sheets, chapter thumbs

- 📄 Story: [specs/epics/08-streaming/story-08-13-posters-sprites.md](../../specs/epics/08-streaming/story-08-13-posters-sprites.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-13-posters-sprites.md](../../specs/epics/08-streaming/plan-08-13-posters-sprites.md)

#### 8.14 — Cache layout and LRU GC

- 📄 Story: [specs/epics/08-streaming/story-08-14-cache-gc.md](../../specs/epics/08-streaming/story-08-14-cache-gc.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-14-cache-gc.md](../../specs/epics/08-streaming/plan-08-14-cache-gc.md)

#### 8.15 — Probe cache

- 📄 Story: [specs/epics/08-streaming/story-08-15-probe-cache.md](../../specs/epics/08-streaming/story-08-15-probe-cache.md)
- 🛠 Plan: [specs/epics/08-streaming/plan-08-15-probe-cache.md](../../specs/epics/08-streaming/plan-08-15-probe-cache.md)
- 🗄 Tables: `media_info`


## Epic 09 — Library Management

**Engine:** API (Go)  ·  **Phase:** 2

**Goal.** A library is a named collection of root paths sharing a configuration

📖 [Epic README](../../specs/epics/09-library-management/README.md)

### Features

#### 9.1 — Library config schema and validation

- 📄 Story: [specs/epics/09-library-management/story-09-01-library-config-schema.md](../../specs/epics/09-library-management/story-09-01-library-config-schema.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-01-library-config-schema.md](../../specs/epics/09-library-management/plan-09-01-library-config-schema.md)

#### 9.2 — Filesystem watcher

- 📄 Story: [specs/epics/09-library-management/story-09-02-filesystem-watcher.md](../../specs/epics/09-library-management/story-09-02-filesystem-watcher.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-02-filesystem-watcher.md](../../specs/epics/09-library-management/plan-09-02-filesystem-watcher.md)

#### 9.3 — Periodic full sweep

- 📄 Story: [specs/epics/09-library-management/story-09-03-periodic-sweep.md](../../specs/epics/09-library-management/story-09-03-periodic-sweep.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-03-periodic-sweep.md](../../specs/epics/09-library-management/plan-09-03-periodic-sweep.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}` *(+1)*
- 🗄 Tables: `videos`

#### 9.4 — Content-hash dedup

- 📄 Story: [specs/epics/09-library-management/story-09-04-content-hash-dedup.md](../../specs/epics/09-library-management/story-09-04-content-hash-dedup.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-04-content-hash-dedup.md](../../specs/epics/09-library-management/plan-09-04-content-hash-dedup.md)
- 🗄 Tables: `videos`

#### 9.5 — Ignore rules and extension filtering

- 📄 Story: [specs/epics/09-library-management/story-09-05-ignore-rules.md](../../specs/epics/09-library-management/story-09-05-ignore-rules.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-05-ignore-rules.md](../../specs/epics/09-library-management/plan-09-05-ignore-rules.md)

#### 9.6 — Manual scan trigger and scan progress

- 📄 Story: [specs/epics/09-library-management/story-09-06-manual-scan.md](../../specs/epics/09-library-management/story-09-06-manual-scan.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-06-manual-scan.md](../../specs/epics/09-library-management/plan-09-06-manual-scan.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}` *(+1)*
- 🗄 Tables: `videos`

#### 9.7 — Library stats query

- 📄 Story: [specs/epics/09-library-management/story-09-07-library-stats.md](../../specs/epics/09-library-management/story-09-07-library-stats.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-07-library-stats.md](../../specs/epics/09-library-management/plan-09-07-library-stats.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}` *(+1)*
- 🗄 Tables: `processing_jobs`, `videos`

#### 9.8 — Auto-categorization: language tag

- 📄 Story: [specs/epics/09-library-management/story-09-08-language-tag.md](../../specs/epics/09-library-management/story-09-08-language-tag.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-08-language-tag.md](../../specs/epics/09-library-management/plan-09-08-language-tag.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`

#### 9.9 — Auto-categorization: topic tag

- 📄 Story: [specs/epics/09-library-management/story-09-09-topic-tag.md](../../specs/epics/09-library-management/story-09-09-topic-tag.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-09-topic-tag.md](../../specs/epics/09-library-management/plan-09-09-topic-tag.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}`

#### 9.10 — Auto-categorization: content type classifier

- 📄 Story: [specs/epics/09-library-management/story-09-10-content-type-classifier.md](../../specs/epics/09-library-management/story-09-10-content-type-classifier.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-10-content-type-classifier.md](../../specs/epics/09-library-management/plan-09-10-content-type-classifier.md)
- 🗄 Tables: `media_features`

#### 9.11 — Speakers, voiceprints, naming, merge

- 📄 Story: [specs/epics/09-library-management/story-09-11-speakers.md](../../specs/epics/09-library-management/story-09-11-speakers.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-11-speakers.md](../../specs/epics/09-library-management/plan-09-11-speakers.md)
- 🔌 API: `GET /speakers`, `POST /speakers/merge`
- 🗄 Tables: `speakers`, `segment_speakers`

#### 9.12 — Tag CRUD and normalization

- 📄 Story: [specs/epics/09-library-management/story-09-12-tag-crud.md](../../specs/epics/09-library-management/story-09-12-tag-crud.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-12-tag-crud.md](../../specs/epics/09-library-management/plan-09-12-tag-crud.md)
- 🗄 Tables: `tags`, `video_tags`

#### 9.13 — Collections (manual ordered)

- 📄 Story: [specs/epics/09-library-management/story-09-13-collections-manual.md](../../specs/epics/09-library-management/story-09-13-collections-manual.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-13-collections-manual.md](../../specs/epics/09-library-management/plan-09-13-collections-manual.md)
- 🗄 Tables: `collection_items`

#### 9.14 — Smart collections

- 📄 Story: [specs/epics/09-library-management/story-09-14-smart-collections.md](../../specs/epics/09-library-management/story-09-14-smart-collections.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-14-smart-collections.md](../../specs/epics/09-library-management/plan-09-14-smart-collections.md)
- 🔌 API: `GET /collections`, `POST /collections`, `GET /collections/{id}`, `PATCH /collections/{id}`, `DELETE /collections/{id}`
- 🗄 Tables: `collection_items`

#### 9.15 — Library deletion

- 📄 Story: [specs/epics/09-library-management/story-09-15-library-deletion.md](../../specs/epics/09-library-management/story-09-15-library-deletion.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-15-library-deletion.md](../../specs/epics/09-library-management/plan-09-15-library-deletion.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}`
- 🗄 Tables: `videos`, `media_info`, `transcripts`, `audio_tracks`, `transcript_segments`, `subtitle_files` *(+5)*

#### 9.16 — Multi-root and overlap detection

- 📄 Story: [specs/epics/09-library-management/story-09-16-multi-root-overlap.md](../../specs/epics/09-library-management/story-09-16-multi-root-overlap.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-16-multi-root-overlap.md](../../specs/epics/09-library-management/plan-09-16-multi-root-overlap.md)

#### 9.17 — Library audit log

- 📄 Story: [specs/epics/09-library-management/story-09-17-library-audit.md](../../specs/epics/09-library-management/story-09-17-library-audit.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-17-library-audit.md](../../specs/epics/09-library-management/plan-09-17-library-audit.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}`
- 🗄 Tables: `audit_log`

#### 9.18 — Chapter inference from transcript topic shifts

- 📄 Story: [specs/epics/09-library-management/story-09-18-chapter-inference.md](../../specs/epics/09-library-management/story-09-18-chapter-inference.md)
- 🛠 Plan: [specs/epics/09-library-management/plan-09-18-chapter-inference.md](../../specs/epics/09-library-management/plan-09-18-chapter-inference.md)
- 🔌 API: `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}`, `DELETE /videos/{id}`, `GET /videos/{id}/chapters`
- 🗄 Tables: `processing_jobs`, `chapters`


## Epic 10 — Auth & Security

**Engine:** API (Go)  ·  **Phase:** 2

**Goal.** Identity, sessions, signed URLs, secret handling, transport hardening.

📖 [Epic README](../../specs/epics/10-auth-security/README.md)

### Features

#### 10.1 — User store + argon2id passwords

- 📄 Story: [specs/epics/10-auth-security/story-10-01-user-store.md](../../specs/epics/10-auth-security/story-10-01-user-store.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-01-user-store.md](../../specs/epics/10-auth-security/plan-10-01-user-store.md)
- 🔌 API: `POST /sessions`
- 🗄 Tables: `playback_state`, `saved_searches`, `users`

#### 10.2 — Web login (cookie + CSRF)

- 📄 Story: [specs/epics/10-auth-security/story-10-02-web-login.md](../../specs/epics/10-auth-security/story-10-02-web-login.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-02-web-login.md](../../specs/epics/10-auth-security/plan-10-02-web-login.md)
- 🔌 API: `POST /auth/login`

#### 10.3 — Native login (JWT access + refresh)

- 📄 Story: [specs/epics/10-auth-security/story-10-03-native-login.md](../../specs/epics/10-auth-security/story-10-03-native-login.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-03-native-login.md](../../specs/epics/10-auth-security/plan-10-03-native-login.md)
- 🔌 API: `POST /auth/login`

#### 10.4 — Token refresh + rotation

- 📄 Story: [specs/epics/10-auth-security/story-10-04-token-refresh.md](../../specs/epics/10-auth-security/story-10-04-token-refresh.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-04-token-refresh.md](../../specs/epics/10-auth-security/plan-10-04-token-refresh.md)
- 🔌 API: `POST /auth/refresh`
- 🗄 Tables: `audit_log`

#### 10.5 — Logout + session revocation

- 📄 Story: [specs/epics/10-auth-security/story-10-05-logout-revocation.md](../../specs/epics/10-auth-security/story-10-05-logout-revocation.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-05-logout-revocation.md](../../specs/epics/10-auth-security/plan-10-05-logout-revocation.md)
- 🔌 API: `POST /auth/logout`, `POST /sessions`

#### 10.6 — RS256 key generation, rotation, JWKS

- 📄 Story: [specs/epics/10-auth-security/story-10-06-rs256-keys-jwks.md](../../specs/epics/10-auth-security/story-10-06-rs256-keys-jwks.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md](../../specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md)

#### 10.7 — Streaming-side offline JWT verification

- 📄 Story: [specs/epics/10-auth-security/story-10-07-streaming-jwt-verify.md](../../specs/epics/10-auth-security/story-10-07-streaming-jwt-verify.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-07-streaming-jwt-verify.md](../../specs/epics/10-auth-security/plan-10-07-streaming-jwt-verify.md)

#### 10.8 — Signed-URL minter

- 📄 Story: [specs/epics/10-auth-security/story-10-08-signed-url-minter.md](../../specs/epics/10-auth-security/story-10-08-signed-url-minter.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-08-signed-url-minter.md](../../specs/epics/10-auth-security/plan-10-08-signed-url-minter.md)

#### 10.9 — Single-user mode (admin token bypass)

- 📄 Story: [specs/epics/10-auth-security/story-10-09-single-user-mode.md](../../specs/epics/10-auth-security/story-10-09-single-user-mode.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-09-single-user-mode.md](../../specs/epics/10-auth-security/plan-10-09-single-user-mode.md)
- 🗄 Tables: `users`

#### 10.10 — CSRF protection (web only)

- 📄 Story: [specs/epics/10-auth-security/story-10-10-csrf-protection.md](../../specs/epics/10-auth-security/story-10-10-csrf-protection.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-10-csrf-protection.md](../../specs/epics/10-auth-security/plan-10-10-csrf-protection.md)

#### 10.11 — Brute-force / credential-stuffing protection

- 📄 Story: [specs/epics/10-auth-security/story-10-11-brute-force-protection.md](../../specs/epics/10-auth-security/story-10-11-brute-force-protection.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-11-brute-force-protection.md](../../specs/epics/10-auth-security/plan-10-11-brute-force-protection.md)

#### 10.12 — Rate limiting on auth endpoints

- 📄 Story: [specs/epics/10-auth-security/story-10-12-rate-limiting-auth.md](../../specs/epics/10-auth-security/story-10-12-rate-limiting-auth.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-12-rate-limiting-auth.md](../../specs/epics/10-auth-security/plan-10-12-rate-limiting-auth.md)
- 🔌 API: `POST /auth/login`, `POST /auth/refresh`

#### 10.13 — Permission model

- 📄 Story: [specs/epics/10-auth-security/story-10-13-permission-model.md](../../specs/epics/10-auth-security/story-10-13-permission-model.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-13-permission-model.md](../../specs/epics/10-auth-security/plan-10-13-permission-model.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}` *(+1)*
- 🗄 Tables: `playback_state`, `saved_searches`

#### 10.14 — Secret loading and redaction

- 📄 Story: [specs/epics/10-auth-security/story-10-14-secret-loading.md](../../specs/epics/10-auth-security/story-10-14-secret-loading.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-14-secret-loading.md](../../specs/epics/10-auth-security/plan-10-14-secret-loading.md)
- 🔌 API: `GET /settings`, `PUT /settings`

#### 10.15 — Transport security

- 📄 Story: [specs/epics/10-auth-security/story-10-15-transport-security.md](../../specs/epics/10-auth-security/story-10-15-transport-security.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-15-transport-security.md](../../specs/epics/10-auth-security/plan-10-15-transport-security.md)
- 🔌 API: `GET /ws`, `GET /system/health`

#### 10.16 — Audit log for security-sensitive actions

- 📄 Story: [specs/epics/10-auth-security/story-10-16-security-audit.md](../../specs/epics/10-auth-security/story-10-16-security-audit.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-16-security-audit.md](../../specs/epics/10-auth-security/plan-10-16-security-audit.md)
- 🗄 Tables: `audit_log`

#### 10.17 — Device pairing endpoint

- 📄 Story: [specs/epics/10-auth-security/story-10-17-auth-pair.md](../../specs/epics/10-auth-security/story-10-17-auth-pair.md)
- 🛠 Plan: [specs/epics/10-auth-security/plan-10-17-auth-pair.md](../../specs/epics/10-auth-security/plan-10-17-auth-pair.md)
- 🗄 Tables: `audit_log`


## Epic 11 — Web UI

**Engine:** Web (React/TS)  ·  **Phase:** 3

**Goal.** A single React 18 + TypeScript + Vite SPA that runs as the

📖 [Epic README](../../specs/epics/11-web-ui/README.md)

### Features

#### 11.1 — Library browser (grid / list view, sorting, filtering)

- 📄 Story: [specs/epics/11-web-ui/story-11-01-library-browser.md](../../specs/epics/11-web-ui/story-11-01-library-browser.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-01-library-browser.md](../../specs/epics/11-web-ui/plan-11-01-library-browser.md)
- 🖼 Mockups: [mockup-11-01-library-browser.html](../../web/mockups/mockup-11-01-library-browser.html)

#### 11.2 — Video detail page (metadata, subtitle tracks, processing status)

- 📄 Story: [specs/epics/11-web-ui/story-11-02-video-detail-page.md](../../specs/epics/11-web-ui/story-11-02-video-detail-page.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-02-video-detail-page.md](../../specs/epics/11-web-ui/plan-11-02-video-detail-page.md)
- 🖼 Mockups: [mockup-11-02-video-detail.html](../../web/mockups/mockup-11-02-video-detail.html)
- 🔌 API: `GET /jobs`, `GET /ws`
- 🗄 Tables: `processing_jobs`

#### 11.3 — Video player (HLS.js / Vidstack, subtitle overlay, chapter nav, speed control)

- 📄 Story: [specs/epics/11-web-ui/story-11-03-video-player.md](../../specs/epics/11-web-ui/story-11-03-video-player.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-03-video-player.md](../../specs/epics/11-web-ui/plan-11-03-video-player.md)
- 🖼 Mockups: [mockup-11-03-video-player.html](../../web/mockups/mockup-11-03-video-player.html)
- 🔌 API: `POST /sessions`, `GET /sessions/{id}`, `DELETE /sessions/{id}`, `POST /sessions/{id}/progress`

#### 11.4 — Search interface (instant search, faceted filters, time-coded results)

- 📄 Story: [specs/epics/11-web-ui/story-11-04-search-interface.md](../../specs/epics/11-web-ui/story-11-04-search-interface.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-04-search-interface.md](../../specs/epics/11-web-ui/plan-11-04-search-interface.md)
- 🖼 Mockups: [mockup-11-04-search-interface.html](../../web/mockups/mockup-11-04-search-interface.html)
- 🔌 API: `GET /search`, `POST /search`, `GET /search/suggest`, `POST /search/save`

#### 11.5 — Processing queue dashboard (progress bars, pause / resume controls)

- 📄 Story: [specs/epics/11-web-ui/story-11-05-processing-queue-dashboard.md](../../specs/epics/11-web-ui/story-11-05-processing-queue-dashboard.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md](../../specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md)
- 🖼 Mockups: [mockup-11-05-processing-queue.html](../../web/mockups/mockup-11-05-processing-queue.html)
- 🔌 API: `GET /jobs`, `GET /jobs/{id}`, `GET /queue/stats`, `GET /ws`

#### 11.6 — Settings page (STT engine config, library paths, user preferences)

- 📄 Story: [specs/epics/11-web-ui/story-11-06-settings-page.md](../../specs/epics/11-web-ui/story-11-06-settings-page.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-06-settings-page.md](../../specs/epics/11-web-ui/plan-11-06-settings-page.md)
- 🖼 Mockups: [mockup-11-06-settings.html](../../web/mockups/mockup-11-06-settings.html)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}` *(+4)*

#### 11.7 — Responsive design (desktop, tablet, mobile)

- 📄 Story: [specs/epics/11-web-ui/story-11-07-responsive-design.md](../../specs/epics/11-web-ui/story-11-07-responsive-design.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-07-responsive-design.md](../../specs/epics/11-web-ui/plan-11-07-responsive-design.md)
- 🖼 Mockups: [mockup-11-07-theme.html](../../web/mockups/mockup-11-07-theme.html)

#### 11.8 — Dark / light theme

- 📄 Story: [specs/epics/11-web-ui/story-11-08-dark-light-theme.md](../../specs/epics/11-web-ui/story-11-08-dark-light-theme.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-08-dark-light-theme.md](../../specs/epics/11-web-ui/plan-11-08-dark-light-theme.md)

#### 11.9 — Keyboard shortcuts

- 📄 Story: [specs/epics/11-web-ui/story-11-09-keyboard-shortcuts.md](../../specs/epics/11-web-ui/story-11-09-keyboard-shortcuts.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-09-keyboard-shortcuts.md](../../specs/epics/11-web-ui/plan-11-09-keyboard-shortcuts.md)

#### 11.10 — Offline capability (PWA service worker)

- 📄 Story: [specs/epics/11-web-ui/story-11-10-offline-pwa.md](../../specs/epics/11-web-ui/story-11-10-offline-pwa.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-10-offline-pwa.md](../../specs/epics/11-web-ui/plan-11-10-offline-pwa.md)
- 🖼 Mockups: [mockup-11-10-offline-pwa.html](../../web/mockups/mockup-11-10-offline-pwa.html)
- 🔌 API: `POST /auth/login`, `POST /auth/refresh`, `GET /search`, `POST /search`, `POST /search/save` *(+6)*

#### 11.11 — Accessibility (WCAG 2.1 AA)

- 📄 Story: [specs/epics/11-web-ui/story-11-11-accessibility.md](../../specs/epics/11-web-ui/story-11-11-accessibility.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-11-accessibility.md](../../specs/epics/11-web-ui/plan-11-11-accessibility.md)

#### 11.12 — i18n (Arabic RTL + English LTR)

- 📄 Story: [specs/epics/11-web-ui/story-11-12-i18n-rtl.md](../../specs/epics/11-web-ui/story-11-12-i18n-rtl.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-12-i18n-rtl.md](../../specs/epics/11-web-ui/plan-11-12-i18n-rtl.md)
- 🖼 Mockups: [mockup-11-12-i18n.html](../../web/mockups/mockup-11-12-i18n.html)

#### 11.13 — Personal Access Token (PAT) management

- 📄 Story: [specs/epics/11-web-ui/story-11-13-pat-management-api.md](../../specs/epics/11-web-ui/story-11-13-pat-management-api.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-13-pat-management-api.md](../../specs/epics/11-web-ui/plan-11-13-pat-management-api.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /videos`
- 🗄 Tables: `audit_log`

#### 11.14 — Active session listing & per-session revoke

- 📄 Story: [specs/epics/11-web-ui/story-11-14-active-sessions-api.md](../../specs/epics/11-web-ui/story-11-14-active-sessions-api.md)
- 🛠 Plan: [specs/epics/11-web-ui/plan-11-14-active-sessions-api.md](../../specs/epics/11-web-ui/plan-11-14-active-sessions-api.md)
- 🔌 API: `POST /auth/logout`, `POST /sessions`, `GET /sessions/{id}`, `DELETE /sessions/{id}`


## Epic 12 — Mobile

**Engine:** Mobile (Capacitor + plugins)  ·  **Phase:** 3

**Goal.** iOS and Android apps that wrap the same web bundle as a native

📖 [Epic README](../../specs/epics/12-mobile/README.md)

### Features

#### 12.1 — iOS app wrapper

- 📄 Story: [specs/epics/12-mobile/story-12-01-ios-app.md](../../specs/epics/12-mobile/story-12-01-ios-app.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-01-ios-app.md](../../specs/epics/12-mobile/plan-12-01-ios-app.md)

#### 12.2 — Android app wrapper

- 📄 Story: [specs/epics/12-mobile/story-12-02-android-app.md](../../specs/epics/12-mobile/story-12-02-android-app.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-02-android-app.md](../../specs/epics/12-mobile/plan-12-02-android-app.md)
- 🔌 API: `GET /system/version`

#### 12.3 — Native video player integration

- 📄 Story: [specs/epics/12-mobile/story-12-03-native-player.md](../../specs/epics/12-mobile/story-12-03-native-player.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-03-native-player.md](../../specs/epics/12-mobile/plan-12-03-native-player.md)
- 🔌 API: `POST /sessions`, `GET /sessions/{id}`, `DELETE /sessions/{id}`, `POST /sessions/{id}/progress`

#### 12.4 — Push notifications (processing complete, new content)

- 📄 Story: [specs/epics/12-mobile/story-12-04-push-notifications.md](../../specs/epics/12-mobile/story-12-04-push-notifications.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-04-push-notifications.md](../../specs/epics/12-mobile/plan-12-04-push-notifications.md)
- 🔌 API: `POST /devices/register`, `GET /devices`

#### 12.5 — Background playback

- 📄 Story: [specs/epics/12-mobile/story-12-05-background-playback.md](../../specs/epics/12-mobile/story-12-05-background-playback.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-05-background-playback.md](../../specs/epics/12-mobile/plan-12-05-background-playback.md)

#### 12.6 — Download for offline viewing

- 📄 Story: [specs/epics/12-mobile/story-12-06-offline-downloads.md](../../specs/epics/12-mobile/story-12-06-offline-downloads.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-06-offline-downloads.md](../../specs/epics/12-mobile/plan-12-06-offline-downloads.md)

#### 12.7 — Share / AirPlay / Chromecast support

- 📄 Story: [specs/epics/12-mobile/story-12-07-share-cast.md](../../specs/epics/12-mobile/story-12-07-share-cast.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-07-share-cast.md](../../specs/epics/12-mobile/plan-12-07-share-cast.md)

#### 12.8 — Haptic feedback

- 📄 Story: [specs/epics/12-mobile/story-12-08-haptics.md](../../specs/epics/12-mobile/story-12-08-haptics.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-08-haptics.md](../../specs/epics/12-mobile/plan-12-08-haptics.md)

#### 12.9 — Deep linking

- 📄 Story: [specs/epics/12-mobile/story-12-09-deep-linking.md](../../specs/epics/12-mobile/story-12-09-deep-linking.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-09-deep-linking.md](../../specs/epics/12-mobile/plan-12-09-deep-linking.md)
- 🔌 API: `GET /search`, `POST /search`, `GET /settings`, `PUT /settings`

#### 12.10 — API: device registration & push fan-out

- 📄 Story: [specs/epics/12-mobile/story-12-10-device-registration-api.md](../../specs/epics/12-mobile/story-12-10-device-registration-api.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-10-device-registration-api.md](../../specs/epics/12-mobile/plan-12-10-device-registration-api.md)
- 🔌 API: `POST /devices/register`, `GET /devices`, `DELETE /devices/{id}`
- 🗄 Tables: `devices`, `audit_log`

#### 12.11 — API: per-device "downloaded" flag sync

- 📄 Story: [specs/epics/12-mobile/story-12-11-downloaded-flag-api.md](../../specs/epics/12-mobile/story-12-11-downloaded-flag-api.md)
- 🛠 Plan: [specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md](../../specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md)
- 🔌 API: `GET /videos`


## Epic 13 — Desktop

**Engine:** Desktop (Tauri)  ·  **Phase:** 3

**Goal.** A Tauri 2 wrapper of the same web bundle producing native

📖 [Epic README](../../specs/epics/13-desktop/README.md)

### Features

#### 13.1 — macOS app

- 📄 Story: [specs/epics/13-desktop/story-13-01-macos.md](../../specs/epics/13-desktop/story-13-01-macos.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-01-macos.md](../../specs/epics/13-desktop/plan-13-01-macos.md)

#### 13.2 — Windows app

- 📄 Story: [specs/epics/13-desktop/story-13-02-windows.md](../../specs/epics/13-desktop/story-13-02-windows.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-02-windows.md](../../specs/epics/13-desktop/plan-13-02-windows.md)

#### 13.3 — Linux app

- 📄 Story: [specs/epics/13-desktop/story-13-03-linux.md](../../specs/epics/13-desktop/story-13-03-linux.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-03-linux.md](../../specs/epics/13-desktop/plan-13-03-linux.md)

#### 13.4 — System tray integration

- 📄 Story: [specs/epics/13-desktop/story-13-04-system-tray.md](../../specs/epics/13-desktop/story-13-04-system-tray.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-04-system-tray.md](../../specs/epics/13-desktop/plan-13-04-system-tray.md)

#### 13.5 — Local server auto-discovery (Bonjour / mDNS)

- 📄 Story: [specs/epics/13-desktop/story-13-05-mdns-discovery.md](../../specs/epics/13-desktop/story-13-05-mdns-discovery.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-05-mdns-discovery.md](../../specs/epics/13-desktop/plan-13-05-mdns-discovery.md)

#### 13.6 — File drag-and-drop to add videos

- 📄 Story: [specs/epics/13-desktop/story-13-06-drag-drop.md](../../specs/epics/13-desktop/story-13-06-drag-drop.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-06-drag-drop.md](../../specs/epics/13-desktop/plan-13-06-drag-drop.md)

#### 13.7 — Keyboard shortcuts (desktop-specific)

- 📄 Story: [specs/epics/13-desktop/story-13-07-keyboard-shortcuts.md](../../specs/epics/13-desktop/story-13-07-keyboard-shortcuts.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-07-keyboard-shortcuts.md](../../specs/epics/13-desktop/plan-13-07-keyboard-shortcuts.md)

#### 13.8 — Auto-update

- 📄 Story: [specs/epics/13-desktop/story-13-08-auto-update.md](../../specs/epics/13-desktop/story-13-08-auto-update.md)
- 🛠 Plan: [specs/epics/13-desktop/plan-13-08-auto-update.md](../../specs/epics/13-desktop/plan-13-08-auto-update.md)


## Epic 14 — TV Apps

**Engine:** TV (Swift / Kotlin)  ·  **Phase:** 3

**Goal.** Native tvOS (Swift / SwiftUI / AVPlayer) and Android TV

📖 [Epic README](../../specs/epics/14-tv-apps/README.md)

### Features

#### 14.1 — tvOS app (Swift / SwiftUI)

- 📄 Story: [specs/epics/14-tv-apps/story-14-01-tvos.md](../../specs/epics/14-tv-apps/story-14-01-tvos.md)
- 🛠 Plan: [specs/epics/14-tv-apps/plan-14-01-tvos.md](../../specs/epics/14-tv-apps/plan-14-01-tvos.md)

#### 14.2 — Android TV app (Kotlin / Leanback)

- 📄 Story: [specs/epics/14-tv-apps/story-14-02-android-tv.md](../../specs/epics/14-tv-apps/story-14-02-android-tv.md)
- 🛠 Plan: [specs/epics/14-tv-apps/plan-14-02-android-tv.md](../../specs/epics/14-tv-apps/plan-14-02-android-tv.md)
- 🔌 API: `GET /search`, `POST /search`, `GET /search/suggest`

#### 14.3 — 10-foot UI design (large text, D-pad navigation)

- 📄 Story: [specs/epics/14-tv-apps/story-14-03-10-foot-ui.md](../../specs/epics/14-tv-apps/story-14-03-10-foot-ui.md)
- 🛠 Plan: [specs/epics/14-tv-apps/plan-14-03-10-foot-ui.md](../../specs/epics/14-tv-apps/plan-14-03-10-foot-ui.md)

#### 14.4 — Voice search integration (Siri, Google Assistant)

- 📄 Story: [specs/epics/14-tv-apps/story-14-04-voice-search.md](../../specs/epics/14-tv-apps/story-14-04-voice-search.md)
- 🛠 Plan: [specs/epics/14-tv-apps/plan-14-04-voice-search.md](../../specs/epics/14-tv-apps/plan-14-04-voice-search.md)
- 🔌 API: `GET /search`, `POST /search`, `GET /search/suggest`

#### 14.5 — Continue Watching row

- 📄 Story: [specs/epics/14-tv-apps/story-14-05-continue-watching.md](../../specs/epics/14-tv-apps/story-14-05-continue-watching.md)
- 🛠 Plan: [specs/epics/14-tv-apps/plan-14-05-continue-watching.md](../../specs/epics/14-tv-apps/plan-14-05-continue-watching.md)
- 🗄 Tables: `playback_state`

#### 14.6 — Recommendations UI

- 📄 Story: [specs/epics/14-tv-apps/story-14-06-recommendations-ui.md](../../specs/epics/14-tv-apps/story-14-06-recommendations-ui.md)
- 🛠 Plan: [specs/epics/14-tv-apps/plan-14-06-recommendations-ui.md](../../specs/epics/14-tv-apps/plan-14-06-recommendations-ui.md)
- 🔌 API: `GET /recommendations`

#### 14.7 — API: recommendations endpoint

- 📄 Story: [specs/epics/14-tv-apps/story-14-07-recommendations-api.md](../../specs/epics/14-tv-apps/story-14-07-recommendations-api.md)
- 🛠 Plan: [specs/epics/14-tv-apps/plan-14-07-recommendations-api.md](../../specs/epics/14-tv-apps/plan-14-07-recommendations-api.md)
- 🔌 API: `GET /recommendations`
- 🗄 Tables: `playback_state`, `media_features`


## Epic 15 — Discovery

**Engine:** API (Go) + Web  ·  **Phase:** 3

**Goal.** Make Maktaba easy to find on the LAN, optionally reachable

📖 [Epic README](../../specs/epics/15-discovery/README.md)

### Features

#### 15.1 — Local network discovery (mDNS / Bonjour)

- 📄 Story: [specs/epics/15-discovery/story-15-01-mdns.md](../../specs/epics/15-discovery/story-15-01-mdns.md)
- 🛠 Plan: [specs/epics/15-discovery/plan-15-01-mdns.md](../../specs/epics/15-discovery/plan-15-01-mdns.md)

#### 15.2 — Global discovery (optional cloud relay)

- 📄 Story: [specs/epics/15-discovery/story-15-02-cloud-relay.md](../../specs/epics/15-discovery/story-15-02-cloud-relay.md)
- 🛠 Plan: [specs/epics/15-discovery/plan-15-02-cloud-relay.md](../../specs/epics/15-discovery/plan-15-02-cloud-relay.md)

#### 15.3 — Server-to-server federation (optional)

- 📄 Story: [specs/epics/15-discovery/story-15-03-federation.md](../../specs/epics/15-discovery/story-15-03-federation.md)
- 🛠 Plan: [specs/epics/15-discovery/plan-15-03-federation.md](../../specs/epics/15-discovery/plan-15-03-federation.md)

#### 15.4 — UPnP / DLNA compatibility

- 📄 Story: [specs/epics/15-discovery/story-15-04-dlna-upnp.md](../../specs/epics/15-discovery/story-15-04-dlna-upnp.md)
- 🛠 Plan: [specs/epics/15-discovery/plan-15-04-dlna-upnp.md](../../specs/epics/15-discovery/plan-15-04-dlna-upnp.md)

#### 15.5 — QR code pairing for mobile → server

- 📄 Story: [specs/epics/15-discovery/story-15-05-qr-pairing.md](../../specs/epics/15-discovery/story-15-05-qr-pairing.md)
- 🛠 Plan: [specs/epics/15-discovery/plan-15-05-qr-pairing.md](../../specs/epics/15-discovery/plan-15-05-qr-pairing.md)

#### 15.6 — API: device pairing endpoints

- 📄 Story: [specs/epics/15-discovery/story-15-06-pairing-api.md](../../specs/epics/15-discovery/story-15-06-pairing-api.md)
- 🛠 Plan: [specs/epics/15-discovery/plan-15-06-pairing-api.md](../../specs/epics/15-discovery/plan-15-06-pairing-api.md)

#### 15.7 — API: federation endpoints + crypto

- 📄 Story: [specs/epics/15-discovery/story-15-07-federation-api.md](../../specs/epics/15-discovery/story-15-07-federation-api.md)
- 🛠 Plan: [specs/epics/15-discovery/plan-15-07-federation-api.md](../../specs/epics/15-discovery/plan-15-07-federation-api.md)


## Epic 16 — Subscriptions

**Engine:** API (Go) + Pipeline  ·  **Phase:** 3

**Goal.** Maktaba is fully usable for free as a self-hosted single-user

📖 [Epic README](../../specs/epics/16-subscriptions/README.md)

### Features

#### 16.1 — Free tier (local only, single user)

- 📄 Story: [specs/epics/16-subscriptions/story-16-01-free-tier.md](../../specs/epics/16-subscriptions/story-16-01-free-tier.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-01-free-tier.md](../../specs/epics/16-subscriptions/plan-16-01-free-tier.md)

#### 16.2 — Premium features (remote access, multi-user, cloud backup)

- 📄 Story: [specs/epics/16-subscriptions/story-16-02-premium-features.md](../../specs/epics/16-subscriptions/story-16-02-premium-features.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-02-premium-features.md](../../specs/epics/16-subscriptions/plan-16-02-premium-features.md)

#### 16.3 — Subscription management

- 📄 Story: [specs/epics/16-subscriptions/story-16-03-subscription-management.md](../../specs/epics/16-subscriptions/story-16-03-subscription-management.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-03-subscription-management.md](../../specs/epics/16-subscriptions/plan-16-03-subscription-management.md)

#### 16.4 — License key validation

- 📄 Story: [specs/epics/16-subscriptions/story-16-04-license-validation.md](../../specs/epics/16-subscriptions/story-16-04-license-validation.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-04-license-validation.md](../../specs/epics/16-subscriptions/plan-16-04-license-validation.md)
- 🔌 API: `GET /settings`, `PUT /settings`

#### 16.5 — Usage analytics (opt-in)

- 📄 Story: [specs/epics/16-subscriptions/story-16-05-telemetry-opt-in.md](../../specs/epics/16-subscriptions/story-16-05-telemetry-opt-in.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-05-telemetry-opt-in.md](../../specs/epics/16-subscriptions/plan-16-05-telemetry-opt-in.md)

#### 16.6 — Feature flags per tier (client surface)

- 📄 Story: [specs/epics/16-subscriptions/story-16-06-feature-flags.md](../../specs/epics/16-subscriptions/story-16-06-feature-flags.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-06-feature-flags.md](../../specs/epics/16-subscriptions/plan-16-06-feature-flags.md)

#### 16.7 — API: telemetry sink

- 📄 Story: [specs/epics/16-subscriptions/story-16-07-telemetry-api.md](../../specs/epics/16-subscriptions/story-16-07-telemetry-api.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-07-telemetry-api.md](../../specs/epics/16-subscriptions/plan-16-07-telemetry-api.md)
- 🔌 API: `GET /devices`

#### 16.8 — API: feature-flag resolution endpoint

- 📄 Story: [specs/epics/16-subscriptions/story-16-08-feature-flags-api.md](../../specs/epics/16-subscriptions/story-16-08-feature-flags-api.md)
- 🛠 Plan: [specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md](../../specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md)
- 🗄 Tables: `audit_log`


## Epic 17 — UX & Design System

**Engine:** Web (React/TS)  ·  **Phase:** 3

**Goal.** A coherent visual + interaction language across web, mobile,

📖 [Epic README](../../specs/epics/17-ux-design-system/README.md)

### Features

#### 17.1 — Design tokens (colors, typography, spacing)

- 📄 Story: [specs/epics/17-ux-design-system/story-17-01-design-tokens.md](../../specs/epics/17-ux-design-system/story-17-01-design-tokens.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-01-design-tokens.md](../../specs/epics/17-ux-design-system/plan-17-01-design-tokens.md)

#### 17.2 — Component library (buttons, cards, modals, forms)

- 📄 Story: [specs/epics/17-ux-design-system/story-17-02-component-library.md](../../specs/epics/17-ux-design-system/story-17-02-component-library.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-02-component-library.md](../../specs/epics/17-ux-design-system/plan-17-02-component-library.md)

#### 17.3 — Motion / animation guidelines

- 📄 Story: [specs/epics/17-ux-design-system/story-17-03-motion.md](../../specs/epics/17-ux-design-system/story-17-03-motion.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-03-motion.md](../../specs/epics/17-ux-design-system/plan-17-03-motion.md)

#### 17.4 — Loading states and skeleton screens

- 📄 Story: [specs/epics/17-ux-design-system/story-17-04-loading-states.md](../../specs/epics/17-ux-design-system/story-17-04-loading-states.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-04-loading-states.md](../../specs/epics/17-ux-design-system/plan-17-04-loading-states.md)

#### 17.5 — Error states and empty states

- 📄 Story: [specs/epics/17-ux-design-system/story-17-05-error-empty-states.md](../../specs/epics/17-ux-design-system/story-17-05-error-empty-states.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md](../../specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md)

#### 17.6 — Onboarding flow (first-time setup wizard)

- 📄 Story: [specs/epics/17-ux-design-system/story-17-06-onboarding.md](../../specs/epics/17-ux-design-system/story-17-06-onboarding.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-06-onboarding.md](../../specs/epics/17-ux-design-system/plan-17-06-onboarding.md)
- 🖼 Mockups: [mockup-17-06-onboarding.html](../../web/mockups/mockup-17-06-onboarding.html)

#### 17.7 — Arabic RTL layout system

- 📄 Story: [specs/epics/17-ux-design-system/story-17-07-rtl-layout.md](../../specs/epics/17-ux-design-system/story-17-07-rtl-layout.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-07-rtl-layout.md](../../specs/epics/17-ux-design-system/plan-17-07-rtl-layout.md)

#### 17.8 — Video player controls design

- 📄 Story: [specs/epics/17-ux-design-system/story-17-08-player-controls.md](../../specs/epics/17-ux-design-system/story-17-08-player-controls.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-08-player-controls.md](../../specs/epics/17-ux-design-system/plan-17-08-player-controls.md)

#### 17.9 — Search results presentation

- 📄 Story: [specs/epics/17-ux-design-system/story-17-09-search-results.md](../../specs/epics/17-ux-design-system/story-17-09-search-results.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-09-search-results.md](../../specs/epics/17-ux-design-system/plan-17-09-search-results.md)

#### 17.10 — Processing progress visualization

- 📄 Story: [specs/epics/17-ux-design-system/story-17-10-processing-progress.md](../../specs/epics/17-ux-design-system/story-17-10-processing-progress.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-10-processing-progress.md](../../specs/epics/17-ux-design-system/plan-17-10-processing-progress.md)

#### 17.11 — Subtitle and transcript presentation

- 📄 Story: [specs/epics/17-ux-design-system/story-17-11-transcript-presentation.md](../../specs/epics/17-ux-design-system/story-17-11-transcript-presentation.md)
- 🛠 Plan: [specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md](../../specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md)


## Epic 18 — Performance

**Engine:** Cross-cutting  ·  **Phase:** 4

**Goal.** Maktaba feels snappy on a single Mac mini / NAS-class host with a

📖 [Epic README](../../specs/epics/18-performance/README.md)

### Features

#### 18.1 — Define and codify latency budgets

- 📄 Story: [specs/epics/18-performance/story-18-01-latency-budgets.md](../../specs/epics/18-performance/story-18-01-latency-budgets.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-01-latency-budgets.md](../../specs/epics/18-performance/plan-18-01-latency-budgets.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /videos`, `GET /videos/{id}`, `PATCH /videos/{id}` *(+3)*

#### 18.2 — Search end-to-end performance

- 📄 Story: [specs/epics/18-performance/story-18-02-search-performance.md](../../specs/epics/18-performance/story-18-02-search-performance.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-02-search-performance.md](../../specs/epics/18-performance/plan-18-02-search-performance.md)

#### 18.3 — Streaming hot-path performance

- 📄 Story: [specs/epics/18-performance/story-18-03-streaming-hot-path.md](../../specs/epics/18-performance/story-18-03-streaming-hot-path.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-03-streaming-hot-path.md](../../specs/epics/18-performance/plan-18-03-streaming-hot-path.md)

#### 18.4 — Pipeline throughput targets

- 📄 Story: [specs/epics/18-performance/story-18-04-pipeline-throughput.md](../../specs/epics/18-performance/story-18-04-pipeline-throughput.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-04-pipeline-throughput.md](../../specs/epics/18-performance/plan-18-04-pipeline-throughput.md)

#### 18.5 — Memory and CPU envelopes

- 📄 Story: [specs/epics/18-performance/story-18-05-memory-cpu-envelopes.md](../../specs/epics/18-performance/story-18-05-memory-cpu-envelopes.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-05-memory-cpu-envelopes.md](../../specs/epics/18-performance/plan-18-05-memory-cpu-envelopes.md)

#### 18.6 — Client-perceived performance

- 📄 Story: [specs/epics/18-performance/story-18-06-client-perceived-performance.md](../../specs/epics/18-performance/story-18-06-client-perceived-performance.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-06-client-perceived-performance.md](../../specs/epics/18-performance/plan-18-06-client-perceived-performance.md)

#### 18.7 — Database query performance and N+1 prevention

- 📄 Story: [specs/epics/18-performance/story-18-07-database-query-performance.md](../../specs/epics/18-performance/story-18-07-database-query-performance.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-07-database-query-performance.md](../../specs/epics/18-performance/plan-18-07-database-query-performance.md)
- 🔌 API: `GET /videos`, `GET /queue/stats`

#### 18.8 — Cache layout and hit-rate floors

- 📄 Story: [specs/epics/18-performance/story-18-08-cache-layout-hit-rates.md](../../specs/epics/18-performance/story-18-08-cache-layout-hit-rates.md)
- 🛠 Plan: [specs/epics/18-performance/plan-18-08-cache-layout-hit-rates.md](../../specs/epics/18-performance/plan-18-08-cache-layout-hit-rates.md)


## Epic 19 — Scalability

**Engine:** Cross-cutting  ·  **Phase:** 4

**Goal.** Maktaba serves the 30 TB / single-household target on one box

📖 [Epic README](../../specs/epics/19-scalability/README.md)

### Features

#### 19.1 — Single-host capacity floor

- 📄 Story: [specs/epics/19-scalability/story-19-01-single-host-capacity.md](../../specs/epics/19-scalability/story-19-01-single-host-capacity.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-01-single-host-capacity.md](../../specs/epics/19-scalability/plan-19-01-single-host-capacity.md)

#### 19.2 — Horizontal scale-out for the API service

- 📄 Story: [specs/epics/19-scalability/story-19-02-api-scale-out.md](../../specs/epics/19-scalability/story-19-02-api-scale-out.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-02-api-scale-out.md](../../specs/epics/19-scalability/plan-19-02-api-scale-out.md)
- 🗄 Tables: `events`

#### 19.3 — Horizontal scale-out for the streaming service

- 📄 Story: [specs/epics/19-scalability/story-19-03-streaming-scale-out.md](../../specs/epics/19-scalability/story-19-03-streaming-scale-out.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-03-streaming-scale-out.md](../../specs/epics/19-scalability/plan-19-03-streaming-scale-out.md)

#### 19.4 — Horizontal scale-out for the pipeline service

- 📄 Story: [specs/epics/19-scalability/story-19-04-pipeline-scale-out.md](../../specs/epics/19-scalability/story-19-04-pipeline-scale-out.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-04-pipeline-scale-out.md](../../specs/epics/19-scalability/plan-19-04-pipeline-scale-out.md)

#### 19.5 — Database scaling and failover

- 📄 Story: [specs/epics/19-scalability/story-19-05-database-scaling.md](../../specs/epics/19-scalability/story-19-05-database-scaling.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-05-database-scaling.md](../../specs/epics/19-scalability/plan-19-05-database-scaling.md)

#### 19.6 — Storage scaling and large library handling

- 📄 Story: [specs/epics/19-scalability/story-19-06-storage-scaling.md](../../specs/epics/19-scalability/story-19-06-storage-scaling.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-06-storage-scaling.md](../../specs/epics/19-scalability/plan-19-06-storage-scaling.md)

#### 19.7 — Concurrency caps and quotas

- 📄 Story: [specs/epics/19-scalability/story-19-07-concurrency-caps.md](../../specs/epics/19-scalability/story-19-07-concurrency-caps.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-07-concurrency-caps.md](../../specs/epics/19-scalability/plan-19-07-concurrency-caps.md)
- 🔌 API: `GET /system/health`

#### 19.8 — Multi-tenant readiness (deferred capacity)

- 📄 Story: [specs/epics/19-scalability/story-19-08-multi-tenant-readiness.md](../../specs/epics/19-scalability/story-19-08-multi-tenant-readiness.md)
- 🛠 Plan: [specs/epics/19-scalability/plan-19-08-multi-tenant-readiness.md](../../specs/epics/19-scalability/plan-19-08-multi-tenant-readiness.md)


## Epic 20 — Testing

**Engine:** Cross-cutting  ·  **Phase:** 4

**Goal.** Every layer of Maktaba has a test posture proportional to its

📖 [Epic README](../../specs/epics/20-testing/README.md)

### Features

#### 20.1 — Test pyramid and runtime budgets

- 📄 Story: [specs/epics/20-testing/story-20-01-test-pyramid.md](../../specs/epics/20-testing/story-20-01-test-pyramid.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-01-test-pyramid.md](../../specs/epics/20-testing/plan-20-01-test-pyramid.md)

#### 20.2 — Fixtures and seed data

- 📄 Story: [specs/epics/20-testing/story-20-02-fixtures-seed-data.md](../../specs/epics/20-testing/story-20-02-fixtures-seed-data.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-02-fixtures-seed-data.md](../../specs/epics/20-testing/plan-20-02-fixtures-seed-data.md)

#### 20.3 — Unit test coverage and conventions

- 📄 Story: [specs/epics/20-testing/story-20-03-unit-test-coverage.md](../../specs/epics/20-testing/story-20-03-unit-test-coverage.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-03-unit-test-coverage.md](../../specs/epics/20-testing/plan-20-03-unit-test-coverage.md)

#### 20.4 — Integration tests with real backends

- 📄 Story: [specs/epics/20-testing/story-20-04-integration-tests.md](../../specs/epics/20-testing/story-20-04-integration-tests.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-04-integration-tests.md](../../specs/epics/20-testing/plan-20-04-integration-tests.md)

#### 20.5 — End-to-end smoke flows

- 📄 Story: [specs/epics/20-testing/story-20-05-e2e-smoke-flows.md](../../specs/epics/20-testing/story-20-05-e2e-smoke-flows.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-05-e2e-smoke-flows.md](../../specs/epics/20-testing/plan-20-05-e2e-smoke-flows.md)

#### 20.6 — Contract tests for service boundaries

- 📄 Story: [specs/epics/20-testing/story-20-06-contract-tests.md](../../specs/epics/20-testing/story-20-06-contract-tests.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-06-contract-tests.md](../../specs/epics/20-testing/plan-20-06-contract-tests.md)

#### 20.7 — Performance regression tests in CI

- 📄 Story: [specs/epics/20-testing/story-20-07-perf-regression-ci.md](../../specs/epics/20-testing/story-20-07-perf-regression-ci.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-07-perf-regression-ci.md](../../specs/epics/20-testing/plan-20-07-perf-regression-ci.md)

#### 20.8 — Flaky test policy

- 📄 Story: [specs/epics/20-testing/story-20-08-flaky-test-policy.md](../../specs/epics/20-testing/story-20-08-flaky-test-policy.md)
- 🛠 Plan: [specs/epics/20-testing/plan-20-08-flaky-test-policy.md](../../specs/epics/20-testing/plan-20-08-flaky-test-policy.md)


## Epic 21 — Observability

**Engine:** Cross-cutting  ·  **Phase:** 4

**Goal.** A self-hoster can answer "what's it doing?" and "why is it

📖 [Epic README](../../specs/epics/21-observability/README.md)

### Features

#### 21.1 — Structured logging

- 📄 Story: [specs/epics/21-observability/story-21-01-structured-logging.md](../../specs/epics/21-observability/story-21-01-structured-logging.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-01-structured-logging.md](../../specs/epics/21-observability/plan-21-01-structured-logging.md)

#### 21.2 — Metrics surface

- 📄 Story: [specs/epics/21-observability/story-21-02-metrics-surface.md](../../specs/epics/21-observability/story-21-02-metrics-surface.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-02-metrics-surface.md](../../specs/epics/21-observability/plan-21-02-metrics-surface.md)

#### 21.3 — Distributed tracing

- 📄 Story: [specs/epics/21-observability/story-21-03-distributed-tracing.md](../../specs/epics/21-observability/story-21-03-distributed-tracing.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-03-distributed-tracing.md](../../specs/epics/21-observability/plan-21-03-distributed-tracing.md)

#### 21.4 — Health and readiness probes

- 📄 Story: [specs/epics/21-observability/story-21-04-health-readiness-probes.md](../../specs/epics/21-observability/story-21-04-health-readiness-probes.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-04-health-readiness-probes.md](../../specs/epics/21-observability/plan-21-04-health-readiness-probes.md)
- 🔌 API: `GET /system/health`

#### 21.5 — Error reporting and alerting integration

- 📄 Story: [specs/epics/21-observability/story-21-05-error-reporting.md](../../specs/epics/21-observability/story-21-05-error-reporting.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-05-error-reporting.md](../../specs/epics/21-observability/plan-21-05-error-reporting.md)

#### 21.6 — Audit log for sensitive actions

- 📄 Story: [specs/epics/21-observability/story-21-06-audit-log.md](../../specs/epics/21-observability/story-21-06-audit-log.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-06-audit-log.md](../../specs/epics/21-observability/plan-21-06-audit-log.md)
- 🔌 API: `GET /libraries`, `POST /libraries`, `GET /libraries/{id}`, `PATCH /libraries/{id}`, `DELETE /libraries/{id}`
- 🗄 Tables: `audit_log`

#### 21.7 — Job and pipeline visibility

- 📄 Story: [specs/epics/21-observability/story-21-07-job-pipeline-visibility.md](../../specs/epics/21-observability/story-21-07-job-pipeline-visibility.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-07-job-pipeline-visibility.md](../../specs/epics/21-observability/plan-21-07-job-pipeline-visibility.md)
- 🔌 API: `GET /jobs`, `GET /jobs/{id}`, `GET /queue/stats`, `GET /ws`

#### 21.8 — Privacy of telemetry

- 📄 Story: [specs/epics/21-observability/story-21-08-telemetry-privacy.md](../../specs/epics/21-observability/story-21-08-telemetry-privacy.md)
- 🛠 Plan: [specs/epics/21-observability/plan-21-08-telemetry-privacy.md](../../specs/epics/21-observability/plan-21-08-telemetry-privacy.md)
- 🔌 API: `GET /settings`, `PUT /settings`


## Epic 22 — DevOps

**Engine:** Cross-cutting  ·  **Phase:** 4

**Goal.** A self-hoster gets running in one command and stays running

📖 [Epic README](../../specs/epics/22-devops/README.md)

### Features

#### 22.1 — Continuous integration pipeline

- 📄 Story: [specs/epics/22-devops/story-22-01-ci-pipeline.md](../../specs/epics/22-devops/story-22-01-ci-pipeline.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-01-ci-pipeline.md](../../specs/epics/22-devops/plan-22-01-ci-pipeline.md)

#### 22.2 — Reproducible builds and artifacts

- 📄 Story: [specs/epics/22-devops/story-22-02-reproducible-builds.md](../../specs/epics/22-devops/story-22-02-reproducible-builds.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-02-reproducible-builds.md](../../specs/epics/22-devops/plan-22-02-reproducible-builds.md)

#### 22.3 — Container images and compose stack

- 📄 Story: [specs/epics/22-devops/story-22-03-container-images.md](../../specs/epics/22-devops/story-22-03-container-images.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-03-container-images.md](../../specs/epics/22-devops/plan-22-03-container-images.md)

#### 22.4 — Database migrations

- 📄 Story: [specs/epics/22-devops/story-22-04-database-migrations.md](../../specs/epics/22-devops/story-22-04-database-migrations.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-04-database-migrations.md](../../specs/epics/22-devops/plan-22-04-database-migrations.md)
- 🗄 Tables: `processing_jobs`

#### 22.5 — Release management and versioning

- 📄 Story: [specs/epics/22-devops/story-22-05-release-management.md](../../specs/epics/22-devops/story-22-05-release-management.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-05-release-management.md](../../specs/epics/22-devops/plan-22-05-release-management.md)
- 🔌 API: `GET /system/version`

#### 22.6 — Upgrade and rollback

- 📄 Story: [specs/epics/22-devops/story-22-06-upgrade-rollback.md](../../specs/epics/22-devops/story-22-06-upgrade-rollback.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-06-upgrade-rollback.md](../../specs/epics/22-devops/plan-22-06-upgrade-rollback.md)

#### 22.7 — Multi-platform packaging

- 📄 Story: [specs/epics/22-devops/story-22-07-multi-platform-packaging.md](../../specs/epics/22-devops/story-22-07-multi-platform-packaging.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-07-multi-platform-packaging.md](../../specs/epics/22-devops/plan-22-07-multi-platform-packaging.md)

#### 22.8 — Local developer workflow

- 📄 Story: [specs/epics/22-devops/story-22-08-local-developer-workflow.md](../../specs/epics/22-devops/story-22-08-local-developer-workflow.md)
- 🛠 Plan: [specs/epics/22-devops/plan-22-08-local-developer-workflow.md](../../specs/epics/22-devops/plan-22-08-local-developer-workflow.md)


## Epic 23 — Security Hardening

**Engine:** API + Streaming  ·  **Phase:** 4

**Goal.** Maktaba is safe to expose on a home LAN by default and safe to

📖 [Epic README](../../specs/epics/23-security/README.md)

### Features

#### 23.1 — Authentication

- 📄 Story: [specs/epics/23-security/story-23-01-authentication.md](../../specs/epics/23-security/story-23-01-authentication.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-01-authentication.md](../../specs/epics/23-security/plan-23-01-authentication.md)

#### 23.2 — Authorization and ACLs

- 📄 Story: [specs/epics/23-security/story-23-02-authorization-acls.md](../../specs/epics/23-security/story-23-02-authorization-acls.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-02-authorization-acls.md](../../specs/epics/23-security/plan-23-02-authorization-acls.md)

#### 23.3 — Transport security

- 📄 Story: [specs/epics/23-security/story-23-03-transport-security.md](../../specs/epics/23-security/story-23-03-transport-security.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-03-transport-security.md](../../specs/epics/23-security/plan-23-03-transport-security.md)

#### 23.4 — Secrets management

- 📄 Story: [specs/epics/23-security/story-23-04-secrets-management.md](../../specs/epics/23-security/story-23-04-secrets-management.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-04-secrets-management.md](../../specs/epics/23-security/plan-23-04-secrets-management.md)
- 🔌 API: `GET /settings`, `PUT /settings`

#### 23.5 — Input validation and content safety

- 📄 Story: [specs/epics/23-security/story-23-05-input-validation.md](../../specs/epics/23-security/story-23-05-input-validation.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-05-input-validation.md](../../specs/epics/23-security/plan-23-05-input-validation.md)
- 🔌 API: `GET /libraries`, `POST /libraries`

#### 23.6 — Rate limiting, lockout, and destructive-action confirmation

- 📄 Story: [specs/epics/23-security/story-23-06-rate-limiting.md](../../specs/epics/23-security/story-23-06-rate-limiting.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-06-rate-limiting.md](../../specs/epics/23-security/plan-23-06-rate-limiting.md)
- 🔌 API: `POST /auth/login`, `POST /auth/refresh`, `GET /libraries`, `POST /libraries`, `GET /libraries/{id}` *(+2)*

#### 23.7 — Supply-chain security

- 📄 Story: [specs/epics/23-security/story-23-07-supply-chain-security.md](../../specs/epics/23-security/story-23-07-supply-chain-security.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-07-supply-chain-security.md](../../specs/epics/23-security/plan-23-07-supply-chain-security.md)

#### 23.8 — Coordinated disclosure and security response

- 📄 Story: [specs/epics/23-security/story-23-08-coordinated-disclosure.md](../../specs/epics/23-security/story-23-08-coordinated-disclosure.md)
- 🛠 Plan: [specs/epics/23-security/plan-23-08-coordinated-disclosure.md](../../specs/epics/23-security/plan-23-08-coordinated-disclosure.md)


## Epic 24 — Data Integrity

**Engine:** Cross-cutting  ·  **Phase:** 4

**Goal.** A user's media library and the platform's derived state survive

📖 [Epic README](../../specs/epics/24-data-integrity/README.md)

### Features

#### 24.1 — Atomic writes for sidecar artifacts

- 📄 Story: [specs/epics/24-data-integrity/story-24-01-atomic-writes.md](../../specs/epics/24-data-integrity/story-24-01-atomic-writes.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-01-atomic-writes.md](../../specs/epics/24-data-integrity/plan-24-01-atomic-writes.md)

#### 24.2 — Idempotent and resumable jobs

- 📄 Story: [specs/epics/24-data-integrity/story-24-02-idempotent-jobs.md](../../specs/epics/24-data-integrity/story-24-02-idempotent-jobs.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-02-idempotent-jobs.md](../../specs/epics/24-data-integrity/plan-24-02-idempotent-jobs.md)

#### 24.3 — Database consistency and constraints

- 📄 Story: [specs/epics/24-data-integrity/story-24-03-database-constraints.md](../../specs/epics/24-data-integrity/story-24-03-database-constraints.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-03-database-constraints.md](../../specs/epics/24-data-integrity/plan-24-03-database-constraints.md)
- 🗄 Tables: `videos`

#### 24.4 — Concurrency and locking

- 📄 Story: [specs/epics/24-data-integrity/story-24-04-concurrency-locking.md](../../specs/epics/24-data-integrity/story-24-04-concurrency-locking.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-04-concurrency-locking.md](../../specs/epics/24-data-integrity/plan-24-04-concurrency-locking.md)

#### 24.5 — Backup and restore

- 📄 Story: [specs/epics/24-data-integrity/story-24-05-backup-restore.md](../../specs/epics/24-data-integrity/story-24-05-backup-restore.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-05-backup-restore.md](../../specs/epics/24-data-integrity/plan-24-05-backup-restore.md)

#### 24.6 — Disaster recovery

- 📄 Story: [specs/epics/24-data-integrity/story-24-06-disaster-recovery.md](../../specs/epics/24-data-integrity/story-24-06-disaster-recovery.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-06-disaster-recovery.md](../../specs/epics/24-data-integrity/plan-24-06-disaster-recovery.md)

#### 24.7 — Integrity verification

- 📄 Story: [specs/epics/24-data-integrity/story-24-07-integrity-verification.md](../../specs/epics/24-data-integrity/story-24-07-integrity-verification.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-07-integrity-verification.md](../../specs/epics/24-data-integrity/plan-24-07-integrity-verification.md)
- 🗄 Tables: `audit_log`

#### 24.8 — Identity stability across operations

- 📄 Story: [specs/epics/24-data-integrity/story-24-08-identity-stability.md](../../specs/epics/24-data-integrity/story-24-08-identity-stability.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-08-identity-stability.md](../../specs/epics/24-data-integrity/plan-24-08-identity-stability.md)
- 🗄 Tables: `videos`

#### 24.9 — Forward and backward compatibility

- 📄 Story: [specs/epics/24-data-integrity/story-24-09-forward-back-compat.md](../../specs/epics/24-data-integrity/story-24-09-forward-back-compat.md)
- 🛠 Plan: [specs/epics/24-data-integrity/plan-24-09-forward-back-compat.md](../../specs/epics/24-data-integrity/plan-24-09-forward-back-compat.md)

