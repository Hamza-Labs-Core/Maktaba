# Story 56.05 — Sort, filter, letter jump and Categories on the TVs

> Epic 56 · Phases 1 and 3 · Depends on: 56.03 · Needed by: 56.02 (the view Play follows)

## Why

A TV library today is newest first, with no way to narrow it. Plex's Library tab has sort,
filters and a letter jump bar, and its Categories tab browses by metadata. Maktaba's grid
already has these filters on the web and the Players (`GET /api/videos`, `GET /api/videos/facets`).

## Description

### 1. Sort

A **Sort** button opens a list: Newest added, Oldest added, Newest published, A–Z, Z–A,
Longest, Shortest, Most played: the grid's existing `sort` values (`created_at`,
`published_at`, `title`, `duration_sec`, `plays`) with `order`. Kept per library on the device.

### 2. Filter

A **Filter** button opens a side panel (from the end edge; from the left in Arabic) with:
Unwatched / In progress / Watched, Language, Speaker, Topic (tag), Year, Length (`duration`),
Resolution, Favorites. Each is a list of values with counts from `GET /api/videos/facets`.
The button shows the number of active filters; **Clear filters** resets them. Back closes the
panel and keeps the choices.

### 3. Letter jump

A rail at the grid's end edge lists the letters that have items (`GET /api/videos/facets` already returns
`letters`): Arabic letters ا…ي first, then A…Z, then `#`. Focusing a
letter and pressing Select scrolls the grid to the first item with that letter (the grid's
`letter` filter used as a cursor seed, not as a filter). Only in A–Z and Z–A sorts.

### 4. Categories tab

`GET /api/libraries/{id}/categories` (new) returns `{speakers, topics, languages, years}`, each a
list of `{value, label, count, poster}` ordered by count, at most 50 per kind. The tab shows four
rows of tiles. Selecting a tile opens the Library tab with that filter set (and Back returns to
Categories).

## Acceptance criteria

- **Given** the Library tab, **when** Sort A–Z is chosen, **then** the grid is in A–Z order and
  the letter rail appears.
- **Given** Filter Speaker X and Unwatched, **then** the grid shows only X's unwatched videos, the
  Filter button shows 2, and Play in the header plays that view.
- **Given** an Arabic library sorted A–Z, **when** the letter ب is selected on the rail, **then**
  the first focused card begins with ب.
- **Given** the Categories tab, **when** a speaker tile is selected, **then** the Library tab opens
  filtered by that speaker.
- **Given** a viewer of another library, **then** `GET /api/libraries/{id}/categories` answers 404.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | Go unit + Postgres | API | seeded speakers, tags | categories | counts, order, 50 cap |
| T02 | Go role test | API | no session; other library's viewer | categories | 401; 404 |
| T03 | Go unit | API | Arabic and Latin titles | facets `letters` | order ا…ي, A…Z, # (a test on today's facet) |
| T04 | Compose UI test | Android TV | mock | Filter: speaker, unwatched | request has both; badge 2 |
| T05 | Compose UI test | Android TV | A–Z | rail Select ب | grid scrolled; focus on a ب title |
| T06 | Compose UI test | Android TV | categories | Select a speaker | Library tab, filter set; Back → Categories |
| T07 | XCUITest (`XCUIRemote`) | Apple TV | `MockHome` facets | open Filter, choose a language | grid request has it |
| T08 | XCUITest | Apple TV | Arabic | open Filter | panel from the left |
| T09 | XCUITest | Apple TV | categories fixture | select a topic | filtered Library tab |
| T10 | Roborazzi | Android TV | panel, rail en/ar dark | render | screenshots |
| T11 | i18n | both | new keys | missing-Arabic tests | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `library.sort` | Sort | ترتيب |
| `library.sort.newest` | Newest added | الأحدث إضافةً |
| `library.sort.oldest` | Oldest added | الأقدم إضافةً |
| `library.sort.az` | A–Z | أ–ي |
| `library.sort.za` | Z–A | ي–أ |
| `library.sort.longest` | Longest | الأطول |
| `library.sort.shortest` | Shortest | الأقصر |
| `library.sort.published` | Newest published | الأحدث نشرًا |
| `library.sort.plays` | Most played | الأكثر تشغيلًا |
| `library.filter` | Filter | تصفية |
| `library.filter.count` | Filter ({n}) | تصفية ({n}) |
| `library.filter.clear` | Clear filters | مسح التصفيات |
| `library.filter.unwatched` | Unwatched | غير مُشاهَد |
| `library.filter.inProgress` | In progress | قيد المشاهدة |
| `library.filter.watched` | Watched | مُشاهَد |
| `library.filter.language` | Language | اللغة |
| `library.filter.speaker` | Speaker | المتحدث |
| `library.filter.topic` | Topic | الموضوع |
| `library.filter.year` | Year | السنة |
| `library.filter.length` | Length | المدة |
| `library.filter.resolution` | Resolution | الدقة |
| `library.filter.favorites` | Favorites | المفضلة |
| `library.categories.speakers` | Speakers | المتحدثون |
| `library.categories.topics` | Topics | المواضيع |
| `library.categories.languages` | Languages | اللغات |
| `library.categories.years` | Years | السنوات |
| `library.empty.filtered` | Nothing matches these filters. | لا شيء يطابق هذه التصفيات. |

## Edge cases

- **A filter value that disappears** (its last video deleted): the chip stays until cleared,
  with a count of 0.
- **Newest published with no dates**: videos without `published_at` come last (the grid's NULLS LAST).
- **The rail in Series view**: letters of series names.

## Build plan

| File | Change |
|---|---|
| `api/internal/handlers/browse/categories.go` (new), `api/internal/handlers/browse/browse.go` | route |
| `api/internal/handlers/browse/facets.go` | letters ordered Arabic first |
| `apps/tv/android/.../ui/screens/library/{SortMenu,FilterPanel,LetterRail,CategoriesTab}.kt` (new) | UI |
| `apps/tv/tvos/MaktabaTV/Views/Library/{SortMenu,FilterPanel,LetterRail,CategoriesView}.swift` (new) | UI |
| tests as in the table | |
