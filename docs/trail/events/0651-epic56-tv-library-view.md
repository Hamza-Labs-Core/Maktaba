---
schema: 1
kind: decision
pr: 651
title: Epic 56 spec: the TV library view
date: 2026-11-05
session: unknown
source: claude-trail.md
---

## What happened
- Merge 3d7507d30. Android TV and Apple TV get Plex's five library tabs (Recommended, Library, Collections, Playlists, Categories), Play all and Shuffle on a library, series, season, collection and playlist, series grouping with series and season pages, sort, filter, letter jump and Up next.
- Everything plays through one play queue on the server (56.01), which shuffles by a seeded hash so a whole library can be shuffled, can be continued on another device, and replaces the web's per-tab queue.
- New automatic collections come from YouTube playlists, YouTube channels and Plex collections; collections stay admin-made and viewers get smart playlists.
- Slots 0184-0186 are planned and taken at merge time (Epic 52 also names 0184). 14 stories, each with English and Arabic strings and tests on both TV platforms.
- Rejected: a queue kept in each app (it cannot shuffle past one page) and user-made collections (they would duplicate playlists). Importing Plex playlists was left out because they belong to Plex users who may not exist in Maktaba.

## Open
- Migration slot 0184 is named by both Epic 52 and Epic 56; slots are taken at merge time.
