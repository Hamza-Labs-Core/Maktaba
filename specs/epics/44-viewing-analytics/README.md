# Epic 44: Analytics counts every way of watching, and every app's use of the API

## Why

The owner asked for every app and the API to record their use, shown on a better analytics page.
Analytics today reads `watch_sessions` alone, and only two players write it:

- **Plays.** The web (and the desktop and mobile shells, which run the web bundle) and Android TV
  call `/api/watch/start|heartbeat|stop`. The iPhone, iPad and Mac Player, tvOS and the Android phone,
  foldable and tablet Player never do: they open a stream session and post
  `/api/stream/sessions/{id}/progress` (for the resume point) and nothing more. On the home on
  2026-10-26: 65 stream sessions, 17 watch sessions.
- **Live TV.** No viewer of a channel is recorded anywhere. The web reads a channel's HLS from the
  streaming front door with no session; Plex's Live TV tunes the HDHomeRun emulation, whose leases live
  in memory (`hdhr_tuner_leases` has no writer); the remote (Cloudflare) TV keeps `tv_remote_sessions`,
  which analytics never reads. `channel_runtime.viewer_count` counts only MPEG-TS viewers, and only now.
- **Apps.** No request says which app sent it: the API's metrics are by route and status only, and
  only the native apps send `X-Maktaba-Client: native` (for the sign-in response).

Plex's own library plays stay out (the owner: "don't want plex usage in itself, just the tv"): Plex
counts only as one more way of watching Maktaba's channels, through the tuner.

## Goals

1. **Every play of a Maktaba video is recorded, whichever app plays it**, server-side, from what every
   player already sends (the stream session and its progress), with the app, its version, platform and
   device. The web's own `/api/watch/*` calls go (one record per play, not two). Android TV keeps
   `/api/watch/*` (it opens no stream session) and says which app it is.
2. **Every Live TV viewer is recorded:** watching a channel in a Maktaba app or the web, through Plex's
   Live TV (the HDHomeRun tuner), and through the remote TV. One row per viewing, with the channel,
   the person when known, the surface and the client.
3. **Every app says which it is**, on every request: `X-Maktaba-Client: <app>/<version> (<platform>;
   <device>)`. The API counts its requests per app and day (requests, errors, time, people) and keeps
   it.
4. **A new analytics page** (admin): watch time and viewers across everything, by surface and app;
   who and what is watching now (videos and channels); trends; top videos, channels, libraries and
   people; the apps (people, requests, errors, speed, versions in use); devices and platforms.
5. The person's own controls stay: `user_analytics_prefs.track_enabled = false` records none of their
   plays or viewing (the anonymous counts of API requests per app go on: no person in them).

## Non-goals

- Plex library plays, view counts or history (Plex's own analytics).
- Per-request logs (the access log has them for a while); the API keeps daily counts.
- Analytics for non-admins beyond what exists (their own history; a video's aggregate stats).

## Design

### Which app: `X-Maktaba-Client`

`<app>/<version> (<platform>; <device>)`, e.g. `player/1.3.0 (ios; phone)`, `remote/1.0 (android;
tablet)`, `web/0.3.25 (macos; desktop)`, `tv/1.0 (tvos; tv)`, `tv/1.0 (androidtv; tv)`.

- `app`: `web`, `desktop` (the Mac/Windows/Linux shell), `mobile` (the Capacitor shell), `player`,
  `remote`, `tv`. `platform`: `ios`, `ipados`, `macos`, `tvos`, `android`, `androidtv`, `windows`,
  `linux`, `web`. `device`: `phone`, `tablet`, `foldable`, `desktop`, `tv`.
- The API parses it once (middleware) into the request's context; anything unparsable or absent is
  `unknown` (old app builds: `native` alone reads as `app=native`). Values are lowercased and bounded
  (32 characters, `[a-z0-9._-]`): the header is a label, never trusted for rights.
- `X-Maktaba-Client: native` keeps its sign-in meaning (an app whose app part is not `web`).

### Plays: from the stream session

- `watch_sessions` gains `stream_session_id` (unique when set), `client_app`, `client_version`; the
  existing `device_type` and `platform` are filled from the header.
- `POST /api/stream/sessions/{id}/progress` (every player, every few seconds) also records the play: the
  first progress of a stream session inserts its `watch_sessions` row (`active`); each later one
  updates `last_heartbeat`, the position's `percent_complete` and the watched seconds (the time between
  two progress posts while the position moved forward, capped at the gap, so a paused player adds
  nothing); a completed position marks it `completed`. Closing the stream session
  (`DELETE /api/stream/sessions/{id}`) stops it; the existing reaper marks one that went quiet
  `interrupted` after 5 minutes.
- Skipped when the person turned tracking off (as `/api/watch/start` does).
- The web stops calling `/api/watch/*` (its player has a stream session). `/api/watch/*` stays for
  players without one (Android TV) and records `client_app` from the header the same way.

### Live TV viewing: `tv_watch_sessions`

One row per viewing of a channel:

| column | |
|---|---|
| `id` | UUID |
| `channel_id` | the channel (FK, cascade) |
| `user_id` | the person when known (a Maktaba app or the web, the remote TV); NULL for the tuner |
| `surface` | `app` (a Maktaba app or the web on the home's network), `tuner` (Plex Live TV through the HDHomeRun emulation), `remote` (the remote TV) |
| `client_app`, `client_version`, `platform`, `device_type` | from `X-Maktaba-Client` when the surface has it |
| `client_addr_hash` | the viewer's address, hashed with the home's salt (the tuner's one way to tell viewers apart) |
| `started_at`, `last_seen_at`, `ended_at` | |
| `seconds` | watched time |
| `end_reason` | `closed`, `idle` (no segment for 60 s), `replaced` |

Writers:

- **App and web:** the streaming front door sees each channel HLS request (it authenticates every one).
  A viewer is (channel, principal, client); its first segment opens a row, each segment moves
  `last_seen_at`, and 60 s without a segment ends it (`idle`). Writes are batched (every 15 s) from
  an in-memory tracker, so a segment fetch never waits on the database.
- **Tuner:** a lease of the HDHomeRun emulation (`/auto/v<channel>`) opens a row when the stream
  starts and ends it when the stream ends; `hdhr_tuner_leases` gets its writer (channel, client,
  started, last seen) for the tuner page.
- **Remote:** `tv_remote_sessions` already holds one row per viewing; analytics reads it directly as
  `surface = remote` (no copy).
- The person's tracking switch applies to the app and remote rows.

### API use per app: `api_usage_daily`, `api_usage_people_daily`

- `api_usage_daily (day, client_app, platform, requests, errors, total_ms, slow)`: one row per day,
  app and platform. `errors` = 5xx; `slow` = over the route's p95 budget when it has one, else 1 s.
- `api_usage_people_daily (day, client_app, user_id)`: who used which app that day (people per app,
  active people). Rows older than the analytics retention go with the reaper.
- Counted in memory and added to the rows every minute (and at shutdown); a lost minute in a crash
  is acceptable. `/metrics` (Prometheus) gains a `client_app` label on the request counter.

### The page

`/admin/analytics` becomes tabs over one time range (today, 7, 30, 90 days, custom) and filters
(library, person, app, surface):

- **Overview:** watch time, plays, viewers, people active, Live TV hours; watch time by surface
  (apps / Live TV in apps / Plex Live TV / remote TV) and by app; the trend (by hour or day);
  the day × hour heatmap.
- **Now:** who is watching what, on which app and device, videos and channels together, live.
- **Videos:** top videos, libraries, genres; completion.
- **Live TV:** top channels, by surface; peak viewers; the remote TV's minutes and cost (from
  `tv_remote_usage_daily`).
- **People:** per person: watch time, plays, channels, apps used, last seen.
- **Apps:** per app and platform: people, requests, errors, error rate, slow requests, versions in use;
  devices and platforms.
- Export (CSV/JSON) of plays and viewings.

All endpoints are admin-only (`requireAdmin`), with role tests.

## Stories

| # | Story | |
|---|---|---|
| 44.1 | Schema (slot 0158): `watch_sessions` columns, `tv_watch_sessions`, `api_usage_daily`, `api_usage_people_daily`, `hdhr_tuner_leases` writer columns | |
| 44.2 | `X-Maktaba-Client`: the middleware; every app sends it (web, desktop and mobile shells, Apple apps, Android apps, Android TV, tvOS) | |
| 44.3 | Plays from the stream session (`/progress`, close, reaper); the web's `/api/watch/*` calls removed; Android TV's labelled | |
| 44.4 | Live TV viewings: the front door's tracker, the tuner's leases; remote TV read as is | |
| 44.5 | API use per app: the counters, the minute flush, the retention, the Prometheus label | |
| 44.6 | Analytics API over plays, viewings and app use (admin, role tests) | |
| 44.7 | The analytics page (tabs, filters, now, export), English and Arabic, help page | |

## Acceptance

- A play on each app (web, Mac/iPhone/iPad Player, Android Player, tvOS, Android TV) appears on the
  page within a minute with its app, platform and device; one row per play.
- Watching a channel in the web, through Plex Live TV and through the remote TV appears under Live TV
  with its surface.
- The Apps tab shows each app's requests and people for today.
- A person with tracking off appears in no play or viewing.
- Non-admins get 403 from every analytics endpoint.
