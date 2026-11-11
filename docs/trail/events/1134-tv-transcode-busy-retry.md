---
schema: 1
kind: pr
pr: 1134
title: "fix(tv): Android TV asks again while every transcoder is busy (59.05)"
date: 2026-11-10
session: 8a8baae1-1408-434b-b1fc-4b955b55d145
---

## What happened
- Story 59.05's #1111 made the phone, tablet, Apple and desktop players wait with a sentence and
  ask again on 503 `transcode_busy`; the Android TV player (`apps/tv/android`, its own
  `TvPlayback`, not the phone Player's `Playback`) stopped on its error screen.
- #1134 (merged at f241461d3) keeps a `transcode_busy` failure as `TvPlayback.waiting` (no error
  screen), shows #1111's `error_transcode_busy` sentence (en, ar) under Preparing, and asks again
  after the home's `Retry-After`. `busyRetryMs` and `BUSY_RETRY_SEC` moved from the phone
  Player into the shared client (`HomeError.kt`), so both apps wait the same.
- Tests: `TvPlaybackBusyTest` (Robolectric, MockWebServer home, the main looper's clock moved
  by hand), `BusyRetryTest` in the client, and the phone `PlaybackTest` asserting the shared wait.

## What we learned
- The Android TV app is a separate Gradle root (`apps/tv/android`) that uses `:core`/`:client`
  but not `:player`; a fix in the phone Player never reaches it. Shared behaviour belongs in
  `:client`.
- `affected-tests` asks for a changed test beside every changed source file, one file at a time:
  moving a helper between modules needs a test changed in both (two red runs here).

## Keep doing
- When a fix lands on the phone Player, check `apps/tv/android` for its own copy of the same flow.

## Stop doing
- Moving code between Android modules without touching a test in each module it leaves and
  enters.

## Open
- None.
