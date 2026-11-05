# Epic 55: The desktop Player catches up with the phone and Mac Players

> **Status:** spec (2026-11-05). **Builds on:** [Epic 42](../42-apps-player-remote-server/README.md)
> (story 42.4 made the Windows and Linux Player, `apps/desktop-player`),
> [Epic 48](../48-native-players/README.md) (the native Players' screens, as built on Android
> and Apple), [Epic 46](../46-big-screen/README.md) (big screen) and
> [Epic 51](../51-configurable-components/README.md) (story 51.11, the capabilities gate).
> **Closes:** issue #638 (a build switch for the gate on the desktop Player).
> **Wiki:** [epic-55-desktop-player-parity.md](../../../docs/wiki/epics/epic-55-desktop-player-parity.md).

## 1. Why

The desktop Player for Windows and Linux ships in every release (`latest-player.json`, the
msi, deb and rpm built by `.github/workflows/desktop-release.yml`), but its features stopped at
story 42.4 (2026-10-27). Since then the Android and Apple Players gained Home rows, Live TV,
search, series and video pages, a profile with history, audio and subtitle tracks, quality, Up
Next, approving a TV and the capabilities gate. The desktop Player still shows a list of
libraries beside a grid of videos, a title search, and a bare player. A person who watches on a
phone and on a Windows computer sees two different products.

This epic brings the desktop Player to the same features, with a desktop's own UI: a mouse, a
keyboard and a resizable window, no picture in picture and no casting.

## 2. What the desktop Player has today

On `main` at `357851f7d`, `apps/desktop-player` is about 1,300 lines of Kotlin (Compose for
Desktop) on the shared client `apps/android/client` (`:client`):

| Area | Today |
|---|---|
| Start | home picker (mDNS, saved homes, an address or a `maktaba://connect` link), sign-in by password, by code (QR) and through Maktaba Cloud |
| Browse | `LibraryBrowser`: libraries on the left, a grid of posters, a title search, Load more |
| Watch | `Watch`: JavaFX `MediaPlayer` through `StreamProxy` (the token is added on `127.0.0.1`), play/pause, ±10 s, a seek bar, progress reported every 10 s |
| Other | big screen (F11), approve a sign-in (menu), language (menu), updates (minisign-checked) |
| Gate | `AppSession` is made with the gate on, but nothing follows capabilities and no screen reads them; no build switch (#638) |

## 3. Gap inventory

Each row is a screen or behaviour of the Android Player (`apps/android/player`) or the Apple
Player (`apps/apple/Player`), and what this epic does with it on the desktop.

| # | Feature | Android | Apple | Desktop today | Epic 55 |
|---|---|---|---|---|---|
| G1 | Five sections: Home, Libraries, Live TV, Search, Profile | yes | yes | no (one browser) | **built**, 55.01 |
| G2 | A stack of pages per section, back | yes | yes | no | **built**, 55.01 (Back button, Alt+Left, the mouse's back button) |
| G3 | Capabilities gate: hide from a viewer, explain to an admin | yes | yes | no | **built**, 55.02 |
| G4 | Gate build switch (`-Pmaktaba.capabilitiesGate`) | yes | yes | no (#638) | **built**, 55.02 |
| G5 | Home hero (featured) | yes | yes | no | **built**, 55.03 |
| G6 | Home rows: Continue Watching, On Deck, What's On Now, Favorites, Recently Added, Recently Watched, pinned libraries | yes | yes | no | **built**, 55.03 |
| G7 | Recommendations ("For You") with Not interested; gated by `recommendations` | yes | yes | no | **built**, 55.03 |
| G8 | Home kept on screen when a refresh fails (last rows) | yes | yes | no | **built**, 55.03 |
| G9 | Search: titles and transcripts, suggestions, paging | yes | yes | title filter only | **built**, 55.04 |
| G10 | Recent searches: remove one, clear all | yes | yes | no | **built**, 55.04 |
| G11 | Transcript hit plays at its moment | yes | yes | no | **built**, 55.04 |
| G12 | Search by meaning gated (`semantic_search`, `transcripts`) | yes | yes | no | **built**, 55.04 |
| G13 | Libraries list with pinning | yes | yes | list only | **built**, 55.05 |
| G14 | Library page: hub rows, videos with sort and filters (language, speaker), series tab | yes | yes | grid only | **built**, 55.05 |
| G15 | Series page: seasons, episodes, continue, mark watched (episode, season) | yes | yes | no | **built**, 55.06 |
| G16 | Video page: Play, Resume from, Play from start, favorite, mark watched, chapters, next episodes, transcript passage | yes | yes | no | **built**, 55.06 |
| G17 | Chapters gated (`chapters`) | yes | yes | no | **built**, 55.06 |
| G18 | Live TV: Now, Guide by time, channel logos | yes | yes | no | **built**, 55.07 |
| G19 | Channel player with channel up/down, back to live, failure sentences | yes | yes | no | **built**, 55.07 |
| G20 | Live away from home (remote session through Maktaba Cloud) | yes | yes | no | **built**, 55.07 |
| G21 | Resume where the person left a video | yes | yes | no (starts at 0) | **built**, 55.08 |
| G22 | Audio track choice | yes | yes | no | **built**, 55.08 |
| G23 | Subtitles on/off and language, remembered; gated (`subtitles`) | yes | yes | no | **built**, 55.08 |
| G24 | Quality choice (Auto, the session's ladder) | yes | yes | no | **built**, 55.08 |
| G25 | Up Next with a countdown and Cancel | yes | yes | no | **built**, 55.08 |
| G26 | Keyboard: Space, ←/→ 10 s, Shift+←/→ 30 s, N next episode, S subtitles, M mute, F full screen, Escape | partial (D-pad) | yes (Mac menu) | Space, ←/→, Escape | **built**, 55.08 |
| G27 | Profile: watch history with Forget | yes | yes | no | **built**, 55.09 |
| G28 | Profile: favorites, playlists (play one) | yes | yes | no | **built**, 55.09 |
| G29 | Profile: sign-in sessions, end one | yes | yes | no | **built**, 55.09 |
| G30 | Approve a TV or device | yes | yes | menu only | **built**, 55.09 (also in Profile) |
| G31 | Playback settings: subtitles on, subtitle and audio language, quality | yes | yes | no | **built**, 55.09 |
| G32 | Language: English, Arabic | yes | yes | menu | **built**, 55.09 (also in Profile) |
| G33 | About: app and home versions; help; switch home; sign out with confirmation | yes | yes | menu, partial | **built**, 55.09 |
| G34 | Maktaba Cloud link state in Profile | no | yes | no | **built**, 55.09 |
| G35 | ReadOrder: a read never undoes a newer change | yes | yes | no | **built**, 55.01 (shared in `:client`), used by 55.03–55.09 |
| G36 | Theme choice (light, dark, system) | yes | yes | dark only | **left out**: the desktop Player is dark like big screen and the TV apps; a light theme is its own story if asked for |
| G37 | Diagnostics page (the app's own log) | yes | yes | no | **left out**: Epic 53 story 53.15 ("Report a problem") gives every app one way to send logs; the desktop Player joins there |
| G38 | Picture in picture | yes | yes | — | **left out**: no such window mode on Windows or Linux desktops in Compose for Desktop; a small window does the same |
| G39 | Cast (Chromecast) and AirPlay | yes | AirPlay | — | **left out**: phone features; a TV has its own Player |
| G40 | Background audio, lock-screen controls (media session) | yes | yes | — | **left out**: a desktop window keeps playing when it loses focus; system media keys are mapped to play/pause (55.08) |
| G41 | Downloads for offline | no | no | — | **left out**: no Player has it |
| G42 | Layout by window size (phone, foldable, tablet) | yes | yes | — | **built differently**: the desktop pages use adaptive grids and a details pane beside the poster when the window is wide |

## 4. Goals

1. Every **built** row in §3 works on Windows and Linux, in English and Arabic (right to left).
2. Screens and words match the Android Player's; where the desktop differs (mouse, keyboard,
   window) it says so in the story.
3. Logic that is not about Android moves into `:client` instead of being copied (§5).
4. Every list with a change (forget, favorite, watched, pin, Not interested, remove a recent
   search, end a session) uses `ReadOrder`, with latch tests like the Android ones.
5. The `desktop-player` CI jobs (`.github/workflows/_desktop-player.yml`) and the `player` legs
   of `desktop-release.yml` stay green, and the installers keep their names.

## 5. The `:client` sharing plan

`apps/android/client` is plain Kotlin on the JVM (OkHttp, kotlinx.serialization, coroutines);
both the Android apps and the desktop Player build on it. The rule: **a class goes into
`:client` when it has no Android or Compose import and both Players need it.** Compose state
(`mutableStateOf`) stays in each app, because `:client` has no Compose dependency and adding one
for a few state holders would tie the Android TV and Remote builds to it.

| Moves into `:client` | From | Package | Why |
|---|---|---|---|
| `ReadOrder` and its tests | `apps/android/player/.../ReadOrder.kt` | `core.session` | pure Kotlin; every list on both Players uses it |
| `homeRows()` (the row builder) and its row types | `player/home/HomeRows.kt` | `core.browse` | turns `/api/home`, recommendations and now-on into rows; no UI (a row's "See all" names a library id instead of the Android `Route`) |
| `HomeCache` (last good rows per home) | `player/home/HomeCache.kt` | `core.browse` | OkHttp `HttpUrl` only |
| Track choice rules (`TrackChoice`, the defaults from settings) | `player/watch/Tracks.kt` (the parts without Media3) | `core.playback` | the desktop decides tracks without ExoPlayer |
| Up Next choice (next episode, countdown length) | `player/watch/UpNext.kt` (logic only) | `core.playback` | same rule on both |
| Playback preferences (keys, defaults, `PlaybackValues`) | `player/profile/PlaybackSettings.kt` (minus `Context`) | `core.playback` | the desktop stores them in its `FileStore` |
| Guide time maths (blocks in a window, now line) | `player/live/GuideTab.kt` (logic only) | `core.live` | both draw the same guide |

The Android Player keeps its screens and Compose state, and imports the moved code. Each move
is its own commit with its tests moved, so the Android jobs (`android-apps`) prove nothing
changed. What stays desktop-only: the shell, the screens, JavaFX playback, `StreamProxy`,
storage, updates.

## 6. Playing video on JavaFX

The desktop Player plays with JavaFX's media stack (H.264, AAC, MP4 or HLS) through
`StreamProxy`, which adds the token for the home's `/stream/` paths only. The streaming
service's `desktop-player` profile (`streaming/internal/capability/matrix.go`) already limits
streams to what JavaFX plays. JavaFX has two limits this epic works around (story 55.08):

- **It draws no subtitles.** The Player reads the session's subtitle renditions from the HLS
  master playlist (through `StreamProxy`), fetches the chosen WebVTT, and draws the cues itself
  in a JavaFX `Text` node over the `MediaView` (inside the JavaFX scene, so Swing does not hide
  it).
- **It plays an HLS stream's default audio only.** `StreamProxy` rewrites the master playlist
  so the chosen `EXT-X-MEDIA` audio rendition is the only one and is `DEFAULT=YES`, then the
  stream reopens at the same position. A direct MP4 with several audio tracks is opened as HLS
  (remux) when the person picks a track that is not the first.

Live TV (55.07) plays through the same proxy: at home, the web port's HLS with the token added;
away from home, the remote session's signed Cloudflare URL, with the newest token written into
every playlist and segment request as `LiveSource.interceptor` does on Android.

## 7. Desktop UI rules

- The sections sit on a navigation rail at the start side (the right in Arabic). A page pushed
  on a section shows a Back button; Alt+Left (Alt+Right in Arabic) and the mouse's back button
  go back.
- Cards take a click, Enter, and show a brass border on hover and on keyboard focus (big
  screen's arrows keep working, Epic 46).
- Pages use the window: adaptive grids, rows that scroll sideways, a details pane beside the
  poster when the window is wide.
- JavaFX video is drawn by Swing above Compose, so nothing Compose is drawn over a video:
  controls sit below it, captions are drawn inside the JavaFX scene.
- No phone-only feature (§3 G36–G41).

## 8. Words

Every string is in English and Arabic in the desktop Player's `Strings.kt`, split into one
table per area (`<area>/<Area>Strings.kt`) so stories do not edit one file. A test fails when a
key has no Arabic, when the placeholders differ, or when a key is defined twice. Wording reuses
the Android Player's (`apps/android/player/src/main/res/values{,-ar}/strings_*.xml`); the home
in Arabic is «الجهاز الرئيسي». Each story lists its new strings.

## 9. Tests

- Unit tests for every state class, against MockWebServer with `:client`'s test fixtures
  (`MemoryTokens`, `jwt`, `MapStore`).
- Screen tests off screen with Compose for Desktop's `runComposeUiTest` (no window, no display),
  in English and Arabic.
- `ReadOrder` latch tests for every change (§4.4): a read that began before the change and
  answers after it does not bring the old state back; a read that began after it applies.
- `StreamProxy` tests for every new rewrite rule, including that only the home's own stream
  paths ever get the token.
- All in the `desktop-player` job (Linux and Windows). Nothing opens a window; nothing calls a
  real host.

## 10. Stories

| Story | Title | Depends on |
|---|---|---|
| [55.01](story-55-01-shell-and-sharing.md) | The shell: five sections, a stack per section, shared state | — |
| [55.02](story-55-02-capabilities-gate.md) | The capabilities gate and its build switch (#638) | 55.01 |
| [55.03](story-55-03-home.md) | Home: hero, rows, recommendations | 55.01, 55.02 |
| [55.04](story-55-04-search.md) | Search: titles, transcripts, recent searches | 55.01, 55.02 |
| [55.05](story-55-05-libraries.md) | Libraries and the library page | 55.01 |
| [55.06](story-55-06-series-and-video.md) | The series page and the video page | 55.01, 55.02, 55.05 |
| [55.07](story-55-07-live-tv.md) | Live TV: Now, Guide, the channel player | 55.01, 55.02 |
| [55.08](story-55-08-watch.md) | Watching: resume, tracks, subtitles, quality, Up Next, keys | 55.01, 55.02 |
| [55.09](story-55-09-profile.md) | Profile: history, favorites, playlists, sessions, settings | 55.01, 55.08 |
| [55.10](story-55-10-help-and-release.md) | Help, README and the release check | 55.03–55.09 |

55.03, 55.04, 55.05, 55.07 and 55.08 can be built at the same time once 55.01 and 55.02 are in.

## 11. Rollout

The epic ships in one integration branch and one PR, built by parallel worktrees per area
(home and search; libraries, series and the video page; Live TV; profile, watching and the
gate). The desktop Player updates itself from `latest-player.json` on the next tag; nothing on
the home changes. The gate switch defaults to on; a build with `-Pmaktaba.capabilitiesGate=off`
shows everything and the home's `409 component_off` still answers.

## 12. Decisions

- **One Compose UI for Windows and Linux**, no web wrapper: the desktop Player stays native
  like the other Players (Epic 42).
- **JavaFX stays the video stack.** VLCJ or an embedded mpv would play more formats and draw
  subtitles, but need a native library per platform in the installers; the home already
  remuxes and transcodes for the `desktop-player` profile, and §6 covers tracks and subtitles.
- **Compose state stays in each app** (§5); only Compose-free logic moves into `:client`.
- **No light theme and no diagnostics page** in this epic (§3 G36, G37).
