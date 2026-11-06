---
schema: 1
kind: pr
pr: 628
title: Library dashboard and queue snapshot stop scanning the job history
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge f010aa7b1. On a home with 3.3 million queue jobs, the library dashboard and the queue snapshot rescanned the whole job history on every request and timed out.
- Migration 0183 `queue_read_paths` adds `processing_done_subjects`, a trigger-maintained summary of finished subjects that locks rows in key order, plus partial indexes. A shared stale-while-revalidate cache with one build in flight serves the snapshot and the library rows.
- On 3.15 million seeded jobs both build in about 0.1 s and are served from cache in under 1 ms, and the dashboard shows slow and failed states with Try again instead of loading forever.

## Open
- `processing_done_subjects` lock order across a multi-statement transaction (#642).
