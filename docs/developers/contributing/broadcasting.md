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
| Cloud link | `cloud/internal/cloudlink/broadcast.go` | the relay agent forwards the broadcast calls (publish, tokens, the revocation list, takedowns) to Maktaba Cloud with the home's credential; the API never holds it |
| Broadcaster | `streaming/internal/broadcast/` | one ladder ffmpeg per channel, the uploader, the control socket, the offline slate (`streaming:broadcast_uploader`, `streaming:broadcast_control`) |
| Database | slots 0226, 0227, 0228 | `broadcast_channels`, `broadcast_uplink`, `broadcast_confirmations`, `broadcast_ingest_tokens`; the publishable rule (0227); `broadcast_takedowns` (0228) |
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
3. **publishable**: at least 6 of the next 24 hours are scheduled with publishable items (see
   "What can I broadcast?" below).
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

## What can I broadcast?

Free to watch isn't a licence to re-broadcast. While a channel is published, it airs only
**publishable** items, and the home records why each one may air. This is a guide to what the
code does, not legal advice.

An item is publishable for one of four reasons, in this order (`video_publishable_reason`, slot
0227; the scheduler, the streaming player and the API all call the same SQL):

| Reason | Where it comes from |
|---|---|
| `own` | the admin marked it their own recording (one item, a folder, a series or a collection) |
| `cleared` | the admin marked one item cleared, with a proof link |
| `cc_by` | yt-dlp or the YouTube API says Creative Commons Attribution; stored in `youtube_videos.license`, read at intake and at every refresh |
| `source_permission` | a standing permission in `publishable_sources` covers its YouTube channel, playlist, speaker (or reciter) or label |

- **Permissions.** `POST /api/tv/broadcast/publishable/sources` records one with the link where
  the source allows sharing (`http` or `https`), an optional credit and a non-commercial flag.
  Withdrawing keeps the row with its reason. Nothing of it goes to Maktaba Cloud.
- **The schedule.** While `broadcast.publishable_only` is on (the default), the scheduler builds a
  published channel from publishable items only (`publishable_only` in the pipeline's
  `channels/pass_.py`). Turning it off takes a typed confirmation, and the Broadcast page marks
  each published channel "Not limited to publishable items".
- **A lost mark.** Withdrawing, marking, the switch, publishing and unpublishing mark the
  published channels' schedules stale, so the next top-up rewrites them. Until then the
  streaming player plays the slate ("Not available online") in place of an item that lost its
  mark.
- **Attribution.** A CC BY item carries "title by author, CC BY" in the guide, and a permission's
  credit when it has one.
- **The page.** Per channel, the publishable share of the next 7 days and the sources that would
  add most, each with **Record permission** (pre-filled, its About page opened).

## Countries

A channel may name an **allow** list (only these countries) or a **deny** list (everywhere
except these). Maktaba's own deny list (`broadcast_geo_deny` in the cloud, set by service
admins with a reason and audited) applies to every channel. The home sends its channel's rule
with each ingest-token request; the cloud merges in its list and signs both into the token, so a
home can't leave it out. The edge checks `CF-IPCountry` at every playlist and answers a viewer
outside with `451 geo_blocked`.

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

## Takedowns and notices

Maktaba Cloud takes copyright notices and viewers' reports about published channels (story
50.24). A service admin decides each one in the console, and a takedown reaches the home as a
signed instruction:

- **The instructions.** Every minute the API asks the agent
  (`GET /admin/cloud-link/broadcast/takedowns`). The answer is a control post signed with the
  broadcast keys (the revocation list's), `typ: "takedowns"`, naming this home. A forged or expired
  batch, or another home's, is refused and logged (`broadcast_takedowns_refused`, SEV2). Without
  the keys the home applies none.
- **The edge id.** Every ingest-token request names the channel's cloud id (the publish
  answer); the cloud refuses one without it (`400 bad_request`). The first token binds the
  channel's edge id to that cloud id, so a takedown's programme slate always names the edge id the
  home uploads to. The speed test before the first publish asks for a speed-test token instead
  (`purpose: "speedtest"`), which the edge takes on its speed test only.
- **Applying one.** A takedown names the item (its id, or the programme on the channel at the
  notice's time, read from `channel_programs`) or a source (a YouTube channel or playlist, a
  speaker, a label). The home writes a row in `broadcast_takedowns`; slot 0228's
  `video_taken_down()` makes `video_publishable_reason()` answer `NULL` for it, so the scheduler
  leaves it out and a slot already scheduled plays the slate. The home answers
  (`POST .../takedowns/{id}/ack`) `applied` with the end of the slot on air now, `not_found` (the
  item is gone; the notice still counts) or `failed`. The answer is shown to staff and changes
  nothing at the edge: the programme slate keeps the end staff gave it (else 4 hours), since the
  home is the publisher the notice is about. A restore clears the row the same way.
- **The cloud learns** only the item's opaque id and its title, the guide's own fields. Nothing of
  the library leaves the home.
- **The page.** `GET /api/tv/broadcast/moderation` returns the cloud's records for this home (the
  statements of reasons, the reports about its content without the reporter's email, the active
  strikes against the limit, whether broadcasting ended) and the takedowns applied here. The
  Broadcast page shows them (`BroadcastNotices.tsx`), with the counter-notice form:
  `POST /api/tv/broadcast/moderation/counter-notices` needs every element the law asks for; the
  cloud's refusals read as `broadcast.counterNoticeIncomplete`, `broadcast.counterNoticeFiled` and
  `broadcast.counterNoticeUnknown`.
- **Strikes.** Each accepted notice is a strike on the publisher (one per item per 30 days). Three
  active strikes in 12 months end broadcasting for the account: the cloud adds a publisher
  `terminate` entry to the revocation list, which stops every channel (see **Taken off air**).

The cloud's side (the public forms, the queue, the strikes, the records) is in
`cloud/internal/moderation/` and `cloud/internal/handlers/moderation/`; the runbooks are
`docs/runbooks/cloud/broadcast-takedown.md` and `broadcast-urgent-report.md`.

## Tests

- `api/internal/broadcast`: the checks (T01, T04, T05), the role test (T09), and the database
  tests for publishing, tokens with the cloud away (T08) and the speed test.
- `streaming/internal/broadcast`: the ladder's arguments (T02), the uploader (T03), and the
  broadcaster against a fake edge (always on, on demand, a revoked channel, an expired token).
- `api/internal/broadcast/publishable_pg_test.go`: permissions and marks (T07), a reciter as a
  source (T05), the share and its suggestions, the switch.
- `pipeline/tests/channels/test_publishable_only.py`: a published channel builds from
  publishable items only (T03); the licence at intake and refresh (T01, T02).
- `streaming/internal/channel/publishable_pg_test.go`: an item that lost its mark plays the
  slate (T04).
- `web/packages/broadcast-edge/test/geo.test.ts` and the cloud's ingest tests: the country rule
  (T06).
- `api/internal/broadcast/revocations_test.go` and `revocations_pg_test.go`: the cloud's signed
  vector, the strongest stop, anti-rollback, a stopped and lifted channel, a stale copy (T04).
- `api/internal/broadcast/takedown_test.go` and `takedown_pg_test.go`: a batch verifies only
  with the broadcast key, for this home; a takedown by time, by id and by source, its answer with
  the slot's end, a restore, a forged batch.
- `cloud/cmd/maktaba-cloud/moderation_integration_test.go`: notice, takedown, counter-notice and
  restore; three strikes end broadcasting; the roles (a customer `401`, the wrong staff role
  `403`).
- `web/src/pages/Admin/LiveTV/BroadcastPanel.test.tsx` and `BroadcastNotices.test.tsx`: the page
  and its notices, in English and Arabic.
