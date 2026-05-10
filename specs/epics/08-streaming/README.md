# Epic 8 — Streaming Service

The Go Streaming Service is every media byte: HLS and DASH manifests,
range-served direct play, FFmpeg-driven on-the-fly transcode and remux,
live subtitle muxing, sprite/poster serving, and session-pinned adaptive
playback (§4). It is its own binary, shares only Postgres and the
read-only media volume with the rest of the system, and validates its
own JWTs offline against the API's published JWKS so it can keep an
in-flight watch session alive even when the API restarts (§9.4, §9.8).

This epic covers the byte-pumping HTTP surface (§9.4 "Streaming Service"
section), the gRPC server consumed by the API (§9.9), and the FFmpeg
orchestration that backs each playback mode (§4.1–4.9). Session
*creation* is owned by the API (Epic 7 Story 7.10); this epic implements
the gRPC handler that accepts the create call and the byte handlers that
serve the resulting URLs.

**Out of scope for Epic 8:** subtitle *generation* (Pipeline / Epic 1 in
the other doc), thumbnail *generation* (Pipeline), session *minting*
(Epic 7 Story 7.10), JWT *issuance* (Epic 10). The Streaming Service
consumes all of those.

## Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 8.1 ✓ | [Server skeleton, signed URL middleware, metrics](story-08-01-server-skeleton.md)     | —          |
| 8.2 ✓ | [Capability matrix and client profile registry](story-08-02-capability-matrix.md)       | 8.1        |
| 8.3 ✓ | [Direct play (range-served `206 Partial Content`)](story-08-03-direct-play.md)    | 8.1, 8.2   |
| 8.4 ✓ | [Direct stream (FFmpeg `-c copy` remux)](story-08-04-direct-stream-remux.md)              | 8.1, 8.2   |
| 8.5 ✓ | [HLS adaptive transcode pipeline](story-08-05-hls-transcode.md)                      | 8.1, 8.2   |
| 8.6 ✓ | [DASH manifest (opt-in per session)](story-08-06-dash-manifest.md)                   | 8.5        |
| 8.7 ✓ | [Hardware acceleration auto-detect](story-08-07-hwaccel-detect.md)                    | 8.5        |
| 8.8 ✓ | [gRPC server: OpenSession / CloseSession / EvictHashCache / GetCapabilities](story-08-08-grpc-server.md) | 8.1, 8.5 |
| 8.9 ✓ | [Session store, sticky transcoder, reaper](story-08-09-session-store.md)             | 8.8        |
| 8.10 ✓| [Concurrency caps, backpressure, slot accounting](story-08-10-concurrency-caps.md)      | 8.5, 8.9   |
| 8.11 ✓| [Live subtitle rendering (auto, sidecar, embedded)](story-08-11-live-subtitle.md)    | 8.5, 8.1   |
| 8.12 ✓| [Chapter delivery (HLS DATERANGE + sidecar JSON)](story-08-12-chapter-delivery.md)      | 8.5, 8.1   |
| 8.13 ✓| [Posters, sprite sheets, chapter thumbs serving](story-08-13-posters-sprites.md)       | 8.1        |
| 8.14 ✓| [Cache layout, LRU GC, cap enforcement](story-08-14-cache-gc.md)                | 8.1        |
| 8.15 ✓| [Probe cache (LRU + Postgres)](story-08-15-probe-cache.md)                         | 8.1        |

## Schema additions owned by this epic

### `streaming_sessions`

Owned by Story 8.9; required because architecture §4.2 references it but
no §8 schema exists. The cascade chain `libraries → videos → streaming_sessions`
covers Epic 9 Story 9.15's deletion semantics (the cascade goes through
`videos.library_id`, not directly).

```
CREATE TABLE streaming_sessions (
  id                UUID PRIMARY KEY,                  -- v7
  video_id          UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  client_profile    TEXT NOT NULL,
  mode              TEXT NOT NULL,                     -- 'direct' | 'remux' | 'transcode' | 'direct-degraded'
  format            TEXT NOT NULL,                     -- 'hls' | 'dash'
  host              TEXT NOT NULL,
  pid               INTEGER,
  started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_segment_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  closed_at         TIMESTAMPTZ,
  closed_reason     TEXT,                              -- 'api' | 'idle' | 'crash' | 'evicted' | 'user-stop' | 'admin-evict' | 'hw_failed_software_failed' | 'store-insert-failed'
  state             TEXT NOT NULL DEFAULT 'active'     -- 'active' | 'queued'
);
CREATE INDEX streaming_sessions_reaper
  ON streaming_sessions (last_segment_at) WHERE closed_at IS NULL;
CREATE INDEX streaming_sessions_user_video
  ON streaming_sessions (user_id, video_id) WHERE closed_at IS NULL;
```

## Streaming JWT validation surface

All Streaming endpoints (manifest, segment, direct, posters, sprites,
subtitles) run through the signed-URL middleware in Story 8.1. The
middleware enforces:

| Path family                              | Required `aud`           | `sub`            | Additional claims used |
|------------------------------------------|--------------------------|------------------|-----------------------|
| `/stream/{session_id}/manifest.*`        | `streaming`              | `session_id`     | `usr`, `lib[]`         |
| `/stream/{session_id}/{rendition}/...`   | `streaming`              | `session_id`     | `usr`, `lib[]`         |
| `/stream/direct/{video_id}`              | `streaming-direct`       | `video_id`       | `usr`, `lib[]`         |
| `/stream/posters/{video_id}.jpg`         | `streaming-static`       | `<artifact-hash>`| `usr`, `lib[]`         |
| `/stream/sprites/{video_id}.{webp,vtt}`  | `streaming-static`       | `<artifact-hash>`| `usr`, `lib[]`         |
| `/stream/thumbs/{video_id}/...`          | `streaming-static`       | `<artifact-hash>`| `usr`, `lib[]`         |
| `/stream/{session_id}/subs/*`            | `streaming-static`       | `<artifact-hash>`| `usr`, `lib[]`         |

Streaming additionally checks `lib[]` against the video's library and
rejects requests whose `lib[]` claim doesn't include the target
library. This is the core of Epic 10 Story 10.7's offline authorization.

## Sequencing

Land in order: 8.1/8.15 → 8.2 → 8.3 → 8.8/8.9 → 8.5/8.7 → 8.10 →
8.4/8.6 → 8.11/8.12 → 8.13/8.14.
