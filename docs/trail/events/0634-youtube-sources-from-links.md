---
schema: 1
kind: pr
pr: 634
title: Each library's YouTube sources come from its linked videos
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge d2f3346cf. Each library's YouTube sources are derived from its linked videos (real playlists that no other library dominates, else its main channel), created off and never changing an existing mode. A channel or playlist already held by another library becomes a duplicate hint instead of a second source.
- It runs after each new link and in the reconciler every 6 hours, whose first pass after the update covers existing links. Every decision is kept in `youtube_source_decisions` (migration 0182 `youtube_sources_from_links`, with `youtube_channel_libraries` as the many-to-many channel map).
- A read-only dry run on live gave the 10 sourceless libraries 7 playlist sources, 1 channel source and 2 duplicate hints.
- It also fixed TestCrashBackoffAndCrashLoop (#645) on main (243f9d678).
