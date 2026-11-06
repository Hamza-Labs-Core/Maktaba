---
schema: 1
kind: pr
pr: 467
title: "48.6: MaktabaKit's browse client"
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
MaktabaKit `Browse.swift`, the browse client from Home to Profile. Not Live (48.7), nor the player's session options, which stay `openStream` until 48.23 needs more.
Fixtures are real answers: the branch API ran on a scratch Postgres (port 55486, a viewer of one library of two, seeded series, chapters, transcript, playlist, search), recorded by a script as that viewer into `MaktabaKit/Tests/Fixtures/Browse/`.

## What we learned
- The fixtures sit outside the test target and are read by `#filePath`, so `Package.swift` (owned by 48.10) stayed untouched.
- A Go test (`browse/native_fixtures_apple_test.go`) decodes the fixtures into the handlers' types with unknown fields refused, so renaming a field fails in the PR that renames it.
- `send` and `sendRaw` in `APIClient.swift` lost `private` (an extension in another file needs them). 48.7 made the same two-word change, which git merges cleanly.
- New model names avoid the apps' views (`BrowseVideo`, `VideoInfo`, `VideoListPage`): the Player has a `VideoDetail` view, tvOS has `VideoCard` and `VideoPage`.
- A hidden video answers 403, not 404 (`videos.canRead`), against FR-18's premise. The client maps a video route's 403 to `.notAvailable` too.
- `GET /api/series/{id}` answers its counts as 0 (only the list counts them): a series page must count its episodes.
- Failed first: `initdb` without `-E UTF8` made a SQL_ASCII database, and the facets route answered 500 on Arabic first letters. Recreated the DB as UTF8; not an API bug.

## Keep doing
- Create scratch Postgres databases with `initdb -E UTF8`.
