# Story 33.7 — Atomic hot swap in the front door

## Description

*As a viewer, I want the channel I am watching to carry on when the owner's change goes live — in the
browser, in Plex, and remotely — with at most a segment boundary to notice. As the owner, I want the
change to land everywhere at once.*

The swap commits the new lineup, the generation flip and the routing epoch in one transaction; then
the front door switches each channel make-before-break: HLS viewers get a stitched playlist with one
discontinuity at the same wall-clock position, TS connections (Plex, VLC) are spliced at a grid IDR
without closing the HTTP connection, and the push relay switches its source the same way (story 33.8).
Watched channels switch at their next programme boundary by default.

Design: [design.md](design.md) §8 (and §9 for the relay).

## Acceptance criteria

- **AC1** Commit: under `pg_advisory_xact_lock('tv_swap')`, one transaction applies the lineup snapshot
  (adds, renumbers, edits, retires), the settings snapshot, `tv_routing` (`live_release_id`, `live_gen`,
  `previous_*`, `epoch + 1`, `standby_until`), slot roles, placement tracks and `tv_channel_switch`
  rows; a failure leaves everything as before (change back to `ready`).
- **AC2** After commit, `lineup.json`, `discover.json`, XMLTV, M3U and `/api/guide` show the new release;
  `plex_livetv` is enqueued with `force` (channel map + `reloadGuide`) and its run is linked to the change.
- **AC3** Planning: channels without demand switch at commit; with `swap_wait = boundary` watched
  channels switch at `B_c` (≤ `boundary_max_wait_minutes`); with `immediate` at commit. The plan
  re-anchors generation N+1 when `B_c` moved since staging (no programme cut).
- **AC4** HLS: a player polling `index.m3u8` across the switch sees media sequence numbers increasing by
  one per segment, exactly one `EXT-X-DISCONTINUITY` before the first staging segment,
  `EXT-X-DISCONTINUITY-SEQUENCE` advancing when it slides out, `PROGRAM-DATE-TIME` continuous (gap or
  overlap ≤ one GOP; 0 with aligned output), and no playlist with fewer segments than `hls_list_size − 1`.
- **AC5** Old segment URIs (`/s/{old}/seg-N.ts`) keep answering while the old slot is standby.
- **AC6** TS: an open `/auto/v{n}` or `live.ts` connection is spliced at a grid IDR: the client's TCP
  connection stays open; PTS/DTS/PCR are continuous (aligned: unchanged; otherwise re-timestamped);
  continuity counters continue per PID; PAT/PMT are re-sent (version bumped only if they differ); the
  tuner lease is unchanged; Plex keeps playing without a re-tune.
- **AC7** The Cloudflare push of every switched channel continues in the same session (story 33.8 AC1–AC4)
  and attached simulcast outputs see no disconnect.
- **AC8** Removed channels: viewers continue until `B_c`, then TS connections are closed and HLS gets
  `EXT-X-ENDLIST`; renumbered channels keep their stream (channel id unchanged) and are found under the
  new number in `lineup.json`.
- **AC9** Rolling swap (budget short): extra encodes stay ≤ budget; each channel is warmed
  `warm_lead` before its switch and released on the old slot right after.
- **AC10** Multi-host: a channel whose staging placement is on another host switches the same way
  (signed upstream); a per-host binary release swaps only that host's slots.
- **AC11** After all switches the change is `live`; the old slot is `standby` until `standby_until`,
  then drains and stops; `tv_change_events` and `audit_log` record commit, each switch, completion.
- **AC12** A second swap request during a swap is refused (409 `swap_in_progress`); a stale epoch is
  ignored by the front door.

## Test cases

- **TC1** `TestPlaylistStitcher` — sequences, discontinuity sequence, PDT alignment, slide-out, old URIs.
- **TC2** `TestTSSpliceAligned` and `TestTSSpliceRetimed` — byte-level: CC continuity, PCR/PTS
  monotonic, PAT/PMT version, audio after the video IDR.
- **TC3** `TestSwapCommitAtomic` — inject a failure after the lineup apply: nothing changed.
- **TC4** `TestBoundaryPlanAndReanchor`.
- **TC5** `TestRollingSwapBudget`.
- **TC6** Integration (two fake-ffmpeg slots + front door + Postgres): an hls.js-like client and a
  TS client across a swap; assert continuity rules above; `plex_livetv` job enqueued.
- **TC7** E2E with a fake RTMP ingest attached (shared with 33.8 TC5): one session, monotonic timestamps.
- **TC8** Manual on the home: Plex (Apple TV client), Safari, Chrome hls.js, VLC across a swap.

## Edge cases

- **EC1** A viewer joins during the switch window: routed by the current epoch; new viewers of a channel
  not yet switched go to live, then are spliced with the others.
- **EC2** Staging upstream fails at a channel's switch: retry once after 5 s, then `skipped` (stays on the
  old slot until a viewer-less moment, then cold); > 25 % skipped → automatic rollback.
- **EC3** A 3-hour film on air with `boundary_max_wait_minutes = 30`: switch at the deadline mid-programme;
  the content is identical (N+1 ≡ N before `B_c`), so only the encoder changes.
- **EC4** Plex re-reads the channel map while a channel is mid-switch: lineup already reflects the commit;
  the stream continues.

## Dependencies

33.1–33.5; Epic 32.3/32.4 (front door, splicing copier); 33.8 for the relay part.
