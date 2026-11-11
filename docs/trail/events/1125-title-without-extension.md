---
schema: 1
kind: pr
pr: 1125
title: "fix(titles): a video with no title shows its file name without the extension"
date: 2026-11-11
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
---

## What happened
- The owner saw library items named like `My Lecture 03.mp4`: a video with no metadata title
  showed its raw file name, extension included.
- The fallback `COALESCE(title, filename)` was copied into about twenty API queries (search,
  series, collections, queues, context cards, channels, analytics, GraphQL rows) and into
  `|| filename` fallbacks in the web app, the big-screen app, Android, Android TV, Apple and tvOS.
- #1125 (merged at 6a54baea7) puts the rule in one place per layer: `api/internal/videotitle`
  (a Go helper plus one SQL expression that runs on both Postgres and SQLite, tested on both),
  `web/src/lib/videoTitle.ts`, a `VideoTitle` helper in the Android `:client` module and in
  MaktabaKit. Only the last extension goes (`a.b.mkv` shows `a.b`); `notes`, `.mp4` and `x.` stay;
  a real metadata title is never shortened; stored data and search on the file name are unchanged.
  File details, paths and duplicate paths still show the full file name.
- The first push failed `affected-tests`: 23 changed files had no changed tests. The second push
  added tests on each user-facing surface (series, Up next, collections, playlists, the
  continue queue, the big-screen screens, GraphQL, the Android play queue, the grouped grid,
  whose test was first checked to fail on the old expression) and a `No-Test-Change` trailer
  naming only the admin queries that just switch to the shared, tested expression.
- `apple-apps / Remote UI tests` then failed with all 7 tests passed: the log artifact upload
  timed out (`Failed to CreateArtifact: ETIMEDOUT`). Re-running the failed job once passed.

## What we learned
- A display fallback copied into every query and client becomes a bug in every place at once;
  a fallback that users see belongs in one helper per layer.
- A test written to pin a fix has to be seen failing on the old code; the first grouped-grid
  test passed on the bug.
- A red job is not a failed test until its log says so: read the failing step before fixing.

## Keep doing
- Route every title-or-filename fallback through `videotitle` / `videoTitle` / `VideoTitle`.
- Confirm the platform jobs actually ran (not skipped by path filters) before merging.

## Stop doing
- Writing `COALESCE(v.title, v.filename)` or `title || filename` for a name that users see.

## Open
- Nothing.
