# Queue item history

- **Route:** `/queue/items/:kind/:id` — `kind` is one of `library`, `series`, `season`, `video`, `catalog` (e.g. `/queue/items/library/lib-1`, `/queue/items/video/v-1`, `/queue/items/season/s-3`). Linked from issue subjects, library rows, running jobs, next up, timeline entries, seasons and promos.
- **Component:** `src/pages/queue/ItemHistory.tsx`; children: `src/components/queue/ItemParts.tsx` (`KindChips`, `AppearsIn`, `visibleAppears`, `Timeline`, `WriteChanges`, `filterTimeline`), `LibraryCollections.tsx` (library only), `RetriggerDialog.tsx`, `useAction.ts`, `useThrottledReload.ts`, `format.ts`.
- **API calls:**
  - `GET /api/items/{kind}/{id}/history?limit=50[&cursor=]` → `ItemHistoryData` `{ subject, kinds, queues, seasons?, appears_in, timeline, next }` via `queueApi.history` (`src/lib/queueApi.ts`); reloaded (throttled 5 s) when a job for this subject finishes on the stream and after a re-queue.
  - `GET /api/extensions` → `{ extensions }` (which Plex / dizqueTV rows to show).
  - `GET /api/reports` → `{ reports: Report[] }` via `reportsApi.list()` (`src/lib/reportsApi.ts`), library only, for the "Review page" link.
  - `GET /api/items/library/{id}/collections` → `LibraryCollections` `{ topics, clip_groups, promos }` (library only).
  - `GET /api/queues` (snapshot, for stage labels), `GET /api/workers`; job stream filtered by `subjectId`.
  - `POST /api/queue/retrigger` `{ items: [{ subject_kind, subject_id, stage? }], mode: "queue" | "force_cascade" }` → `RetriggerResponse`.
- **Access:** any signed-in user (`RequireAuth`); no admin check in the page; the server enforces admin on retrigger.
- **Existing tests:** `src/pages/queue/ItemHistory.test.tsx` covers: title, kind chips, seasons as bars, timeline; write changes; filter changes/problems; re-queue one queue and "Everything from the start" with the note; disabled re-queue when `can_requeue` is false; Plex/dizqueTV rows only with the extension enabled; no extension rows when none enabled; refresh on a finished job for this item; library topics/clip groups/promos and review link; no collections panel / review link when empty. `src/pages/queue/a11y.test.tsx` "item page has no violations". `queueParts.test.tsx` "timeline filter". Gaps: not found; load errors; loading; null lists; error boundary/Back; signed out; non-admin; failed re-queue; Load older and its interaction with live reloads; navigation between items; video/season variants; RTL; theme; phone.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test?                                |
| ------ | ----------------------------------------------------------- | ------------- | -------- | -------------------------------------------------------- |
| QIH-01 | Library item renders header, re-queue, seasons, timeline    | Happy path    | P1       | Yes (`ItemHistory.test.tsx`)                             |
| QIH-02 | Video and season items: breadcrumb and "Open" link          | Happy path    | P2       | No                                                       |
| QIH-03 | Shows Loading while history is pending                      | Loading       | P2       | No                                                       |
| QIH-04 | Unknown id shows "Nothing is known about this item."        | Navigation    | P1       | No                                                       |
| QIH-05 | 500 / network / 401 is not reported as "nothing known"      | Error         | P1       | No                                                       |
| QIH-06 | Unknown `kind` in the URL                                   | Navigation    | P2       | No                                                       |
| QIH-07 | History list fields `null` or missing                       | Empty state   | P1       | No                                                       |
| QIH-08 | Extensions / reports / collections / retrigger lists `null` | Empty state   | P1       | No                                                       |
| QIH-09 | Valid empty history shows empty states                      | Empty state   | P2       | Partial (`ItemHistory.test.tsx`: collections panel only) |
| QIH-10 | Render crash shows route error fallback; Back works         | Error         | P1       | No                                                       |
| QIH-11 | Signed-out user is sent to `/login`                         | Permissions   | P1       | No                                                       |
| QIH-12 | Non-admin re-queue gets a readable 403                      | Permissions   | P1       | No                                                       |
| QIH-13 | Re-queue one queue and "Everything from the start"          | Happy path    | P1       | Yes (`ItemHistory.test.tsx`)                             |
| QIH-14 | Failed re-queue shows an alert                              | Error         | P2       | No                                                       |
| QIH-15 | Re-queue disabled where the queue cannot run on this item   | Validation    | P2       | Yes (`ItemHistory.test.tsx`)                             |
| QIH-16 | History filter: Everything / Changes only / Problems only   | Happy path    | P2       | Yes (`ItemHistory.test.tsx`)                             |
| QIH-17 | Load older pages the timeline; failure keeps the button     | Navigation    | P2       | No                                                       |
| QIH-18 | Live reload after a finished job keeps loaded pages         | Happy path    | P2       | Partial (`ItemHistory.test.tsx`: reload only)            |
| QIH-19 | Plex / dizqueTV rows follow enabled extensions              | Happy path    | P2       | Yes (`ItemHistory.test.tsx`)                             |
| QIH-20 | Library collections panel and review page link              | Happy path    | P2       | Yes (`ItemHistory.test.tsx`)                             |
| QIH-21 | Links between items, stages and back; data resets per item  | Navigation    | P1       | No                                                       |
| QIH-22 | "Choose queues…" dialog is prefilled with this item         | Happy path    | P2       | No                                                       |
| QIH-23 | Unknown kinds / outcomes do not show raw i18n keys          | Empty state   | P3       | No                                                       |
| QIH-24 | Phone width 375 px                                          | Responsive    | P3       | No                                                       |
| QIH-25 | Arabic: RTL and Arabic titles                               | RTL           | P3       | No                                                       |
| QIH-26 | Light and dark theme                                        | Theme         | P3       | No                                                       |
| QIH-27 | Keyboard and accessibility basics                           | Accessibility | P3       | Partial (`a11y.test.tsx`: axe only)                      |

## Cases

### QIH-01 — Library item renders header, re-queue, seasons, timeline

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `ItemHistory.test.tsx` "renders title, kind chips, seasons as bars and the timeline"
- **Preconditions / data:** `GET /api/items/library/lib-1/history` → `libraryHistoryFixture`; `/api/queues` → `snapshotFixture`; `/api/extensions` → `{ extensions: extensionsFixture }`; `/api/reports` → `{ reports: [] }`; `/api/items/library/lib-1/collections` → `{ topics: [], clip_groups: [], promos: [] }`.
- **Steps:**
  1. Open `/queue/items/library/lib-1`.
- **Expected:** breadcrumb "Processing queues" → `/queue` and current "Islamic lectures"; eyebrow "Library"; `h1` "Islamic lectures"; kind chips list "What kind of item"; link "Open in the library" → `/library/lib-1`; "Re-queue" section with one "Re-queue <queue>" button per queue and the last result or "never run"; "Years" (kinds include `ongoing`) or "Seasons" with bars; "Where it appears"; "History" with the timeline.

### QIH-02 — Video and season items: breadcrumb and "Open" link

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/items/video/v-1/history` → `videoHistoryFixture` (subject library `lib-1` "Islamic lectures"); a season item with `subject.kind: "season"`.
- **Steps:**
  1. Open `/queue/items/video/v-1`; then `/queue/items/season/s-3`.
- **Expected:** video: breadcrumb has a middle link "Islamic lectures" → `/queue/items/library/lib-1`; "Open in the library" → `/videos/v-1`; no collections panel, no `/api/reports` call. Season: no "Open in the library" link. Series: link → `/series/<id>`.

### QIH-03 — Shows Loading while history is pending

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** history request never resolves.
- **Steps:**
  1. Open `/queue/items/library/lib-1`.
- **Expected:** `role="status"` "Loading…" only.

### QIH-04 — Unknown id shows "Nothing is known about this item."

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/items/video/nope/history` → 404 `{ "title": "Not Found", "status": 404 }`.
- **Steps:**
  1. Open `/queue/items/video/nope` directly.
  2. Click "Back to the queues".
- **Expected:** `role="alert"` "Nothing is known about this item." and link "Back to the queues" → `/queue`; no re-queue buttons. Step 2 renders the overview.

### QIH-05 — 500 / network / 401 is not reported as "nothing known"

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** history → (a) 500; (b) `fetch` rejects `TypeError("Failed to fetch")`; (c) 401; (d) 200 with `Content-Type: text/html`.
- **Steps:**
  1. Open `/queue/items/library/lib-1`.
- **Expected:** a load error with "Try again" (or a login redirect for 401), not "Nothing is known about this item.". (currently unhandled — likely bug: `.catch(() => setMissing(true))` in `ItemHistory.tsx` maps every failure to "not found"; (d) stores a string and the render throws on `data.subject.kind`.)

### QIH-06 — Unknown `kind` in the URL

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `/queue/items/bogus/x`; API → 400 `{ "title": "Bad Request", "detail": "unknown kind" }`; second run where the API answers 200 with `subject.kind: "bogus"`.
- **Steps:**
  1. Open the URL.
- **Expected:** 400: not-found / error message, no crash. 200: page renders; eyebrow does not show the raw key `queue.subject.bogus` (fallback to the kind text).

### QIH-07 — History list fields `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `libraryHistoryFixture` with each of these set to `null`, then deleted: `kinds`, `queues`, `seasons`, `appears_in`, `timeline`, `timeline[0].triggered`, `timeline[0].reasons`, `timeline[0].changes`, `timeline[0].changes[0].items`, `queues[0].last_job`; `next` missing.
- **Steps:**
  1. Open `/queue/items/library/lib-1`; switch filters.
- **Expected:** page renders; no kind chips; "Re-queue" shows only "Everything from the start" / "Choose queues…"; no seasons section; "Not used anywhere yet."; "Nothing to show."; entries render without "Because:" / "Re-queued:" lines; no "Load older". (currently unhandled — likely bug: `kinds.map` in `KindChips`, `data.queues.map`, `entries.filter` in `visibleAppears` / `filterTimeline`, `e.triggered.length` and `e.reasons.length` in `Timeline` throw; `seasons` and `changes` are already guarded.)

### QIH-08 — Extensions / reports / collections / retrigger lists `null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** one at a time: `/api/extensions` → `{ extensions: null }`; `/api/reports` → `{ reports: null }`; `/api/items/library/lib-1/collections` → `{ topics: null, clip_groups: [], promos: [] }` (and each of the three `null`/missing); `POST /api/queue/retrigger` → `{ results: null }`.
- **Steps:**
  1. Open the library item; click "Re-queue <queue>".
- **Expected:** extension rows hidden; no review link; no collections panel (or a panel with only the non-null tables); the re-queue shows a status or an error, never a blank page. (currently unhandled — likely bug: `data.topics.length` in `LibraryCollections.tsx` and `r.results.filter` in `ItemHistory.tsx` throw; extensions/reports `null` are swallowed by `.catch`.)

### QIH-09 — Valid empty history shows empty states

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Partial — `ItemHistory.test.tsx` "has no collections panel or review link when there is nothing to show"
- **Preconditions / data:** history with `kinds: []`, `queues: []`, `seasons: []`, `appears_in: []`, `timeline: []`, `next: null`.
- **Steps:**
  1. Open the item.
- **Expected:** "Not used anywhere yet."; "Nothing to show."; no seasons section; filter buttons still work and keep "Nothing to show.".

### QIH-10 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** app routes; start at `/queue`; item history with `timeline: null` (QIH-07) or `Timeline` mocked to throw.
- **Steps:**
  1. Click a library name in the Libraries table.
  2. See the fallback; press Back; click the "Processing" nav link.
- **Expected:** route error fallback inside the shell (not blank); Back renders the overview without a refresh; nav works.

### QIH-11 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** no user.
- **Steps:**
  1. Open `/queue/items/library/lib-1`.
- **Expected:** redirect to `/login` with `state.from = "/queue/items/library/lib-1"`; no history request.

### QIH-12 — Non-admin re-queue gets a readable 403

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** non-admin; `POST /api/queue/retrigger` → 403 `{ "title": "Forbidden", "detail": "admin only" }`.
- **Steps:**
  1. Click "Re-queue <queue>"; click "Everything from the start".
- **Expected:** `role="alert"` "Forbidden: admin only"; no status line; history not reloaded. Better: re-queue buttons disabled for non-admins (currently unhandled — likely bug: no admin check).

### QIH-13 — Re-queue one queue and "Everything from the start"

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `ItemHistory.test.tsx` "re-queues one queue, and everything from the start with the note"
- **Preconditions / data:** retrigger → `{ results: [{ subject_kind: "library", subject_id: "lib-1", stage: "detect", job_id: 9, outcome: "inserted" }] }`.
- **Steps:**
  1. Click "Re-queue Place episodes"; click "Everything from the start".
- **Expected:** posts `{ items: [{ subject_kind: "library", subject_id: "lib-1", stage: "detect" }], mode: "queue" }`, then `{ items: [{ subject_kind: "library", subject_id: "lib-1" }], mode: "force_cascade" }`; `role="status"` "1 queued, 0 added to jobs already waiting, 0 skipped."; history reloaded; note "This only queues the jobs; what changes then flows on by itself." visible.

### QIH-14 — Failed re-queue shows an alert

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** retrigger → 500 `{ "title": "Internal Server Error", "detail": "db down" }`; then rejects with `TypeError("Failed to fetch")`.
- **Steps:**
  1. Click "Re-queue <queue>" twice.
- **Expected:** `role="alert"` "Internal Server Error: db down", then "Failed to fetch"; the old status line is cleared or kept consistently; the button leaves loading.

### QIH-15 — Re-queue disabled where the queue cannot run on this item

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes — `ItemHistory.test.tsx` "disables re-queue for queues that cannot run on this item"
- **Preconditions / data:** a `queues[]` entry with `can_requeue: false`.
- **Steps:**
  1. Open the item.
- **Expected:** that "Re-queue <queue>" button is disabled; others enabled.

### QIH-16 — History filter: Everything / Changes only / Problems only

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `ItemHistory.test.tsx` "filters the history: changes only / problems only"
- **Preconditions / data:** timeline with a failed job, an `awaiting_review` job, a `changed` job and a plain `unchanged` job.
- **Steps:**
  1. Click each filter button in group "Show".
- **Expected:** `aria-pressed="true"` on the chosen one; Changes only shows changed/triggered/changes entries; Problems only shows failed / awaiting review / error_cause entries; a filter with no match shows "Nothing to show.".

### QIH-17 — Load older pages the timeline; failure keeps the button

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** first page `next: "c2"`; `GET /api/items/library/lib-1/history?cursor=c2&limit=50` → first 500, then `{ ...page2, next: null }` with `timeline: null` in a third run.
- **Steps:**
  1. Click "Load older" (fails); click again (works).
- **Expected:** failure keeps the entries and the button (silent today; an error note would be better); success appends page 2 after page 1 and hides the button; ids that repeat across pages do not cause React key warnings. Third run (`timeline: null` on page 2) must not crash (currently unhandled — likely bug: `[...prev.timeline, ...next.timeline]` throws).

### QIH-18 — Live reload after a finished job keeps loaded pages

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `ItemHistory.test.tsx` "refreshes when a job for this item finishes on the stream"
- **Preconditions / data:** page 1 and page 2 loaded (QIH-17); then a hub/SSE `job.finished` for `subject_id: "lib-1"`.
- **Steps:**
  1. Emit the finished event; wait for the throttled reload (5 s).
- **Expected:** the history refetches; the user should not lose the older page they loaded or at least keep the "Load older" button (currently unhandled — likely bug: `load()` replaces `data` with page 1 only, so loaded older entries vanish while reading). An event for another subject does not reload.

### QIH-19 — Plex / dizqueTV rows follow enabled extensions

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `ItemHistory.test.tsx` "lists Plex and dizqueTV only while those extensions are enabled", "hides every extension row when no extension is enabled"
- **Preconditions / data:** `appears_in` with `plex_collection`, `dizquetv_show`, `maktaba_channel` targets; extensions plex enabled, dizquetv disabled; then `/api/extensions` → 500.
- **Steps:**
  1. Open the item.
- **Expected:** Plex and Maktaba rows show, dizqueTV row hidden; with the 500 only `maktaba_channel` / `channel_slot` rows show.

### QIH-20 — Library collections panel and review page link

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `ItemHistory.test.tsx` "shows the library's topics, clip groups and promos and links its review page"
- **Preconditions / data:** collections with one topic, one clip group, one promo (`season_id: "s-3"`); `/api/reports` → `{ reports: [{ name: "lib-1.html", kind: "review", library_id: "lib-1", ... }] }`.
- **Steps:**
  1. Open the library item; click "Review page".
- **Expected:** three tables with captions; promo links to `/videos/<id>` and `/queue/items/season/s-3`; "Review page" goes to `/queue/reports?open=lib-1.html`.

### QIH-21 — Links between items, stages and back; data resets per item

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** app routes; library item loaded; season `s-3` history delayed 500 ms; video item history ready.
- **Steps:**
  1. Click a season bar link → `/queue/items/season/s-3`.
  2. Before it loads, Back to the library; then Forward.
  3. Click a timeline stage chip → `/queue/<stage>`; Back.
- **Expected:** each item shows "Loading…" then its own data (never the previous item's title under the new URL); the status line from a re-queue on the previous item is cleared; a late response for an item you left does not overwrite the current one (currently unhandled — likely bug: `load()` has no stale-response guard); Back/Forward render without a refresh.

### QIH-22 — "Choose queues…" dialog is prefilled with this item

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** video item; snapshot stages loaded; retrigger resolves.
- **Steps:**
  1. Click "Choose queues…"; tick one queue; submit; click "Done".
- **Expected:** dialog "Queue work" lists the item ("Lecture 04" with its kind) ticked and no library list; submit posts the item with the chosen stage (only if the stage's `subject_kinds` includes `video`, else "None of the chosen queues work on the chosen items."); on done the history reloads.

### QIH-23 — Unknown kinds / outcomes do not show raw i18n keys

- **Priority:** P3
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `kinds: ["mystery"]`; `queues[0].last_job.outcome: "brand_new"`; `timeline[0].job.outcome: "brand_new"`; `triggered[0].outcome: "brand_new"`; `appears_in[0].target: "other"`.
- **Steps:**
  1. Open the item.
- **Expected:** kind chip shows "mystery" (already handled); outcome, `queue.enq.*` and `queue.appears.*` text fall back to the raw value, not `queue.outcome.brand_new` (currently unhandled — likely bug: only `KindChips` has a fallback).

### QIH-24 — Phone width 375 px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e 375×812; library with long Arabic and English names.
- **Steps:**
  1. Open the library item.
- **Expected:** no horizontal page scroll; re-queue rows wrap; collection tables scroll inside their wrapper; filter buttons fit.

### QIH-25 — Arabic: RTL and Arabic titles

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; subject label Arabic.
- **Steps:**
  1. Open the item and `/queue/items/video/nope`.
- **Expected:** `dir="rtl"`; `h1` shows the Arabic title isolated; buttons in Arabic; not-found "لا معلومات عن هذا العنصر."; season bars grow from the right.

### QIH-26 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light / dark.
- **Steps:**
  1. Open the item in both.
- **Expected:** only `inlineSize` inline styles (season bars), no inline colours; problem / change timeline tones readable in both.

### QIH-27 — Keyboard and accessibility basics

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** Partial — `a11y.test.tsx` "item page has no violations"
- **Preconditions / data:** library fixture.
- **Steps:**
  1. Tab through breadcrumb, re-queue buttons, filters, Load older.
- **Expected:** focus visible; filter group named "Show" with `aria-pressed`; season bars `role="img"` with "N episodes"; status line `role="status"`, errors `role="alert"`; axe clean also in the not-found state.
