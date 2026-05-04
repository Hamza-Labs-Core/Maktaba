# Maktaba — Architecture

> Self-hosted media intelligence platform. Plex-class library, streaming, and
> apps for every screen — plus "every word in every video is searchable."
> Arabic-first, language-agnostic.

---

## 1. System Overview

### 1.1 What Maktaba is

Maktaba is a self-hosted media platform that turns a folder tree of video files
(lectures, sermons, interviews, archives, films) into a searchable, streamable,
intelligently-organized library, watchable on every screen the household owns.
It is positioned as a **full Plex alternative** — library, transcoding origin,
and first-party apps for web, mobile, desktop, and TV — and its differentiator
on top of that is **content intelligence**: every video is transcribed,
segmented, indexed for full-text and semantic search, and the user can jump
to the exact second any phrase was spoken from any client.

The target deployment is a single user / single household with 30 TB+ of
content on a NAS or workstation, with the option to scale horizontally to
multi-user installations later. The first-class language is Arabic
(مكتبة = "library"), with full Unicode and bidirectional-text correctness
throughout, but the STT and indexing layers are language-agnostic.

### 1.2 Top-level architecture

Three backend services and a unified client surface, sharing one Postgres
for state and one media volume for bytes:

```
   ┌───────────────────────────────────────────────────────────────────────┐
   │                            Client surface                             │
   │   Web (PWA · React+Vite)   ·   iOS / Android (Capacitor wrappers)     │
   │   Desktop (Tauri)          ·   tvOS (Swift) · Android TV (Kotlin)     │
   └────────────────┬─────────────────────────┬────────────────────────────┘
                    │ HTTPS / WSS             │ HLS / DASH (HTTPS, range)
                    ▼                         ▼
   ┌────────────────────────────────┐   ┌──────────────────────────────────┐
   │     API Service  (Go 1.23)     │   │     Streaming Service  (Go)      │
   │  REST + GraphQL + WebSocket    │◄─►│  HLS / DASH origin               │
   │  Auth · libraries · search     │gRPC│  on-the-fly transcode + remux   │
   │  job orchestration · settings  │   │  subtitle mux · range serving    │
   │  watch progress · collections  │   │  thumbnail / sprite generation   │
   └─────┬───────────────────┬──────┘   └──────────┬───────────────────────┘
         │ gRPC              │                     │
         │                   │ Postgres            │ Postgres + media volume
         ▼                   ▼                     ▼
   ┌──────────────────┐  ┌────────────────────────────────────────────────┐
   │ Pipeline Service │  │            PostgreSQL 16 (or SQLite)           │
   │  (Python 3.12)   │◄─┤   metadata · jobs · FTS · LISTEN/NOTIFY pub/sub │
   │  STT · embed.    │  └────────────────────────────────────────────────┘
   │  Chroma · diariz.│                              ▲
   │  worker pool     │                              │
   └────┬─────────────┘                              │
        │                                            │
        ▼                                            │
   ┌────────────────────┐                            │
   │  ChromaDB on disk  │                            │
   │  HF model cache    │────────────────────────────┘
   │  watchdog → roots  │
   └────────────────────┘
```

Each backend service owns one concern:

- **API Service (Go)** — every request that isn't a media byte. Auth, library
  CRUD, search orchestration (FTS + Chroma fan-in), job control, settings,
  watch state, real-time WebSocket progress. Stateless behind Postgres.
- **Streaming Service (Go)** — every media byte. HLS and DASH manifests,
  on-the-fly transcoding/remuxing via FFmpeg subprocesses, range-request
  direct play, subtitle muxing, sprite/poster cache. Stateless across
  requests; coordinates only via Postgres for session pinning.
- **Pipeline Service (Python)** — every ML/AI byte. Whisper STT (MLX / CUDA /
  CPU / API), multilingual embeddings, ChromaDB indexing, diarization, the
  filesystem watcher. Owns the worker pool that drains the job queue.

The client surface is a single React PWA wrapped natively for mobile via
Capacitor and for desktop via Tauri, with native Swift / Kotlin only for the
two TV platforms where browser APIs aren't enough (§6).

### 1.3 Why this language split

The mandate is "one person ships a Plex-class platform with content
intelligence." That ranks **time-to-market and operational simplicity above
raw performance** at every layer where they conflict. The split below is the
minimum number of languages that gets each layer to production grade without
paying a velocity tax.

| Layer                         | Language                | Why this and not the alternatives                                                                                                                                                                                                                                                                                                                                                                                                                          |
|-------------------------------|-------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| ML / AI pipeline              | **Python 3.12+**        | Whisper (incl. `mlx-whisper` and `faster-whisper`), ChromaDB, sentence-transformers, pyannote, Hugging Face — everything we depend on is Python-native. Calling them via a remote runtime from Rust/Go would buy nothing and cost the iteration speed ML work demands.                                                                                                                                                                                      |
| API + business logic          | **Go 1.23+**            | ASP.NET Core (C#) and Spring (Java) are battle-tested for this layer, but adding a third runtime alongside Python and the streaming server's runtime is operational tax for a one-person project. Go gives strong typing, first-class HTTP/2, first-class gRPC, generics that are sufficient for the domain, and single-binary deployment. The deciding factor: the streaming server (next row) is also Go, and we want shared models, middleware, and FFmpeg wrappers between them. |
| Streaming origin              | **Go 1.23+**            | The hot path is FFmpeg-as-subprocess plus byte-pumping. Both Rust and Go are dramatically faster than the FFmpeg work itself, so the framing layer is not the bottleneck. Rust would offer marginal CPU/memory wins and stronger zero-copy primitives, but at the cost of a second backend language. Go's `net/http`, `io.Copy`, and goroutine-per-connection model handle thousands of concurrent range requests trivially. We pick Go and put **API and Streaming in the same monorepo** — sharing `internal/auth`, `internal/db`, `internal/ffmpeg`, `internal/telemetry` — but ship them as **two independent binaries** so streaming can scale separately and API restarts don't drop watch sessions. |
| Web client                    | **TypeScript / React + Vite** | PWA-quality so the same codebase wraps into mobile and desktop. RTL- and bidi-correct out of the box; mature ecosystem (Video.js / Vidstack) for HLS playback and caption rendering.                                                                                                                                                                                                                                                                                  |
| Mobile wrappers (iOS/Android) | **Capacitor** over the web app | Reuses the React codebase; native plugin layer for background download, file association, push notifications, native player handoff. One UI to maintain instead of two.                                                                                                                                                                                                                                                                                                |
| Desktop wrapper (mac/Win/Linux) | **Tauri** over the web app | Native window chrome, file associations, system tray, OS-level codec hardware paths via the embedded WebView. ~10 MB binaries vs. Electron's ~120 MB. Same React codebase.                                                                                                                                                                                                                                                                                                |
| TV apps (tvOS / Android TV)   | **Swift / SwiftUI** + **Kotlin / Jetpack Compose** | Both TV platforms have a broken-or-absent PWA story. Remote-control input, focus engine, and 4K HDR codec hardware paths warrant native. These are the only places we eat the cost of a separate UI codebase, and only because nothing else works.                                                                                                                                                                                                                |

**Explicitly rejected:**

- **C# (.NET) for the API.** ASP.NET Core is excellent and EF Core is the
  best ORM in this matrix, but adding the .NET runtime alongside Python and
  Go means three deployment toolchains, three test runners, three dependency
  graphs, three Dockerfiles. The marginal gain over Go for our specific
  business logic (CRUD, auth, search orchestration, WebSocket fan-out) does
  not justify the operational cost.
- **Rust for the streaming server.** Real wins (lower latency, lower memory,
  better safety), but FFmpeg subprocess time dominates the wall-clock and
  `ffmpeg-next` is a thinner ecosystem than Go's `os/exec` plus a clean byte
  pipeline. Reconsider in v2 if Go's GC or per-connection memory becomes a
  measurable problem; not in v1.
- **Splitting API and Streaming into two repos.** Same language, mostly
  shared models. One monorepo, two `cmd/` entry points, shared `internal/`.
  Two binaries at deploy time, one codebase at dev time.
- **React Native or Flutter for mobile.** A second UI codebase that has to
  track the web one. Capacitor over the same React app is the
  one-person-friendly bet.
- **Electron for desktop.** Tauri's WebView-based model is dramatically
  smaller, faster to launch, and uses the OS's hardware video pipeline.

### 1.4 Service topology & communication

There are exactly four state-bearing surfaces in the system:

| Surface                | Owner                          | Other services' access                        |
|------------------------|--------------------------------|-----------------------------------------------|
| PostgreSQL             | shared (with strict ownership per table set) | All services read; only the owning service writes its tables. |
| Media volume (read)    | shared (filesystem)            | Streaming and Pipeline read; nobody writes.    |
| Cache volume (HLS, thumbs, sprites) | Streaming Service | API reads URLs from Postgres; Pipeline doesn't touch. |
| ChromaDB on disk       | Pipeline Service               | API queries via gRPC, never directly on disk. |

**Inter-service contracts:**

- **API ↔ Pipeline** — gRPC, mostly query-side. The API calls Pipeline for
  query-time embedding (`Embed(text) → vector`), search-side reranking, and
  ad-hoc operations like "transcribe this clip with backend X right now."
  Bulk job control flows through Postgres, not gRPC: the API enqueues jobs
  by INSERT and Pipeline workers claim them with `SELECT … FOR UPDATE SKIP
  LOCKED` (§7). One DB transaction, full visibility, no extra service.
- **API ↔ Streaming** — gRPC for session lifecycle (`OpenSession`,
  `CloseSession`, `EvictHashCache`) and capability negotiation. Stream URLs
  themselves are signed by the API and consumed directly by clients against
  Streaming over plain HTTPS, so byte traffic never round-trips the API.
- **Postgres LISTEN/NOTIFY** — pub/sub for "job state changed", "new video
  ready", "subtitle index updated". The API translates these into
  per-client WebSocket frames. No Redis, no Kafka, no NATS — the queue
  Maktaba already runs is the bus Maktaba already runs.
- **Filesystem events** — the Pipeline Service owns the watcher; the
  Streaming Service receives no FS events and only reads files when a
  manifest request asks it to.

We deliberately do **not** introduce a separate message broker (Redis,
RabbitMQ, NATS). For a 30 TB / single-household deployment, Postgres
LISTEN/NOTIFY plus the existing `processing_jobs` table is sufficient
through the lifetime of v1 and most of v2. A broker is added the day a
measurable component contention demands it, not before.

### 1.5 Design principles

- **One language per concern, no overlap.** Python does ML, Go does servers,
  TypeScript does UI. A new feature does not shop across runtimes; it
  belongs to whichever service owns the relevant state.
- **Modular pipeline stages.** Each Python pipeline stage implements a small
  interface (`run(item) -> result`) and communicates only through the job
  store. Replacing the STT engine does not touch the scanner, the indexer,
  the API, or the UI.
- **Content-addressable identity.** A video is identified by its
  `content_hash` (BLAKE3 of the first + last 4 MiB plus file size). Renaming,
  moving, or copying a file does not cause re-processing on either Pipeline
  (no redundant transcribe) or Streaming (cache stays warm).
- **Idempotent, resumable jobs.** Every pipeline stage can be re-run safely.
  A crash during transcription leaves no partial subtitle file in the
  library — outputs are written to a temp path and atomically renamed.
- **Sidecar outputs.** Generated artifacts (`.srt`, `.vtt`, segment JSON,
  thumbnails) live next to the source file in a hidden `.maktaba/`
  directory, so the library remains portable and a Plex/VLC user could still
  consume the subtitles directly.
- **Graceful degradation across services.** Pipeline down? Library still
  browses and streams; only new ingest stops. Streaming down? Library still
  browses and search still works; only playback breaks. API down? Streaming
  honors signed URLs already in flight. No single component failure should
  break the whole platform.

---

## 2. Tech Stack

### 2.1 By service

**API Service (Go 1.23+):**

| Concern            | Choice                                  | Rationale                                                                       |
|--------------------|-----------------------------------------|---------------------------------------------------------------------------------|
| HTTP framework     | `chi` + `net/http`                       | Stdlib-first; battle-tested router; trivial middleware composition.             |
| GraphQL            | `gqlgen` (schema-first)                  | Code-gen from `.graphql`; type-safe resolvers; subscriptions via WebSocket.     |
| ORM / SQL          | `sqlc` + `pgx/v5`                        | Generated typed Go from raw SQL — keeps the schema DDL canonical, no ORM magic. |
| Migrations         | `goose` (or `atlas`)                     | Embedded, single binary; runs at boot or via `maktaba migrate`.                 |
| Auth               | JWT (RS256) + cookie sessions; argon2id passwords | Mobile/TV clients use bearer tokens; web uses httpOnly cookies + CSRF.    |
| Validation         | `go-playground/validator/v10`            | Struct-tag-driven request validation.                                           |
| WebSocket          | `coder/websocket` (formerly nhooyr)      | Modern, context-aware, no goroutine-per-connection internals.                   |
| gRPC client        | `google.golang.org/grpc`                 | Talks to Pipeline (embeddings, ad-hoc transcribe) and Streaming (sessions).     |
| Background tasks   | Native goroutines + Postgres LISTEN      | No external scheduler; the API is mostly request-response.                      |
| Config             | `viper` + env overrides                  | Layered (defaults → TOML → env → flags).                                        |
| Logging            | `slog` (stdlib) + JSON handler           | Stdlib structured logging; grep-friendly; OTel-bridge available.                |
| Telemetry          | OpenTelemetry SDK (opt-in)               | Traces flow across services via gRPC/HTTP propagators.                          |

**Streaming Service (Go 1.23+):**

| Concern            | Choice                                  | Rationale                                                                       |
|--------------------|-----------------------------------------|---------------------------------------------------------------------------------|
| HTTP framework     | `chi` + `net/http`                       | Same as API; shared `internal/http` middleware (logging, recovery, signed-URL). |
| Range serving      | `http.ServeContent` + custom HEAD path   | Conditional GETs, `Accept-Ranges`, byte ranges; Safari-correct.                 |
| HLS / DASH         | FFmpeg subprocess (HLS muxer + dashenc)  | Battle-tested manifest generation; we orchestrate, FFmpeg muxes.                |
| Transcode pool     | Per-host semaphore + per-session FFmpeg  | Capped concurrency; graceful eviction under pressure.                           |
| Probe cache        | LRU in-memory + Postgres `media_info`    | Avoid re-probing on every manifest request.                                     |
| Subtitle muxing    | Native VTT writer + FFmpeg passthrough   | Generated subs join HLS via `#EXT-X-MEDIA:TYPE=SUBTITLES`.                      |
| Image generation   | `disintegration/imaging` + FFmpeg        | Posters, sprite sheets, chapter thumbs.                                         |
| Cache GC           | LRU on disk (default 50 GiB cap)          | Bounded; survives restarts.                                                     |
| Config             | `viper` (shared `internal/config`)       | Same loader as API; `[streaming]` section.                                      |

**Pipeline Service (Python 3.12+):**

| Concern            | Choice                                       | Rationale                                                                 |
|--------------------|----------------------------------------------|---------------------------------------------------------------------------|
| Async runtime      | `asyncio` + `anyio`                          | Native to Python 3.12; required for streaming STT.                        |
| gRPC server        | `grpc.aio`                                   | Async server; one process per worker box.                                 |
| Worker loop        | Custom claim loop on Postgres                | Same `SELECT … FOR UPDATE SKIP LOCKED` queue the API enqueues into.       |
| DB access          | `asyncpg` (Postgres) / `aiosqlite` (SQLite)  | No ORM; thin SQL layer mirrors the Go side's `sqlc` queries.              |
| Domain models      | Pydantic v2                                  | Validation at service boundaries; serialize to gRPC via `betterproto`.    |
| STT engines        | `mlx-whisper` · `faster-whisper` · `openai`  | One per backend; chosen at runtime per library.                           |
| Embeddings         | `sentence-transformers` (`multilingual-e5-large`) | Strong on Arabic and English, sentence-level.                        |
| Vector store       | ChromaDB (persistent client, DuckDB+Parquet) | On-disk, embedded, no extra service.                                      |
| Diarization        | `pyannote.audio` (opt-in)                    | Heavyweight; off by default, on per library.                              |
| Filesystem watch   | `watchdog`                                   | Cross-platform inotify / FSEvents / ReadDirectoryChangesW.                |
| Media probe        | `ffmpeg-python` for ffprobe                  | Canonical metadata source; output written to Postgres.                    |
| Packaging          | `uv` + `pyproject.toml`                      | Fast resolver; matches existing skeleton.                                 |
| Logging            | `structlog` → JSON                           | Same shape as Go side; one log pipeline.                                  |

**Shared infrastructure:**

| Concern            | Choice                                       | Rationale                                                                 |
|--------------------|----------------------------------------------|---------------------------------------------------------------------------|
| Metadata DB        | PostgreSQL 16 (prod) · SQLite (dev/single-user) | One source of truth; sqlc-generated Go and asyncpg Python share the schema. |
| Full-text search   | Postgres `tsvector` (multi-user) · SQLite FTS5 (single-user) | Strong Arabic support via `unicode61 remove_diacritics 2`.        |
| Pub/sub            | Postgres `LISTEN/NOTIFY`                     | One transaction, one bus; no Redis/NATS/Kafka in v1.                      |
| IPC schemas        | Protobuf 3 + gRPC                            | One `.proto` source generates Go and Python clients.                      |
| Container runtime  | Docker + Compose                              | Reproducible single-host deploy; one `docker-compose.yml` brings it all up.|
| Reverse proxy / TLS| Caddy (default) · nginx (optional)            | Caddy auto-issues certs and routes `/api`, `/stream`, `/` cleanly.        |

**Web / Mobile / Desktop:**

| Concern            | Choice                                       | Rationale                                                                 |
|--------------------|----------------------------------------------|---------------------------------------------------------------------------|
| Web framework      | React 18 + TypeScript + Vite + Tailwind      | Mature; RTL-friendly with `dir="rtl"`.                                    |
| Router / data      | TanStack Router + TanStack Query             | Type-safe routes; cache + invalidation that matches the WS-driven UI.     |
| Player             | Vidstack (or Video.js fallback)              | HLS + DASH, sidecar VTT, captions, chapter markers, mobile-friendly.      |
| State              | Zustand (UI state) + TanStack Query (server) | No Redux ceremony; small surface.                                         |
| GraphQL client     | `graphql-request` + codegen                  | Lightweight; types generated from server schema.                          |
| PWA shell          | `vite-plugin-pwa` + Workbox                  | Background sync, offline metadata, installable on iOS/Android.            |
| Mobile wrapper     | Capacitor 6                                  | Native shell over the web app; native player handoff plugin.              |
| Desktop wrapper    | Tauri 2                                      | ~10 MB binary, native menus, file association, system tray.               |
| TV: tvOS           | Swift / SwiftUI + AVPlayer                   | Native focus engine, AVPlayer for HLS, top-shelf integration.             |
| TV: Android TV     | Kotlin + Jetpack Compose for TV + ExoPlayer  | Native focus, ExoPlayer for adaptive streaming, Leanback row APIs.        |

### 2.2 Why these defaults

- **Why not Celery / Redis / Kafka?** A 30 TB single-household library has
  dozens of jobs in flight, not millions. A Postgres-backed queue with
  `SELECT … FOR UPDATE SKIP LOCKED` gives atomic claim, full visibility
  through the same DB the UI already reads, and one fewer service to run.
- **Why not an ORM in Go?** `sqlc` reads the same SQL DDL the Python side
  reads and generates typed Go without runtime reflection. The schema
  remains canonical; neither runtime can drift from it silently.
- **Why GraphQL alongside REST?** REST is the boring high-cacheability
  surface for streaming/manifest URLs and webhooks. GraphQL is the
  composable surface for client-driven views (a TV "row" needs a different
  shape than a phone "list"). One server (`gqlgen`) emits both from shared
  resolvers.
- **Why JWT + cookies?** Web sets httpOnly secure cookies. Mobile/TV apps
  store a refresh token in Keychain/Keystore and present a short-lived
  bearer JWT to both the API and Streaming. Streaming validates JWTs
  offline against the API's RS256 public key — the API doesn't need to be
  reachable for an in-flight watch session to keep playing.

---

## 3. Pipeline Service

The Pipeline Service is the Python backend. It owns every ML/AI byte: STT,
embeddings, ChromaDB, diarization, the filesystem watcher, and the worker
pool that drains the job queue. It exposes a small gRPC surface for the API
Service (query-time embedding, ad-hoc transcribe, model-info) but its main
loop is the queue claim — bulk work is enqueued by the API as Postgres
INSERTs (§7) and consumed here without an extra hop.

Pipeline stages are implemented as classes that conform to a small `Stage`
protocol:

```python
class Stage[I, O](Protocol):
    name: str                           # "scan", "probe", "transcribe", ...
    input_state: VideoState             # what state the video must be in
    output_state: VideoState            # what state to advance it to
    def run(self, ctx: StageContext, item: I) -> O: ...
```

Stages do **not** call each other. They read/write through the job store,
which advances `videos.state` along a finite-state machine. State names
are **lowercase** in SQL (`videos.state TEXT`); diagrams show uppercase
purely for visual emphasis.

```
discovered → probed → audio_extracted → transcribed → subtitle_gen → indexed → thumbnailed → ready
                ↘ failed (per-stage, with retry counter and backoff)

Auxiliary terminal states (set by sweeps and dedup, not the linear pipeline):
    ready_no_audio  — file has no audio stream; pipeline short-circuits past STT
    missing         — file removed/unreachable; sweep flips state, row kept for audit
    superseded      — replaced by another `content_hash`; row kept for audit, hidden in lists
    corrupted       — probe failed irrecoverably; surfaced in admin UI
```

The seven canonical pipeline **stages** are `scan`, `probe`, `extract`,
`transcribe`, `subtitle_gen`, `index`, `thumbnail` — these are the strings
written to `processing_jobs.stage`. `subtitle_gen` runs after `transcribe`
and emits the SRT/VTT sidecar files; it is a separate stage so the
queue/UI can surface its progress independently.

The orchestrator picks the next eligible stage for each video by joining
`videos` against `processing_jobs`. This makes "where is video X stuck?" a
trivial SQL query.

### 3.1 Scanner

**Trigger:** `watchdog` filesystem event, or periodic full sweep
(default every 6 h), or manual `POST /api/libraries/{id}/scan`.

**Inputs:** library root path, ignore globs, supported extensions.

**Outputs:** rows in `videos` for newly-seen files, in state `discovered`.

**Notes:**
- Skip files that match an existing `content_hash` even at a new path —
  treat as a move/rename and update `videos.path`.
- Hash computation uses BLAKE3 over the first 4 MiB + last 4 MiB + size; full
  hashing 30 TB is infeasible and unnecessary for identity.
- Hidden files, partial downloads (`*.part`, `*.crdownload`), and `.maktaba/`
  sidecar directories are ignored.

### 3.2 Probe

**Inputs:** `videos` row in state `DISCOVERED`.

**Outputs:** populated `media_info` row (duration, container, video codec,
audio codecs and languages, resolution, bitrate, fps, has_subtitles).
Advances state to `PROBED`.

**Implementation:** `ffprobe -v quiet -print_format json -show_streams
-show_format`.

### 3.3 Audio Extractor

**Inputs:** `videos` row in state `PROBED`, audio track selection.

**Outputs:** zero-or-more `audio_tracks` rows; an extracted mono 16 kHz WAV
streamed directly into the transcriber via a pipe (no intermediate file
unless the STT backend requires one).

**Track selection rules:**
1. If the user has pinned a preferred audio language in library settings, use
   the matching track.
2. Else, prefer the track tagged `ara` (Arabic).
3. Else, the first track.
4. Multiple tracks can be transcribed if the library is flagged
   `multi_audio = true` (rare; opt-in).

**Implementation:** `ffmpeg -i {file} -map 0:a:{idx} -ac 1 -ar 16000 -f wav -`.

### 3.4 Transcriber (pluggable STT)

The transcriber is the **only** stage with multiple swappable
implementations. All implementations conform to:

```python
class STTBackend(Protocol):
    name: str                       # "whisper-mlx", "openai-api", "gemma-audio"
    supports_streaming: bool
    cost_per_minute: float | None   # for budgeting
    def transcribe(
        self,
        audio: AudioSource,         # path, file-like, or async iterator of PCM
        language: str | None,       # ISO 639-1, None = auto-detect
        hints: TranscriptionHints,  # initial prompt, vocabulary, speaker count
    ) -> TranscriptionResult:       # segments with start/end/text/confidence
        ...
```

**Built-in backends:**

| Backend          | Library              | Use case                             | Latency      |
|------------------|----------------------|--------------------------------------|--------------|
| `whisper-mlx`    | `mlx-whisper`        | Default on Apple Silicon. Fast, free.| ~0.3× RT     |
| `whisper-cpu`    | `openai-whisper`     | Fallback / Linux without GPU.        | ~3× RT       |
| `whisper-cuda`   | `faster-whisper`     | Linux with NVIDIA GPU.               | ~0.1× RT     |
| `openai-api`     | `openai`             | When local hardware is unavailable.  | network-bound|
| `gemma-audio`    | (future)             | Reserved for Gemma audio releases.   | TBD          |

The active backend is chosen per-library (so the user can use the API for
priority items and local Whisper for the bulk archive).

**Output schema (canonical, regardless of backend):**

```python
class Segment(BaseModel):
    index: int
    start: float        # seconds
    end: float          # seconds
    text: str           # normalized; bidi-safe
    speaker: str | None # if diarization is on
    confidence: float | None
    words: list[Word] | None  # if word-level timestamps available
```

### 3.5 Subtitle Generator

**Inputs:** segments from the transcriber.

**Outputs:** `{video_basename}.srt` and `{video_basename}.vtt` written to the
sidecar directory `.maktaba/subs/`, plus a copy at
`{video_basename}.{lang}.srt` next to the source file (so external players
auto-discover it). Both are produced from the same segments array — never
parsed back from disk.

**Formatting rules:**
- Maximum 42 characters per line, max 2 lines per cue (configurable).
- Sentence-aware splitting; never break mid-word.
- Arabic punctuation (`،`, `؟`, `؛`) preferred over Latin equivalents when
  the source language is `ar`.
- VTT cues include speaker tags (`<v Speaker 1>...`) when diarization ran.

### 3.6 Indexer

**Inputs:** segments + video metadata.

**Outputs:** rows in `transcript_segments`, `transcripts_fts` (FTS5 virtual
table), and a ChromaDB collection per library.

**Two indexes, two purposes:**
- **FTS5** — exact-phrase, prefix, and proximity queries. Used for "find
  every video where the speaker says الحمد لله". Cheap, deterministic.
- **ChromaDB** — semantic queries ("videos about gratitude in prayer"),
  cross-language retrieval ("English query, Arabic videos"), and "more like
  this" expansions.

A segment is split into "search units" of 1–3 sentences (target ~200
characters) before embedding, with original segment offsets preserved so a
hit always resolves back to a precise timestamp.

### 3.7 Search Engine

**Hybrid retrieval:**

```
                  ┌──────────────────────────────────────┐
                  │              search query            │
                  └──────────────────────────────────────┘
                                    │
                 ┌──────────────────┴──────────────────┐
                 ▼                                     ▼
        ┌────────────────┐                    ┌─────────────────┐
        │ FTS5 lookup    │                    │ Chroma top-K    │
        │ (BM25 ranked)  │                    │ (cosine sim.)   │
        └────────┬───────┘                    └────────┬────────┘
                 ▼                                     ▼
                 └──────────────┬──────────────────────┘
                                ▼
                  ┌──────────────────────────────┐
                  │  Reciprocal Rank Fusion      │
                  │  + filters (lang, library,   │
                  │    date, duration, speaker)  │
                  └──────────────┬───────────────┘
                                 ▼
                  ┌──────────────────────────────┐
                  │  highlight & timestamp links │
                  └──────────────────────────────┘
```

Default weight is 0.5 BM25 / 0.5 semantic; advanced users can tune via the
search settings panel. RRF avoids score-scale incompatibility.

---

## 4. Streaming Service

The Streaming Service is the Plex-class media origin: it speaks HLS and DASH,
serves direct-play byte ranges, transcodes or remuxes on the fly when the
client can't play the source, multiplexes subtitle tracks, generates posters
and sprite sheets, and supports session-pinned adaptive playback. It is
written in Go, deployed as its own binary, and shares only Postgres and the
read-only media volume with the rest of the system.

It does **not** touch the video catalog: it is told "stream `video_id`" via
a signed URL minted by the API, looks up the file path and probe metadata in
Postgres, and from there onward only reads bytes. This means the Streaming
binary can be scaled, restarted, or pinned to a specific NIC/host independent
of the API.

### 4.1 Playback modes

In order of preference per request:

1. **Direct play.** If the file's container, video codec, audio codec, and
   profile are on the client's reported capability list, stream the file
   with HTTP range requests (`206 Partial Content`). Zero transcoding,
   zero remuxing, zero CPU.
2. **Direct stream (remux only).** If the codecs are compatible but the
   container is wrong (MKV → MP4 fragmented for browsers, AVI → MP4 for
   AppleTV), FFmpeg copies the streams (`-c copy`) into the target
   container. Low CPU, no quality loss.
3. **HLS / DASH adaptive transcode.** Fallback for HEVC, AV1, VP9-on-Safari,
   uncommon audio codecs (AC3, DTS), or constrained-bandwidth clients.
   FFmpeg encodes to a ladder of H.264+AAC renditions; the manifest
   advertises the rungs and the player picks dynamically. CPU-expensive;
   per-host concurrency cap.

The decision is made by a small **capability matrix** maintained per client
profile (browser UA, iOS native, Android native, tvOS, AndroidTV) and
overridable per session ("force HLS 720p" for a user on a slow link). The
Streaming Service does not trust the client to volunteer capabilities for
free — the API tells it during `OpenSession` what the client is.

### 4.2 Session model

A **streaming session** ties a client to a video for the duration of a watch:

```
client → API:  POST /api/stream/sessions {video_id, client_profile, audio_track?, subtitle_track?, start_sec?}
API → Streaming (gRPC): OpenSession(...) → returns session_id, manifest_url, ttl
API → client:  {session_id, manifest_url (signed JWT URL), expires_at}
client → Streaming (HLS/DASH): GET {manifest_url}
```

Why sessions:

- **Sticky transcoder.** Adaptive switching needs the same FFmpeg subprocess
  per session so segment numbering stays monotonic. The session id pins the
  worker.
- **Concurrency accounting.** The Streaming Service knows exactly how many
  active transcodes it owns and refuses new ones above the per-host cap,
  rather than letting CPU thrash.
- **Watch-progress reporting.** Clients POST progress to
  `/api/stream/sessions/{id}/progress`; the API persists it to
  `playback_state`. WebSocket fanouts let other devices show "you watched
  to 23:14 on your phone."
- **Bandwidth caps.** A session can be capped (`max_bitrate_kbps`) for
  cellular users or per-user quotas.
- **Clean teardown.** On `CloseSession`, the FFmpeg subprocess is killed,
  the per-session HLS segments outside the rolling window are GC'd, and
  the slot is released.

Sessions live in `streaming_sessions` (Postgres). Stale sessions (no segment
fetch in 90 s) are reaped every 30 s.

### 4.3 HLS manifest

```
#EXTM3U
#EXT-X-VERSION:7
#EXT-X-INDEPENDENT-SEGMENTS

#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud-default",NAME="Arabic",LANGUAGE="ar",DEFAULT=YES,AUTOSELECT=YES,URI="audio/ar/index.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud-default",NAME="English",LANGUAGE="en",AUTOSELECT=YES,URI="audio/en/index.m3u8"

#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Arabic (auto)",LANGUAGE="ar",DEFAULT=YES,AUTOSELECT=YES,FORCED=NO,URI="subs/ar.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",AUTOSELECT=NO,FORCED=NO,URI="subs/en.m3u8"

#EXT-X-STREAM-INF:BANDWIDTH=4500000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud-default",SUBTITLES="subs"
1080p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2500000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud-default",SUBTITLES="subs"
720p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=854x480,CODECS="avc1.42c01e,mp4a.40.2",AUDIO="aud-default",SUBTITLES="subs"
480p/index.m3u8
```

For DASH the equivalent MPD is generated; both are produced by the same
FFmpeg invocation when transcoding (`-f hls` + `-f dash` to separate paths
sharing one encode is not supported, so DASH is opt-in per session).

### 4.4 Transcode pipeline

A single FFmpeg subprocess per session writes:
- `1080p/seg-N.ts`, `720p/seg-N.ts`, `480p/seg-N.ts`
- `audio/{lang}/seg-N.aac` per selected language
- `subs/{lang}/seg-N.vtt` per selected subtitle (generated from
  `transcript_segments` for auto-generated subs; remuxed from sidecar SRT
  for external ones)

```go
ffmpeg -ss {start_sec} -i {input} \
  -map 0:v:0 -filter:v "scale=-2:1080" -c:v libx264 -preset veryfast -crf 22 \
  -map 0:v:0 -filter:v "scale=-2:720"  -c:v libx264 -preset veryfast -crf 23 \
  -map 0:v:0 -filter:v "scale=-2:480"  -c:v libx264 -preset veryfast -crf 24 \
  -map 0:a:0 -c:a aac -b:a 128k -ac 2 \
  -f hls -hls_time 4 -hls_list_size 6 -hls_flags independent_segments+delete_segments \
  -hls_segment_filename "{session_dir}/v%v/seg-%d.ts" -master_pl_name index.m3u8 \
  -var_stream_map "v:0,a:0,name:1080p v:1,a:0,name:720p v:2,a:0,name:480p" \
  {session_dir}/v%v/index.m3u8
```

Hardware acceleration is auto-detected: VideoToolbox on Apple Silicon
(`-hwaccel videotoolbox -c:v h264_videotoolbox`), NVENC on NVIDIA
(`h264_nvenc`), QuickSync on Intel (`h264_qsv`). Falls back to libx264 on
unknown hardware.

### 4.5 Subtitle handling

Three sources, all exposed as VTT to the player:

1. **Auto-generated** (from `transcript_segments`). Rendered live by the
   Streaming Service from the DB — never read from a `.vtt` file. This
   means the user sees subtitles as soon as the first segments are
   indexed, even before transcription is fully complete.
2. **Sidecar SRT/VTT** in the library folder (`{name}.{lang}.srt`).
   Auto-discovered by the Pipeline Service during scan; converted to VTT
   on first request and cached.
3. **Embedded** in the container (MKV `S_TEXT/UTF8` etc.). Extracted on
   first request via `ffmpeg -map 0:s:N -c:s webvtt`.

Subtitles are **never burned in.** The player handles rendering. Clients
that don't support sidecar subtitles (rare) can request burned-in mode per
session, which forces a transcode.

### 4.6 Chapters

Three sources, picked in order:

1. Embedded chapters from the container (`ffprobe -show_chapters`).
2. Chapters defined manually by the user via the API.
3. Inferred chapters from transcript-level topic shifts (cosine drop between
   adjacent segment embeddings > threshold), capped at one chapter per
   ~3 minutes of content.

Stored in `chapters`; served as part of the manifest's
`#EXT-X-DATERANGE:CLASS="chapter"` markers (HLS) or as a sidecar
`chapters.json` resource referenced from the API.

### 4.7 Watch progress sync

The player POSTs `{position_sec, completed?}` to the API every 10 s and on
pause/seek. The API writes to `playback_state (user_id, video_id)` and
fans out to other sessions over `WS /ws/playback/{video_id}`, so:

- A user starting a video on their phone sees "Resume at 23:14" on their
  TV the moment they pick it up.
- "Watched" state (`completed = true` once `position_sec / duration > 0.95`)
  is synced across clients in real time.
- Per-client "next up" computations stay consistent.

### 4.8 Cache layout

```
/var/maktaba/cache/streaming/
├── direct/                      # transient buffers for direct-play range serves
├── remux/{hash[:2]}/{hash}/     # short-lived remuxed MP4s (LRU)
├── hls/{session_id}/            # per-session live HLS segments
├── posters/{hash[:2]}/{hash}.jpg
├── sprites/{hash[:2]}/{hash}.{webp,vtt}
└── thumbs/{hash[:2]}/{hash}/chapter-{n}.jpg
```

`hls/{session_id}/` is purged on `CloseSession` or session reap.
`remux/`, `posters/`, `sprites/`, `thumbs/` are LRU-capped (default 50 GiB
combined). Posters and sprites are pre-generated by the Pipeline Service at
the `thumbnail` stage and reused indefinitely; the Streaming Service serves
them as static files.

### 4.9 Thumbnails and previews

Generated at the `thumbnail` pipeline stage (Pipeline Service), stored in the
shared cache:

- One **poster** per video (auto-selected at 10% of duration, ignoring black
  frames via `blackdetect`; user can override).
- One **sprite sheet** of preview thumbs at 10-second intervals (WebP), with
  a sidecar VTT mapping time → sprite cell. Used by the player for scrub
  preview.
- Optional **chapter posters**, one per detected chapter.

The Streaming Service serves these as plain HTTP — no transformation, no
session.

---

## 5. Library Management

A **library** is a named collection of root paths sharing a configuration
profile. Libraries are first-class to support setups like:

- `Lectures` — `/mnt/media/lectures/`, language=ar, STT=whisper-mlx, large model.
- `Films` — `/mnt/media/films/`, language=en, STT=whisper-cpu, tiny model.
- `Archive` — `/mnt/cold/archive/`, scan-only, no STT.

### 5.1 Folder watching

The Pipeline Service owns the watcher; the API and Streaming services
receive no filesystem events. Each library spawns one `watchdog` observer.
Events are debounced (default 2 s) so that copies in progress are not
picked up mid-write. A file is considered settled when its size has not
changed for one debounce interval. New `videos` rows trigger a Postgres
NOTIFY (`channel = "videos.new"`); the API listens and pushes WebSocket
updates to subscribed clients.

### 5.2 Auto-categorization

Categorization is done lazily after `INDEXED`:

- **Language tag** — from STT detection, stored on `videos.detected_language`.
- **Topic tag** — top-K nearest cluster centroids in the library's vector
  space (clusters are recomputed nightly via mini-batch k-means).
- **Speaker tag** — when diarization is enabled, voice-prints are matched
  against a per-library `speakers` table; new voices are tagged
  `unknown-{n}` until the user names them.
- **Content type** — `lecture | sermon | interview | film | music_video |
  unknown`, predicted from a small classifier over duration, segment
  density, music-vs-speech ratio (from FFmpeg `silencedetect` and
  `loudnorm` stats).

### 5.3 Collections and tags

- **Collections** are user-defined ordered lists (e.g., a series of lectures).
- **Smart collections** are saved searches (e.g., "all videos by Speaker X
  longer than 30 minutes mentioning تفسير").
- **Tags** are free-form labels with a many-to-many relation to videos.

---

## 6. Clients: Web & Apps

Maktaba ships first-party clients on every screen the household uses. The
strategy is **one shared web codebase wrapped natively where possible, two
native TV codebases where it isn't.** A single React app runs as the PWA, in
the Capacitor mobile shells, and in the Tauri desktop shell; tvOS and
Android TV are the only places we maintain a second UI codebase.

### 6.1 Recommended app strategy (and why not the alternatives)

We evaluated three options:

- **Option A — Native everywhere** (Swift/SwiftUI for iOS+macOS+tvOS,
  Kotlin for Android+AndroidTV, C#/WinUI for Windows, GTK for Linux). Best
  per-platform UX. **Rejected**: a solo developer cannot maintain six native
  codebases plus the web app and ever ship features evenly. Plex itself
  has dozens of engineers and still ships features unevenly across native
  apps.
- **Option B — Cross-platform UI framework** (React Native or Flutter for
  mobile, Electron for desktop). One mobile codebase, one desktop codebase,
  one web codebase = three UI codebases. **Rejected**: still doubles UI
  surface vs. the web; React Native's Video player story for HLS+DASH
  with sidecar VTT and adaptive bitrate is fragile; Flutter requires
  re-implementing the entire UI.
- **Option C — PWA + thin native wrappers** ✅ **chosen**. One React
  codebase becomes the web app, the iOS app (Capacitor), the Android app
  (Capacitor), and the desktop app (Tauri). TV apps are the only native
  codebases — and they have to be, because tvOS doesn't have a usable PWA
  story and Android TV's web-based "Cast Receiver" model is not a
  full-screen app.

This means: **one UI to design, one UI to translate, one UI to test**, plus
two thin shells (Capacitor and Tauri) and two native TV apps that share
nothing with the web but do share the API contract.

### 6.2 Web (PWA)

A React 18 single-page app served from the API binary at `/`. The same Go
process serves the static bundle in production; in dev, Vite proxies `/api`
and `/stream` to the corresponding service.

PWA features:

- Installable on iOS Safari, Android Chrome, desktop Chromium.
- Service worker caches the app shell, library list, recent search results.
  Video bytes are never cached — they go through the Streaming Service.
- "Add to Home Screen" is the offline-installation path for users who don't
  want the wrapped mobile app.

**Pages:**

- **Home** — recently added, in-progress (across devices), recommended
  ("more like what you watch").
- **Library** — paginated grid, filterable by language, type, duration,
  speaker, tag, library.
- **Video detail** — player (Vidstack), transcript-as-sidebar (clickable,
  syncs with playback), chapter list, metadata, related videos.
- **Search** — single search box, hybrid results with highlighted snippets
  and timestamp deep-links (`/watch/{id}?t=3725.4`).
- **Speakers** — per-speaker page with all known appearances.
- **Tags / Collections** — browse and manage.
- **Queue** — live view of the worker pool and processing jobs (WebSocket).
- **Settings** — libraries, STT backends, language preferences, search
  weights, cache caps, integrations.

**Internationalization & RTL:** the shell supports `dir="rtl"` and
`dir="ltr"` per-route based on the active UI language. Transcript snippets
render with Unicode bidi isolates (`⁨...⁩`) so mixed Arabic/English text
aligns correctly even when results from different languages are
interleaved. Arabic UI strings are first-class translations, not
afterthoughts.

**Live updates:**

- WebSocket `/ws/jobs` — job state changes, progress percent, ETA.
- WebSocket `/ws/library/{id}` — newly discovered or processed videos.
- WebSocket `/ws/playback/{video_id}` — cross-device watch progress.
- Server-sent events as a fallback where WebSocket is blocked.

### 6.3 Mobile (iOS / Android — Capacitor)

The same React app, packaged with **Capacitor 6** into a native iOS and
Android shell. Capacitor provides the bridge to native APIs without
rewriting the UI:

- **Native player handoff.** A custom plugin opens the system AVPlayer
  (iOS) / ExoPlayer (Android) for full-screen playback, including AirPlay,
  Picture-in-Picture, and lock-screen controls. The HLS manifest URL from
  the API is handed off; metadata (title, poster, duration) is published
  via `MPNowPlayingInfoCenter` / Android `MediaSession`.
- **Background download** — Maktaba can download a video to the device for
  offline viewing (uses the system download manager; resumable, survives
  app suspension).
- **Push notifications** — "library scan complete", "new video ready"
  (opt-in, via APNs / FCM bridged through the API).
- **Deep links** — `maktaba://watch/{video_id}?t=...` opens the in-app
  player at the timestamp.
- **Auth keychain** — refresh tokens stored in iOS Keychain / Android
  Keystore.

App Store distribution is via TestFlight initially; Play Store internal
testing track. Self-hosters can sideload via Xcode / `adb install` or,
later, side-loading marketplaces.

### 6.4 Desktop (macOS / Windows / Linux — Tauri)

The same React app, packaged with **Tauri 2** into a native shell using the
OS's WebView (WKWebView on macOS, WebView2 on Windows, WebKitGTK on Linux).

- ~10 MB binary vs. Electron's ~120 MB; ~80 MB RAM at idle vs. ~400 MB.
- Native menus (File, Edit, View, Library, Window, Help).
- Native file association — double-clicking a `.maktaba` shortcut opens the
  app pointed at that server.
- System tray with playback controls and "next up" preview.
- Native fullscreen and HDR video paths via the WebView's hardware decoder.
- Auto-updater built into Tauri (signed delta updates).

Distribution: signed `.dmg` (notarized) for macOS, `.msi` for Windows,
`.AppImage` and `.deb` for Linux.

### 6.5 TV apps (tvOS — Swift, Android TV — Kotlin)

These are the only fully native UIs in the system. The remote-control input
model (focus engine, swipe gestures, voice search) and the 4K HDR codec
hardware paths warrant native; PWA on TV is, in practice, unusable.

**tvOS (Swift / SwiftUI + AVPlayer):**
- SwiftUI views built around the native focus engine.
- AVPlayer for HLS playback, including HDR (HLG, Dolby Vision where
  available), AirPlay, and the system scrub UI.
- Top Shelf integration: "Continue Watching" surfaced on the home screen.
- Siri Remote: voice search dispatches to `/api/search/suggest`.

**Android TV (Kotlin / Jetpack Compose for TV + ExoPlayer):**
- Compose for TV with the Leanback row layouts.
- ExoPlayer for HLS / DASH adaptive playback.
- Recommendations channel on the Android TV home screen.

Both TV apps share the same JSON API as every other client. There is no
separate "TV API"; the client renders different layouts from the same
GraphQL queries (`tvDashboard`, `tvRow`, etc.).

### 6.6 Shared client surface

Every client talks to:

- `https://{host}/api` — REST + GraphQL + WebSocket (API Service).
- `https://{host}/stream` — HLS / DASH / range (Streaming Service).

The web bundle, Capacitor shells, and Tauri shell all consume the same
GraphQL schema with generated TypeScript types (`graphql-codegen`). The
native TV apps consume hand-written Swift / Kotlin clients generated from
the same `.graphql` schema (`apollo-ios` / `apollo-kotlin`). Schema is the
single source of truth for what every client can ask for.

---

## 7. Batch Processing

A 30 TB library means jobs that run for hours per file and weeks per
library. Three properties are non-negotiable:

1. **Real-time durability** — every transcribed second is persisted before
   the worker moves on. A power loss after 4 h of transcription must not
   cost more than the in-flight segment (typically ≤30 s of audio).
2. **First-class pause/resume** — the user must be able to pause any video
   at any moment and resume it later from the exact second it stopped, even
   across process restarts, host reboots, or backend swaps.
3. **No re-work** — resuming, recovering from a crash, or upgrading the
   worker must never re-transcribe an audio range whose segments are already
   in the DB.

These properties are enforced primarily inside the `transcribe` stage,
because it is the only long-running stage; other stages are short enough
that simple "claim → work → commit" suffices. The job record carries the
state machine and progress accounting for all stages.

### 7.1 Job store

```sql
CREATE TABLE processing_jobs (
    id                       BIGSERIAL PRIMARY KEY,
    video_id                 UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    stage                    TEXT NOT NULL,           -- scan|probe|extract|transcribe|subtitle_gen|index|thumbnail
    state                    TEXT NOT NULL,           -- see 7.2 state machine
    priority                 INT  NOT NULL DEFAULT 100,
    attempts                 INT  NOT NULL DEFAULT 0,
    max_attempts             INT  NOT NULL DEFAULT 3,
    claimed_by               TEXT,                    -- worker id
    claimed_at               TIMESTAMPTZ,
    last_heartbeat_at        TIMESTAMPTZ,             -- updated every 5 s while running
    not_before               TIMESTAMPTZ,             -- backoff target
    error                    TEXT,

    ----- Progress accounting (all stages, but most meaningful for transcribe) -----
    total_duration_seconds   REAL,                    -- audio length to process
    processed_seconds        REAL NOT NULL DEFAULT 0, -- audio committed to DB
    segments_completed       INT  NOT NULL DEFAULT 0,
    last_segment_end_sec     REAL NOT NULL DEFAULT 0, -- canonical resume offset
    estimated_remaining_sec  REAL,                    -- wall-clock ETA, EWMA
    realtime_factor          REAL,                    -- audio_sec / wall_sec, EWMA
    progress_updated_at      TIMESTAMPTZ,

    ----- Pause / control -----
    pause_requested          BOOLEAN NOT NULL DEFAULT false,
    cancel_requested         BOOLEAN NOT NULL DEFAULT false,
    paused_at                TIMESTAMPTZ,
    paused_at_sec            REAL,                    -- audio offset where pause took effect
    paused_reason            TEXT,                    -- "user" | "shutdown" | "budget" | "policy"
    resumed_at               TIMESTAMPTZ,
    resume_count             INT  NOT NULL DEFAULT 0,

    metrics                  JSONB,                   -- runtime, model, backend, …
    created_at               TIMESTAMPTZ DEFAULT now(),
    finished_at              TIMESTAMPTZ
);

CREATE INDEX ON processing_jobs (state, priority, not_before);
CREATE INDEX ON processing_jobs (video_id, stage);
CREATE INDEX ON processing_jobs (state, last_heartbeat_at)
    WHERE state IN ('claimed', 'running', 'resuming');
CREATE INDEX ON processing_jobs (pause_requested) WHERE pause_requested = true;
```

The `last_segment_end_sec` field is the **canonical resume offset**: it is
advanced inside the same DB transaction that inserts the segment row(s) it
covers (see §7.6). It is the single source of truth for "where do we pick
up from?" — the worker never trusts wall clock, file mtime, or external
checkpoints for resume positioning.

### 7.2 Job state machine

States and the only legal transitions:

```
                              ┌──────────────┐
                              │   PENDING    │◄──────────────┐
                              └──────┬───────┘               │
                          claim()   │                        │ retry()
                                    ▼                        │
                              ┌──────────────┐               │
                              │   CLAIMED    │               │
                              └──────┬───────┘               │
                          start()   │                        │
                                    ▼                        │
                              ┌──────────────┐    fail()     │
                       ┌──────┤   RUNNING    ├──────────────►┤
                       │      └──┬─────────┬─┘               │
                       │         │         │                 │
                pause_         done()      cancel_           │
                requested        │         requested         │
                       │         │         │                 │
                       ▼         ▼         ▼                 │
                ┌────────────┐ ┌─────┐  ┌──────────┐         │
                │   PAUSED   │ │ DONE│  │CANCELLED │         │
                └─────┬──────┘ └─────┘  └──────────┘         │
                      │                                      │
              resume()│                                      │
                      ▼                                      │
                ┌────────────┐                               │
                │  RESUMING  │                               │
                └─────┬──────┘                               │
                      │  (rebuild context, then →RUNNING)    │
                      ▼                                      │
                ┌────────────┐                               │
                │   RUNNING  ├──────────────────────────────►┘
                └────────────┘
                                      ┌─────────────┐
                                      │   FAILED    │ (terminal once attempts ≥ max_attempts)
                                      └─────────────┘
```

| State        | Meaning                                                                             |
|--------------|-------------------------------------------------------------------------------------|
| `pending`    | In queue, eligible for claim.                                                       |
| `claimed`    | A worker has reserved it; setup not yet started. Has `claimed_by`, `claimed_at`.    |
| `running`    | Actively executing. Heartbeats every 5 s; progress fields tick in real time.        |
| `paused`     | Stopped at `paused_at_sec`. Holds no worker. Resume restarts from that offset.      |
| `resuming`   | A worker has picked up a paused job and is rebuilding context (loading model, seeking the audio decoder). Short-lived; fails forward to `running`. |
| `done`       | Terminal success. `finished_at` set.                                                |
| `failed`     | Terminal failure (attempts exhausted). `error` populated.                           |
| `cancelled`  | Terminal user-initiated abort.                                                      |

Notes:
- `paused` and `cancelled` are reached from `running` by the worker itself,
  in response to the `pause_requested` / `cancel_requested` flags. The API
  never mutates the live state directly — it only sets the request flag.
- `failed` is reached when `attempts >= max_attempts`; otherwise a transient
  failure flips back to `pending` with `not_before = now() + backoff`.

### 7.3 Claim loop

```sql
UPDATE processing_jobs
   SET state             = 'claimed',
       claimed_by        = $worker_id,
       claimed_at        = now(),
       last_heartbeat_at = now(),
       attempts          = attempts + 1
 WHERE id = (
   SELECT id FROM processing_jobs
    WHERE state IN ('pending', 'paused')
      AND (state = 'pending'                                  -- normal claim
           OR (state = 'paused' AND pause_requested = false)) -- resume claim
      AND (not_before IS NULL OR not_before <= now())
      AND cancel_requested = false
      AND stage = ANY($supported_stages)
    ORDER BY priority, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
 )
RETURNING *;
```

Resuming a paused job is the same operation as claiming a fresh one, except
the worker reads `last_segment_end_sec` and seeks its inputs there before
flipping the state to `resuming` → `running`. `SKIP LOCKED` keeps N workers
contention-free. On SQLite the workers share a process and use an asyncio
lock instead.

### 7.4 Concurrency model

Workers declare what stages they can run and how many of each they can run
concurrently. Defaults:

| Stage         | Concurrency | Reason                                             |
|---------------|-------------|----------------------------------------------------|
| scan, probe   | 4           | I/O-bound, cheap.                                  |
| extract       | 2           | Disk-bound; competes with streaming.               |
| transcribe    | 1 per GPU   | GPU is the bottleneck; serialization avoids OOM.   |
| index         | 4           | DB writes; small batches.                          |
| thumbnail     | 2           | Mild CPU.                                          |

Limits are enforced by per-stage semaphores in the worker process. A
GPU-bound stage acquires a process-global lock keyed by device id.

### 7.5 Priority scheduling

Priority is an integer; lower wins. Defaults:
- 50 — user-initiated (clicked "Process now")
- 100 — newly discovered
- 200 — re-process (model upgrade, settings change)
- 500 — bulk backfill

The user can override per-video or per-library. The UI exposes
"Move to front of queue" on every video.

### 7.6 Real-time progress persistence

The transcribe stage emits segments incrementally as the STT backend
produces them. Each segment is durably committed before the worker is
allowed to move on:

```python
async def run_transcribe(ctx, job, video):
    transcript = await ensure_transcript_row(video, job)
    seek_from = job.last_segment_end_sec  # 0.0 on a fresh job

    async with audio_decoder(video, start_sec=seek_from) as audio, \
               stt.session(language=video.detected_language) as stt:

        async for segment in stt.transcribe_stream(audio):
            # Single transaction: segment row + job progress + heartbeat.
            async with ctx.db.begin() as tx:
                await tx.execute(insert_segment_stmt, {
                    "transcript_id": transcript.id,
                    "seq":           job.segments_completed + 1,
                    "start_sec":     segment.start,
                    "end_sec":       segment.end,
                    "text":          segment.text,
                    "speaker":       segment.speaker,
                    "confidence":    segment.confidence,
                })
                await tx.execute(advance_job_progress_stmt, {
                    "id":                       job.id,
                    "segments_completed_delta": 1,
                    "processed_seconds":        segment.end - seek_from,
                    "last_segment_end_sec":     segment.end,
                    "realtime_factor":          ewma(job.realtime_factor, rt_factor),
                    "estimated_remaining_sec":  estimate_remaining(job, segment),
                    "progress_updated_at":      "now()",
                    "last_heartbeat_at":        "now()",
                })

            # Cooperative cancel/pause check after each commit.
            if await ctx.should_pause(job.id):
                await mark_paused(job.id, at_sec=segment.end, reason="user")
                return PauseResult(at_sec=segment.end)
            if await ctx.should_cancel(job.id):
                await mark_cancelled(job.id, at_sec=segment.end)
                return CancelResult(at_sec=segment.end)

    await mark_done(job.id)
```

Key properties:

- **Atomic per-segment commit.** The `segments` insert and the
  `processing_jobs` progress update share one transaction. If the process
  dies between segments, the DB is consistent: the last `segments` row's
  `end_sec` always equals `processing_jobs.last_segment_end_sec`.
- **Granularity is "one Whisper segment"** (typically 5–30 s). This is the
  natural cut point of the STT backend; cutting finer would split sentences
  and degrade subtitle quality. Coarser (e.g., minutes) would cost
  unacceptable rework on crash.
- **Audio-time progress, not wall-clock.** `processed_seconds` is the sum
  of audio durations actually transcribed, not how long the worker has been
  running. The UI shows "1h 23m 17s of 4h 12m" — meaningful even if the
  worker stalled, swapped backends, or migrated hosts.
- **EWMA for ETA.** `realtime_factor` is exponentially smoothed
  (α = 0.2) so a single slow segment doesn't make the ETA jitter. ETA is
  `(total_duration - processed) / realtime_factor`.
- **Heartbeat coupling.** The progress UPDATE doubles as the heartbeat,
  saving a separate write. A pure heartbeat tick still fires every 5 s in
  case a single segment takes longer than the stale-claim window
  (relevant for very slow CPU backends).

Sidecar files (`.srt`, `.vtt`) are **only** written when the job reaches
`done`. While the job is in flight, partial subtitles can be rendered on
demand by querying `transcript_segments` — the DB is the live truth, no
intermediate file format to keep in sync.

### 7.7 Pause and resume

**Pause is cooperative.** The API marks `pause_requested = true`; the
running worker checks the flag after every committed segment and exits
cleanly:

```
1. UI calls POST /api/jobs/{id}/pause
2. API: UPDATE processing_jobs SET pause_requested=true WHERE id=$id
3. Worker (next segment boundary):
   a. Commits the current segment (no segments are ever lost or duplicated)
   b. UPDATE … SET state='paused',
                  paused_at=now(),
                  paused_at_sec=last_segment_end_sec,
                  paused_reason='user',
                  pause_requested=false,
                  claimed_by=NULL
   c. Releases GPU lock and exits the stage.
4. WS /ws/jobs broadcasts the new state.
```

If the worker is stuck inside a single segment for longer than
`pause_grace_sec` (default 60 s), the API offers a "Force pause" button
which flips the job to `paused` without waiting and reverts
`last_segment_end_sec` to the last committed value. The in-flight segment is
discarded (it was never persisted) and will be re-transcribed on resume.

**Resume is just a claim against `paused`.** The claim loop already accepts
`paused` rows whose `pause_requested = false` (§7.3). The worker that picks
up the job:

1. Sets state to `resuming`, increments `resume_count`, sets `resumed_at`.
2. Reloads the STT backend, decoder, and any in-memory context (the prompt
   for Whisper is rebuilt from the last K segments' text — preserving
   continuity across the resume seam).
3. Opens the audio decoder seeked to `last_segment_end_sec` (FFmpeg
   `-ss {sec}` for fast-forward seek; for VBR sources, decoder-level seek
   to the nearest preceding keyframe and discard the lead-in).
4. Sets state to `running`. The transcribe loop continues identically to
   §7.6, with `seek_from = last_segment_end_sec`.

**Audio-offset, not segment-index, is the resume key.** Backend or model
upgrades may emit different segment boundaries; an offset in seconds
survives those changes, while a segment index does not.

### 7.8 Graceful shutdown

The worker traps `SIGTERM` and `SIGINT` and treats them as a global pause:

```
1. Signal received → ctx.shutdown_requested.set()
2. The transcribe loop's per-segment check sees both should_pause and
   shutdown_requested:
     - Commit the current segment (already inside §7.6's transaction).
     - Mark all this worker's running jobs paused with reason='shutdown'.
     - Release GPU locks; close decoder pipes.
3. Worker process exits 0.
4. On next start, the reaper (§7.9) finds no stale 'claimed'/'running'
   rows because the shutdown converted them to 'paused' cleanly. The
   workers re-claim them as resumes; the user sees no interruption beyond
   "paused for shutdown" in the job history.
```

Shutdown deadline: the worker waits up to `shutdown_grace_sec` (default
120 s) for the in-flight segment to commit. If the deadline expires, the
worker is allowed to exit with the in-flight segment uncommitted —
correctness is preserved (the partial segment was never persisted), at the
cost of re-transcribing that ≤30 s on resume. A second SIGTERM forces
immediate exit with the same correctness guarantee.

A second SIGINT (Ctrl-C twice) is interpreted as "abort, don't even wait
for the segment" — same outcome as the deadline path.

### 7.9 Crash recovery

Crashes are the same problem as graceful shutdown, minus the cooperation:

- **Reaper.** A periodic task (every 30 s) scans for jobs in
  `claimed`/`running`/`resuming` whose `last_heartbeat_at < now() -
  stale_claim_sec` (default 90 s, > 3× heartbeat interval). It flips them
  to `paused` with `paused_reason='crash'`, `paused_at_sec =
  last_segment_end_sec`. They become re-claimable like any other paused
  job.
- **No replay log needed.** The DB already holds the durable history:
  every committed segment is in `transcript_segments`, and
  `last_segment_end_sec` matches by construction (§7.6). Recovery is
  resume; resume is a normal claim.
- **Atomic file writes for non-DB outputs.** Stages that produce files
  (`subtitle_gen`, `thumbnail`, HLS segmenter) write to
  `…/.tmp/{uuid}` and `os.replace()` to the final path on success. A crash
  mid-write leaves a stray temp file that the next scan removes. These
  stages are short — they always re-run from scratch on resume; no
  intermediate checkpoint is needed.
- **No JSON sidecar checkpoints.** The DB *is* the checkpoint. A previous
  draft of this design used `.maktaba/transcripts/{hash}.partial.json`
  files; that approach was rejected because it created two sources of truth
  for resume position (file vs. DB) and a bug in either could re-transcribe
  hours of audio. Sidecar `.srt`/`.vtt` files are now strictly outputs of
  successful jobs, never inputs to a resume.

### 7.10 Progress reporting to the UI

The worker's per-segment commit (§7.6) bumps `progress_updated_at`. A
listener (Postgres `LISTEN/NOTIFY` in prod, polling on SQLite) pushes
deltas into the WebSocket fan-out:

```json
{
  "type":   "job.progress",
  "id":     842,
  "video_id": "…",
  "stage":  "transcribe",
  "state":  "running",
  "total_duration_seconds": 15124.0,
  "processed_seconds":      4988.5,
  "segments_completed":     742,
  "last_segment_end_sec":   4988.5,
  "realtime_factor":        0.32,
  "estimated_remaining_sec": 31738.4,
  "updated_at": "2026-05-03T15:42:11.218Z"
}
```

The UI throttles renders to 1 Hz per visible job — the message firehose can
go higher without the browser melting.

### 7.11 Throughput estimate (30 TB)

Assume average 2 GB / hour of content → 30 TB ≈ 15,000 hours. On Apple
Silicon M-series with `mlx-whisper large-v3` at ~0.3× realtime, single GPU:
~4,500 GPU-hours ≈ 6 months wall-clock continuous, or ~2 months at 24/7
with two M-class machines. On a single A100 with `faster-whisper large-v3`
at ~0.1× realtime: ~6 weeks. Plan for incremental processing; never demand
the user wait for a full backfill before the UI is useful.

---

## 8. Database Schema

PostgreSQL syntax shown; SQLite is identical aside from `JSONB`→`JSON`,
`TIMESTAMPTZ`→`DATETIME`, and `UUID`→`TEXT`.

### 8.1 Core tables

```sql
-- A logical collection of folders sharing a config profile.
CREATE TABLE libraries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL UNIQUE,
    settings     JSONB NOT NULL DEFAULT '{}'::jsonb,
    deleted_at   TIMESTAMPTZ,                 -- soft delete; lists filter on NULL
    created_at   TIMESTAMPTZ DEFAULT now(),
    updated_at   TIMESTAMPTZ DEFAULT now()
);

-- Library root paths live in their own table (one library, many roots).
-- Owner: plan-09-16. Plans pre-09-16 may treat `libraries.roots TEXT[]`
-- as a transitional column; the canonical store is `library_roots`.
CREATE TABLE library_roots (
    id          BIGSERIAL PRIMARY KEY,
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path        TEXT NOT NULL,                -- absolute path
    created_at  TIMESTAMPTZ DEFAULT now(),
    UNIQUE (library_id, path)
);
CREATE INDEX ON library_roots (library_id);

-- One row per unique video file (by content hash).
CREATE TABLE videos (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id         UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    content_hash       TEXT NOT NULL UNIQUE,    -- BLAKE3 fingerprint
    path               TEXT NOT NULL,           -- current absolute path
    filename           TEXT NOT NULL,
    size_bytes         BIGINT NOT NULL,
    mtime              TIMESTAMPTZ NOT NULL,
    state              TEXT NOT NULL DEFAULT 'discovered',
    detected_language  TEXT,                    -- ISO 639-1, set by STT
    title              TEXT,
    description        TEXT,
    poster_path        TEXT,                    -- relative to cache root
    sprite_path        TEXT,
    duration_sec       REAL,
    content_type       TEXT,                    -- 'lecture'|'lesson'|'movie'|...; set by classifier
    deleted_at         TIMESTAMPTZ,             -- soft delete (file gone or user-removed)
    created_at         TIMESTAMPTZ DEFAULT now(),
    updated_at         TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX ON videos (library_id, state);
CREATE INDEX ON videos (detected_language);
CREATE INDEX ON videos (content_type);

-- One row per probe; we keep history if a file changes.
CREATE TABLE media_info (
    video_id        UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    container       TEXT,
    video_codec     TEXT,
    width           INT,
    height          INT,
    fps             REAL,
    bitrate_kbps    INT,
    has_subtitles   BOOLEAN DEFAULT false,
    raw_ffprobe     JSONB,
    probed_at       TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE audio_tracks (
    id           BIGSERIAL PRIMARY KEY,
    video_id     UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    index        INT NOT NULL,            -- ffmpeg stream index
    codec        TEXT,
    channels     INT,
    sample_rate  INT,
    language     TEXT,                    -- as tagged in the file
    title        TEXT,
    is_default   BOOLEAN DEFAULT false,
    UNIQUE (video_id, index)
);

-- One transcript per (video, audio_track, stt_run).
CREATE TABLE transcripts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id            UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    audio_track_id      BIGINT NOT NULL REFERENCES audio_tracks(id),
    language            TEXT NOT NULL,         -- ISO 639-1; primary language tag
    detected_language   TEXT,                  -- raw STT detection (may differ from `language`)
    language_confidence REAL,                  -- 0..1, set by language-tag stage
    backend             TEXT NOT NULL,         -- whisper-mlx, openai-api, ...
    model               TEXT NOT NULL,         -- large-v3, ...
    backend_version     TEXT,
    word_level          BOOLEAN NOT NULL,
    diarized            BOOLEAN NOT NULL,
    quality_score       REAL,                  -- aggregate confidence
    superseded_at       TIMESTAMPTZ,           -- non-null = newer transcript replaces this one
    created_at          TIMESTAMPTZ DEFAULT now(),
    UNIQUE (video_id, audio_track_id, backend, model)
);
CREATE INDEX ON transcripts (video_id) WHERE superseded_at IS NULL;

CREATE TABLE transcript_segments (
    id             BIGSERIAL PRIMARY KEY,
    transcript_id  UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq            INT NOT NULL,
    start_sec      REAL NOT NULL,
    end_sec        REAL NOT NULL,
    text           TEXT NOT NULL,
    speaker        TEXT,
    confidence     REAL,
    UNIQUE (transcript_id, seq)
);
CREATE INDEX ON transcript_segments (transcript_id, start_sec);

-- Optional word-level timing for karaoke-style highlighting.
CREATE TABLE transcript_words (
    id            BIGSERIAL PRIMARY KEY,
    segment_id    BIGINT NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
    seq           INT NOT NULL,
    start_sec     REAL NOT NULL,
    end_sec       REAL NOT NULL,
    text          TEXT NOT NULL,
    confidence    REAL
);

CREATE TABLE subtitle_files (
    id            BIGSERIAL PRIMARY KEY,
    video_id      UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id UUID REFERENCES transcripts(id) ON DELETE SET NULL,
    format        TEXT NOT NULL,         -- srt | vtt
    language      TEXT NOT NULL,
    path          TEXT NOT NULL,         -- absolute path on disk
    is_external   BOOLEAN NOT NULL,      -- shipped with the video, not generated
    created_at    TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE chapters (
    id           BIGSERIAL PRIMARY KEY,
    video_id     UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    seq          INT NOT NULL,
    start_sec    REAL NOT NULL,
    end_sec      REAL NOT NULL,
    title        TEXT,
    source       TEXT NOT NULL,         -- embedded | inferred | manual
    UNIQUE (video_id, seq)
);
```

### 8.2 Library organization

```sql
CREATE TABLE tags (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    name_fold  TEXT,                    -- diacritic-folded form for search
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE video_tags (
    video_id UUID REFERENCES videos(id) ON DELETE CASCADE,
    tag_id   BIGINT REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (video_id, tag_id)
);

CREATE TABLE collections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id  UUID REFERENCES libraries(id) ON DELETE CASCADE,  -- NULL = cross-library
    name        TEXT NOT NULL,
    description TEXT,
    is_smart    BOOLEAN NOT NULL DEFAULT false,
    smart_query JSONB,                   -- filter spec when is_smart
    created_at  TIMESTAMPTZ DEFAULT now(),
    updated_at  TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX ON collections (library_id);

CREATE TABLE collection_items (
    collection_id UUID REFERENCES collections(id) ON DELETE CASCADE,
    video_id      UUID REFERENCES videos(id) ON DELETE CASCADE,
    position      INT NOT NULL,
    added_at      TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (collection_id, video_id)
);

CREATE TABLE speakers (
    id          BIGSERIAL PRIMARY KEY,
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name        TEXT,                    -- null = unknown
    voiceprint  BYTEA,                   -- d-vector / x-vector
    updated_at  TIMESTAMPTZ DEFAULT now(),
    UNIQUE (library_id, name)
);

CREATE TABLE segment_speakers (
    segment_id BIGINT REFERENCES transcript_segments(id) ON DELETE CASCADE,
    speaker_id BIGINT REFERENCES speakers(id),
    confidence REAL,
    PRIMARY KEY (segment_id, speaker_id)
);

-- Per-video derived features used by classifiers and recommenders.
-- Owned by Epic 09 (`plan-09-10-content-type-classifier.md`).
CREATE TABLE media_features (
    video_id    UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    features    JSONB NOT NULL,          -- { duration_bucket, speech_ratio, music_ratio, ... }
    model       TEXT NOT NULL,           -- classifier model id
    updated_at  TIMESTAMPTZ DEFAULT now()
);

-- Search-indexable token unit (typically a transcript segment or sentence
-- chunk; populated by `index` stage). Decoupled from `transcript_segments`
-- so chunking strategy can change without rewriting STT outputs.
CREATE TABLE transcript_units (
    id              BIGSERIAL PRIMARY KEY,
    transcript_id   UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    video_id        UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    segment_id      BIGINT REFERENCES transcript_segments(id) ON DELETE CASCADE,
    seq             INT NOT NULL,
    start_sec       REAL NOT NULL,
    end_sec         REAL NOT NULL,
    text            TEXT NOT NULL,
    language        TEXT,
    UNIQUE (transcript_id, seq)
);
CREATE INDEX ON transcript_units (video_id);
CREATE INDEX ON transcript_units (segment_id);
```

### 8.2.1 Audit log

Append-only event store, used both for security events (login,
permission-deny, signed-URL mint, …) and for library/admin actions
(library deletion, scan trigger, …). Owned by Epic 9
(`plan-09-17-library-audit.md`); Epic 10 extends the security category
(`plan-10-16-security-audit.md`).

```sql
CREATE TABLE audit_log (
    id          BIGSERIAL,
    category    TEXT NOT NULL CHECK (category IN ('library','security','device','admin')),
    event       TEXT NOT NULL,           -- e.g. 'library.deleted', 'auth.login.success'
    actor_user  UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_ip    INET,
    target_id   TEXT,                    -- resource id (library/video/device/...)
    target_kind TEXT,                    -- 'library'|'video'|'device'|'session'|...
    payload     JSONB NOT NULL DEFAULT '{}'::jsonb,
    dedupe_key  TEXT,                    -- security writers may set for once-per-window dedupe
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Monthly partitions; the management script in plan-09-17 keeps a 13-month rolling window.
-- Unique indexes on partitioned tables must include the partition key:
CREATE UNIQUE INDEX audit_log_security_dedupe
  ON audit_log (created_at, dedupe_key)
  WHERE category = 'security' AND dedupe_key IS NOT NULL;
```

`category='device'` is reserved for device registration / token
rotation events (Epic 12). Mobile push-token events go here, not under
`security`.

### 8.2.2 Events bus replay log

A short-retention table the API uses to replay missed WebSocket frames
when a client reconnects. Owned by Epic 7
(`plan-07-16-websocket-fanout.md`).

```sql
CREATE TABLE events (
    id          BIGSERIAL PRIMARY KEY,
    channel     TEXT NOT NULL,            -- 'videos.new', 'jobs.progress', ...
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON events (channel, id);
-- Reaper trims rows older than 1 h.
```

### 8.2.3 Devices

Mobile / desktop / TV clients register a device on first launch and on
push-token rotation. One row per (user, token_hash) pair; raw push
tokens are not stored. Owned by Epic 12
(`plan-12-10-device-registration-api.md`); supersedes the earlier
`plan-07-22-devices-register.md` migration.

```sql
CREATE TABLE devices (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform      TEXT NOT NULL,           -- 'ios'|'android'|'macos'|'windows'|'linux'|'tvos'|'androidtv'
    bundle_id     TEXT,                    -- APNs topic / Android packageName; required on iOS/macOS/tvOS
    token         TEXT,                    -- raw push token; never read after writing
    token_hash    TEXT GENERATED ALWAYS AS (encode(sha256(coalesce(token, '')::bytea), 'hex')) STORED,
    app_version   TEXT,
    os_version    TEXT,
    locale        TEXT,
    categories    JSONB NOT NULL DEFAULT '[]'::jsonb,  -- notification topics opted into
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    UNIQUE (user_id, token_hash)
);
CREATE INDEX ON devices (user_id) WHERE revoked_at IS NULL;
```

### 8.3 Search index (SQLite FTS5 variant)

```sql
CREATE VIRTUAL TABLE transcripts_fts USING fts5(
    text,
    video_id  UNINDEXED,
    segment_id UNINDEXED,
    language  UNINDEXED,
    tokenize = 'unicode61 remove_diacritics 2'
);
-- Triggers keep transcripts_fts in sync with transcript_segments.
```

For Postgres, use `tsvector` with a `arabic` text search configuration and a
GIN index, plus `pg_trgm` for prefix queries.

### 8.4 Vector store (ChromaDB)

One collection per library:

```python
client = chromadb.PersistentClient(path="/var/maktaba/chroma")
col = client.get_or_create_collection(
    name=f"library-{library.id}",
    metadata={"hnsw:space": "cosine"},
    embedding_function=multilingual_e5_large,
)
col.add(
    ids=[f"{segment.transcript_id}:{segment.seq}"],
    documents=[segment.text],
    metadatas=[{
        "video_id": str(video.id),
        "start": segment.start_sec,
        "end": segment.end_sec,
        "language": transcript.language,
        "speaker": segment.speaker,
    }],
)
```

### 8.5 User & playback state (single user friendly, multi-user ready)

```sql
CREATE TABLE users (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username   TEXT NOT NULL UNIQUE,
    pw_hash    TEXT,                     -- argon2id; null for "single user, no auth"
    is_admin   BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE playback_state (
    user_id      UUID REFERENCES users(id) ON DELETE CASCADE,
    video_id     UUID REFERENCES videos(id) ON DELETE CASCADE,
    position_sec REAL NOT NULL,
    completed    BOOLEAN NOT NULL DEFAULT false,
    updated_at   TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);

CREATE TABLE saved_searches (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    kind       TEXT,                          -- 'fts'|'semantic'|'hybrid' (default at save time)
    query      JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);
```

### 8.6 Auth tables (cross-reference)

The auth tables (`web_sessions`, `refresh_tokens`, `pairing_codes`,
`personal_access_tokens`, `library_acl`, `jwt_keys`) are owned by
Epic 10. Notable cross-cutting columns:

- `refresh_tokens.device_id UUID REFERENCES devices(id) ON DELETE CASCADE` —
  set on native logins (iOS, Android, desktop, TV). Owned by
  `plan-10-03-native-login.md`. The web flow leaves it null.
- `pairing_codes.code_hash TEXT NOT NULL` — argon2id of the displayed
  code. Owner: `plan-10-17-auth-pair.md`. The plaintext `code` column is
  not stored.
- `library_acl (library_id, user_id, role)` — permission grants.
  Owner: `plan-10-13-permission-model.md`. `LibrariesForUser(user_id)`
  is the canonical accessor.

### 8.7 Plan-introduced schema extensions

The base schema in §8.1–§8.5 is what every service can rely on. Implementation
plans extend it with additional columns and tables; CI rejects any SQL that
references a column or table not in §8.1–§8.5 **or** the table below. The
single source of truth for slot numbering is
[`shared/db/migrations/MANIFEST.md`](../shared/db/migrations/MANIFEST.md).

**Column extensions to base tables.**

| Base table | Column | Type | Owning plan | Slot |
|------------|--------|------|-------------|------|
| `videos` | `metadata` | `JSONB NOT NULL DEFAULT '{}'` | plan-01-05 | 0001 |
| `videos` | `last_seen_at` | `TIMESTAMPTZ` | plan-01-05 | 0007 |
| `videos` | `deleted_at` | `TIMESTAMPTZ` | plan-01-05 | 0007 |
| `audio_tracks` | `disposition` | `JSONB NOT NULL DEFAULT '{}'` | plan-02-02 | 0009 |
| `audio_tracks` | `detected_language` | `TEXT` (ISO-639-3) | plan-02-02 | 0009 |
| `audio_tracks` | `detected_language_confidence` | `REAL` (0..1) | plan-02-02 | 0009 |
| `audio_tracks` | `last_extracted_at` | `TIMESTAMPTZ` | plan-02-03 | 0010 |
| `processing_jobs` | full `architecture.md §7.1` shape + `payload JSONB` + CHECK constraints | — | plan-06-01 | 0002 |
| `processing_jobs` | `error` | `JSONB` (replaces base `TEXT`) | plan-02-03 | 0010 |
| `transcripts` | `is_active` | `BOOLEAN NOT NULL DEFAULT true` | plan-03-05 | 0012 |
| `transcripts` | `metadata` | `JSONB NOT NULL DEFAULT '{}'` | plan-03-05 | 0012 |
| `transcripts` | `last_indexed_segment_seq` | `INT NOT NULL DEFAULT 0` | plan-05-05 | 0025 |
| `transcripts` (UNIQUE) | drops `(video_id, audio_track_id, backend, model)` global UNIQUE; replaced with partial UNIQUE on `is_active = true` | — | plan-03-05 | 0012 |
| `subtitle_files` | `is_embedded`, `is_default`, `flags`, `size_bytes`, `mtime_ns`, `track_index`, `metadata`, `revived_count`, `deleted_at` | various | plan-04-03 | 0015 |
| `transcript_units` (table itself) | new — search-time chunks of transcript text | — | plan-05-01 | 0017 |
| `transcript_units` | `tsv` (generated `tsvector`) | `tsvector` | plan-05-02 | 0021 |
| `transcript_units` | `indexed_at_in_chroma` | `TIMESTAMPTZ` | plan-05-05 | 0025 |
| `chapters` | `lang`, `confidence`, `metadata` | `TEXT`, `REAL`, `JSONB` | plan-05-07 | 0026 |
| `chapters` (UNIQUE) | `(video_id, source, seq)` (instead of base `(video_id, seq)`) so `inferred` / `embedded` / `manual` can coexist | — | plan-05-07 | 0026 |

**Plan-introduced tables.**

| Table | Owning plan | Slot | Purpose |
|-------|-------------|------|---------|
| `library_scan_state` | plan-01-05 | 0006 | Per-library scan watermarks + counters; carries `cancel_requested`/`progress_pct` (plan-01-04). |
| `purge_log` | plan-01-05 | 0006 | Audit trail for `--purge-missing` deletions. |
| `audio_cache` | plan-02-03 | 0010 | Ledger of cached extracted-WAV files keyed by `(content_hash, audio_index)`. |
| `track_selection_decisions` | plan-02-02 | 0009 | One row per video: which rule fired and which tracks won. Debugging aid for "why did it pick that track?" |
| `stt_usage` | plan-03-04 | 0011 | Per-chunk billing ledger for paid STT backends (OpenAI). |
| `transcript_units` | plan-05-01 | 0017 | Search-time chunks (~200 chars) materialized from `transcript_segments`. |
| `vector_index_dead_letter` | plan-05-05 | 0025 | DLQ for Chroma indexing failures. |
| `search_suggestion_terms` | plan-05-06 | 0027 | Typeahead corpus (saved searches + speakers + ngrams). |

**REVIEW resolutions baked into §8.6.**

- The `transcripts (video_id, audio_track_id, backend, model)` UNIQUE
  in §8.1 is **replaced** by a partial unique index on
  `is_active = true` (REVIEW §1.1.b; plan-03-05).
- The `subtitle_files.is_embedded` distinction (REVIEW §1.1.c; plan-04-03)
  joins `is_external` so the table can describe sidecars, embedded
  extractions, and pipeline-generated artifacts in one shape.
- The SQLite FTS5 virtual table at §8.3 keys on
  `transcript_id`/`unit_id` (instead of `video_id`/`segment_id`)
  because the unit grain is what the indexer feeds (REVIEW §1.1.d;
  plan-05-02).

---

## 9. API Design

The API Service (Go) exposes three surfaces on one port:

- **REST** under `/api/*` for CRUD, control, and webhooks. JSON,
  cursor-paginated (`?cursor=...&limit=...`), errors as RFC 9457
  `application/problem+json`.
- **GraphQL** at `/graphql` (and `/graphql/subscriptions` for WebSocket) for
  client-driven view composition. Schema-first via `gqlgen`; resolvers
  share the same domain code as the REST handlers.
- **WebSocket** under `/ws/*` for fire-and-forget broadcast streams (job
  progress, library updates, watch sync). Subscriptions that need
  per-query selection use GraphQL subscriptions instead.

The Streaming Service (Go) exposes its own surface on a different port (or
under `/stream/*` if behind a reverse proxy):

- HLS / DASH / direct-play HTTP under `/stream/*`. JWT-signed URLs from the
  API; bytes only.

The endpoints below are the REST surface. The GraphQL schema mirrors the
same domain types (`Library`, `Video`, `Segment`, `Job`, `Session`,
`Speaker`, `Collection`, `Tag`).

### 9.1 Library

```
GET    /api/libraries
POST   /api/libraries                    {name, roots, settings}
GET    /api/libraries/{id}
PATCH  /api/libraries/{id}               {name?, roots?, settings?}
DELETE /api/libraries/{id}               ?purge=true|false
POST   /api/libraries/{id}/scan          → enqueues scan job
GET    /api/libraries/{id}/stats         → counts, durations, processed%
```

### 9.2 Videos

```
GET    /api/videos                       ?library&language&type&tag&q&sort&cursor
GET    /api/videos/{id}                  → full metadata
PATCH  /api/videos/{id}                  {title?, description?, tags?}
DELETE /api/videos/{id}                  ?purge=true (also removes file)
POST   /api/videos/{id}/process          {stage?, priority?}
POST   /api/videos/{id}/reprocess        {from_stage}     → resets state
GET    /api/videos/{id}/segments         ?from&to         → transcript window
GET    /api/videos/{id}/subtitles        → list of SubtitleFile
GET    /api/videos/{id}/chapters
```

### 9.3 Search

```
POST   /api/search                       {q, mode, filters, limit}
                                         mode = "fts" | "semantic" | "hybrid" (default)
GET    /api/search/suggest?q=...         → autocomplete
POST   /api/search/save                  {name, query}
GET    /api/search/saved
```

Search response shape:

```json
{
  "took_ms": 84,
  "total": 137,
  "hits": [
    {
      "video_id": "…",
      "title": "…",
      "language": "ar",
      "score": 0.83,
      "matches": [
        {
          "segment_id": 4421,
          "start_sec": 372.4,
          "end_sec": 379.1,
          "text": "…<mark>الحمد لله</mark>…",
          "speaker": "Sheikh A"
        }
      ]
    }
  ],
  "next": "…cursor…"
}
```

### 9.4 Streaming

The API mints sessions; the Streaming Service serves bytes. Two surfaces:

**API Service** (session lifecycle):
```
POST   /api/stream/sessions              {video_id, client_profile, audio_track?, subtitle_track?, start_sec?}
                                         → {session_id, manifest_url, expires_at}
GET    /api/stream/sessions/{id}         → session info, bitrate ladder, current rendition
DELETE /api/stream/sessions/{id}         → close session, free transcoder slot
POST   /api/stream/sessions/{id}/progress {position_sec, completed?}
GET    /api/stream/capabilities          → server capabilities (codecs, hwaccel, max bitrate)
```

`manifest_url` is a signed URL (`/stream/{session_id}/manifest.m3u8?sig=...`)
valid for `expires_at`; the client passes it to the player and the player
talks to the Streaming Service directly.

**Streaming Service** (bytes only):
```
GET    /stream/{session_id}/manifest.m3u8        → HLS master
GET    /stream/{session_id}/manifest.mpd         → DASH (if requested)
GET    /stream/{session_id}/{rendition}/index.m3u8
GET    /stream/{session_id}/{rendition}/seg-{n}.ts    → audio is muxed into the variant via var_stream_map
GET    /stream/{session_id}/subs/{lang}.vtt      → live-rendered from DB
GET    /stream/direct/{video_id}                 → 206 Partial Content (signed JWT in query)
GET    /stream/posters/{video_id}.jpg
GET    /stream/sprites/{video_id}.{webp,vtt}
```

Range-request handling supports HEAD, conditional requests, and partial
content properly so Safari (the strictest) plays back without reload loops.
All Streaming endpoints validate a JWT signature against the API's RS256
public key; the Streaming Service does not call back to the API to authorize.

**JWT audiences** (token `aud` claim):

| `aud` value         | Issued for                                    | Carried to                                            |
|---------------------|-----------------------------------------------|-------------------------------------------------------|
| `api`               | API REST/GraphQL                              | API Service                                            |
| `streaming`         | HLS/DASH manifests + segments + live subs     | `/stream/{session}/manifest.*`, `/stream/{session}/...` |
| `streaming-direct`  | Direct-play 206 Partial Content               | `GET /stream/direct/{video_id}`                        |
| `streaming-static`  | Posters, sprites, chapter thumbs              | `GET /stream/posters/*`, `/stream/sprites/*`           |

Signed-URL minting (`plan-10-08-signed-url-minter.md`) emits `lib=[library_id]`
as a **singleton** containing only the resource's library, not the user's
full library set — leaking a URL must not disclose other library
memberships. The full snapshot lives only in the API access token (`api`
audience).

### 9.5 Processing

```
GET    /api/jobs                         ?state&stage&video&cursor
GET    /api/jobs/{id}
GET    /api/videos/{id}/jobs             → jobs for a single video (used by detail page)
POST   /api/jobs/{id}/pause              → sets pause_requested
POST   /api/jobs/{id}/pause?force=true   → flips state immediately, drops in-flight segment
POST   /api/jobs/{id}/resume             → makes a paused job re-claimable
POST   /api/jobs/{id}/cancel             → sets cancel_requested
POST   /api/jobs/{id}/retry              → resets attempts (failed → pending)
POST   /api/jobs/{id}/priority           {priority}  → adjust queue priority
POST   /api/jobs:bulk-pause              {ids: [...] | filter: {...}}
POST   /api/jobs:bulk-resume             {ids: [...] | filter: {...}}
POST   /api/jobs:bulk-cancel             {ids: [...] | filter: {...}}
POST   /api/jobs:bulk-retry              {ids: [...] | filter: {...}}

POST   /api/videos/{id}/pause            → pauses every active job for this video
POST   /api/videos/{id}/resume           → resumes every paused job for this video
POST   /api/videos/{id}/process          {stage?, priority?}
POST   /api/videos/{id}/reprocess        {from_stage}     → resets state

GET    /api/queue/stats                  → per-stage counts and ETA
WS     /ws/jobs                          → live job state + progress (see §7.10)
```

The `from_stage` request key is canonical on `reprocess`; `stage` is
canonical on `process`. The seven canonical pipeline stage strings are
`scan`, `probe`, `extract`, `transcribe`, `subtitle_gen`, `index`,
`thumbnail` (see §3).

Bulk and priority endpoints flip flags / insert rows in
`processing_jobs` directly — they do **not** call Pipeline gRPC. This
matches §1.4: "bulk job control flows through Postgres, not gRPC."

`pause`, `resume`, and `cancel` are idempotent: re-issuing a request that
matches the current state (or pending request flag) returns 200 with the
unchanged job. The endpoints never block on the worker; they set flags and
return immediately. Clients observe the actual state transition over the
WebSocket.

### 9.6 Collections, tags, speakers

```
GET/POST/PATCH/DELETE  /api/collections[/{id}]
POST                   /api/collections/{id}/items     {video_id, position}
DELETE                 /api/collections/{id}/items/{video_id}

GET/POST               /api/tags
PATCH                  /api/videos/{id}/tags           {add: [...], remove: [...]}

GET                    /api/speakers
PATCH                  /api/speakers/{id}              {name}
POST                   /api/speakers/merge             {keep, drop}
```

### 9.7 Settings & system

```
GET/PATCH  /api/settings                 → app-wide config
GET        /api/settings/stt-backends    → enumerate available backends
POST       /api/settings/stt-test        {backend, config} → dry run
GET        /api/system/health
GET        /api/system/version
GET        /api/system/metrics           → Prometheus exposition
```

### 9.7.1 Per-user (`/api/me`)

```
GET    /api/me                            → user profile
PATCH  /api/me                            {display_name?, locale?, ...}
POST   /api/me/password                   {current_password, new_password}
PATCH  /api/me/playback-state             {video_id, position_sec, completed?}
GET    /api/me/devices                    → list devices for the current user
GET    /api/me/sessions                   → active web/native sessions
DELETE /api/me/sessions/{id}              → revoke a session
GET    /api/me/pats                       → personal access tokens
POST   /api/me/pats                       {name, scopes, ttl}
DELETE /api/me/pats/{id}
```

`PATCH /api/me/playback-state` is the offline-tolerant write path the
PWA flushes on reconnect; the streaming-session progress endpoint
(§9.4) writes the same row from an active watch session.

### 9.7.2 Recommendations & devices

```
GET    /api/recommendations              ?library&limit  → "next up", "continue watching"
GET    /api/recommendations/similar/{video_id}

POST   /api/devices                      {platform, bundle_id?, token, app_version, ...}
                                         → registers / refreshes a device row
DELETE /api/devices/{id}                 → revokes a device
PATCH  /api/devices/{id}                 {categories?, locale?, app_version?}
```

### 9.7.3 Universal links / app association (`/.well-known`)

```
GET  /.well-known/apple-app-site-association
GET  /.well-known/assetlinks.json
```

Static-file responses owned by the API server skeleton
(`plan-07-01-http-server-skeleton.md`). Content is generated from the
configured iOS bundle id + Android package id at boot.

### 9.8 Auth

Two surfaces, one identity:

- **Web** — argon2id-hashed passwords, login via `POST /api/auth/login`,
  httpOnly secure cookies (`sameSite=lax`), CSRF tokens for state-changing
  requests.
- **Mobile / desktop / TV** — argon2id login → short-lived bearer JWT
  (RS256, 15 min) + opaque refresh token (30 d, stored in
  Keychain/Keystore). Refresh via `POST /api/auth/refresh`.

The same JWT authenticates against the Streaming Service; Streaming
validates offline against the API's published JWKS
(`GET /api/.well-known/jwks.json`), so an in-flight watch session keeps
playing even if the API restarts.

**Single-user mode** still works: an env-configured admin token bypasses
the user table entirely; the UI stores it after first boot. This is the
zero-configuration path for self-hosters.

### 9.9 Inter-service gRPC

The internal gRPC schema (not exposed to clients) lives in
`shared/proto/`:

```protobuf
service Pipeline {
  rpc Embed(EmbedRequest) returns (EmbedResponse);
  rpc Transcribe(TranscribeRequest) returns (stream TranscribeEvent);
  rpc ListBackends(google.protobuf.Empty) returns (BackendList);
  rpc HealthCheck(google.protobuf.Empty) returns (HealthStatus);
}

service Streaming {
  rpc OpenSession(OpenSessionRequest) returns (OpenSessionResponse);
  rpc CloseSession(CloseSessionRequest) returns (google.protobuf.Empty);
  rpc EvictHashCache(EvictRequest) returns (EvictHashCacheResponse);
  rpc GetCapabilities(google.protobuf.Empty) returns (CapabilitiesResponse);
  rpc WatchQueue(WatchQueueRequest) returns (stream QueueEvent);
  rpc HealthCheck(google.protobuf.Empty) returns (HealthStatus);
}

message OpenSessionResponse {
  Session session = 1;
  CapabilitiesResponse capabilities = 2;          // returned for handshake convenience
}

message EvictHashCacheResponse {
  int32 entries_removed = 1;
  repeated string artifacts = 2;                  // cleared cache file paths
}
```

**Pipeline does not expose `Enqueue*`.** Bulk job control flows through
Postgres, not gRPC: the API enqueues by `INSERT INTO processing_jobs`
and Pipeline workers claim with `SELECT … FOR UPDATE SKIP LOCKED` (§7;
also §1.4). Synthetic transcribe runs (used by settings dry-run) reuse
`Pipeline.Transcribe` with a fixture audio source — there is no
`RunSyntheticTranscribe` RPC. Embedded subtitle extraction is folded
into the `extract` stage of the linear pipeline; clients do not invoke
it directly.

**Streaming extensions** (`GetCapabilities`, `WatchQueue`, richer
response messages) are non-breaking additions consumed by Epic 8's
streaming-server plan and Epic 7's `plan-07-18-grpc-clients.md`
client wrapper.

`shared/proto/` generates Go (via `protoc-gen-go-grpc`) and Python (via
`grpc_tools.protoc`) clients; both are checked in so neither side needs
the other's runtime to build.

---

## 10. Scalability

### 10.1 30 TB feasibility

| Dimension              | Scale assumption              | Storage / cost                          |
|------------------------|-------------------------------|-----------------------------------------|
| Source media           | 30 TB                          | unchanged (read-mostly).                |
| Transcripts (SQLite/PG)| ~15,000 h × 100 KB/h           | ~1.5 GB.                                |
| FTS5 index             | ~2× transcript size            | ~3 GB.                                  |
| Vector store (Chroma)  | ~150,000 segments × 1024 dim × 4 B | ~600 MB raw + HNSW overhead ≈ ~1.5 GB. |
| Subtitles (SRT+VTT)    | 2× ~50 KB/h                    | ~1.5 GB.                                |
| Thumbnails             | poster + sprite per video      | ~5–10 GB total.                         |
| HLS cache              | LRU-capped (default 50 GiB)    | bounded.                                |

**Total derived data: ~70 GiB**, comfortably on the same volume as the media.

### 10.2 Incremental processing

- Identity is `content_hash`, so a rename, move, or copy never re-processes.
- Each stage records the `(backend, model, config_hash)` it ran with.
  Re-processing happens only if the user explicitly bumps the model/backend
  on a library, and only the affected stages re-run.
- The scan loop is O(changed files), not O(library size), thanks to
  `watchdog` + a sparse periodic full sweep that compares only `(path, size,
  mtime)` against the DB.

### 10.3 Horizontal scale-out

Each service scales on its own axis:

- **API Service.** Stateless behind a single Postgres; add Go replicas
  behind any L7 load balancer. WebSocket fan-out uses Postgres
  LISTEN/NOTIFY so any replica receives every event.
- **Streaming Service.** Stateless across requests; sticky-session routing
  (consistent hash on `session_id`) keeps a session pinned to the box that
  owns its FFmpeg process. Sessions can be migrated by closing and
  reopening (the client resumes from `position_sec`).
- **Pipeline Service.** Workers are stateless; add boxes by pointing them
  at the same Postgres and shared media volume (NFS / SMB / S3+rclone).
  GPU-bound stages take a per-device lock; CPU stages run with
  per-host concurrency caps.
- **Postgres.** A single primary handles the entire household indefinitely
  (the dominant write rate is one row per ~10 s of audio transcribed). Add
  read replicas only if search QPS becomes a bottleneck — unlikely below
  thousands of users.
- **ChromaDB.** Persistent client is single-writer; for multi-writer
  scale-out, swap to a ChromaDB server deployment or to Qdrant behind the
  same `VectorStore` interface in the Pipeline Service.

### 10.4 Cost control

- Per-library budget caps for paid STT backends (`max_usd_per_month`).
- The job orchestrator refuses to claim API-backed transcribe jobs once the
  cap is hit; jobs return to `pending` with `not_before = next month`.
- A "dry run" cost estimate is shown before bulk re-processing.
- Streaming transcodes are CPU-budgeted: per-host max concurrent transcodes
  defaults to `(num_cores / 4)`; new sessions above the cap fall back to
  direct play with a quality cap, or queue with a "starting soon" UI hint.

---

## 11. Configuration

One config file per service, all reading shared `[database]` and
`[telemetry]` sections from a top-level include. `viper` (Go) and
`pydantic-settings` (Python) both support TOML + env override out of the
box, so the same file shape works across runtimes.

### 11.1 Layered configuration

```
defaults (in code)
  ↓ overridden by
/etc/maktaba/{service}.toml       (system-wide, per service)
  ↓ overridden by
$MAKTABA_HOME/{service}.toml      (per-user, per service)
  ↓ overridden by
environment variables (MAKTABA_{SERVICE}_*)
  ↓ overridden by
CLI flags
  ↓ overridden by
DB-stored settings (UI-editable)  (last write wins for runtime knobs)
```

DB-backed settings are limited to runtime knobs (search weights, cache
caps, library configs); secrets (DB URL, JWT keys, API keys) live only in
env or config file.

### 11.2 Example `api.toml`

```toml
[app]
home              = "/var/maktaba"
log_level         = "info"
admin_token_env   = "MAKTABA_ADMIN_TOKEN"

[server]
listen            = "0.0.0.0:8080"
public_origin     = "https://maktaba.local"

[database]
url               = "postgres://maktaba:@/maktaba?host=/var/run/postgresql"
# url             = "sqlite:///var/maktaba/maktaba.db"

[auth]
jwt_private_key_env = "MAKTABA_JWT_PRIVATE_KEY_PEM"
jwt_public_key_env  = "MAKTABA_JWT_PUBLIC_KEY_PEM"
access_ttl_sec      = 900
refresh_ttl_sec     = 2592000        # 30 days
argon2_memory_kib   = 65536
cookie_secure       = true
cookie_samesite     = "lax"

[search]
fts_weight        = 0.5
semantic_weight   = 0.5

[grpc]
pipeline_addr     = "127.0.0.1:50051"
streaming_addr    = "127.0.0.1:50052"
```

### 11.3 Example `streaming.toml`

```toml
[server]
listen            = "0.0.0.0:8081"
grpc_listen       = "127.0.0.1:50052"
public_origin     = "https://maktaba.local"

[database]
url               = "postgres://maktaba:@/maktaba?host=/var/run/postgresql"

[auth]
jwt_public_key_env = "MAKTABA_JWT_PUBLIC_KEY_PEM"   # offline JWT validation

[ffmpeg]
binary            = "/usr/local/bin/ffmpeg"
hwaccel           = "auto"             # videotoolbox | nvenc | qsv | none | auto
preset            = "veryfast"

[transcode]
max_concurrent    = 4
ladder            = ["1080p", "720p", "480p"]
hls_segment_sec   = 4

[cache]
root              = "/var/maktaba/cache/streaming"
max_gib           = 50
session_idle_sec  = 90
```

### 11.4 Example `pipeline.toml`

```toml
[app]
home              = "/var/maktaba"
log_level         = "info"

[grpc]
listen            = "127.0.0.1:50051"

[database]
url               = "postgresql+asyncpg://maktaba@localhost/maktaba"

[search]
embedding_model   = "intfloat/multilingual-e5-large"
embedding_device  = "auto"             # mlx | cuda | cpu | auto

[stt.default]
backend           = "whisper-mlx"
model             = "large-v3"
language          = "auto"
diarize           = false
word_timestamps   = true
initial_prompt_ar = "بسم الله الرحمن الرحيم"

[stt.backends.openai]
api_key_env       = "OPENAI_API_KEY"
model             = "whisper-1"
max_usd_per_month = 50

[workers]
concurrency       = { scan = 4, probe = 4, extract = 2, transcribe = 1, index = 4, thumbnail = 2 }
heartbeat_sec     = 60
job_timeout_min   = { transcribe = 240, default = 30 }

[libraries.lectures]
roots             = ["/mnt/media/lectures"]
language          = "ar"
stt_profile       = "default"

[libraries.films]
roots             = ["/mnt/media/films"]
language          = "auto"
stt_profile       = "default"
```

### 11.5 Secrets

- `MAKTABA_ADMIN_TOKEN` — bootstrap admin token (single-user mode).
- `MAKTABA_DATABASE_URL` — overrides `[database].url`.
- `MAKTABA_JWT_PRIVATE_KEY_PEM` / `MAKTABA_JWT_PUBLIC_KEY_PEM` — RS256
  keys; private key is read by the API only, public key by both API and
  Streaming.
- `OPENAI_API_KEY` (and equivalents) — per-backend, Pipeline only.

Secrets are never logged, never returned by `/api/settings`, and never
shared between services that don't need them (the Streaming Service never
sees the JWT private key or any STT backend keys).

JWT signing keys are read **only from environment variables**
(`MAKTABA_JWT_PRIVATE_KEY_PEM` / `MAKTABA_JWT_PUBLIC_KEY_PEM`); they are
not stored DB-encrypted. Rotation is handled by overlap (publish new
public key in JWKS, rotate signer, retire old key) rather than by an
in-DB key store.

### 11.6 Telemetry

Optional telemetry block (Epic 21). Defaults disabled.

```toml
[telemetry]
enabled              = false
otel_endpoint        = ""           # OTLP/gRPC; empty = no exporter
sample_ratio         = 0.05         # head-based; 1.0 = sample all
redact_attrs         = ["transcript_text", "search_query", "path"]
sentry_dsn_env       = "MAKTABA_SENTRY_DSN"      # Epic 21.5 error reporter
admin_listen         = "127.0.0.1:9100"          # /metrics + /healthz mux (Epic 21.4)
```

The `admin_listen` port hosts the merged admin mux (metrics, health,
readiness) — owned by `plan-21-04`; `plan-21-02` registers `/metrics`
against it.

---

## 12. Project Structure & Deployment

### 12.1 Monorepo layout

The repo is one tree with one language per top-level subdir. Each backend
language has its own build tool and dependency graph; `shared/` holds the
contracts (proto schemas, SQL migrations, fixtures) that cross language
boundaries.

```
Maktaba/
├── README.md
├── Makefile                          # top-level: bring up everything for dev
├── docker-compose.yml                # full-stack local: api + streaming + pipeline + postgres + caddy
│
├── api/                              # Go — REST + GraphQL + WebSocket
│   ├── go.mod
│   ├── cmd/
│   │   └── api/main.go               # entry point: `go run ./cmd/api`
│   ├── internal/
│   │   ├── config/                   # viper-based loader
│   │   ├── http/                     # chi router, middleware, problem+json
│   │   ├── graphql/                  # gqlgen resolvers
│   │   ├── ws/                       # WebSocket fan-out + Postgres LISTEN
│   │   ├── auth/                     # JWT issuance, argon2id, JWKS
│   │   ├── db/                       # sqlc-generated queries
│   │   ├── domain/                   # libraries, videos, search orchestration
│   │   ├── grpcclient/               # pipeline + streaming clients
│   │   └── jobs/                     # enqueue, pause/resume, status
│   ├── sqlc.yaml
│   └── Dockerfile
│
├── streaming/                        # Go — HLS / DASH / direct play
│   ├── go.mod
│   ├── cmd/
│   │   └── streaming/main.go
│   ├── internal/
│   │   ├── config/
│   │   ├── http/                     # signed-URL middleware, range serving
│   │   ├── grpcserver/               # OpenSession / CloseSession
│   │   ├── sessions/                 # session store + reaper
│   │   ├── transcode/                # FFmpeg orchestration, hwaccel detection
│   │   ├── manifest/                 # HLS + DASH writers
│   │   ├── subtitles/                # live VTT renderer from DB
│   │   └── cache/                    # LRU on-disk cache
│   └── Dockerfile
│
├── pipeline/                         # Python — ML/AI workers + gRPC server
│   ├── pyproject.toml                # uv / hatch
│   ├── src/maktaba_pipeline/
│   │   ├── __init__.py
│   │   ├── cli.py                    # `maktaba-pipeline serve | worker | scan | …`
│   │   ├── settings.py               # pydantic-settings
│   │   ├── grpc_server.py            # asyncio gRPC server (Embed, Transcribe, …)
│   │   ├── db/                       # asyncpg / aiosqlite, mirrors sqlc queries
│   │   ├── domain/                   # identity (BLAKE3), state machine, search fusion
│   │   ├── pipeline/
│   │   │   ├── stages/               # scan, probe, extract, transcribe, …
│   │   │   └── runner.py             # claim loop, heartbeat, retry
│   │   ├── stt/                      # backends: whisper_mlx, faster_whisper, openai_api
│   │   ├── search/                   # fts adapter, chroma adapter, embeddings, fusion
│   │   ├── media/                    # ffmpeg wrappers, thumbnails, subtitle writers
│   │   ├── library/                  # watcher, auto-tag, categorization
│   │   └── tasks/                    # reaper, cache GC, nightly recluster
│   ├── tests/
│   └── Dockerfile
│
├── web/                              # TypeScript — React + Vite PWA
│   ├── package.json
│   ├── vite.config.ts
│   ├── index.html
│   ├── public/
│   │   └── manifest.webmanifest
│   ├── src/
│   │   ├── main.tsx
│   │   ├── routes/                   # TanStack Router routes
│   │   ├── pages/
│   │   ├── components/               # Vidstack player, transcript sidebar, …
│   │   ├── lib/
│   │   │   ├── api.ts                # REST client
│   │   │   ├── graphql.ts            # graphql-request + generated types
│   │   │   └── ws.ts
│   │   └── i18n/
│   │       ├── ar.json
│   │       └── en.json
│   └── Dockerfile
│
├── apps/
│   ├── mobile/                       # Capacitor — wraps web/
│   │   ├── ios/                      # generated Xcode project
│   │   ├── android/                  # generated Gradle project
│   │   ├── capacitor.config.ts
│   │   └── plugins/native-player/    # AVPlayer / ExoPlayer handoff
│   ├── desktop/                      # Tauri — wraps web/
│   │   ├── src-tauri/
│   │   │   ├── Cargo.toml
│   │   │   ├── tauri.conf.json
│   │   │   └── src/main.rs
│   │   └── icons/
│   ├── tvos/                         # Swift / SwiftUI native app
│   │   ├── Maktaba.xcodeproj/
│   │   └── Sources/
│   │       ├── App/
│   │       ├── Features/             # Home, Library, Player, Search, Settings
│   │       └── API/                  # Apollo-generated GraphQL client
│   └── androidtv/                    # Kotlin / Compose for TV
│       ├── build.gradle.kts
│       └── src/main/java/io/maktaba/tv/
│
├── shared/
│   ├── proto/                        # gRPC schemas — one source of truth
│   │   ├── pipeline.proto
│   │   ├── streaming.proto
│   │   ├── common.proto
│   │   ├── gen/go/                   # checked-in generated Go
│   │   └── gen/python/               # checked-in generated Python
│   ├── graphql/
│   │   └── schema.graphql            # consumed by gqlgen + graphql-codegen
│   ├── db/
│   │   ├── migrations/               # goose-format SQL, shared by Go and Python
│   │   │   ├── 0001_init.sql
│   │   │   ├── 0002_jobs.sql
│   │   │   └── ...
│   │   └── queries/                  # sqlc input + asyncpg reference
│   └── fixtures/
│       └── samples/                  # short royalty-free clips for tests
│
├── deploy/
│   ├── docker/
│   │   ├── caddy/Caddyfile
│   │   └── postgres/init.sql
│   ├── compose/
│   │   ├── docker-compose.yml        # the canonical self-host bundle
│   │   ├── docker-compose.mac.yml    # overlay: bind to host FFmpeg + MLX
│   │   └── docker-compose.dev.yml    # overlay: live-reload mounts
│   ├── homebrew/
│   │   └── maktaba.rb                # `brew install maktaba/tap/maktaba`
│   └── launchd/                      # macOS service plists
│       ├── io.maktaba.api.plist
│       ├── io.maktaba.streaming.plist
│       └── io.maktaba.pipeline.plist
│
└── specs/
    └── architecture.md               # this document
```

### 12.2 Build & dev workflow

The `Makefile` exposes one verb per common operation; under the hood it
delegates to each language's native tooling:

```
make dev              # docker compose -f deploy/compose/docker-compose.yml \
                      #                -f deploy/compose/docker-compose.dev.yml up
make build            # parallel: go build (api, streaming) + uv build pipeline + vite build web
make test             # go test ./... + pytest pipeline + vitest web
make proto            # regenerate gRPC clients from shared/proto into checked-in dirs
make migrate          # goose up against DATABASE_URL
make lint             # golangci-lint + ruff + tsc + eslint
make apps             # build mobile (capacitor sync), desktop (tauri build), tvos (xcodebuild)
```

There is no top-level "monorepo tool" (Nx, Bazel, Turborepo) — each
language's native toolchain stays in charge of its own subtree, and the
`Makefile` orchestrates across them.

### 12.3 Per-service CLIs

Each Go binary takes flags directly; the Python service has its own CLI:

```
# API Service (Go)
maktaba-api serve [--config /etc/maktaba/api.toml]
maktaba-api migrate                 # goose-driven schema migrations
maktaba-api adduser <username>      # interactive password prompt

# Streaming Service (Go)
maktaba-streaming serve [--config /etc/maktaba/streaming.toml]
maktaba-streaming probe <video_id>  # debug: dump capabilities + cached probe
maktaba-streaming gc                # one-shot cache sweep

# Pipeline Service (Python)
maktaba-pipeline serve              # gRPC server + worker pool (default)
maktaba-pipeline worker --stages transcribe,index
maktaba-pipeline scan --library NAME
maktaba-pipeline reprocess --library NAME --from-stage transcribe
maktaba-pipeline doctor             # ffmpeg, GPU, DB, write perms, model cache
```

### 12.4 Deployment

**Mac single-host (the user's primary target).** Two paths, both supported:

1. **`docker compose up`** — One YAML brings the four containers (Postgres,
   API, Streaming, Pipeline) plus a Caddy reverse proxy that terminates
   TLS and routes `/api`, `/graphql`, `/ws`, `/stream`, and `/` to the
   right service. The compose overlay `docker-compose.mac.yml` bind-mounts
   the host's FFmpeg and exposes the GPU/Neural Engine for MLX
   transcription. This is the recommended path.
2. **`brew install maktaba`** — A Homebrew tap installs the three native
   binaries (Go API, Go Streaming, Python pipeline as a `uv`-managed
   venv), creates `/usr/local/var/maktaba/`, drops three `launchd` plists,
   and starts them. No Docker, no Postgres-in-a-container — uses the
   user's local Postgres or installs one. This is the "I already have
   Postgres and want native MLX" path.

Both paths land at `https://maktaba.local` (mDNS) by default; Caddy's
local-CA mode auto-issues a trusted cert to the machine's keychain.

**Linux self-host (NAS, workstation).** `docker compose up` is the only
supported path; Caddy auto-issues Let's Encrypt certs against the user's
domain.

**Multi-host scale-out (future).** Each binary can be promoted to its own
host:
- Postgres on its own VM with WAL archiving.
- N copies of API behind any L7 LB.
- M copies of Streaming behind a sticky-session LB (consistent hash on
  `session_id` cookie).
- K copies of Pipeline pointed at the same shared media volume; GPU stages
  are pinned to GPU hosts.

There is no Kubernetes requirement at any scale; Compose + a small
`systemd` unit per host is sufficient through the lifetime of v1.

### 12.5 Conventions

**Go (api, streaming):**
- `errors.Is`/`errors.As` everywhere; never string-match on errors.
- Context propagation in every public function.
- `slog` with one global logger; no `fmt.Println`.
- Generated code (sqlc, gqlgen, protobuf) lives next to the file that
  consumes it and is checked in.

**Python (pipeline):**
- `from __future__ import annotations` everywhere; PEP 695 generics.
- Async by default; sync only at FFmpeg subprocess and Whisper boundaries.
- All paths through `pathlib.Path`; never raw strings.
- All times stored as UTC `datetime`; client converts.

**TypeScript (web):**
- Strict mode on; no `any` outside generated types.
- `tsc --noEmit` runs in CI; `eslint` and `prettier` enforce style.
- All API access via generated types from `shared/graphql/schema.graphql`.

**Cross-cutting:**
- All times stored UTC; clients render with the user's timezone.
- All UUIDs are v7 (sortable); never v4 in user-visible IDs.
- Tests for each service are runnable in isolation against a SQLite test
  DB; integration tests run against Postgres in CI.

---

## Appendix A — End-to-end data flow (single video)

```
 User drops video.mkv into /mnt/media/lectures/
              │
              ▼
 ┌──────────────────────────────────────────────────────────────────────┐
 │  watchdog event → debounced 2 s → scanner inserts videos row          │
 │  state = DISCOVERED, content_hash computed                            │
 └──────────────────────────────────────────────────────────────────────┘
              │
              ▼
 ┌──────────────────────────────────────────────────────────────────────┐
 │  job(probe) claimed → ffprobe → media_info, audio_tracks rows         │
 │  state → PROBED                                                       │
 └──────────────────────────────────────────────────────────────────────┘
              │
              ▼
 ┌──────────────────────────────────────────────────────────────────────┐
 │  job(extract) claimed → ffmpeg pipes 16k mono PCM into                │
 │  job(transcribe) — same worker, GPU lock held                         │
 │  → for each STT segment (5–30 s of audio):                            │
 │      one DB tx commits the segment row + advances                     │
 │      processing_jobs.last_segment_end_sec + heartbeats.               │
 │      User can pause / process can crash between any two segments;     │
 │      resume picks up from last_segment_end_sec exactly.               │
 │  → on completion: state → TRANSCRIBED                                 │
 └──────────────────────────────────────────────────────────────────────┘
              │
              ▼
 ┌──────────────────────────────────────────────────────────────────────┐
 │  job(subtitle_gen) → writes .srt / .vtt sidecars (atomic rename)      │
 │  job(index) → FTS5 inserts + ChromaDB upserts                         │
 │  job(thumbnail) → poster + sprite to cache                            │
 │  state → READY                                                        │
 └──────────────────────────────────────────────────────────────────────┘
              │
              ▼
 ┌──────────────────────────────────────────────────────────────────────┐
 │  WS /ws/library/{id} broadcasts new ready video                       │
 │  UI grid updates; user can immediately stream + search inside it.     │
 └──────────────────────────────────────────────────────────────────────┘
```

## Appendix B — Out of scope (v1)

- Multi-tenant SaaS hosting and billing.
- Live ingestion (streaming sources). Maktaba is for archives, not live feeds.
- Translation between languages on the fly (transcripts are stored in source
  language; translation can be added as an extra stage later).
- DRM-protected content.
- Cast / AirPlay receiver targets (clients can AirPlay/Cast *to* a TV, but
  Maktaba does not run a Cast Receiver app).

**In scope but staged later:** the TV apps (tvOS, Android TV) ship after the
PWA + mobile + desktop wave; native TV codebases are real work and gated on
the API surface stabilizing.

## Appendix C — Open questions to resolve before v1

1. **Diarization quality vs. cost** — `pyannote.audio` is heavyweight; do we
   ship it on by default, opt-in, or as a separate "Pro" stage?
2. **Embedding model size** — `multilingual-e5-large` is 560M params and slow
   without a GPU. Do we ship `e5-base` as the default and let users upgrade?
3. **PostgreSQL vs SQLite default** — recommend the docker-compose path
   (Postgres) and document SQLite as "single user, low write load"?
4. **Auth in single-user mode** — bootstrap token only, or full local user
   account? Token is simpler but less safe on a shared LAN.
5. **GPU sharing across boxes** — do we bother with a remote-worker model in
   v1, or assume one machine owns transcription?
6. **GraphQL vs. REST priority** — ship GraphQL from day one (more work, but
   the right shape for TV apps later) or REST-first and add GraphQL when the
   TV apps land?
7. **Tauri 2 maturity** — confirm the WebView-based desktop story handles
   our HLS player needs across macOS/Windows/Linux, or fall back to Electron
   for desktop-only.
