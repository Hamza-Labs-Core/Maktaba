---
schema: 1
kind: pr
pr: 1269
title: "The older playback profiles read ffprobe's codec names and weigh HDR"
date: 2026-11-14
session: 8a5fe67b-8f90-49d7-b654-5f67ba42dc6f
agent: claude-code
---

## What happened
- #1269 merged as ac8611c7b. While building 58.02 (#1239), two playback bugs were found. Each was
  checked against main with a failing test first.
- HEVC naming was real. The older profiles in `streaming/internal/capability/matrix.go` spelled
  HEVC `h265`, the probe stores ffprobe's `hevc`, and the older `Decide` compared the two byte for
  byte, so no older profile ever matched an HEVC file. `Decide` now reads codecs through
  `canonVideo`/`canonAudio`, as `decideDetailed` and `Apply` already did.
- Because HEVC now matches, the older path also weighs HDR (`showsPicture`): HDR or Dolby Vision
  a profile does not show is transcoded. A Dolby Vision source plays as its base layer where the
  profile shows that base. Before this, HDR10 AV1 direct-played to `browser-chrome`.
- Each profile's HEVC claim was checked against its platform. `android-native` and `androidtv`
  keep HEVC. `browser-chrome` dropped it (Chrome decodes it only on some hardware). `browser-safari`,
  `ios-native` and `tvos` dropped it: Safari and AVPlayer refuse an MP4 whose HEVC is tagged
  `hev1`, ffmpeg's default, and the probe does not record the tag.
- `accept_queue` being dropped by the API was real, but it was left unforwarded on purpose. The
  streaming service would mark the session `queued`, and nothing promotes it
  (`slots.PromoteFromQueue` has no caller). Its queue count is never given back, and the API's
  answer has no `state`, so the client would get a manifest that never comes. A note at the
  field explains why.

## What we learned
- No shipping app sends a `client_profile` except the desktop Player (`desktop-player`). Web,
  Apple and Android get `generic`. The older profiles matter only to API callers that name one,
  so the playback guide's table of which app uses which profile is aspirational.
- The older tests passed `VideoCodec: "h265"` as the source, a name the probe never stores. That
  hid the bug. Test sources should use ffprobe's names.
- The `affected-tests` gate counts a comment-only change to a source file. A `No-Test-Change:`
  trailer clears it.

## Keep doing
- Check whether a "forward the flag" fix would make things worse: follow the flag to what
  consumes it before wiring it through.

## Stop doing
- Writing capability test sources with a profile's spelling instead of the probe's.

## Open
- A real transcode queue: promotion when a slot frees, a session store update, `state` and
  `queue` in the API's answer, and clients polling. Until then a full home refuses with 503.
- The probe could record the HEVC sample tag (`hvc1`/`hev1`). The Apple profiles could then
  direct-play `hvc1` HEVC in MP4.
