---
schema: 1
kind: pr
pr: 1615
title: "No YouTube channel or Plex collection badge on a collection, and no Plex collection kind"
date: 2026-11-24
session: fe0671de-b191-4f28-8ce0-c1efde306088
agent: claude-code
---

## What happened
A product decision of 2026-11-24 removed both collection source badges ("YouTube channel" and "Plex collection") from every app. PR #1615 does that across the Apple Player and Apple TV, Android and Android TV, the web app and the big screen, with their English and Arabic strings. The Windows and Linux Player, the Remote and Roku never had these badges. A YouTube playlist keeps its badge.

Collections have not been read in from Plex since #969, so the browse API's `plex` kind went too: `KindPlex`, its filter and the error text. The Apple TV and Android TV "From Plex" kind went with it. The API stays compatible: `?kind=plex` answers 200 with an empty list and `counts.plex = 0`. A leftover `source_kind='plex'` row lists as curated with no `source_kind`, and `GET /api/collections/{id}` treats it as an ordinary, editable collection. The Plex Live TV plugin is untouched.

## What we learned
- Not every client had the badges: grep `youtube_channel`, `collections.badge.*` and the Arabic strings before listing an "every app" change.
- The web's collection card fell back to the "Automatic" badge whenever there was no source badge. Dropping a source badge has to keep that fallback from taking its place (`!c.source_kind && c.automatic`).
- `shared/api/openapi.ar.json` is checked pointer by pointer (`checkOverlay` in the devportal's `parity.mjs`). A new parameter description needs its Arabic entry under that parameter's index.
- `affected-tests` failed on the first push: `groups.go` changed but none of the browse package's own tests did, so the router PG test was not enough. `TestLibraryCollectionsNoPlexKind` (SQLite, in `groups_test.go`) covers it.
- `trail check` from agent-trail v0.2.0 ran for over 14 minutes on the Mac before it was stopped; v0.2.1 finished in seconds (d20261122-trail-check-git-reads). Run v0.2.1 locally.
- The Apple TV shot on maktaba.dev (`appletv-library-collections-ar.webp`) is CI's `tv-library-collections-ar` from the `tv-ui-tests` artifact. It is refreshed from CI, never from a local UI test run.

## Keep doing
- Keep an old query value answering an empty list rather than a 400 when a kind is retired, and say so in the OpenAPI parameter.

## Open
- The merge waits for the freeze to lift (a tag with #1592).
- The new Apple TV reference image comes from #1615's CI run and is listed for the owner's visual check.
- `pipeline/src/maktaba_pipeline/engine/sourced_collections.py` still knows a `plex` source kind; nothing calls it with one.
