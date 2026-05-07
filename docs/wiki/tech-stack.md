# Tech Stack — Catalog

Comprehensive catalog of every library, framework, and tool referenced in
the architecture and the Pipeline epic plans (01 – 06). Versions reflect
what is pinned in plans or in code skeletons; "—" means the source
specifies the choice but not a version.

Sources: architecture §2 (Tech Stack) and §2.2 (Why these defaults), plus
plans listed in [`epics/epic-01-scanner.md`](epics/epic-01-scanner.md)
through [`epics/epic-06-job-queue.md`](epics/epic-06-job-queue.md).

---

## 1. Backend services

### 1.1 API Service (Go 1.23+)

| Concern | Library / tool | Version | Why this and not the alternative |
|---------|----------------|---------|----------------------------------|
| HTTP framework | `chi` + `net/http` | v5.1.0 | Stdlib-first, battle-tested router; trivial middleware composition. |
| GraphQL | `gqlgen` (schema-first) | — | Code-gen from `.graphql`; type-safe resolvers; subscriptions over WebSocket. |
| ORM / SQL | `sqlc` + `pgx/v5` | sqlc 1.27.0; pgx 5.6.0 | Generated typed Go from raw SQL — keeps DDL canonical. No ORM reflection. |
| Migrations | `goose` (or `atlas`) | goose 3.21.1 | Embedded, single-binary; runs at boot or via `maktaba-api migrate`. |
| Auth — JWT | RS256 — stdlib + `crypto` | — | Mobile/TV use bearer; web uses httpOnly cookies + CSRF. |
| Password hash | argon2id | — | Memory-hard. Tunable: `argon2_memory_kib=65536`. |
| Validation | `go-playground/validator/v10` | — | Struct-tag-driven request validation. |
| WebSocket | `coder/websocket` (formerly nhooyr) | — | Modern, context-aware, no goroutine-per-conn internals. |
| gRPC client | `google.golang.org/grpc` | — | Talks to Pipeline + Streaming. |
| Background tasks | Native goroutines + Postgres `LISTEN` | stdlib | No external scheduler. |
| Config | `viper` | 1.19.0 | Layered: defaults → TOML → env → flags. |
| Logging | `slog` (stdlib) + JSON handler | stdlib | Grep-friendly; OTel-bridge available. |
| Telemetry | OpenTelemetry SDK (opt-in) | — | Cross-service trace propagation. |
| UUIDs | `google/uuid` | 1.6.0 | UUID v7 (sortable). |
| Tests | `stretchr/testify` | 1.9.0 | Assertions. |
| Test infra | `testcontainers/testcontainers-go` (postgres) | 0.31.0 | Real Postgres in CI. |
| Prometheus | `prometheus/client_golang/prometheus` | — | `/metrics` endpoint. |

### 1.2 Streaming Service (Go 1.23+)

| Concern | Library / tool | Version | Why |
|---------|----------------|---------|-----|
| HTTP framework | `chi` + `net/http` | v5.1.0 | Same as API; shared `internal/http` middleware. |
| Range serving | `http.ServeContent` + custom HEAD path | stdlib | Conditional GETs, `Accept-Ranges`, byte ranges; Safari-correct. |
| HLS / DASH | FFmpeg subprocess (HLS muxer + dashenc) | external | Battle-tested manifest generation. We orchestrate; FFmpeg muxes. |
| Transcode pool | Per-host semaphore + per-session FFmpeg | custom | Capped concurrency, graceful eviction under pressure. |
| Probe cache | LRU in-memory + Postgres `media_info` | — | Avoid re-probing on every manifest request. |
| Subtitle muxing | Native VTT writer + FFmpeg passthrough | custom | Generated subs join HLS via `#EXT-X-MEDIA:TYPE=SUBTITLES`. |
| Image generation | `disintegration/imaging` + FFmpeg | — | Posters, sprite sheets, chapter thumbs. |
| Cache GC | LRU on disk (default 50 GiB cap) | custom | Bounded; survives restarts. |
| Config | `viper` (shared loader) | 1.19.0 | Same as API. |

### 1.3 Pipeline Service (Python 3.12+)

| Concern | Library / tool | Version | Why |
|---------|----------------|---------|-----|
| Async runtime | `asyncio` + `anyio` | stdlib + latest | Native to 3.12; required for streaming STT. |
| gRPC server | `grpc.aio` | latest | Async; one process per worker box. |
| Worker loop | Custom claim loop on Postgres | — | Same `SELECT FOR UPDATE SKIP LOCKED` queue the API enqueues into. |
| Postgres driver | `asyncpg` | latest | LISTEN/NOTIFY support; cheap async. |
| SQLite driver (dev/tests) | `aiosqlite` | latest | Async SQLite for the single-user / test path. |
| Domain models | Pydantic v2 | latest | Validation at boundaries; `betterproto` for gRPC serialisation. |
| Settings | `pydantic-settings` | latest | TOML + env override; same shape as Go's viper. |
| STT — MLX | `mlx-whisper` | latest | Apple Silicon default; ~0.3× RT. (Epic 03 Story 3.2) |
| STT — Faster-Whisper | `faster-whisper` | ≥ 1.0 | CUDA + CPU; ctranslate2 backend. (Epic 03 Story 3.3) |
| STT — OpenAI | `openai` | ≥ 1.0 | Network backend with budget cap. (Epic 03 Story 3.4) |
| Embeddings | `sentence-transformers` | latest | `intfloat/multilingual-e5-large` by default. |
| Vector store | `chromadb` (Persistent client, DuckDB+Parquet) | latest | Embedded; no extra service. |
| Diarization (opt-in) | `pyannote.audio` | ≥ 2.1 | Heavyweight; lazy import; v1.1 deferred speaker matching. |
| Audio I/O | `audioread`, `librosa` | latest | Silence detection / silence-map for OpenAI chunker. |
| Filesystem watch | `watchdog` | latest | inotify / FSEvents / ReadDirectoryChangesW. |
| Hash | `blake3` (PyPI) | latest | Same as Go's `zeebo/blake3`. |
| Language tags | `langcodes` | latest | ISO 639 normalisation. |
| Light language detect | `whisper-cpp-python` | latest | Cheap detection for `und` audio tracks. |
| Media probe | `ffmpeg-python` | latest | Wraps `ffprobe`. |
| Grapheme matching | `regex` | latest | `\X` extended grapheme clusters (stdlib `re` cannot). |
| Packaging | `uv` + `pyproject.toml` | latest | Fast resolver. |
| Logging | `structlog` → JSON | latest | Same JSON shape as Go side. |
| Metrics | `prometheus_client` | ≥ 0.20 | `:9101` scrape endpoint. |
| GPU enumeration | `pynvml` (optional) | ≥ 11.5 | Per-host caps for `transcribe`. |

---

## 2. Shared infrastructure

| Concern | Choice | Version | Why |
|---------|--------|---------|-----|
| Metadata DB (prod) | PostgreSQL | 16 | One source of truth; `sqlc` + `asyncpg` share the schema. |
| Metadata DB (dev / single-user) | SQLite | latest | One-file install path. |
| FTS (multi-user) | Postgres `tsvector` | 16 | Custom `arabic` config; diacritic strip via `maktaba_normalize`. |
| FTS (single-user) | SQLite FTS5 | latest | `tokenize='unicode61 remove_diacritics 2'`. |
| Pub/sub | Postgres `LISTEN/NOTIFY` | 16 | One transaction, one bus; no Redis/NATS/Kafka in v1. |
| IPC schemas | Protobuf 3 + gRPC | — | One `.proto`; Go and Python clients regenerated from `shared/proto/`. |
| Container runtime | Docker + Compose | latest | Reproducible single-host deploy. |
| Reverse proxy / TLS | Caddy | 2 | Auto-issues local-CA (Mac) and Let's Encrypt (Linux) certs. |
| Reverse proxy alt. | nginx | — | Optional — same routes; manual cert management. |
| External binaries | `ffmpeg` / `ffprobe` | 6.x – 7.x | Probe + extract + transcode + subtitle mux. |

### 2.1 Why these defaults

- **Why not Celery / Redis / Kafka?** A single-household 30 TB library has
  dozens of jobs in flight, not millions. A Postgres-backed queue with
  `SELECT … FOR UPDATE SKIP LOCKED` gives atomic claim, full visibility
  through the same DB the UI already reads, and one fewer service.
- **Why not an ORM in Go?** `sqlc` reads the same DDL Python reads and
  generates typed Go without runtime reflection — neither runtime can
  drift from the schema silently.
- **Why GraphQL alongside REST?** REST is the boring high-cacheability
  surface for streaming/manifest URLs and webhooks. GraphQL is the
  composable surface for client-driven views.
- **Why JWT + cookies?** Streaming validates JWTs offline against the
  API's RS256 public key — the API doesn't need to be reachable for an
  in-flight watch session to keep playing.

---

## 3. Epic-specific Pipeline (01 – 06) introductions

| Library | Versions | Service | Used by epic | Why |
|---------|----------|---------|--------------|-----|
| `zeebo/blake3` | 0.2.4 | Go (scanner) | 01 | BLAKE3 head+tail+size hashing. |
| `blake3` (PyPI) | latest | Pipeline | 01 | Same algorithm in Python; reads same constants from `shared/db/queries/identity.sql`. |
| `watchdog` | latest | Pipeline | 01 | Cross-platform live watch (FSEvents, inotify, ReadDirectoryChangesW). |
| `fsnotify` (Go) | latest | Go (scanner) | 01 | Native watch in the Go reuse path. |
| `langcodes` | latest | Pipeline | 02 | ISO 639 normalisation for track language. |
| `whisper-cpp-python` | latest | Pipeline | 02 | Cheap language detection for `und` tracks. |
| `mlx-whisper` | latest | Pipeline | 03 | Apple Silicon STT (default on Mac). |
| `faster-whisper` | ≥ 1.0 | Pipeline | 03 | CUDA / CPU STT. |
| `openai` | ≥ 1.0 | Pipeline | 03 | Whisper API STT backend. |
| `pyannote.audio` | ≥ 2.1 | Pipeline | 03 | Diarization (opt-in). |
| `audioread` | latest | Pipeline | 03 | Silence-map preparation. |
| `librosa` | latest | Pipeline | 03 | Silence detection for OpenAI chunker. |
| `regex` | latest | Pipeline | 04 | Grapheme-cluster line wrapping for SRT/VTT. |
| `chromadb` | latest | Pipeline | 05 | Embedded vector DB (PersistentClient). |
| `sentence-transformers` | latest | Pipeline | 05 | `intfloat/multilingual-e5-large` + `-base` fallback. |
| `pg_trgm` (Postgres ext.) | bundled | Postgres | 05 | GIN index for fuzzy suggestion fallback. |
| `aiosqlite` | latest | Pipeline tests | 06 | Fast SQLite-backed unit tests for queue logic. |
| `pynvml` | ≥ 11.5 (optional) | Pipeline | 06 | GPU enumeration for `transcribe` concurrency caps. |
| `asyncpg` | latest | Pipeline | 06 | LISTEN/NOTIFY listener for `jobs.*` channels. |
| `prometheus_client` | ≥ 0.20 | Pipeline | 06 | Counters / histograms / summaries. |
| `structlog` | latest | Pipeline | 06 | JSON-structured logs. |

---

## 4. Web / Mobile / Desktop / TV

| Concern | Choice | Version | Why |
|---------|--------|---------|-----|
| Web framework | React 18 + TypeScript + Vite + Tailwind | latest | Mature; RTL-friendly with `dir="rtl"`. |
| Router / data | TanStack Router + TanStack Query | latest | Type-safe routes; cache + invalidation that matches the WS-driven UI. |
| Player | Vidstack (or Video.js fallback) | latest | HLS + DASH, sidecar VTT, captions, chapter markers, mobile-friendly. |
| State | Zustand (UI) + TanStack Query (server) | latest | No Redux ceremony; small surface. |
| GraphQL client | `graphql-request` + codegen | latest | Lightweight; types generated from the server schema. |
| PWA shell | `vite-plugin-pwa` + Workbox | latest | Background sync, offline metadata, installable on iOS/Android. |
| Mobile wrapper | Capacitor 6 | 6 | Native shell over the web app; native player handoff. |
| Desktop wrapper | Tauri 2 | 2 | ~10 MB binary, native menus, file association, system tray. |
| TV — tvOS | Swift / SwiftUI + AVPlayer | — | Native focus engine, AVPlayer for HLS, top-shelf integration. |
| TV — Android TV | Kotlin + Jetpack Compose for TV + ExoPlayer | — | Native focus, ExoPlayer for adaptive streaming, Leanback row APIs. |

---

## 5. Build & dev tooling

| Tool | Used for |
|------|----------|
| `Makefile` (top-level) | One verb per common operation; orchestrates language toolchains. |
| `go build`, `go test`, `golangci-lint` | Go services. |
| `uv build`, `pytest`, `ruff` | Python pipeline. |
| `vite build`, `vitest`, `tsc`, `eslint`, `prettier` | Web. |
| `goose up` | Schema migrations. |
| `protoc` + `protoc-gen-go-grpc` + `betterproto` | gRPC code-gen. |
| `nfpm` | deb / rpm package generation (Story 22.7). |
| `ko` | Reproducible Go container images (Story 22.2 / 22.3). |
| `docker buildx` | Multi-arch Pipeline image. |
| Caddy | Local-CA + Let's Encrypt issuance. |
| `xcodebuild`, `gradle` | TV apps + mobile-via-Capacitor builds. |
| Tauri CLI | Desktop builds (.dmg / .msi / .AppImage). |

There is no top-level monorepo tool (Nx, Bazel, Turborepo) — each
language's native toolchain owns its subtree, and the `Makefile` is the
only orchestrator.
