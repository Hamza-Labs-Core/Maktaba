# Epic 08 — Streaming Service

> The Go Streaming Service: every media byte. HLS and DASH manifests, range-served direct play, FFmpeg-driven on-the-fly transcode and remux, live subtitle muxing, sprite/poster serving, and session-pinned adaptive playback. Its own binary, shares only Postgres and the read-only media volume; validates its own JWTs offline against the API's published JWKS so a playing video survives an API restart.

- **Spec README:** [`specs/epics/08-streaming/README.md`](../../../specs/epics/08-streaming/README.md)
- **Architecture anchors:** §4 (playback modes), §9.4 (Streaming HTTP surface), §9.8 (offline JWT verify), §9.9 (gRPC)
- **Out of scope:** subtitle *generation* (Pipeline / Epic 03), thumbnail *generation* (Pipeline / Epic 02), session *minting* ([Epic 07](epic-07-api-server.md) story 7.10), JWT *issuance* ([Epic 10](epic-10-auth-security.md)).

## Stories & Plans

| #    | Story                                                    | Plan                                                    | Depends on   |
|------|----------------------------------------------------------|---------------------------------------------------------|--------------|
| 8.1  | [Server skeleton, signed-URL middleware, metrics](../../../specs/epics/08-streaming/story-08-01-server-skeleton.md) | [plan](../../../specs/epics/08-streaming/plan-08-01-server-skeleton.md) | —            |
| 8.2  | [Capability matrix and client profile registry](../../../specs/epics/08-streaming/story-08-02-capability-matrix.md) | [plan](../../../specs/epics/08-streaming/plan-08-02-capability-matrix.md) | 8.1          |
| 8.3  | [Direct play (range-served `206 Partial Content`)](../../../specs/epics/08-streaming/story-08-03-direct-play.md) | [plan](../../../specs/epics/08-streaming/plan-08-03-direct-play.md) | 8.1, 8.2     |
| 8.4  | [Direct stream (FFmpeg `-c copy` remux)](../../../specs/epics/08-streaming/story-08-04-direct-stream-remux.md) | [plan](../../../specs/epics/08-streaming/plan-08-04-direct-stream-remux.md) | 8.1, 8.2     |
| 8.5  | [HLS adaptive transcode pipeline](../../../specs/epics/08-streaming/story-08-05-hls-transcode.md) | [plan](../../../specs/epics/08-streaming/plan-08-05-hls-transcode.md) | 8.1, 8.2     |
| 8.6  | [DASH manifest (opt-in per session)](../../../specs/epics/08-streaming/story-08-06-dash-manifest.md) | [plan](../../../specs/epics/08-streaming/plan-08-06-dash-manifest.md) | 8.5          |
| 8.7  | [Hardware acceleration auto-detect](../../../specs/epics/08-streaming/story-08-07-hwaccel-detect.md) | [plan](../../../specs/epics/08-streaming/plan-08-07-hwaccel-detect.md) | 8.5          |
| 8.8  | [gRPC server: OpenSession / CloseSession / EvictHashCache / GetCapabilities](../../../specs/epics/08-streaming/story-08-08-grpc-server.md) | [plan](../../../specs/epics/08-streaming/plan-08-08-grpc-server.md) | 8.1, 8.5     |
| 8.9  | [Session store, sticky transcoder, reaper](../../../specs/epics/08-streaming/story-08-09-session-store.md) | [plan](../../../specs/epics/08-streaming/plan-08-09-session-store.md) | 8.8          |
| 8.10 | [Concurrency caps, backpressure, slot accounting](../../../specs/epics/08-streaming/story-08-10-concurrency-caps.md) | [plan](../../../specs/epics/08-streaming/plan-08-10-concurrency-caps.md) | 8.5, 8.9     |
| 8.11 | [Live subtitle rendering (auto, sidecar, embedded)](../../../specs/epics/08-streaming/story-08-11-live-subtitle.md) | [plan](../../../specs/epics/08-streaming/plan-08-11-live-subtitle.md) | 8.5, 8.1     |
| 8.12 | [Chapter delivery (HLS DATERANGE + sidecar JSON)](../../../specs/epics/08-streaming/story-08-12-chapter-delivery.md) | [plan](../../../specs/epics/08-streaming/plan-08-12-chapter-delivery.md) | 8.5, 8.1     |
| 8.13 | [Posters, sprite sheets, chapter thumbs serving](../../../specs/epics/08-streaming/story-08-13-posters-sprites.md) | [plan](../../../specs/epics/08-streaming/plan-08-13-posters-sprites.md) | 8.1          |
| 8.14 | [Cache layout, LRU GC, cap enforcement](../../../specs/epics/08-streaming/story-08-14-cache-gc.md) | [plan](../../../specs/epics/08-streaming/plan-08-14-cache-gc.md) | 8.1          |
| 8.15 | [Probe cache (LRU + Postgres)](../../../specs/epics/08-streaming/story-08-15-probe-cache.md) | [plan](../../../specs/epics/08-streaming/plan-08-15-probe-cache.md) | 8.1          |

## DB tables owned

| Table                | Story | Purpose                                                                                  |
|----------------------|-------|------------------------------------------------------------------------------------------|
| `streaming_sessions` | 8.9   | One row per active session; cascades through `videos.library_id` so [Epic 09](epic-09-library-management.md) story 9.15 deletes propagate. Indexed for the idle-reaper and per-user lookup. |

> See [`specs/epics/08-streaming/README.md`](../../../specs/epics/08-streaming/README.md#schema-additions-owned-by-this-epic) for full DDL.

## Endpoints owned

> The Streaming Service is **not** in [`shared/api/openapi.yaml`](../../../shared/api/openapi.yaml) (which describes only the API binary). All paths live behind signed-URL middleware (story 8.1) and validate the JWT offline against the JWKS published by [Epic 10](epic-10-auth-security.md) story 10.6.

| Path family                                  | Required `aud`        | `sub`              | Story  |
|----------------------------------------------|-----------------------|--------------------|--------|
| `/stream/{session_id}/manifest.{m3u8,mpd}`   | `streaming`           | `session_id`       | 8.5, 8.6 |
| `/stream/{session_id}/{rendition}/...`       | `streaming`           | `session_id`       | 8.5    |
| `/stream/direct/{video_id}`                  | `streaming-direct`    | `video_id`         | 8.3, 8.4 |
| `/stream/posters/{video_id}.jpg`             | `streaming-static`    | `<artifact-hash>`  | 8.13   |
| `/stream/sprites/{video_id}.{webp,vtt}`      | `streaming-static`    | `<artifact-hash>`  | 8.13   |
| `/stream/thumbs/{video_id}/...`              | `streaming-static`    | `<artifact-hash>`  | 8.13   |
| `/stream/{session_id}/subs/*`                | `streaming-static`    | `<artifact-hash>`  | 8.11   |
| `/healthz`, `/metrics`                       | none                  | —                  | 8.1    |

**gRPC server (consumed by Epic 07 / 7.18):**

| Method            | Story | Purpose                                          |
|-------------------|-------|--------------------------------------------------|
| `OpenSession`     | 8.8   | API mints session → Streaming pins transcoder    |
| `CloseSession`    | 8.8   | API tears down → Streaming reaps transcoder      |
| `EvictHashCache`  | 8.8   | Pipeline notifies on file change → invalidate    |
| `GetCapabilities` | 8.8   | Health + hwaccel + slot count snapshot           |

## JWT validation surface

Streaming additionally checks `lib[]` against the video's library and rejects requests whose `lib[]` claim doesn't include the target library. This is the core of [Epic 10](epic-10-auth-security.md) story 10.7's offline authorization.

## Mockups

This epic has no UI mockups (it's a pure byte-pumping service). Player UIs that consume this surface live in:

- [`web/mockups/mockup-11-03-video-player.html`](../../../web/mockups/mockup-11-03-video-player.html) — web HLS player ([Epic 11](epic-11-web-ui.md))
- [`web/mockups/mobile/player.html`](../../../web/mockups/mobile/player.html) — mobile native player ([Epic 12](epic-12-mobile.md))
- [`web/mockups/tv/player-tv.html`](../../../web/mockups/tv/player-tv.html) — TV player (Epic 14)

## Diagrams

| Diagram | Type | Coverage |
|---|---|---|
| [`streaming-flow.drawio`](../../../specs/diagrams/streaming-flow.drawio) | Flow | Session create → manifest → segment → close |
| [`api-streaming-stories.drawio`](../../../specs/diagrams/api-streaming-stories.drawio) | Story-relationship | All Epic 08 stories grouped with 07/09/10 |
| [`system-architecture.drawio`](../../../specs/diagrams/system-architecture.drawio) | System | Streaming service as a separate binary |
| [`security-architecture.drawio`](../../../specs/diagrams/security-architecture.drawio) | Security | Offline JWT validation against published JWKS |
| [`entity-relationship.drawio`](../../../specs/diagrams/entity-relationship.drawio) | ER | `streaming_sessions` → `videos` → `libraries` cascade |

## Dependencies on other epics

- **[Epic 10](epic-10-auth-security.md) story 10.6:** RS256 keypair + JWKS publication (consumed offline by 8.1).
- **[Epic 10](epic-10-auth-security.md) story 10.7:** the offline-verify spec lives there; this epic implements the consumer side.
- **[Epic 07](epic-07-api-server.md) story 7.10:** session minting → calls 8.8.
- **[Epic 07](epic-07-api-server.md) story 7.18:** gRPC client → 8.8 server.
- **Epic 02 (Audio Extraction) / Epic 03 (Transcription):** produce the artifacts (audio, subs) that 8.5/8.11 stream.
- **Epic 05 (Search Indexing):** chapter inference produces the JSON sidecars 8.12 serves.

## Key decisions

- **Stateless transcoders, sticky session affinity.** The session store (8.9) pins each session to a single Streaming host; the API consults the store before sending segment URLs.
- **Offline JWT verify against JWKS.** No live API call per segment; Streaming caches JWKS and refreshes on `kid` miss. A playing video survives an API outage ([story 10.7](../../../specs/epics/10-auth-security/story-10-07-streaming-jwt-verify.md)).
- **Per-segment authorization via `lib[]` claim.** Library-scoped JWT means [Epic 09](epic-09-library-management.md) library deletion immediately invalidates in-flight tokens for that library.
- **HLS is the canonical adaptive format**, DASH is opt-in per session (8.6) — most clients are HLS-native.
- **Hardware acceleration is auto-detected** (8.7), not configured — falls back to software encode if the probe fails.
- **Probe cache** (8.15) — FFprobe results memoized in Postgres + LRU; eviction triggered by file-hash change events from the Pipeline.
- **Concurrency caps with slot accounting** (8.10) — backpressure surfaces as `503 Retry-After`, not connection drop.

## Sequencing

Land in order: **8.1/8.15 → 8.2 → 8.3 → 8.8/8.9 → 8.5/8.7 → 8.10 → 8.4/8.6 → 8.11/8.12 → 8.13/8.14.**
