---
schema: 1
kind: pr
pr: 1302
title: "PairingTest keeps its request log in a copy-on-write list"
date: 2026-11-14
session: 6f2ebca8-d5af-45b2-b452-999d8bc1a098
agent: claude-code
---

## What happened
- #1302 merged as c1536d3c7, test-only. It was opened on the belief that #1268's merged head
  (47f7d3376, which includes #1276) still hit `ConcurrentModificationException` in `PairingTest`.
  The job logs say otherwise: the only failure was `desktop-player / Desktop Player (windows)`,
  run 43578498213 attempt 1, head befd2e1ca, at `PairingTest.kt:276`
  (`theSessionSignsInWithACode`). That head predates #1276, so it was the race #1276 already fixed.
  The 47f7d3376 runs never failed it, and main had no other unlocked iteration of the log.
- To keep the bug class from coming back, the request log is now a `CopyOnWriteArrayList`
  (any read may iterate while the server thread appends), and the `route` lambda the server
  thread reads is `@Volatile` (tests swap it mid-test). 20 of 20 local reruns of the 15 tests
  passed.

## What we learned
- Before fixing a flake reported against a PR, find the failing job and its head sha:
  `gh run list --branch B` lists every run, and a failure on an earlier head can be one a later
  merged fix already covers. Run every attempt (`gh run view ID --attempt N --json jobs`) and
  grep each failed job's log (`gh api repos/O/R/actions/jobs/J/logs`).
- The Android client unit tests run in two places: `android-apps` (Linux) and the Desktop Player
  job (`:client:test` from `apps/desktop-player`, including Windows). Windows timing is where
  test-thread races show up.
- For a request log written by mockwebserver's dispatcher thread, use a copy-on-write list
  rather than a synchronized one. Then no read needs a lock, and a later edit cannot bring
  the race back.

## Keep doing
- Trust the job log over the summary; check `git merge-base --is-ancestor FIX HEAD` before
  assuming a fix was in the failing build.
