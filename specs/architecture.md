# Maktaba — Architecture

> Self-hosted media intelligence platform. Plex-class library and streaming, plus
> "every word in every video is searchable." Arabic-first, language-agnostic.

---

## 1. System Overview

### 1.1 What Maktaba is

Maktaba is a self-hosted application that turns a folder tree of video files
(lectures, sermons, interviews, archives, films) into a searchable, streamable,
intelligently-organized library. It is positioned as a Plex alternative, but
its differentiator is **content intelligence**: every video is transcribed,
segmented, indexed for full-text and semantic search, and exposed through a
web UI where the user can jump to the exact second a phrase was spoken.

The target deployment is a single user / single household with 30 TB+ of
content on a NAS or workstation, with the option to scale horizontally to
multi-user installations later. The first-class language is Arabic (مكتبة =
"library"), with full Unicode and bidirectional-text correctness throughout,
but the STT and indexing layers are language-agnostic.

### 1.2 Top-level components

```
                        ┌──────────────────────────────────────────────┐
                        │                  Web UI (SPA)                │
                        │     React + TypeScript + Vite + Tailwind     │
                        └───────────────┬───────────────┬──────────────┘
                                        │ REST          │ WebSocket
                                        ▼               ▼
                        ┌──────────────────────────────────────────────┐
                        │           API Gateway (FastAPI)              │
                        │   /library  /search  /stream  /jobs  /admin  │
                        └─┬─────────┬──────────┬──────────┬────────┬───┘
                          │         │          │          │        │
                          ▼         ▼          ▼          ▼        ▼
                  ┌──────────┐ ┌─────────┐ ┌────────┐ ┌──────┐ ┌────────┐
                  │ Library  │ │ Search  │ │ Stream │ │ Jobs │ │ Config │
                  │ Service  │ │ Service │ │ Service│ │  API │ │  API   │
                  └────┬─────┘ └────┬────┘ └───┬────┘ └──┬───┘ └────┬───┘
                       │            │          │         │          │
                       ▼            ▼          ▼         ▼          ▼
       ┌────────────────────────────────────────────────────────────────────┐
       │                        Domain Core (pure Python)                   │
       │   models · pipeline contracts · scheduling · hashing · settings    │
       └─────┬──────────────┬───────────────┬──────────────┬────────────────┘
             │              │               │              │
             ▼              ▼               ▼              ▼
     ┌─────────────┐ ┌─────────────┐ ┌────────────┐ ┌──────────────┐
     │  Metadata   │ │  ChromaDB   │ │   FTS5     │ │ Object Cache │
     │ (Postgres / │ │  (vectors,  │ │ (SQLite,   │ │  (thumbs,    │
     │   SQLite)   │ │  on-disk)   │ │  segments) │ │   HLS, SRT)  │
     └─────────────┘ └─────────────┘ └────────────┘ └──────────────┘

                     ─── separate process group ───
       ┌────────────────────────────────────────────────────────────────────┐
       │                       Worker Pool (background)                     │
       │   Scanner · Probe · AudioExtract · Transcribe · Index · Thumbnails │
       │   coordinated through the Job Store; GPU-bound stages serialized   │
       └────────────────────────────────────────────────────────────────────┘
                                        ▲
                                        │ filesystem events (watchdog)
       ┌────────────────────────────────┴───────────────────────────────────┐
       │                         Media Storage (read-mostly)                │
       │              /libraries/{name}/...   ·   /var/maktaba/             │
       └────────────────────────────────────────────────────────────────────┘
```

The **API process** and the **worker process** are separate so that long
transcription jobs cannot stall HTTP responses, and so the worker can be
restarted, scaled, or pinned to specific hardware (e.g., an Apple Silicon box
with MLX) independently of the UI.

### 1.3 Design principles

- **Modular stages over monolithic pipelines.** Each pipeline stage implements
  a small interface (`run(item) -> result`), and stages communicate only
  through the job store. Replacing the STT engine should not touch the
  scanner, the indexer, or the UI.
- **Content-addressable identity.** A video is identified by its
  `content_hash` (BLAKE3 of the first + last 4 MiB plus file size). Renaming,
  moving, or copying a file does not cause re-processing.
- **Idempotent, resumable jobs.** Every stage can be re-run safely. Crash
  during transcription leaves no partial subtitle file in the library —
  outputs are written to a temp path and atomically renamed.
- **Sidecar outputs.** Generated artifacts (`.srt`, `.vtt`, `.json` segments,
  thumbnails) live next to the source file in a hidden `.maktaba/` directory,
  so the library remains portable and a Plex/VLC user can still consume the
  subtitles directly.
- **Graceful degradation.** Vector search down? Fall back to FTS. STT
  unavailable? The video is still browsable and streamable. Thumbnails
  missing? Fall back to a placeholder. No single component failure should
  break browsing.

---

## 2. Tech Stack

| Layer              | Choice                                       | Rationale                                                                 |
|--------------------|----------------------------------------------|---------------------------------------------------------------------------|
| Language           | Python 3.12+                                 | Async-native, ecosystem for ML/media, matches existing skeleton.          |
| API                | FastAPI + Uvicorn (or Hypercorn for HTTP/2)  | Async, OpenAPI-first, WebSockets, Pydantic validation.                    |
| Domain models      | Pydantic v2 + SQLAlchemy 2.x                 | Single source of truth between API and DB.                                |
| Metadata DB        | PostgreSQL 16 (prod) · SQLite (single-user)  | Same SQLAlchemy models; Postgres scales, SQLite is zero-ops for home use. |
| Full-text search   | SQLite FTS5 with `unicode61` + `arabic` rules · or Postgres `tsvector` | FTS5 is excellent for sidecar use; Postgres FTS for multi-user.           |
| Vector search      | ChromaDB (persistent client, DuckDB+Parquet) | On-disk, embedded, no extra service.                                      |
| Embeddings         | `multilingual-e5-large` (default) · pluggable | Strong on Arabic and English, sentence-level.                             |
| Job queue          | Custom Postgres/SQLite-backed queue         | One DB, one transaction, full visibility, easy to debug.                  |
| Filesystem watch   | `watchdog`                                   | Cross-platform inotify/FSEvents/ReadDirectoryChangesW.                    |
| Media probe        | `ffprobe` via `ffmpeg-python`                | Already in deps; canonical metadata source.                               |
| Audio extraction   | FFmpeg (CLI)                                 | Streaming pipe to STT, no intermediate file when possible.                |
| STT engine         | Whisper (MLX on Apple Silicon, CUDA elsewhere) | Already in deps; best Arabic OSS model; pluggable.                        |
| Streaming          | HLS via FFmpeg segmenter; direct MP4 fallback | Browser-native, supports adaptive bitrate later.                          |
| Thumbnails         | FFmpeg `select` filter + `Pillow`            | One sprite + chapter posters per video.                                   |
| Frontend           | React 18 + TypeScript + Vite + Tailwind      | Mature; RTL-friendly with `dir="rtl"`.                                    |
| Player             | Video.js or Vidstack                         | HLS, sidecar VTT, captions, chapter markers out of the box.               |
| Packaging          | uv / Hatch                                   | Fast resolver; matches existing pyproject.                                |
| Container          | Docker Compose (api, worker, postgres)       | Reproducible single-host deploy.                                          |
| Config             | Pydantic Settings + TOML + env override      | Layered: defaults → file → env → CLI flag.                                |
| Logging            | `structlog` + JSON sink                      | Structured, grep-able, ready for ELK/Loki.                                |
| Telemetry          | OpenTelemetry (optional)                     | Off by default; opt-in for prod.                                          |

**Why not Celery/Redis?** A 30 TB single-user library has dozens of jobs
in flight, not millions. A Postgres-backed queue with `SELECT … FOR UPDATE
SKIP LOCKED` gives us atomic claim, full visibility through the same DB the
UI already reads, and one fewer service to run. Redis can be added later as a
cache for hot library queries; it is not required for correctness.

---

## 3. Core Pipeline

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
which advances `videos.state` along a finite-state machine:

```
DISCOVERED → PROBED → AUDIO_EXTRACTED → TRANSCRIBED → INDEXED → THUMBNAILED → READY
                ↘ FAILED (per-stage, with retry counter and backoff)
```

The orchestrator picks the next eligible stage for each video by joining
`videos` against `processing_jobs`. This makes "where is video X stuck?" a
trivial SQL query.

### 3.1 Scanner

**Trigger:** `watchdog` filesystem event, or periodic full sweep
(default every 6 h), or manual `POST /api/libraries/{id}/scan`.

**Inputs:** library root path, ignore globs, supported extensions.

**Outputs:** rows in `videos` for newly-seen files, in state `DISCOVERED`.

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

## 4. Media Server

Maktaba serves video to the browser through three modes, in preference order:

1. **Direct play.** If the file is MP4/H.264/AAC and the browser supports it,
   stream the file with HTTP Range requests. Zero CPU cost.
2. **HLS remux (no transcode).** If the codecs are browser-compatible but the
   container is MKV/AVI, FFmpeg re-segments on the fly into HLS without
   re-encoding (`-c copy`). Low CPU.
3. **HLS transcode.** Fallback for HEVC/AV1/etc. on browsers without support.
   FFmpeg encodes to H.264 + AAC. CPU-expensive; capped concurrency.

Each rendition is keyed by `(content_hash, resolution, codec_profile)` and
cached under `/var/maktaba/cache/hls/{hash[:2]}/{hash}/...`. Old segments are
GC'd by an LRU policy (default 50 GiB cap).

**Subtitles** are served as VTT sidecars referenced from the HLS manifest
(`#EXT-X-MEDIA:TYPE=SUBTITLES`). The browser handles rendering — Maktaba
never burns subtitles into the video stream. Sidecar `.srt` files inside the
library folder are auto-discovered and exposed alongside generated ones.

**Thumbnails:**
- One **poster** per video (auto-selected at 10% of duration, ignoring black
  frames via `blackdetect`).
- One **sprite sheet** of preview thumbs at 10-second intervals, displayed
  on player scrub.
- Optional **chapter posters**, one per detected chapter.

**Chapter detection** runs in the indexer stage:
1. Use embedded chapters from the container if present.
2. Else, infer chapters from transcript-level topic shifts (cosine drop
   between adjacent segment embeddings > threshold) → coarse chapters.
3. Cap at one chapter per ~3 minutes of content; let the user override.

---

## 5. Library Management

A **library** is a named collection of root paths sharing a configuration
profile. Libraries are first-class to support setups like:

- `Lectures` — `/mnt/media/lectures/`, language=ar, STT=whisper-mlx, large model.
- `Films` — `/mnt/media/films/`, language=en, STT=whisper-cpu, tiny model.
- `Archive` — `/mnt/cold/archive/`, scan-only, no STT.

### 5.1 Folder watching

Each library spawns one `watchdog` observer. Events are debounced (default
2 s) so that copies in progress are not picked up mid-write. A file is
considered settled when its size has not changed for one debounce interval.

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

## 6. Web UI

A single-page React app served from the API at `/`. The same FastAPI process
serves the static bundle in production; in dev, Vite proxies `/api` to it.

### 6.1 Pages

- **Home** — recently added, in-progress, recommended ("more like what you
  watch").
- **Library** — paginated grid, filterable by language, type, duration,
  speaker, tag, library.
- **Video detail** — player, transcript-as-sidebar (clickable, syncs with
  playback), chapter list, metadata, related videos.
- **Search** — single search box, hybrid results with highlighted snippets
  and timestamp deep-links (`/watch/{id}?t=3725.4`).
- **Speakers** — per-speaker page with all known appearances.
- **Tags / Collections** — browse and manage.
- **Queue** — live view of the worker pool and processing jobs (WebSocket).
- **Settings** — libraries, STT backends, language preferences, search
  weights, cache caps, integrations.

### 6.2 Internationalization & RTL

The shell supports `dir="rtl"` and `dir="ltr"` per-route based on the active
UI language. Transcript snippets render with Unicode bidi isolates
(`⁨...⁩`) so that mixed Arabic/English text aligns correctly even
when results from different languages are interleaved. The Arabic UI strings
are first-class translations, not afterthoughts.

### 6.3 Live updates

- WebSocket `/ws/jobs` — job state changes, progress percent, ETA.
- WebSocket `/ws/library/{id}` — newly discovered or processed videos.
- Server-sent events as a fallback where WebSocket is blocked.

---

## 7. Batch Processing

### 7.1 Job store

```sql
CREATE TABLE processing_jobs (
    id            BIGSERIAL PRIMARY KEY,
    video_id      UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    stage         TEXT NOT NULL,            -- scan|probe|extract|transcribe|index|thumb
    state         TEXT NOT NULL,            -- pending|claimed|running|done|failed|cancelled
    priority      INT  NOT NULL DEFAULT 100,-- lower = sooner
    attempts      INT  NOT NULL DEFAULT 0,
    max_attempts  INT  NOT NULL DEFAULT 3,
    claimed_by    TEXT,                     -- worker id
    claimed_at    TIMESTAMPTZ,
    not_before    TIMESTAMPTZ,              -- backoff target
    error         TEXT,
    progress      REAL,                     -- 0..1
    metrics       JSONB,                    -- runtime, model, etc.
    created_at    TIMESTAMPTZ DEFAULT now(),
    finished_at   TIMESTAMPTZ
);
CREATE INDEX ON processing_jobs (state, priority, not_before);
CREATE INDEX ON processing_jobs (video_id, stage);
```

### 7.2 Claim loop

Each worker runs:

```sql
UPDATE processing_jobs
   SET state = 'claimed', claimed_by = $worker_id, claimed_at = now()
 WHERE id = (
   SELECT id FROM processing_jobs
    WHERE state = 'pending'
      AND (not_before IS NULL OR not_before <= now())
      AND stage = ANY($supported_stages)
    ORDER BY priority, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
 )
RETURNING *;
```

`SKIP LOCKED` lets N workers contend without blocking each other. On SQLite
(single-user), the workers are in-process and use an asyncio lock instead.

### 7.3 Concurrency model

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

### 7.4 Priority scheduling

Priority is an integer; lower wins. Defaults:
- 50 — user-initiated (clicked "Process now")
- 100 — newly discovered
- 200 — re-process (model upgrade, settings change)
- 500 — bulk backfill

The user can override per-video or per-library. The UI exposes
"Move to front of queue" on every video.

### 7.5 Resume on crash

- `claimed` jobs older than 30 min with no heartbeat are reverted to
  `pending` by a cron tick. Heartbeats are written every 60 s while running.
- Stages write outputs to `…/.tmp/{uuid}` and `os.replace()` to the final
  path on success. A crash mid-write leaves a stray temp file that the next
  scan removes.
- `transcribe` checkpoints every N segments to a JSON file in the sidecar
  directory; on resume, transcription continues from the last checkpoint
  rather than from zero.

### 7.6 Throughput estimate (30 TB)

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
    roots        TEXT[] NOT NULL,         -- absolute paths
    settings     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ DEFAULT now(),
    updated_at   TIMESTAMPTZ DEFAULT now()
);

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
    created_at         TIMESTAMPTZ DEFAULT now(),
    updated_at         TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX ON videos (library_id, state);
CREATE INDEX ON videos (detected_language);

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
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id       UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    audio_track_id BIGINT NOT NULL REFERENCES audio_tracks(id),
    language       TEXT NOT NULL,
    backend        TEXT NOT NULL,         -- whisper-mlx, openai-api, ...
    model          TEXT NOT NULL,         -- large-v3, ...
    backend_version TEXT,
    word_level     BOOLEAN NOT NULL,
    diarized       BOOLEAN NOT NULL,
    quality_score  REAL,                  -- aggregate confidence
    created_at     TIMESTAMPTZ DEFAULT now(),
    UNIQUE (video_id, audio_track_id, backend, model)
);

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
    id    BIGSERIAL PRIMARY KEY,
    name  TEXT NOT NULL UNIQUE
);

CREATE TABLE video_tags (
    video_id UUID REFERENCES videos(id) ON DELETE CASCADE,
    tag_id   BIGINT REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (video_id, tag_id)
);

CREATE TABLE collections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    is_smart    BOOLEAN NOT NULL DEFAULT false,
    smart_query JSONB,                   -- filter spec when is_smart
    created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE collection_items (
    collection_id UUID REFERENCES collections(id) ON DELETE CASCADE,
    video_id      UUID REFERENCES videos(id) ON DELETE CASCADE,
    position      INT NOT NULL,
    PRIMARY KEY (collection_id, video_id)
);

CREATE TABLE speakers (
    id          BIGSERIAL PRIMARY KEY,
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name        TEXT,                    -- null = unknown
    voiceprint  BYTEA,                   -- d-vector / x-vector
    UNIQUE (library_id, name)
);

CREATE TABLE segment_speakers (
    segment_id BIGINT REFERENCES transcript_segments(id) ON DELETE CASCADE,
    speaker_id BIGINT REFERENCES speakers(id),
    confidence REAL,
    PRIMARY KEY (segment_id, speaker_id)
);
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
    query      JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);
```

---

## 9. API Design

All endpoints are JSON. Errors follow RFC 9457 problem+json. Pagination is
cursor-based (`?cursor=...&limit=...`) with `next` and `prev` returned in the
response envelope.

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

```
GET    /api/stream/{video_id}/manifest.m3u8
GET    /api/stream/{video_id}/seg/{n}.ts
GET    /api/stream/{video_id}/direct      → 206 Partial Content with Range
GET    /api/stream/{video_id}/subs/{lang}.vtt
GET    /api/stream/{video_id}/poster.jpg
GET    /api/stream/{video_id}/sprite.json   → sprite map
```

Range-request handling supports HEAD, conditional requests, and partial
content properly so Safari (the strictest) plays back without reload loops.

### 9.5 Processing

```
GET    /api/jobs                         ?state&stage&video&cursor
GET    /api/jobs/{id}
POST   /api/jobs/{id}/cancel
POST   /api/jobs/{id}/retry              → resets attempts
GET    /api/queue/stats                  → per-stage counts and ETA
WS     /ws/jobs                          → live job state
```

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
```

### 9.8 Auth

Single-user mode: an env-configured admin token; UI stores it after first
boot. Multi-user mode: argon2id passwords, session cookies (httpOnly,
sameSite=lax), CSRF tokens for state-changing requests.

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

- The API process is stateless behind a single Postgres; add replicas
  freely.
- Workers are stateless; add boxes by pointing them at the same Postgres and
  shared media volume (NFS / SMB / S3+rclone).
- ChromaDB persistent client is single-writer; for multi-writer scale-out,
  swap the embedding function for a Chroma server deployment or for Qdrant
  behind the same `VectorStore` interface.

### 10.4 Cost control

- Per-library budget caps for paid STT backends (`max_usd_per_month`).
- The job orchestrator refuses to claim API-backed transcribe jobs once the
  cap is hit; jobs return to `pending` with `not_before = next month`.
- A "dry run" cost estimate is shown before bulk re-processing.

---

## 11. Configuration

### 11.1 Layered configuration

```
defaults (in code)
  ↓ overridden by
/etc/maktaba/config.toml          (system-wide)
  ↓ overridden by
$MAKTABA_HOME/config.toml         (per-user)
  ↓ overridden by
environment variables (MAKTABA_*)
  ↓ overridden by
CLI flags
  ↓ overridden by
DB-stored settings (UI-editable)  (last write wins for runtime knobs)
```

DB-backed settings are limited to runtime knobs (search weights, cache
caps, library configs); secrets (API keys, DB URLs) live only in env or
config file.

### 11.2 Example `config.toml`

```toml
[app]
home              = "/var/maktaba"
log_level         = "info"
admin_token_env   = "MAKTABA_ADMIN_TOKEN"

[database]
url               = "postgresql+psycopg://maktaba@localhost/maktaba"
# url             = "sqlite+aiosqlite:////var/maktaba/maktaba.db"

[search]
fts_weight        = 0.5
semantic_weight   = 0.5
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

[media]
ffmpeg            = "/usr/local/bin/ffmpeg"
ffprobe           = "/usr/local/bin/ffprobe"
hls_cache_gib     = 50
thumb_interval_sec= 10

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

### 11.3 Secrets

- `MAKTABA_ADMIN_TOKEN` — bootstrap admin token.
- `MAKTABA_DATABASE_URL` — DB URL with credentials.
- `OPENAI_API_KEY` (and equivalents) — per-backend.

Secrets are never logged, never returned by `/api/settings`, and never sent
to the worker over the wire — workers read them from their own env.

---

## 12. Project Structure

```
maktaba/
├── pyproject.toml
├── README.md
├── docker/
│   ├── Dockerfile.api
│   ├── Dockerfile.worker
│   └── docker-compose.yml
├── alembic/                         # DB migrations
│   ├── env.py
│   └── versions/
├── specs/
│   └── architecture.md              # this document
├── frontend/                        # standalone React app
│   ├── package.json
│   ├── vite.config.ts
│   ├── index.html
│   └── src/
│       ├── main.tsx
│       ├── routes/
│       ├── components/
│       ├── lib/api.ts
│       └── i18n/
│           ├── ar.json
│           └── en.json
├── maktaba/                         # Python package
│   ├── __init__.py
│   ├── cli.py                       # `maktaba serve | worker | scan | …`
│   ├── settings.py                  # Pydantic Settings, layered config
│   ├── logging.py
│   ├── db/
│   │   ├── __init__.py
│   │   ├── engine.py                # async engine + session
│   │   ├── models/
│   │   │   ├── library.py
│   │   │   ├── video.py
│   │   │   ├── transcript.py
│   │   │   ├── job.py
│   │   │   ├── collection.py
│   │   │   ├── speaker.py
│   │   │   └── user.py
│   │   └── repositories/            # data access; no business logic
│   │       ├── videos.py
│   │       ├── transcripts.py
│   │       └── jobs.py
│   ├── domain/                      # pure-Python core
│   │   ├── identity.py              # content hashing
│   │   ├── states.py                # video state machine
│   │   ├── pipeline.py              # Stage protocol + orchestration
│   │   └── search.py                # hybrid fusion, query model
│   ├── pipeline/
│   │   ├── stages/
│   │   │   ├── scan.py
│   │   │   ├── probe.py
│   │   │   ├── audio_extract.py
│   │   │   ├── transcribe.py        # delegates to stt backends
│   │   │   ├── subtitle_gen.py
│   │   │   ├── index.py
│   │   │   ├── thumbnail.py
│   │   │   └── chapter_detect.py
│   │   └── runner.py                # worker loop, claim/heartbeat/retry
│   ├── stt/
│   │   ├── base.py                  # STTBackend protocol + types
│   │   ├── registry.py
│   │   ├── whisper_mlx.py
│   │   ├── whisper_cpu.py
│   │   ├── whisper_cuda.py
│   │   └── openai_api.py
│   ├── search/
│   │   ├── fts.py                   # FTS5 / Postgres tsvector
│   │   ├── vector.py                # ChromaDB adapter
│   │   ├── embeddings.py            # pluggable embedder
│   │   ├── fusion.py                # RRF
│   │   └── service.py               # public hybrid search API
│   ├── media/
│   │   ├── ffmpeg.py                # subprocess wrapper, async
│   │   ├── hls.py                   # remux/transcode + manifest
│   │   ├── thumbnails.py
│   │   └── subtitles.py             # SRT/VTT writers and parsers
│   ├── library/
│   │   ├── service.py               # CRUD + categorization
│   │   ├── watcher.py               # watchdog observers
│   │   └── auto_tag.py
│   ├── api/
│   │   ├── app.py                   # FastAPI factory
│   │   ├── deps.py                  # auth, DB session, rate limit
│   │   ├── routers/
│   │   │   ├── libraries.py
│   │   │   ├── videos.py
│   │   │   ├── search.py
│   │   │   ├── stream.py
│   │   │   ├── jobs.py
│   │   │   ├── collections.py
│   │   │   ├── tags.py
│   │   │   ├── speakers.py
│   │   │   ├── settings.py
│   │   │   └── ws.py                # WebSocket endpoints
│   │   └── static.py                # serves the built frontend
│   ├── auth/
│   │   ├── tokens.py
│   │   └── users.py
│   └── tasks/
│       ├── reaper.py                # reclaim stale claimed jobs
│       ├── retention.py             # HLS cache GC
│       └── recluster.py             # nightly topic clustering
└── tests/
    ├── conftest.py
    ├── unit/
    │   ├── test_identity.py
    │   ├── test_states.py
    │   ├── test_fusion.py
    │   └── test_subtitles.py
    ├── integration/
    │   ├── test_pipeline_e2e.py     # tiny sample WAV → segments → SRT
    │   ├── test_search_hybrid.py
    │   └── test_jobs_concurrency.py # SKIP LOCKED behavior
    └── fixtures/
        └── samples/                 # short royalty-free clips
```

### 12.1 CLI surface

```
maktaba serve                  # run API
maktaba worker [--stages ...]  # run a worker
maktaba scan [--library NAME]  # one-shot scan
maktaba reprocess --library NAME --from-stage transcribe
maktaba search "query" [--lang ar]
maktaba export-subtitles --video ID --format srt
maktaba migrate                # alembic upgrade head
maktaba doctor                 # checks ffmpeg, GPU, DB, write perms
```

### 12.2 Conventions

- `from __future__ import annotations` everywhere; PEP 695 generics.
- Async by default; sync only at FFmpeg subprocess and Whisper boundaries.
- All paths through `pathlib.Path`; never raw strings.
- All times stored as UTC `datetime`; client converts.
- Tests are runnable without GPU (Whisper backend is mocked in unit tests;
  one integration test uses a 5-second WAV and tiny model).

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
 │  → segments emitted incrementally, checkpointed to                    │
 │    .maktaba/transcripts/{hash}.partial.json                            │
 │  → on completion: transcripts + transcript_segments rows               │
 │  state → TRANSCRIBED                                                  │
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
- Mobile native apps (the web UI is responsive; mobile apps come later).
- Live ingestion (streaming sources). Maktaba is for archives, not live feeds.
- Translation between languages on the fly (transcripts are stored in source
  language; translation can be added as an extra stage later).
- DRM-protected content.

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
