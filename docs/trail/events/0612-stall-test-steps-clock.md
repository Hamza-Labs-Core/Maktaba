---
schema: 1
kind: pr
pr: 612
title: Writing-encoder stall test steps the watcher's clock itself
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 30c420126. TestExecRunner_WritingEncoderRunsOn failed on #604's integration job. Its fake encoder, a shell loop starting `date` and `sleep` every 50 ms, got no CPU time for longer than the 400 ms stall limit on a busy runner, and the watcher correctly stopped it.
- The product code was right and the test raced the scheduler. Running it at background priority under CPU load reproduced it (10 of 40 failed); the missing ffprobe made no difference.
- The stall watcher takes its ticker and start time from test hooks and judges each check by its tick's time. The test steps that clock itself and writes files with their times already set, so it checks both sides exactly with no real waiting, and moved to the unit tier.

## What we learned
- Running a timing test at background priority under CPU load is a good way to reproduce a scheduler race locally.
