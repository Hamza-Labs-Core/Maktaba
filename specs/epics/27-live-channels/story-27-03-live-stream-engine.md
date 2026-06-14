# Story 27.3 — Live stream engine

## Description

Produce a **continuous live HLS stream per channel** from the schedule
([27.2](story-27-02-program-scheduler.md)), reusing the existing FFmpeg
transcode infrastructure in the **Streaming Service** (Go). A channel is
modelled as a **long-lived virtual streaming session**: when the first
viewer tunes, the engine reads `channel_programs`, computes the
wall-clock join offset, feeds the upcoming program files to one FFmpeg
process via the **concat demuxer**, and emits a single **sliding HLS
window**. When the last viewer leaves, the session is reaped after a
grace period — channels are **lazily activated**, so transcode cost
scales with *watched* channels, not *defined* channels.

This is the runtime counterpart to the scheduler. It does **not** decide
*what* plays (the schedule does); it decides *how* the already-decided
timeline becomes bytes, joined at the right second so every viewer is in
sync.

## Key behaviours

- **Wall-clock join.** On activation (or a viewer joining a live
  channel), the engine finds the block where `start_at ≤ now < end_at`
  and starts FFmpeg seeking that block's source to
  `now − block.start_at + block.source_offset`. Subsequent blocks are
  appended to the concat input in order.
- **Sliding HLS playlist.** One bounded HLS window per rendition
  (`-hls_list_size`, `delete_segments+append_list`), continuously
  rotating — exactly the live-HLS shape, distinct from the VOD playlists
  of [Story 8.5](../08-streaming/README.md).
- **Pre-transcode look-ahead.** The engine keeps the encoder a few
  segments ahead of the live edge and pre-resolves the *next* program
  file so a program boundary never stalls the window — this is what makes
  a freshly-tuned channel start fast and boundaries seamless.
- **ABR ladder.** Reuse the existing rendition ladder + hwaccel
  detection; a channel master playlist lists the same quality tiers as
  on-demand transcode.
- **Transitions.** `cut` (default) is a clean concat boundary;
  `crossfade` is an opt-in `xfade` filter graph with documented extra
  cost. Filler/bumper blocks are just more concat inputs.
- **Lazy activation + warm window.** Cold channel → no FFmpeg. First tune
  spawns it. Last viewer leaving starts a grace timer (default ~60 s);
  re-tune within the window reuses the warm encoder for an instant
  switch; grace expiry reaps it via the existing reaper.

## Acceptance criteria

- **AC1** `POST /api/channels/{id}/tune` (proxied to streaming
  `OpenChannel` over gRPC) returns a session + `manifest_url`; the
  resulting `GET /stream/channel/{id}/manifest.m3u8` is a **live** HLS
  master + media playlists that update as the channel plays.
- **AC2** The stream joins at the **correct wall-clock offset**: a viewer
  tuning at `T` sees the frame at `T − block.start_at` of the current
  program (within one segment duration), and two viewers tuning at the
  same `T` see the **same** content.
- **AC3** The engine transcodes a channel **only while it has ≥1 active
  viewer** (segment fetches within the idle window). Zero viewers for the
  grace period → the FFmpeg child is reaped (reuses
  `streaming/internal/session` reaper); a defined-but-unwatched channel
  consumes **no** transcode CPU.
- **AC4** **Program boundaries are seamless**: the next program is
  appended to the concat input and pre-resolved before the current one
  ends; the live window does not stall, gap, or emit a discontinuity that
  breaks playback (an HLS `EXT-X-DISCONTINUITY` is emitted where codecs
  change, so players re-init cleanly rather than freezing).
- **AC5** The channel master playlist exposes the **ABR ladder** (same
  tiers as on-demand); a player can switch renditions live.
- **AC6** **Instant re-tune within the warm window**: leaving and
  re-tuning the same channel within the grace period reuses the running
  encoder (no re-spawn, no re-seek); the new viewer attaches to the live
  edge.
- **AC7** A **per-host concurrent-channel cap** bounds how many channels
  transcode at once; tuning a channel beyond the cap either evicts the
  least-recently-watched warm (zero-viewer) channel or queues via the
  existing session queue (Story 8.10), never thrashing the box.
- **AC8** The stream enforces the **same auth** as on-demand playback
  (per-session signed segment URLs); an unauthenticated or expired
  request to a channel segment is rejected like any `/stream` request.
- **AC9** **Crossfade** (when `transition=crossfade`) blends program
  boundaries via an `xfade` graph; with `cut` (default) boundaries are
  hard joins. Both keep the window contiguous.
- **AC10** If the schedule's current block references a **missing/broken
  source**, the engine skips to the next playable block and inserts
  filler/slate for the gap rather than crashing the channel; the failure
  is logged and surfaced to diagnostics.
- **AC11** The engine records runtime state (`channel_runtime`: host,
  pid, viewer_count, last_segment_at) so the API/admin can show which
  channels are live and the reaper/cap logic has a registry.

## Test cases

- **TC1** `test_tune_returns_live_manifest` — tune → master + media
  playlists present; media playlist is live (no `EXT-X-ENDLIST`).
- **TC2** `test_join_offset_matches_walclock` — with a fake clock, tune
  at a block's `start_at + 600s` → FFmpeg seek arg ≈ 600 s (within one
  segment).
- **TC3** `test_two_viewers_same_content` — two tunes at the same fake
  `now` → identical current segment.
- **TC4** `test_lazy_no_viewer_no_ffmpeg` — define a channel, never tune
  → no FFmpeg spawned; `channel_runtime` absent/idle.
- **TC5** `test_reaped_after_grace` — tune, stop fetching, advance past
  grace → reaper kills the child; `channel_runtime` cleared.
- **TC6** `test_warm_window_instant_retune` — tune, leave, re-tune within
  grace → same pid; no new FFmpeg process; attaches at live edge.
- **TC7** `test_program_boundary_seamless` — drive across a block
  boundary → window continues; discontinuity tag only where codec
  changes; no stall/gap.
- **TC8** `test_abr_ladder_present` — master lists the configured tiers;
  switching rendition mid-stream works.
- **TC9** `test_concurrent_cap` — cap=2; tuning a 3rd cold channel evicts
  an idle warm one or queues; never exceeds 2 live encoders.
- **TC10** `test_segment_auth_enforced` — unsigned/expired segment
  request → 401/403, same as `/stream`.
- **TC11** `test_broken_source_skips` — current block's file missing →
  engine inserts slate/filler and advances; channel stays up.
- **TC12** `test_crossfade_boundary` — `transition=crossfade` → xfade
  filter present in the FFmpeg invocation; window stays contiguous.

## Edge cases

- **EC1 Tune exactly at a block boundary.** `now == block.end_at` →
  engine starts at the *next* block at offset 0, not a 0-length seek into
  the finished one.
- **EC2 Schedule horizon runs out mid-watch.** If the live edge reaches
  the end of generated blocks (top-up lagged), the engine inserts filler
  and signals the scheduler to extend, rather than ending the stream.
- **EC3 Mixed codecs/resolutions across programs.** The concat demuxer
  requires consistent stream params; the engine **normalises** every
  program through the same transcode ladder (it does not stream-copy
  heterogeneous sources), emitting a discontinuity only when unavoidable.
- **EC4 Source shorter than its scheduled block.** Shouldn't happen
  (scheduler pads), but if a file is truncated on disk, the engine fills
  the remainder with filler/slate to the block's `end_at` to preserve
  the clock.
- **EC5 Host failover / session pinning.** Channel sessions are pinned to
  one host like all sessions (§4.2); if that host dies, a re-tune
  re-anchors on a new host from the schedule + wall clock (no per-session
  state lost — the schedule is the truth).
- **EC6 Crossfade across a discontinuity.** When a crossfade would span a
  forced codec discontinuity, the engine degrades that single boundary to
  a hard cut rather than producing a broken blend.
- **EC7 Very high tune churn (channel surfing).** Rapid tune up/down
  ([27.7](story-27-07-live-channel-player.md)) leans on the warm window +
  cap eviction; surfing 10 channels in 10 s must not spawn 10 permanent
  encoders — warm grace + LRU eviction bound it.
- **EC8 DVR-style "watch from beginning."** A request to play the
  *current program from its start* is **not** the live channel — it is an
  ordinary on-demand session for that `video_id` at offset 0
  ([27.7](story-27-07-live-channel-player.md) AC), so it doesn't perturb
  the shared live timeline.
