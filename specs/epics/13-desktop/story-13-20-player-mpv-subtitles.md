# Story 13.20 — Subtitles through mpv: a file's own tracks, size and colour

> Epic 13 · Build · Follows 13.18 (PR #1591) · Depends on: 13.18 (the Player plays with
> libmpv) · Size: M

## Why

Story 13.18 draws the Player's captions with mpv, as an ASS event on mpv's OSD
(`MpvVideo.captionAss`), but only the subtitles the Player already knew: the renditions of an
HLS stream's master playlist. A file played directly offers no subtitles at all
(`Playback.followTracks` fills `subtitleOptions` only for HLS), and the file's own tracks stay
off (`sid=no`). An MKV with Arabic SRT and a Blu-ray rip with PGS tracks both play with no
Subtitles menu.

The Player's subtitle size (`SubtitleSize` in the shared `PlaybackValues`) is not applied on
the watch screen either (`Watch.kt` gives `VideoSurface` no `captionScale`), the desktop
Profile → Playback page has no size choice, and no Player has a colour choice.

## Description

**The Subtitles menu, in direct play.** Two sources, in this order:
- the home's tracks, from `GET /api/videos/{id}/subtitles` (sources `external`: a sidecar file
  beside the video; `generated`: Maktaba's transcription), both served at the session's
  `subs/sidecar/{lang}.vtt` and fetched through the stream proxy with the session's token, as
  the HLS renditions are; entries that resolve to the same address are one, and a row with no
  language is left out;
- the file's own, from mpv's `track-list` after `file-loaded` (entries of type `sub`): text
  (`subrip`, `ass`, `webvtt`, `mov_text`) and picture (`hdmv_pgs_subtitle`, `dvd_subtitle`,
  `dvb_subtitle`). Each is named by `Texts.trackName` (its title, else its language, else
  "Track n"), with **In the file** after it, and **Picture** for a picture track.

The home's `embedded` rows are left out in direct play: mpv's track list is the same tracks,
read from the file itself. Forced tracks (`forced` in the track list) are not offered as a
choice: mpv shows one with the audio of its language when subtitles are off
(`subs-with-matching-audio=forced`), as players do.

**In HLS (remux and transcode)** the menu is as today, the master's renditions; picture tracks
are not in the stream and are not offered. A file's picture track in a transcode is out of
scope (the home would have to draw it into the picture).

**Drawn by mpv, every one.**
- A file's own track: `sid` set to its id (`no` for off).
- The home's track (text: VTT, SRT, ASS): added with `sub-add <proxy URL> select <title>
  <lang>`, so mpv parses and times it; the Player's own cue drawing (`overlay`,
  `captionAss`) stays for the JavaFX engine and for HLS renditions as today.
- The choice and its language are kept for the next video (FR-34, `TrackChoice`), and
  Profile → Playback's subtitles-on and language pick the first track at open (`pickTrack`),
  across both sources, the home's first when two share a language.
- **S** toggles them as today.

**Size and colour.**
- `PlaybackValues` (shared `:client`) gains `subtitleColour`: **White** (default) and
  **Yellow**, stored as `playback.subtitle_colour` (`white`, `yellow`). The Android Players
  read it in a later story; nothing changes for them now.
- Profile → Playback on the desktop gets **Subtitle size** (Small, Medium, Large: the shared
  `SubtitleSize`) and **Subtitle colour** (White, Yellow), as `FilterChip` rows like the page's
  others, in English and Arabic.
- The watch screen passes the size to the player (`captionScale`), and the colour beside it.
- **Applied to text tracks without their own styling** (SRT, WebVTT, `mov_text`, the home's
  VTT and SRT) through `sub-scale` (the size's scale), `sub-color` (`#FFFFFF` or `#FFE14D`),
  `sub-border-color` black and `sub-border-size` 2, and to the OSD captions through
  `captionAss`'s `\fs` and `\1c`.
- **ASS tracks** keep their own styles; only the size applies (`sub-ass-override=scale`).
- **Picture tracks** are drawn as authored, neither size nor colour; the settings page says so
  under the colour row.
- A change in Profile applies at the next video; the open video is not changed.

**Arabic.** libass shapes and orders Arabic with HarfBuzz and FriBidi (13.18's build has both;
`build.sh` fails without them). Text tracks without styling use the Player's face
(`sub-font` IBM Plex Sans Arabic, as 13.18). ASS tracks use the fonts the file attaches
(`embeddedfonts=yes`); where a style's font has no Arabic, libass falls back through the
system (DirectWrite on Windows, fontconfig on Linux) and `sub-fonts-dir`, which holds the
Player's face. A right-to-left line keeps 13.18's RLE so a line starting with a digit reads
from the right.

**maktaba.dev and help.** The Apps page's Windows and Linux Player text and the help page
(`web/src/help/content/{en,ar}/desktop-player.md`, Watch and Profile) say which subtitles show
and the two new settings, in English and Arabic; the Apps page gets a screenshot of the
Subtitles menu on test data.

## Acceptance criteria

- **Given** a direct MKV with an Arabic SRT, an English ASS and a PGS track, and a sidecar
  English SRT on the home, **then** the Subtitles menu lists the sidecar first, then the three
  in-file tracks, the PGS one marked **Picture**.
- **Given** each of them chosen, **then** mpv draws it (its `sid` or the added track is the
  selected one), timed with the picture.
- **Given** Large and Yellow, **then** an SRT line and an OSD caption are drawn 1.3 times
  larger and yellow; an ASS line only larger; a PGS picture as authored.
- **Given** Arabic text in an SRT and in an ASS whose style names a Latin-only font, **then**
  both are drawn joined and right to left.
- **Given** subtitles on in Arabic in Profile, **when** a direct file with an Arabic in-file
  track and no sidecar opens, **then** that track is on.
- **Given** the JavaFX engine, **then** the menu is as before 13.20 and the caption JavaFX draws takes
  the size and colour.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | a recorded `track-list` (SRT ar, ASS en, PGS, a forced SRT) and the home's list (external en, generated ar, embedded ar) | build the menu | external en, generated ar, then SRT ar, ASS en, PGS (**Picture**); no embedded row; no forced row |
| T02 | unit | `captionAss` with Large and Yellow, rtl | build | `\fs` 1.3 times Medium's, `\1c&H4DE1FF&`, RLE first |
| T03 | unit | `PlaybackSettings` | write and read `subtitleColour`; an old store without it | kept; White |
| T04 | unit (`PlaybackMpvTest`) | a fake `Video` recording calls | choose an in-file track, a home track, off | `sid` id; `sub-add` with the proxy URL; `sid=no` |
| T05 | integration (real libmpv, CI's Desktop Player job) | `test-media.sh` muxes an MKV with SRT (ar), ASS (ar, style font `Arial`) and a committed PGS `.sup` (`src/test/resources/subs/arabic.sup`) | select each, seek to a line, render a frame with the software renderer | each drawn; the frame matches its reference within 13.17's tolerance; Arabic joined (reference reviewed once) |
| T06 | integration (real libmpv) | the same MKV with Large and Yellow | render the SRT and ASS lines | SRT yellow and larger, ASS larger in its own colour (pixel checks on the line's box) |
| T07 | screenshot (13.17) | Profile → Playback with the new rows; the Subtitles menu over a direct file; en and ar | capture | match their references |
| T08 | unit | the help page and the Apps page | the devportal and help checks | both languages present |

## Edge cases

- A file with 30 subtitle tracks: the menu scrolls; names that repeat get their track number.
- A track whose language is `und` and has no title: "Track n", **In the file**.
- A sidecar SRT in Windows-1256: the home serves it as UTF-8 (its subtitle route); mpv's
  `sub-codepage=auto` covers a raw file.
- A picture track at another resolution than the video (a 1080p PGS on a 2160p file): mpv
  scales it to the picture.
- A home that answers `/subtitles` with an error: the in-file tracks are offered alone, and the
  error is in the problem report's log.
- The **subtitles** feature turned off on the home: no menu, no in-file tracks either, as today.

## Build plan

| File | Change |
|---|---|
| `apps/android/client/…/playback/PlaybackValues.kt` | `SubtitleColour`, its key and default |
| `apps/desktop-player/src/main/kotlin/…/mpv/MpvVideo.kt` | the file's subtitle tracks from `track-list`; `sid`, `sub-add`; `sub-*` options; colour in `captionAss` |
| `apps/desktop-player/src/main/kotlin/…/Playback.kt`, `Watch.kt`, `VideoSurface.kt` | the two sources in direct play; size and colour passed |
| `apps/desktop-player/src/main/kotlin/…/watch/TrackMenus.kt`, `watch/WatchStrings.kt` | **In the file**, **Picture** |
| `apps/desktop-player/src/main/kotlin/…/profile/PlaybackSettingsPage.kt`, `ProfileStrings.kt` | the size and colour rows, en and ar |
| `apps/desktop-player/libmpv/test-media.sh`, `src/test/resources/subs/` | the subtitle test MKV and the PGS fixture |
| `apps/desktop-player/src/test/…/mpv/` | T04–T06 |
| `web/src/help/content/{en,ar}/desktop-player.md`, `web/packages/devportal/scripts/apps.mjs` | what shows, the settings, a screenshot |
