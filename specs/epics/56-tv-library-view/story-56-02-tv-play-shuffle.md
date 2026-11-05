# Story 56.02 — Play and Shuffle on the TVs

> Epic 56 · Phase 3 · Depends on: 56.01, 56.03 · Needed by: 56.11

## Why

A library, a series, a season, a collection and a playlist on a TV can only be played one video
at a time. Plex puts Play and Shuffle on each of them; people expect to start a library on
shuffle from the couch and leave it.

## Description

### 1. Where the buttons are

| Surface | Buttons | Source sent to 56.01 |
|---|---|---|
| A library (header, on Recommended and Library) | **Play**, **Shuffle** | `library` + the Library tab's current filters and sort |
| A series page | **Continue** (or **Play** when nothing is started), **Shuffle** | `series` |
| A season page | **Play**, **Shuffle** | `season` |
| A collection page | **Play**, **Shuffle** | `collection` (or `topic:` / `clip:` for the pipeline's groups) |
| A playlist page | **Play**, **Shuffle** | `playlist` |
| The Play/Pause key on a focused card | plays that card (README §6.5) | `video`, `series`, `collection` or `playlist` |

Play is the first focusable button on each page and takes the initial focus on series,
season, collection and playlist pages (not on a library, which opens on its tabs).

### 2. What happens

The app calls `POST /api/me/play-queues`, then opens its existing player
(Android TV `PlayerScreen.kt`, tvOS `PlayerView`) on the queue's current item. A queue-aware
player model, shared per platform (Android: `core` module beside `TvApi`; Apple: `MaktabaKit`,
`PlayQueue.swift`), holds the window, moves with `PUT …/current` at each change and refills the
window when within 5 items of its edge. The player's overlay gains **Previous**, **Next** and,
for a shuffled queue, **Shuffle again**.

While the queue is being made the button shows a spinner (no second press is sent); a failure
shows `queue.error.load` with **Try again**.

## Acceptance criteria

- **Given** a library on either TV, **when** Shuffle is pressed with the remote, **then** the
  player starts within 2 s on the LAN with a random video of that library, and Next plays another
  random video of the same library.
- **Given** the Library tab filtered to Unwatched and sorted A–Z, **when** Play is pressed in the
  header, **then** the first video played is the first of that view.
- **Given** a series with episodes 1–3 watched, **when** Continue is pressed, **then** episode 4
  plays; Previous in the overlay plays episode 3.
- **Given** a shuffled queue, **when** Shuffle again is chosen in the overlay, **then** the
  current video keeps playing and Next plays from a new order.
- **Given** the app in Arabic, **then** Play is on the right of Shuffle and the overlay's
  Previous / Next are mirrored.
- **Given** the server answers 500, **then** the page shows `queue.error.load` with Try again and
  focus moves to Try again.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | Compose UI test (Robolectric, `app/src/test`) | Android TV | MockWebServer: library, queue | D-pad to Shuffle, Center | POST body `{source:{kind:library…}, shuffle:true}`; player shown |
| T02 | Compose UI test | Android TV | series page | initial focus | focus on Continue |
| T03 | Compose UI test | Android TV | queue window of 3 | Next ×3 | PUT current with each pos; window refill request at the edge |
| T04 | Roborazzi | Android TV | library header, en and ar, dark | render | screenshots for review; ar mirrored |
| T05 | XCUITest (`Maktaba TV UITests`, `XCUIRemote`) | Apple TV | `MockHome` with a library and a queue fixture | press Up to Shuffle, Select | the mock records the POST; the player appears |
| T06 | XCUITest | Apple TV | series fixture, 3 watched | Select on Continue | the mock records `kind: series`; player title is episode 4 |
| T07 | XCUITest | Apple TV | Arabic launch argument | open a collection | Play is right of Shuffle (frames compared) |
| T08 | XCTest (MaktabaKit) | Apple | `PlayQueue` with a stubbed client | next / previous / reshuffle | requests and window as specified |
| T09 | unit (Kotlin, core) | Android | queue model | same as T08 | same |
| T10 | i18n | both | new keys | missing-Arabic tests | pass |

tvOS UI tests run only in CI on a throwaway simulator, never on a local Mac.

## Strings

| Key | English | Arabic |
|---|---|---|
| `library.play` | Play | تشغيل |
| `library.shuffle` | Shuffle | تشغيل عشوائي |
| `series.continue` | Continue | متابعة |
| `player.previous` | Previous | السابق |
| `player.next` | Next | التالي |
| `player.shuffleAgain` | Shuffle again | خلط من جديد |
| `queue.error.load` | The queue could not be loaded. | تعذّر تحميل قائمة الانتظار. |
| `common.tryAgain` | Try again | حاول مرة أخرى |

Android uses the same keys with `_` (`library_play`) in `res/values` and `res/values-ar`; tvOS
in `apps/tv/tvos/MaktabaTV/Localizable.xcstrings`; shared ones in MaktabaKit's catalog.

## Edge cases

- **An empty view** (a filter with no match): Play and Shuffle are disabled, with the grid's own
  empty state.
- **A one-item source**: Shuffle is hidden; Play plays it.
- **The queue's next item is no longer readable**: the player skips it and says nothing.

## Build plan

| File | Change |
|---|---|
| `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/` | buttons on library, series, season, collection, playlist screens; `PlayerScreen.kt` takes a queue |
| `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/data/TvApi.kt` | queue routes |
| `apps/tv/tvos/MaktabaTV/Views/` | `LibraryView`, new `SeriesView`, `CollectionView`, `PlaylistView`; player overlay |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/PlayQueue.swift` (new) | shared queue model |
| `apps/tv/tvos/MaktabaTVUITests/TVLibraryPlayUITests.swift` (new), `apps/apple/UITests/MockHome.swift`, `apps/apple/UITests/Fixtures` | T05–T07 |
| `apps/tv/android/app/src/test/java/com/hamzalabs/maktaba/tv/PlayShuffleTest.kt` (new) | T01–T04 |
