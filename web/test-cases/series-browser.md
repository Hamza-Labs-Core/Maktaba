# Series browser

- **Route:** `/series`
- **Component:** `src/pages/Library/SeriesBrowser.tsx` (inner `SeriesPoster`); uses `@ds` `Card`, `Badge`, `ProgressBar`, `EmptyState`, `ErrorState`.
- **API calls:** `GET /api/series?sort=<name|progress>` via `api.get` (`src/lib/api.ts`) → `{ items: SeriesItem[] }`, `SeriesItem = { id, name, poster_path?, poster_video_id?, library_id?, year?, numbering, season_count, episode_count, watched_count, in_progress }`. Posters: `poster_path` when it starts with `http(s)://` or `/api/`, else `/api/videos/{poster_video_id}/poster`, else a placeholder. Re-fetches when the sort changes.
- **Access:** signed-in user (`RequireAuth`). Server scopes series to libraries the user can access.
- **Existing tests:** `src/pages/Library/SeriesBrowser.test.tsx` — covers: a card renders from the list; an Arabic name has `dir="auto"`; empty state "No series detected yet". Gaps: errors, loading, null fields, sort, posters, progress, navigation, crash fallback. Note: the header comment promises a per-library filter (`library_id=`), but the page has none.

## Summary

| ID     | Title                                                     | Category      | Priority | Covered by existing test?      |
| ------ | --------------------------------------------------------- | ------------- | -------- | ------------------------------ |
| SER-01 | Grid shows a card per series with name, counts, progress  | Happy path    | P1       | Yes (`SeriesBrowser.test.tsx`) |
| SER-02 | `items: null` and missing `items` render the empty state  | Empty state   | P1       | Yes (`SeriesBrowser.test.tsx`) |
| SER-03 | Null / missing numeric and optional fields do not crash   | Empty state   | P1       | Yes (`SeriesBrowser.test.tsx`) |
| SER-04 | Empty list shows "No series detected yet"                 | Empty state   | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-05 | Render crash shows route error fallback; Back still works | Error         | P1       | Yes (`SeriesBrowser.test.tsx`) |
| SER-06 | Signed-out user is sent to `/login`                       | Permissions   | P1       | Yes (`SeriesBrowser.test.tsx`) |
| SER-07 | Only series from accessible libraries are shown           | Permissions   | P1       | No (e2e-only)                  |
| SER-08 | Loading text while pending                                | Loading       | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-09 | Server error (500 / 403) shows ErrorState                 | Error         | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-10 | Network failure and malformed JSON show generic error     | Error         | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-11 | Error state offers a way to retry                         | Error         | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-12 | Sort change re-fetches with `sort=progress`               | Navigation    | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-13 | Fast sort changes: last response wins                     | Navigation    | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-14 | Poster source rules and broken-poster fallback            | Happy path    | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-15 | Card links to `/series/:id`; Back returns                 | Navigation    | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-16 | Year badge rules (`year` 0 / missing)                     | Empty state   | P3       | No                             |
| SER-17 | Arabic UI and Arabic series names                         | RTL           | P2       | Yes (`SeriesBrowser.test.tsx`) |
| SER-18 | Phone width: grid fits, no horizontal scroll              | Responsive    | P3       | No                             |
| SER-19 | Light/dark theme readability                              | Theme         | P3       | No                             |
| SER-20 | Keyboard and a11y: sort label, link names, progress label | Accessibility | P3       | No                             |

## Cases

### SER-01 — Grid shows a card per series with name, counts, progress

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** `GET /api/series?sort=name` → `{ "items": [ { "id": "s1", "name": "Breaking Bad", "numbering": "season", "season_count": 5, "episode_count": 62, "watched_count": 31, "in_progress": 1, "year": 2008, "poster_video_id": "v9" } ] }`
- **Steps:**
  1. Open `/series`.
- **Expected:** heading "Series"; request URL is exactly `/api/series?sort=name`; one `li` inside `ul[role="list"]` with a link `href="/series/s1"`; name "Breaking Bad"; badge "2008" (no grouping comma); badge "5 · 62"; a progress bar at 50 with label "31 of 62 watched".

### SER-02 — `items: null` and missing `items` render the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** `GET /api/series` → (a) `{ "items": null }`, (b) `{}`. List fields read: `items`.
- **Steps:**
  1. Open `/series`.
- **Expected:** EmptyState "No series detected yet" / "Series appear here once episodes are grouped across your libraries." No crash, no blank page.

### SER-03 — Null / missing numeric and optional fields do not crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** items: `{ "id": "s1", "name": "A", "numbering": "season", "season_count": null, "episode_count": 0, "watched_count": 0, "in_progress": 0 }`, `{ "id": "s2", "name": "", "numbering": "absolute", "episode_count": null, "watched_count": null }`, `{ "id": "s3", "name": null, "poster_path": null, "poster_video_id": null }`.
- **Steps:**
  1. Open `/series`.
- **Expected:** all three cards render; progress is 0 (not NaN) when `episode_count` is 0 or null; counts badge does not show "NaN"; placeholder poster (`.mkt-series-card__poster--ph`) shows; empty/null name gives an empty but clickable card (wanted: a fallback label such as the series id).

### SER-04 — Empty list shows "No series detected yet"

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes — `SeriesBrowser.test.tsx` "shows an empty state when there are no series".
- **Preconditions / data:** `GET /api/series` → `{ "items": [] }`.
- **Steps:**
  1. Open `/series`.
- **Expected:** EmptyState title and description from `series.empty.*`; the sort select is still shown.

### SER-05 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** full app routes inside `AppShell`; mock `@ds/components/ProgressBar/ProgressBar` (or `Card`) to throw for this page, or return `{ "items": "oops" }` (string → `.map` is not a function).
- **Steps:**
  1. Open `/library`, click the "Series" nav link.
  2. Press browser Back.
  3. Click another nav link (e.g. "Search").
- **Expected:** step 1 shows a route-level error fallback inside the shell, not a blank page. Steps 2 and 3 render the target page without a reload. (Also note `{ "items": "oops" }` passes `?? []`; wanted: the page treats a non-array as empty.)

### SER-06 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** full `App`; `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/series`.
- **Expected:** redirected to `/login` with `state.from = "/series"`; no `GET /api/series` sent.

### SER-07 — Only series from accessible libraries are shown

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e against the real API: non-admin user with ACL on library A only; series exist in A and B.
- **Steps:**
  1. Sign in as the user; open `/series`.
- **Expected:** only series from library A appear; opening `/series/<id of a B series>` directly does not show its episodes (see SED-07).

### SER-08 — Loading text while pending

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** `GET /api/series` never resolves.
- **Steps:**
  1. Open `/series`.
- **Expected:** "Loading…" (`common.loading`) shows; no empty state or error. Wanted: the loading text is in a `role="status"` region (currently a plain `<p>`).

### SER-09 — Server error (500 / 403) shows ErrorState

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** (a) 500 problem+json `{ "title": "list series failed", "status": 500 }`; (b) 403 `{ "title": "forbidden", "status": 403 }`.
- **Steps:**
  1. Open `/series`.
- **Expected:** ErrorState "Something went wrong." with the problem title as description; no empty state and no loading text at the same time.

### SER-10 — Network failure and malformed JSON show generic error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** (a) fetch rejects; (b) 200 `application/json` body `<html>`.
- **Steps:**
  1. Open `/series`.
- **Expected:** ErrorState with description "Something went wrong."; no uncaught rejection.

### SER-11 — Error state offers a way to retry

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** first call 500, second call returns one series.
- **Steps:**
  1. Open `/series`; wait for the error.
  2. Look for "Try again".
- **Expected:** wanted: a "Try again" button (`common.retry`) that re-fetches and shows the series. Currently the ErrorState has no action; the only way is changing the sort (currently unhandled — likely bug).

### SER-12 — Sort change re-fetches with `sort=progress`

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** `sort=name` → `[A, B]`; `sort=progress` → `[B, A]`.
- **Steps:**
  1. Open `/series`; select "Progress" in the select named "Sort".
- **Expected:** a second request `/api/series?sort=progress`; "Loading…" shows briefly; cards render in server order B, A. Wanted: the sort is kept in the URL (`?sort=progress`) so Back/refresh keep it (currently component state only — resets on reload).

### SER-13 — Fast sort changes: last response wins

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** `sort=progress` resolves after 300 ms with `[P]`; the following `sort=name` resolves after 10 ms with `[N]`.
- **Steps:**
  1. Open `/series`; select "Progress", then quickly "Name".
- **Expected:** final grid shows `[N]` with select "Name". (The effect has no cancel flag, so the slow `progress` response overwrites it — likely bug.)

### SER-14 — Poster source rules and broken-poster fallback

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** items: (a) `poster_path: "https://img.example/p.jpg"`; (b) `poster_path: "/api/series/s2/poster"`; (c) `poster_path: "/mnt/media/poster.jpg"`, `poster_video_id: "v3"`; (d) neither.
- **Steps:**
  1. Open `/series`; fire `error` on image (a).
- **Expected:** (a) `img src="https://img.example/p.jpg"`; (b) src `/api/series/s2/poster`; (c) src `/api/videos/v3/poster` (filesystem path never used); (d) placeholder. After the error (a) shows the placeholder. All posters have `alt=""`.

### SER-15 — Card links to `/series/:id`; Back returns

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** full routes; one series `s1`; `/api/series/s1*` mocked.
- **Steps:**
  1. Open `/series`; click the card.
  2. Press browser Back.
- **Expected:** lands on `/series/s1` (Series detail). Back renders the grid again.

### SER-16 — Year badge rules (`year` 0 / missing)

- **Priority:** P3
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** items with `year: 0`, `year` missing, `year: 1999`.
- **Steps:**
  1. Open `/series`.
- **Expected:** no year badge for 0 or missing; "1999" for the last. (Today `{s.year && ...}` prints a bare "0" for `year: 0` — likely bug.)

### SER-17 — Arabic UI and Arabic series names

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`SeriesBrowser.test.tsx`)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; item `{ "name": "باب الحارة", "season_count": 11, "episode_count": 400, "watched_count": 12 }`.
- **Steps:**
  1. Open `/series`.
- **Expected:** `<html dir="rtl">`; heading, sort label and options come from `ar.json`; counts badge uses Arabic number formatting; the progress label uses the Arabic `series.progress.label` (note: `watched`/`total` are passed as `String(...)`, so they are not localized digits while the badge is — check they are consistent); grid flows right to left.

### SER-18 — Phone width: grid fits, no horizontal scroll

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e at 375x812; 12 series with long names.
- **Steps:**
  1. Open `/series`.
- **Expected:** no horizontal page scroll; long names wrap or ellipsize; the sort select stays on screen; bottom nav reachable.

### SER-19 — Light/dark theme readability

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e; `data-theme` light and dark.
- **Steps:**
  1. Open `/series` with items, then the empty and error states.
- **Expected:** name, badges, progress bar track/fill and the placeholder glyph are readable in both themes.

### SER-20 — Keyboard and a11y: sort label, link names, progress label

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** two series.
- **Steps:**
  1. Tab through the page.
- **Expected:** select has accessible name "Sort"; each card is one focusable link whose name includes the series name; focus ring visible; the progress bar exposes `role="progressbar"` with label "N of M watched".
