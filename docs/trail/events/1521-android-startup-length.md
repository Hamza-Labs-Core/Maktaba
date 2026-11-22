---
schema: 1
kind: pr
pr: 1521
title: "The Android Player plays files as they are, and shows a transcode's real length"
date: 2026-11-22
session: 616b806a-5410-419d-91f2-6705103db238
agent: claude-code
---

## What happened
- Two Android Player faults were reported: a long wait between play and the first frame, and a
  seek bar and length that covered only what had been converted so far.
- **Cause of the slow start.** `Playback.open` never sent `client_profile` or `device_caps`,
  and never had (`git log -S android-native -- apps/android` is empty). The home then used
  `generic`: H.264 and AAC in MP4 or TS, 720p, 4 Mbit/s and stereo at most. So nearly every
  film was transcoded, and the first frame waited for ffmpeg's first 6-second segment of every
  rung (1080p, 720p and 480p in one process).
  - Measured with this branch's `HLSArgs` and real ffmpeg on a 120 s 1080p H.264 / AC-3 5.1 MKV,
    on an M3 Ultra: 0.86–0.89 s to the first playable playlist on all cores, 1.34–1.39 s on 4
    threads and 2.55–2.63 s on one. A home NAS is several times slower.
- **Cause of the short length.** The transcode's rendition is an EVENT playlist with no
  `#EXT-X-ENDLIST` until the transcode is done. ExoPlayer reads it as live, so its duration is
  only what is written, and `Watch.kt` preferred that duration over the catalogue's. Three
  seconds after the first segment the playlist listed 12 s of 120 on one thread, 60 s on all
  cores.
- **The fix (#1521):**
  - The Player opens with `android-native` and the device's report. `DeviceCapsReader` moved
    from the TV app to `core`.
  - `android-native` gained a ceiling, so a phone's report adds the Dolby, FLAC, Vorbis and DTS
    audio its own decoders take.
  - A direct file that fails to decode opens once as `force_transcode`.
  - While a playlist grows, the length is `duration_sec`, a seek outside what is written opens
    the stream there, and the speed stays at 1×.
  - After the fix that MKV plays directly on a phone that decodes AC-3. Against the fake home,
    play to playing takes about 0.1 s, with no playlist requested.
- **An independent review of a7253fcd9 found more problems, all fixed in #1521:**
  - A seek's or the fallback's reopen was not stopped with the video. One that finished after
    the person left built a hidden player playing the old video.
  - The fallback bypassed the busy-slot retry, and the 10-second watch could take the first
    file's error for the new stream's.
  - An older home's Cast received the phone's direct URL, which may be a file only the phone
    plays.
  - Non-Detailed profiles ignored a report's per-codec height, bit depth and frame rate.
  - A forced transcode could be handed the user's direct-play session from another app.
  - A +10 s skip near the written end restarted the transcode.
  - The fallback test's second session was still direct play.

## What we learned
- A client that omits `client_profile` silently gets `generic`, the narrowest profile, with no
  error and no log line saying so. Check every client's open body when playback is slow.
- Media3 under Robolectric never calls a live (dynamic) HLS window READY with the stand-in
  renderers. A test of a growing playlist should wait for the timeline (dynamic, duration
  known), not for READY.
- A fake EVENT playlist that does not grow is "stuck" to ExoPlayer within 3.5 target
  durations of the fake clock (`PlaylistStuckException`). The fake must add a segment at each
  read.

## Keep doing / stop doing
- Keep: measure a startup path with the server's exact ffmpeg arguments (print `HLSArgs` from a
  test) before guessing at buffer settings.
- Stop: reading `Player.duration` as a video's length when the window is dynamic.
- Keep: every reopen in `Playback` goes through one job that `stop()` cancels, and `open()`
  checks the video is still the one it opened before it builds a player.

## Open
- The TV app's player and the media notification still show a growing transcode's partial
  duration. Web (hls.js) and Apple (AVPlayer) read the same EVENT playlist the same way. A
  server-side fix (a full VOD playlist up front, with the transcode restarted at the requested
  segment) would cover every client.
- The older profiles have no audio-only remux: DTS or TrueHD on a phone without that decoder
  still transcodes the video as well.
