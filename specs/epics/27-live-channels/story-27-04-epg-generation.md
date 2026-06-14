# Story 27.4 — EPG generation & exports

## Description

Expose the **Electronic Program Guide**: the channel schedule
([27.2](story-27-02-program-scheduler.md)) as guide data, in three forms
— an **internal JSON API** for Maktaba's own clients
([27.6](story-27-06-epg-grid-ui.md), [27.9](story-27-09-home-widget.md)),
a standard **XMLTV** export for external guide consumers, and an **M3U
playlist** for generic IPTV players (VLC, etc.). All three are **read
paths over `channel_programs`** filtered by time — there is no separate
guide store; the schedule is the single source of truth.

This story owns the **API** (Go, `api/`). The HDHomeRun lineup
(`/lineup.json` etc.) is its sibling and lives in streaming
([27.5](story-27-05-hdhomerun-emulation.md)); this story owns the
human/standards-format guide.

## Outputs

| Output | Endpoint | Consumer |
|--------|----------|----------|
| Guide (grid) | `GET /api/channels/guide?start=&end=&category=` | EPG grid UI (27.6) |
| Guide (one channel) | `GET /api/channels/{id}/guide?start=&end=` | channel detail, mini-guide |
| What's on now | `GET /api/channels/now` | home widget (27.9), lineup summaries |
| XMLTV | `GET /api/channels/xmltv` | Plex/Jellyfin guide import, external EPG apps |
| M3U | `GET /api/channels/playlist.m3u` | VLC, IPTV players |

## Guide payload (per program block)

`channel` (id, number, name, logo), `start`/`stop` (ISO-8601 absolute),
`title`, `sub_title` (episode title), `desc`, `poster`, `genre`/`category`,
`rating`, `series`/`season`/`episode`, `kind` (program/filler/bumper),
`is_live` (true for the block currently on air), `progress` (0..1 for the
current block). Most fields come from the block's `title_snapshot`
([27.2](story-27-02-program-scheduler.md) AC11), so guide reads don't
join the whole library at request time.

## Acceptance criteria

- **AC1** `GET /api/channels/guide?start=&end=` returns, for every
  enabled channel the user can read, the program blocks overlapping
  `[start, end)`, each with the full guide payload, ordered by channel
  then time. `?category=` filters channels.
- **AC2** `GET /api/channels/{id}/guide` returns one channel's blocks for
  the range; out-of-range or ungenerated tails return what exists plus a
  `horizon_until` marker (the client can show "guide ends here").
- **AC3** `GET /api/channels/now` returns each channel's **currently
  airing** block (the block where `start ≤ now < stop`) plus the **next**
  block, with `progress` for the current one — the widget/lineup summary
  payload, computed without spinning up any transcode.
- **AC4** `GET /api/channels/xmltv` produces **valid XMLTV**: a
  `<tv>` document with `<channel>` elements (id = stable channel slug,
  display-name, icon) and `<programme>` elements (`start`/`stop` with
  timezone offset, `title`, `sub-title`, `desc`, `category`, `icon`,
  `episode-num` in `xmltv_ns` and `onscreen` systems). It validates
  against the XMLTV DTD and imports cleanly into Plex/Jellyfin.
- **AC5** `GET /api/channels/playlist.m3u` produces a valid **M3U**:
  `#EXTM3U` header with `url-tvg`/`x-tvg-url` pointing at the XMLTV
  endpoint, and one `#EXTINF` per enabled channel carrying
  `tvg-id` (= slug, matching XMLTV channel id), `tvg-name`, `tvg-logo`,
  `tvg-chno` (= number), `group-title` (= category), followed by the
  channel's live HLS URL.
- **AC6** The XMLTV/M3U `tvg-id` and the channel slug **match exactly**,
  so an external player can join the playlist's channels to the guide's
  programmes.
- **AC7** Guide times are **absolute** and consistent with what the live
  engine plays (same `channel_programs` rows); the guide and the stream
  never disagree about what's on.
- **AC8** Export responses are **cached with a short TTL** (e.g. 30–60 s)
  keyed by range + user-visible channel set; a schedule regeneration
  invalidates the cache. Reads are O(blocks-in-range), never a full
  library scan.
- **AC9** Guide endpoints enforce `libraryacl`: a user sees only channels
  in libraries they can read; XMLTV/M3U for an **external** consumer use
  a scoped access token (the same token model used by `/auto/v{ch}`),
  not the interactive session, so a token only exposes its permitted
  channels.
- **AC10** `filler`/`bumper` blocks are **collapsed** in the
  human/external guide by default (shown as part of the surrounding
  program or as a generic "Up Next"/station-ID entry), configurable, so
  the guide isn't littered with 15-second rows.

## Test cases

- **TC1** `test_guide_grid_range` — request a 3 h window → every enabled
  channel's overlapping blocks returned, ordered, with full payload.
- **TC2** `test_guide_now_current_and_next` — `/now` → current block has
  `is_live=true` and a `progress`; next block present.
- **TC3** `test_xmltv_validates` — output parses and validates against
  the XMLTV DTD; channel ids == slugs; `episode-num` present for
  episodic blocks.
- **TC4** `test_m3u_tvg_id_matches_xmltv` — every M3U `tvg-id` has a
  corresponding XMLTV `<channel id>`.
- **TC5** `test_m3u_has_live_urls` — each `#EXTINF` is followed by a
  resolvable live HLS URL for that channel.
- **TC6** `test_guide_matches_stream` — guide's current block for a
  channel == the block the live engine reports playing.
- **TC7** `test_export_cache_and_invalidation` — two quick XMLTV
  requests hit cache; a regen invalidates it.
- **TC8** `test_acl_scopes_channels` — a user without access to library X
  sees none of X's channels in guide/xmltv/m3u; a scoped token exposes
  only its channels.
- **TC9** `test_filler_collapsed_in_guide` — a slot padded with bumpers
  shows one program entry (+ optional "Up Next"), not N micro-entries.
- **TC10** `test_horizon_marker` — request beyond generated horizon →
  blocks up to `horizon_until` + the marker.

## Edge cases

- **EC1 Range spanning past + future.** Past blocks are returned as-is
  (immutable history) alongside future ones; the "now" line sits between.
- **EC2 Channel with no schedule yet.** A just-created channel mid-first-
  generation returns an empty block list + a `generating` flag, not an
  error.
- **EC3 XMLTV for a huge horizon.** XMLTV is capped to the generated
  horizon (≤7 d); requesting more returns what exists. Response is
  streamed, not buffered, for large lineups.
- **EC4 DST in XMLTV timestamps.** `start`/`stop` carry explicit UTC
  offsets; a programme spanning a DST change has correct offsets on each
  end.
- **EC5 Disabled channel mid-range.** A channel disabled after generation
  is excluded from all guide outputs even though its `channel_programs`
  rows still exist.
- **EC6 Empty/degraded channel.** A channel showing a slate appears in
  the guide as a single rolling "No content" programme, so external
  guides don't show a gap.
- **EC7 Token scope vs. interactive scope.** An external XMLTV token
  scoped to channels 1–5 must not leak channel 6 even if the operator who
  minted it can see it; the export is scoped to the **token**, not the
  minter.
