# Story 56.04 — Series grouping, series and season pages on the TVs

> Epic 56 · Phases 1 and 3 · Depends on: 56.03 · Needed by: 56.11

## Why

Most of a Maktaba library is lessons in series and seasons. The phone Players, the web and the
web's `/big` view group them; the TVs show loose videos only. Plex's Library tab has a type
picker (shows, seasons, episodes); Maktaba's has **Show: Videos | Series**.

## Description

### 1. Server: the grid by series

`GET /api/videos` gains `group=series`: the same filters, and one card per series that has a
matching video (videos in no series appear as themselves). Sorts: `name`, `newest` (the newest
matching episode), `recent` (last watched by the caller) — the sorts of
`GET /api/libraries/{id}/series`, which this reuses. Each series card carries `id`, `name`,
`poster`, `episode_count`, `matching_count`, `unwatched_count`, `next_episode`. Paged by cursor.

### 2. The Show picker

On the Library tab, a **Show** button cycles Videos / Series (Select opens a small menu on tvOS).
The choice is kept per library on the device. In Series, the Play and Shuffle of the header play
the matching videos (not whole series).

### 3. The series page

Backdrop and poster, name, year span, episode and season counts, description; buttons
**Continue** / **Play** and **Shuffle** (56.02) and **Mark watched**; a row of season pills
(`series_seasons` names, else `Season {n}`); the selected season's episodes as a list with
number, title, length, progress and watched mark. The list order follows the season's kind:
`ongoing` newest first, the rest in episode order (Play always goes in episode order).
Selecting a pill and pressing Select opens the season page.

### 4. The season page

Name, episode count, total length; **Play**, **Shuffle**, **Mark season watched**; the episodes.

### 5. Getting there

From a series card in the grid, Recommended rows (On Deck cards open the episode, with a
"Go to series" action), the video page ("Go to series" when the video is an episode), and deep
link `maktaba://series/{id}` (Epic 48 FR-9).

## Acceptance criteria

- **Given** a library of 3 series and 4 loose videos, **when** Show is set to Series, **then** the
  grid has 7 cards: 3 series and 4 videos.
- **Given** Show Series and the filter Unwatched, **then** a fully watched series is not shown.
- **Given** a series with an ongoing season, **then** its episodes list newest first, and Play on
  the season starts at its oldest unwatched episode.
- **Given** an episode's video page, **when** Go to series is chosen, **then** the series page
  opens with that episode's season selected and the episode focused.
- **Given** a viewer without access to the series' library, **then** `GET /api/videos?group=series`
  lists none of it and the series page answers not available.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | Go unit + Postgres | API | 3 series, 4 loose | `group=series` | 7 cards; counts |
| T02 | Go unit | API | filters | `watched=false&group=series` | watched series absent |
| T03 | Go role test | API | viewer of another library | `group=series` | none of it |
| T04 | Compose UI test | Android TV | mock grid | Show → Series | series cards |
| T05 | Compose UI test | Android TV | series mock | open; Down to seasons; Select | season page |
| T06 | Compose UI test | Android TV | episode video page | Go to series | season selected, episode focused |
| T07 | XCUITest (`XCUIRemote`) | Apple TV | `MockHome` series fixture | open series; press Select on a season pill | season page |
| T08 | XCUITest | Apple TV | ongoing season fixture | open | newest first |
| T09 | XCUITest | Apple TV | Arabic | series page | mirrored; `Season {n}` in Arabic |
| T10 | Roborazzi | Android TV | series page en/ar dark | render | screenshots |
| T11 | i18n | both | new keys | missing-Arabic tests | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `library.show` | Show | العرض |
| `library.show.videos` | Videos | المقاطع |
| `library.show.series` | Series | السلاسل |
| `series.season` | Season {n} | الموسم {n} |
| `series.episodes` | {n} episodes | {n} حلقة |
| `series.seasons` | {n} seasons | {n} موسم |
| `series.markWatched` | Mark watched | تعليم كمُشاهَد |
| `series.markSeasonWatched` | Mark season watched | تعليم الموسم كمُشاهَد |
| `series.goTo` | Go to series | الانتقال إلى السلسلة |
| `series.untitled` | Untitled series | سلسلة بلا عنوان |
| `series.notFound` | Series not found. | السلسلة غير موجودة. |

Arabic plurals use each platform's plural rules (Android `plurals`, xcstrings variations).

## Edge cases

- **A series in two libraries**: never (a series has one `library_id`).
- **A series with one season**: no pills; the episodes list directly.
- **An episode with no number**: listed after the numbered ones by date.

## Build plan

| File | Change |
|---|---|
| `api/internal/handlers/videos/videos.go`, `api/internal/handlers/browse/groups.go` | `group=series` |
| `apps/tv/android/.../ui/screens/library/LibraryGridTab.kt`, `ui/screens/series/` (new) | picker, series and season pages |
| `apps/tv/tvos/MaktabaTV/Views/Series/` (new) | `SeriesView.swift`, `SeasonView.swift` (reuse MaktabaKit's series models) |
| `apps/tv/tvos/MaktabaTVUITests/TVSeriesUITests.swift` (new), fixtures | T07–T09 |
| `apps/tv/android/app/src/test/.../SeriesTest.kt` (new) | T04–T06, T10 |
