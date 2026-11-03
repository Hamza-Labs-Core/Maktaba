# Story 50.21 — The broadcast edge: ingest, R2 segments, playlists, one fetch from the home

> Epic 50 · Phase B · Depends on: 50.01 · Needed by: 50.22, 50.25, 50.26, 50.28, 50.29

## Why

A home uplink can't serve many viewers: 100 viewers at 6.5 Mbps is 650 Mbps. The Hetzner relay
can't either: it carries about 30 viewers a node at 1080p, has no edge near viewers, and its
capacity belongs to paid remote play. Research §3–§4 shows the alternative: the home **pushes
each segment once** to Cloudflare R2, and a Worker serves it from Cloudflare's edge. That costs
about $0.001 a viewer-hour at any resolution, and the home's upload doesn't depend on the number
of viewers.

Epic 34's Worker (`web/packages/tvsite/`) is the starting point, but it serves one home on the
owner's own account, with one shared `HOME_HMAC`. This story builds a **multi-tenant** edge on
**Maktaba's** Cloudflare account.

**The edge is infrastructure only** (README §14.6). It stores and delivers the segments and the
guide the home sends, and enforces access, limits and revocations. It never chooses, reorders,
ranks or recommends content. The only content-related things it does are what hosting status
requires: takedown and kill switch (50.24, 50.25).

## Description

### 1. The package and the names

A new Worker package, `web/packages/broadcast-edge/`, kept separate from `tvsite`, which stays
the self-hosted Epic 34 site. It shares `homeauth`-style helpers and `@maktaba/tvplayer`. It
binds:

| Binding | What |
|---|---|
| `SEGMENTS` | R2 bucket `maktaba-broadcast-<env>` |
| `GUIDE` | R2 bucket for guide blobs (34.2 format), or a prefix in the same bucket |
| `CHANNEL` | DO class `ChannelLive`, one per published channel |
| `PUBLISHER` | DO class `PublisherControl`, one per publisher: plan state, limits, sessions, usage (50.20, 50.26) |
| `REVOCATIONS` | DO class `Revocations`, one per environment (50.25) |
| `AE` | Analytics Engine dataset `broadcast_events` (50.28) |
| `RL` | Rate Limiting binding |
| vars and secrets | `CLOUD_PUBKEYS` (Ed25519 public keys for ingest tokens and the revocation list), `VIEWER_TOKEN_KEY` (HMAC for viewer tokens), `EDGE_SIGNING_KEY` (signs posts to the API), `BROADCAST_GLOBAL_OFF` (break-glass, 50.25), `BROADCAST_DELIVERY` (`r2` \| `stream`) |

Three hostnames, all Workers Custom Domains (proxied) on `maktaba.tv`. They are added to
50.01's table in this story's PR.

| Name | Serves |
|---|---|
| `ingest.maktaba.tv` | `PUT /v1/ingest/<channel>/<rung>/<seq>.ts`, `POST /v1/ingest/<channel>/state`, `GET /v1/ingest/ws` (control WebSocket), `PUT /v1/ingest/speedtest` |
| `live.maktaba.tv` | `/<token>/<channel>/master.m3u8`, `/<token>/<channel>/<rung>/index.m3u8`, `/<token>/<channel>/<rung>/<seq>-<hash>.ts` |
| `watch.maktaba.tv` | site and API (50.26, 50.27); guide blobs |

### 2. Ingest (home → edge, outbound only)

- **Ingest token.** `api.maktaba.tv` issues it over the cloud link: an Ed25519-signed token
  `{pub, ch, rungs[], home, exp = now + 24h, kid}`.
  - The home refreshes it every hour.
  - The edge verifies it with `CLOUD_PUBKEYS`, so there is no shared secret at the edge.
  - Each request also carries `X-Maktaba-Time` and a body hash. The edge refuses a time more than
    60 s off, and replays (per-channel sequence numbers that only go up).
- **A segment upload** is `PUT /v1/ingest/<channel>/<rung>/<seq>.ts` with the body, its
  SHA-256, and its duration in a header. The Worker:
  1. checks the token, the revocation list (50.25) and the publisher's plan (rung count, top
     kbps);
  2. writes R2 key `<env>/<channel>/<rung>/<seq>-<hash8>.ts`;
  3. calls `ChannelLive.append(rung, seq, hash, duration, discontinuity?)`;
  4. returns `201`.

  A retried PUT with the same `seq` and hash is `200` and writes nothing. A same `seq` with a
  different hash is `409`.
- **Size and rate guards.** A segment is at most `duration × top_kbps × 1.5`. Bursts are limited
  to 4 PUTs a second per channel and rung. Anything else is `413` / `429`, and the home logs it.
- **Uplink test.** `PUT /v1/ingest/speedtest` takes up to 10 s of body, returns the measured rate
  and writes nothing (50.22).

### 3. `ChannelLive`: playlists and state

- It holds, per rung, the last 30 segments (≈ 2 min): `seq`, key, duration, and a discontinuity
  flag. It also holds the channel's live state: `live | offline_slate | off_air | revoked`, and
  `last_segment_at`.
- **Media playlist:** standard HLS v6:
  - `#EXT-X-TARGETDURATION` set from the segments;
  - a live window of the last 6 segments;
  - `#EXT-X-DISCONTINUITY` where flagged;
  - `#EXT-X-PROGRAM-DATE-TIME` from the home's time, for the latency probe (50.28).
- **Master playlist:** the rungs the publisher's mode allows for **this session** (50.20 §2),
  with `BANDWIDTH` and `RESOLUTION` from the home's state post.
- **Offline.** If `last_segment_at` is more than 3 × target duration old, playlists continue with
  the channel's **offline slate** segments: a short loop the home uploads once per publish
  (50.22). They are preceded by a discontinuity. When segments resume, there is another
  discontinuity and the channel is live again. After 24 h offline the state becomes `off_air`,
  and play is refused with `503 off_air`.
- **Clean-up.** An alarm every 60 s deletes R2 objects older than 10 minutes (deletes are free).
  An R2 lifecycle rule of 1 day is the backstop.

### 4. Delivery (edge → viewers), and one fetch from the home

- **Viewer token** (minted by `watch.maktaba.tv` in 50.26). It is an HMAC over
  `{pub, ch, session, mode, exp ≤ 10 min, country?}`, placed in the URL path, so players need no
  headers. That matters on native players and TVs.
- **Segments:**
  1. The Worker checks the token. This is cheap: no DO and no R2 yet.
  2. It looks up `caches.default` with a **cache key that has no token**
     (`https://live.maktaba.tv/c/<channel>/<rung>/<seq>-<hash>.ts`).
  3. On a miss it reads R2, stores the result with
     `Cache-Control: public, max-age=86400, immutable`, and returns it.

  Every viewer in a location shares one cached copy. Cloudflare's cache lock collapses
  simultaneous misses to one R2 read per location (research §2.5).
- **Playlists:** the Worker checks the token and the revocation state (the DO answers that with
  the playlist). It looks up the cache under a token-free key per rung, or per mode for the
  master playlist, with a TTL of 1 s. On a miss it asks `ChannelLive`. So the DO sees about one
  request per rung per location per second.
- **The home is never fetched.** Its upload is one PUT per segment per rung, the same for 1 viewer
  as for 10,000. 50.29's fan-out test asserts this.
- **Headers:** CORS for the watch origins and the app origins, plus
  `Timing-Allow-Origin` and `X-Content-Type-Options`. No cookies on `live.maktaba.tv`.

### 5. The Stream fallback (`BROADCAST_DELIVERY = stream`)

- **Why:** for publishers whose uplink can't carry a ladder, or if Cloudflare reads its terms
  against R2 delivery (README §14.10, question 8). In this mode the edge gives the home a **Stream
  live input on Maktaba's account** (one per channel, `requireSignedURLs`, recording automatic).
- **What the home does:** it pushes one rendition by RTMPS through Epic 34's push relay
  (`streaming/internal/frontdoor/relay`).
- **What the edge does:** `live.maktaba.tv` redirects players to the Stream manifest with a signed
  Stream token (Epic 34's `sign.ts`).
- Viewer tokens, modes, limits, revocations and analytics are the same. Stream's cost
  ($0.06 a viewer-hour) is metered from the same heartbeats.
- Per publisher: `broadcast_delivery` in `PublisherControl`. The default comes from the global var.

### 6. Logs and metrics

- The Worker logs in the 53.01 schema, with `service = broadcast-edge` and hashed ids. Logs go
  to Workers Logs and are forwarded by Logpush to the witness W (53.17's model).
- Every 60 s each `ChannelLive` reports `{segments_in, bytes_in, playlist_hits, cache_status
  counts}` to Analytics Engine.

## Acceptance criteria

- **Given** a valid ingest token for channel C with 3 rungs,
  **when** the home PUTs segment 100 of each rung,
  **then** each is in R2 within 1.5 s (p95) of the PUT starting, and the rung playlists list it.
- **Given** an ingest token for channel C,
  **when** it is used for channel D, or for a fourth rung,
  **then** the edge answers `403` and writes nothing.
- **Given** 1,000 viewers in one location watching C,
  **when** segment 101 is published,
  **then** R2 sees at most a handful of reads for it (one per cache miss in that location), and
  the home sends it exactly once per rung.
- **Given** a token in the segment path,
  **when** two viewers with different tokens fetch the same segment,
  **then** the second is a cache hit.
- **Given** the home stops uploading,
  **when** 12 s pass,
  **then** the playlists continue with the offline slate after a discontinuity, and the players
  keep playing without an error.
- **Given** the home resumes,
  **when** its next segment arrives,
  **then** the playlists return to live after a discontinuity.
- **Given** a segment older than 10 minutes,
  **when** the alarm runs,
  **then** its R2 object is deleted.
- **Given** `BROADCAST_DELIVERY = stream` for a publisher,
  **when** a viewer plays,
  **then** they are redirected to a signed Stream manifest, and limits and metering still apply.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (vitest) | ingest token verify | good / expired / wrong channel / wrong rung / bad signature / replayed seq | accept / 401 / 403 / 403 / 401 / 409 |
| T02 | unit | `ChannelLive` | append 40 segments, a discontinuity, a gap of 13 s | window of 6, discontinuity tags, slate after the gap, live again after |
| T03 | unit | cache key | two tokens, one segment | same key; the token never appears in the key |
| T04 | unit | master playlist | modes `full` / `lowest` | 3 rungs / 1 rung |
| T05 | unit | size and rate guards | oversize body, burst of 10 | 413 / 429 |
| T06 | integration (Miniflare + local R2) | uploader fake + 200 fake viewers | 10 minutes of segments | R2 reads ≤ segments × rungs × locations; home PUTs = segments × rungs |
| T07 | integration | alarm | segments 11 minutes old | deleted; newer kept |
| T08 | integration | Stream mode, a fake Stream API | play | 302 to a signed manifest; heartbeats metered |
| T09 | unit | headers | segment, playlist | immutable on segments, 1 s on playlists, no `Set-Cookie` on `live.` |
| T10 | rehearsal (CI, 50.29 `broadcast-fanout`) | home stack + edge in Miniflare | 1 home, 1,000 simulated viewers | home bytes out flat whatever the viewers; viewer p95 segment latency within target |

## Edge cases

- **A segment arrives out of order** (a retry after a later one). It is inserted by `seq` if it
  is still within the window, and dropped (`200`, nothing written) if it is older than the
  window.
- **A rung stops but the others continue** (the home's lower encode crashed). That rung's playlist
  goes to the slate after 12 s while the others stay live. Players on the master playlist move to
  a live rung. The home is told over the control socket.
- **The DO's location.** `ChannelLive` is created near the home (the first ingest request picks
  its location) with a location hint from the home's country. Players far away read
  edge-cached playlists, so only playlist misses travel to the DO.
- **A huge viewer spike in a new location.** The first viewer misses on each object, the cache
  lock holds the rest, and R2 reads stay one per object per location.
- **R2 is briefly unavailable.**
  - An ingest PUT returns `503` with `Retry-After: 1`; the home retries for up to 2 target
    durations, then skips that segment and marks the next one as a discontinuity.
  - Viewers keep playing cached segments; a miss returns `503`, and the player retries.
- **Clock skew at the home.** `PROGRAM-DATE-TIME` uses the home's clock. The edge refuses
  ingest more than 60 s off and tells the home to fix its time (`broadcast.clockSkew` in 50.22).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/packages/broadcast-edge/` (new): `wrangler.toml`, `src/worker.ts`, `src/ingest.ts`, `src/live.ts`, `src/channel_live.ts`, `src/publisher.ts`, `src/tokens.ts`, `src/cache.ts`, `src/stream_fallback.ts`, tests | The Worker, the DOs, tokens, caching. |
| `web/packages/tvsite/src/homeauth.ts`, `sign.ts` | Exported for reuse (no behaviour change). |
| `cloud/internal/broadcast/tokens.go` (new) | Ingest-token issuing (Ed25519), key rotation with `kid`. |
| `cloud/internal/handlers/broadcast/` (new) | `POST /v1/broadcast/ingest-token` (home over the cloud link). |
| `cloud/internal/config/config.go` | Broadcast keys and the edge's public key. |
| `.github/workflows/broadcast-edge.yml` (new) | Lint, typecheck, vitest, Miniflare integration; deploy job (manual, `production` environment, never run by this story's CI). |
| `specs/epics/50-cloud-no-downtime/story-50-01-names-and-dns.md` | Three rows in the names table (in the code PR). |
| `docs/broadcast-edge.md` (new) | How the edge works and its settings. |

### Tests

- T01–T09 in the new `broadcast-edge.yml` (path-filtered to `web/packages/broadcast-edge/**`
  and `cloud/internal/broadcast/**`, so it runs only for its own code).
- **CI only:** rehearsal `broadcast-fanout` (50.29).
- No live Cloudflare call in CI: Miniflare, local R2, and a fake Stream API.

### Rollout

1. Deploy to `maktaba-broadcast-staging` on Maktaba's account (the owner runs the first deploy;
   the token scopes are listed in `docs/broadcast-edge.md`).
2. The canary publisher (50.28) on staging.
3. Production with `broadcast.enabled = false`.
4. Then the broadcasting gate (README §14.11).

### Rollback

- Workers versions: `wrangler rollback` to the previous version. DO state is kept.
- `BROADCAST_DELIVERY = stream`, per publisher or globally, if R2 delivery must stop.
- `BROADCAST_GLOBAL_OFF = 1` stops everything (50.25).

### Dependencies

50.01 (names). Used by 50.22 (the uploader), 50.25 (revocations), 50.26 (viewer tokens),
50.28 (analytics) and 50.29 (rehearsal).
