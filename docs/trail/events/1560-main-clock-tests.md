---
schema: 1
kind: pr
pr: 1560
title: "Two tests that failed main on time: the watch pages after 23:30 UTC, and the 75 restart judged by the wall clock"
date: 2026-11-24
session: 71f9924d-5382-420c-bb6a-d0e85685bd01
agent: claude-code
prs: [1560]
---

## What happened
- Main's run 45292196113 on b8af3b24c (the #1550 merge) had every Apple UI job green on its
  first attempt: 12 of 12 Player UI shards, both shots shards, Mac, TV, Remote, and Roku. Three
  other jobs failed:
  - the CVE gate: fixed by #1554;
  - broadcast-edge;
  - launcher (windows-latest).
- **broadcast-edge, "the watch pages":** the channel and guide pages list today's
  programmes, and today is the UTC day (`today` in `src/guide.ts`). The test's guide puts its
  next programme 30 minutes after `now()`, so from 23:30 UTC that programme is tomorrow's and
  drops off both pages. The run was at 23:51 UTC: "Cartoons next" and its CC BY credit were
  missing. #1560 pins the edge clock (`src/clock.ts`) to 12:00 UTC for that suite. With the
  pin moved to 23:51, the same two tests fail as on main; at 12:00 all 192 of the package's
  tests pass.
- **launcher, `TestLauncherRestartsOn75`:** it judged "no backoff" by the wall clock across
  three process starts. A loaded Windows runner took 2.255 s, past the 2 s backoff, with no
  backoff asked for. The test now records the waits the launcher asks for through
  `Config.Sleep`, as `TestLauncherBackoff` already did, and expects none. A backoff planted
  on the 75 path fails it.

## What we learned
- A test that builds data around `now()` and a page that cuts at a calendar boundary fails
  for part of every day. CI runs late in the UTC evening, so it shows up.
- The launcher's tests had already met wall-clock gaps once (a fixed 1.5 s failed at
  1.54 s), and raising the bound only moved the edge. Asserting on what the code asks for
  removes it.

## Keep doing
- Reproduce a time-of-day failure by moving the clock, as here with the pin at 23:51.

## Stop doing
- Asserting on gaps between process starts on shared runners.
