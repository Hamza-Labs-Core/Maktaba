---
schema: 1
kind: pr
pr: 1382
title: "Broadcast: send each segment's start time, not ffmpeg's late date"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1382 merged as 694fc9bf6. The edge's canary glass-to-glass (visible time minus
  `X-Maktaba-Segment-Time`) always read 0: the home sent ffmpeg's `EXT-X-PROGRAM-DATE-TIME`,
  which the hls muxer stamps from when it opened, after probing the live TS (about 4.7 s with
  the ladder's flags), so every date landed at or after its segment's close.
- `RungWatcher` now corrects the date by the smallest `(seen - duration - PDT)` over a run of
  segments (poll lag only makes `seen` later), resetting on a sequence break or a date jump over
  1 s; with no date it sends `seen - duration`. `X-Maktaba-Time` is unchanged. Specs 50.21 and
  50.22 say the segment time is its start.
- The edge change was a comment only, so the PR carried a `No-Test-Change:` trailer naming that
  file; affected-tests accepted it.

## What we learned
- A metric clamped with `max(0, …)` hides a sign error as a flat zero; a zero latency is a bug
  signal, not a good number.
- ffmpeg has no option to anchor its program date to input arrival; correct it from when each
  segment first appears in the playlist instead.

## Open
- broadcast-observe's glass-to-glass check is still in its expected failures; take it out once
  it passes on main.
