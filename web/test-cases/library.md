# Library browser

- **Route:** `/library` (all libraries) and `/library/:libraryId` (one library; `libraryId` is sent as `?library=`). `/` redirects to `/library`.
- **Component:** `src/pages/LibraryBrowser.tsx` (inner `StateBadge`, `Poster`, `formatDuration`); children: `src/components/WhatsOnNow.tsx`.
- **API calls:**
  - `GET /api/videos?sort=<updated_at|title>&limit=60[&library=<id>][&language=<lang>][&cursor=<next>]` via `api.get` in `src/lib/api.ts` → `{ items: Video[], next: string | null }`. `Video = { id, title?, filename, state, detected_language?, duration_sec?, size_bytes?, updated_at? }`.
  - `POST /api/libraries/{libraryId}/scan` (admin "Scan now" on the empty state; errors swallowed).
  - Posters: `<img src="/api/videos/{id}/poster">` with an `onError` fallback.
  - `GET /api/channels/now` via `channelsApi.now()` in `src/lib/channels.ts` → `{ server_time, items: NowEntry[] }` (WhatsOnNow rail; refreshes every 30 s; hides itself when empty).
- **Access:** signed-in user (`RequireAuth` in `src/lib/auth.tsx`). Server scopes videos to the user's libraries (no ACL → `{ items: [], next: null }`). "Scan now" shows only when `user.is_admin` and a `libraryId` is in the URL.
- **State in URL / storage:** `?sort=`, `?order=asc`, `?language=` (all `replace: true`); view mode in `localStorage["mkt:lib:view"]` (`grid` | `list`).
- **Upload / drop:** none on this page. File drop lives in `src/components/DesktopBridge.tsx` + `DropOverlay.tsx` (desktop shell only), so it is out of scope here.
- **Existing tests:** `src/pages/LibraryBrowser.test.tsx` — covers: request has `sort=updated_at` + `limit=60` and an Arabic title renders; "Load more" sends `cursor=<next>` and appends page 2; first-run empty state "No videos yet". `src/components/WhatsOnNow.test.tsx` covers the rail alone. Gaps: errors, retry, null fields, list view, sort/order/language filters, admin scan, posters, `/library/:libraryId`, RTL, crash fallback.

## Summary

| ID     | Title                                                        | Category      | Priority | Covered by existing test?                                    |
| ------ | ------------------------------------------------------------ | ------------- | -------- | ------------------------------------------------------------ |
| LIB-01 | Grid shows one card per video with title, duration, language | Happy path    | P1       | Partial (`LibraryBrowser.test.tsx`: only title checked)      |
| LIB-02 | `/library/:libraryId` sends `library=<id>`                   | Navigation    | P1       | No                                                           |
| LIB-03 | `items: null` and missing `items` render the empty state     | Empty state   | P1       | No                                                           |
| LIB-04 | Null optional video fields do not crash                      | Empty state   | P1       | No                                                           |
| LIB-05 | WhatsOnNow `items: null` / missing does not break the page   | Empty state   | P1       | No                                                           |
| LIB-06 | First-run empty state; admin sees "Scan now"                 | Permissions   | P1       | Partial (`LibraryBrowser.test.tsx`: text only, non-admin)    |
| LIB-07 | Non-admin never sees "Scan now"                              | Permissions   | P1       | No                                                           |
| LIB-08 | Library the user has no ACL access to                        | Permissions   | P1       | No                                                           |
| LIB-09 | Unknown or malformed `libraryId`                             | Navigation    | P1       | No                                                           |
| LIB-10 | Signed-out user is sent to `/login`                          | Permissions   | P1       | No                                                           |
| LIB-11 | Render crash shows route error fallback; Back still works    | Error         | P1       | No                                                           |
| LIB-12 | Loading skeleton while the list is pending                   | Loading       | P2       | No                                                           |
| LIB-13 | Server error (500 problem+json) shows ErrorState             | Error         | P2       | No                                                           |
| LIB-14 | Network failure and malformed JSON show generic error        | Error         | P2       | No                                                           |
| LIB-15 | "Try again" re-fetches the list                              | Error         | P2       | No                                                           |
| LIB-16 | Load more pages via `next`; button hides at the end          | Happy path    | P2       | Partial (`LibraryBrowser.test.tsx`: hide-at-end not checked) |
| LIB-17 | Load more does not duplicate videos                          | Happy path    | P1       | No                                                           |
| LIB-18 | Load more failure keeps loaded items and shows the error     | Error         | P2       | No                                                           |
| LIB-19 | Grid/list toggle and persistence in localStorage             | Happy path    | P2       | No                                                           |
| LIB-20 | Sort select and order toggle update URL and order            | Navigation    | P2       | No                                                           |
| LIB-21 | Language chips filter; "Clear filters" on filtered-out empty | Navigation    | P2       | No                                                           |
| LIB-22 | Deep link with `?sort=title&order=asc&language=ar`           | Navigation    | P2       | No                                                           |
| LIB-23 | Problem-state badges only for problem states                 | Happy path    | P2       | No                                                           |
| LIB-24 | Broken poster falls back to the placeholder                  | Empty state   | P3       | No                                                           |
| LIB-25 | Card click opens video detail; Back returns with filters     | Navigation    | P2       | No                                                           |
| LIB-26 | Arabic UI: rtl, Arabic strings, Arabic titles isolated       | RTL           | P2       | Partial (`LibraryBrowser.test.tsx`: Arabic title text only)  |
| LIB-27 | Phone width: no horizontal scroll, toolbar wraps             | Responsive    | P3       | No                                                           |
| LIB-28 | Light/dark theme readability                                 | Theme         | P3       | No                                                           |
| LIB-29 | Keyboard and a11y: toolbar labels, radios, order button name | Accessibility | P3       | No                                                           |

## Cases

### LIB-01 — Grid shows one card per video with title, duration, language

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `LibraryBrowser.test.tsx` "requests /api/videos with sort+limit and renders items" checks the URL and the title only.
- **Preconditions / data:** signed in (non-admin); `localStorage` empty; `GET /api/channels/now` → `{ "server_time": "", "items": [] }`; `GET /api/videos?...` → `{ "items": [ { "id": "v1", "title": "Intro", "filename": "a.mp4", "state": "ready", "duration_sec": 65, "detected_language": "en" }, { "id": "v2", "title": null, "filename": "b.mp4", "state": "ready", "duration_sec": 3725 } ], "next": null }`
- **Steps:**
  1. Open `/library`.
- **Expected:** heading "Library" (`nav.library`). Two `li.mkt-card` links: `href="/videos/v1"` shows "Intro", "1:05", "· en"; `href="/videos/v2"` falls back to filename "b.mp4" and shows "1:02:05". Request URL contains `sort=updated_at` and `limit=60` and no `library=`, `language=` or `cursor=`. No "Load more" button.

### LIB-02 — `/library/:libraryId` sends `library=<id>`

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** route `/library/:libraryId`; `libraryId = "3f1c2a4e-0000-4000-8000-000000000001"`; videos → one item.
- **Steps:**
  1. Open `/library/3f1c2a4e-0000-4000-8000-000000000001`.
  2. Navigate in-app to `/library/<another uuid>`.
- **Expected:** first request has `library=3f1c2a4e-...0001`. After step 2 a new request has the new id, the skeleton shows again in between, and the old list is not left on screen.

### LIB-03 — `items: null` and missing `items` render the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** run three times: `GET /api/videos` → (a) `{ "items": null, "next": null }`, (b) `{ "next": null }`, (c) `{ "items": [], "next": null }`. List fields read from this endpoint: `items`.
- **Steps:**
  1. Open `/library`.
- **Expected:** each run shows the first-run EmptyState "No videos yet" / "Add a library and run a scan to get started." No thrown error, no blank page, no language chip group, no "Load more".

### LIB-04 — Null optional video fields do not crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** videos → `{ "items": [ { "id": "v1", "title": null, "filename": "x.mp4", "state": "ready", "detected_language": null, "duration_sec": null }, { "id": "v2", "title": "", "filename": "y.mp4", "state": "weird_new_state" } ], "next": null }`
- **Steps:**
  1. Open `/library` in grid view, then switch to List.
  2. Choose sort "Title", then "Duration".
- **Expected:** grid shows "x.mp4" and "y.mp4" with no duration or language text and no badge. List view shows "—" in Duration, Language and State columns. Sorting by title or duration does not throw. Also try a row with `filename: null` and `title: null`: expected the row renders (empty title) and sort by Title does not throw (currently `localeCompare` on `undefined` would throw — likely bug).

### LIB-05 — WhatsOnNow `items: null` / missing does not break the page

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/channels/now` → (a) `{ "server_time": "2026-10-19T10:00:00Z", "items": null }`, (b) `{}`, (c) rejects with 500, (d) `{ "items": [ { "channel": null } ] }`. Videos → one item. List fields: `items`.
- **Steps:**
  1. Open `/library`.
- **Expected:** in every run the rail (`data-testid="whats-on-now-rail"`) is not rendered and the video grid renders normally. No console error from React.

### LIB-06 — First-run empty state; admin sees "Scan now"

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Partial — `LibraryBrowser.test.tsx` "shows the first-run empty state" (non-admin, `/library`, text only).
- **Preconditions / data:** `useAuth` user `{ is_admin: true }`; videos → `{ "items": [], "next": null }`; `POST /api/libraries/lib-1/scan` → 202.
- **Steps:**
  1. Open `/library/lib-1`.
  2. Click "Scan now".
  3. Repeat with the scan POST rejecting (500).
- **Expected:** "No videos yet" plus a "Scan now" button (`library.scanNow`). Click sends `POST /api/libraries/lib-1/scan`. On reject nothing crashes. Wanted: a toast or status saying the scan started / failed (currently no feedback at all — likely bug). On `/library` (no id) an admin sees no "Scan now".

### LIB-07 — Non-admin never sees "Scan now"

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** user `{ is_admin: false }`; videos → `{ "items": [], "next": null }`.
- **Steps:**
  1. Open `/library/lib-1`.
- **Expected:** "No videos yet" shows; no button named "Scan now"; no POST is sent.

### LIB-08 — Library the user has no ACL access to

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** non-admin user; the server returns `{ "items": [], "next": null }` for a library outside the user's ACL (see `api/internal/handlers/videos/videos.go`). Also run with the server answering `403` problem+json `{ "title": "forbidden", "status": 403 }`.
- **Steps:**
  1. Open `/library/<uuid of a library the user cannot see>`.
- **Expected:** no video from that library is shown. With the empty response, the page shows an empty state (wanted: a "no access / not found" message instead of the first-run "Add a library and run a scan" text, which is misleading — currently unhandled). With 403, the ErrorState "Something went wrong." shows the problem title "forbidden" (wanted: the `common.forbidden.*` style message).

### LIB-09 — Unknown or malformed `libraryId`

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** (a) `/library/00000000-0000-4000-8000-000000000000` (valid UUID, no such library) → `{ "items": [], "next": null }`; (b) `/library/not-a-uuid`.
- **Steps:**
  1. Open each URL.
- **Expected:** (a) empty state, not a crash. (b) wanted: a "library not found" state. Note: the server ignores a non-UUID `library` param and returns all the user's videos, so the page would list every video under a bogus URL (currently unhandled — likely bug). The test should mock the server to return all videos for (b) and assert the page does not pretend they belong to that library (expected to fail today).

### LIB-10 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** render the full `App` route tree; `GET /api/auth/me` (AuthProvider) → 401.
- **Steps:**
  1. Open `/library/lib-1?language=ar`.
- **Expected:** redirected to `/login`; no `GET /api/videos` request. After signing in, the user lands back on `/library/lib-1` (note: `RequireAuth` stores only `location.pathname` as `from`, so `?language=ar` is lost — likely bug).

### LIB-11 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** full app routes inside `AppShell`; start on `/search`. Make `/library` throw during render, e.g. videos → `{ "items": [ { "id": "v1", "filename": null, "title": null, "state": "ready" } ] }` with `?sort=title` (localeCompare on undefined), or mock `WhatsOnNow` to throw.
- **Steps:**
  1. Open `/search`, then click the "Library" nav link (URL `/library?sort=title`).
  2. Press browser Back.
  3. Click the "Library" nav link again after fixing the mock, or click another nav link.
- **Expected:** step 1 shows a route-level error fallback inside the shell (nav still visible), not a blank page. Step 2 renders the Search page without a reload. Step 3 renders the target page normally.

### LIB-12 — Loading skeleton while the list is pending

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/videos` returns a never-resolving promise.
- **Steps:**
  1. Open `/library`.
- **Expected:** a `ul[aria-busy="true"]` with 6 skeleton cards. No EmptyState, no ErrorState, no "Load more". Toolbar is usable.

### LIB-13 — Server error (500 problem+json) shows ErrorState

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/videos` → 500, `Content-Type: application/problem+json`, body `{ "type": "about:blank", "title": "query videos failed", "status": 500 }`. Also run with 400 `{ "title": "invalid sort" }` (e.g. hand-typed `?sort=bogus`).
- **Steps:**
  1. Open `/library`.
- **Expected:** ErrorState titled "Something went wrong." with description "query videos failed", and a "Try again" button. The first-run empty state is NOT shown at the same time. For `?sort=bogus`, wanted: the page falls back to `updated_at` instead of sending `sort=undefined` (currently `SERVER_SORT["bogus"]` is `undefined` and is sent literally — likely bug).

### LIB-14 — Network failure and malformed JSON show generic error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** (a) `fetch` rejects with `TypeError("Failed to fetch")`; (b) 200 with `Content-Type: application/json` and body `{not json`.
- **Steps:**
  1. Open `/library`.
- **Expected:** both show ErrorState "Something went wrong." with description "Something went wrong." (`common.error`), and "Try again". No uncaught promise rejection.

### LIB-15 — "Try again" re-fetches the list

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** first `GET /api/videos` → 500; second → `{ "items": [ { "id": "v1", "filename": "a.mp4", "state": "ready" } ], "next": null }`.
- **Steps:**
  1. Open `/library`; wait for the error.
  2. Click "Try again".
- **Expected:** a second `GET /api/videos` is sent, the error goes away and "a.mp4" shows. (Currently the button only sets `?_r=<ts>`, which is not in `buildQuery`'s deps, so no new request is made — likely bug; this case should fail today.)

### LIB-16 — Load more pages via `next`; button hides at the end

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `LibraryBrowser.test.tsx` "paginates using the server `next` cursor" (does not check the count label, loading state or that the button goes away).
- **Preconditions / data:** page 1 → `{ "items": [v1], "next": "CURSOR_2" }`; `cursor=CURSOR_2` → `{ "items": [v2], "next": null }` delayed 100 ms.
- **Steps:**
  1. Open `/library`; click "Load more 1".
- **Expected:** button text is "Load more" plus the loaded count ("Load more 1"). While page 2 loads the button is in loading state and a second click sends no extra request. After it resolves, v1 and v2 both show and the button is gone. Page-2 request keeps the same `sort`, `library`, `language` params.

### LIB-17 — Load more does not duplicate videos

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** mimic the real server, which currently ignores `cursor` (`api/internal/handlers/videos/videos.go` never reads it): every request → `{ "items": [v1, v2], "next": "2026-10-19T10:00:00Z" }`.
- **Steps:**
  1. Open `/library`; click "Load more".
- **Expected:** each video id appears once (no duplicate cards, no React duplicate-key warning). Today the page appends the same page again — likely bug (server cursor + client has no de-dupe). Also an e2e case against the real API: with 70 videos, Load more must show videos 61–70.

### LIB-18 — Load more failure keeps loaded items and shows the error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** page 1 → `{ "items": [v1], "next": "C2" }`; `cursor=C2` → 500 `{ "title": "boom" }`.
- **Steps:**
  1. Open `/library`; click "Load more".
- **Expected:** v1 stays visible; an error message "boom" is shown; "Load more" is still there (cursor kept) so the user can retry. The page does not go blank.

### LIB-19 — Grid/list toggle and persistence in localStorage

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** videos → two items; `localStorage` empty.
- **Steps:**
  1. Open `/library`; click radio "List".
  2. Unmount and render again.
  3. Repeat with `localStorage.getItem` throwing.
- **Expected:** "Grid" radio `aria-checked="true"` at first. After step 1, a table (`aria-label="Library"`) with headers Title, Duration, Language, State; `localStorage["mkt:lib:view"] === "list"`. Step 2 opens in list view. Step 3: page still renders in grid view, no crash.

### LIB-20 — Sort select and order toggle update URL and order

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** videos → `[ {id:"a", title:"Beta", duration_sec:10}, {id:"b", title:"alpha", duration_sec:300}, {id:"c", title:"Gamma", duration_sec:5} ]`, `next: null`.
- **Steps:**
  1. Open `/library`; select "Title".
  2. Click the order button (name "Ascending").
  3. Select "Duration".
- **Expected:** step 1: URL has `?sort=title`, request has `sort=title`; order is desc (Gamma, Beta, alpha). Step 2: URL gains `order=asc`, button name becomes "Descending", order alpha, Beta, Gamma, no new request needed. Step 3: request is sent with `sort=updated_at` (no server duration sort); cards ordered by duration ascending (c, a, b). URL changes use replace, so Back leaves the page instead of stepping through sorts.

### LIB-21 — Language chips filter; "Clear filters" on filtered-out empty

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** first load → items with `detected_language` "ar" and "en"; request with `language=ar` → `{ "items": [], "next": null }`.
- **Steps:**
  1. Open `/library`; click chip "ar".
  2. Click "Clear filters".
- **Expected:** a group named "Language" with chips "ar", "en" (sorted). Step 1: URL `?language=ar`, request has `language=ar`, EmptyState "No videos match your filters." with "Clear filters" (no "Scan now", even for admin). Step 2: `language` removed from URL, list reloads. Note: chips are built from loaded videos, so after filtering to an empty result the chips disappear; wanted: the selected chip stays visible so it can be toggled off (currently the only way back is "Clear filters").

### LIB-22 — Deep link with `?sort=title&order=asc&language=ar`

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** videos for `language=ar` → two Arabic items.
- **Steps:**
  1. Open `/library?sort=title&order=asc&language=ar` directly.
- **Expected:** sort select shows "Title", order button name "Descending" (meaning current is asc), chip "ar" is selected, request contains `sort=title` and `language=ar`.

### LIB-23 — Problem-state badges only for problem states

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** items with `state`: `ready`, `transcoding`, `missing`, `corrupted`, `failed`, `ready_no_audio`, `superseded`.
- **Steps:**
  1. Open `/library` in grid, then list view.
- **Expected:** badges "Missing", "Corrupted", "Failed" (error tone), "No audio", "Replaced" (warn tone). `ready` and `transcoding` show no badge (list view shows "—").

### LIB-24 — Broken poster falls back to the placeholder

- **Priority:** P3
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** one video `v1`.
- **Steps:**
  1. Open `/library`; fire `error` on the `img[src="/api/videos/v1/poster"]`.
- **Expected:** the img is replaced by `div.mkt-card__poster--fallback[aria-hidden]`; card title still shows; the img has `alt=""` and `loading="lazy"`.

### LIB-25 — Card click opens video detail; Back returns with filters

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** full routes; videos → `[v1]`.
- **Steps:**
  1. Open `/library?sort=title&language=en`; click the v1 card.
  2. Press browser Back.
- **Expected:** step 1 lands on `/videos/v1`. Step 2 returns to `/library?sort=title&language=en` with the same sort and filter applied and the list re-fetched.

### LIB-26 — Arabic UI: rtl, Arabic strings, Arabic titles isolated

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Partial — `LibraryBrowser.test.tsx` renders an Arabic title but does not check `dir` or Arabic UI strings.
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; videos → `[ { "id": "v1", "title": "السيرة النبوية ( 18 )", "filename": "a.mp4", "state": "ready", "duration_sec": 65 } ]`.
- **Steps:**
  1. Open `/library`; switch to List.
- **Expected:** `<html dir="rtl" lang="ar">`; heading and toolbar use the `ar.json` strings for `nav.library`, `library.sort`, `library.view.grid`; card title and list cell have `dir="auto"` and show the title with the "( 18 )" not flipped. "Load more" count uses Arabic number formatting from `n()`. Toolbar order is mirrored.

### LIB-27 — Phone width: no horizontal scroll, toolbar wraps

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e, viewport 375x812; 20 videos with long titles; list view also.
- **Steps:**
  1. Open `/library` in grid, then list view.
- **Expected:** `document.documentElement.scrollWidth <= 375`; toolbar (sort, order, view) wraps and stays tappable; the list table scrolls inside its own container if needed; bottom nav is reachable.

### LIB-28 — Light/dark theme readability

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e; `localStorage["mkt:theme"]` = `light`, then `dark`.
- **Steps:**
  1. Open `/library` with items, the empty state, and the error state.
- **Expected:** `<html data-theme="light|dark">`; card titles, meta text, badges, chips, the poster fallback and the ErrorState are readable (WCAG AA contrast); no hardcoded white/black backgrounds.

### LIB-29 — Keyboard and a11y: toolbar labels, radios, order button name

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** videos → two items.
- **Steps:**
  1. Tab from the page start through the toolbar to the first card.
- **Expected:** toolbar `role="toolbar"` named "Sort"; select named "Sort"; the order button has an accessible name ("Ascending"/"Descending"), not just the arrow glyph; view group `role="radiogroup"` named "View" with `aria-checked` set. Note the radios are plain buttons, so arrow keys do not move between them (wanted: radio arrow-key behaviour or use `aria-pressed` toggle buttons). Focus ring visible on every control and card link.
