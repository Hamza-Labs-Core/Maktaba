---
schema: 1
kind: pr
pr: 667
title: Epic 55, the desktop Player catches up with the Android and Apple Players
date: 2026-11-06
session: c25d87b8-12e9-4665-bc89-678b2d71b714
issue: 638
---

## What happened
All ten Epic 55 stories in one PR: the desktop Player (Compose for Desktop) has the five sections,
the capabilities gate with its build switch (#638), Home, Search, Libraries, series and video
pages, Live TV, watching with tracks, subtitles, quality and Up next, and Profile. ReadOrder, the
Home row builder and cache, the guide's time maths and the playback rules moved into `:client`,
one commit each; the Android Player imports them unchanged in behaviour.

## What we learned
- The home's search modes are `fts`, `semantic` and `hybrid`; any other mode (the story's
  `keyword`) silently becomes `hybrid`. Words-only search sends `fts`.
- Compose for Desktop's `runComposeUiTest` runs headless on macOS and in CI with no display, so
  every desktop screen is testable off screen; its `waitUntil` takes `timeoutMillis =` by name.
- A `Playback` method named `setVolume` clashes with the setter of a `var volume` (same JVM
  signature): name the method something else.
- The stream session API already takes `audio_track` and `format`; `:client` only lacked them.
- `--tests` on the desktop build must target `:test`, or `:client:test` fails with no matching tests.

## Keep doing
- Hold a read open with a latch in the mock home, make the change, then release: one pattern
  covers every ReadOrder test.

## Open
- The desktop Player was verified by unit and off-screen screen tests only, not against a live
  home with a real JavaFX window.
