---
schema: 1
kind: pr
pr: 1430
title: "Two slow unit tests do only the work they check"
date: 2026-11-15
session: 93956a29-f060-4698-b954-258789da8f9a
agent: claude-code
prs: [1426]
---

## What happened
- On #1426's CI (run 43745326693, attempt 1, job 132907388664), two tests that #1426 didn't
  touch broke the unit tier's 1.2 s hard cap. They broke it even when re-run alone:
  - `cmd/maktaba-launcher::TestANewerAppTakesOverFromTheKeptCopy`: 1.31 s, then 1.45 s
    alone.
  - `shared/logkeeper/go::TestRollAndCompress`: 1.51 s, then 1.25 s alone.
  - They passed on the next attempt. This closes the first Open item of
    `1426-logs-index-idle`.
- Cause, from `-race` cpu profiles:
  - The launcher test spent 85% of its time in `layout.CopyFile`. It copied the race-built
    test binary (13 MB+, fsynced) into six payloads, and it never starts any of them:
    `bundlePayload()` only reads `payload.json` and stats `bin/maktaba-server`.
  - The logkeeper test was plain volume: 1,100 lines of 1 KB through spool, roll, zstd and
    read, to show a single roll.
  - Neither test sleeps, polls or reads the wall clock.
- Fix (#1430, merge 9c380e847):
  - The launcher test now uses `stubPayload`, whose server is a few bytes.
  - The logkeeper test rolls at 64 KB with 110 lines.
  - Each test still fails with its fix removed: `appSupersedes` disabled, or the size roll
    disabled.
  - No budget was raised.
- Timings, `-race -count=20` on Apple Silicon, median: launcher 0.17 s → 0.03 s, logkeeper
  0.37 s → 0.08 s. On #1430's CI, the unit job reported neither test.

## What we learned
- On a CI runner, a race-built Go test binary is tens of MB. A fixture that copies it with
  `layout.CopyFile`, which syncs, costs about 0.2 s per copy. Copy it only into payloads that
  the test actually starts. Elsewhere, write a stub.
- A test that breaks the hard cap even when re-run alone isn't failing because of runner load.
  It's doing too much work.

## Keep doing
- Profile a slow test (`-cpuprofile`, `pprof -top -cum`) before trimming it. Then trim the
  fixture, not the assertion.
