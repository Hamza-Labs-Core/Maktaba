# Story 56.12 — The phone and tablet Players catch up

> Epic 56 · Phase 4 · Depends on: 56.01, 56.08, 56.09 · Needed by: —

## Why

The Android and Apple Players (phone, tablet, Mac Catalyst / Mac) have a library's Videos and
Series tabs, series pages and Up next for episodes, but no Play all, no Shuffle, no collections,
and playlists only under Profile. What the TV gains should not be missing on the phone.

## Description

### 1. Play and Shuffle

On a library (above its grid, following its sort and filters), a series, a season, a collection
and a playlist: **Play** and **Shuffle** buttons, through 56.01's queue, using the shared queue
models of 56.02 (`PlayQueue.swift` in MaktabaKit; the Kotlin model in `apps/android/core`).

### 2. Tabs

A library's tabs become **Videos, Series, Collections, Playlists** (Apple `LibraryTab`, Android
`LibraryTab`). Recommended stays as the library page's hub rows above the tabs; Categories stays
the filter sheet. Collections and Playlists pages as on the TV, with touch.

### 3. Queue actions

A video's menu (long press / context menu) gains **Play next** and **Add to queue**. The player's
Up next (`UpNext.swift`, `UpNext.kt`) reads the queue first, then the series (as today when no
queue).

### 4. Smart playlists

The filter sheet gains **Save as smart playlist** (56.09). Playlists under Profile stay.

### 5. Home

**Continue your queue** (56.11) on Home.

## Acceptance criteria

- **Given** the Apple Player on iPhone and the Android Player on a phone, **when** Shuffle is
  tapped on a library, **then** playback starts with a random video of the whole library and Next
  plays another.
- **Given** a library, **then** its tabs are Videos, Series, Collections, Playlists in English, and
  their Arabic below, mirrored.
- **Given** Play next on a video during playback, **then** it plays after the current one.
- **Given** the filter sheet with a speaker chosen, **when** Save as smart playlist is used,
  **then** the playlist exists and plays on the TV.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | XCUITest (`Maktaba Player UITests`) | iPhone | `MockHome` | Shuffle on a library | POST recorded; player shows |
| T02 | XCUITest (`Maktaba Player iPad UITests`) | iPad | `MockHome` | Collections tab | cards; collection page |
| T03 | XCUITest | iPhone | Arabic | library tabs | Arabic, mirrored |
| T04 | XCUITest (`Maktaba Player Mac UITests`, CI only) | Mac | `MockHome` | Play on a playlist | POST recorded |
| T05 | Robolectric + Compose UI test | Android Player | MockWebServer | Shuffle | POST; player |
| T06 | Robolectric | Android Player | menu | Play next | POST items `where: next` |
| T07 | Roborazzi | Android Player | tabs en/ar dark, phone and tablet | render | screenshots |
| T08 | i18n | both | new keys | missing-Arabic tests | pass |

Mac and simulator UI tests run in CI only (throwaway simulators).

## Strings

Reuses 56.02's `library.play`, `library.shuffle`, 56.03's tab names, 56.09's smart playlist strings
and 56.11's `home.continueQueue`. New:

| Key | English | Arabic |
|---|---|---|
| `queue.playNext` | Play next | شغّل تاليًا |
| `queue.add` | Add to queue | أضف إلى قائمة الانتظار |
| `queue.added` | Added to the queue | أُضيف إلى قائمة الانتظار |
| `library.tab.videos` | Videos | المقاطع |

## Edge cases

- **A Player signed in through the cloud relay** (play-scoped): queues work; smart playlist
  creation is refused by the server, so the button is hidden when the token's scope is play.
- **The Duo's hinge split**: the queue continues across postures (one player).

## Build plan

| File | Change |
|---|---|
| `apps/apple/Player/Libraries/` (`LibrariesModels.swift`, `SeriesScreen.swift`, new `CollectionsTab.swift`, `PlaylistsTab.swift`), `apps/apple/Player/Watch/UpNext.swift`, catalogs | Apple |
| `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/libraries/`, `watch/UpNext.kt`, `res/values{,-ar}` | Android |
| `apps/apple/UITests/PlayerLibrariesUITests.swift`, `apps/android/player/src/test/...` | tests |
