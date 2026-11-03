# Story 50.22 — The home's broadcaster: publish, ladder at home, uplink and encoder checks

> Epic 50 · Phase B · Depends on: 50.20 (the `broadcast` licence flag), 50.21, Epic 34.1 (public
> flag), Epic 34.3 (public mux, slates), Epic 51 (built), Epic 52.17 (rows for new component ids)
> · Needed by: 50.23, 50.25, 50.29

## Why

The edge (50.21) takes segments. Something on the home must:

- choose what to publish;
- keep the channel encoding 24/7 while it is published;
- make the lower renditions;
- upload every segment once;
- tell the publisher, before publishing, whether their uplink and encoders can carry it.

Everything that decides **what airs** stays here on the home, under the publisher's control
(README §14.6). The cloud only carries the result.

## Description

### 1. The Broadcast page (Admin → Live TV → Broadcast)

- **Role.** Home admins only. The API enforces it (`403` for anyone else, with a role test). The
  page is shown only on a home whose plan has a `BroadcastTier` (50.20).
- **Per channel it shows:**
  - its publishable share (50.23): how many of the channel's scheduled hours this week are
    publishable;
  - the access (`invite` or `open`);
  - the ladder;
  - the mode (always-on, or on demand for invite-only);
  - the countries;
  - the state: `draft`, `live`, `offline`, `off_air`, `revoked`;
  - the link, QR code and short code once it is live.
- **Publishing** a channel runs these checks in order, and names the first one that fails:
  1. **The plan.** The channel count, access and ladder are within the tier.
  2. **The Broadcaster agreement** is accepted (50.20 §0).
  3. **Publishable content.** At least 6 hours of the next 24 are publishable (50.23).
  4. **Uplink.** The published channels' total, including this one, is ≤ 50 % of the measured
     uplink (§3).
  5. **Encoder slots** (§4).
  6. **A rights confirmation.** One tick: "I have the rights to broadcast what this channel airs".
     It is stored on the home with the agreement version, the admin's name and the time. Only
     the date and version go to the cloud.
- Publishing calls `POST /v1/broadcast/channels` on the cloud API with the slug, access, rungs
  and mode, gets an ingest token (50.21 §2), starts the broadcaster (§2), and uploads the offline
  slate (§5).

### 2. The broadcaster process

- It runs in the streaming service (`streaming/internal/broadcast/`, new), one **broadcaster**
  per published channel.
- **Source.** The channel's public mux (Epic 34.3). While the channel is published the scheduler
  fills it with publishable items only (50.23), so it is the same as the channel mux. Epic 34's
  slate covers an item that loses its mark after it was scheduled.
- **Ladder.** One ffmpeg reads the mux:
  - the top rung is `-c copy` (the channel's own encode, watermark and strip burned in);
  - each lower rung is a hardware encode (VideoToolbox, NVENC, Quick Sync or VAAPI, chosen as the
    channel's own encoder is), with 4 s segments on the channel's keyframe grid
    (`keyint_seconds`);
  - software x264 is used only when the admin allows it (`broadcast.allow_software_rungs`,
    default off, because it takes about one core per rung).
- **Uploader.**
  - It watches each rung's segment directory and PUTs each finished segment to
    `ingest.maktaba.tv` with its hash and duration.
  - It retries for up to 2 target durations with jitter. After that it skips the segment and
    flags the next one as a discontinuity.
  - It keeps at most 8 segments per rung on disk.
  - It posts state every 10 s (`POST …/state`): rungs, bandwidths, resolutions, encoder health.
- **Always-on vs on demand.**
  - `open` channels are **always-on**: the channel is placed and **never released** by the warm
    grace while it is published.
  - Invite-only channels in `on_demand` mode start when the edge sends `wake` over the control
    WebSocket (`/v1/ingest/ws`), and stop 5 minutes after the edge reports no viewers. That
    matches Epic 34 R3.6's `top_n` wake. The target is the first segment at the edge ≤ 15 s after
    `wake`.
- **The control WebSocket** (outbound, with reconnect and jitter) carries:
  - `wake`, `sleep`;
  - `limits` (a rung removed by plan mode);
  - `off_air` and `revoked` (50.25);
  - `clock_skew`.
- **Token refresh.** The broadcaster refreshes the ingest token every hour over the cloud link. If
  the cloud is unreachable it keeps using the current token until it expires (24 h), then stops
  with `broadcast.tokenExpired`.

### 3. Uplink check

- `PUT /v1/ingest/speedtest` for 10 s, at publish time and once a day at a quiet hour; the result
  goes in `broadcast_uplink (measured_at, kbps)`.
- A channel's need is the rungs' total × 1.1. **The published total may use at most 50 % of the
  last measurement** (`broadcast.uplink_share`, default 0.5, range 0.2–0.8).
- Too much → publishing is refused with `broadcast.uplinkLow`, which suggests the largest ladder
  that fits (for example "720p + 480p fits; 1080p doesn't").
- If a daily test later falls below the need (the publisher's ISP changed), the home drops the
  top rung of the newest channel first, tells the publisher, and never stops a channel entirely
  for this.

### 4. Encoder slots and `max_active_channels`

- A published channel holds **one slot of `tv_output.max_active_channels` permanently** on its
  host (Epic 32 placement, pinned while published). Each lower rung holds
  `broadcast.rung_slot_weight` of a slot (default 0.5, to be measured on the M3 Ultra's
  VideoToolbox and the PC's NVENC in this story's spike and set per encoder family).
- Hardware session caps (`nvenc_max_sessions`, `videotoolbox_max_sessions`) count one session per
  encoded rung. The `copy` rung doesn't count.
- Publishing is refused (`broadcast.noCapacity`) if it would leave fewer than Epic 34's
  `tv_public.reserve_slots` (default 2) free for LAN and Plex viewers.
- The Streaming hosts page (Epic 32) shows "published channels: N (slots S)" per host.
- **Watch-only vs broadcast, as the admin sees it:** a watch-only channel encodes only while
  someone watches. A published channel encodes all the time it is published, so it uses power
  and a slot even at night.
- `docs/hardware.md` doesn't exist on main (2026-11-03). If it lands, this page links its
  encoder table; until then the numbers above are the guidance.

### 5. The offline slate

At publish time the home renders a 4 s, single-rung, looping slate through the normal block path,
with the channel's look. It shows the channel name and "Offline: we'll be back soon" (EN/AR).
The home uploads it once to `…/<channel>/slate/`. The edge plays it when the home goes quiet
(50.21 §3).

### 6. The guide

The home publishes the channel's guide with Epic 34.2's publisher, to the edge's `GUIDE` bucket
through `POST /v1/ingest/<channel>/guide` (the same blobs, `schema: 1`). Publishable programmes
only; there is nothing else on the schedule. CC BY items carry their attribution fields (50.23).

### 7. An Epic 51 component

The broadcaster is a new part of the home, so it is a component in
[`shared/components/catalog.toml`](../../../shared/components/catalog.toml) (README §4.6). The
`catalog` CI job fails without it, because its loops would belong to none.

```toml
[[component]]
id        = "livetv.broadcast"
kind      = "loop"
group     = "livetv"
title_key = "components.livetv.broadcast.title"
desc_key  = "components.livetv.broadcast.desc"
# one broadcaster per published channel runs under these; its ffmpeg children are streaming's
loops     = ["streaming:broadcast_uploader", "streaming:broadcast_control"]
requires  = ["livetv", "cloud.link"]
settings  = ["rules.broadcast"]
licence   = "broadcast"
placement = "home"
platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
bundles   = []
since     = "<the release that ships this story>"
```

- **Off until an admin turns it on.** It is in no bundle; 52.17's seed gives it `want =
  installed` on homes already seeded. The Broadcast page's first "Publish" asks to turn it on
  (`PUT /api/components/livetv.broadcast`, `want = enabled`, through the same preview as the
  Components page) when it is off. This replaces a home `broadcast.enabled` rule (the cloud's
  `broadcast.enabled` flag, README §14.11, is a different switch and stays).
- **Licence.** `licence = "broadcast"`, the flag 50.20 adds to
  `api/internal/flags/declarations.go` (Pro for guest sharing, Broadcaster). Without it, turning
  it on answers `403 licence_missing`; a lapsed plan makes it *Waiting* (`licence_missing`) and
  keeps the wish (story 51.19).
- **Requirements.** With `livetv` or `cloud.link` off it is *Waiting* (`dependency_off`) and
  every published channel goes to its offline slate at the edge.
- **Routes.** `GET/PUT /api/tv/broadcast/...` are added to `ComponentRoutes`
  (`api/internal/router/components_routes.go`): while `livetv.broadcast` is off they answer
  `409 component_off` with `"component": "livetv.broadcast"`.
- **Stop and hold.** `POST /api/components/livetv.broadcast/stop` (a hold, until start) stops
  every broadcaster within the loops' stop grace (default 5 s) and sends `offline_slate` to the
  edge; no published channel is deleted. Turning it off (`want = installed`) does the same.
  Desired state is the home admin's alone: the cloud's kill switch (50.25) never writes it.
- **Settings.** `broadcast.uplink_share`, `broadcast.rung_slot_weight` and
  `broadcast.allow_software_rungs` are the `rules.broadcast` section, with a `SCHEMA_VERSION`
  (`pipeline/src/maktaba_pipeline/settings.py`, story 51.13); they survive turning it off.
- **Words.** `components.livetv.broadcast.title` = Broadcasting / البث للعموم,
  `components.livetv.broadcast.desc` = Publishes chosen Live TV channels to viewers outside the
  home, through Maktaba Cloud. / ينشر قنوات بث مباشر مختارة لمشاهدين خارج المنزل عبر سحابة مكتبة.

## Acceptance criteria

- **Given** a Pro home with a channel that is 80 % publishable for the next 24 h,
  **when** the admin publishes it as `invite`,
  **then** within 30 s it is `live`, its link plays, and the home uploads each segment once per
  rung.
- **Given** a measured uplink of 20 Mbps and one published channel at 15.6 Mbps,
  **when** the admin publishes a second one at 15.6 Mbps,
  **then** publishing is refused with `broadcast.uplinkLow` suggesting `720p + 480p`.
- **Given** a host with `max_active_channels = 8`, 5 channels in use and `reserve_slots = 2`,
  **when** the admin publishes a channel with 3 rungs (1 + 0.5 + 0.5 = 2 slots),
  **then** it is refused with `broadcast.noCapacity`.
- **Given** a published `open` channel with no viewers for an hour,
  **when** the warm grace passes,
  **then** the channel stays placed and keeps uploading.
- **Given** an invite-only on-demand channel,
  **when** a guest presses play,
  **then** the edge wakes the home and the first segment is live within 15 s, while the tuning
  card shows.
- **Given** the cloud API is unreachable for 3 hours,
  **when** the broadcaster runs,
  **then** it keeps uploading with its current token.
- **Given** a user who isn't an admin,
  **when** they call `PUT /api/tv/broadcast/channels/{id}`,
  **then** the API answers `403`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | publish checks | each failing check in turn | the first failing check named; nothing started |
| T02 | unit | ladder builder | copy only / copy + 480p / 3 rungs; VideoToolbox, NVENC, software off | ffmpeg args; software refused unless allowed |
| T03 | unit | uploader | PUT failures, a retry window, a skip | retries with jitter; the skip flags a discontinuity; ≤ 8 segments on disk |
| T04 | unit | uplink rule | measurements and channels | refuse and suggest; a later drop removes the newest channel's top rung |
| T05 | unit | slot accounting | channels, rungs, reserve | refuse at the reserve; the `copy` rung doesn't count a session |
| T06 | integration | streaming service + fake edge | publish, run 5 min, unpublish | segments × rungs PUTs; placement pinned while published, released after |
| T07 | integration | on-demand | wake, no viewers for 5 min | starts ≤ 15 s; stops after 5 min |
| T08 | integration | cloud unreachable | 3 h, then 25 h | keeps going; stops with `broadcast.tokenExpired` at 24 h |
| T09 | role test | home API | non-admin calls | `403` |
| T10 | Arabic | new keys | strings check | Arabic in `web/src/i18n/ar.json`, the Apple catalog, Android `values-ar` |
| T11 | rehearsal (CI, 50.29 `broadcast-home`) | home stack + edge in Miniflare | publish, kill the encoder, restart it | the edge plays the slate, then live again; no player error |

## Edge cases

- **The channel's host fails** (Epic 32 failover). The channel moves to another host. The
  broadcaster follows, and the next segment carries a discontinuity. If no host has a slot, the
  channel goes `offline` (slate) and the publisher is told.
- **An Epic 33 hot swap** changes what the station plays, not the mux, so the broadcaster doesn't
  notice.
- **The channel's own encode changes** (an admin edits `video_kbps`). If the top rung would pass
  the plan's `MaxTopKbps`, the change is refused for published channels, or the top rung becomes
  an encode at the cap. The admin chooses; the default is to refuse.
- **The home sleeps.** On macOS the server's power assertion already keeps it awake while
  channels run. Published channels count as running.
- **The publisher unpublishes.**
  - The broadcaster stops.
  - The cloud marks the channel `unpublished`.
  - The edge answers `410 broadcast.ended` to new plays.
  - Playing viewers see the ended card at the next playlist.
  - Links stop working, and a re-publish gets a fresh slug unless the old one is re-used within
    30 days.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `broadcast.title` | Broadcast | البث |
| `broadcast.publish` | Publish this channel | انشر هذه القناة |
| `broadcast.confirmRights` | I have the rights to broadcast everything this channel airs. | أملك حقوق بث كل ما تعرضه هذه القناة. |
| `broadcast.uplinkLow` | Your internet upload ({kbps}) can't carry this. {suggestion} fits. | سرعة الرفع لديك ({kbps}) لا تكفي لهذا. يناسبها: {suggestion}. |
| `broadcast.noCapacity` | This machine has no room for another always-on channel while keeping {reserve} free for your home. | لا يتسع هذا الجهاز لقناة أخرى تعمل دائمًا مع إبقاء {reserve} متاحة لمنزلك. |
| `broadcast.alwaysOnNote` | A published channel keeps encoding all the time, even when nobody is watching. | تواصل القناة المنشورة الترميز طوال الوقت، حتى عندما لا يشاهدها أحد. |
| `broadcast.tokenExpired` | Broadcasting paused: this home couldn't reach Maktaba Cloud for a day. It resumes when the connection is back. | توقف البث مؤقتًا: تعذّر على هذا الجهاز الوصول إلى سحابة مكتبة ليوم كامل. سيُستأنف عند عودة الاتصال. |
| `broadcast.clockSkew` | This home's clock is off by more than a minute. Fix the time to keep broadcasting. | ساعة هذا الجهاز متأخرة أو متقدمة بأكثر من دقيقة. صحّح الوقت لمواصلة البث. |
| `broadcast.slate.offline` | Offline: we'll be back soon | غير متصل: سنعود قريبًا |
| `broadcast.state.live` | Live | مباشر |
| `broadcast.state.offline` | Home offline | المنزل غير متصل |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `streaming/internal/broadcast/` (new): `broadcaster.go`, `ladder.go`, `uploader.go`, `control.go`, `slate.go`, tests | The broadcaster, ladder, uploader, control socket, slate. |
| `streaming/internal/placement/` | Pin published channels; rung slot weights. |
| `streaming/internal/channel/settings.go` | `broadcast.*` settings (`uplink_share`, `rung_slot_weight`, `allow_software_rungs`). |
| `api/internal/broadcast/` (new) | Home API: `GET/PUT /api/tv/broadcast/...`, publish checks, cloud calls. |
| `api/migrations/` (next free slot) | `broadcast_channels`, `broadcast_uplink`, `broadcast_confirmations`. |
| `web/src/pages/LiveTv/Broadcast*.tsx`, `web/src/i18n/en.json`, `ar.json` | The Broadcast page. |
| `cloud/internal/handlers/broadcast/channels.go` (new) | `POST/PATCH/DELETE /v1/broadcast/channels`. |
| `docs/broadcasting.md` (new), `docs/tv-cluster-and-remote.md` | How to publish; pinned channels on the Streaming hosts page. |

### Tests

- T01–T10 in the streaming, API and web unit jobs (path-filtered).
- **CI only:** rehearsal `broadcast-home` (50.29), which uses a test-pattern channel and a fake
  edge. No live Cloudflare.

### Rollout

1. Behind the `livetv.broadcast` component (§7: in no bundle, so off until an admin turns it on).
2. Staging home with the canary publisher.
3. The live home with one invite-only channel.
4. The broadcasting gate.

### Rollback

- Unpublish all: one button, or turn the component off (`maktaba-server components disable
  livetv.broadcast`). The broadcaster stops; nothing else on the home changes.
- The migration is additive (new tables only).

### Dependencies

50.20 (the `broadcast` flag), 50.21 (edge, ingest tokens). Epic 51 (built: the catalog, the
streaming loop runner 51.05, `component_off` 51.09, licence gating 51.19) and Epic 52.17 (rows
for new ids). Epic 34.1 (the public flag the channel reuses). Epic 34.3 (the
public mux and the slate path).
