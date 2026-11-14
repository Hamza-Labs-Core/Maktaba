---
schema: 1
kind: pr
pr: 1239
title: "Epic 58 story 58.02: playback that fits each TV (device caps, TV profiles, remux)"
date: 2026-11-14
session: 0c3e16d1-4344-4763-a698-1cb3e76a4e60
agent: claude-code
---

## What happened
- #1239 merged as 92a30b817. `POST /api/stream/sessions` takes an optional `device_caps`
  (containers, codecs with height/fps/bit depth, HDR, audio, passthrough, HLS segment formats,
  DASH, bitrate). Unknown keys are ignored; wrong types or lists over 32 are a 400; an empty list
  means the device has none.
- Six TV profiles (`firetv`, `tizen`, `webos`, `roku`, `cast`, `vega`): a baseline (H.264
  1080p, TS) plus a platform ceiling. A report narrows freely and widens only up to the ceiling;
  the rest is logged as `clamped`.
- For TV profiles the remux is served, not turned into a transcode: video copied, HEVC/AV1 in
  fMP4, Dolby Vision 8/7 sent as its HDR10/HLG base layer, TrueHD/DTS to EAC3 where the TV takes
  it, a stereo AAC rendition beside Dolby audio on Roku, AV1 on Roku by DASH. The probe now
  reads the DV record and the frame rate, and HDR comes from the transfer.
- Every session carries `stream_sig`, the token for its subtitle and chapter paths, for TV
  players that send no cookie or header.
- `shared/playback/caps-vectors.json` holds 13 recorded device reports with the home's verdict
  for 6 sources; the streaming and API tests and every TV app's reader test against it.
- maktaba.dev gained the guide *Playback: profiles, device reports and signed URLs* (en/ar).
- Fixes during review: remuxes got their own cap (`MaxRemux`, default 16), and one user keeps
  at most two fits of one video open (a third closes the oldest); a remux place is reserved
  under a lock before ffmpeg starts.

## What we learned
- A cap that counts open sessions misses opens in flight: the session row lands only after
  ffmpeg starts, so 10 concurrent opens passed a cap of 2. The place must be reserved under a
  lock between the check and the insert, and a store that cannot count must refuse.
- A remux costs no transcode slot, so without its own cap a client changing `device_caps` on
  each open could start an ffmpeg each time.
- fMP4 sessions start subtitles at timestamp base 0, not TS's 126000; the subtitle renditions
  must follow the session's base or they drift.
- The colour matrix says nothing about HDR: deriving HDR from it saw every file as SDR.
- Adding a field to a protected message (`Session.stream_sig`) needs the API levels snapshot
  updated in the same PR.

## Keep doing
- One shared vectors file that the home and every client test against.
- Keep the older profiles' decisions unchanged when a new path is added beside them.

## Open
- DV profile 5, or HDR a device does not show, gets a tone-mapped SDR H.264 transcode: the
  transcode ladder is H.264 only. Roku without DASH gets AV1 transcoded to H.264, not HEVC.
- ffmpeg below 7.1 keeps the DV configuration record in the output.
- Pre-existing: the older profiles name HEVC `h265` while the probe says `hevc`, so they never
  match HEVC; the API's realclient drops `accept_queue`.
- This event was written after the merge by a separate session; no session event existed.
