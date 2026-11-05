# Story 55.05 — Libraries and the library page

> Epic 55 · Phase 2 · Depends on: 55.01 · Needed by: 55.06, 55.10

## Why

The desktop Player lists libraries beside one grid of videos. The Android and Apple Players
(Epic 45, story 48.27) have a Libraries section with pinning, and a library page with its hub
rows, a videos tab with sort and filters, and a series tab. This story replaces the desktop's
`LibraryBrowser` with the same pages.

## Description

### 1. Libraries section (`desktop/libraries/LibrariesScreen.kt`)

- `librarySummaries()` as cards (name, videos and length), pinned ones first under "Pinned".
- Pin and Unpin on each card (a pin button, and the right-click menu): `setPinnedLibraries(ids)`
  through `ReadOrder.saving`, applied at once; a failure puts it back with "Couldn't save that.
  Try again." Pinned libraries are Home's library rows (55.03).
- Choosing a library opens `Route.Library(id)`.
- Empty: "No libraries yet".

### 2. Library page (`desktop/libraries/LibraryPage.kt`)

As the Android `LibraryPage` and `LibrariesModels`:
- **Hub**: `libraryHub(id)` rows (On Deck, Continue Watching, Recently Added, Recently Watched,
  series, years) as `Rail`s.
- **Videos** tab: an adaptive grid (`GridCells.Adaptive(220.dp)`) of `videoPage(query)`, more
  as the person scrolls to the end, with Sort (Newest, A–Z) and filters from `videoFacets(id)`
  (Language, Speaker). Filters show as menus above the grid on a wide window.
- **Series** tab: `librarySeries(id, sort)` cards with "{0} of {1} watched"; a series opens
  `Route.Series(id)`.
- A library that is gone or not shared: "Not available" with its detail line.

### 3. Words

| Key | English | Arabic |
|---|---|---|
| `lib.pinned` | Pinned | مثبّتة |
| `lib.pin` | Pin {0} | ثبّت {0} |
| `lib.unpin` | Unpin {0} | ألغِ تثبيت {0} |
| `lib.none` | No libraries yet | لا توجد مكتبات بعد |
| `lib.saveFailed` | Couldn't save that. Try again. | تعذّر الحفظ. حاول مرة أخرى. |
| `lib.tab.videos` | Videos | المقاطع |
| `lib.tab.series` | Series | المسلسلات |
| `lib.sort` | Sort | ترتيب |
| `lib.sort.newest` | Newest | الأحدث |
| `lib.sort.title` | A–Z | أ–ي |
| `lib.language` | Language | اللغة |
| `lib.anyLanguage` | Any language | كل اللغات |
| `lib.speaker` | Speaker | المتحدث |
| `lib.anySpeaker` | Any speaker | كل المتحدثين |
| `lib.noVideos` | No videos yet | لا توجد مقاطع بعد |
| `lib.noMatch` | No videos match your filters. | لا توجد مقاطع تطابق عوامل التصفية. |
| `lib.seriesNone` | No series yet. | لا توجد سلاسل بعد. |
| `lib.seriesProgress` | {0} of {1} watched | تمت مشاهدة {0} من {1} |
| `lib.notAvailable` | Not available | غير متاح |
| `lib.notAvailableDetail` | It may have been removed, or it is not shared with you. | ربما حُذف، أو لم يُشارَك معك. |

The hub rows reuse Home's row titles (`home.row.*`).

## Acceptance criteria

- **Given** three libraries, one pinned, **when** Libraries shows, **then** the pinned one is
  first under Pinned.
- **Given** a library, **when** Pin is chosen, **then** it moves to Pinned at once and an older
  read does not move it back; Home shows its row on the next read.
- **Given** a library page, **when** the person chooses Language "ar" and Sort A–Z, **then** the
  grid shows only Arabic videos in title order, more loading at the end.
- **Given** the Series tab, **when** a series is chosen, **then** its page opens.
- **Given** a library that is not shared, **when** its page opens, **then** "Not available" and
  its detail show.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `LibrariesState`, MockWebServer | load | pinned first |
| T02 | latch | a summaries read held, Pin, the read answers unpinned | release | stays pinned |
| T03 | latch | Pin fails | — | unpinned again, sentence shown |
| T04 | unit | `LibraryVideos`, filters and sort | change filter mid-page | page restarts with the new query; an older page dropped |
| T05 | unit | 404 on `libraryHub` | open | not-available state |
| T06 | screen | a wide and a narrow window, `en`/`ar` | compose | filters as menus / in a row; Arabic labels |

## Edge cases

- **A video removed** between pages is not shown twice when the next page shifts (dedupe by id,
  as the old `LibraryBrowser` did).
- **No facets** (a home without speakers): the filter menu is not shown.
- **All videos**: the Libraries section has an "All videos" card that opens a grid over every
  library, as before.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/libraries/LibrariesScreen.kt`, `LibrariesState.kt`, `LibraryPage.kt`, `LibraryVideos.kt` (new) | The pages. |
| `.../desktop/libraries/LibrariesStrings.kt` | §3 words. |
| `.../desktop/Strings.kt` | Drop the old `lib.empty`, `lib.more`, `lib.searchHint`, `nav.all`, `nav.search`. |
| `apps/desktop-player/src/test/.../libraries/LibrariesStateTest.kt`, `LibraryVideosTest.kt`, `LibrariesScreenTest.kt` (new) | T01–T06. |
