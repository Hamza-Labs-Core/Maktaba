# Broadcasting from home: the home's side

A home can publish chosen Live TV channels to viewers outside it. The home makes each channel's
qualities and sends every segment once to Maktaba's broadcast edge. The edge serves every
viewer from its cache, so the home's upload is the same for one viewer as for ten thousand.
This guide is for contributors who work on the home's side: the Broadcast page, the publish
checks, and the broadcaster in the front door. The edge itself is in `docs/broadcast-edge.md`.

Every route here is described in the [API reference](https://maktaba.dev/api/), under
**Broadcasting**. All of them are admin only, and the API enforces that: a viewer gets `403`, a
caller with no session `401`, before anything is read or sent to Maktaba Cloud.

## The parts

| Part | Where | What it does |
|---|---|---|
| Broadcast page | `web/src/pages/Admin/LiveTV/BroadcastPanel.tsx` | set up a channel, publish it, see its state, measure the upload |
| Home API | `api/internal/broadcast/` | the routes, the publish checks, the speed test, the ingest-token refresh (`api:broadcast_tokens`) |
| Cloud link | `cloud/internal/cloudlink/broadcast.go` | the relay agent forwards five calls to Maktaba Cloud with the home's credential; the API never holds it |
| Broadcaster | `streaming/internal/broadcast/` | one ladder ffmpeg per channel, the uploader, the control socket, the offline slate (`streaming:broadcast_uploader`, `streaming:broadcast_control`) |
| Database | slot 0226 | `broadcast_channels`, `broadcast_uplink`, `broadcast_confirmations`, `broadcast_ingest_tokens` |
| Settings | `rules.broadcast` | `uplink_share` (0.5), `rung_slot_weight` (0.5), `allow_software_rungs` (off) |

All three loops belong to the `livetv.broadcast` component. It is in no bundle, so it is off
until an admin turns it on; the Broadcast page offers to.

## Publishing

`POST /api/tv/broadcast/channels/{id}/publish` runs the checks in this order and stops at the
first failure. The answer is `409` with `code: broadcast.<code>`, `check` and `params`, and
nothing is started.

1. **plan**: the Maktaba Cloud plan allows another channel, its access, its mode, its number of
   qualities and its top bitrate.
2. **agreement**: the publisher agreement is accepted.
3. **publishable**: enough of the next 24 hours is scheduled with items that may air. The
   `Publishable` interface is the seam the publishable-items story fills.
4. **uplink**: the measured upload, times `uplink_share`, carries this channel and the published
   ones (each ladder's total, plus 10 %). A refusal suggests a smaller ladder that fits.
5. **encoder**: the channel's host keeps `reserve_slots` free. A channel counts 1 slot, each
   encoded quality `rung_slot_weight` more, and the copied top quality none. Without a hardware
   encoder the lower qualities need `allow_software_rungs`.
6. **rights**: the admin ticked that the home has the rights to what the channel airs. The
   confirmation stays on the home; only its date and the agreement version go to the cloud.

Then the API publishes on the cloud and fetches the channel's ingest token through the agent.
Tokens are stored in `broadcast_ingest_tokens`, which a machine's database login cannot read.
The API refreshes them hourly; with the cloud away, the current token keeps the channel live
until it expires (24 h), then the channel waits at its slate with `broadcast.tokenExpired`.

## The broadcaster

- **Source.** It reads the channel's TS through the front door's relay source and holds the
  channel while it runs, so the warm grace never releases a published channel.
- **Ladder.** One ffmpeg reads the TS on stdin. The top quality (`src`) is copied; `720p`,
  `480p` and `360p` are encoded with the channel's encoder family (VideoToolbox, NVENC, QSV,
  VA-API, or libx264 when allowed) on the channel's keyframe grid. Segments are 4 s, numbered
  from the start time in 4 s slots, so a restarted ladder never reuses a number.
- **Uploader.** It reads each quality's playlist and PUTs each new segment once, with its hash,
  duration and program date time. It retries with jitter for two segment durations, then skips
  the segment and marks the next one as a discontinuity. At most 8 segments per quality stay on
  disk.
- **State.** Every 10 s it posts the qualities' attributes for the master playlist, and writes
  its report (segments sent, restarts) to `broadcast_channels.report`.
- **Control socket.** One outbound WebSocket per channel, reconnected with jitter. `wake` starts
  an on-demand channel; `sleep` stops it 5 minutes later. Until the edge has sent either, an
  on-demand channel runs as if always on, so a published channel is never dark. `revoked`, `plan_off` and `restored`
  change the channel's state; `limits` removes qualities the plan no longer allows.
- **Slate.** Once per token it makes a 4 s offline card (the channel's name and the offline
  words in English and Arabic) and uploads it. The texts are read from files, never put in the
  filter graph.

## Taken off air

Maktaba can stop a broadcast with a signed revocation list (story 50.25; the edge's side is in
`docs/broadcast-edge.md`, **Revocations**). The home honours it too:

- **Its copy.** Every 10 minutes the API reads the list through the agent
  (`GET /admin/cloud-link/broadcast/revocations`). It checks the Ed25519 signature against
  `BuiltinRevocationKeys` plus `MAKTABA_BROADCAST_PUBKEYS` (`{"<kid>": "<base64url key>"}`), and
  keeps the newest copy in `app_settings` (`broadcast.revocations`). An older version is refused,
  even after a restart. The cloud sends a home only its own entries and the opaque ones, signed
  as `typ: "revocations-home"`. With no keys configured the home does not honour a list.
- **What it does.** An entry naming a published channel's edge id, its publisher or this home
  sets the channel to `revoked` with detail `list:<reason>`. A later list that no longer names
  it lifts it; a stop the edge sent over the control socket is lifted by the edge's `restored`.
  A programme's slate changes nothing on the home: the edge loops the slate.
- **Publishing.** A stop refuses a new publish with `broadcast.revoked.channel`,
  `broadcast.revoked.suspended` or `broadcast.terminated`, each with `params.reason`. A copy
  past its 48 h expiry, or none, refuses a new publish with `broadcast.revocationsStale`
  (`params.days`); channels already published keep running.
- **The page.** `GET /api/tv/broadcast` carries `revocations: {version, fetched_at, stale, days,
  home?}`; the Broadcast page shows the stale warning, why the home is stopped, and why each
  revoked channel was taken off (`broadcast.reason.<reason>`).

## Tests

- `api/internal/broadcast`: the checks (T01, T04, T05), the role test (T09), and the database
  tests for publishing, tokens with the cloud away (T08) and the speed test.
- `streaming/internal/broadcast`: the ladder's arguments (T02), the uploader (T03), and the
  broadcaster against a fake edge (always on, on demand, a revoked channel, an expired token).
- `api/internal/broadcast/revocations_test.go` and `revocations_pg_test.go`: the cloud's signed
  vector, the strongest stop, anti-rollback, a stopped and lifted channel, a stale copy (T04).
- `web/src/pages/Admin/LiveTV/BroadcastPanel.test.tsx`: the page, in English and Arabic.
