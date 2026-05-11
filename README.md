# Maktaba

[![CI](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml)

**Plex-alternative media intelligence platform for 30 TB+ video libraries, with Arabic-first transcription.**

Maktaba (مكتبة, "library") is a self-hosted media platform that turns a folder
tree of video files into a fully streamable, intelligently-organized library
where **every word in every video is searchable**. It is positioned as a full
Plex alternative — library + transcoding origin + first-party apps for web,
mobile, desktop, and TV — with content intelligence layered on top: transcription,
diarization, semantic indexing, and second-precise navigation.

The target deployment is a single household with 30 TB+ of content on a NAS or
workstation. Arabic is a first-class language throughout (Unicode, bidi, RTL),
but the STT and indexing layers are language-agnostic and ship with English
support out of the box.

---

## Architecture

Four cooperating services share one Postgres for state and one media volume for
bytes:

| Service | Language | Role |
|---|---|---|
| **API** | Go 1.23 | REST + GraphQL + WebSocket. Auth, library CRUD, search orchestration, job control, settings, watch state. |
| **Streaming** | Go 1.23 | HLS / DASH origin. FFmpeg-driven transcode/remux, range serving, subtitle muxing, sprites. |
| **Pipeline** | Python 3.12 | Scanner, audio extractor, Whisper STT (MLX / faster-whisper / OpenAI), embedding, ChromaDB indexer, diarization. Drains the durable job queue. |
| **Web** | React 18 + TS + Vite | Single PWA wrapped natively into iOS/Android (Capacitor), macOS/Windows/Linux (Tauri), and tvOS / Android TV (Swift / Kotlin). |
| **Cloud Relay** *(optional)* | Go 1.23 | Hosted SaaS layer for remote access: identity, server linking, WSS-tunneled HTTP relay, push fanout, Stripe billing. Lives in `cloud/`. |

See [`specs/architecture.md`](specs/architecture.md) for the canonical design,
data flow, and rationale behind the language split.

---

## Key features

- **File scanner with BLAKE3 content identity** — detects every video under a
  library's roots, identifies it by streaming-BLAKE3 fingerprint, and tracks it
  through a deterministic state machine. Move-aware, dedup-aware, atomic
  filesystem watcher.
- **MLX Whisper transcription** — Apple Silicon-optimized STT via `mlx-whisper`,
  with `faster-whisper` (CUDA / CPU) and OpenAI API backends behind a
  pluggable registry. Pause / resume / crash recovery preserve every segment
  already committed.
- **Hybrid FTS + semantic search** — Postgres `tsvector` full-text search fanned
  out with ChromaDB vector search via reciprocal-rank fusion. Search any phrase
  spoken in any video and jump to the exact second from any client.
- **HLS / DASH adaptive streaming** — on-the-fly transcode and remux via FFmpeg
  subprocesses, range-request direct play, subtitle muxing, signed-URL gated
  session capability matrix, hardware-accelerated decode paths.
- **Arabic-first with English support** — bidi/RTL correct from input to
  caption rendering. Pipeline is language-agnostic; UI ships AR and EN.
- **Cloud relay for remote access** — optional hosted layer that links a
  self-hosted server to a Maktaba Cloud account, tunnels HTTP through WSS, and
  fans out APNs / FCM push without exposing the home box to the internet. Free
  tier; paid tiers via Stripe.

---

## Quick start

```sh
make prereqs        # verify docker, go, uv, pnpm, node are present
make dev            # live-reload stack: postgres, chroma, api, streaming, pipeline, web
make test           # unit tier — no network, no sudo
make help           # list every target, grouped by section
```

Or, plain Docker Compose without the live-reload tooling:

```sh
docker compose -f deploy/compose/docker-compose.yml up --build
```

CI runs the same `make` targets you run locally — see
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the canonical dev workflow,
troubleshooting, and pre-commit setup.

---

## Project structure

```
maktaba/
├── api/                  # Go API service (Epic 7) — REST + GraphQL + WS
├── streaming/            # Go streaming service (Epic 8) — HLS/DASH origin
├── pipeline/             # Python pipeline (Epics 1–6) — scan, STT, index
├── cloud/                # Go cloud relay service (Epic 25) — hosted SaaS
├── web/                  # React PWA + design system + wiki app (Epic 11, 17)
├── apps/
│   ├── mobile/           # Capacitor wrappers for iOS / Android (Epic 12)
│   ├── desktop/          # Tauri wrapper for mac/Win/Linux (Epic 13)
│   └── tv/               # Swift (tvOS) + Kotlin (Android TV) (Epic 14)
├── shared/
│   ├── api/              # openapi.yaml, GraphQL SDL — single source of truth
│   ├── db/migrations/    # numbered SQL migrations (Postgres + SQLite duals)
│   ├── log/              # structured logging libraries (go + py)
│   ├── metrics/          # OpenTelemetry helpers
│   └── testtier/         # cross-language test pyramid budgets
├── specs/                # design source of truth: 25 epics, 272 stories
│   ├── architecture.md   # canonical system design
│   ├── epics/            # one directory per epic, README + stories + plans
│   └── PLAN_REVIEW*.md   # independent spec reviews
├── docs/
│   ├── testing.md        # test pyramid, tiers, runtime budgets
│   └── wiki/             # generated wiki — INDEX, stories-map, entities, …
├── deploy/               # docker-compose stacks, terraform for repo config
├── tools/                # migration-lint and other dev tooling
├── tests/                # cross-service integration suites
└── Makefile              # canonical entry points (CI uses these too)
```

---

## Tech stack

- **Backend:** Go 1.23+ (api, streaming, cloud), Python 3.12+ (pipeline)
- **Frontend:** TypeScript, React 18, Vite, TailwindCSS, Vidstack
- **Native:** Capacitor (mobile), Tauri 2 (desktop), Swift / SwiftUI (tvOS),
  Kotlin / Jetpack Compose (Android TV)
- **Data:** PostgreSQL 16 (or SQLite for single-user mode), ChromaDB (vectors)
- **Media:** FFmpeg for transcoding / remuxing / probing
- **STT / ML:** Whisper via `mlx-whisper`, `faster-whisper`, or OpenAI API;
  multilingual `sentence-transformers` embeddings; `pyannote.audio` diarization
- **Observability:** OpenTelemetry traces + metrics, structured JSON logs
- **Tooling:** `uv` (Python), `pnpm` (Node), `goose` (migrations),
  `golangci-lint`, `ruff` + `mypy --strict`, Vitest + Playwright

---

## Status

| | |
|---|---|
| Epics | **25** ([scanner](specs/epics/01-scanner/) → [cloud relay](specs/epics/25-cloud-relay/)) |
| Stories | **272** implemented |
| Phases | **17** delivered (foundation → cloud relay) |
| Plans | 274 implementation plans across 5 plan-review passes |

See:

- [`specs/architecture.md`](specs/architecture.md) — system design.
- [`specs/FULL_IMPLEMENTATION_AUDIT.md`](specs/FULL_IMPLEMENTATION_AUDIT.md) — end-to-end audit of all 25 epics on `main`.
- [`specs/TEST_RESULTS.md`](specs/TEST_RESULTS.md) — full-suite test results snapshot.
- [`docs/testing.md`](docs/testing.md) — test pyramid, tier budgets, CI gates.
- [`docs/wiki/INDEX.md`](docs/wiki/INDEX.md) — generated wiki index.
- [`docs/wiki/build-order.md`](docs/wiki/build-order.md) — phase-by-phase implementation order.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — day-1 setup, day-N inner loop, troubleshooting.

---

## License

See [`LICENSE`](LICENSE).
