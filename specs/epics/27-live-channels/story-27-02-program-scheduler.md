# Story 27.2 — Program scheduler

## Description

Turn a channel's rule + the media library into a **continuous,
wall-clock-anchored, 24/7 linear schedule**: an ordered list of
`channel_programs` blocks, each with an absolute `start_at`/`end_at`, a
source video (or filler), and the seek offset into that source. This is
the planning brain of Epic 27, and it lives in the **Pipeline Service**
(Python) because **smart-mix** consumes Epic 26's content classification
and because generating a 48-hour timeline is batch planning, not a
request-time operation.

The scheduler supports four modes, always pads slots to their boundary so
the timeline is **contiguous** (no gaps — wall-clock anchoring depends on
it), generates a rolling **48-hour horizon** in advance, and regenerates
incrementally on demand or when a channel's rule changes.

The output is the single source of truth for everything downstream: the
live engine ([27.3](story-27-03-live-stream-engine.md)) reads it to drive
FFmpeg, and all guide surfaces ([27.4](story-27-04-epg-generation.md),
[27.6](story-27-06-epg-grid-ui.md), [27.9](story-27-09-home-widget.md))
read it to show "what's on."

## Programming modes

| Mode | `mode_config` shape | Behaviour |
|------|---------------------|-----------|
| **shuffle** | `{seed?, reshuffle: "daily"\|"never", filter}` | Randomly orders videos resolved from `source_filter` (+ optional extra `filter`); reshuffles per period; avoids back-to-back repeats; loops the bag. |
| **marathon** | `{series_id?, order: "aired"\|"dvd"\|"filename", loop: true}` | Plays a series **in order** (reuses `series_episodes` ordering from [26.3](../26-content-intelligence/story-26-03-series-detection.md)); loops to S01E01 when done. |
| **schedule** | `{slots: [{days, start, end, source}], fill}` | Time-slot grid: specific content in specific dayparts (kids 06:00–09:00, movies 20:00–24:00); `fill` defines what plays outside declared slots. |
| **smart_mix** | `{daypart_profile, genre_weights?, diversity}` | AI-driven: balances genres/topics across the day like a network would, using `video_classification`/`video_topics`; falls back to weighted shuffle if Epic 26 data is absent. |

## Block kinds

Each `channel_programs` row has `kind ∈ {program, filler, bumper}`. A
`program` references a `video_id` (with `source_offset`/`source_duration`
to allow mid-file resume on marathon loops); `filler`/`bumper` reference a
`filler_item_id` ([27.10](story-27-10-filler-bumper-system.md)).

## Acceptance criteria

- **AC1** For an enabled channel, generation produces a **contiguous**
  sequence of blocks covering `[anchor_at, horizon_until)` (default
  horizon 48 h): for every adjacent pair, `prev.end_at == next.start_at`
  exactly. There are **no gaps and no overlaps**.
- **AC2** Block times are **absolute wall-clock** and stable: a block
  already in the past or currently playing is **never rewritten** by a
  regeneration — only the future tail is (re)generated. (Rewriting the
  current block would jump live viewers.)
- **AC3** **Shuffle** orders the resolved video set with a seeded RNG,
  never plays the same video twice in a row when the set has >1 item, and
  exhausts the bag before repeating (a fair shuffle, not independent
  random draws).
- **AC4** **Marathon** plays episodes in the configured order using
  `series_episodes`; on reaching the end it loops to the start; partial
  episodes are never split across the loop boundary.
- **AC5** **Schedule** honours declared slots on the declared days/times
  (channel-local timezone), fills undeclared time with `fill`, and
  resolves slot overlaps deterministically (later-declared slot wins, or
  explicit priority).
- **AC6** **Smart-mix** produces a genre/topic distribution across
  dayparts that matches the `daypart_profile` within tolerance (e.g.
  mornings skew kids/light, evenings skew feature-length), reusing
  `video_classification`; with classification disabled it produces a
  valid weighted-shuffle schedule and logs the fallback.
- **AC7** **Padding** always closes a slot to its boundary: when the
  chosen program is shorter than the remaining slot, filler/bumpers
  ([27.10](story-27-10-filler-bumper-system.md)) fill the remainder;
  absent filler, an "up next" card or a tail-replay of the prior block
  fills it. The timeline never has dead time.
- **AC8** Generation is **idempotent and incremental**: re-running over an
  already-generated horizon is a no-op for unchanged future blocks;
  changing the rule regenerates only the future tail from the next block
  boundary.
- **AC9** A **horizon top-up** runs periodically (cron, ~every 6 h) and
  extends each enabled channel's schedule so it always has ≥24 h of
  future blocks; an on-demand `regenerate` rebuilds the future tail
  immediately.
- **AC10** Generation **never raises** on a degenerate library: an empty
  source produces a single rolling "no content" slate block (so the
  channel still has a defined timeline and the guide can show it), not a
  crash or a gap.
- **AC11** Each block snapshots its display metadata (`title_snapshot`,
  poster ref, series/episode) at generation time so the guide is cheap to
  read and stable even if the underlying video's metadata later changes.
- **AC12** Generation respects a **horizon cap** (default 48 h, max 7 d)
  and a per-pass block cap, so a misconfigured channel cannot generate an
  unbounded schedule.

## Test cases

- **TC1** `test_contiguous_no_gaps` — generate 48 h shuffle → every
  adjacent pair touches exactly; total coverage == horizon.
- **TC2** `test_past_blocks_immutable` — generate, advance the clock into
  block 3, regenerate → blocks 1–3 byte-identical; only ≥4 change.
- **TC3** `test_shuffle_no_adjacent_repeat` — 3-video source → no video
  follows itself; bag exhausted before any repeat.
- **TC4** `test_marathon_order_and_loop` — 5-episode series → S01E01…E05
  then back to E01; ordering matches `series_episodes`.
- **TC5** `test_schedule_slots_and_fill` — kids 06–09, movies 20–24,
  fill=shuffle → guide at 07:00 is kids, 21:00 is a movie, 14:00 is fill.
- **TC6** `test_smart_mix_daypart_distribution` — seeded library with
  classifications → evening blocks are majority feature-length; morning
  majority short/kids, within tolerance.
- **TC7** `test_smart_mix_fallback` — classification disabled →
  weighted-shuffle schedule produced; fallback logged; no crash.
- **TC8** `test_padding_fills_slot` — a 22-min program in a 30-min slot →
  remaining 8 min filled with filler/bumper blocks to the boundary.
- **TC9** `test_idempotent_regen` — regen unchanged horizon → 0 future
  blocks rewritten.
- **TC10** `test_horizon_topup` — advance clock 12 h, run top-up → future
  coverage restored to ≥24 h.
- **TC11** `test_empty_source_slate` — empty source → one rolling slate
  block; channel flagged degraded; no gap, no raise.
- **TC12** `test_horizon_cap` — request 30 d horizon → capped at 7 d.
- **TC13** `test_metadata_snapshot_stable` — generate, then change a
  video's title → existing block's `title_snapshot` unchanged.

## Edge cases

- **EC1 Source larger than horizon.** A 10 000-video shuffle generates
  only the 48 h it needs; the shuffle bag persists in
  `channel_schedule_state.cursor` so the next top-up continues the bag,
  not a fresh reshuffle (unless `reshuffle: daily`).
- **EC2 Program longer than its slot (schedule mode).** A 3-h film in a
  2-h slot: either it overruns into the next slot (config
  `allow_overrun`) or it is excluded and a fitting item chosen — never
  hard-cut mid-film by the scheduler (the live engine doesn't truncate
  programs; only the slot policy decides inclusion).
- **EC3 DST / timezone shifts.** Schedule mode uses the channel-local
  timezone; the contiguous-absolute-UTC invariant still holds across a
  DST boundary (a 23-h or 25-h local day), tested explicitly.
- **EC4 Single-video source.** Shuffle of one video just loops it with
  filler between; the no-adjacent-repeat rule is vacuously satisfied.
- **EC5 Deleted/unavailable video at generation.** Skipped; if the whole
  source vanishes mid-horizon, future blocks become slate until content
  returns.
- **EC6 Marathon series shrinks.** An episode removed from a series
  mid-loop: the next loop uses the new episode set; an in-flight block
  for the removed episode is repaired (gap → filler) on regen.
- **EC7 Clock skew between pipeline and streaming.** Both anchor to the
  DB `now()`; the live engine computes offset against the same clock the
  scheduler stamped, so generator/server skew cannot desync the join.
- **EC8 Very short filler vs. large gap.** A 60-min gap with only 15-s
  bumpers available is filled by repeating/sequencing filler to the
  boundary, capped so it doesn't generate thousands of micro-blocks
  (coalesce into a single looping filler block where possible).
