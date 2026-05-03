# Story 12.3 — Native video player integration

A Capacitor plugin (`plugins/native-player/`) that opens the system
AVPlayer (iOS) or ExoPlayer (Android) for full-screen playback. The web
player is used for inline (non-fullscreen) playback; tapping fullscreen
hands off to native for AirPlay / Cast / PiP.

**Anchors:** [`architecture.md` §4.1](../../architecture.md), §6.3.
Depends on Epic 7 Story 7.10 (sessions), Epic 8 Story 8.3 (direct play),
Epic 10 Story 10.8 (signed-URL minter).

## AC

- Plugin API: `nativePlayer.open({manifestUrl | directUrl, posterUrl,
  title, startSec, audioTrack?, subtitleTrack?})` → returns a session
  handle.
- Native shells call `POST /api/stream/sessions` first (per
  [Story 11.3](../11-web-ui/story-11-03-video-player.md) handshake) and
  pass the resulting `direct_url` (mode `direct`) or `manifest_url`
  (modes `remux`, `transcode`).
- iOS uses `AVPlayerViewController` with `AVPlayerItem`; Android uses
  `ExoPlayer` with `MediaItem`.
- Now Playing metadata: title, poster, duration published via
  `MPNowPlayingInfoCenter` (iOS) and `MediaSession` (Android), so lock
  screen and AirPods controls work.
- Subtitle track switching is exposed in the native player UI; selection
  echoes back to the web layer via plugin event.
- Closing native player returns the user to the in-app detail page with
  the latest position synced.
- Position sync uses `POST /api/stream/sessions/{id}/progress` every
  10 s; no monotonicity check (consistent with
  [REVIEW §1.5.a](../../REVIEW.md) resolution).

## TC

- Tap fullscreen on iPhone, swipe to AirPlay: stream continues on Apple
  TV; play/pause from the iPhone control center works.
- Lock the device mid-playback: the lock screen shows poster + scrubber.
- Cast from Pixel to a Chromecast: ExoPlayer supports the
  cast-discover-session flow; switching back to the device resumes
  position.
- Position sync: native player reports every 10 s to
  `/api/stream/sessions/{id}/progress`.

## EC

- HLS source's audio language list disagrees with the manifest selection:
  prefer the manifest, log a warning.
- AirPlay receiver can't decode HEVC sidecar: the API generates a
  compatibility transcode; we never attempt to push an unsupported codec.
- Background audio mode (audio continues after lock): see
  [Story 12.5](story-12-05-background-playback.md).
- User force-closes the app mid-stream: server-side session reaper
  ([architecture.md §4.2](../../architecture.md)) closes the slot within
  90 s.
