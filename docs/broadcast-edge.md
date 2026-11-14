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
| Edge post | headers `X-Maktaba-Edge-Time`, `X-Maktaba-Edge-Signature` (Ed25519 over `maktaba-broadcast-edge-v1\n<METHOD>\n<path>\n<ms>\n<hex sha256 body>`) | the edge (`EDGE_SIGNING_KEY`) | the cloud (`broadcast.edge_public_key`, `broadcast.VerifyEdgeRequest`) |

- **Ingest token** payload: `{typ: "ingest", pub, ch, rungs[], home, kbps, iat, exp, kid}`.
  `pub` is the publisher (the home's owner), `ch` the edge's channel id, `rungs` the rung names
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
  from public labels; `go test ./internal/broadcast -run TestVectors -update` rewrites them).

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

## Viewer access (50.26)

Who may watch a channel. Viewers have no account and never pay. Each channel is **invite** (the
default) or **open**; open needs a verified publisher (a paid plan, a confirmed email, 7 days
since the first broadcast), the Broadcaster add-on, and the cloud's
`broadcast.open_access.enabled`.

| Credential | Shape | Made by | Lives |
|---|---|---|---|
| Invite | `mkbn1.<payload>.<Ed25519>` (domain `maktaba-broadcast-invite-v1`): `{typ, pub, chs, inv, max, iat, exp, kid}` | the cloud (`cloud/internal/broadcast/invites.go`), for the home's owner or admin | up to 365 days, `max` devices |
| Short code | `MK-` and 4 of A–Z, 0–9 | the cloud, beside each invite | the invite's life |
| Guest pass | `mkbg1.` HMAC (`VIEWER_TOKEN_KEY`, domain `maktaba-broadcast-guest-v1`) | the edge, one per device that redeems | 30 days, renewed past half |
| Open session | `mkbo1.` HMAC (domain `maktaba-broadcast-open-v1`) | the edge, after Turnstile or an app attestation | 12 h |
| Viewer token | the 50.21 token | the edge, per play | 10 minutes, refreshed after 5 |

The watch routes (on `WATCH_HOST`, and under `/api/` and `/i/` on any host in dev):

| Route | What |
|---|---|
| `GET /i/<invite>` | a server-rendered page (Arabic by default, `noindex`) with one button; `POST` redeems |
| `POST /api/v1/redeem` | `{invite}` or `{code}`: a guest pass (cookie `mkp_<pub>`, or the body for apps) |
| `POST /api/v1/open/turnstile` | `{token}`: an open session (cookie `mko`) |
| `POST /api/v1/open/challenge`, `/api/v1/open/attest` | a stateless 5-minute challenge, then `{platform, attestation, nonce}`: an open session |
| `POST /api/v1/play` | `{channel}`: `session_id`, `beat_key`, `master_url`, `refresh_after` |
| `POST /api/v1/play/<session>/beat` | `{beat_key}`: keeps the session (150 s without one ends it) and meters the minute |

`ViewerAccess` (Durable Object `VIEWER`, one per publisher) holds the access post the cloud
pushes (`PUT /v1/control/access`, a control post `{typ: "access", pub, version, channels, codes,
revoked_invites, revoked_passes, max_sessions_per_ip, daily_minutes}`; an older version is
`409`), the passes, the live sessions (one per credential: a new play ends the old one), the
counts, and the short-code tries (10 a minute per address, 60 failed a minute per publisher).
`POST /v1/control/passes` answers the passes and each invite's uses for the publisher's page.
Spent attestation challenges are rows in 16 more instances (`attest-spent-N`), spent only after
the attestation holds, and refused rather than forgotten when 50,000 are live.

Limits: the plan's concurrent viewers (`max_viewers`), a channel's own cap, the open sessions
per network (default 3; a guest pass never counts), the daily minutes, and `PLAY_RL` (30 plays)
and `API_RL` (120 requests) a minute per address. A limit stops a new viewer, never one
watching: `wait` and a full channel are `429 broadcast_full`, `off` is `403`, no plan yet is
`503 plan_unknown`. The cloud pushes each change at once and every pending one each minute, and
refreshes every publisher's post hourly. Addresses are kept only as a salted hash that changes
every UTC day.

## Guest viewers: the watch pages, guide and TV pairing (50.27)

The home publishes each channel's guide with its ingest token:
`POST /v1/ingest/<channel>/guide`, at most 256 KB, replaced whole on every post (`guide.ts`):

```json
{ "schema": 1,
  "publisher": { "name": "…", "logo": "data:image/png;base64,…" },
  "channel": { "name": "…", "number": 7 },
  "programmes": [ { "start": "ISO", "end": "ISO", "title": "…",
                    "attribution": { "title": "…", "by": "…", "licence": "CC BY 4.0", "url": "https://…" } } ] }
```

It lands in `GUIDE` at `<env>/guide/<pub>/<channel>.json`, with the publisher's branding (a name
and an optional PNG, WebP or JPEG logo of at most 64 KB) beside it. The edge checks the shape,
sorts the programmes by start and keeps those from a day ago to 8 days ahead; it never adds to
or reorders the schedule.

The pages (server-rendered, Arabic by default; `/ar/` and `/en/` decide, else
`Accept-Language`; `/<publisher>…` redirects to the guest's language):

| Route | What |
|---|---|
| `GET /<lang>/<publisher>` | the channels the guest's pass or open session allows (only open ones without a credential), with now and next |
| `GET /<lang>/<publisher>/<channel>` | the player, now and next, today, a CC BY programme's credit, Report; `noindex` unless the publisher set the channel indexable |
| `GET /<lang>/<publisher>/guide` | today's programmes per channel |
| `GET /<lang>/legal/{copyright,terms,privacy}` | summaries, linking to the full text on `maktaba.tv` |
| `GET /pair?tv=<nonce>`, `POST` | the phone's side of TV pairing: paste an invite link, type a short code (with `&p=<publisher>`), or send its open session |
| `GET /s/watch.js`, `/s/watch.css` | the pages' script and stylesheet, served by the edge |

`POST /i/<invite>` now ends with a `303` to the channel (an invite for one) or the grid. A page
links only to its own publisher's channels and guide, Maktaba's legal pages, the edge's script
and style, hls.js, Turnstile, and a CC BY programme's source (a test lists every link). The
player is hls.js on the page (from jsDelivr, as Epic 34's site): it starts on the lowest rung,
swaps each new viewer token from the beat into the playlist and segment URLs, and switches with
up/down, PageUp/PageDown, a vertical swipe and number entry. Its states come from
`site/states.ts` (`viewerState`), whose source the page script embeds, so the page and the
Worker map answers the same way. `play` and `beat` answers carry `channel_state` (`live`,
`waiting`, `offline_slate`, `off_air`, `revoked`): the slate plays with the offline note and no
error.

The apps' reads, with the pass as `Authorization: Bearer`:

| Route | What |
|---|---|
| `GET /api/v1/lineup?publisher=` | `{publisher: {id, name, logo?}, channels: [{id, name, number, state, now, next}]}` |
| `GET /api/v1/guide?publisher=&channel=` | the channel's programmes; `403` for a channel the credential does not open |
| `POST /api/v1/pair` | a TV's pairing: `{nonce, poll_key, code, url, expires_at, poll}`, 10 minutes |
| `GET /api/v1/pair/<nonce>?wait=25` | the TV's long-poll, with `X-Maktaba-Pair-Key: <poll_key>`: `200 {publisher, credential, kind, channels, expires_at}` once, `204` not yet, `410 pair_gone` (also a missing or wrong key) |
| `POST /api/v1/pair/<nonce>` | the phone: `{invite}` (a link or the token), `{publisher, code}`, or `{publisher, open: true}` with its open session |

`Pairing` (Durable Object `PAIRING`, migration `v3`, one per nonce) keeps the credential until
the TV takes it, then forgets it, and deletes everything at the end of its 10 minutes. The QR
code carries only the nonce; the poll key stays on the TV, so someone who sees the code cannot
collect the credential. The TV shows a 4-character code beside the QR code and the phone's page
shows the same one, to check before sending (RFC 8628 §5.4). An invite sent to a TV counts a new
device use; the phone's own pass is never reused. For an open session the TV gets its own token
of the phone's session (the same id and expiry, never the phone's cookie), so it shares the
phone's limits and no Turnstile check is skipped or extended.

`WATCH_SITE_OFF = 1` turns every page into "temporarily unavailable" while the API and playback
stay up; `TURNSTILE_SITE_KEY` shows the Turnstile widget on an open channel's page.

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
| 50.20 | The cloud pushes a publisher's plan: `PUT /v1/control/plan` with a control post `{typ: "plan", pub, mode, version, max_rungs?, max_top_kbps?, max_viewers?, delivery?}`. An older `version` is `409`. `mode` is what a new session gets; `off` stops ingest and play. | `PublisherControl.setPlan`, `ChannelLive.setPlan` |
| 50.20 | Usage: 5-minute windows `{key: "<pub>:<window start>", publisher, window_start, window_end, minutes_full, minutes_lowest, minutes_stream, segments_in, bytes_in, peak_sessions}`, kept 7 days, posted every 5 minutes to `API_ORIGIN` + `/_edge/usage` as `{edge: "broadcast-edge", windows: [...]}`, signed (edge post). A window is resent until a `2xx`. | `PublisherControl.alarm`, `pendingUsage`, `ack` |
| 50.22 | The home's uploader: the ingest endpoints above, the slate, the control socket, the speed test. | `ingest.ts` |
| 50.25 | The revocation list: `PUT /v1/control/revocations` with a control post `{typ: "revocations", version, pubs[], channels[], homes[]}`; `GET` answers the version held. A newer list revokes what it adds (ingest and playlists refused, the home told) and restores what it drops. `BROADCAST_GLOBAL_OFF = 1` stops everything. | `Revocations.install`, `ChannelLive.revoke` |
| 50.26 | Viewer tokens (mint with `mintViewerToken`, the same `VIEWER_TOKEN_KEY`), the session's mode, the concurrent count (`PublisherControl.beat` answers `concurrent`), the watch site on `watch.` | `tokens.ts`, `publisher.ts`, `worker.ts` |
| 50.28 | Analytics Engine `broadcast_events`: per request `[kind, channel, cache status]` (segment, playlist, master), per channel a minute's `[segments_in, bytes_in, playlist_misses]`, per viewer-minute `[channel, mode, delivery]` | `live.ts`, `channel_live.ts`, `publisher.ts` |

## Settings

| Name | Kind | What |
|---|---|---|
| `SEGMENTS` | R2 | `maktaba-broadcast-<env>`: segments and slates |
| `GUIDE` | R2 | `maktaba-broadcast-guide-<env>`: the channels' guides and the publishers' branding (50.27) |
| `CHANNEL`, `PUBLISHER`, `REVOCATIONS`, `VIEWER`, `PAIRING` | Durable Objects | `ChannelLive`, `PublisherControl`, `Revocations`, `ViewerAccess` (migration `v2`), `Pairing` (migration `v3`, 50.27) |
| `AE` | Analytics Engine | `broadcast_events` |
| `RL` | Rate Limiting | 60 per 10 s per channel and rung, per location |
| `PLAY_RL`, `API_RL` | Rate Limiting | 30 plays and 120 other watch requests a minute per address (50.26) |
| `TURNSTILE_API`, `ATTEST_API` | service bindings | optional: Turnstile's siteverify (else the public one) and the app-attestation verifier (50.27; unbound, attestation answers `503`) |
| `BROADCAST_ENV` | var | `staging` or `production`: the R2 keys' first part |
| `BROADCAST_DELIVERY` | var | `r2` (default) or `stream` |
| `BROADCAST_GLOBAL_OFF` | var | `1` stops every broadcast (50.25's break-glass) |
| `INGEST_HOST`, `LIVE_HOST`, `WATCH_HOST` | var | the hostnames (the cache keys use `LIVE_HOST`) |
| `API_ORIGIN` | var | where usage is posted; unset, nothing is |
| `CORS_ORIGINS` | var | comma-separated; unset, the defaults above |
| `WATCH_SITE_OFF` | var | `1`: the watch pages say "temporarily unavailable" (50.27's rollback) |
| `TURNSTILE_SITE_KEY` | var | Turnstile's public site key for the open channels' widget (50.27) |
| `CLOUD_PUBKEYS` | secret | `{"<kid>": "<base64url Ed25519 public key>"}`: `broadcast.PublicKeysJSON` prints it |
| `VIEWER_TOKEN_KEY` | secret | base64url, 32 bytes; the same on `watch.` |
| `TURNSTILE_SECRET` | secret | Turnstile's secret for open channels' web visitors (50.26) |
| `EDGE_SIGNING_KEY` | secret | base64url Ed25519 seed; its public half is the cloud's `broadcast.edge_public_key` |
| `STREAM_ACCOUNT_ID`, `STREAM_CUSTOMER_CODE`, `STREAM_KEY_ID` | var | the fallback's account and signing key id |
| `STREAM_API_TOKEN`, `STREAM_KEY_JWK` | secret | the fallback's API token (Stream: Edit) and signing key |

The cloud's side (`cloud.toml`): `[broadcast] enabled`, `key_path` (the Ed25519 seed, 0600;
the api role refuses to start with `enabled` and no key), `ingest_url` (default
`https://ingest.maktaba.tv`) and `edge_public_key`. Until 50.20 lands, an active Pro owner's home
gets Pro's guest sharing (2 rungs, 3,000 kbps) and any other plan none.

## Running it

```sh
cd web/packages/broadcast-edge
npm ci
npm test                        # typecheck and the tests in Miniflare (50.21, 50.26, 50.27)
node scripts/rehearsal.mjs      # wrangler dev, a fake home, 100 viewers (CI: 1,000)
npx wrangler deploy --dry-run   # the bundle and bindings, offline
```

CI runs all of this in `_broadcast-edge.yml` whenever the package or the cloud's broadcast
package changes; the rehearsal's report is kept as the `broadcast-edge-rehearsal` artifact.

## Before the first deploy (owner)

Nothing here is deployed. The first deploy needs, on Maktaba's Cloudflare account:

1. Cloudflare's written confirmation that serving HLS from R2 through Workers fits its terms at
   our volume (README §14.10, question 8). Until then, deploy with `BROADCAST_DELIVERY = stream`
   or not at all.
2. The R2 buckets `maktaba-broadcast-staging`, `maktaba-broadcast-guide-staging` (and the
   `-production` pair), each with a lifecycle rule deleting objects after 1 day.
3. The Rate Limiting namespaces `5021` (staging) and `5022` (production), and for viewer access
   `5261`/`5262` (staging) and `5263`/`5264` (production); if the account's numbering differs,
   changed in `wrangler.toml`.
4. The secrets, with `wrangler secret put` (`--env production` for production): `CLOUD_PUBKEYS`,
   `VIEWER_TOKEN_KEY`, `EDGE_SIGNING_KEY`, and for the fallback `STREAM_API_TOKEN` and
   `STREAM_KEY_JWK` (with `STREAM_ACCOUNT_ID`, `STREAM_CUSTOMER_CODE` and `STREAM_KEY_ID` as vars),
   and `TURNSTILE_SECRET` (a Turnstile widget for `watch.maktaba.tv`) before any channel is open.
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
- `WATCH_SITE_OFF = 1` takes the watch pages down alone (50.27).
