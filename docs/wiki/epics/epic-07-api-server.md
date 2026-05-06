# Epic 07 — API Server

> The Go API Service: every request that isn't a media byte. Library CRUD, search, job control, settings, watch state, real-time WebSocket fan-out, the gRPC client surface to Pipeline and Streaming, and the cross-cutting concerns every request inherits (pagination, error format, validation, request limits, observability). Stateless behind Postgres; one binary scales horizontally without session affinity.

- **Spec README:** [`specs/epics/07-api-server/README.md`](../../../specs/epics/07-api-server/README.md)
- **Architecture anchors:** §1.2, §9 (REST + GraphQL + WS), §9.9 (gRPC), §10.3
- **Auth issuance owner:** [Epic 10](epic-10-auth-security.md) — this epic only consumes the middleware Epic 10 produces.
- **Out of scope:** GraphQL client codegen (web epic), Streaming binary ([Epic 08](epic-08-streaming.md)), filesystem watching ([Epic 09](epic-09-library-management.md) / Pipeline), authentication flow itself ([Epic 10](epic-10-auth-security.md)).

## Stories & Plans

| #    | Story                                                  | Plan                                                  | Depends on                                  |
|------|--------------------------------------------------------|-------------------------------------------------------|---------------------------------------------|
| 7.1  | [HTTP server skeleton, problem+json, request IDs](../../../specs/epics/07-api-server/story-07-01-http-server-skeleton.md) | [plan](../../../specs/epics/07-api-server/plan-07-01-http-server-skeleton.md) | —                                           |
| 7.2  | [Cursor pagination primitive](../../../specs/epics/07-api-server/story-07-02-cursor-pagination.md) | [plan](../../../specs/epics/07-api-server/plan-07-02-cursor-pagination.md) | 7.1                                         |
| 7.3  | [Library CRUD endpoints](../../../specs/epics/07-api-server/story-07-03-library-crud.md) | [plan](../../../specs/epics/07-api-server/plan-07-03-library-crud.md) | 7.1, 7.2                                    |
| 7.4  | [Video listing, detail, patch, delete](../../../specs/epics/07-api-server/story-07-04-video-crud.md) | [plan](../../../specs/epics/07-api-server/plan-07-04-video-crud.md) | 7.1, 7.2                                    |
| 7.5  | [Video processing control](../../../specs/epics/07-api-server/story-07-05-video-processing-control.md) | [plan](../../../specs/epics/07-api-server/plan-07-05-video-processing-control.md) | 7.1                                         |
| 7.6  | [Transcript window endpoint](../../../specs/epics/07-api-server/story-07-06-transcript-window.md) | [plan](../../../specs/epics/07-api-server/plan-07-06-transcript-window.md) | 7.1, 7.2                                    |
| 7.7  | [Subtitles & chapters read endpoints](../../../specs/epics/07-api-server/story-07-07-subtitles-chapters-read.md) | [plan](../../../specs/epics/07-api-server/plan-07-07-subtitles-chapters-read.md) | 7.1                                         |
| 7.8  | [Search API (FTS, semantic, hybrid)](../../../specs/epics/07-api-server/story-07-08-search-api.md) | [plan](../../../specs/epics/07-api-server/plan-07-08-search-api.md) | 7.1, gRPC client                            |
| 7.9  | [Saved searches](../../../specs/epics/07-api-server/story-07-09-saved-searches.md) | [plan](../../../specs/epics/07-api-server/plan-07-09-saved-searches.md) | 7.8                                         |
| 7.10 | [Streaming session lifecycle](../../../specs/epics/07-api-server/story-07-10-streaming-session-lifecycle.md) | [plan](../../../specs/epics/07-api-server/plan-07-10-streaming-session-lifecycle.md) | 7.1, gRPC client → Streaming, Epic 10 JWT signer |
| 7.11 | [Watch progress sync](../../../specs/epics/07-api-server/story-07-11-watch-progress-sync.md) | [plan](../../../specs/epics/07-api-server/plan-07-11-watch-progress-sync.md) | 7.10, 7.16                                  |
| 7.12 | [Job control endpoints (pause/resume/cancel/retry)](../../../specs/epics/07-api-server/story-07-12-job-control.md) | [plan](../../../specs/epics/07-api-server/plan-07-12-job-control.md) | 7.1                                         |
| 7.13 | [Queue stats endpoint](../../../specs/epics/07-api-server/story-07-13-queue-stats.md) | [plan](../../../specs/epics/07-api-server/plan-07-13-queue-stats.md) | 7.1                                         |
| 7.14 | [Collections, tags, speakers endpoints](../../../specs/epics/07-api-server/story-07-14-collections-tags-speakers.md) | [plan](../../../specs/epics/07-api-server/plan-07-14-collections-tags-speakers.md) | 7.1, 7.2                                    |
| 7.15 | [Settings & system endpoints](../../../specs/epics/07-api-server/story-07-15-settings-system.md) | [plan](../../../specs/epics/07-api-server/plan-07-15-settings-system.md) | 7.1, Epic 10                                |
| 7.16 | [WebSocket fan-out](../../../specs/epics/07-api-server/story-07-16-websocket-fanout.md) | [plan](../../../specs/epics/07-api-server/plan-07-16-websocket-fanout.md) | 7.1, Postgres LISTEN                        |
| 7.17 | [GraphQL schema + resolvers](../../../specs/epics/07-api-server/story-07-17-graphql-schema.md) | [plan](../../../specs/epics/07-api-server/plan-07-17-graphql-schema.md) | 7.3–7.15                                    |
| 7.18 | [gRPC clients to Pipeline and Streaming](../../../specs/epics/07-api-server/story-07-18-grpc-clients.md) | [plan](../../../specs/epics/07-api-server/plan-07-18-grpc-clients.md) | 7.1                                         |
| 7.19 | [Validation, body/query limits, rate limiting](../../../specs/epics/07-api-server/story-07-19-validation-rate-limiting.md) | [plan](../../../specs/epics/07-api-server/plan-07-19-validation-rate-limiting.md) | 7.1                                         |
| 7.20 | [Health, version, metrics, observability](../../../specs/epics/07-api-server/story-07-20-health-version-metrics.md) | [plan](../../../specs/epics/07-api-server/plan-07-20-health-version-metrics.md) | 7.1                                         |
| 7.21 | [Recommendations endpoint](../../../specs/epics/07-api-server/story-07-21-recommendations.md) | [plan](../../../specs/epics/07-api-server/plan-07-21-recommendations.md) | 7.1, 7.8                                    |
| 7.22 | [Device registration for push](../../../specs/epics/07-api-server/story-07-22-devices-register.md) | [plan](../../../specs/epics/07-api-server/plan-07-22-devices-register.md) | 7.1, Epic 10                                |

## DB tables owned

| Table          | Story | Purpose                                                                                  |
|----------------|-------|------------------------------------------------------------------------------------------|
| `app_settings` | 7.15  | Runtime-editable knobs (key/value JSONB). `NOTIFY 'settings_changed'` on change.          |
| `devices`      | 7.22  | Per-device push tokens (iOS / Android / web). Required by [Epic 12](epic-12-mobile.md) story 12.4. |
| `idempotency_keys` | 7.1 | Short-TTL store (24 h) for `Idempotency-Key` replay protection on POST/PUT/PATCH/DELETE. |

> See [`specs/epics/07-api-server/README.md`](../../../specs/epics/07-api-server/README.md#schema-additions-owned-by-this-epic) for full DDL.

## API endpoints owned

> Canonical OpenAPI: [`shared/api/openapi.yaml`](../../../shared/api/openapi.yaml). Streaming-byte endpoints (`/stream/...`) belong to [Epic 08](epic-08-streaming.md). Auth endpoints (`/auth/...`) belong to [Epic 10](epic-10-auth-security.md). All paths below are the `/api/...` REST surface owned by this epic.

| Tag             | Endpoints                                                                                                                                                                                                                                          | Story  |
|-----------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------|
| Libraries       | `GET/POST /libraries`, `GET/PATCH/DELETE /libraries/{id}`, `POST /libraries/{id}/scan`, `GET /libraries/{id}/stats`                                                                                                                                | 7.3    |
| Videos          | `GET /videos`, `GET/PATCH/DELETE /videos/{id}`, `GET /videos/{id}/segments`, `GET /videos/{id}/subtitles`, `GET /videos/{id}/chapters`, `PATCH /videos/{id}/tags`                                                                                   | 7.4, 7.6, 7.7 |
| Video lifecycle | `POST /videos/{id}/process`, `POST /videos/{id}/reprocess`, `POST /videos/{id}/pause`, `POST /videos/{id}/resume`, `POST /videos/{id}/cancel`                                                                                                       | 7.5    |
| Watch progress  | `GET/PUT /videos/{id}/progress`                                                                                                                                                                                                                    | 7.11   |
| Search          | `GET /search`, `GET /search/suggest`, `POST /search/save`, `GET /search/saved`, `DELETE /search/saved/{id}`                                                                                                                                        | 7.8, 7.9 |
| Jobs            | `GET /jobs`, `GET /jobs/{id}`, `POST /jobs/{id}/{pause,resume,cancel,retry}`                                                                                                                                                                       | 7.12   |
| Queue           | `GET /queue/stats`                                                                                                                                                                                                                                 | 7.13   |
| Collections     | `GET/POST /collections`, `GET/PATCH/DELETE /collections/{id}`, `GET/PUT /collections/{id}/videos`, `DELETE /collections/{id}/videos/{videoId}`                                                                                                      | 7.14   |
| Tags            | `GET/POST /tags`                                                                                                                                                                                                                                   | 7.14   |
| Speakers        | `GET/POST /speakers`, `GET/PATCH/DELETE /speakers/{id}`, `POST /speakers/merge`                                                                                                                                                                    | 7.14   |
| Sessions        | `POST /sessions`, `GET /sessions/{id}`, `DELETE /sessions/{id}`, `PUT /sessions/{id}/progress`, `GET /sessions/capabilities`                                                                                                                       | 7.10, 7.11 |
| Settings        | `GET/PUT /settings`, `GET /settings/stt-backends`, `POST /settings/stt-test`                                                                                                                                                                       | 7.15   |
| Devices         | `POST /devices/register`, `GET /devices`, `DELETE /devices/{id}`                                                                                                                                                                                   | 7.22   |
| Recommendations | `GET /recommendations`                                                                                                                                                                                                                             | 7.21   |
| WebSocket       | `GET /ws` (upgrade)                                                                                                                                                                                                                                | 7.16   |
| System          | `GET /system/health`, `GET /system/version`, `GET /system/metrics`                                                                                                                                                                                 | 7.20   |
| GraphQL         | `POST /graphql` (single endpoint over the REST resolvers)                                                                                                                                                                                          | 7.17   |

> **Canonical naming.** `/api/jobs/*` and `/api/queue/stats` are the canonical job/queue surfaces. Any `/api/processing/*` references in NFR Epic 21 are duplicates — consumers target this epic.

## Mockups (clients consuming this surface)

This epic has no UI mockups of its own; UIs live in [Epic 11](epic-11-web-ui.md) (web), [Epic 12](epic-12-mobile.md) (mobile), [Epic 13](epic-13-desktop.md) (desktop). The admin surfaces below also consume Epic 07 endpoints:

- [`web/mockups/admin/job-pipeline.html`](../../../web/mockups/admin/job-pipeline.html) — `/jobs`, `/queue/stats` (7.12, 7.13)
- [`web/mockups/admin/sessions.html`](../../../web/mockups/admin/sessions.html) — `/sessions` (7.10)
- [`web/mockups/admin/library-config.html`](../../../web/mockups/admin/library-config.html) — `/libraries` (7.3)
- [`web/mockups/admin/speaker-manager.html`](../../../web/mockups/admin/speaker-manager.html) — `/speakers` (7.14)
- [`web/mockups/mockup-11-05-processing-queue.html`](../../../web/mockups/mockup-11-05-processing-queue.html) — `/jobs`, `/queue/stats`
- [`web/mockups/mockup-11-04-search-interface.html`](../../../web/mockups/mockup-11-04-search-interface.html) — `/search`

## Diagrams

| Diagram | Type | Coverage |
|---|---|---|
| [`api-streaming-stories.drawio`](../../../specs/diagrams/api-streaming-stories.drawio) | Story-relationship | All Epic 07 stories grouped with 08-10 |
| [`system-architecture.drawio`](../../../specs/diagrams/system-architecture.drawio) | System | API service in the macro topology |
| [`data-flow.drawio`](../../../specs/diagrams/data-flow.drawio) | Flow | End-to-end request lifecycle |
| [`entity-relationship.drawio`](../../../specs/diagrams/entity-relationship.drawio) | ER | All API-owned tables in context |
| [`epic-dependencies.drawio`](../../../specs/diagrams/epic-dependencies.drawio) | Story-relationship | Epic 07's outbound and inbound deps |

## Dependencies on other epics

- **[Epic 10](epic-10-auth-security.md):** middleware (cookie/JWT verify, CSRF, rate-limit hooks), JWT signer for streaming-session minting (7.10), permission model.
- **[Epic 08](epic-08-streaming.md):** gRPC server consumed via 7.18; session-create handshake for 7.10.
- **Epic 06 (Job Queue):** the queue this epic exposes via 7.12 / 7.13.
- **Epic 05 (Search Indexing):** the indexer fronting 7.8.
- **[Epic 09](epic-09-library-management.md):** the behaviors behind 7.3 / 7.14 (libraries, collections, tags, speakers).

## Key decisions

- **chi over gin/echo.** Standard `net/http`-compatible router, no unjustified weight ([plan 7.1](../../../specs/epics/07-api-server/plan-07-01-http-server-skeleton.md)).
- **RFC 9457 problem+json** for all errors. One package (`api/internal/httperror`) owns serialization; `http.Error` direct calls fail CI via `analysispass`.
- **UUID v7 request IDs.** Reused if a syntactically valid `X-Request-Id` is supplied (idempotent client retries keep their ID).
- **Idempotency-Key:** Postgres-backed 24 h replay store on state-changing verbs. Body-hash mismatch → 409 `idempotency-key-conflict`.
- **GraphQL is one schema layered over REST resolvers** (7.17), not a parallel service.
- **WebSocket fan-out via Postgres `LISTEN/NOTIFY`** (7.16) — no Redis or in-process bus, so the API stays stateless.
- **Cursor pagination primitive** (7.2) is shared across every list endpoint; never offset.
- Stories are independently deployable behind feature flags, except for "blocked by" dependencies in the story map.

## Sequencing

Land in order: **7.1 → 7.19 → 7.2 → 7.3 → 7.4 → 7.18 → 7.10 → 7.6/7.7/7.8/7.9 → 7.5/7.12/7.13 → 7.14/7.15 → 7.11/7.16 → 7.17 → 7.20 → 7.21/7.22.**
