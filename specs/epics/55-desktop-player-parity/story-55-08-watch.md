# Story 55.08 — Watching: resume, tracks, subtitles, quality, Up Next, keys

> Epic 55 · Phase 2 · Depends on: 55.01, 55.02 · Needed by: 55.09, 55.10

## Why

The desktop player starts every video at 0, has no audio or subtitle choice, no quality, no Up
Next and few keys. The Android and Apple players (story 48.32) resume where the person left,
choose audio and subtitles (remembered), cap quality, offer the next episode, and the Mac
Player has a full Playback menu. JavaFX draws no subtitles and plays only an HLS stream's
default audio, so this story also says how the desktop does both (README §6).

## Description

### 1. Shared choices into `:client` (`core.playback`)

From the Android Player, without their Media3 parts: `TrackChoice` and the rule that picks the
audio and subtitle choice from the person's last choice and the playback settings
(`watch/Tracks.kt`), the playback settings' keys and defaults (`PlaybackValues`, from
`profile/PlaybackSettings.kt`), and the Up Next rule (the next episode, the countdown length,
`watch/UpNext.kt`). The Android Player imports them.

### 2. Resume

`PlayRequest(startSec = null)` reads `myVideo(id).playback`: the saved position, unless the video
was completed or the position is over 95%, then 0. Explicit `startSec` wins.

### 3. Audio and subtitles

- `StreamProxy` keeps the session's master playlist and lists its `EXT-X-MEDIA` renditions
  (`TYPE=AUDIO`, `TYPE=SUBTITLES`, with `LANGUAGE` and `NAME`).
- **Audio**: choosing a track makes the proxy serve a master playlist where that rendition is
  the only audio one and `DEFAULT=YES`; the stream reopens at the same position (the session is
  kept). A direct MP4 is reopened as HLS when the chosen track is not its first.
- **Subtitles**: the proxy fetches the chosen subtitle playlist and its WebVTT; a cue parser
  (`watch/Vtt.kt`) gives the cues; the player draws the cue for the current time in a JavaFX
  `Text` node over the `MediaView`, inside the JavaFX scene (Swing would hide a Compose
  overlay). `VideoSurface` takes an optional caption.
- The choice is remembered for the next video (as Android: off stays off; a language carries
  over when the next video has it).
- **Gate**: `subtitles` off hides the Subtitles control from a viewer (existing subtitle files
  still play when the person had them on); an admin sees the control with the off note.

### 4. Quality

A Quality menu: **Auto** and the session's `ladder` ("1080p", "720p"…). A choice reopens the
stream with `openStream(maxBitrateKbps = rung.bitrateKbps)` at the same position; the choice is
kept for the session, and the default comes from the playback settings (55.09).

### 5. Up Next

When an episode ends (or 20 seconds before its end when the series has a next episode), a card
below the picture: the next episode's poster and title, "Up next in {0} s", Play now and
Cancel. At 0 it plays the next episode from its start. Cancel or Escape keeps the ended video.
N plays the next episode at once.

### 6. Controls and keys

Below the picture: play/pause, back and forward 10 s, the seek bar with chapter marks (when
`chapters` is on), times, volume and mute, Subtitles, Audio, Quality, full screen, close.

| Key | Does |
|---|---|
| Space, the media play/pause key | play / pause |
| ← / → | back / forward 10 s (mirrored in Arabic) |
| Shift+← / Shift+→ | back / forward 30 s (mirrored) |
| N | next episode |
| S | subtitles on/off (the last language) |
| M | mute |
| ↑ / ↓ | volume |
| F | full screen (F11 stays big screen) |
| Escape | leave full screen, else close |

### 7. Words

| Key | English | Arabic |
|---|---|---|
| `watch.subtitles` | Subtitles | الترجمة |
| `watch.subtitlesOff` | Off | إيقاف |
| `watch.audio` | Audio | الصوت |
| `watch.quality` | Quality | الجودة |
| `watch.qualityAuto` | Auto | تلقائي |
| `watch.qualityHeight` | {0}p | {0}p |
| `watch.back30` | Back 30 seconds | رجوع 30 ثانية |
| `watch.forward30` | Forward 30 seconds | تقديم 30 ثانية |
| `watch.volume` | Volume | مستوى الصوت |
| `watch.mute` | Mute | كتم الصوت |
| `watch.unmute` | Unmute | إلغاء كتم الصوت |
| `watch.fullScreen` | Full screen | ملء الشاشة |
| `watch.leaveFullScreen` | Leave full screen | الخروج من ملء الشاشة |
| `watch.trackUnknown` | Track {0} | المسار {0} |
| `upnext.title` | Up next | التالي |
| `upnext.in` | Up next in {0} s | التالي خلال {0} ث |
| `upnext.playNow` | Play now | شغّل الآن |
| `upnext.cancel` | Cancel | إلغاء |
| `watch.reopening` | Changing… | جارٍ التغيير… |

## Acceptance criteria

- **Given** a video left at 12:04, **when** it is played from a card, **then** it starts at
  12:04; from "Play from start", at 0.
- **Given** a stream with Arabic and English audio, **when** English is chosen, **then** English
  plays from the same moment, and the next video starts in English when it has it.
- **Given** subtitles in Arabic, **when** they are turned on, **then** the cues show over the
  picture at their times, right to left.
- **Given** the ladder 1080p and 480p, **when** 480p is chosen, **then** the stream reopens
  capped at 480p from the same moment.
- **Given** the last minute of an episode with a next one, **when** the countdown reaches 0,
  **then** the next episode plays; Cancel stops it.
- **Given** Arabic, **when** → is pressed, **then** the video goes back 10 s.
- **Given** `subtitles` off and a viewer, **when** a video plays, **then** there is no Subtitles
  control.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `ResumePosition` with saved, completed, 96% | start | 724, 0, 0 |
| T02 | unit (`:client`) | track rule (moved tests) | settings and last choice | as on Android |
| T03 | unit | `StreamProxy`, a master with two audio groups | choose `en` | one `EXT-X-MEDIA` audio, `DEFAULT=YES`, `en` |
| T04 | unit | `Vtt` parser | cues with styles, overlaps, `X-TIMESTAMP-MAP` | right cue at each time |
| T05 | unit | `UpNextState` with a fake clock | end, count, cancel | next plays at 0 / not after cancel |
| T06 | unit | quality choice | 480p | `max_bitrate_kbps` sent; same position |
| T07 | unit | key map, `ar` | → , Shift+→ | −10 s, −30 s |
| T08 | screen | `subtitles` off, viewer and admin | compose controls | hidden / note |

## Edge cases

- **A stream with no audio renditions** (one muxed track): no Audio menu.
- **A reopen fails** (track or quality change): the old stream keeps playing and a line says
  why, as `Playback` already does for a renewal.
- **The signed URL renews** while a track choice is in force: the renewal keeps it.

## Build plan

| File | Change |
|---|---|
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/playback/Tracks.kt`, `PlaybackValues.kt`, `UpNext.kt` (moved logic) and tests | Shared rules. |
| `apps/android/player/.../watch/Tracks.kt`, `UpNext.kt`, `profile/PlaybackSettings.kt` | Use them. |
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/Playback.kt`, `Watch.kt`, `VideoSurface.kt` | Resume, choices, captions, keys. |
| `.../desktop/watch/Vtt.kt`, `Captions.kt`, `UpNextState.kt`, `TrackMenus.kt` (new) | New parts. |
| `.../desktop/StreamProxy.kt` | Master playlist read and audio rewrite; subtitle fetch. |
| `.../desktop/watch/WatchStrings.kt` | §7 words. |
| `apps/desktop-player/src/test/.../watch/*Test.kt` (new), `StreamProxyTest.kt` | T01, T03–T08. |
