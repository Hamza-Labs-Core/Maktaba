# Story 33.3 — Schedule generations

## Description

*As the owner, I want a lineup or schedule change to be prepared next to the schedule that is on air,
not on top of it, so nothing on air changes until the switch — and no programme is ever cut.*

`channel_programs` and `channel_schedule_state` gain `sched_gen`. Live readers read `tv_live_gen()`;
a slot reads its release's generation. A new generation copies the old one up to each channel's next
block boundary and is regenerated after it. New channels are `staged` until the swap; removed ones
`retired` after it.

Design: [design.md](design.md) §4.

## Acceptance criteria

- **AC1** Migration 0123 adds `sched_gen` (default 0) with the new unique and time indexes built
  concurrently; existing data is generation 0; `tv_live_gen()` answers 0 without a routing row.
- **AC2** Every live reader listed in design §4.2 reads `channel_programs_live` or passes a generation;
  a test seeds two generations and checks each reader returns only the live one.
- **AC3** `build_generation(release, T)` copies, per channel, the blocks of N starting before `B_c(T)`
  (the end of the block on air at T) and regenerates after `B_c` only for `sched_changed` channels;
  unchanged channels copy N whole. The block on air at any instant before `B_c` is identical (id of
  video, offsets, start, end) in N and N+1.
- **AC4** Re-anchoring when `swap_at` moves replaces N+1's blocks between the old and the new `B_c`
  with N's and regenerates the tail; programmes before the earlier boundary are untouched.
- **AC5** The top-up extends every open generation (live, staging, standby within grace) with its own
  advisory lock; a `ready` change waiting 3 days still has a 7-day horizon.
- **AC6** `channels.lifecycle` (`staged`/`live`/`retired`); number and slug unique among `live`; every
  live reader (HDHR lineup, guide, API lists, `write_channels._actual`) filters `live`.
- **AC7** The stale flag no longer rewrites the live generation when staging is on (it marks the draft;
  story 33.12). With staging off, today's behaviour (generation 0 rewritten in place) is unchanged.
- **AC8** GC (daily) deletes generations not live/staging/within retention, retired channels past
  retention, staged channels of failed/cancelled changes, deferred filler removals.

## Test cases

- **TC1** `test_generation_copy_to_boundary` — on-air block kept, tail regenerated.
- **TC2** `test_generation_unchanged_channel_full_copy`.
- **TC3** `test_reanchor_moves_boundary`.
- **TC4** `test_topup_open_generations` — two generations, each extended once, locks distinct.
- **TC5** `TestLiveReadersFilterGeneration` (Go: hdhr guide, api guide, channel repo).
- **TC6** `test_lifecycle_unique_numbers` — a staged channel may use a number a live channel has.
- **TC7** `test_gc_retention`.
- **TC8** Migration test on a copy of a real database (≈ 20 channels × 7 days): runs online, no lock
  held over 1 s on `channel_programs`.

## Edge cases

- **EC1** A live block (yt-dlp) on air at T: its end is its scheduled end; same rule.
- **EC2** A block longer than `boundary_max_wait_minutes` (a 3 h film): `B_c` is still its end for the
  schedule (no cut in N+1); the *switch* may happen earlier mid-programme with identical content (33.7).
- **EC3** SQLite installs: same columns; partial indexes supported; `tv_live_gen()` becomes a
  subquery in the SQLite repo.

## Dependencies

33.2.
