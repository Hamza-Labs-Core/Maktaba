# Story 55.06 — The series page and the video page

> Epic 55 · Phase 2 · Depends on: 55.01, 55.02, 55.05 · Needed by: 55.08, 55.10

## Why

On the desktop, choosing a video plays it at once: there is no page to see what it is, resume
it, start it over, mark it watched or add it to favorites, and series have no page at all. The
Android and Apple Players have both pages (stories 48.27 and 48.28). This story brings them.

## Description

### 1. Series page (`desktop/libraries/SeriesPage.kt`)

As the Android `SeriesPage`: `series(id)` and `seriesEpisodes(id, sort)`.
- Header: the series' poster, name, episodes and length, **Continue watching** (the next
  unwatched episode, from where it was left).
- Seasons as tabs (or one list when there is one season); episodes as rows with their poster,
  "Episode {0}", length, a progress strip and a Watched mark.
- **Mark watched / Mark not watched** on an episode, and **Mark the season watched / not
  watched**: `markWatched(ids, watched)` through `ReadOrder.saving`, shown at once.
- Choosing an episode opens its video page.

### 2. Video page (`desktop/video/VideoPage.kt`)

As the Android `VideoScreen`: `videoDetail(id)` and `myVideo(id)`.
- Wide window: the poster on the start side, details beside it; narrow: one column.
- Title, series and "Season {0}, Episode {1}", year, length, language, speakers, description.
- **Play** (from the start when never played), **Resume from 12:04** and **Play from start**
  when there is a saved position: `home.play(PlayRequest(id, title, duration, startSec))`.
- **Favorite** (a toggle) and **Mark watched** (a toggle): `setFavorite` / `markWatched`
  through `ReadOrder.saving`; a failure reverts with "Couldn't save that. Try again."
- **Chapters**: `chapters(id)` as a list (time and title); a chapter plays from its start.
  Gate: `chapters` off hides the list from a viewer; an admin reads the off note.
- **From the transcript**: when the page was opened from a transcript hit, that passage with
  "Play from {0}".
- **Next episodes**: the following episodes of the series as a `Rail`.
- **Playlists** the video is in (`MyVideo.playlists`), each opening its playlist in Profile
  (55.09).
- Gone or not allowed: "This video isn't available" and its detail.

### 3. Words

| Key | English | Arabic |
|---|---|---|
| `series.continue` | Continue watching | متابعة المشاهدة |
| `series.season` | Season {0} | الموسم {0} |
| `series.episode` | Episode {0} | الحلقة {0} |
| `series.markWatched` | Mark watched | تعليم كمُشاهَد |
| `series.markUnwatched` | Mark not watched | تعليم كغير مُشاهَد |
| `series.markSeason` | Mark the season watched | تعليم الموسم كمُشاهَد |
| `series.unmarkSeason` | Mark the season not watched | تعليم الموسم كغير مُشاهَد |
| `series.watched` | Watched | مُشاهَد |
| `series.untitled` | Untitled series | سلسلة بلا عنوان |
| `video.play` | Play | تشغيل |
| `video.resumeAt` | Resume from {0} | استئناف من {0} |
| `video.fromStart` | Play from start | التشغيل من البداية |
| `video.favorite` | Favorite | المفضلة |
| `video.markWatched` | Mark watched | تمييز كمُشاهَد |
| `video.chapters` | Chapters | الفصول |
| `video.transcript` | From the transcript | من النص المفرَّغ |
| `video.nextEpisodes` | Next episodes | الحلقات التالية |
| `video.seasonEpisode` | Season {0}, Episode {1} | الموسم {0}، الحلقة {1} |
| `video.playFrom` | Play from {0} | التشغيل من {0} |
| `video.playlists` | In playlists | في قوائم التشغيل |
| `video.saveFailed` | Couldn't save that. Try again. | تعذّر الحفظ. حاول مرة أخرى. |
| `video.notAvailable` | This video isn't available | هذا الفيديو غير متاح |
| `video.notAvailableDetail` | It may have been removed, or your account can't see it. | ربما حُذف، أو لا يملك حسابك صلاحية مشاهدته. |

## Acceptance criteria

- **Given** a video watched to 12:04, **when** its page opens, **then** it shows "Resume from
  12:04" and "Play from start", and Resume plays from 12:04.
- **Given** the page, **when** Favorite is chosen, **then** it shows on at once, stays on when
  an older read answers, and the video is in Profile's Favorites.
- **Given** chapters, **when** one is chosen, **then** the video plays from its start.
- **Given** `chapters` off, **when** a viewer opens the page, **then** there is no chapter list;
  an admin reads the off note.
- **Given** a series, **when** "Mark the season watched" is chosen, **then** every episode of the
  season shows Watched and Continue watching moves to the next season.
- **Given** Arabic, **when** the page opens, **then** the poster is on the right and the times
  read left to right inside the Arabic labels.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `VideoPageState`, MockWebServer with a position | load | resume offered at that second |
| T02 | latch | a detail read held, Favorite on, the read answers off | release | stays on |
| T03 | latch | Mark watched fails | — | reverted, sentence shown |
| T04 | latch | `SeriesState`: an episodes read held, mark the season, read answers unwatched | release | stays watched |
| T05 | unit | `SeriesState` | Continue watching | the first unwatched episode, from its position |
| T06 | screen | `chapters` off, viewer and admin, `en`/`ar` | compose | hidden / note |
| T07 | screen | Resume | click | `play(PlayRequest(startSec = 724))` |

## Edge cases

- **A video that is also an episode** shows its series link and next episodes; one that is not
  shows neither.
- **Position near the end** (over 95%): Play from start only, as Android.
- **The home answers 404** for chapters (an old home): no chapter list, no error.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/libraries/SeriesPage.kt`, `SeriesState.kt` (new) | Series page. |
| `.../desktop/video/VideoPage.kt`, `VideoPageState.kt` (new) | Video page. |
| `.../desktop/libraries/LibrariesStrings.kt`, `.../desktop/video/VideoStrings.kt` | §3 words. |
| `apps/desktop-player/src/test/.../libraries/SeriesStateTest.kt`, `.../video/VideoPageStateTest.kt`, `VideoPageTest.kt` (new) | T01–T07. |
