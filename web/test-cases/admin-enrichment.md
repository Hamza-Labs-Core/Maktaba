# Admin Enrichment Review

- **Route:** `/admin/enrichment` (no params, no query-string state; not linked from the sidebar `ADMIN` list in `AppShell.tsx`, reachable by URL only)
- **Component:** `src/pages/Admin/Enrichment.tsx`; child: `src/components/EnrichmentPanel.tsx` (keyed by the selected `video_id`)
- **API calls:**
  - `GET /api/enrichment/pending` via `api.get` in the page → `{ items: [{ video_id, library_id, video_title, candidate_title, provider, confidence (0..1) }] }` (fetched on mount and again when the language changes, because `load` depends on `t`)
  - In `EnrichmentPanel`: `GET /api/videos/{id}/enrichment` → `{ candidates: Candidate[] }`; `POST /api/videos/{id}/enrichment/accept` `{ external_id }`; `POST /api/videos/{id}/enrichment/dismiss` `{ external_id }`; `POST /api/videos/{id}/enrichment/search` `{ query }` → `{ candidates }`
  - Window `keydown` listener: `j` / `k` move the selection
- **Access:** signed-in users only through `RequireAuth`. There is NO `AdminGate` on this page (unlike the other admin pages); a non-admin relies on the server 403, which is shown as a generic server ErrorState.
- **Existing tests:** No test for `Enrichment.tsx`. `src/components/EnrichmentPanel.test.tsx` covers: the panel headline / diff / protected marker, and the empty state with the manual-search CTA. Gaps: the whole queue page, j/k keys, selection, errors, null lists, permissions, queue refresh after accept/dismiss, RTL, theme, phone width.

## Summary

| ID     | Title                                                      | Category      | Priority | Covered by existing test?                                        |
| ------ | ---------------------------------------------------------- | ------------- | -------- | ---------------------------------------------------------------- |
| ENR-01 | Queue lists pending matches and opens the first            | Happy path    | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-02 | Clicking a row selects it and loads its panel              | Happy path    | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-03 | j / k move the selection and stop at the ends              | Happy path    | P2       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-04 | j / k ignored while typing in an input                     | Accessibility | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-05 | `items: []` shows "Nothing to review"                      | Empty state   | P2       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-06 | `items: null` or missing key shows the empty state         | Empty state   | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-07 | Missing / null fields on a pending row                     | Empty state   | P2       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-08 | Duplicate `video_id` rows                                  | Error         | P3       | No                                                               |
| ENR-09 | Loading text while the queue loads                         | Loading       | P3       | No                                                               |
| ENR-10 | Queue load fails (500, network, malformed JSON)            | Error         | P2       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-11 | Panel candidates `null` / load fails                       | Empty state   | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-12 | Accept / dismiss: queue refresh and errors                 | Happy path    | P1       | No (it.fails: bug; toasts and 409 pass in `Enrichment.test.tsx`) |
| ENR-13 | Non-admin gets the "Admins only" state, not a server error | Permissions   | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-14 | Signed-out redirect to `/login`                            | Permissions   | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-15 | Language switch keeps the selection                        | Navigation    | P3       | No                                                               |
| ENR-16 | Deep link, Back / Forward, discoverability                 | Navigation    | P3       | No                                                               |
| ENR-17 | Render crash shows route error fallback; Back works        | Error         | P1       | Yes (`Enrichment.test.tsx`)                                      |
| ENR-18 | Phone width (375 px)                                       | Responsive    | P3       | No                                                               |
| ENR-19 | Arabic / RTL                                               | RTL           | P3       | No                                                               |
| ENR-20 | Light and dark theme                                       | Theme         | P3       | No                                                               |
| ENR-21 | Listbox semantics and focus                                | Accessibility | P2       | Yes (`Enrichment.test.tsx`)                                      |

## Cases

Shared fixture (`PENDING`): `{ "items": [ { "video_id": "v1", "library_id": "l1", "video_title": "omar.e01.mkv", "candidate_title": "Omar (2012) S01E01", "provider": "tmdb", "confidence": 0.93 }, { "video_id": "v2", "library_id": "l1", "video_title": "omar.e02.mkv", "candidate_title": "Omar (2012) S01E02", "provider": "tmdb", "confidence": 0.884 }, { "video_id": "v3", "library_id": "l1", "video_title": "doc.mkv", "candidate_title": "The Doc", "provider": "tvdb", "confidence": 0.71 } ] }`. Panel default: `GET /api/videos/{id}/enrichment` → `{ "candidates": [{ "id": "k1", "provider": "tmdb", "external_id": "tt1", "confidence": 0.93, "accepted": false, "title": "Omar", "year": 2012, "fields": [] }] }`. Signed in as admin.

### ENR-01 — Queue lists pending matches and opens the first

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** `GET /api/enrichment/pending` → `PENDING`
- **Steps:**
  1. Open `/admin/enrichment`.
- **Expected:** Heading "Review matches", description "Videos with a pending web-metadata match. Use j / k to move through the queue.". A `listbox` named "Review matches" with 3 `option` buttons showing the video title, the candidate title and a badge "93%", "88%", "71%". The first option is `aria-selected="true"` and the detail column shows `EnrichmentPanel` for v1 (request to `/api/videos/v1/enrichment`).

### ENR-02 — Clicking a row selects it and loads its panel

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** `PENDING`
- **Steps:**
  1. Click the "doc.mkv" option.
- **Expected:** That option becomes `aria-selected="true"` (others false) with the selected style; a new request to `/api/videos/v3/enrichment`; the panel shows "Loading…" then v3's candidates (the panel remounts, so its search text resets).

### ENR-03 — j / k move the selection and stop at the ends

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** `PENDING`
- **Steps:**
  1. Press `k` (at the first row).
  2. Press `j` three times.
  3. Press `k` once. Press `J` (shift) and `ArrowDown`.
- **Expected:** `k` on the first row keeps v1. `j` moves to v2, then v3, then stays on v3. `k` goes back to v2. Upper-case `J` and arrow keys do nothing (only lower-case j/k are handled). Each move loads the new panel. The selected row should scroll into view in a long queue (currently not done; `listRef` is unused — polish).

### ENR-04 — j / k ignored while typing in an input

- **Priority:** P1
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** `PENDING`; v1 panel loaded
- **Steps:**
  1. Click the panel's search box (`aria-label` "Search manually") and type "jordan king".
- **Expected:** The text appears in the box and the selection stays on v1. (currently unhandled — likely bug: the window `keydown` handler at `Enrichment.tsx:50` does not check the event target, so every "j"/"k" typed moves the selection; the panel is keyed by `video_id`, so it remounts and the half-typed query is lost.) Same for any other input or textarea on the page and for keys with Ctrl/Meta/Alt held.

### ENR-05 — `items: []` shows "Nothing to review"

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** `GET /api/enrichment/pending` → `{ "items": [] }`
- **Steps:**
  1. Open the page; press `j`.
- **Expected:** EmptyState "Nothing to review" / "There are no pending enrichment matches right now."; no listbox, no panel; pressing `j` does nothing and throws nothing.

### ENR-06 — `items: null` or missing key shows the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** a) `{ "items": null }`; b) `{}`
- **Steps:**
  1. Open the page; press `j` and `k`.
- **Expected:** "Nothing to review" empty state; no crash; keys do nothing.

### ENR-07 — Missing / null fields on a pending row

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** items `[{ "video_id": "v1", "video_title": null, "candidate_title": null, "provider": null, "confidence": null }, { "video_id": "v2", "video_title": "x", "candidate_title": "y", "confidence": 1.5 }]`
- **Steps:**
  1. Open the page.
- **Expected:** Row 1 renders with empty titles (or a placeholder such as the video id) and badge "0%", no "NaN%". Row 2 badge "150%" — expected the value clamped to 100 % (currently not clamped — minor). A row with `confidence` missing shows "NaN%" (currently unhandled — likely bug: `Math.round(undefined * 100)`).

### ENR-08 — Duplicate `video_id` rows

- **Priority:** P3
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** two items with `video_id: "v1"` (two providers)
- **Steps:**
  1. Open the page; press `j`.
- **Expected:** Both rows render without React key warnings; selection and j/k can reach both (today the key and the selection are both the `video_id`, so both rows look selected and `j` cannot leave the first — note as a likely bug if the server can send duplicates).

### ENR-09 — Loading text while the queue loads

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/enrichment/pending` never resolves
- **Steps:**
  1. Open the page.
- **Expected:** Heading and description shown, plus "Loading…"; no empty state, no listbox.

### ENR-10 — Queue load fails (500, network, malformed JSON)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** `GET /api/enrichment/pending` → a) 500 `{ "title": "Internal error" }`; b) fetch rejects; c) 200 with `{bad` as JSON
- **Steps:**
  1. Open the page for each.
- **Expected:** ErrorState (server kind) titled "Something went wrong." with description a) "Internal error", b, c) "Something went wrong.". A retry action should be offered (currently none; the user must reload — likely bug). The heading is gone in the error state (whole page replaced).

### ENR-11 — Panel candidates `null` / load fails

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** queue `PENDING`; `GET /api/videos/v1/enrichment` → a) `{ "candidates": null }`; b) `{}`; c) 500; d) a candidate with `fields: null`; e) `POST .../search` → `{ "candidates": null }`
- **Steps:**
  1. Open the page; in e) type "omar" in the search box (with j/k guarded) and click "Search manually".
- **Expected:** a, b, c) The panel shows its empty state with the manual search, no crash (c should ideally say loading failed — today it looks like "no candidates"). d) Candidate renders without a diff table. e) Empty state, no crash.

### ENR-12 — Accept / dismiss: queue refresh and errors

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No (it.fails: bug; toasts and 409 pass in `Enrichment.test.tsx`)
- **Preconditions / data:** `PENDING`; `POST /api/videos/v1/enrichment/accept` → 200, then a run with 409 `{ "title": "Already applied" }`; dismiss → 200
- **Steps:**
  1. In v1's panel accept the top candidate.
  2. Dismiss a candidate on v2.
  3. Repeat accept with the 409.
- **Expected:** Toast "Metadata applied." / "Suggestion dismissed."; the handled video leaves the queue (or is marked done) and the selection moves to the next row. (currently unhandled — likely bug: the page never refetches `/api/enrichment/pending` after an action, so the accepted video stays in the queue until reload, which breaks the "clear the queue quickly" flow.) 409 shows a toast "Already applied" and the row stays.

### ENR-13 — Non-admin gets the "Admins only" state, not a server error

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** signed in as `{ username: "bob", is_admin: false }`; `GET /api/enrichment/pending` → 403 `{ "title": "Forbidden", "status": 403 }`
- **Steps:**
  1. Open `/admin/enrichment`.
- **Expected:** ErrorState "Admins only" / "You need administrator access to view this page." (`common.forbidden.*`) and no request to `/api/enrichment/pending`, like the other admin pages. (currently unhandled — likely bug: `Enrichment.tsx` has no `AdminGate`; the non-admin sees a generic "Something went wrong." server error with "Forbidden" after a failed request.)

### ENR-14 — Signed-out redirect to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** no session
- **Steps:**
  1. Open `/admin/enrichment` directly.
- **Expected:** Redirect to `/login` with `state.from = "/admin/enrichment"`; no queue request; after admin login the user returns to the queue.

### ENR-15 — Language switch keeps the selection

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `PENDING`
- **Steps:**
  1. Select v3; switch the language to Arabic.
- **Expected:** The queue is refetched (because `load` depends on `t`), the selection stays on v3 (it keeps `cur`), the panel does not reload. If v3 is no longer pending after the refetch, the selection should move to a row that exists (currently it keeps a stale id and shows a panel for a video not in the list — minor).

### ENR-16 — Deep link, Back / Forward, discoverability

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `PENDING`
- **Steps:**
  1. From `/admin/users` type `/admin/enrichment` into the address bar (or navigate with the router).
  2. Select v2; press Back; press Forward.
  3. Look for a sidebar link to this page.
- **Expected:** The page loads from a direct URL. Selection is not in the URL, so Forward returns with v1 selected again (acceptable; note). Back returns to `/admin/users` without a reload. There is no admin nav entry for the page (only reachable by URL) — confirm whether a link is wanted.

### ENR-17 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** start on `/admin/users`; mock `EnrichmentPanel` to throw on render (or return `{ "items": [{ "video_id": "v1", "video_title": { "a": 1 } }] }`, an object child, which React refuses)
- **Steps:**
  1. Navigate to `/admin/enrichment`.
  2. Press browser Back; click a sidebar link.
- **Expected:** A route-level error fallback with the shell, not a blank page. Back renders `/admin/users` without a reload; nav links work; the window `keydown` listener from the crashed page is removed (pressing `j` elsewhere does nothing).

### ENR-18 — Phone width (375 px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375 × 812; `PENDING` with long titles
- **Steps:**
  1. Open the page; select rows.
- **Expected:** No horizontal page scroll; the queue and the detail panel stack (queue above detail); long titles wrap; after tapping a row the panel is reachable (ideally scrolled into view); j/k hint is irrelevant on touch but harmless.

### ENR-19 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** language Arabic; items with Arabic `video_title` ("عمر الحلقة ١") and Latin `candidate_title`
- **Steps:**
  1. Open the page.
- **Expected:** `dir="rtl"`; heading "مراجعة المطابقات"; titles use `dir="auto"` so the Arabic one reads RTL and the Latin one LTR within the row; the layout is mirrored (queue on the inline start side); percentages render.

### ENR-20 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme="light"` then `"dark"`
- **Steps:**
  1. View the queue with a selected row and the panel.
- **Expected:** Selected row style visible in both themes; badge and muted text readable; no hardcoded colours.

### ENR-21 — Listbox semantics and focus

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Enrichment.test.tsx`)
- **Preconditions / data:** `PENDING`
- **Steps:**
  1. Tab into the queue; press Tab again; press Enter on the second option.
  2. Press `j` with focus on an option.
- **Expected:** Visible focus on options; Enter selects. The `role="option"` sits on a `button` inside an `li` of a `role="listbox"` `ul` — screen readers expect options to be direct children of the listbox (fix: put `role="presentation"` on the `li`, or use a plain list of buttons with `aria-current`). After `j`, focus should follow the selection (today focus stays on the old button while the selection moves — polish).
