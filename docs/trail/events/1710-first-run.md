---
schema: 1
kind: pr
pr: 1710
title: "With PosterBoard and apsd off: the first run, and two corrections"
date: 2026-11-25
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- #1710's own run had all 14 iPhone and iPad jobs green on the first attempt, with no failure to
  end the app and no restart of the test process.
- The simulators did not settle quicker, as #1710's first estimate said they would: 35 to 322 s on
  the first 12 jobs. Of the four logs opened, one has the runner idle as it settled.
- The first event says apsd and diagnosticd together held over one of the three cores. They held
  up to that: at least 63 % of a core on average, over 100 % in 11 of 48 looks, 136 % at the most.

## What we learned
- A step whose commands all end in `|| true` looks the same when it did nothing. #1713 makes this
  one warn when the simulator lists no services, and limits each call to 30 s.

## Open
- One green run is weak proof for failures this rare: what shows the fix is their absence over the
  runs that follow.
- What the settling still waits for is not known.
