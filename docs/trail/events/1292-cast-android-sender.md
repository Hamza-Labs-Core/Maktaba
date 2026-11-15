---
schema: 1
kind: pr
pr: 1292
title: "The Android Player casts to Maktaba's receiver with a grant (story 58.06)"
date: 2026-11-14
session: ff9eaade-bd8f-4224-9087-4fbcea60ab40
agent: claude-code
---

## What happened
- Written after the pull request merged, from its description, commits and diff; the session
  that did the work left no event.
- #1292 merged as 7d796e6fa, stacked on #1286 and after the receiver (#1267). The Player's Cast
  button sends the video, or the whole Play all / Shuffle queue, to Maktaba's receiver. The
  phone mints a cast grant and passes it in the load's custom data; the TV plays and reports
  progress itself, so the phone closes its own stream session and posts nothing while the
  receiver reports. Up next follows the TV's `queue_pos`; when the cast ends, the TV's video
  opens on the phone at the TV's position. A fresh grant goes out on
  `urn:x-cast:tv.maktaba.cast` 30 minutes before the current one ends.
- Gradle reads `receiver_app_id` from `shared/cast/app.json` into `BuildConfig.CAST_APP_ID`
  (`-Pmaktaba.castAppId` overrides it). An empty id turns Cast off completely: no Cast context
  starts and no button shows.
- `CastItems` makes `contentId` the video id and keeps the grant only in `customData`; it never
  copies a URL, header or token, and on the way back keeps only `video_id`, `queue_id` and
  `queue_pos`. `castData()` is tested against `shared/cast/load-vectors.json`.

## What we learned
- Media3's `CastPlayer` takes a `TransferCallback`, which lets `Playback` mint the grant before
  the handover: the receiver stays empty until the grant exists, and a refused grant ends the
  cast while the phone keeps playing with the reason on screen.
- An older home is found with a probe: `POST /api/cast/grants {}` answers 422 (a JSON problem)
  on a home that has the route and a plain 404 on one that doesn't. For the older home,
  `CastContext.setReceiverApplicationId(DEFAULT_MEDIA_RECEIVER)` must run before any session
  starts; that home then casts the plain signed URL to Google's Default Media Receiver and the
  phone reports progress as before. A probe that fails offline is retried at the next cast.
  Apple can't switch app ids after setup, which is why its sender falls back differently
  (#1286, #1287).

## Open
- The receiver's application ID is still empty in `shared/cast/app.json`, so no Android build
  shows Cast until the owner registers the receiver in the Cast console.
- T06 real-device rows in `docs/stores/cast.md`.
