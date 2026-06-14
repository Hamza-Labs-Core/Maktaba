# Epic 27 — Live Channels & TV Programming

> **Status:** spec. **Source:** `specs/epics/27-live-channels/`.
> **Anchors:** [`architecture.md` §4 (Streaming Service)](../../architecture.md#4-streaming-service),
> [§4.2 (Session model)](../../architecture.md#42-session-model),
> [§4.3 (HLS manifest)](../../architecture.md#43-hls-manifest),
> [§4.4 (Transcode pipeline)](../../architecture.md#44-transcode-pipeline),
> [§6 (Clients)](../../architecture.md#6-clients-web--apps),
> [§9.4 (API — Streaming)](../../architecture.md#94-streaming),
> [§9.9 (Inter-service gRPC)](../../architecture.md#99-inter-service-grpc).

## Goal

Maktaba is, today, a **video-on-demand** server: the user picks a file
and it plays. What it cannot do is the thing a cable box or Plex Live TV
does — give you a set of **channels** you can flip through, each running a
continuous 24/7 program you didn't have to assemble, all synchronised so
that tuning in at 8:32 pm drops you 32 minutes into the 8 pm movie, the
same place everyone else in the house is.

Epic 27 builds that — a **Plex Live TV alternative that is fully
self-sourced**. There is no cable feed, no IPTV provider, no tuner card.
Every channel is **virtual**: a programming rule plus the user's own
library equals a linear stream. This is the
[ErsatzTV](https://ersatztv.org/) / [DizqueTV/Tunarr](https://tunarr.com/)
idea, but built into Maktaba natively — server *and* client — instead of
bolted on as an external tuner.

The shape of the feature:

1. **Virtual channels** — the user defines channels (name, number, logo,
   category) and a **programming mode**: shuffle a filter, marathon a
   series, run a time-slot grid, or let a **smart-mix** mode program the
   day the way a real network would, using Epic 26's content
   classification to balance genres across dayparts.
2. **A linear schedule** — the server turns each channel's rule + the
   library into a continuous, wall-clock-anchored timeline of program
   blocks (generated 48 h ahead), with **filler/bumpers** padding the
   gaps so the clock never drifts.
3. **A live stream per channel** — a continuous HLS (and MPEG-TS for
   HDHomeRun) stream produced by the **existing FFmpeg transcode
   infrastructure**, lazily activated only for channels someone is
   actually watching, joined at the correct wall-clock offset.
4. **A guide** — an EPG: an in-app channel × time grid, plus standard
   **XMLTV** and **M3U** exports and **HDHomeRun tuner emulation** so
   Plex DVR / Jellyfin / Emby can discover Maktaba's channels with zero
   config.
5. **Receiver UIs** — an EPG grid, a dedicated live player with
   channel-surfing and a banner overlay, an admin channel builder, and a
   "What's On Now" home-screen rail — on web **and** the native TV,
   mobile, and desktop apps.

### The non-goals

This is **self-sourced linear TV**, not a DVR and not a re-broadcaster.
Maktaba ingests no external live signal; it never records off a tuner;
it never restreams copyrighted broadcast feeds. A "channel" is purely a
deterministic scheduling view over content the user already owns. The
cloud relay (Epic 25) is **not** extended — channels are a server-local
feature; the relay only tunnels the resulting HLS exactly as it tunnels
on-demand playback today.

## How Epic 27 fits the existing architecture

Epic 27 spans three services, drawn along Maktaba's existing language
split ([§1.3](../../architecture.md#13-why-this-language-split)):

```
                 ┌──────────────────────── Pipeline (Python) ───────────────────────┐
                 │  channels/ scheduler:  rule + library → channel_programs (48h)    │
                 │    shuffle | marathon | time-slot | smart-mix (uses Epic 26)       │
                 │    debounced library pass + 6-hourly horizon top-up (cron)         │
                 └───────────────┬───────────────────────────────────────────────────┘
                                 │ writes channel_programs (absolute start_at/end_at)
                                 ▼
   ┌──── API (Go) ────┐   ┌──────────────────── Streaming (Go) ────────────────────┐
   │ channels CRUD    │   │  channel/ engine: read schedule → concat-demuxer FFmpeg │
   │ EPG / guide read │◄─►│    → sliding HLS window  (reuses internal/ffmpeg)        │
   │ XMLTV + M3U      │   │  lazy activation: spawn on first tune, reap on idle      │
   │ tune → OpenCh.   │   │  HDHomeRun emulation: SSDP + /discover + /auto/v{ch}     │
   │ filler mgmt      │   │    (MPEG-TS output) → Plex/Jellyfin/Emby                 │
   └────────┬─────────┘   └───────────────────────────┬─────────────────────────────┘
            │  REST                                    │  HLS / MPEG-TS
            ▼                                          ▼
   ┌─────────────────────── Clients (web + native apps) ──────────────────────────┐
   │  EPG grid · live player (surf + banner + mini-guide) · admin builder ·         │
   │  "What's On Now" home rail   — web, tvOS, Android TV, mobile, desktop          │
   └───────────────────────────────────────────────────────────────────────────────┘
```

- **Scheduling is Python.** Generating a 24/7 timeline is a batch
  planning problem, and **smart-mix** needs Epic 26's
  `video_classification` / `video_topics` to balance genres across the
  day. It runs as a debounced library pass plus a periodic horizon
  top-up, the same way Epic 26's series/auto-collection passes run
  ([26.4](../26-content-intelligence/story-26-04-auto-collection-builder.md)).
- **Streaming is Go.** A live channel is just a long-lived **virtual
  streaming session** that reads `channel_programs`, feeds the program
  files to FFmpeg through the **concat demuxer**, and emits a sliding HLS
  window — reusing `streaming/internal/ffmpeg` (the transcode ladder,
  hwaccel detection) and `streaming/internal/session` (the idle reaper).
  Channels are **lazily activated**: no viewers → no FFmpeg.
- **The API service** exposes the REST surface (channel CRUD, guide,
  exports, tune) and proxies live tunes to streaming over the existing
  `OpenSession` gRPC seam ([§9.9](../../architecture.md#99-inter-service-grpc)),
  extended with channel mode.
- **HDHomeRun emulation lives in streaming** — it is fundamentally about
  serving video (an MPEG-TS mux per tuner connection), and it must be
  reachable on the LAN for SSDP discovery, which the streaming binary
  already is.

### Wall-clock anchoring (the one idea everything depends on)

A linear channel is **not** "play this list of files." It is: *at
absolute time `T`, program `P` is playing at offset `T − P.start_at`.*
Every `channel_programs` block carries absolute `start_at`/`end_at`
timestamps. Consequences that fall out for free:

- **Clock-sync.** Two viewers who tune at the same wall-clock second
  compute the same seek offset and see the same frame — the channel is a
  single shared timeline, not a per-viewer playlist.
- **Lazy activation is correct, not approximate.** A channel can be cold
  (no FFmpeg) and still have a fully-defined "what's on now"; the engine
  reconstructs the exact join point from the schedule when the first
  viewer tunes.
- **The guide is just a query.** EPG, XMLTV, M3U, and "what's on now" are
  all read paths over `channel_programs` filtered by time — no separate
  guide store.

## Stories

### Server — Channel Engine (pipeline + streaming + api)

| #     | Story                                                       | Service(s) | Summary |
|-------|------------------------------------------------------------|------------|---------|
| 27.1  | [Channel definition (CRUD)](story-27-01-channel-definition.md) | api + web | Virtual channels: name, number, logo, category, programming mode + config. Postgres + REST + admin scaffolding. |
| 27.2  | [Program scheduler](story-27-02-program-scheduler.md)      | pipeline   | Rule + library → continuous 48 h linear schedule. Shuffle / marathon / time-slot / smart-mix; padding; wall-clock anchoring; regenerate. |
| 27.3  | [Live stream engine](story-27-03-live-stream-engine.md)    | streaming  | Continuous HLS per channel via concat-demuxer FFmpeg; sliding playlist; ABR ladder; lazy activation; instant switch via warm window. |
| 27.4  | [EPG generation & exports](story-27-04-epg-generation.md)  | api        | Internal guide API + XMLTV + M3U exports over `channel_programs`. Program metadata, posters, series/episode info. |
| 27.5  | [HDHomeRun emulation](story-27-05-hdhomerun-emulation.md)  | streaming  | SSDP discovery + `/discover.json` + `/lineup.json` + `/lineup_status.json` + `/auto/v{ch}` (MPEG-TS) → Plex DVR / Jellyfin / Emby. |

### Client — Receiver UI (web + native apps)

| #     | Story                                                       | Service(s) | Summary |
|-------|------------------------------------------------------------|------------|---------|
| 27.6  | [EPG grid UI](story-27-06-epg-grid-ui.md)                  | web + apps | Channel × time guide, now-line, click-to-tune, program details, category filter, "what's on now"; responsive + D-pad. |
| 27.7  | [Live channel player](story-27-07-live-channel-player.md)  | web + apps | Channel surfing (up/down + number keys), tune banner, mini-guide overlay, watch-from-beginning, PiP. |
| 27.8  | [Channel management admin UI](story-27-08-channel-admin-ui.md) | web   | Channel CRUD form, visual programming rule builder, 48 h schedule preview, reorder, enable/disable, filler management. |

### Integration

| #     | Story                                                       | Service(s) | Summary |
|-------|------------------------------------------------------------|------------|---------|
| 27.9  | ["What's On Now" home widget](story-27-09-home-widget.md)  | api + web + apps | Home-screen rail: each channel's current program + progress + next-up + Tune In. All platforms. |
| 27.10 | [Filler & bumper system](story-27-10-filler-bumper-system.md) | pipeline + api + web | Designate short clips as filler/bumper; global + per-channel pools; auto-insert to fill gaps; auto "up next" bumpers. |

## Key technical decisions

- **Wall-clock-anchored schedule blocks; no per-viewer playlists.**
  `channel_programs` stores absolute `start_at`/`end_at`. The live engine
  and every guide surface derive their state from `now` against those
  timestamps. **Rationale:** clock-sync, cheap guide reads, and correct
  cold-channel "now playing" all fall out of one invariant
  ([27.2](story-27-02-program-scheduler.md)). The alternative — a relative
  playlist with a per-session cursor — cannot answer "what's on channel 5
  right now" without spinning the channel up, and desyncs viewers.

- **Scheduling in Python, streaming in Go.** The scheduler is a pipeline
  module because **smart-mix** consumes Epic 26 classification and
  scheduling is batch planning; the live stream is a Go streaming
  concern because it is FFmpeg + HLS, which already lives there.
  **Rationale:** keeps each side in the language and service it belongs
  to (mirrors Epic 26's `classify`-in-Python / serve-in-Go split) and
  lets `smart-mix` reuse the embedder/topic tables with zero new model.

- **A channel is a long-lived virtual streaming session.** The live
  engine reuses `streaming/internal/session` (state + idle reaper) and
  `streaming/internal/ffmpeg` (ladder, hwaccel). A channel session is
  spawned on **first tune** and reaped after a grace period with **zero
  viewers**. **Rationale:** transcoding 30 idle channels 24/7 would melt
  the box; lazy activation means cost scales with *watched* channels, not
  *defined* channels. Reuses the exact lifecycle machinery on-demand
  playback already has ([§4.2](../../architecture.md#42-session-model)).

- **Concat demuxer, not a re-implemented stitcher.** A channel's upcoming
  program files are fed to one FFmpeg process via the **concat demuxer**,
  with the first program seeked to `now − start_at`, emitting a single
  sliding HLS window (`-hls_flags delete_segments+append_list`,
  bounded `-hls_list_size`). **Rationale:** FFmpeg already does
  gapless concatenation and segment rotation; we add scheduling, not
  muxing. Hard cuts between programs are clean; crossfade (an `xfade`
  filter graph) is a per-channel opt-in with documented cost, not the
  default ([27.3](story-27-03-live-stream-engine.md)).

- **Padding is mandatory; the clock must never drift.** A program rarely
  fills its slot exactly. The scheduler always closes a slot to its
  boundary with **filler** (station IDs, bumpers, short clips) or, absent
  filler, an "up next" card or a tail-replay — never dead air, never a
  gap. **Rationale:** wall-clock anchoring only works if the timeline is
  contiguous; a 4-minute gap would desync every downstream block
  ([27.2](story-27-02-program-scheduler.md) + [27.10](story-27-10-filler-bumper-system.md)).

- **The guide is a read path, not a store.** EPG, XMLTV, M3U, and "what's
  on now" are all queries over `channel_programs` by time range; only the
  export *responses* are cached (short TTL). **Rationale:** the schedule
  is the single source of truth; a parallel guide store would be a
  consistency hazard for zero benefit ([27.4](story-27-04-epg-generation.md)).

- **HDHomeRun emulation speaks MPEG-TS, not HLS.** Plex/Jellyfin/Emby
  tuners pull a continuous MPEG-TS over HTTP from `/auto/v{ch}`, so that
  endpoint runs an FFmpeg `-f mpegts` mux joined at the wall-clock
  offset, distinct from the HLS path. SSDP advertises one virtual
  device with a configurable tuner count (= max concurrent external
  pulls). **Rationale:** zero-config discovery in the dominant media
  servers is the whole point; meeting their wire format is non-negotiable
  ([27.5](story-27-05-hdhomerun-emulation.md)).

- **Channels are library-scoped and ACL-gated like everything else.** A
  channel belongs to a library (or is multi-library via an explicit
  source filter) and inherits the existing
  [`libraryacl`](../../../api/internal/handlers/libraryacl) rules: only
  editors create/edit channels; viewers tune. **Rationale:** reuse the
  established permission model; no new authz surface.

- **Smart-mix degrades to shuffle.** If Epic 26 classification is absent
  or disabled for a library, smart-mix falls back to a weighted shuffle
  rather than failing. **Rationale:** channels must work on a library
  that never ran enrichment; Epic 26 is an enhancer, not a hard
  dependency.

## API surface (new, on the local API service unless noted)

```
# Channels — CRUD (27.1)
GET    /api/channels                          # ?library_id= ?category= ?enabled=
POST   /api/channels
GET    /api/channels/{id}
PATCH  /api/channels/{id}
DELETE /api/channels/{id}
POST   /api/channels/reorder                  # [{id, number}] bulk renumber
POST   /api/channels/{id}/logo                # logo upload (re-encoded via thumbnail path)

# Schedule / programming (27.2)
GET    /api/channels/{id}/schedule?start=&end=     # generated program blocks
POST   /api/channels/{id}/schedule/regenerate      # force regen from now
GET    /api/channels/{id}/schedule/preview?hours=48 # dry-run (admin builder)

# Guide / EPG (27.4)
GET    /api/channels/guide?start=&end=&category=    # grid: all channels × time
GET    /api/channels/{id}/guide?start=&end=         # one channel's guide
GET    /api/channels/now                            # what's-on-now (widget, 27.9)
GET    /api/channels/xmltv                          # XMLTV XML export
GET    /api/channels/playlist.m3u                    # M3U playlist (VLC/IPTV)

# Live playback (27.3) — api proxies tune to streaming gRPC OpenChannel
POST   /api/channels/{id}/tune                      # → {session, manifest_url}
GET    /stream/channel/{id}/manifest.m3u8           # served by streaming
GET    /stream/channel/{id}/{rendition}/{seg}.ts    # served by streaming

# Filler & bumpers (27.10)
GET    /api/filler/pools                             # ?library_id=
POST   /api/filler/pools
PATCH  /api/filler/pools/{id}
DELETE /api/filler/pools/{id}
POST   /api/filler/pools/{id}/items                 # designate video(s) as filler/bumper
DELETE /api/filler/items/{id}
PATCH  /api/channels/{id}/filler                    # assign pools + padding policy

# HDHomeRun emulation (27.5) — served by the streaming binary, not /api
GET    /discover.json
GET    /lineup.json
GET    /lineup_status.json
GET    /lineup.post                                  # no-op scan ack (Plex compat)
GET    /auto/v{channel}                              # continuous MPEG-TS for one tuner
        + SSDP/UPnP M-SEARCH responder on udp/1900
```

## DB schema (new tables / alters — local `shared/db/migrations/`)

The local migration sequence is at slot **0072**; Epic 26 claims
**0073–0080** ([Epic 26 README](../26-content-intelligence/README.md));
Epic 27 claims **0081–0085**. Every slot ships the dual `*.sql` +
`*.sqlite.sql` pair the runner expects. (See
[`shared/db/migrations/MANIFEST.md`](../../../shared/db/migrations/MANIFEST.md).)

| Slot | Story | File | Tables / changes |
|------|-------|------|------------------|
| 0081 | 27.1 | `0081_channels.sql` | `channels` (id, library_id, number, name, slug, logo_path, category, mode, mode_config JSONB, source_filter JSONB, enabled, sort_order, transition, created/updated; unique `(scope, number)`) |
| 0082 | 27.2 | `0082_channel_programs.sql` | `channel_programs` (channel_id, seq, kind, video_id?, filler_item_id?, start_at, end_at, source_offset, source_duration, title_snapshot); `channel_schedule_state` (channel_id PK, anchor_at, horizon_until, last_generated_at, generator_version, cursor JSONB) |
| 0083 | 27.3 | `0083_channel_sessions.sql` | ALTER `streaming_sessions` ADD `channel_id`, extend `mode` (+`channel`); `channel_runtime` (channel_id PK, host, pid, started_at, last_segment_at, viewer_count, state) |
| 0084 | 27.5 | `0084_hdhomerun.sql` | `hdhr_device` (singleton: device_id, device_uuid, friendly_name, tuner_count, enabled); `hdhr_tuner_leases` (id, channel_id, client_addr, started_at, last_seen) |
| 0085 | 27.10 | `0085_channel_filler.sql` | `filler_pools` (id, library_id, name, scope, kind_default); `filler_items` (pool_id, video_id, kind, duration_ms, weight); `channel_filler` (channel_id, pool_id, policy JSONB) |

27.4, 27.6, 27.7, 27.8, and 27.9 add **no migrations** — they are read
paths and UIs over the tables above plus existing `videos`, `play_state`,
and the Epic 26 metadata tables.

## Reused infrastructure (do not rebuild)

| Need | Existing component | Owner |
|------|--------------------|-------|
| FFmpeg transcode + ABR ladder | `streaming/internal/ffmpeg/{transcode,remux,hwaccel}.go` | Epic 8 |
| HLS manifest + segment serving | `streaming/internal/handlers/manifest.go`, cache `cache/hls/{id}` | Story 8.5/8.6 |
| Streaming session store + idle reaper | `streaming/internal/session/{session,reaper}.go` (slot 0039) | Story 8.9/8.10 |
| Open/Close session gRPC seam | `streaming/internal/grpcsrv`, `api` streaming proxy | Story 8.8 (§9.9) |
| HW accel detection | `streaming/internal/ffmpeg/hwaccel.go` | Epic 8 |
| Content classification (smart-mix) | `video_classification`, `video_topics` (slots 0046/0074) | Epic 26 / Story 9.9 |
| Series grouping (marathon ordering) | `series` / `series_episodes` (slot 0075) | Story 26.3 |
| Smart-query / library filters (shuffle source) | `collections.smart_query` evaluator (slot 0033) | Story 7.14 |
| Posters / thumbnails (logo + guide art) | thumbnail path, `web/design-system` image handling | Epic 8/17 |
| Job queue + debounced library pass | `processing_jobs` (slot 0002), `orchestrator/advance.py` | Epics 1/6 |
| Library ACL | `api/internal/handlers/libraryacl` (slot 0072) | Epic 7/10 |
| Watch progress (watch-from-beginning) | `play_state` / `watch_history` | Epic 8 |
| Web design system | `web/design-system/components/`, `web/src/lib/keyboard` (D-pad) | Epic 17 |
| Shared client surface (apps) | `apps/{tv,mobile,desktop}` (§6.6) | Epic 18 |

## Threat & abuse model (summary)

| Concern | Mitigation |
|---------|------------|
| Transcode DoS by spinning up many channels | Lazy activation + a per-host concurrent-channel cap + the existing session queue (Story 8.10); defining a channel costs nothing, only tuning does. |
| HDHomeRun tuner exhaustion / unauthenticated LAN pulls | `/auto/v{ch}` enforces `tuner_count` leases and the same stream auth as `/stream`; SSDP only advertises, it does not bypass auth. |
| SSRF / arbitrary file in concat list | The concat list is built **only** from `channel_programs.video_id` → resolved library paths under the validated roots; no user string ever reaches the demuxer. |
| Path traversal in M3U/XMLTV/logo | Logos go through the re-encoding thumbnail path; export URLs are server-generated; channel slug is a sanitised, validated token. |
| Clock-sync abuse / stream key sharing | Channel HLS uses the existing per-session signed URLs; tuning issues a fresh session, the relay tunnels it like any other stream. |
| Schedule generation cost blowup | Horizon is bounded (48 h default, cap 7 d); generation is a debounced, idempotent, incremental pass — never per-request. |
| Filler/bumper used to bypass library ACL | Filler items are library-scoped `videos`; a viewer only sees channels in libraries they can access. |

## Out of scope (v1)

- **Ingesting external live signals** (real tuner cards, IPTV provider
  M3Us, broadcast capture). Channels are self-sourced from the library
  only. Maktaba is not a re-streamer.
- **Recording / DVR.** Maktaba does not record its own channels back to
  disk; the content already exists on disk. (Plex/Jellyfin *can* DVR the
  HDHomeRun feed on their side — that is their feature, not ours.)
- **Per-viewer personalised channels.** A channel is one shared timeline.
  Personalised "channels" are Epic 14 recommendation rails, not linear
  TV.
- **Frame-accurate ad insertion / SCTE-35 markers.** Filler is inserted
  at program boundaries, not mid-program.
- **Crossfade as default.** Hard cut is the default transition; crossfade
  is a documented per-channel opt-in (extra FFmpeg cost).
- **Cloud-side channel hosting.** Channels run on the user's server; the
  relay only tunnels the stream (no change to Epic 25).
- **Live DRM / Widevine for channels.** Channel streams use the same
  auth model as on-demand playback; no separate DRM.

## See also

- [Epic 08 — Streaming](../08-streaming/README.md) (FFmpeg, HLS, sessions, reaper)
- [Epic 26 — Content Intelligence](../26-content-intelligence/README.md) (classification for smart-mix; series for marathon)
- [Epic 07 — API Server](../07-api-server/README.md) (smart-query, collections, ACL)
- [Epic 18 — Native Apps](../18-apps/README.md) (tvOS / Android TV / mobile / desktop client surface)
- [`architecture.md` §4](../../architecture.md#4-streaming-service), [§6](../../architecture.md#6-clients-web--apps), [§9.4](../../architecture.md#94-streaming), [§9.9](../../architecture.md#99-inter-service-grpc)
