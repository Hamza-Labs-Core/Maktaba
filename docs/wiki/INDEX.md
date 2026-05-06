# Maktaba Wiki — Master Index

> Comprehensive index of every artifact in the Maktaba project: 24 epics,
> 236 stories, 237 plans, 14 diagrams,
> 48 mockups, 70 API endpoints, 32 DB entities.

## Quick navigation

| Catalog | File | Entries |
|---|---|---|
| Stories | [stories-map.md](stories-map.md) | 236 |
| Features | [features.md](features.md) | per epic |
| DB entities | [entities.md](entities.md) | 32 |
| API endpoints | [api-catalog.md](api-catalog.md) | 70 |
| Machine-readable DB | [db/wiki.json](db/wiki.json) | 666 entries |
| JSON schema | [db/wiki-schema.json](db/wiki-schema.json) | — |

## Epic table

| # | Epic | Phase | Engine | Stories | Plans | Mockups | Goal |
|---|---|---|---|---|---|---|---|
| 01 | [Scanner](../../specs/epics/01-scanner/README.md) | 1 | Pipeline (Python) | 6 | 6 | 0 | Detect every video file under a library's roots, assign it a |
| 02 | [Audio Extraction](../../specs/epics/02-audio-extraction/README.md) | 1 | Pipeline (Python) | 4 | 5 | 0 | From a probed video, pick the right audio track, extract it as |
| 03 | [Transcription](../../specs/epics/03-transcription/README.md) | 1 | Pipeline (Python) | 9 | 9 | 0 | Convert extracted audio into a sequence of `transcript_segments` |
| 04 | [Subtitles](../../specs/epics/04-subtitles/README.md) | 1 | Pipeline (Python) | 5 | 5 | 0 | Convert finalized transcripts into well-formed `.srt` and `.vtt` |
| 05 | [Search & Indexing](../../specs/epics/05-search-indexing/README.md) | 1 | Pipeline (Python) + API (Go) | 7 | 7 | 0 | Make every transcribed second searchable in two complementary |
| 06 | [Job Queue](../../specs/epics/06-job-queue/README.md) | 1 | Pipeline + API (cross-cutting) | 10 | 10 | 0 | Implement the durable, atomic, pause-aware job queue that every |
| 07 | [API Server](../../specs/epics/07-api-server/README.md) | 2 | API (Go) | 22 | 22 | 0 | The Go API Service is every request that isn't a media byte: library CRUD, |
| 08 | [Streaming](../../specs/epics/08-streaming/README.md) | 2 | Streaming (Go) | 15 | 15 | 0 | The Go Streaming Service is every media byte: HLS and DASH manifests, |
| 09 | [Library Management](../../specs/epics/09-library-management/README.md) | 2 | API (Go) | 18 | 18 | 15 | A library is a named collection of root paths sharing a configuration |
| 10 | [Auth & Security](../../specs/epics/10-auth-security/README.md) | 2 | API (Go) | 17 | 17 | 0 | Identity, sessions, signed URLs, secret handling, transport hardening. |
| 11 | [Web UI](../../specs/epics/11-web-ui/README.md) | 3 | Web (React/TS) | 14 | 14 | 9 | A single React 18 + TypeScript + Vite SPA that runs as the |
| 12 | [Mobile](../../specs/epics/12-mobile/README.md) | 3 | Mobile (Capacitor + plugins) | 11 | 11 | 6 | iOS and Android apps that wrap the same web bundle as a native |
| 13 | [Desktop](../../specs/epics/13-desktop/README.md) | 3 | Desktop (Tauri) | 8 | 8 | 3 | A Tauri 2 wrapper of the same web bundle producing native |
| 14 | [TV Apps](../../specs/epics/14-tv-apps/README.md) | 3 | TV (Swift / Kotlin) | 7 | 7 | 4 | Native tvOS (Swift / SwiftUI / AVPlayer) and Android TV |
| 15 | [Discovery](../../specs/epics/15-discovery/README.md) | 3 | API (Go) + Web | 7 | 7 | 0 | Make Maktaba easy to find on the LAN, optionally reachable |
| 16 | [Subscriptions](../../specs/epics/16-subscriptions/README.md) | 3 | API (Go) + Pipeline | 8 | 8 | 0 | Maktaba is fully usable for free as a self-hosted single-user |
| 17 | [UX & Design System](../../specs/epics/17-ux-design-system/README.md) | 3 | Web (React/TS) | 11 | 11 | 11 | A coherent visual + interaction language across web, mobile, |
| 18 | [Performance](../../specs/epics/18-performance/README.md) | 4 | Cross-cutting | 8 | 8 | 0 | Maktaba feels snappy on a single Mac mini / NAS-class host with a |
| 19 | [Scalability](../../specs/epics/19-scalability/README.md) | 4 | Cross-cutting | 8 | 8 | 0 | Maktaba serves the 30 TB / single-household target on one box |
| 20 | [Testing](../../specs/epics/20-testing/README.md) | 4 | Cross-cutting | 8 | 8 | 0 | Every layer of Maktaba has a test posture proportional to its |
| 21 | [Observability](../../specs/epics/21-observability/README.md) | 4 | Cross-cutting | 8 | 8 | 0 | A self-hoster can answer "what's it doing?" and "why is it |
| 22 | [DevOps](../../specs/epics/22-devops/README.md) | 4 | Cross-cutting | 8 | 8 | 0 | A self-hoster gets running in one command and stays running |
| 23 | [Security Hardening](../../specs/epics/23-security/README.md) | 4 | API + Streaming | 8 | 8 | 0 | Maktaba is safe to expose on a home LAN by default and safe to |
| 24 | [Data Integrity](../../specs/epics/24-data-integrity/README.md) | 4 | Cross-cutting | 9 | 9 | 0 | A user's media library and the platform's derived state survive |

## Diagrams

| Diagram | File |
|---|---|
| Api Streaming Stories | [specs/diagrams/api-streaming-stories.drawio](../../specs/diagrams/api-streaming-stories.drawio) |
| Auth Flow | [specs/diagrams/auth-flow.drawio](../../specs/diagrams/auth-flow.drawio) |
| Client Stories | [specs/diagrams/client-stories.drawio](../../specs/diagrams/client-stories.drawio) |
| Data Flow | [specs/diagrams/data-flow.drawio](../../specs/diagrams/data-flow.drawio) |
| Entity Relationship | [specs/diagrams/entity-relationship.drawio](../../specs/diagrams/entity-relationship.drawio) |
| Epic Dependencies | [specs/diagrams/epic-dependencies.drawio](../../specs/diagrams/epic-dependencies.drawio) |
| Job Lifecycle | [specs/diagrams/job-lifecycle.drawio](../../specs/diagrams/job-lifecycle.drawio) |
| Nonfunctional Stories | [specs/diagrams/nonfunctional-stories.drawio](../../specs/diagrams/nonfunctional-stories.drawio) |
| Pipeline Stories | [specs/diagrams/pipeline-stories.drawio](../../specs/diagrams/pipeline-stories.drawio) |
| Search Architecture | [specs/diagrams/search-architecture.drawio](../../specs/diagrams/search-architecture.drawio) |
| Security Architecture | [specs/diagrams/security-architecture.drawio](../../specs/diagrams/security-architecture.drawio) |
| Streaming Flow | [specs/diagrams/streaming-flow.drawio](../../specs/diagrams/streaming-flow.drawio) |
| System Architecture | [specs/diagrams/system-architecture.drawio](../../specs/diagrams/system-architecture.drawio) |
| Transcription Pipeline | [specs/diagrams/transcription-pipeline.drawio](../../specs/diagrams/transcription-pipeline.drawio) |

## Reviews

| Review | File |
|---|---|
| REVIEW | [specs/REVIEW.md](../../specs/REVIEW.md) |
| PLAN_REVIEW | [specs/PLAN_REVIEW.md](../../specs/PLAN_REVIEW.md) |
| PLAN_REVIEW_07_13 | [specs/PLAN_REVIEW_07_13.md](../../specs/PLAN_REVIEW_07_13.md) |
| PLAN_REVIEW_14_17 | [specs/PLAN_REVIEW_14_17.md](../../specs/PLAN_REVIEW_14_17.md) |
| PLAN_REVIEW_18_24 | [specs/PLAN_REVIEW_18_24.md](../../specs/PLAN_REVIEW_18_24.md) |

## Cross-platform mockups

| Surface | Count | Folder |
|---|---|---|
| Admin (web) | 15 | [web/mockups/admin/](../../web/mockups/admin/) |
| Mobile | 6 | [web/mockups/mobile/](../../web/mockups/mobile/) |
| Desktop | 3 | [web/mockups/desktop/](../../web/mockups/desktop/) |
| TV | 4 | [web/mockups/tv/](../../web/mockups/tv/) |
| Theme library | 10 | [web/mockups/theme-library/](../../web/mockups/theme-library/) |

## Reference docs

- [specs/architecture.md](../../specs/architecture.md) — full system architecture (12 sections + 3 appendices)
- [shared/api/openapi.yaml](../../shared/api/openapi.yaml) — OpenAPI 3.1 spec
- [shared/db/migrations/MANIFEST.md](../../shared/db/migrations/MANIFEST.md) — migration slot manifest

*Generated by `docs/wiki/db/wiki.json` · cross-references derive from `git`-tracked files in this branch and the spec branch (`merge-origin-license`).*
