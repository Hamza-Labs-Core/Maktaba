# Settings: Privacy tab

- **Route:** `/settings/privacy`
- **Component:** `src/pages/Settings.tsx` → `PrivacyTab` (Toggle from `@ds/components/Choice/Toggle`, a `mkt-table`, toasts via `useToast`).
- **API calls (all in `src/lib/analytics.ts`, `analyticsApi`):**
  - `GET /api/me/activity/settings` via `getPrivacy()` → `{ track_enabled: boolean }`. On any error the code assumes `track_enabled: true`.
  - `GET /api/me/history?limit=20` via `history({ limit: 20 })` → `{ items: HistoryItem[], limit, offset }` where `HistoryItem = { video_id, title, duration_sec, times_watched, total_watch_sec, best_percent, last_watched_at, position_sec }`. On error the list becomes `[]`.
  - `PUT /api/me/activity/settings` via `setPrivacy(next)` body `{ track_enabled }` → `{ track_enabled }`.
  - `DELETE /api/me/history/{video_id}` via `deleteHistory(id)` → 204.
- **Access:** signed-in user (`RequireAuth`); each user sees only their own data (server-scoped `/api/me/...`). No admin gate.
- **Existing tests:** none for `PrivacyTab`. Gaps: all below.

Mock `../lib/analytics` (`analyticsApi.getPrivacy/history/setPrivacy/deleteHistory`) or `api.get/put/delete` from `../lib/api`. `formatPercent(n)` → `"${Math.round(n)}%"`; `formatWatchTime(sec)` → `"0m"`, `"5m"`, `"1h 30m"`.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test?                                     |
| ------ | ----------------------------------------------------------- | ------------- | -------- | ------------------------------------------------------------- |
| SPR-01 | Shows tracking toggle and watch history table               | Happy path    | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-02 | Tracking off shows "Tracking paused"                        | Happy path    | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-03 | Turn tracking off saves and toasts                          | Happy path    | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-04 | Toggle is disabled while saving; no double request          | Loading       | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-05 | Save tracking fails (403 / 500 / network)                   | Error         | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-06 | Loading states before data arrives                          | Loading       | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-07 | Privacy settings load fails: toggle shows "on" (misleading) | Error         | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-08 | Empty history `items: []`                                   | Empty state   | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-09 | `items: null` from history                                  | Empty state   | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-10 | `items` key missing from history: crash                     | Empty state   | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-11 | History load fails (401 / 500 / network / bad JSON)         | Error         | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-12 | Missing or null optional fields in a history row            | Empty state   | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-13 | Remove a history item                                       | Happy path    | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-14 | Remove fails: row stays, error toast                        | Error         | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-15 | Removing the last item shows the empty text                 | Empty state   | P3       | No                                                            |
| SPR-16 | Video title links to the video page; Back returns           | Navigation    | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-17 | Signed-out deep link redirects to /login                    | Permissions   | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-18 | Render crash shows route error fallback; Back works         | Error         | P1       | Yes (`Settings.test.tsx`)                                     |
| SPR-19 | Arabic: RTL, Arabic strings, Arabic titles                  | RTL           | P2       | Yes (`Settings.test.tsx`)                                     |
| SPR-20 | Phone width: history table does not cause page scroll       | Responsive    | P2       | No (e2e-only)                                                 |
| SPR-21 | Keyboard and labels: switch, table, Remove buttons          | Accessibility | P2       | Yes (`Settings.test.tsx`; it.fails: Remove not named per row) |
| SPR-22 | Both themes readable                                        | Theme         | P3       | No                                                            |

## Cases

### SPR-01 — Shows tracking toggle and watch history table

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `GET /api/me/activity/settings` → `{ "track_enabled": true }`; `GET /api/me/history?limit=20` → `{ "items": [{ "video_id": "v1", "title": "Tafsir 1", "duration_sec": 3600, "times_watched": 2, "total_watch_sec": 5400, "best_percent": 87.4, "last_watched_at": "2026-10-18T20:00:00Z", "position_sec": 120 }, { "video_id": "v2", "title": "Fiqh 2", "duration_sec": 600, "times_watched": 1, "total_watch_sec": 300, "best_percent": 50, "last_watched_at": "2026-10-17T20:00:00Z", "position_sec": 0 }], "limit": 20, "offset": 0 }`.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected:** `h2` "Activity tracking" with hint text; a `switch` named "Tracking on", checked. `h2` "Watch history"; a `table` named "Watch history" with headers "Video", "Progress", "Watched" and an empty header; rows "Tafsir 1 | 87% | 1h 30m | Remove" and "Fiqh 2 | 50% | 5m | Remove". The history call uses `limit=20`.

### SPR-02 — Tracking off shows "Tracking paused"

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `getPrivacy` → `{ track_enabled: false }`.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected:** `switch` named "Tracking paused", not checked.

### SPR-03 — Turn tracking off saves and toasts

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** as SPR-01; `PUT /api/me/activity/settings` → `{ "track_enabled": false }`.
- **Steps:**
  1. Click the switch.
- **Expected:** One `PUT /api/me/activity/settings` with body `{ "track_enabled": false }` (and the CSRF header). Switch becomes unchecked, named "Tracking paused". Success toast "Privacy settings saved." (role `status`). The UI uses the server answer: if the server returns `{ track_enabled: true }`, the switch stays on.

### SPR-04 — Toggle is disabled while saving; no double request

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `setPrivacy` returns a pending promise.
- **Steps:**
  1. Click the switch twice quickly; then resolve.
- **Expected:** Switch disabled while pending; only one PUT. After resolve, enabled again.

### SPR-05 — Save tracking fails (403 / 500 / network)

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `PUT` → 500 problem+json `{ "title": "Internal Server Error", "status": 500, "detail": "db down" }`; then 403 `{ "title": "Forbidden", "status": 403 }`; then fetch rejects.
- **Steps:**
  1. Click the switch.
- **Expected:** Error toast (role `alert`) "Internal Server Error: db down" / "Forbidden" / "Something went wrong." for network. Switch keeps its old value (still "Tracking on", checked) and is enabled again.

### SPR-06 — Loading states before data arrives

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** both GETs pending.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected:** Headings and hint render; no switch yet (it appears only once `track_enabled` is known); under "Watch history" the text "Loading…" (`common.loading`). After resolving, loading text is gone.

### SPR-07 — Privacy settings load fails: toggle shows "on" (misleading)

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `GET /api/me/activity/settings` → 500, or 401, or fetch rejects. Server-side truth is `track_enabled: false`.
- **Steps:**
  1. Open `/settings/privacy`.
  2. Click the switch.
- **Expected (wanted):** an error state for the setting ("could not load"), switch hidden or disabled, so the user is not told tracking is on when it may be off. **Current:** `.catch(() => setTracking(true))` shows "Tracking on" checked; clicking it sends `PUT { track_enabled: false }` (currently unhandled — likely bug: shows a guess as fact on a privacy control).

### SPR-08 — Empty history `items: []`

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** history → `{ "items": [], "limit": 20, "offset": 0 }`.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected:** "Nothing here yet." (`common.empty`) under "Watch history"; no table. Toggle still works.

### SPR-09 — `items: null` from history

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** history → `{ "items": null, "limit": 20, "offset": 0 }`.
- **Steps:**
  1. Open `/settings/privacy`; wait 2 s.
- **Expected (wanted):** "Nothing here yet." **Current:** `setHistory(null)` keeps the "Loading…" text forever (currently unhandled — likely bug: null list read as "still loading").

### SPR-10 — `items` key missing from history: crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** history → `{}` (or `{ "limit": 20 }`); also try a 200 with body `[]`.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected (wanted):** "Nothing here yet."; the rest of the tab works. **Current:** `history` becomes `undefined`, the `history === null` check fails, and `history.length` throws `TypeError` during render: the whole page goes blank (currently unhandled — likely bug).

### SPR-11 — History load fails (401 / 500 / network / bad JSON)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** history → 500; then 401; then fetch rejects; then 200 `Content-Type: application/json` with body `{bad json`.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected (wanted):** an error message with a retry. **Current:** every failure silently shows "Nothing here yet." (`.catch(() => setHistory([]))`), so a user can believe their history is empty (likely bug, P2). No crash in any variant.

### SPR-12 — Missing or null optional fields in a history row

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** items: `[{ "video_id": "v3", "title": "", "best_percent": null, "total_watch_sec": null }, { "video_id": "v4" }]`.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected (wanted):** each row renders; empty title falls back to something clickable (e.g. "Untitled" or the id); progress/watched show "0%"/"0m" or "—". **Current:** `formatPercent(undefined)` gives "NaN%" and `formatWatchTime(undefined)` gives "NaNm"; an empty title makes an empty, unclickable link (currently unhandled — likely bug). No crash.

### SPR-13 — Remove a history item

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** as SPR-01; `DELETE /api/me/history/v1` → 204.
- **Steps:**
  1. Click "Remove" in the "Tafsir 1" row.
- **Expected:** One `DELETE /api/me/history/v1`. The row disappears; "Fiqh 2" stays. No toast on success. No confirmation step (the delete also clears the resume point; if a confirm is added, assert it).

### SPR-14 — Remove fails: row stays, error toast

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `DELETE` → 404 `{ "title": "Not Found", "status": 404 }`; then 500; then network reject.
- **Steps:**
  1. Click "Remove" on "Tafsir 1".
- **Expected:** Error toast "Not Found" (or `common.error` for network). The row stays.

### SPR-15 — Removing the last item shows the empty text

- **Priority:** P3
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** one item; DELETE → 204.
- **Steps:**
  1. Click "Remove".
- **Expected:** Table replaced by "Nothing here yet."

### SPR-16 — Video title links to the video page; Back returns

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** as SPR-01; a second item with `video_id: "a/b?c"`.
- **Steps:**
  1. Click "Tafsir 1".
  2. Press Back.
- **Expected:** Goes to `/videos/v1`; Back shows `/settings/privacy` with the table again (data refetched). The odd id should be URL-encoded (`/videos/a%2Fb%3Fc`); **current** code does not encode it (likely bug, low impact).

### SPR-17 — Signed-out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected:** Redirect to `/login`; no `/api/me/history` or `/api/me/activity/settings` request.

### SPR-18 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full app with `AppShell`; start at `/settings`. History → `{}` (makes the tab throw, see SPR-10).
- **Steps:**
  1. Click "Privacy".
  2. Press browser Back; then click "Appearance" in the Settings nav and a sidebar link.
- **Expected:** A route-level error fallback (not a blank page). Back shows the Account tab without a reload; other links render.

### SPR-19 — Arabic: RTL, Arabic strings, Arabic titles

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `mkt:locale=ar`; item title "تفسير سورة البقرة".
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected:** `dir="rtl"`; headings "تتبّع النشاط", "سجل المشاهدة"; switch "التتبّع مُفعّل"; headers "الفيديو", "التقدم", "تمت المشاهدة"; button "إزالة". The title cell has `dir="auto"` and renders correctly; columns mirror (Video column at the right).

### SPR-20 — Phone width: history table does not cause page scroll

- **Priority:** P2
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e 375x812; long title (120 chars).
- **Steps:**
  1. Open `/settings/privacy`.
- **Expected:** Table wraps or scrolls inside its own container; `document.documentElement.scrollWidth <= innerWidth`; "Remove" reachable.

### SPR-21 — Keyboard and labels: switch, table, Remove buttons

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Settings.test.tsx`; it.fails: Remove not named per row)
- **Preconditions / data:** as SPR-01.
- **Steps:**
  1. Tab to the switch, press Space; Tab to each "Remove".
  2. Run axe.
- **Expected:** Space toggles and saves. Each "Remove" is reachable with a visible focus ring. Wanted: each Remove names its row (e.g. `aria-label="Remove Tafsir 1"`); **current** all are just "Remove" (minor a11y gap). The 4th header cell is empty; axe may flag it (`empty-table-header`). The toast is announced.

### SPR-22 — Both themes readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e light and dark.
- **Steps:**
  1. Open `/settings/privacy` in each theme.
- **Expected:** Switch track, table borders, muted hint and links readable in both.
