# Epic 7 — API Server

The Go API Service is every request that isn't a media byte: library CRUD,
search, job control, settings, watch state, real-time WebSocket fan-out,
auth issuance, and the gRPC client surface to Pipeline and Streaming. It is
stateless behind Postgres and one binary scales horizontally without any
session affinity (§1.2, §10.3).

This epic covers the REST surface (§9.1–9.7), the GraphQL schema (§9 intro),
WebSocket fan-out (§9.5, §7.10), the inter-service gRPC clients (§9.9), and
the cross-cutting concerns every request inherits: pagination, error format,
validation, request limits, observability. Auth issuance (login, JWT,
cookies, refresh) lives in **Epic 10**; this epic only consumes the
middleware Epic 10 produces.

**Out of scope for Epic 7:** the GraphQL client codegen on the web side
(client epic), the Streaming Service binary (Epic 8), filesystem watching
(Pipeline / Epic 9), and authentication flow itself (Epic 10).

> **Canonical naming.** Job/queue REST endpoints in this epic
> (`/api/jobs/*`, `/api/queue/stats`) are the canonical surfaces. Any
> `/api/processing/*` references in other epics (e.g. NFR Epic 21) are
> duplicates — consumers should target the Epic 7 endpoints.

## Conventions

- **AC** = Acceptance Criteria (Given / When / Then). A story is only
  "done" when every AC has at least one passing test.
- **Test case** = a concrete unit / integration / e2e check, written as a
  one-liner the implementer can paste into a test description.
- **Edge case** = a known failure mode the implementer must consciously
  handle and have a test for. Edge cases also carry one-line GWT or a
  test-case description.
- **Endpoints** are written `METHOD /path` and link back to the architecture
  section that owns them.
- All times are UTC; all string IDs are UUID v7; all monetary numbers are
  ISO-4217 strings; all language codes are ISO 639-1.
- "The API" = the Go API Service binary (§1.2). "Streaming" = the Go
  Streaming Service binary. "Pipeline" = the Python Pipeline Service.
- Stories are independently deployable behind a feature flag unless noted
  ("blocked by"). Feature flags are read from `app_settings` (UI-editable)
  with a code default that matches the production rollout target.

## Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 7.1   | [HTTP server skeleton, problem+json, request IDs](story-07-01-http-server-skeleton.md)      | —          |
| 7.2   | [Cursor pagination primitive](story-07-02-cursor-pagination.md)                          | 7.1        |
| 7.3   | [Library CRUD endpoints](story-07-03-library-crud.md)                               | 7.1, 7.2   |
| 7.4   | [Video listing, detail, patch, delete](story-07-04-video-crud.md)                 | 7.1, 7.2   |
| 7.5   | [Video processing control](story-07-05-video-processing-control.md)                            | 7.1        |
| 7.6   | [Transcript window endpoint](story-07-06-transcript-window.md)                          | 7.1, 7.2   |
| 7.7   | [Subtitles & chapters read endpoints](story-07-07-subtitles-chapters-read.md)                  | 7.1        |
| 7.8   | [Search API (FTS, semantic, hybrid)](story-07-08-search-api.md)                   | 7.1, gRPC client |
| 7.9   | [Saved searches](story-07-09-saved-searches.md)                                       | 7.8        |
| 7.10  | [Streaming session lifecycle](story-07-10-streaming-session-lifecycle.md) | 7.1, gRPC client to Streaming, Epic 10 JWT signer |
| 7.11  | [Watch progress sync](story-07-11-watch-progress-sync.md)           | 7.10, 7.16 |
| 7.12  | [Job control endpoints (pause/resume/cancel/retry)](story-07-12-job-control.md)    | 7.1        |
| 7.13  | [Queue stats endpoint](story-07-13-queue-stats.md)                                 | 7.1        |
| 7.14  | [Collections, tags, speakers endpoints](story-07-14-collections-tags-speakers.md)                | 7.1, 7.2   |
| 7.15  | [Settings & system endpoints](story-07-15-settings-system.md)                          | 7.1, Epic 10 |
| 7.16  | [WebSocket fan-out](story-07-16-websocket-fanout.md) | 7.1, Postgres LISTEN |
| 7.17  | [GraphQL schema + resolvers](story-07-17-graphql-schema.md)     | 7.3–7.15   |
| 7.18  | [gRPC clients to Pipeline and Streaming](story-07-18-grpc-clients.md)               | 7.1        |
| 7.19  | [Request validation, body/query limits, rate limiting](story-07-19-validation-rate-limiting.md) | 7.1        |
| 7.20  | [Health, version, metrics, observability](story-07-20-health-version-metrics.md)              | 7.1        |
| 7.21  | [Recommendations endpoint](story-07-21-recommendations.md)                            | 7.1, 7.8 |
| 7.22  | [Device registration for push](story-07-22-devices-register.md)                       | 7.1, Epic 10 |

## Schema additions owned by this epic

The following tables are introduced or referenced by stories in this epic
and are required to be added to `architecture.md` §8 if not already present.

### `app_settings`

Holds runtime-editable knobs (Story 7.15). Each row is one key/value pair.

```
CREATE TABLE app_settings (
  key        TEXT PRIMARY KEY,
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);
-- Postgres NOTIFY 'settings_changed' on UPDATE/INSERT.
```

### `devices`

Holds per-device push-notification tokens (Story 7.22). Required by the
mobile-push pathway in the clients epic (Epic 12.4).

```
CREATE TABLE devices (
  id            UUID PRIMARY KEY,             -- v7
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform      TEXT NOT NULL,                -- 'ios' | 'android' | 'web'
  push_token    TEXT NOT NULL,
  bundle_id     TEXT NOT NULL,
  app_version   TEXT,
  locale        TEXT,
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at    TIMESTAMPTZ,
  UNIQUE (user_id, platform, push_token)
);
CREATE INDEX devices_user_active ON devices (user_id) WHERE revoked_at IS NULL;
```

## Sequencing

Land in order: 7.1 → 7.19 → 7.2 → 7.3 → 7.4 → 7.18 → 7.10 → 7.6/7.7/7.8/7.9 →
7.5/7.12/7.13 → 7.14/7.15 → 7.11/7.16 → 7.17 → 7.20 → 7.21/7.22.
