# Story 17.8 — Video player controls design

A minimal, focus-aware control bar with play/pause, scrubber, time,
captions, audio, settings, fullscreen, PiP, AirPlay/Cast.

**Anchors:** [`architecture.md` §6](../../architecture.md). Implements
the visual language consumed by
[Story 11.3](../11-web-ui/story-11-03-video-player.md) and
[Story 12.3](../12-mobile/story-12-03-native-player.md).

## AC

- Auto-hide after 3 s of inactivity; reveal on mouse move, tap, or
  remote D-pad.
- Scrubber shows chapter ticks, sprite preview on hover, current
  position, buffered range.
- Captions button: cycles through tracks; off-state badge shows
  current language.
- Settings menu: speed, quality, audio track, subtitle track,
  subtitle styling.
- Touch targets: 44 × 44 CSS px on touch.
- TV variant: same controls, larger spacing, focus-ring driven.
- Subtitle style controls: size, color, background, font (sans / serif),
  position (bottom / top); persisted per user.
- Mini-player: appears when navigating away from `/watch/{id}`,
  pinnable, dismissable. Implementation choice (persist Vidstack
  instance vs. recreate-with-saved-position) is the open question
  carried over from the original Epic 03 spec — to be settled before
  this story ships.

## TC

- Hover scrubber on web: sprite preview appears within 200 ms.
- D-pad on TV: focus moves predictably across controls.
- Subtitle styling change: live-applied without restarting the video.

## EC

- A video with no chapters: ticks hidden.
- Sprite cache miss: scrubber preview shows the poster instead.
- Caption track upload mid-watch: button updates to include the new
  track; doesn't pause playback.
