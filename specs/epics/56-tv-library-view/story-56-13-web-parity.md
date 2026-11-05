# Story 56.13 — The web on the server queue, with smart playlists

> Epic 56 · Phase 4 · Depends on: 56.01, 56.08, 56.09 · Needed by: —

## Why

The web already has Play all and Shuffle (library grid, collections, playlists) and an Up next,
but its queue lives in one tab's `sessionStorage` (`web/src/lib/playQueue.ts`) and Shuffle is one
random page. Its 10-foot view (`/big`, Epic 46) has no Play or Shuffle at all. The web should
use the same queue as the apps, so a queue moves between the browser, the phone and the TV.

## Description

### 1. The server queue

`web/src/lib/playQueue.ts` keeps its interface (`playAll`, `shuffle`, `advanceTo`, `next`) but
calls 56.01's routes; `sessionStorage` keeps only the queue id for the tab. Existing callers
(`LibraryBrowser.tsx`, `PlaylistPage.tsx`, `CollectionPage.tsx`, `AutoCollectionPage.tsx`,
`UpNext.tsx`) are unchanged apart from the import. Play all no longer pages the grid in the
browser.

### 2. Series and seasons

The series page (`SeriesDetail.tsx`) gains **Shuffle** beside Continue, and a season's header
**Play** and **Shuffle**.

### 3. Library tabs

The library page's tabs (Home, Browse, Series, Collections) gain **Playlists** (56.10's
`?library=` list). A video's menu gains **Play next** and **Add to queue**.

### 4. Admin collection settings

From 56.08: the library settings' collection mode; the collection page's Show on Recommended, Show
on Home, Rename, Hide, Duplicate as curated; badges for YouTube and Plex collections.

### 5. `/big`

`bigscreen/screens/Libraries.tsx` and `Series.tsx` gain Play and Shuffle (focus order as README
§6.5, keyboard and TV-remote keys); `Watch.tsx` advances through the server queue and shows Up
next as 56.11.

### 6. Home

**Continue your queue** (56.11) on Home and `/big` Home.

## Acceptance criteria

- **Given** a library of 5,000 videos, **when** Shuffle is clicked, **then** one request makes the
  queue, and the 300th Next still plays a video not played before in that queue.
- **Given** a queue started in the browser, **when** the TV opens Home, **then** it offers to
  continue it.
- **Given** `/big` on a series, **when** Shuffle is chosen with the keyboard, **then** a shuffled
  series queue plays.
- **Given** a viewer, **then** the collection admin controls are not shown and the API refuses
  them (56.08's role tests).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (vitest) | mocked fetch | `playQueue.shuffle()` | POST once; window used |
| T02 | unit | callers | render with the new module | unchanged behaviour |
| T03 | unit | series page en/ar | render | Shuffle, season Play |
| T04 | unit | `/big` focus | arrow keys + Enter | Play/Shuffle reachable; queue POST |
| T05 | e2e (Playwright) | real home | shuffle library, Next ×5 | five distinct videos |
| T06 | e2e | real home | start a queue; read `current` | same queue |
| T07 | unit | ar locale, RTL tests | library tabs, menus | RTL; Arabic strings |
| T08 | i18n | en/ar parity test | new keys | pass |

## Strings

Reuses `browse.playAll` (Play all / تشغيل الكل), `browse.shuffle` (Shuffle / تشغيل عشوائي),
`nav.playlists`, and the keys of 56.02, 56.03, 56.08, 56.09, 56.11 and 56.12. New:

| Key | English | Arabic |
|---|---|---|
| `libraryPage.tab.playlists` | Playlists | قوائم التشغيل |
| `big.play` | Play | تشغيل |
| `big.shuffle` | Shuffle | تشغيل عشوائي |
| `series.shuffle` | Shuffle the series | شغّل السلسلة عشوائيًا |
| `series.season.play` | Play the season | شغّل الموسم |
| `series.season.shuffle` | Shuffle the season | شغّل الموسم عشوائيًا |

## Edge cases

- **Two tabs on one queue**: the second's move gets 409 `stale_queue`; it reloads the window and
  shows `queue.error.stale`.
- **An old tab with a `sessionStorage` queue from before**: read once, ignored, replaced by a server
  queue on the next Play.

## Build plan

| File | Change |
|---|---|
| `web/src/lib/playQueue.ts` | server queue |
| `web/src/pages/Library/{LibraryTabs,SeriesDetail}.tsx`, `web/src/components/UpNext.tsx` | tabs, series, Up next |
| `web/src/pages/Collections/*`, library settings | admin controls |
| `web/src/bigscreen/screens/{Home,Libraries,Series,Watch}.tsx` | `/big` |
| `web/src/i18n/{en,ar}.json` | strings |
