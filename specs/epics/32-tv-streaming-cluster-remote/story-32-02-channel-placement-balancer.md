# Story 32.2 — Channel placement and balancer

## Description

*As a viewer, I want any channel I tune to start on whichever machine has room; as the owner, I
want each channel encoded once in the whole cluster, with the Mac doing the work first.*

The home decides where each channel is encoded. The decision is a row per channel
(`channel_placements`) with a generation number that fences stations: a host may run a channel
only while the row names it with that generation.

Design: [design.md](design.md) §3.

## Acceptance criteria

- **AC1** The first request for a cold channel (HLS, `/auto`, `live.ts`, remote push) places it:
  under a per-channel advisory lock the balancer picks a host, a new generation is taken from the
  cluster sequence, the row is written as `placing`, and `NOTIFY tv_placement` is sent.
- **AC2** Policy `home_first` (default): the home while its used slots are below `spill_at` % of
  its capacity (default 100); then the online node with the lowest used/capacity (ties: higher
  weight, then host id); the home again if no node has room.
- **AC3** Policy `balanced`: the lowest used/capacity over all online hosts; the home wins ties.
- **AC4** A pin (`tv_cluster.pins[slug] = host name`) wins while that host is online with room;
  otherwise the policy applies and the admin page says the pin was skipped and why.
- **AC5** Channels with live (yt-dlp) blocks within the next 24 h go only to hosts with
  `can_live`. A remote push puts no constraint on the host: the home's push relay pushes (design
  §7.4, Epic 33.8).
- **AC6** Capacity counts placements, not reported stations: two concurrent first requests for
  different channels never overfill a host.
- **AC7** A station starts only when its host holds the current generation (home: the resolved
  placement; node: the `X-Maktaba-Placement` header matches the row). On start the host acks
  (`state = live`). Every station re-checks its placement every 5 s and on NOTIFY and stops within
  5 s when superseded.
- **AC8** Placed channels are never moved to rebalance load (only failover, drain, move, or a pin
  change while idle).
- **AC9** When the engine reaps an idle station it releases its placement (only if it still holds
  that generation).
- **AC10** No room anywhere: HLS 503 `Retry-After: 30`, HDHomeRun "all tuners in use" (as today),
  and the reason (`all hosts full`, `no host can run live blocks`, …) is shown on the admin page.
- **AC11** With only the home online, placement always answers the home and behaviour is today's.

## Test cases

- **TC1** `TestChooseHomeFirst` — table: home at 15/16 → home; 16/16 → least-loaded node; all full → none.
- **TC2** `TestChooseBalanced` — ratios and tie rules.
- **TC3** `TestPinWinsAndFallsBack`.
- **TC4** `TestPlaceConcurrentSameChannel` — 20 goroutines → one row, one generation.
- **TC5** `TestPlaceConcurrentCapacity` — a host with one free slot, two channels at once → one placed there.
- **TC6** `TestFenceStopsSupersededStation` — bump the generation → the station stops within one check.
- **TC7** `TestReleaseOnReap` — a reaped station releases only its own generation.
- **TC8** `TestLiveBlocksNeedCanLive`.

## Edge cases

- **EC1** Two home streaming processes by mistake: the advisory lock serialises decisions; fencing
  stops the loser's stations; the admin page flags two `home` hosts.
- **EC2** A placement written but never acked within 20 s → treated as failed and re-placed.
- **EC3** A channel deleted while placed: `ON DELETE CASCADE` removes the row; the station's next
  check finds no row and stops (fixes the audit's "a deleted channel slates until reaped").

## Dependencies

32.1 (hosts, heartbeats, slot 0123).
