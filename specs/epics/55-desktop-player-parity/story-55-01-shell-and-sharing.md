# Story 55.01 — The shell: five sections, a stack per section, shared state

> Epic 55 · Phase 1 · Depends on: — · Needed by: every other story

## Why

The desktop Player has one screen after sign-in: `LibraryBrowser`. Every feature in this epic
needs somewhere to live: the five sections the Android and Apple Players have (Home, Libraries,
Live TV, Search, Profile), pages pushed on a section (a video, a series, a library, a channel),
and one object every screen reads (the client, the user, the navigation, the player). This
story builds that shell, moves `ReadOrder` into `:client`, and splits the strings so the area
stories never edit one file.

## Description

### 1. Navigation (`apps/desktop-player/src/main/kotlin/.../desktop/shell/`)

- `Route` (sealed): the five roots, and `Video(id)`, `Series(id)`, `Library(id)`,
  `Channel(id)`, as the Android Player's `shell/Route.kt`. Playing a video is not a route: the
  window shows the player until it is closed, then the page under it.
- `Section` (enum, in the Android order) with its string key and root route.
- `Navigator`: one stack per section (Compose state). `select(s)` shows a section, or returns
  it to its root when it is already shown; `push(route)` adds a page (never the same page twice
  on top); `back()` returns. A section keeps its stack while another is shown.
- `Shell`: a `NavigationRail` at the start side with the shown sections (55.02 filters Live
  TV), the page beside it, and a Back button when the section has a page to return to.
  Alt+Left (Alt+Right in Arabic) and the mouse's back button call `back()`.

### 2. `DesktopHome` and `LocalDesktopHome`

What every signed-in screen reads, as the Android `PlayerHome`: `session`, `user`, `client`,
`nav`, `playback`, `posters`, `prefs` (language and the settings store), a `background` scope
for a change that must reach the home after its page is left, `signedOut()`, `webUrl`,
`play(PlayRequest)`, `keep(key) { state }` (a page's state kept while the home is signed in)
and `features()` (55.02).

### 3. Shared pieces on the desktop

- `PlayRequest(videoId, title, durationSec, startSec)`: `startSec = null` means where the
  person left it (55.08 reads it); `Playback.play(PlayRequest)` replaces `play(Video)`.
- `VideoSurface(player)`: the JavaFX picture, out of `Watch.kt`, so Live TV uses it too.
- `Cards.kt`: `Posters`, `PosterImage`, `PosterCard` (16:9 or 2:3, progress strip, title,
  subtitle line), `Modifier.choosable` (click, Enter, brass border on hover and focus),
  `Rail` (a titled row that scrolls sideways), `formatDuration`.
- `LibraryBrowser` is removed; 55.05 replaces it.

### 4. `ReadOrder` into `:client`

`apps/android/player/.../ReadOrder.kt` and `ReadOrderTest.kt` move to
`apps/android/client/.../core/session/`. The Android Player's four users (`HomeState`,
`LibrariesModels`, `SearchModel`, `ProfileState`) import it from there. Nothing else changes in
the Android Player. The other moves in README §5 happen in the stories that need them
(55.03, 55.07, 55.08, 55.09).

### 5. Strings split

`Strings.EN` and `Strings.AR` become the start screens' table plus one table per area
(`shell/ShellStrings.kt`, `home/HomeStrings.kt`, `search/…`, `libraries/…`, `video/…`,
`live/…`, `profile/…`, `watch/…`), each `Strings.Part(en, ar)`, merged by `Strings.merge`,
which throws on a key defined twice. Keys carry their area's prefix.

### 6. Words

| Key | English | Arabic |
|---|---|---|
| `section.home` | Home | الرئيسية |
| `section.libraries` | Libraries | المكتبات |
| `section.liveTv` | Live TV | البث المباشر |
| `section.search` | Search | بحث |
| `section.profile` | Profile | الملف الشخصي |
| `shell.back` | Back | رجوع |

## Acceptance criteria

- **Given** a signed-in person, **when** the window opens, **then** the rail shows Home,
  Libraries, Live TV, Search and Profile in that order, and Home is shown.
- **Given** a video page opened from Home, **when** the person shows Search and then Home
  again, **then** the video page is still on Home's stack.
- **Given** a section showing a pushed page, **when** its rail item is chosen again, **then**
  the section returns to its root.
- **Given** a pushed page, **when** the person presses Alt+Left (Alt+Right in Arabic), the
  mouse's back button or Back, **then** the page before shows; at a root nothing happens.
- **Given** Arabic, **when** the window opens, **then** the rail is on the right and every
  label is Arabic.
- **Given** the Android Player, **when** it builds after the move, **then** its tests pass
  unchanged and `ReadOrderTest` runs in `:client`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `Navigator` | push on Home, select Search, push, select Home | each stack kept |
| T02 | unit | `Navigator` | push the same route twice, back, back at root | no double; back false at root |
| T03 | unit | `Strings.merge` | two tables with one key | throws, naming the key |
| T04 | unit | `StringsTest` | every table | same keys and placeholders in both languages |
| T05 | screen | `Shell` with a fake `DesktopHome`, `ar` | compose | five labels in Arabic, rail on the right |
| T06 | unit (`:client`) | `ReadOrderTest` (moved) | as on Android | passes |
| T07 | build | `android-apps` | compile and test the Android Player | green, no behaviour change |

## Edge cases

- **Signing out or switching home** drops `DesktopHome` and every kept state with it.
- **A video playing** hides the shell; closing it shows the same page and stack as before.
- **Big screen** (F11): the rail grows with the 1.75× scale and the arrows move between rail
  items and cards.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/shell/Navigator.kt` (new) | `Route`, `Section`, `Navigator`. |
| `.../desktop/shell/DesktopHome.kt` (new) | `DesktopHome`, `AppPrefs`, `LocalDesktopHome`. |
| `.../desktop/shell/Shell.kt` (new) | Rail, Back, routing to each area's screen. |
| `.../desktop/shell/ShellStrings.kt` (new), `<area>/<Area>Strings.kt` (new, empty) | Area tables. |
| `.../desktop/Strings.kt` | Base table plus parts, `merge`. |
| `.../desktop/Main.kt` | Builds `DesktopHome`, shows `Shell` or `Watch`. |
| `.../desktop/Playback.kt`, `Watch.kt`, `VideoSurface.kt` (new), `Cards.kt` (new); `Library.kt` removed | Shared pieces. |
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/session/ReadOrder.kt` (moved), its test | Shared. |
| `apps/android/player/.../home/HomeState.kt`, `libraries/LibrariesModels.kt`, `search/SearchModel.kt`, `profile/ProfileState.kt` | Import from `:client`. |
| `apps/desktop-player/build.gradle.kts` | `testImplementation(compose.desktop.uiTestJUnit4)` for screen tests. |
| `apps/desktop-player/src/test/.../shell/NavigatorTest.kt`, `ShellTest.kt` (new), `StringsTest.kt` | T01–T05. |
