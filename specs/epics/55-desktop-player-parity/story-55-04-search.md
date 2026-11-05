# Story 55.04 — Search: titles, transcripts, recent searches

> Epic 55 · Phase 2 · Depends on: 55.01, 55.02 · Needed by: 55.10

## Why

The desktop Player's search filters titles in the current library. The Android and Apple
Players search titles and words said in videos (Epic 49), suggest as the person types, keep
recent searches, and play a transcript hit at its moment. This story brings that search to the
desktop, with a keyboard-first field.

## Description

### 1. `SearchModel` on the desktop (`desktop/search/SearchModel.kt`)

As the Android `SearchModel`: `searchSuggestions(q)` as the person types (debounced 200 ms, a
stale answer dropped), `search(q, mode)` on Enter, then `searchTitles` and
`searchTranscripts` pages ("Show more"), `recentSearches()` when the field is empty,
`forgetRecentSearch(id)` and `clearRecentSearches()` through `ReadOrder.saving`. The mode is
`hybrid` when `semantic_search` is on and `keyword` when it is off; a degraded answer (meaning
search did not answer) shows "Text matches only".

### 2. The screen (`desktop/search/SearchScreen.kt`)

- A search field across the top, focused when the section opens; Ctrl+F (Cmd+F on a Mac build)
  and `/` focus it from anywhere in the Player and open Search.
- Under the field: suggestions as the person types (Up/Down to choose, Enter to search), or
  recent searches with their counts, a Remove button each and Clear all.
- Results in two groups: **Titles** (poster cards, opening the video page) and **In the
  transcript** (passage text with the matched words in bold, the time, "Play from 12:04"
  which plays at that second with `PlayRequest(startSec = …)`).
- When the window is wide, choosing a result shows its video page beside the results ("Choose
  a result to see it on this page."), as the Android tablet layout.
- **Gate**: `transcripts` off hides the transcript group from a viewer; an admin reads the off
  note there. `semantic_search` off: keyword results only; an admin also reads "Searching by
  words only. Search by meaning is off."

### 3. Words

| Key | English | Arabic |
|---|---|---|
| `search.hint` | Search titles and transcripts | ابحث في العناوين والتفريغات |
| `search.clear` | Clear the search | مسح البحث |
| `search.recent` | Recent searches | عمليات البحث الأخيرة |
| `search.recentClear` | Clear all | مسح الكل |
| `search.recentRemove` | Remove {0} | إزالة {0} |
| `search.recentCounts` | {0} in titles · {1} in transcripts | في العناوين: {0} · في التفريغات: {1} |
| `search.titles` | Titles | العناوين |
| `search.transcripts` | In the transcript | في التفريغ |
| `search.matches` | Matches: {0} | عدد المطابقات: {0} |
| `search.more` | Show more | عرض المزيد |
| `search.playFrom` | Play from {0} | التشغيل من {0} |
| `search.emptyHint` | Search for a title, or for words said in a video. | ابحث عن عنوان، أو عن كلمات قيلت في فيديو. |
| `search.noResults` | Nothing matches “{0}”. | لا شيء يطابق «{0}». |
| `search.degraded` | Text matches only: the meaning search did not answer. | مطابقات نصية فقط: لم يستجب البحث بالمعنى. |
| `search.wordsOnly` | Searching by words only. Search by meaning is off. | البحث بالكلمات فقط. البحث بالمعنى متوقف. |
| `search.failed` | Search failed | تعذّر البحث |
| `search.previewHint` | Choose a result to see it on this page. | اختر نتيجة لتراها في هذه الصفحة. |

## Acceptance criteria

- **Given** the Player, **when** the person presses Ctrl+F or `/`, **then** Search shows with
  the field focused.
- **Given** a typed word, **when** suggestions arrive for an older word after a newer one,
  **then** the older ones are not shown.
- **Given** results, **when** "Play from 12:04" is chosen on a transcript hit, **then** the
  video plays from 12:04.
- **Given** recent searches, **when** one is removed, **then** it leaves at once and a read
  that began before does not bring it back; Clear all empties the list the same way.
- **Given** `semantic_search` off and an admin, **when** they search, **then** keyword results
  show and the words-only line is under the field; a viewer sees no line.
- **Given** `transcripts` off and a viewer, **when** they search, **then** only titles show.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `SearchModel`, MockWebServer | type, Enter, Show more | suggestions, results, next page appended |
| T02 | unit | suggestions for "ab" answer after "abc"'s | — | "abc"'s kept |
| T03 | latch | a recent-searches read held, Remove, read answers with it | release | stays removed |
| T04 | latch | Clear all, then an older read answers | release | list stays empty |
| T05 | unit | `semantic_search` off | search | `mode=keyword` sent |
| T06 | screen | admin and viewer with `transcripts`/`semantic_search` off, `en`/`ar` | compose | note / line / hidden group |
| T07 | screen | a transcript hit | click Play from | `play(PlayRequest(startSec = 724))` |

## Edge cases

- **An empty query** shows recent searches, never a search.
- **The home has no search routes** (an old home, 404): "Search failed" with Try again; titles
  still open.
- **Right to left**: the times and the counts stay left-to-right inside the Arabic sentence
  (isolated, as `formatDuration` does).

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/search/SearchModel.kt`, `SearchScreen.kt` (new) | The page. |
| `.../desktop/shell/Shell.kt` | Ctrl+F and `/` open Search (one key handler). |
| `.../desktop/search/SearchStrings.kt` | §3 words. |
| `apps/desktop-player/src/test/.../search/SearchModelTest.kt`, `SearchScreenTest.kt` (new) | T01–T07. |
