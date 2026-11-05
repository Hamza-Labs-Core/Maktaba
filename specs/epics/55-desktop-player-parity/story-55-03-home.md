# Story 55.03 — Home: hero, rows, recommendations

> Epic 55 · Phase 2 · Depends on: 55.01, 55.02 · Needed by: 55.10

## Why

The Android and Apple Players open on Home: a featured hero, then rows (Continue Watching, On
Deck, What's On Now, Favorites, Recently Added, Recently Watched, pinned libraries, For You).
The desktop Player opens on a grid of every video. This story gives the desktop the same Home,
built from the same row builder.

## Description

### 1. Rows from `:client`

`homeRows(rails, recs, now, known)` and its row types move from
`apps/android/player/.../home/HomeRows.kt` into `:client` (`core.browse`), with their tests
(`HomeRowsTest`). `HomeCache` (last good rows per home) moves with it. A row's "See all"
target becomes a library id instead of the Android `Route`. The Android Player
imports them; its screens do not change.

### 2. `HomeState` on the desktop (`desktop/home/HomeState.kt`)

As the Android `HomeState`: reads `homeRails()`, `recommendations("desktop-home")` (only when
`recommendations` is on) and `LiveApi.now()` (only when `live_tv` is on) in parallel, builds the
rows, keeps the last good rows (shown with "Couldn't refresh — showing the last rows." when a
refresh fails), and refreshes when Home is shown again after five minutes. Kept with
`home.keep("home")` so switching sections does not reload it.

### 3. The screen (`desktop/home/HomeScreen.kt`)

- **Hero**: the featured video or new series, poster wide across the page, title, a Play (or
  Resume) button and a More button that opens its page.
- **Rows**: each a `Rail` of `PosterCard`s with a progress strip where there is progress. A
  video opens `Route.Video`; a series `Route.Series`; a channel `Route.Channel`; a row's "See
  all" opens its library. Continue Watching plays directly from where the person left it
  (`PlayRequest(startSec = null)`), as on Android.
- **Not interested** on a For You card (a right-click menu and a "More options" button):
  `dismissRecommendation(id)` through `ReadOrder.saving`; the card leaves at once and a read
  that began before it does not bring it back. A failure puts it back with "Could not tell the
  home. Try again."
- **Gate**: no For You row for a viewer when `recommendations` is off; an admin sees
  `FeatureOffNote("recommendations")` in its place. What's On Now follows `live_tv` the same way.
- **Empty**: "Nothing to watch yet" with the viewer's or the admin's explanation, as Android.
- **Loading**: placeholder cards with the shimmer; **error**: "Home could not load" and Try again.

### 4. Words

| Key | English | Arabic |
|---|---|---|
| `home.row.continue` | Continue Watching | تابع المشاهدة |
| `home.row.onDeck` | On Deck | الدرس التالي |
| `home.row.liveNow` | What's On Now | ما يُعرض الآن |
| `home.row.favorites` | Favorites | المفضلة |
| `home.row.recentlyAdded` | Recently Added | أضيف حديثًا |
| `home.row.recentlyWatched` | Recently Watched | شوهد مؤخرًا |
| `home.row.forYou` | For You | مقترح لك |
| `home.row.fromLibrary` | From your library | من مكتبتك |
| `home.hero.newSeries` | New series | سلسلة جديدة |
| `home.hero.featured` | Featured | مختار |
| `home.seeAll` | See all | عرض الكل |
| `home.moreOptions` | More options | خيارات أخرى |
| `home.notInterested` | Not interested | غير مهتم |
| `home.notInterestedFailed` | Could not tell the home. Try again. | تعذّر إبلاغ الجهاز الرئيسي. أعد المحاولة. |
| `home.stale` | Couldn't refresh — showing the last rows. | تعذّر التحديث — تُعرض آخر الصفوف. |
| `home.error` | Home could not load | تعذّر تحميل الرئيسية |
| `home.empty` | Nothing to watch yet | لا شيء للمشاهدة بعد |
| `home.emptyViewer` | Open Libraries to see the videos you may watch. | افتح المكتبات لترى المقاطع المتاحة لك. |
| `home.emptyAdmin` | Add a library on the home's web page and scan it; its videos show here. | أضف مكتبة من صفحة الجهاز الرئيسي على الويب وافحصها؛ ستظهر مقاطعها هنا. |

## Acceptance criteria

- **Given** a home with progress, favorites and new videos, **when** Home shows, **then** the
  hero and the rows appear in the Android order, with progress strips.
- **Given** Continue Watching, **when** a card is chosen, **then** the video plays from where
  it was left.
- **Given** a For You card, **when** Not interested is chosen, **then** the card leaves at once
  and does not come back from a read that began before.
- **Given** `recommendations` off, **when** a viewer opens Home, **then** there is no For You
  row; an admin sees the off note instead.
- **Given** Home loaded once, **when** a refresh fails, **then** the last rows stay with the
  stale line.
- **Given** Arabic, **when** Home shows, **then** rows scroll from the right and every label is
  Arabic.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (`:client`) | `HomeRowsTest` (moved) | as on Android | passes |
| T02 | unit | `HomeState`, MockWebServer | load | rows in order; recommendations not asked when off |
| T03 | unit | `HomeState`, a refresh answering 500 | refresh | last rows kept, `stale` true |
| T04 | latch | `HomeState`: a read held open, then Not interested, then the read answers with the card | release the read | the card stays gone |
| T05 | latch | Not interested fails | — | card back, sentence shown |
| T06 | screen | viewer and admin, `recommendations` off, `en`/`ar` | compose | no row / off note |
| T07 | screen | empty home, viewer and admin | compose | the two explanations |

## Edge cases

- **A pinned library removed** on the web: its row is gone on the next read.
- **A video in two rows** (Continue Watching and Recently Added): both cards show the same
  progress.
- **Live TV off** for a viewer: no What's On Now row and no channel cards.

## Build plan

| File | Change |
|---|---|
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/browse/HomeRows.kt`, `HomeCache.kt` (moved) and tests | Shared rows. |
| `apps/android/player/.../home/HomeState.kt`, `HomeScreen.kt` | Imports. |
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/home/HomeState.kt`, `HomeScreen.kt`, `Hero.kt` (new) | The page. |
| `.../desktop/home/HomeStrings.kt` | §4 words. |
| `apps/desktop-player/src/test/.../home/HomeStateTest.kt`, `HomeScreenTest.kt` (new) | T02–T07. |
