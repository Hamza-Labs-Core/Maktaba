# The broadcast edge

Epic 50 story 50.21. The code is `web/packages/broadcast-edge/` (the Worker and its Durable
Objects) and `cloud/internal/broadcast/` with `cloud/internal/handlers/broadcast/` (the cloud's
ingest tokens). README §14.2 has the design; this page is how the edge works and how to run it.

The edge is **infrastructure only** (README §14.6). It stores and delivers the segments and the
guide a home sends, and it enforces access, limits and revocations. It never chooses, reorders,
ranks or recommends content.

## The picture

```
home ──PUT each segment once──► ingest.maktaba.tv ──► R2 (10 minutes)
                                       │                    ▲ miss: one read per location
                                       └─► ChannelLive ◄────┼──── live.maktaba.tv ◄── players
                                           (playlists)      │      (token in the path,
cloud API ──signed plan / revocations──► PublisherControl   │       cache key without it)
        ◄──signed usage reports──────────  Revocations ─────┘
```

- **One Worker, three hostnames**, all proxied Workers Custom Domains (they have no origin):
  `ingest.maktaba.tv`, `live.maktaba.tv` and `watch.maktaba.tv`; staging is
  `staging-ingest.maktaba.dev`, `staging-live.maktaba.dev` and `staging-watch.maktaba.dev`
  (docs/cloud-dns.md). On any other host (wrangler dev, the rehearsal) `/v1/…` is ingest and
  everything else is live.
- **The home is never fetched.** It pushes each segment of each rung once; the edge serves every
  viewer from R2 and the edge cache. The home's upload is the same for 1 viewer as for 10,000.

## Credentials

No secret is shared with a home. Every signature covers `"<domain>." + base64url(payload)`, so
one kind never passes as another.

| Credential | Format | Signed by | Checked with |
|---|---|---|---|
| Ingest token | `mkbi1.<payload>.<Ed25519>`, domain `maktaba-broadcast-ingest-v1` | the cloud (`broadcast.key_path`) | `CLOUD_PUBKEYS` |
| Control post | `mkbc1.<payload>.<Ed25519>`, domain `maktaba-broadcast-control-v1` | the cloud | `CLOUD_PUBKEYS` |
| Viewer token | `mkbv1.<payload>.<HMAC-SHA256>`, domain `maktaba-broadcast-viewer-v1` | `watch.maktaba.tv` (50.26) | `VIEWER_TOKEN_KEY` |
| Edge post | headers `X-Maktaba-Edge-Time` (unix ms), `X-Maktaba-Edge-Signature` (base64url Ed25519 over `maktaba-broadcast-edge-v1\n<METHOD>\n<path>\n<ms>\n<hex sha256 body>`), at most 5 minutes from the cloud's clock | the edge (`EDGE_SIGNING_KEY`) | the cloud (`broadcast.edge_public_key`, `broadcast.VerifyEdgeRequest`) |

- **Ingest token** payload: `{typ: "ingest", pub, ch, rungs[], home, kbps, iat, exp, kid}`.
  `pub` is the publisher: the home (its server id, the same as `home`), the id the edge keys
  `PublisherControl` by and the cloud bills usage to; `ch` the edge's channel id, `rungs` the rung names
  the channel may push (top first, at most 4, never `slate`), `kbps` the plan's top bitrate (the
  size guard). It lives 24 h; the home asks again every hour, so a cloud outage stops no
  broadcast for a day. A token for another channel, or a rung it does not list, is `403`; a bad,
  unknown-key or expired token is `401`.
- **Channel ids** are opaque: `hex(sha256("maktaba-broadcast-channel-v1\0" + server id + "\0" +
  the home's channel id))[:32]` (`broadcast.ChannelID`). Viewers see them in URLs; they say
  nothing about the home.
- **Key rotation.** Each payload names its key (`kid`, the first 8 bytes of the public key's
  SHA-256 in hex). Add the new public key to `CLOUD_PUBKEYS`, switch `broadcast.key_path` to the
  new key, and remove the old public key 24 h later.
- **Viewer token** payload: `{pub, ch, session, mode, exp, country?}`, `exp` at most 10 minutes
  ahead. `mode` is `full`, `lowest`, `wait` (`429 broadcast_full`) or `off` (`403`).
- Both sides check the same vectors: `cloud/internal/broadcast/testdata/vectors.json` (TEST keys
  from public labels; `go test ./internal/broadcast -run TestVectors -update` rewrites them):
  the ingest token, the control posts, and the whole edge↔cloud contract below (a signed usage
  post, the cloud's reply to it, a publisher's state and the plan it becomes). The Go tests run
  the vectors' usage post through the cloud's handler and compare its reply byte for byte; the
  edge's tests (`test/contract.test.ts`) build the same post and read the same reply and push.

## The edge↔cloud contract

One contract, written once in Go (`cloud/internal/broadcast/edge.go`) and mirrored in
`src/publisher.ts` and `src/edge_sign.ts`; the vectors keep the two from drifting.

| Call | Direction | Body | Answer |
|---|---|---|---|
| `POST /_edge/usage` | edge → `API_ORIGIN`, edge post | `{edge, windows: [UsageWindow]}`, at most 2,000 windows (a longer queue goes in pages) | `200 {taken: [key], duplicates: [key], rejected: [{key, error}], states: [PublisherState], plans: [Plan]}`; `401 bad_edge_signature`, `400` over 2,000, `503 try_again` (resent later) |
| `GET /_edge/publishers/{id}` | edge → cloud, edge post | none | `200 PublisherState`, `404 unknown_publisher` |
| `PUT /v1/control/plan` | cloud → edge, control post `typ: "plan"` (15 minutes) | `Plan` | `200 {status: "applied"}`, `409 older_plan` |
| `PUT /v1/control/revocations` | cloud → edge, control post `typ: "revocations"` | `{version, pubs[], channels[], homes[]}` | `200`; `GET` answers the version held |

- **UsageWindow**: `{key, publisher, window_start, window_end, minutes_full, minutes_lowest,
  minutes_stream, segments_in, bytes_in, peak_sessions}`, in that order. `key` is
  `<publisher>:<window_start>`; times are RFC 3339 UTC in whole seconds (`2026-11-13T00:05:00Z`,
  never `.000Z`), `window_end` 5 minutes after `window_start`. The cloud bills `minutes_full`
  and `minutes_lowest`; the other counters it checks (not negative) and does not store yet. A
  window posted again under its key is counted once, so the edge resends freely after an outage.
- **The reply** names each window by the key the edge sent. Taken, duplicate and rejected
  (`unknown_publisher`, `bad_window`: never taken) windows are done; any other stays queued.
- **Plan**: `{pub, version, mode, max_rungs?, max_top_kbps?, max_viewers?, delivery?}`
  (`broadcast.PlanFromState`: `version` is the state's time in unix ms, `max_viewers` the
  concurrent-viewer limit). The cloud pushes it signed (`broadcast.SignPlan`) and returns it in
  every usage reply, so the edge's plan follows the cloud's at least every 5 minutes; an older
  `version` never replaces a newer one.

## Ingest (home → edge)

Every request carries `Authorization: Bearer <ingest token>` and `X-Maktaba-Time` (unix ms). A
time more than 60 s from the edge's is `400 clock_skew`, with the edge's time in
`X-Maktaba-Edge-Now` (the home shows `broadcast.clockSkew`, 50.22).

| Request | Answer |
|---|---|
| `POST /v1/broadcast/ingest-token` on `api.maktaba.tv` (basic auth: server id and secret), `{"channel": "<home's id>", "rungs": ["src", "720p", "480p"]}` | `{token, channel, expires_at, refresh_after_seconds, ingest_url, rungs, max_top_kbps}`; `401` without the home's credential, `403 broadcast_not_in_plan`, `403 plan_rungs`, `503 broadcast_disabled` |
| `PUT /v1/ingest/<channel>/<rung>/<seq>.ts` with `X-Maktaba-Body-SHA256`, `X-Maktaba-Duration` (s, ≤ 12), optional `X-Maktaba-Discontinuity: 1` and `X-Maktaba-Segment-Time` (ms, the PROGRAM-DATE-TIME) | `201` new; `200 duplicate` (a retry, nothing written); `200 stale` (its slot passed, nothing written); `409 conflict` (same seq, another body); `413 too_large`; `429 rate_limited` (`Retry-After: 1`); `403 revoked`, `broadcast_off`, `plan_rungs`; `503 storage_unavailable` (`Retry-After: 1`) |
| `PUT /v1/ingest/<channel>/slate/<0-7>.ts` (same headers) | a segment of the offline slate loop, uploaded once per publish |
| `POST /v1/ingest/<channel>/state` `{"rungs": [{"name", "bandwidth", "resolution", "codecs", "frame_rate"}], "on_air": bool}` | the master playlist's attributes; `on_air: false` ends the broadcast (`503 off_air` until segments come again) |
| `POST /v1/ingest/<channel>/stream` | the channel's Stream live input (the fallback): `{rtmps: {url, stream_key}}` |
| `GET /v1/ingest/ws` (WebSocket) | the control socket: `hello`, `rung_stalled`, `on_air`, `off_air`, `revoked`, `restored`, `plan_off`; `ping` answers `pong` |
| `PUT /v1/ingest/speedtest` | up to 10 s of body; `{bytes, ms, kbps}`, nothing written |

- **Guards.** A segment is at most `duration × kbps × 1.5` bytes (`kbps` from the token, or the
  plan's if lower). Each channel and rung takes 4 PUTs a second (exact, in `ChannelLive`); the
  `RL` binding is a coarse per-location guard in front.
- **Order.** Segments publish in `seq` order. One that arrives early waits until the gap fills;
  after one target duration the gap is skipped and the next segment carries a discontinuity. A
  segment for a slot already published or skipped is `200 stale`.
- **R2 keys:** `<env>/<channel>/<rung>/<seq>-<hash8>.ts` (`hash8`: the body's SHA-256, first 8
  hex digits).

## Delivery (edge → viewers)

| Request | What it does |
|---|---|
| `GET /<token>/<channel>/master.m3u8` | the rungs the session's mode allows (`full`: all; `lowest`: the lowest bandwidth), cached 1 s per mode |
| `GET /<token>/<channel>/<rung>/index.m3u8` | HLS v6: TARGETDURATION, a window of 6, DISCONTINUITY, PROGRAM-DATE-TIME; cached 1 s per rung and mode; a rung the session's mode or the plan does not allow is `403 rung_not_allowed` |
| `GET /<token>/<channel>/<rung>/<seq>-<hash8>.ts` | a segment: token check only, then the cache, then R2 on a miss (`immutable`, a day) |
| `POST /<token>/<channel>/beat` | the session's 60 s heartbeat: one viewer-minute in `full` or `lowest` |

- **Cache keys have no token:** `https://live.maktaba.tv/c/<channel>/<rung>/<file>` and
  `…/c/<channel>/master-<mode>.m3u8`, `…/c/<channel>/<rung>/index-<mode>.m3u8`. A segment needs only a token for its channel: its name is known only from a playlist the session was allowed, and the plan's rungs are refused at ingest. Every viewer in a location shares one copy, and
  Cloudflare's cache lock collapses simultaneous misses to one R2 read per location.
  `X-Maktaba-Cache: hit|miss` says which.
- **Headers:** CORS for `CORS_ORIGINS` (default the watch, app and site origins and the apps'
  web views), added per request and never stored; `Timing-Allow-Origin`;
  `X-Content-Type-Options: nosniff`; never a cookie.
- **Offline.** A rung with no segment for 3 target durations (12 s at 4 s) continues with the
  slate after a discontinuity, one slate entry per slate duration; the next segment returns it to
  live after another discontinuity. Each rung goes on its own: one stalled rung goes to the slate
  while the others stay live, and the home hears `rung_stalled`. After 24 h the channel is off
  air (`503 off_air`).
- **No DVR (Q19).** An alarm every 60 s deletes objects 10 minutes after they were written, and
  sweeps any untracked object of the channel older than that. An R2 lifecycle rule of 1 day is
  the backstop (an owner action).

## The Stream fallback

`BROADCAST_DELIVERY = stream`, or a publisher's plan with `delivery: "stream"`. The home asks
`POST /v1/ingest/<channel>/stream` for its live input (one per channel, on Maktaba's account,
signed URLs required, recording automatic) and pushes one rendition by RTMPS through Epic 34's
push relay. The channel's refusals come first (revoked, the plan off, off air), for the redirect and for the live input alike; then players asking `live.` for a playlist get a `302` to the Stream manifest with a signed
token (Epic 34's `sign.ts`, valid until the viewer token expires). Tokens, modes, limits,
revocations and metering stay the same; Stream's viewer-minutes are counted apart
(`minutes_stream`).

## The seams later stories fill

| Story | Seam | Where |
|---|---|---|
| 50.20, 50.26 | The cloud pushes a publisher's `Plan` (above) to `PUT /v1/control/plan`; until it does, the plan comes with each usage reply. `mode` is what a new session gets; `off` stops ingest and play. | `PublisherControl.setPlan`, `ChannelLive.setPlan`, `parsePlan` |
| 50.20 | Usage: 5-minute windows (above), kept 7 days, posted every 5 minutes to `API_ORIGIN` + `/_edge/usage` in pages of 2,000, signed (edge post). A window is resent until the cloud's reply names it. | `PublisherControl.alarm`, `pendingUsage`, `ack`, `readUsageReply` |
| 50.22 | The home's uploader: the ingest endpoints above, the slate, the control socket, the speed test. | `ingest.ts` |
| 50.25 | The revocation list: `PUT /v1/control/revocations` with a control post `{typ: "revocations", version, pubs[], channels[], homes[]}`; `GET` answers the version held. A newer list revokes what it adds (ingest and playlists refused, the home told) and restores what it drops. `BROADCAST_GLOBAL_OFF = 1` stops everything. | `Revocations.install`, `ChannelLive.revoke` |
| 50.26 | Viewer tokens (mint with `mintViewerToken`, the same `VIEWER_TOKEN_KEY`), the session's mode, the concurrent count (`PublisherControl.beat` answers `concurrent`), the watch site on `watch.` | `tokens.ts`, `publisher.ts`, `worker.ts` |
| 50.28 | Analytics Engine `broadcast_events`: per request `[kind, channel, cache status]` (segment, playlist, master), per channel a minute's `[segments_in, bytes_in, playlist_misses]`, per viewer-minute `[channel, mode, delivery]` | `live.ts`, `channel_live.ts`, `publisher.ts` |

## Settings

| Name | Kind | What |
|---|---|---|
| `SEGMENTS` | R2 | `maktaba-broadcast-<env>`: segments and slates |
| `GUIDE` | R2 | `maktaba-broadcast-guide-<env>`: guide blobs (50.26) |
| `CHANNEL`, `PUBLISHER`, `REVOCATIONS` | Durable Objects | `ChannelLive`, `PublisherControl`, `Revocations` |
| `AE` | Analytics Engine | `broadcast_events` |
| `RL` | Rate Limiting | 60 per 10 s per channel and rung, per location |
| `BROADCAST_ENV` | var | `staging` or `production`: the R2 keys' first part |
| `BROADCAST_DELIVERY` | var | `r2` (default) or `stream` |
| `BROADCAST_GLOBAL_OFF` | var | `1` stops every broadcast (50.25's break-glass) |
| `INGEST_HOST`, `LIVE_HOST`, `WATCH_HOST` | var | the hostnames (the cache keys use `LIVE_HOST`) |
| `API_ORIGIN` | var | where usage is posted; unset, nothing is |
| `CORS_ORIGINS` | var | comma-separated; unset, the defaults above |
| `CLOUD_PUBKEYS` | secret | `{"<kid>": "<base64url Ed25519 public key>"}`: `broadcast.PublicKeysJSON` prints it |
| `VIEWER_TOKEN_KEY` | secret | base64url, 32 bytes; the same on `watch.` |
| `EDGE_SIGNING_KEY` | secret | base64url Ed25519 seed; its public half is the cloud's `broadcast.edge_public_key` |
| `STREAM_ACCOUNT_ID`, `STREAM_CUSTOMER_CODE`, `STREAM_KEY_ID` | var | the fallback's account and signing key id |
| `STREAM_API_TOKEN`, `STREAM_KEY_JWK` | secret | the fallback's API token (Stream: Edit) and signing key |

The cloud's side (`cloud.toml`): `[broadcast] enabled` (50.20's broadcasting gate; the api role
refuses to start with it on and no `key_path`), `key_path` (the Ed25519 seed, 0600; without it
the ingest-token route answers `503 broadcast_disabled`), `ingest_url` (default
`https://ingest.maktaba.tv`) and `edge_public_key` (base64url, as printed from the seed; standard
base64 is read too).

Who gets an ingest token is 50.20's entitlement (`broadcasth.StorePlans`): a home with no plan,
or whose owner is not active, gets `403 broadcast_not_in_plan`; while `broadcast.enabled` is off,
only a test publisher a service admin granted gets one (`403 broadcast_not_enabled` for the
others); otherwise the home's tier from the catalog sets the rungs and the top bitrate (Pro's
guest sharing, or the Broadcaster add-on), and more rungs than it allows is `403 plan_rungs`.

## Running it

```sh
cd web/packages/broadcast-edge
npm ci
npm test                        # typecheck and the tests in Miniflare (T01-T09)
node scripts/rehearsal.mjs      # wrangler dev, a fake home, 100 viewers (CI: 1,000)
npx wrangler deploy --dry-run   # the bundle and bindings, offline
```

CI runs all of this in `_broadcast-edge.yml` whenever the package, the cloud's broadcast package
or its edge API (`cloud/internal/handlers/edge`) changes; the rehearsal's report is kept as the `broadcast-edge-rehearsal` artifact.

## Before the first deploy (owner)

Nothing here is deployed. The first deploy needs, on Maktaba's Cloudflare account:

1. Cloudflare's written confirmation that serving HLS from R2 through Workers fits its terms at
   our volume (README §14.10, question 8). Until then, deploy with `BROADCAST_DELIVERY = stream`
   or not at all.
2. The R2 buckets `maktaba-broadcast-staging`, `maktaba-broadcast-guide-staging` (and the
   `-production` pair), each with a lifecycle rule deleting objects after 1 day.
3. The Rate Limiting namespaces `5021` (staging) and `5022` (production), if the account's
   numbering differs, changed in `wrangler.toml`.
4. The secrets, with `wrangler secret put` (`--env production` for production): `CLOUD_PUBKEYS`,
   `VIEWER_TOKEN_KEY`, `EDGE_SIGNING_KEY`, and for the fallback `STREAM_API_TOKEN` and
   `STREAM_KEY_JWK` (with `STREAM_ACCOUNT_ID`, `STREAM_CUSTOMER_CODE` and `STREAM_KEY_ID` as vars).
5. The cloud's key: an Ed25519 seed at `broadcast.key_path` on both nodes, and
   `broadcast.edge_public_key`.
6. The CI token (or `CLOUDFLARE_API_TOKEN_BROADCAST`) with Account: Workers Scripts Edit, Workers
   R2 Storage Edit; Zone `maktaba.tv` and `maktaba.dev`: Workers Routes Edit, Zone Read. Custom
   domains need no DNS Edit. `tools/cloudflare/token-check.mjs --pieces broadcast` checks it.
7. Run **Broadcast edge deploy** (`broadcast-edge.yml`) by hand: staging first, then production
   with `broadcast.enabled = false` in the cloud until the broadcasting gate (README §14.11).

## Rollback

- **Broadcast edge deploy** with `rollback: true` (`wrangler rollback`); Durable Object state
  is kept.
- `BROADCAST_DELIVERY = stream`, globally or per publisher (a plan push), if R2 delivery must
  stop.
- `BROADCAST_GLOBAL_OFF = 1` stops everything.
