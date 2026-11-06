---
schema: 1
kind: decision
title: Issues filed for problems that did not fail CI
date: 2026-11-04
session: unknown
source: claude-trail.md
prs: [634]
---

## What happened
- Issues were filed for problems that neither failed CI nor blocked a release. #645 (TestCrashBackoffAndCrashLoop) was closed once #634 fixed it on main.

## Open
- #636: a restart for loop-only components.
- #637: the pipeline picking up a new Arabic dictionary or ffmpeg pack without a restart.
- #638: a Desktop Player build switch for the capabilities gate.
- #639: the simulator tap race.
- #640: outbound calls from test stacks.
- #641: a whole-server components-live scenario.
- #642: `processing_done_subjects` lock order across a multi-statement transaction.
- #643: the MANIFEST gaps at migration slots 0176 and 0177.
- #644: a streaming test that fails on a test database URL without `sslmode=disable`.
- #646: one unexplained local components-live failure with no output kept.
- #647: finishing the early-exit pipe sweep (a `head` in testflight.yml and `grep -q`).
- #649: Player UI shards left in progress after their run completed.
