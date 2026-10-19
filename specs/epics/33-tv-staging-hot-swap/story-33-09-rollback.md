# Story 33.9 — Rollback

## Description

*As the owner, I want a button that puts the previous TV back — instantly while the old process is still
warm, and in a few minutes after that — and I want Maktaba to do it by itself if the new one crashes
right after a swap. Viewers, remote viewers and simulcast outputs must not notice any more than they
noticed the swap.*

Design: [design.md](design.md) §10.

## Acceptance criteria

- **AC1** Warm rollback (`POST /api/tv/rollback` while `now < standby_until` and the standby slot is
  healthy): one commit restores the previous lineup snapshot into `channels` (un-retiring removed
  channels, returning added ones to `staged`), the previous settings, `live_gen := previous_gen`,
  `epoch + 1`, roles swapped; then every channel switches back **at once** with the same HLS stitch and TS
  splice as a swap; `plex_livetv` runs again. Completes within 10 s for warm channels.
- **AC2** Channels the standby no longer holds warm are warmed first (≤ 10 s) or switched cold if the
  budget is short (one stall), never left on the rolled-back release.
- **AC3** **Push and simulcast continuity are symmetric to the swap**: the relay switches source back in
  the same RTMPS/SRT session; same Cloudflare video id; tokens valid; simulcast outputs (YouTube, Facebook,
  custom RTMP) see no disconnect (33.8 AC2–AC4 hold for rollback).
- **AC4** Cold rollback (after the grace, within `rollback_retention_hours`): creates a `rollback` change
  whose release is the previous one with a fresh generation copied from the retained one and anchored at
  the next boundary; verify → swap; auto-promoted unless `auto_promote.rollback = approval`.
- **AC5** Automatic rollback inside the grace on: live slot crash, crash loop (2 restarts in 10 min), or a
  post-swap health check failing (`channel.*`, `ts.*`) for > 30 s; the change ends `rolled_back` with
  `end_reason = auto:<cause>`; a notification is sent (33.13). Outside the grace there is no automatic
  rollback (watchdog restart; see design §12).
- **AC6** After a rollback the rolled-back release can be re-staged as a new change (its draft is kept).
- **AC7** The Changes page shows "Roll back" with "warm (instant) — 12 min left" or "cold (~3 min)" and
  disables it when no rollback point exists.

## Test cases

- **TC1** `TestWarmRollbackRestoresLineupAndGen` — adds/removes/renumbers reversed.
- **TC2** `TestRollbackContinuity` — HLS and TS clients across swap + rollback (integration).
- **TC3** `TestAutoRollbackOnCrashInGrace` — kill the live slot 1 min after the swap; rollback ≤ 10 s.
- **TC4** `TestNoAutoRollbackAfterGrace`.
- **TC5** `TestColdRollbackAnchorsAtBoundary`.
- **TC6** E2E push: the 33.8 TC5 fake-ingest + attached-output test runs swap → rollback and asserts one
  ingest session and one output session throughout, monotonic timestamps.

## Edge cases

- **EC1** Rollback while a swap is still switching (some channels at `B_c` later): switched channels go
  back; unswitched channels are simply not switched; the plan is cancelled.
- **EC2** A second change was staged after the swap: rollback cancels its verification first (its draft
  stays).
- **EC3** Retention expired: rollback answers 409 `no_rollback_point`; the runbook explains re-staging an
  older release by hand.

## Dependencies

33.7, 33.8.
