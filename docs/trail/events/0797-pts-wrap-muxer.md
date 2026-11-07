---
schema: 1
kind: pr
pr: 797
title: A muxer started just before the PTS wrap keeps the wall clock's timestamps
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 790
---

## What happened
- Both media-e2e failures of 2026-11-07 (`MuxerRestart` and `TestTwoHostsOneMachine`) had a muxer that started within 60 s before the 33-bit PTS wrap. Its output ran 30–40 s ahead of the wall clock.
- Viewers saw it too: the program date-time, the continuous TS for Plex/HDHomeRun clients, and the failover splice were all off by that much for as long as that muxer ran.
- The station's offsets were right. ffmpeg's demuxer `correct_ts_overflow` (on by default) takes 2^33 off a stream whose first timestamp lies in the last 60 s before the wrap, and with `-copyts` the output then starts near 0. `MuxerArgs` now passes `-correct_ts_overflow 0`.
- `TestStation_RealFFmpeg_AtTheWrap` uses a fake clock (`ptsShiftTicks`) to start a station 30 s before a wrap, restarts its muxer, and crosses the wrap. Before the fix it was 25.6, 13.9 and 14.6 s ahead; now it is in the media-e2e list. The tests compare timestamps modulo the wrap (`wrapDelta`), and ffprobe reads them raw.

## What we learned
- ffprobe applies the same correction, so a probe near the wrap reports negative start times. Probe with `-correct_ts_overflow 0` when the timestamps are already the truth.
- A wrap bug that shows up only one day in 26 can be reproduced on demand by shifting the station's clock with `ptsShiftTicks` to a whole number of IDR intervals before the wrap.
- A muxer that runs across the wrap needs no correction; only one that starts in the last minute before it is affected.

## Keep doing
- Before fixing a wall-clock test that fails near the wrap, check whether the product itself is wrong. Here the test was right and the muxer was wrong.

## Open
- Nothing.
