# Series detail

- **Route:** `/series/:id` (deep link; `id` is URL-encoded into every API path)
- **Component:** `src/pages/Library/SeriesDetail.tsx`; uses `@ds` `Button`, `Badge`, `ProgressBar`, `EmptyState`, `ErrorState`, `useToast`.
- **API calls (all via `api` in `src/lib/api.ts`, fired in parallel on mount and on `id` change):**
  - `GET /api/series/{id}` → `SeriesHeader { id, name, poster_path?, year?, numbering }`. Errors are swallowed (header becomes `null`, title falls back to "Series").
  - `GET /api/series/{id}/episodes` → `{ seasons: [{ season, episodes: Episode[] }], numbering, next_episode?: Episode }`, `Episode = { video_id, season?, episode?, absolute_number?, title, thumbnail?, duration_sec?, progress_pct, watched }`. Its error drives the page ErrorState.
  - `GET /api/series/{id}/missing` → `{ gaps: [{ season, episode }], numbering }`. Errors swallowed (no ghost cells).
  - `POST /api/series/{id}/enrichment/accept-all` ("Accept all episode matches" button) → toast `series.acceptAll.done` or error toast.
- **Access:** signed-in user (`RequireAuth`). Server: reads are ACL-scoped; accept-all is a write that needs an editor (admin in v1, `api/internal/handlers/enrichment/enrichment.go` `canEdit`). The page shows the accept-all button to everyone.
- **Existing tests:** none (no `SeriesDetail.test.tsx`). Gaps: everything.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test? |
| ------ | ----------------------------------------------------------- | ------------- | -------- | ------------------------- |
| SED-01 | Header, seasons and episode grid render                     | Happy path    | P1       | No                        |
| SED-02 | "Continue watching" links to the next episode's player      | Happy path    | P1       | No                        |
| SED-03 | `seasons: null` / missing renders "No episodes" empty state | Empty state   | P1       | No                        |
| SED-04 | A season with `episodes: null` / missing does not crash     | Empty state   | P1       | No                        |
| SED-05 | `gaps: null` / missing renders no ghost cells               | Empty state   | P1       | No                        |
| SED-06 | Render crash shows route error fallback; Back still works   | Error         | P1       | No                        |
| SED-07 | Series in a library without ACL access                      | Permissions   | P1       | No                        |
| SED-08 | Non-admin and accept-all                                    | Permissions   | P1       | No                        |
| SED-09 | Signed-out user is sent to `/login`                         | Permissions   | P1       | No                        |
| SED-10 | Unknown series id (404)                                     | Error         | P2       | No                        |
| SED-11 | Episodes 500 / network failure / malformed JSON             | Error         | P2       | No                        |
| SED-12 | Header fails but episodes load: fallback title              | Error         | P2       | No                        |
| SED-13 | Loading text until episodes arrive                          | Loading       | P2       | No                        |
| SED-14 | Missing episodes show as ghost cells per season             | Happy path    | P2       | No                        |
| SED-15 | Season 0 shows as "Specials"; absolute numbering badge      | Happy path    | P2       | No                        |
| SED-16 | Episode number label: E01 / #12 / none                      | Happy path    | P2       | No                        |
| SED-17 | Watched and in-progress markers                             | Happy path    | P2       | No                        |
| SED-18 | Accept-all success and error toasts; button loading         | Happy path    | P2       | No                        |
| SED-19 | Navigating to another series clears the old one             | Navigation    | P2       | No                        |
| SED-20 | Episode click opens video detail; Back returns              | Navigation    | P2       | No                        |
| SED-21 | Id with special characters is URL-encoded                   | Navigation    | P3       | No                        |
| SED-22 | Arabic UI and Arabic titles                                 | RTL           | P2       | No                        |
| SED-23 | Phone width: episode grid fits                              | Responsive    | P3       | No                        |
| SED-24 | Light/dark theme: ghost cells and markers readable          | Theme         | P3       | No                        |
| SED-25 | Keyboard and a11y: headings, links, missing cells           | Accessibility | P3       | No                        |

## Cases

### SED-01 — Header, seasons and episode grid render

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/series/s1` → `{ "id": "s1", "name": "Breaking Bad", "year": 2008, "numbering": "season", "poster_path": "https://img.example/bb.jpg" }`; `GET /api/series/s1/episodes` → `{ "numbering": "season", "seasons": [ { "season": 1, "episodes": [ { "video_id": "e1", "season": 1, "episode": 1, "title": "Pilot", "thumbnail": "/api/videos/e1/poster", "progress_pct": 0, "watched": true }, { "video_id": "e2", "season": 1, "episode": 2, "title": "Cat's in the Bag", "progress_pct": 0, "watched": false } ] }, { "season": 2, "episodes": [ { "video_id": "e3", "season": 2, "episode": 1, "title": "737", "progress_pct": 0, "watched": false } ] } ] }`; `GET /api/series/s1/missing` → `{ "gaps": [], "numbering": "season" }`.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** h1 "Breaking Bad" (`dir="auto"`), badge "2008", poster img; h2 "Season 1" with 2 episode links (`/videos/e1`, `/videos/e2`) and h2 "Season 2" with 1. Labels "E01", "E02", "E01". Button "Accept all episode matches". No "Continue watching" (no `next_episode`).

### SED-02 — "Continue watching" links to the next episode's player

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** as SED-01 plus `"next_episode": { "video_id": "e2", "title": "Cat's in the Bag", "progress_pct": 40, "watched": false }`.
- **Steps:**
  1. Open `/series/s1`; click "Continue watching".
- **Expected:** link "▶ Continue watching" with `href="/videos/e2/watch"`; click lands on the player route. Wanted: it resumes at the saved position (link carries no `?t=`, and the player does not read a saved position — see VPL-09).

### SED-03 — `seasons: null` / missing renders "No episodes" empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** episodes → (a) `{ "seasons": null, "numbering": "season" }`, (b) `{ "numbering": "season" }`, (c) `{ "seasons": [] }`. List fields: `seasons`.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** EmptyState "No episodes in this series yet."; header still shows; no crash.

### SED-04 — A season with `episodes: null` / missing does not crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** episodes → `{ "numbering": "season", "seasons": [ { "season": 1, "episodes": null }, { "season": 2 }, { "season": 3, "episodes": [ { "video_id": "e9", "episode": 1, "title": "Ok", "progress_pct": 0, "watched": false } ] } ] }`; gaps → `{ "gaps": [ { "season": 1, "episode": 4 } ] }`. Nested list field: `seasons[].episodes`.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** the page renders: Season 1 shows only the ghost cell "E04 Missing", Season 2 shows an empty row, Season 3 shows "Ok". (Today `s.episodes.map` throws on `null` and the whole page goes blank — likely bug, the Live TV `hosts: null` class.)

### SED-05 — `gaps: null` / missing renders no ghost cells

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** missing → (a) `{ "gaps": null }`, (b) `{}`, (c) 500, (d) `{ "gaps": [ null ] }`. List fields: `gaps`.
- **Steps:**
  1. Open `/series/s1` with the SED-01 episodes.
- **Expected:** (a)–(c) episodes render, no ".mkt-episode--missing" cells. (d) wanted: the bad entry is skipped (today `g.season` on `null` throws inside `useMemo` and crashes the page — likely bug).

### SED-06 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** full app routes inside `AppShell`; episodes response from SED-04 (`episodes: null`), which throws during render today. Start on `/series`.
- **Steps:**
  1. Open `/series`; click a series card to `/series/s1`.
  2. Press browser Back.
  3. Click the "Library" nav link.
- **Expected:** step 1 shows a route-level error fallback inside the shell (not a blank page). Step 2 shows the Series grid without a reload. Step 3 shows the Library.

### SED-07 — Series in a library without ACL access

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** non-admin; the three GETs answer 404 (or 403) problem+json `{ "title": "series not found", "status": 404 }`.
- **Steps:**
  1. Open `/series/<id in a library the user cannot see>` directly.
- **Expected:** no episode titles, thumbnails or names leak; ErrorState shows. Wanted: a "not found" kind with a link back to `/series` (today it is `kind="server"`, title "Something went wrong.", no action). E2e against the real API: confirm the server really refuses all three endpoints.

### SED-08 — Non-admin and accept-all

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `useAuth` user `{ is_admin: false }`; SED-01 data; `POST /api/series/s1/enrichment/accept-all` → 403 `{ "title": "forbidden", "status": 403 }`.
- **Steps:**
  1. Open `/series/s1`.
  2. If the button is shown, click it.
- **Expected:** wanted: "Accept all episode matches" is hidden for non-admins (the server refuses non-editors). Today it is shown to everyone; clicking shows an error toast "forbidden" (currently unhandled in the UI — likely bug). Admin variant: button visible.

### SED-09 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** full `App`; `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** redirected to `/login` with `state.from = "/series/s1"`; no series requests sent; after sign-in the user returns to `/series/s1`.

### SED-10 — Unknown series id (404)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** all three GETs → 404 problem+json `{ "title": "series not found", "status": 404 }`.
- **Steps:**
  1. Open `/series/does-not-exist`.
- **Expected:** ErrorState with description "series not found". Wanted: not-found wording and a link back to Series (see SED-07).

### SED-11 — Episodes 500 / network failure / malformed JSON

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** episodes → (a) 500 `{ "title": "boom", "status": 500 }`; (b) fetch rejects; (c) 200 `application/json` body `{bad`. Header and missing succeed.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** ErrorState "Something went wrong." with description "boom" (a) or "Something went wrong." (b, c). Wanted: a "Try again" button (none today).

### SED-12 — Header fails but episodes load: fallback title

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/series/s1` → 500; episodes and missing as SED-01.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** h1 "Series" (`series.title`), no poster, no year badge; seasons render normally.

### SED-13 — Loading text until episodes arrive

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** header resolves at once; episodes never resolves.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** "Loading…" shows (header is not rendered yet); no empty state.

### SED-14 — Missing episodes show as ghost cells per season

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** SED-01 episodes; gaps → `{ "gaps": [ { "season": 1, "episode": 3 }, { "season": 1, "episode": 5 }, { "season": 4, "episode": 1 } ] }`.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** Season 1 has ghost cells "E03 Missing" and "E05 Missing" (`aria-disabled="true"`, not links). Season 4 does not exist in `seasons`, so its gap is dropped — wanted: a Season 4 row with the ghost cell (currently not shown — likely bug). Wanted: ghost cells sorted in episode order among real episodes (today they are appended after all real ones).

### SED-15 — Season 0 shows as "Specials"; absolute numbering badge

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** episodes → `{ "numbering": "absolute", "seasons": [ { "season": 0, "episodes": [ ... ] }, { "season": 1, "episodes": [ ... ] } ] }`.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** h2 "Specials" and "Season 1"; badge "Absolute numbering" in the header.

### SED-16 — Episode number label: E01 / #12 / none

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** episodes `{ episode: 1 }`, `{ episode: 12 }`, `{ episode: null, absolute_number: 12 }`, `{ episode: 0 }`, neither set.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** "E01", "E12", "#12", "E00", and an empty label (no "undefined"/"null" text).

### SED-17 — Watched and in-progress markers

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** episodes: `{ watched: true, progress_pct: 100 }`, `{ watched: false, progress_pct: 42.6 }`, `{ watched: false, progress_pct: 0 }`, `{ watched: false, progress_pct: null }`.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** first shows the "✓" marker and no progress bar; second shows a progress bar labelled "43% watched"; third and fourth show neither. Wanted: the watched state is exposed to screen readers (the ✓ is `aria-hidden` and there is no text equivalent).

### SED-18 — Accept-all success and error toasts; button loading

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** admin; `POST /api/series/s1/enrichment/accept-all` (a) resolves after 100 ms with 204, (b) 500 `{ "title": "accept failed" }`. Render inside `ToastProvider`.
- **Steps:**
  1. Open `/series/s1`; click "Accept all episode matches".
- **Expected:** button shows loading and does not send a second POST on a double click while pending. (a) success toast "Episode matches applied."; (b) error toast "accept failed". Wanted: a confirm step (it changes metadata for every episode) and the episode titles refresh after success (today no reload).

### SED-19 — Navigating to another series clears the old one

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `/series/s1` loaded as SED-01; for `s2` the header resolves after 300 ms with name "Other", episodes after 10 ms.
- **Steps:**
  1. From `/series/s1`, navigate in-app to `/series/s2` (e.g. via a test nav button).
- **Expected:** "Loading…" then s2's episodes; the h1 never shows "Breaking Bad" with s2's episodes. (Today `header` is not reset on `id` change and requests have no cancel flag, so s1's name shows over s2's episodes until s2's header lands — likely bug.)

### SED-20 — Episode click opens video detail; Back returns

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** full routes; SED-01 data; `/api/videos/e1` mocked.
- **Steps:**
  1. Open `/series/s1`; click episode "Pilot".
  2. Press browser Back.
- **Expected:** lands on `/videos/e1`; Back shows the series page again with its seasons.

### SED-21 — Id with special characters is URL-encoded

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** route `/series/a%2Fb%3Fx` (id `a/b?x`).
- **Steps:**
  1. Open the URL.
- **Expected:** requests go to `/api/series/a%2Fb%3Fx`, `/api/series/a%2Fb%3Fx/episodes`, `/api/series/a%2Fb%3Fx/missing`.

### SED-22 — Arabic UI and Arabic titles

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; header name "باب الحارة"; episode titles in Arabic; season 2.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** `<html dir="rtl">`; h1 and episode titles have `dir="auto"`; "Season 2", "Specials", "Missing", "Continue watching" use `ar.json` strings; the ▶ glyph and grid order read correctly right to left. Note "E01" is built in code, not translated — confirm it is acceptable in Arabic or move it to i18n.

### SED-23 — Phone width: episode grid fits

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e 375x812; 3 seasons of 20 episodes, long titles.
- **Steps:**
  1. Open `/series/s1`.
- **Expected:** no horizontal page scroll; header poster, title and both action buttons stack; buttons at least 44 px tall.

### SED-24 — Light/dark theme: ghost cells and markers readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e; light and dark; data with gaps, watched and in-progress episodes.
- **Steps:**
  1. Open `/series/s1` in each theme.
- **Expected:** ghost cells look disabled but text is still readable; ✓ marker and progress bar visible on thumbnails in both themes.

### SED-25 — Keyboard and a11y: headings, links, missing cells

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** SED-14 data.
- **Steps:**
  1. Tab through the page.
- **Expected:** order: Continue watching, Accept all, then episode links season by season; ghost cells are skipped (not focusable). Heading levels h1 then h2 per season. Each episode link's accessible name includes its number and title. Toasts are announced (live region from `ToastProvider`).
