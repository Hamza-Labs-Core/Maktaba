---
schema: 1
kind: pr
pr: 621
title: Library dashboard and a page for each library
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 8648eea4b. The Queues page's libraries table (42,565 px tall for 65 libraries, sideways scroll at 375 px) is replaced by a library dashboard (`/queue/libraries`) with one tile per library and an admin page for each library (`/admin/libraries/:id`); the Queues page keeps a short list of libraries needing attention.
- New admin-only aggregate `GET /api/queues/libraries/activity`: each library's finished and failed jobs per day, cached for a minute.
- The progress ring always uses the done colour, so its percentage reads as work done; failures show only as the red line and the tile's edge.
- Running the branch API with the default HOME swept expired files from the live `~/.maktaba/cluster` (the same 14-day rule the live hub applies hourly, so nothing it would keep was lost).

## Keep doing
- Branch stacks point HOME, `MAKTABA_CLUSTER_DIR` and `MAKTABA_ARCHIVE_DIR` at scratch folders.
