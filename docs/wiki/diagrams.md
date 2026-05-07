# Diagrams Catalog

Every draw.io diagram in [`specs/diagrams/`](../../specs/diagrams/), with its type, what it shows, and which epics it covers.

> Edit them with [diagrams.net](https://app.diagrams.net) (the local app or the web version). All files target a dark-theme palette consistent with the rest of the spec.

## Types

- **System** — service-level boxes-and-arrows of the running system.
- **Flow** — sequenced action diagrams (request lifecycle, login, streaming, ingest).
- **ER** — entity relationships of the canonical schema.
- **Story-relationship** — story-to-story dependency map for an epic group.
- **Detail** — single sub-system zoomed in (search, security, transcription).

---

## Catalog

| File | Type | What it shows | Epics covered |
|------|------|----------------|---------------|
| [system-architecture.drawio](../../specs/diagrams/system-architecture.drawio) | System | Macro topology — Pipeline (Python), API (Go), Streaming (Go), Postgres, media volume, web/mobile/desktop/TV clients, mDNS, and the gRPC + REST + WS edges between them. | All epics (canonical reference) |
| [data-flow.drawio](../../specs/diagrams/data-flow.drawio) | Flow | End-to-end data flow from "new file lands on disk" through scan → probe → transcribe → index → categorize → browse → play. | 1, 2, 3, 5, 7, 8, 9, 11 |
| [entity-relationship.drawio](../../specs/diagrams/entity-relationship.drawio) | ER | All canonical tables: `users`, `libraries`, `videos`, `processing_jobs`, `streaming_sessions`, `web_sessions`, `refresh_tokens`, `pairing_codes`, `tags`, `collections`, `speakers`, `library_topics`, `video_topics`, `media_features`, `library_sweeps`, `library_stats_cache`, `app_settings`, `devices`, `audit_log`. | 1, 5, 6, 7, 8, 9, 10, 12 |
| [epic-dependencies.drawio](../../specs/diagrams/epic-dependencies.drawio) | Story-relationship | Epic-level dependency edges across all 24 epics — what blocks what, and where the "first to land" wedge sits. | All epics |
| [auth-flow.drawio](../../specs/diagrams/auth-flow.drawio) | Flow | Web cookie login + native JWT login + refresh rotation + logout + device pairing. | **10**, 7, 12, 13, 14 |
| [security-architecture.drawio](../../specs/diagrams/security-architecture.drawio) | Detail | Trust boundaries, JWKS publication, signed-URL minter (10.8), Streaming offline JWT verify (10.7), CSRF, secret loading, transport hardening. | **10**, 7, 8 |
| [streaming-flow.drawio](../../specs/diagrams/streaming-flow.drawio) | Flow | Session create → pinned transcoder → manifest → segment → close. Includes hwaccel branch and direct/remux/transcode mode-pick. | **8**, 7, 10 |
| [search-architecture.drawio](../../specs/diagrams/search-architecture.drawio) | Detail | Indexer + FTS + semantic embeddings + hybrid scorer feeding `GET /search`. | 5, 7, 11 |
| [transcription-pipeline.drawio](../../specs/diagrams/transcription-pipeline.drawio) | Detail | Audio extraction → STT backend selection → diarization → segments → subtitle render. | 2, 3, 4, 6 |
| [job-lifecycle.drawio](../../specs/diagrams/job-lifecycle.drawio) | Flow | Queue worker state machine: pending → running → succeeded / failed / paused / cancelled / retrying. | 6, 7 |
| [pipeline-stories.drawio](../../specs/diagrams/pipeline-stories.drawio) | Story-relationship | Inter-story dependency map for Epics 1–6 (Pipeline). | 1, 2, 3, 4, 5, 6 |
| [api-streaming-stories.drawio](../../specs/diagrams/api-streaming-stories.drawio) | Story-relationship | Inter-story dependency map for Epics 7–10 (API + Streaming + Library mgmt + Auth). | **7, 8, 9, 10** |
| [client-stories.drawio](../../specs/diagrams/client-stories.drawio) | Story-relationship | Inter-story dependency map for Epics 11–17 (Web UI, Mobile, Desktop, TV, Discovery, Subscriptions, Design system). | **11, 12, 13**, 14, 15, 16, 17 |
| [nonfunctional-stories.drawio](../../specs/diagrams/nonfunctional-stories.drawio) | Story-relationship | Inter-story dependency map for Epics 18–24 (Performance, Scalability, Testing, Observability, DevOps, Security, Data integrity). | 18, 19, 20, 21, 22, 23, 24 |

---

## Epic-to-diagram index

- **[Epic 07 — API Server](epics/epic-07-api-server.md):** `system-architecture`, `data-flow`, `entity-relationship`, `api-streaming-stories`, `epic-dependencies`.
- **[Epic 08 — Streaming](epics/epic-08-streaming.md):** `streaming-flow`, `api-streaming-stories`, `system-architecture`, `security-architecture`, `entity-relationship`.
- **[Epic 09 — Library Management](epics/epic-09-library-management.md):** `data-flow`, `entity-relationship`, `api-streaming-stories`, `system-architecture`, `epic-dependencies`.
- **[Epic 10 — Auth & Security](epics/epic-10-auth-security.md):** `auth-flow`, `security-architecture`, `api-streaming-stories`, `entity-relationship`.
- **[Epic 11 — Web UI](epics/epic-11-web-ui.md):** `client-stories`, `system-architecture`, `data-flow`, `epic-dependencies`.
- **[Epic 12 — Mobile](epics/epic-12-mobile.md):** `client-stories`, `system-architecture`, `data-flow`, `auth-flow`.
- **[Epic 13 — Desktop](epics/epic-13-desktop.md):** `client-stories`, `system-architecture`, `data-flow`, `security-architecture`.

## Conventions

- **Vertical layouts** — diagrams are tall, not wide, so they render legibly in PDF and on the wiki.
- **Dark-theme palette** — readable in dark mode by default.
- **One concept per page** — multi-tab `.drawio` files are unpacked into separate files (no hidden tabs).
- **Anchors** — every diagram has an `id` attribute matching its filename (e.g. `<diagram id="streaming-flow">`).
