---
schema: 1
kind: migration
title: More flaky tests fixed at their root
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [249, 257, 343, 356, 357, 358, 405, 425]
---

## What happened
More flaky tests were fixed at their cause rather than re-run: #249 (web player and queue tests),
#257 (Python per-test soft cap on Windows and under coverage), #343 (updater retries a just-written
binary Linux says is busy), #356, #357, #358 (front door, channel lag step, devmigrate port), #405
and #425 (streaming tests wait for a transcode's end).
