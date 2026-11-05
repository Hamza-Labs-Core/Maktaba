# Story 56.03 — Library tabs and remote navigation on the TVs

> Epic 56 · Phase 3 · Depends on: — (56.04, 56.05, 56.08, 56.10 fill the tabs) · Needed by: 56.02, 56.04, 56.05, 56.08, 56.10

## Why

Plex's TV apps show a library as five tabs: Recommended, Library, Collections, Playlists and
Categories. Maktaba's TV apps show one grid. The tabs are the frame every other TV story fills,
and they must work with the remote alone, in both languages.

## Description

### 1. The tabs

| Tab | Content | Reads |
|---|---|---|
| **Recommended** (opens here) | rows: On Deck, Continue Watching, Recently Added, Recently Watched, then promoted collections (56.08); empty rows hidden | `GET /api/libraries/{id}/hub` |
| **Library** | the grid with Show, Sort, Filter and the letter rail (56.04, 56.05) | `GET /api/videos?library=…` / `group=series` |
| **Collections** | collection cards (56.08) | `GET /api/libraries/{id}/collections` |
| **Playlists** | the user's playlists with items here (56.10) | `GET /api/me/playlists?library=` |
| **Categories** | Speakers, Topics, Languages, Years (56.05) | `GET /api/libraries/{id}/categories` |

A tab with nothing to show (no collections, no playlists) stays, with its empty state, so the
tab row never shifts. The last tab chosen is kept per library on the device (Android
`DataStore`, Apple `@AppStorage`) and reopened next time.

### 2. Layout and focus

As README §6.4 and §6.5. Android TV uses Compose for TV's `TabRow` with `Tab`s that select on
focus after 300 ms (so scrolling across tabs does not load each); tvOS uses a top
segmented row in a `.focusSection()` above the content, selecting on focus the same way.

- Back from content scrolls to the top and focuses the first row's first card; Back again
  focuses the tab row; Back again leaves the library.
- Each tab keeps its scroll and focused card while the user visits another tab or a detail
  page.
- Android TV: the library screen moves from `LibrariesScreen.kt`'s `LibraryScreen` into its own
  `ui/screens/library/` folder with one file per tab.

### 3. Android TV's Home

Android TV's Home reads `GET /api/home` (Continue Watching, On Deck, Recently Added, pinned
libraries, promoted collections) instead of `GET /api/recommendations` only, as the Apple TV's
`HomeShelves` already does; the For You row stays.

## Acceptance criteria

- **Given** either TV app, **when** a library opens, **then** the Recommended tab is selected and
  focused, and five tabs show with the names below.
- **Given** focus on the tab row, **when** Right is pressed four times, **then** Categories is
  selected; in Arabic, Left does the same.
- **Given** the Library tab scrolled to row 20, **when** Back is pressed, **then** focus is on the
  first card of row 1; Back again focuses the Library tab; Back again shows the libraries list.
- **Given** the user left on Collections, **when** the library is reopened, **then** Collections
  is selected.
- **Given** Android TV's Home, **then** Continue Watching shows the videos of `GET /api/home`.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | Compose UI test | Android TV | MockWebServer library | open | five tabs, Recommended focused |
| T02 | Compose UI test | Android TV | ar locale | D-pad Left ×4 | Categories selected |
| T03 | Compose UI test | Android TV | 400-item grid | scroll, Back ×3 | focus steps as specified |
| T04 | Compose UI test | Android TV | tab kept | reopen | last tab |
| T05 | Compose UI test | Android TV | `/api/home` mock | Home | rows from home |
| T06 | Roborazzi | Android TV | each tab, en/ar, dark | render | screenshots |
| T07 | XCUITest (`XCUIRemote`) | Apple TV | `MockHome` library | open; press right ×4 | Categories selected |
| T08 | XCUITest | Apple TV | long grid | swipe down, Menu ×3 | focus steps; libraries list last |
| T09 | XCUITest | Apple TV | Arabic | open | tab order mirrored (frames) |
| T10 | XCUITest | Apple TV | accessibility audit | each tab | `performAccessibilityAudit` passes |
| T11 | i18n | both | new keys | missing-Arabic tests | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `library.tab.recommended` | Recommended | المقترحات |
| `library.tab.library` | Library | المكتبة |
| `library.tab.collections` | Collections | المجموعات |
| `library.tab.playlists` | Playlists | قوائم التشغيل |
| `library.tab.categories` | Categories | الفئات |
| `library.row.onDeck` | On Deck | التالي للمشاهدة |
| `library.row.continue` | Continue Watching | تابع المشاهدة |
| `library.row.recentlyAdded` | Recently Added | أُضيف حديثًا |
| `library.row.recentlyWatched` | Recently Watched | شوهد حديثًا |
| `library.empty.recommended` | Nothing to recommend yet. Browse the Library tab. | لا مقترحات بعد. تصفّح تبويب المكتبة. |

## Edge cases

- **A library with one video**: all tabs show; Recommended shows Recently Added only.
- **A slow hub** (> 1 s): the tab shows its placeholder cards, then fills (no layout jump).
- **A library the user loses access to while open**: the next read answers 404; the app shows
  the not-available page and Back returns to the libraries list.

## Build plan

| File | Change |
|---|---|
| `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/library/` (new) | `LibraryTabs.kt`, one file per tab |
| `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/HomeScreen.kt` | reads `/api/home` |
| `apps/tv/android/app/src/main/res/values{,-ar}/strings.xml` | strings |
| `apps/tv/tvos/MaktabaTV/Views/Library/` (new) | `LibraryTabsView.swift` and one view per tab |
| `apps/tv/tvos/MaktabaTV/Localizable.xcstrings` | strings |
| `apps/tv/tvos/MaktabaTVUITests/TVLibraryTabsUITests.swift` (new) | T07–T10 |
| `apps/tv/android/app/src/test/java/com/hamzalabs/maktaba/tv/LibraryTabsTest.kt` (new) | T01–T06 |
