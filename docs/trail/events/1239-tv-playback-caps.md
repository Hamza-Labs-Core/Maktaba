---
schema: 1
kind: pr
pr: 1239
title: "Playback that fits each TV: device_caps, TV profiles, remux in fMP4, Dolby Vision base layer (story 58.02)"
date: 2026-11-14
session: 6fbde8bc-c0fd-4263-a31c-859d7547e437
agent: claude-code
---

## What happened
- #1239 merged as 92a30b817. A stream session takes an optional `device_caps` report, merged
  into six new TV profiles (`firetv`, `tizen`, `webos`, `roku`, `cast`, `vega`): each a baseline
  (H.264 1080p, TS) plus a platform ceiling the report can widen up to and never past.
- The TV profiles' remux verdict is now served (before, every remux became a transcode): video
  copied into HLS (fMP4 for HEVC/AV1, TS for H.264) or DASH (Roku AV1), Dolby Vision 8/7 sent
  as the HDR10/HLG base layer, TrueHD/DTS as EAC3, a stereo AAC rendition beside Dolby audio on
  Roku. Each session carries `stream_sig` for its subtitle and chapter paths.
- `shared/playback/caps-vectors.json` holds 13 recorded device reports with the home's verdict
  per source; the streaming and API tests read it, and each TV app's reader is meant to.

## What we learned
- The probe never set HDR, so the matrix saw every file as SDR; HDR now comes from the transfer
  (PQ → hdr10, HLG → hlg) and the Dolby Vision record from `side_data_list`. The older profiles'
  `Decide` does not read HDR, so they did not change.
- `dovi_rpu=strip=1` alone keeps an RPU it cannot parse; `filter_units=remove_types=62|63`
  removes RPU and EL on every ffmpeg the home runs (4.4 on), with `dovi_rpu` chained after it
  where present (7.1 on) to drop the configuration record.
- fMP4 HLS segments start at a timestamp base of 0, not TS's 126000 (1.4 s): a WebVTT
  X-TIMESTAMP-MAP must follow the session's segment format or subtitles drift.
- `omitempty` on a report's lists turns the device's "none" (`[]`) into "left out" (the
  profile's value) on a re-marshal; the lists carry no omitempty, null is "left out".
- A process that takes no transcode slot still needs a bound: a remux (each an ffmpeg) now has
  its own cap (MaxRemux, default 16), and a user keeps at most two sessions of a video open for
  different device reports; a third closes the oldest as `superseded`. A security review of the
  first push found the gap: a client changing its report on each open stacked processes.
  A second review found the cap itself raced (a row lands only after its ffmpeg starts, so ten
  concurrent opens passed a cap of two): a place is now reserved under a lock before ffmpeg
  starts. A cap counted from rows inserted after the spawn is no cap.
- `-master_pl_name` in a remux overwrites the orchestrator's master playlist (CODECS,
  VIDEO-RANGE); leave it off.

## Keep doing
- Prove each new real-ffmpeg test on ffmpeg 4.4 (an ubuntu:22.04 container), the version
  media-e2e and the .deb use, as well as on the Mac's newer ffmpeg.
- Keep a shared vectors file as the contract between the home and every app reader.

## Stop doing
- Treating a verdict mode as served without a runtime path behind it: the remux verdict existed
  for a long time with nothing that served it.

## Open
- DV profile 5, or HDR a device does not show, gets a tone-mapped SDR H.264 transcode (the
  ladder is H.264 only); Roku without DASH gets AV1 as H.264.
- The older profiles name HEVC `h265` while the probe says `hevc`, so they never match HEVC;
  the API's realclient drops `accept_queue`. Both predate this story.
- The TV apps' capability readers (58.05, 58.07, 58.11, 58.13, 58.18) are to be tested against
  `shared/playback/caps-vectors.json`.
