# Admin Watch Analytics

- **Route:** `/admin/analytics` (no params; the range lives in component state only, not in the URL)
- **Component:** `src/pages/Admin/Analytics.tsx`; children: `LineChart`, `BarList`, `Heatmap` from `src/components/charts/Charts.tsx`; design-system `Card`, `Select`, `Button`, `EmptyState`
- **API calls** (all in `src/lib/analytics.ts`, `analyticsApi`):
  - `GET /api/admin/analytics/summary?range={r}` via `summary()` → `Summary { range, total_watch_sec, total_sessions, unique_viewers, completion_rate (0..1), devices: CountStat[], platforms: CountStat[], libraries: LabelStat[], genres: CountStat[] }`
  - `GET /api/admin/analytics/top-videos?range={r}&limit=10` via `topVideos()` → `{ videos: TopVideo[], range }`
  - `GET /api/admin/analytics/activity?range={r}&bucket=day` via `activityStats()` → `ActivityResponse { bucket, series: [{ bucket, watch_sec, sessions }], heatmap: number[7][24] }` (row 0 = Sunday)
  - `GET /api/admin/analytics/users?range={r}&limit=10` via `users()` → `{ users: ActiveUser[], range }`
  - The four above run together with `Promise.all` on mount, on range change and on "Try again".
  - `GET /api/admin/analytics/live` via `live()` → `{ sessions: LiveSession[] }`, polled every 15 s (`setInterval`), errors ignored
  - Export links: `<a href="/api/admin/analytics/export?format=csv|json&range={r}">` via `exportUrl()`
- **Access:** admin only. `RequireAuth` (signed out → `/login`), then `AdminGate` (non-admin → `common.forbidden.*`). Server 403s non-admins.
- **Existing tests:** No component test for `Analytics.tsx` or `Charts.tsx`. `src/lib/analytics.test.ts` covers only `formatWatchTime`, `formatPercent`, `formatPercentRatio`, `platformFromUA`. Gaps: everything on the page.

## Summary

| ID     | Title                                                        | Category      | Priority | Covered by existing test? |
| ------ | ------------------------------------------------------------ | ------------- | -------- | ------------------------- |
| ANL-01 | Dashboard renders all cards from full data                   | Happy path    | P1       | No                        |
| ANL-02 | Range change refetches all four endpoints and export links   | Happy path    | P1       | No                        |
| ANL-03 | Live card polls every 15 s and keeps the last value on error | Happy path    | P2       | No                        |
| ANL-04 | Live: `sessions: []` shows "Nobody is watching right now."   | Empty state   | P2       | No                        |
| ANL-05 | Live: `sessions: null` or missing key                        | Empty state   | P1       | No                        |
| ANL-06 | Empty series and lists render placeholders                   | Empty state   | P2       | No                        |
| ANL-07 | Null / missing list fields in summary, top videos, users     | Empty state   | P1       | No                        |
| ANL-08 | Activity `heatmap` null, missing, empty or ragged            | Empty state   | P1       | No                        |
| ANL-09 | Activity `series` null / missing; one point; bad values      | Empty state   | P2       | No                        |
| ANL-10 | Summary numbers null or missing                              | Empty state   | P2       | No                        |
| ANL-11 | Loading placeholders before data arrives                     | Loading       | P3       | No                        |
| ANL-12 | One endpoint fails (500, 403, network)                       | Error         | P1       | No                        |
| ANL-13 | Malformed JSON / HTML 200 response                           | Error         | P1       | No                        |
| ANL-14 | "Try again" clears the error and refetches                   | Error         | P2       | No                        |
| ANL-15 | Fast range switching: stale response does not win            | Error         | P2       | No                        |
| ANL-16 | Error from an old range is not shown over new data           | Error         | P3       | No                        |
| ANL-17 | Non-admin sees "Admins only", no requests, no polling        | Permissions   | P1       | No                        |
| ANL-18 | Signed-out redirect to `/login`                              | Permissions   | P1       | No                        |
| ANL-19 | Polling stops when leaving the page                          | Navigation    | P2       | No                        |
| ANL-20 | Deep link, reload and Back keep a sane range                 | Navigation    | P3       | No                        |
| ANL-21 | Render crash shows route error fallback; Back works          | Error         | P1       | No                        |
| ANL-22 | Chart maths: scaling, ticks, bar widths, heatmap opacity     | Happy path    | P2       | No                        |
| ANL-23 | Phone width (375 px)                                         | Responsive    | P3       | No                        |
| ANL-24 | Arabic / RTL                                                 | RTL           | P3       | No                        |
| ANL-25 | Light and dark theme for charts                              | Theme         | P3       | No                        |
| ANL-26 | Accessibility: chart labels, table, alert, controls          | Accessibility | P2       | No                        |

## Cases

Shared fixtures:

- `SUMMARY`: `{ "range": "7d", "total_watch_sec": 9000, "total_sessions": 42, "unique_viewers": 5, "completion_rate": 0.634, "devices": [{ "label": "web", "sessions": 30, "watch_sec": 6000 }, { "label": "mobile", "sessions": 12, "watch_sec": 3000 }], "platforms": [], "libraries": [{ "id": "l1", "label": "Series", "sessions": 40, "watch_sec": 8000 }], "genres": [{ "label": "Drama", "sessions": 20, "watch_sec": 5000 }] }`
- `TOP`: `{ "range": "7d", "videos": [{ "video_id": "v1", "title": "Omar E1", "sessions": 10, "watch_sec": 3600, "unique_viewers": 3 }] }`
- `ACT`: `{ "bucket": "day", "series": [{ "bucket": "2026-10-13", "watch_sec": 600, "sessions": 2 }, ... 7 points], "heatmap": <7 arrays of 24 numbers, one cell 3600, rest 0> }`
- `USERS`: `{ "range": "7d", "users": [{ "user_id": "u1", "username": "sara", "watch_sec": 5400, "sessions": 9, "last_seen_at": "2026-10-18T20:00:00Z" }] }`
- `LIVE`: `{ "sessions": [{ "session_id": "s1", "user_id": "u1", "username": "sara", "video_id": "v1", "title": "Omar E1", "started_at": "...", "elapsed_sec": 1500, "percent_complete": 41.6, "device_type": "web" }] }`
- Signed in as admin. Use fake timers for polling cases.

### ANL-01 — Dashboard renders all cards from full data

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** all endpoints return the shared fixtures
- **Steps:**
  1. Open `/admin/analytics`.
- **Expected:** Heading "Watch analytics"; Range select "Range" with value "Last 7 days". KPIs: Watch time "2h 30m", Sessions "42", Unique viewers "5", Completion rate "63%". "Currently watching" table (labelled the same) with columns User, Video, Elapsed, Progress, Device and a row sara / Omar E1 / 25m / 42% / web. Line chart `role="img"` named "Watch time over time" with a path. Heatmap `role="img"` named "Peak viewing hours" with 7 rows labelled Sun..Sat and 24 cells each. Bar lists: "Most watched" (Omar E1, "10 · 1h"), "Most active users" (sara, "1h 30m"), "Popular genres" (Drama, "20"), "Devices" (web, mobile), "Library usage" (Series, "2h 13m"). No error alert.

### ANL-02 — Range change refetches all four endpoints and export links

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** fixtures; record requested URLs
- **Steps:**
  1. Open the page; choose each range: Today, Last 30 days, ..., All time.
- **Expected:** On mount: summary, top-videos, activity, users requested with `range=7d` (top-videos and users with `limit=10`, activity with `bucket=day`). Each change sends the four requests again with the new `range` value (`today`, `30d`, `90d`, `1y`, `all`) and the cards update. The live endpoint is not refetched by a range change. "Export CSV" / "Export JSON" hrefs become `/api/admin/analytics/export?format=csv&range=30d` etc. Activity bucket stays `day` even for `1y` / `all` (365+ points; check the chart stays readable — consider week/month buckets).

### ANL-03 — Live card polls every 15 s and keeps the last value on error

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; live → `LIVE`, then 500, then `{ "sessions": [] }`
- **Steps:**
  1. Open the page; advance 15 s; advance 15 s again.
- **Expected:** One live request on mount, one per 15 s after. When the second call fails, the table still shows sara (no error shown, no toast). The third call empties it and shows "Nobody is watching right now.". The 15 s poll does not refetch summary/top/activity/users.

### ANL-04 — Live: `sessions: []` shows "Nobody is watching right now."

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** live → `{ "sessions": [] }`
- **Steps:**
  1. Open the page.
- **Expected:** The live card shows EmptyState "Nobody is watching right now."; no table.

### ANL-05 — Live: `sessions: null` or missing key

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** live → a) `{ "sessions": null }`; b) `{}`; c) a session with `username: null`, `title: null`, `device_type` missing, `elapsed_sec: null`, `percent_complete: null`
- **Steps:**
  1. Open the page for each.
- **Expected:** a, b) "Nobody is watching right now." and the rest of the dashboard renders. c) Row renders with blanks / "—", "0m", "0%". (currently unhandled — likely bug: `Analytics.tsx:74` stores `l.sessions` as is; `null` leaves the card on "Loading…" for ever, and a missing key gives `undefined`, which passes the `live === null` check and throws on `live.length` at `Analytics.tsx:134`, blanking the page.)

### ANL-06 — Empty series and lists render placeholders

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** summary with `devices: [], platforms: [], libraries: [], genres: []`, zero totals; top `{ "videos": [] }`; users `{ "users": [] }`; activity `{ "bucket": "day", "series": [], "heatmap": [[0 x24] x7] }`
- **Steps:**
  1. Open the page.
- **Expected:** KPIs "0m", "0", "0", "0%". Line chart is an `svg` `role="img"` with the "—" placeholder text and no path. Each bar list card shows "—". Heatmap shows 7 × 24 cells all at the low opacity (0.06), no NaN in styles. A friendlier empty message ("No watch activity in this range") would be better than "—" (suggestion).

### ANL-07 — Null / missing list fields in summary, top videos, users

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** run once with each field `null` and once with the key removed: `summary.devices`, `summary.genres`, `summary.libraries`, `summary.platforms`, `topVideos.videos`, `users.users`; also items with `title: null`, `username: null`, `label: null`
- **Steps:**
  1. Open the page.
- **Expected:** Each affected card shows "—"; the other cards render normally; no crash, no console error. Items with null labels render an empty or fallback label without throwing (`BarList` uses the label in a React key and `title`).

### ANL-08 — Activity `heatmap` null, missing, empty or ragged

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** activity → a) `{ "series": [], "heatmap": null }`; b) `{ "series": [] }`; c) `{ "series": [], "heatmap": [] }`; d) `heatmap` with 7 rows where row 3 is `null`; e) rows of 12 values instead of 24
- **Steps:**
  1. Open the page for each.
- **Expected:** The Peak viewing hours card shows an empty placeholder and the rest of the page renders. (currently unhandled — likely bug: `Charts.tsx:125` calls `matrix.flat()` and `Charts.tsx:132` calls `row.map()`; a) and b) throw and blank the whole dashboard, d) throws too; c) renders an empty box with no message.) e) renders 12 cells per row without crashing.

### ANL-09 — Activity `series` null / missing; one point; bad values

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** activity with valid heatmap and a) `series: null`; b) no `series` key; c) one point `[{ "bucket": "2026-10-19", "watch_sec": 300 }]`; d) points with `watch_sec: null` and `"bucket": null`; e) all zero values
- **Steps:**
  1. Open the page for each.
- **Expected:** a, b) Line chart placeholder "—". c) A single dot/flat line at x = left padding, one tick label, no NaN in the `d` attribute. d) No `NaN` in any path (null treated as 0), empty tick label. e) Flat line at the bottom (max clamps to 1).

### ANL-10 — Summary numbers null or missing

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** summary `{ "total_watch_sec": null, "total_sessions": null, "unique_viewers": null, "completion_rate": null }`; second run with those keys missing
- **Steps:**
  1. Open the page.
- **Expected:** KPIs show "0m"/"0"/"0"/"0%" or "—"; never "null", "undefined", "NaNm" or "NaN%". (currently unhandled — likely bug: `String(summary.total_sessions)` gives "null"/"undefined", and `formatWatchTime(undefined)` gives "NaNm", `formatPercentRatio(undefined)` gives "NaN%".)

### ANL-11 — Loading placeholders before data arrives

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** all endpoints pending
- **Steps:**
  1. Open the page.
- **Expected:** Header, range select and export links show at once. Live card and Peak hours card show "Loading…"; KPIs show "—"; line chart and bar lists show their "—" placeholder (no spinner — acceptable but note that the user cannot tell loading from empty there).

### ANL-12 — One endpoint fails (500, 403, network)

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET .../top-videos` → a) 500 `{ "title": "Internal error", "detail": "db down" }`; b) 403 `{ "title": "Forbidden" }`; c) fetch rejects `TypeError("Failed to fetch")`; other three succeed
- **Steps:**
  1. Open the page for each.
- **Expected:** An alert (`role="alert"`) with a readable message: a) "Internal error: db down"; b) "Forbidden"; c) "Failed to fetch" (better: the localised "Something went wrong."). Because of `Promise.all`, none of the four cards gets data even though three calls succeeded — KPIs stay "—". Expected would be partial rendering of the cards that loaded (currently all-or-nothing — likely bug). The live card is independent and still renders.

### ANL-13 — Malformed JSON / HTML 200 response

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** a) `GET .../summary` → 200, `Content-Type: application/json`, body `{bad`; b) `GET .../activity` → 200, `Content-Type: text/html`, body `<!doctype html>...` (SPA fallback through a misrouted proxy)
- **Steps:**
  1. Open the page for each.
- **Expected:** a) Alert with a message and the page stays usable (currently the raw `SyntaxError` text in English). b) The page does not crash; the activity cards show placeholders or an error. (currently unhandled — likely bug: `request()` returns the HTML string as the response, `activity.heatmap` is `undefined` and `Heatmap` throws on `matrix.flat()`.)

### ANL-14 — "Try again" clears the error and refetches

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** first summary call → 500; second → `SUMMARY`
- **Steps:**
  1. Open the page; see the alert.
  2. Click "Try again" (`common.retry`).
- **Expected:** The alert disappears as the retry starts; the four endpoints are requested again with the current range; cards fill in. "Try again" is also there with no error and refetches without clearing data (OK).

### ANL-15 — Fast range switching: stale response does not win

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `range=7d` responses delayed 500 ms with `total_sessions: 7`; `range=30d` responses immediate with `total_sessions: 30`
- **Steps:**
  1. Open the page; immediately change the range to "Last 30 days"; wait 1 s.
- **Expected:** Sessions KPI shows "30" and stays at 30. (currently unhandled — likely bug: `load()` has no abort or "latest request" guard, so the late 7d response overwrites the 30d data while the select says 30 days.)

### ANL-16 — Error from an old range is not shown over new data

- **Priority:** P3
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `range=7d` summary rejects after 500 ms; `range=30d` all succeed at once
- **Steps:**
  1. Open the page; switch to 30d right away; wait.
- **Expected:** No error alert for the abandoned 7d request; 30d data shown. (Same missing guard as ANL-15.)

### ANL-17 — Non-admin sees "Admins only", no requests, no polling

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** signed in as `{ username: "bob", is_admin: false }`; fake timers
- **Steps:**
  1. Open `/admin/analytics`; advance 30 s.
- **Expected:** ErrorState "Admins only"; zero requests to `/api/admin/analytics/*`; no interval running; no "Analytics" link in the sidebar.

### ANL-18 — Signed-out redirect to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** no session
- **Steps:**
  1. Open `/admin/analytics` directly.
- **Expected:** Redirect to `/login` with `state.from = "/admin/analytics"`; no analytics request is sent.

### ANL-19 — Polling stops when leaving the page

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; fixtures
- **Steps:**
  1. Open the page; click another sidebar link (e.g. "Channels"); advance 60 s.
  2. Press browser Back.
- **Expected:** After leaving, no more `/api/admin/analytics/live` requests (interval cleared on unmount). Back renders the dashboard again with a fresh load and one new interval (not two).

### ANL-20 — Deep link, reload and Back keep a sane range

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open `/admin/analytics?range=30d` directly.
  2. Choose "Last 90 days"; reload.
  3. Click "Export CSV".
- **Expected:** The query string is ignored today (range starts at "Last 7 days") and reload resets to 7 days; if range-in-URL is wanted, it should be added (suggestion). Export link points at the current range and downloads without leaving the SPA in a broken state (it is a plain link; a 403/500 there shows the browser's raw response — note).

### ANL-21 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** start on `/admin/channels`; activity → `{ "series": [], "heatmap": null }` (throws in `Heatmap`, see ANL-08), or mock `Heatmap` to throw
- **Steps:**
  1. Navigate to `/admin/analytics` from the sidebar.
  2. Press browser Back; then click "Analytics" again with good data.
- **Expected:** A route-level error fallback with the shell and nav, not a blank page. Back renders `/admin/channels` without a reload. Returning with good data renders the dashboard; the old interval from the crashed page is not left running.

### ANL-22 — Chart maths: scaling, ticks, bar widths, heatmap opacity

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** render the charts directly: `LineChart` with 30 points (values 0..2900); `BarList` with values `[50, 100, 0]`; `Heatmap` with one cell 100 and one 50, rest 0; negative value `-5` in a bar list
- **Steps:**
  1. Render each and read attributes.
- **Expected:** LineChart: 6 tick labels shown (every 5th point), highest point at y = top padding, all coordinates finite. BarList widths "50%", "100%", "0%"; the `sub` text shown instead of the raw value when given. Heatmap: the 100 cell opacity 1, the 50 cell 0.575, zero cells 0.06, `title` like "Sun 0:00 — 100s". A negative value must not give a negative CSS width (currently gives "-5%" — harmless but worth clamping).

### ANL-23 — Phone width (375 px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375 × 812; fixtures with long video titles
- **Steps:**
  1. Open the page; scroll through.
- **Expected:** No horizontal page scroll; controls wrap under the heading; KPI cards stack in 2 columns or 1; the live table scrolls inside its card; the SVG line chart scales to the card width; the 24-column heatmap fits or scrolls inside its card; long bar labels ellipsize (they have `title`).

### ANL-24 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** language Arabic; fixtures with Arabic titles (`"title": "عمر الحلقة ١"`) and usernames
- **Steps:**
  1. Open the page.
- **Expected:** `dir="rtl"`; heading "تحليلات المشاهدة", range labels Arabic, day labels Arabic. Arabic titles and usernames render (cells use `dir="auto"`). Bar lists and the heatmap mirror. Note: `formatWatchTime` returns "2h 30m" and percentages with Latin "h"/"m" in Arabic too (not localised — likely bug / polish). Line chart time axis still reads oldest to newest in a sensible direction.

### ANL-25 — Light and dark theme for charts

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme="light"` then `"dark"`
- **Steps:**
  1. View KPIs, line chart, bar lists, heatmap.
- **Expected:** Chart strokes and fills use `currentColor` / theme tokens and are visible on both backgrounds; tick text readable; heatmap low cells still visible against the card in dark mode; the error alert readable.

### ANL-26 — Accessibility: chart labels, table, alert, controls

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** fixtures
- **Steps:**
  1. Tab through: range select, Export CSV, Export JSON, Try again.
  2. Query by role.
- **Expected:** Visible focus on each control; the select has the label "Range". Each chart has `role="img"` with its card's title as name; bar lists are `ul` with `aria-label`. The live table has an `aria-label` and header cells. The error paragraph has `role="alert"`. Note: charts expose no data to screen readers beyond the name — consider a visually hidden table or summary (suggestion).
