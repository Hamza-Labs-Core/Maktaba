# Architecture overview

This page is a short map. The canonical design, with data flow and the reasons behind
each choice, is
[specs/architecture.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/architecture.md).

## The services

A Maktaba home runs four services that share one Postgres database for state and one
media volume for the video files. Apps and the optional cloud talk to them.

| Service | Language | Role |
|---|---|---|
| API | Go | REST, GraphQL and WebSocket. Auth and roles, libraries, search orchestration, job control, settings, watch state. Signs stream URLs. |
| Streaming | Go | HLS and DASH origin. FFmpeg transcode and remux, range serving, subtitles, sprites, and the live TV channels. |
| Pipeline | Python | Scanner, probe, audio extraction, Whisper transcription (MLX, faster-whisper or OpenAI), embeddings, indexing, diarization. Drains the job queue. |
| Web | React, TypeScript, Vite | The web app (a PWA) and the in-app user guide at `/help`. The desktop server apps show it. |
| Native apps | Swift, Kotlin | Maktaba Player and Maktaba Remote (iPhone, iPad, Mac, Android), Maktaba TV (Apple TV, Android TV), and the Player for Windows and Linux. |
| Cloud relay (optional) | Go | Hosted layer for remote access: identity, server linking, a WSS-tunnelled HTTP relay and push fan-out. Lives in `cloud/`. |

`maktaba-server` bundles the API, streaming and the web UI into one binary for
installs; the desktop apps (Tauri) carry that server.

## One language per concern

Python does ML, Go does servers, TypeScript does UI. A new feature belongs to the
service that owns the relevant state; it does not spread across runtimes.

## The database

- PostgreSQL 16 with pgvector holds all state: libraries, videos, transcripts, the job
  queue, users and settings. Full-text search (`tsvector`) and semantic search
  (pgvector) run in the same database.
- SQLite is supported for single-user mode. Migrations ship as numbered SQL files
  under `shared/db/migrations/`, with Postgres and SQLite versions kept in step.
- Every service may read; only the owning service writes its own tables.

## How the services talk

- **API to pipeline:** gRPC for query-side work such as embedding a search query.
  Bulk job control goes through Postgres: the API inserts jobs, and pipeline workers
  claim them with `SELECT ... FOR UPDATE SKIP LOCKED`.
- **API to streaming:** gRPC for session lifecycle and capability negotiation. The API
  signs stream URLs; clients fetch bytes straight from streaming over HTTPS, so video
  traffic never passes through the API.
- **Events:** Postgres `LISTEN/NOTIFY` carries "job state changed", "new video ready"
  and similar events. The API turns them into WebSocket frames for clients. There is
  no separate message broker.
- **Files:** the pipeline owns the filesystem watcher. Streaming reads files only when
  a request asks for them. Generated sidecars (subtitles, thumbnails) live in a hidden
  `.maktaba/` folder next to each source file, so a library stays portable.
- **Clients:** the web app and native apps use the API (contracts in `shared/api/`:
  `openapi.yaml` and the GraphQL schema) and play from streaming.
- **Cloud:** a home links to a Maktaba Cloud account and keeps an outbound tunnel to the
  relay, so apps can reach it away from home without opening ports.

## Design principles

- **Content-addressed identity.** A video is identified by a BLAKE3 hash of its content,
  so renames and moves never cause reprocessing.
- **Idempotent, resumable jobs.** Every pipeline stage can be re-run safely; outputs are
  written to a temp path and renamed atomically.
- **Modular stages.** Each pipeline stage has a small interface and talks only through
  the job store, so replacing the speech-to-text engine touches nothing else.
- **Graceful degradation.** If the pipeline is down, the library still browses and
  streams; if streaming is down, browse and search still work.

## Where to read next

- [Repository layout](repo-layout.md) to find the code for each service.
- [Local setup](local-setup.md) to run it.
- The epics under
  [specs/epics](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/specs/epics) for the
  design of each feature.
