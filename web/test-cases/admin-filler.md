# Admin Filler Pools

- **Route:** `/admin/filler` (no params, no query-string state)
- **Component:** `src/pages/Admin/FillerPools.tsx`; children: `SourceSearch` and `useDuration` from `src/pages/Admin/channels/pickers.tsx`; design-system `Card`, `Badge`, `EmptyState`, `Input`, `Select`, `Toast`
- **API calls:**
  - `GET /api/filler/pools` via `fillerApi.pools()` in `src/lib/channels.ts` → `{ items: FillerPool[] }` where `FillerPool = { id, name, channel_id?, external_key?, items?: FillerItem[] }` and `FillerItem = { id, pool_id, video_id, type: "filler"|"bumper"|"station_id", title?, duration_ms? }` (reloaded after every mutation)
  - `POST /api/filler/pools` body `{ name }` via `fillerApi.createPool()` → `FillerPool`
  - `DELETE /api/filler/pools/{id}` via `fillerApi.deletePool()`
  - `POST /api/filler/pools/{id}/items` body `{ items: [{ video_id, type }] }` via `fillerApi.addItems()`
  - `DELETE /api/filler/items/{id}` via `fillerApi.deleteItem()`
  - `GET /api/channels/sources?kind=video&q&limit=25` via `channelsApi.sources()` (inside `SourceSearch`, debounced 250 ms) → `{ kind, items: SourceHit[] }`
- **Access:** admin only. `RequireAuth` (signed out → `/login`), then `AdminGate` (non-admin → `common.forbidden.*`). Server re-checks.
- **Existing tests:** `src/pages/Admin/FillerPools.test.tsx` — covers: pools listed with clip count and running time, the plan badge, adding a clip from the library search (one test). Gaps: create pool, delete pool, remove item, errors on every call, null lists, search filter, empty states, kind select, permissions, error boundary, RTL, theme, phone width, keyboard.

## Summary

| ID     | Title                                                | Category      | Priority | Covered by existing test?                                              |
| ------ | ---------------------------------------------------- | ------------- | -------- | ---------------------------------------------------------------------- |
| FIL-01 | Lists pools with clip count, running time and badges | Happy path    | P1       | Partial (`FillerPools.test.tsx`: "One channel" badge not tested)       |
| FIL-02 | Expand a pool: items, plan note, links to the player | Happy path    | P2       | Partial (`FillerPools.test.tsx`: plan note and link href not asserted) |
| FIL-03 | `items: []` shows the empty state                    | Empty state   | P2       | No                                                                     |
| FIL-04 | `items: null` / missing key / pool `items: null`     | Empty state   | P1       | No                                                                     |
| FIL-05 | Missing optional fields on pools and items           | Empty state   | P2       | No                                                                     |
| FIL-06 | Pool with `name: null` does not crash the search     | Error         | P1       | No                                                                     |
| FIL-07 | Pools load fails (500, network, malformed JSON)      | Error         | P2       | No                                                                     |
| FIL-08 | Loading text while pools load                        | Loading       | P3       | No                                                                     |
| FIL-09 | Create a pool; name validation                       | Validation    | P1       | No                                                                     |
| FIL-10 | Create pool fails                                    | Error         | P2       | No                                                                     |
| FIL-11 | Delete a pool: confirm, cancel, error                | Happy path    | P1       | No                                                                     |
| FIL-12 | Add a clip with the chosen kind                      | Happy path    | P1       | Partial (`FillerPools.test.tsx`: kind other than default not tested)   |
| FIL-13 | Add clip fails                                       | Error         | P2       | No                                                                     |
| FIL-14 | Remove a clip; remove fails                          | Happy path    | P1       | No                                                                     |
| FIL-15 | Video search states: loading, none, null, error      | Empty state   | P2       | No                                                                     |
| FIL-16 | Search pools filter and its "no match" text          | Happy path    | P2       | No                                                                     |
| FIL-17 | Non-admin sees "Admins only", no requests            | Permissions   | P1       | No                                                                     |
| FIL-18 | Signed-out redirect to `/login`                      | Permissions   | P1       | No                                                                     |
| FIL-19 | Navigation from Channels and Back / Forward          | Navigation    | P2       | No                                                                     |
| FIL-20 | Render crash shows route error fallback; Back works  | Error         | P1       | No                                                                     |
| FIL-21 | Phone width (375 px)                                 | Responsive    | P3       | No                                                                     |
| FIL-22 | Arabic / RTL                                         | RTL           | P3       | No                                                                     |
| FIL-23 | Light and dark theme                                 | Theme         | P3       | No                                                                     |
| FIL-24 | Keyboard and accessibility basics                    | Accessibility | P2       | No                                                                     |

## Cases

Shared fixture (`POOLS`): `{ "items": [ { "id": "p1", "name": "News clips", "external_key": "news.topic", "items": [ { "id": "i1", "pool_id": "p1", "video_id": "v1", "type": "filler", "title": "Headlines", "duration_ms": 90000 }, { "id": "i2", "pool_id": "p1", "video_id": "v2", "type": "bumper", "title": "Sting", "duration_ms": 30000 } ] }, { "id": "p2", "name": "Bumpers", "channel_id": "c1", "items": [] } ] }`. Video search default: `{ "kind": "video", "items": [{ "kind": "video", "id": "v9", "name": "Station ident", "count": 1, "duration_ms": 15000 }] }`. Signed in as admin.

### FIL-01 — Lists pools with clip count, running time and badges

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `FillerPools.test.tsx` "lists pools with counts and adds a clip from the search"
- **Preconditions / data:** `GET /api/filler/pools` → `POOLS`
- **Steps:**
  1. Open `/admin/filler`.
- **Expected:** Heading "Filler pools", help text "Clips, bumpers and station idents...". One card per pool. News clips toggle button shows the `channels.filler.poolMeta` text with 2 clips and 2 min; badge "From plan". Bumpers shows 0 clips and badge "One channel". Each card has a Delete button. All toggles start `aria-expanded="false"`.

### FIL-02 — Expand a pool: items, plan note, links to the player

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `FillerPools.test.tsx` opens a pool to add a clip
- **Preconditions / data:** `POOLS`
- **Steps:**
  1. Click the News clips toggle; then click it again; open Bumpers.
- **Expected:** News clips opens (`aria-expanded="true"`), shows the warning "The lineup plan writes this pool: the next lineup run replaces its clips with the plan's.", each item with its kind badge ("Filler", "Bumper"), a link "Headlines" with `href="/videos/v1/watch?t=0"`, its duration, and a remove button labelled "Remove Headlines". Clicking again collapses it. Only one pool is open at a time. Bumpers shows "No items yet." and no plan note.

### FIL-03 — `items: []` shows the empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/filler/pools` → `{ "items": [] }`
- **Steps:**
  1. Open `/admin/filler`.
- **Expected:** EmptyState "No filler pools" / "Create a pool, then add clips by searching the library." The search and the "Pool name" input with "Add pool" are still shown and work.

### FIL-04 — `items: null` / missing key / pool `items: null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/filler/pools` → a) `{ "items": null }`; b) `{}`; c) `{ "items": [{ "id": "p1", "name": "A", "items": null }] }`; d) `{ "items": [{ "id": "p1", "name": "A" }] }` (no `items` key)
- **Steps:**
  1. Open `/admin/filler`; in c) and d) expand pool A.
- **Expected:** a, b) Empty state, no crash. c, d) Pool A shows 0 clips and 0 duration; expanded shows "No items yet." and the add controls. No console errors.

### FIL-05 — Missing optional fields on pools and items

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** pool `{ "id": "p1", "name": "A", "external_key": null, "channel_id": null, "items": [{ "id": "i1", "pool_id": "p1", "video_id": "v1", "type": "filler", "title": null, "duration_ms": null }, { "id": "i2", "pool_id": "p1", "video_id": "v2", "type": "unknown_kind" }] }`
- **Steps:**
  1. Open the page; expand A.
- **Expected:** No badges. Item i1 link text falls back to "v1", remove label "Remove v1", duration shows 0 min. Item i2 renders without crashing (the kind badge shows a fallback rather than a raw i18n key — currently it would show `filler.kind.unknown_kind`, minor).

### FIL-06 — Pool with `name: null` does not crash the search

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `{ "items": [{ "id": "p1", "name": null, "items": [] }] }`
- **Steps:**
  1. Open the page; type "a" in "Search pools".
- **Expected:** The page keeps rendering; the nameless pool is filtered out or shown with a placeholder. (currently unhandled — likely bug: `FillerPools.tsx:100` calls `p.name.toLowerCase()` and throws, blanking the route.)

### FIL-07 — Pools load fails (500, network, malformed JSON)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/filler/pools` → a) 500 `{ "title": "Internal error" }`; b) fetch rejects; c) 200 with invalid JSON; d) 403
- **Steps:**
  1. Open `/admin/filler` for each.
- **Expected:** An error message with a retry action, not the "No filler pools" empty state. (currently unhandled — likely bug: `FillerPools.tsx:44` turns every failure into `[]`, so the admin is told there are no pools and may create duplicates.)

### FIL-08 — Loading text while pools load

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/filler/pools` never resolves
- **Steps:**
  1. Open `/admin/filler`.
- **Expected:** Only "Loading…" is shown; no heading or inputs yet.

### FIL-09 — Create a pool; name validation

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** `POOLS`; `POST /api/filler/pools` → `{ "id": "p3", "name": "Idents", "items": [] }`; then the reload returns `POOLS` plus p3
- **Steps:**
  1. Check "Add pool" with an empty name and with " ".
  2. Type " Idents " and click "Add pool".
- **Expected:** "Add pool" is disabled while the name is empty or whitespace. Request body is `{ "name": "Idents" }` (trimmed). The input clears, the list reloads and the new pool p3 is open (`aria-expanded="true"`). Pressing Enter in the name input does not need to submit (no form), but must not reload the page.

### FIL-10 — Create pool fails

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST /api/filler/pools` → a) 409 `{ "title": "A pool with that name exists" }`; b) fetch rejects
- **Steps:**
  1. Type "News clips"; click "Add pool".
- **Expected:** a) Toast "A pool with that name exists"; b) toast "Something went wrong.". The typed name stays in the input; no reload happens.

### FIL-11 — Delete a pool: confirm, cancel, error

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `POOLS`; `window.confirm` mocked; `DELETE /api/filler/pools/p2` → 204, later → 409 `{ "title": "Pool in use" }`
- **Steps:**
  1. Click Delete on Bumpers; Cancel. Then OK.
  2. Delete News clips with the 409.
- **Expected:** Confirm text "Delete the pool “Bumpers”?". Cancel sends nothing. OK sends `DELETE /api/filler/pools/p2` and the list reloads without it. 409 shows a toast "Pool in use" and the pool stays.

### FIL-12 — Add a clip with the chosen kind

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `FillerPools.test.tsx` adds a clip with the default kind
- **Preconditions / data:** `POOLS`; `POST /api/filler/pools/p2/items` → `{ "items": [...] }`
- **Steps:**
  1. Open Bumpers; set "Kind" to "Bumper" (`filler.kind.bumper`); type "ident" in "Add a clip from the library"; click "Station ident".
  2. Open News clips; check the Kind select value.
- **Expected:** Body `{ "items": [{ "video_id": "v9", "type": "bumper" }] }` to `/api/filler/pools/p2/items`; list reloads and Bumpers shows 1 clip. The Kind choice is shared across pools (it stays "Bumper" when another pool is opened) — confirm this is intended; it is easy to add a clip with the wrong kind.

### FIL-13 — Add clip fails

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST /api/filler/pools/p2/items` → a) 400 `{ "title": "Video already in pool" }`; b) fetch rejects
- **Steps:**
  1. Open Bumpers; pick "Station ident".
- **Expected:** Toast with "Video already in pool" / "Something went wrong."; the pool stays open; counts unchanged.

### FIL-14 — Remove a clip; remove fails

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `POOLS`; `DELETE /api/filler/items/i1` → 204, then → 500 `{ "title": "Down" }`
- **Steps:**
  1. Open News clips; click "Remove Headlines".
  2. Repeat with the 500 on "Remove Sting".
- **Expected:** `DELETE /api/filler/items/i1` (no confirm); list reloads, Headlines gone, count 1. Failure toasts "Down" and the item stays.

### FIL-15 — Video search states: loading, none, null, error

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/channels/sources?kind=video...` → a) pending; b) `{ "items": [] }`; c) `{ "items": null }`; d) `{}`; e) 500; f) a hit with `"detail": null`
- **Steps:**
  1. Open a pool; type "zz" in the clip search for b).
- **Expected:** a) "Loading…" under the search. b) "Nothing matches “zz”." (and "Nothing to pick from yet." with an empty query). c, d) treated as empty, no crash. e) "Something went wrong.". f) renders the hit name and duration only. Requests are debounced (one request after typing stops for 250 ms).

### FIL-16 — Search pools filter and its "no match" text

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `POOLS`
- **Steps:**
  1. Type "NEWS" in "Search pools"; then "topic"; then "zzz"; then clear.
- **Expected:** Case-insensitive match on name keeps News clips; "topic" matches the `external_key` and keeps News clips; "zzz" shows no pools. The no-match view should say nothing matches the search (currently it shows the "No filler pools" empty state with "Create a pool..." — misleading, likely bug at `FillerPools.tsx:130`). Clearing shows all pools.

### FIL-17 — Non-admin sees "Admins only", no requests

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** signed in as `{ username: "bob", is_admin: false }`
- **Steps:**
  1. Open `/admin/filler`.
- **Expected:** ErrorState "Admins only" / "You need administrator access to view this page."; no request to `/api/filler/pools`; no admin nav link.

### FIL-18 — Signed-out redirect to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** no session
- **Steps:**
  1. Open `/admin/filler` directly.
- **Expected:** Redirect to `/login` with `state.from = "/admin/filler"`; after admin login the user returns to `/admin/filler`.

### FIL-19 — Navigation from Channels and Back / Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `POOLS`; channels list loaded
- **Steps:**
  1. From `/admin/channels` click "Filler pools".
  2. Open a pool, click the "Headlines" link.
  3. Press Back twice; press Forward.
  4. Click the sidebar "Filler pools" admin link (`nav.admin.filler`) from another page.
- **Expected:** 1) URL `/admin/filler`, sidebar item active. 2) Goes to `/videos/v1/watch?t=0`. 3) Back returns to the pools page (pool collapsed, list reloaded), Back again to Channels; Forward returns to pools. 4) Page renders from the sidebar. The plain `<a href>` causes a full page load — consider a router `Link` (minor).

### FIL-20 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** start on `/admin/channels`; `GET /api/filler/pools` → `{ "items": [{ "id": "p1", "name": null }] }` and type in search (throws, see FIL-06), or mock `SourceSearch` to throw on open
- **Steps:**
  1. Navigate to `/admin/filler`, trigger the throw.
  2. Press browser Back; click another sidebar link.
- **Expected:** A route-level error fallback with the shell visible, not a blank page. Back shows `/admin/channels` without a reload; nav links work.

### FIL-21 — Phone width (375 px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375 × 812; `POOLS` with a long pool name and long clip titles
- **Steps:**
  1. Open the page; expand a pool; use the search.
- **Expected:** No horizontal page scroll; filter inputs and "Add pool" stack; card header wraps badges and Delete; long titles wrap or ellipsize; the remove buttons stay tappable (about 44 px target).

### FIL-22 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** language Arabic; pool `"name": "فواصل الأخبار"`, item title Arabic
- **Steps:**
  1. Open `/admin/filler`; expand the pool.
- **Expected:** `dir="rtl"`; heading "مجموعات الفواصل", button "إضافة مجموعة"; counts and durations localised; Arabic names render; layout mirrored (Delete at the inline end); confirm text Arabic ("حذف المجموعة «…»؟").

### FIL-23 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme="light"` then `"dark"`
- **Steps:**
  1. View the list with an expanded plan pool.
- **Expected:** Card, badges, the plan warning and links readable in both themes; no hardcoded colours.

### FIL-24 — Keyboard and accessibility basics

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** `POOLS`
- **Steps:**
  1. Tab through search, pool name, Add pool, each pool toggle, Delete, items, the Kind select and the clip search.
  2. Press Enter / Space on a pool toggle.
- **Expected:** Visible focus everywhere; toggles are buttons with `aria-expanded`; inputs have labels ("Search pools", "Pool name", "Kind", "Add a clip from the library"); the "✕" remove buttons have the "Remove {name}" label; the search hits are a `listbox` of options reachable by Tab. Toasts are announced (Toast live region).
