---
schema: 1
kind: pr
pr: 329
title: TestCrashLoopBacksOffThenFails was a real race
date: 2026-10-26
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`TestCrashLoopBacksOffThenFails` was a real race, fixed in #329 (commit e259aba3a): the supervisor's
crash-loop transition set the failed state, unlocked, then emitted the failed event, so a snapshot
could show "failed" with no event saying why. The event is now recorded in the same locked step.
