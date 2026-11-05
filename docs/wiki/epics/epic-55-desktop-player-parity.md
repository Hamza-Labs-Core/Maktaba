# Epic 55 — The desktop Player catches up with the phone and Mac Players

> **Status:** spec (2026-11-05). **Source:** [`specs/epics/55-desktop-player-parity/`](../../../specs/epics/55-desktop-player-parity/README.md).
> **Anchors:** [Epic 42](../../../specs/epics/42-apps-player-remote-server/README.md) (story 42.4, the Windows and Linux Player) · [Epic 48](../../../specs/epics/48-native-players/README.md) (the native Players' screens) · [Epic 51](epic-51-configurable-components.md) (story 51.11, the capabilities gate) · issue #638.

## Goal

The Maktaba Player for Windows and Linux (`apps/desktop-player`, Compose for Desktop on the
shared client `apps/android/client`) stopped at story 42.4: a list of libraries, a grid, a title
search and a bare player. The Android and Apple Players have Home rows, Live TV, search, series
and video pages, a profile with history, audio and subtitle tracks, quality, Up Next, approving
a TV and the capabilities gate. This epic brings the desktop Player to the same features with a
desktop's own UI (mouse, keyboard, window), and leaves out what only phones do (picture in
picture, casting, background audio).

## The design in one table

| | Choice |
|---|---|
| Shell | five sections on a navigation rail (Home, Libraries, Live TV, Search, Profile), a stack of pages per section, Back and Alt+Left |
| Shared code | Compose-free logic moves into `:client` (`ReadOrder`, Home's row builder and cache, track and Up Next rules, playback settings, guide maths); Compose state stays in each app |
| Video | JavaFX through `StreamProxy`, as today; subtitles drawn from the home's WebVTT inside the JavaFX scene; audio chosen by rewriting the HLS master playlist |
| Live TV | `LiveSource` from `:client`; `StreamProxy` adds the token for the web port's live paths and rewrites the Cloudflare token away from home |
| Gate | `-Pmaktaba.capabilitiesGate` into `BuildInfo.CAPABILITIES_GATE` (#638), capabilities followed while signed in, hidden from viewers, explained to admins |
| Words | English and Arabic in one table per area, a test for missing Arabic and duplicate keys |
| Left out | theme choice, diagnostics page (Epic 53 covers reports), picture in picture, casting, background audio, downloads |

## Stories

| # | Story | Phase |
|---|---|---|
| 55.01 | [The shell: five sections, a stack per section, shared state](../../../specs/epics/55-desktop-player-parity/story-55-01-shell-and-sharing.md) | 1 |
| 55.02 | [The capabilities gate and its build switch (#638)](../../../specs/epics/55-desktop-player-parity/story-55-02-capabilities-gate.md) | 1 |
| 55.03 | [Home: hero, rows, recommendations](../../../specs/epics/55-desktop-player-parity/story-55-03-home.md) | 2 |
| 55.04 | [Search: titles, transcripts, recent searches](../../../specs/epics/55-desktop-player-parity/story-55-04-search.md) | 2 |
| 55.05 | [Libraries and the library page](../../../specs/epics/55-desktop-player-parity/story-55-05-libraries.md) | 2 |
| 55.06 | [The series page and the video page](../../../specs/epics/55-desktop-player-parity/story-55-06-series-and-video.md) | 2 |
| 55.07 | [Live TV: Now, Guide, the channel player](../../../specs/epics/55-desktop-player-parity/story-55-07-live-tv.md) | 2 |
| 55.08 | [Watching: resume, tracks, subtitles, quality, Up Next, keys](../../../specs/epics/55-desktop-player-parity/story-55-08-watch.md) | 2 |
| 55.09 | [Profile: history, favorites, playlists, sessions, settings](../../../specs/epics/55-desktop-player-parity/story-55-09-profile.md) | 2 |
| 55.10 | [Help, README and the release check](../../../specs/epics/55-desktop-player-parity/story-55-10-help-and-release.md) | 3 |

## Hard calls (details in the README §12)

One native Compose UI for Windows and Linux, not a web wrapper. JavaFX stays the video stack
(VLCJ or mpv would need a native library per platform in the installers); the home already
transcodes for the `desktop-player` profile, and the Player draws subtitles and picks audio
itself. Only Compose-free code moves into `:client`, so the Android TV and Remote builds do not
take a Compose dependency. No light theme and no diagnostics page in this epic.
