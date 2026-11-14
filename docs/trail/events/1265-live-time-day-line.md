---
schema: 1
kind: pr
pr: 1265
title: "The live TV time test holds in the hour before the home day line"
date: 2026-11-14
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- #1265 merged as 8e4f464ea. `TestP57LiveTimePG` expected a 60-minute daily limit to end a live
  TV stream an hour from now. In the hour before the home day line (04:00 home time) today's
  minutes reach the line and the stream carries on into the next day's 60, so the home answers
  an hour after the line; the test failed every day in that hour (a 58.03 session met it
  locally). Both checks now use `endsIn(used)`, computed from the home zone and the day line.
  Test-only.
- Proven with a scratch Postgres at 08:18 UTC: under `TZ=America/New_York` (03:18 there) the old
  test failed with `time_ends_at = 10:00Z` and the new one passed; also UTC, Asia/Amman and
  Pacific/Kiritimati.

## What we learned
- A time test that reads `time.Now()` needs every boundary the product has in its expectation:
  here the day line, and the carry into the next day's allowance (a first fix that expected the
  stream to end *at* the line was wrong; the product was right).
- Reproduce a clock window by choosing the zone, not by waiting: the parental tests take the
  home zone from `time.Local` when none is set, so `TZ=` moves the test into the window.
- macOS has no `timeout`: a merge watcher that wraps `gh` in it fails every read and never
  merges, silently. Use `perl -e 'alarm shift; exec @ARGV'` (or check the watcher's first log
  line before walking away). A merge refused with "Base branch was modified" is a race with
  another merge: re-read the checks and merge again on the same head.

## Keep doing
- Fix a time-dependent test the day it is seen, with the window reproduced both ways (old fails,
  new passes), even when it belongs to another epic.
