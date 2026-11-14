---
schema: 1
kind: pr
pr: 1313
title: "Epic 58 story 58.13: the Roku player, certification and release"
date: 2026-11-14
session: 0c3e16d1-4344-4763-a698-1cb3e76a4e60
agent: claude-code
---

## What happened
- #1313 merged as b8e43a51b, on top of 58.12's channel (#1307).
- Each stream session sends `client_profile: roku` and this Roku's `device_caps` from
  roDeviceInfo (`source/caps.brs`); the Video node's `streamFormat` comes from the session
  (hls, dash, or the direct file's mkv or mp4).
- Maktaba's overlay replaces the Video node's bar: title, series, chapter, position, a
  trickplay thumbnail while seeking (mirrored in Arabic), and `ChoiceDialog` pickers for
  Audio and Subtitles.
- Subtitles: the sidecar WebVTT signed with `stream_sig`. Arabic is drawn by the channel's own
  Label through VisualText (58.15) with Roku's renderer off; other languages use Roku's
  renderer and its caption settings.
- Progress every 10 s and on pause, seek, end and Back; the session closes on Back. Play all
  and Shuffle walk the server queue, with Up next 15 s before the end and a stale queue (409)
  followed. A Video error retries once with `force_transcode`; a second shows the home's reason.
- `roku-release.yml`: on a `v*` tag whose channel changed, it tests, builds
  `maktaba-roku-<version>.zip` (`build_version` = minor × 1000 + patch) and attaches it with its
  `.sha256`. It reads no secret. `docs/stores/roku.md` gained the listing (en/ar), the
  certification table, deep-link sample IDs, Automated Channel Testing (T05) and real-device
  rows 19–29 (T06).
- No home-side change: 58.02 already serves the `roku` profile (T04 is its tests).
- Tests: `CapsFromAnswers` on three models equals the vectors (T01), subtitle choice and an
  Arabic two-line cue (T02), routing per media type (T03), `tests/player_test.brs` (71 checks),
  and screen tests with eight new dark references in en and ar.

## What we learned
- The devportal Apps pages take only `<name>-en.webp` and `<name>-ar.webp`; a shot with no UI
  text besides an Arabic cue still needs both names (the same image).
- brs-engine has no playing Video node: real playback, HDR, passthrough and Roku's timing can
  only be real-device rows.
- A story that a previous story already served (the home's `roku` profile) should say so and
  point at the tests, not add code.

## Keep doing
- Build release packages in a workflow that reads no secret and leave signing on the keyed
  device as an owner step.

## Open
- Owner: signing on the keyed Roku, the beta channel, the dashboard and the submission
  (`docs/stores/roku.md`); real-device rows 19–29.
- The player's own parental answers (time's up, blocked, on a refused session or on progress)
  are unowned: #1313 left them to 58.14, and #1331 (58.14, merged as 930f7aabf) left the
  player's display to 58.13. Neither PR built it.
- This event was written after the merge by a separate session; no session event existed.
