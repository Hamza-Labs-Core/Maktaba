# Story 27.10 — Filler & bumper system

## Description

Manage the short content that makes a channel feel like a real network:
**filler** (interstitials, short clips) and **bumpers** (station IDs,
"up next" cards, "we'll be right back"). Filler is what the scheduler
([27.2](story-27-02-program-scheduler.md)) uses to **pad slots to their
boundary** so the wall-clock timeline stays contiguous, and bumpers are
what plays at program transitions. This story lets the user designate
videos as filler/bumper, organise them into **pools** (global or
per-channel), assign pools to channels with a **policy**, and opt into
**auto-generated "up next" bumpers** that show the next program's poster
and title.

This story spans the data model + scheduler integration (pipeline), the
management API (Go, `api/`), and the management UI hooks (surfaced in the
admin builder, [27.8](story-27-08-channel-admin-ui.md)). The migration is
slot 0085.

## Concepts

- **filler_item** — a video designated as filler or bumper, with a `kind`
  (`station_id` | `bumper` | `interstitial` | `up_next`) and a known
  duration.
- **filler_pool** — a named collection of filler items, scoped global
  (library) or assignable to channels.
- **channel_filler** — a channel↔pool assignment plus a `policy` (when to
  insert bumpers, max consecutive filler, prefer-short-to-fit, enable
  auto up-next).

## Acceptance criteria

- **AC1** A user can **designate** one or more videos as filler/bumper
  (`POST /api/filler/pools/{id}/items`) with a `kind`; the item's
  duration is taken from the probed video metadata.
- **AC2** **Pools** are CRUD-able (`/api/filler/pools`) and scoped to a
  library; a pool is either **global** (eligible for any channel in the
  library) or assigned per-channel.
- **AC3** A channel is assigned pools + a **policy**
  (`PATCH /api/channels/{id}/filler`): which pools to draw from, max
  consecutive filler items, a "prefer item that best fits the remaining
  gap" toggle, and an "auto up-next bumper" toggle.
- **AC4** The **scheduler auto-inserts filler to fill gaps**: when a
  program leaves a remainder in its slot, the scheduler picks filler from
  the channel's pools to close the gap to the boundary, honouring the
  policy (fit preference, max-consecutive), emitting `kind=filler`/
  `bumper` `channel_programs` blocks
  ([27.2](story-27-02-program-scheduler.md) AC7).
- **AC4a** Large gaps with only short filler are filled by **sequencing/
  repeating** filler to the boundary, **coalesced** so a 60-min gap with
  15-s clips doesn't generate thousands of micro-blocks
  ([27.2](story-27-02-program-scheduler.md) EC8).
- **AC5** **Auto "up next" bumpers**: when enabled, the scheduler inserts
  (or the engine overlays) a short bumper before a program showing the
  **next** program's poster + title; these are generated, not
  pre-uploaded, from the schedule's `title_snapshot`.
- **AC6** Filler/bumper blocks are **collapsed in the guide** by default
  ([27.4](story-27-04-epg-generation.md) AC10) so they don't clutter the
  EPG, while still being real blocks the engine plays.
- **AC7** Filler items are ordinary library `videos`, so they obey
  **ACL** and are only usable on channels in libraries the user can
  access; deleting the underlying video removes it from pools and repairs
  affected schedules on the next pass.
- **AC8** A channel with **no filler available** still pads (falls back to
  an "up next" card or tail-replay per
  [27.2](story-27-02-program-scheduler.md) AC7) — filler is an enhancer,
  not a hard requirement.
- **AC9** Per-channel **and** global pools compose: a channel draws from
  its assigned pools plus eligible global pools, deduped, per policy.

## Test cases

- **TC1** `test_designate_filler_item` — POST a video as `kind=bumper` →
  item created with duration from probe.
- **TC2** `test_pool_crud_and_scope` — create global + per-channel pools;
  scope respected in eligibility.
- **TC3** `test_assign_pool_and_policy` — assign pools + policy to a
  channel; policy persisted.
- **TC4** `test_scheduler_fills_gap` — a 22-min program in a 30-min slot →
  filler blocks fill to the boundary, honouring max-consecutive.
- **TC5** `test_fit_preference` — with fit-preference on, an 8-min gap
  prefers an 8-min item over two 4-min items when available.
- **TC6** `test_large_gap_coalesced` — a 60-min gap with only 15-s clips →
  filled to the boundary, blocks coalesced (no thousands of rows).
- **TC7** `test_auto_up_next_bumper` — enabled → a bumper referencing the
  next program's title/poster is inserted before it.
- **TC8** `test_filler_collapsed_in_guide` — guide shows the program (+
  optional "Up Next"), not each filler item.
- **TC9** `test_no_filler_fallback` — channel with empty pools still pads
  (up-next card / tail-replay); no gap.
- **TC10** `test_delete_video_repairs` — deleting a filler video removes
  it from pools and repairs schedules on the next pass.
- **TC11** `test_acl_scoped_filler` — filler from a forbidden library is
  not usable/visible.
- **TC12** `test_global_and_channel_pools_compose` — channel draws from
  both; items deduped.

## Edge cases

- **EC1 Filler longer than the gap.** Never truncate a filler item to fit;
  pick a shorter item or sequence shorter ones; if only over-long filler
  exists, fall back to an up-next card sized to the gap.
- **EC2 Only one filler item.** Repeats it (with max-consecutive cap); if
  the cap is hit before the gap closes, fall back to a card/tail-replay
  for the remainder.
- **EC3 Up-next bumper for the last block before horizon end.** If the
  "next" program isn't generated yet, the bumper degrades to a generic
  station ID rather than referencing nothing.
- **EC4 Filler video deleted mid-flight.** A live block referencing a
  just-deleted filler item → engine skips to slate/next, schedule
  repaired on the next pass (consistent with
  [27.3](story-27-03-live-stream-engine.md) AC10).
- **EC5 Bumper kind mismatch.** Designating a 2-hour movie as a "station
  ID" is allowed but warned (kinds are advisory); the fit logic still
  uses the real duration, so a giant "bumper" simply won't fit small
  gaps.
- **EC6 Global pool spanning libraries.** A global pool is library-scoped;
  there is no cross-library global pool (keeps ACL simple) — a
  multi-library channel composes the global pools of each source library
  it can read.
- **EC7 Auto up-next overlay vs. inserted block.** Two implementations are
  possible — a discrete bumper block, or an overlay rendered by the
  engine. v1 uses a discrete inserted block (simpler, shows in the
  timeline); an engine overlay is a documented future option.
