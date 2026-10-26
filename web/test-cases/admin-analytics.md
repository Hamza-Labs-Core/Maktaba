# Admin Analytics

- **Route:** `/admin/analytics` (no params; the tab, range and filters live in component state only, not in the URL)
- **Component:** `src/pages/Admin/Analytics.tsx` (header, range and filters, `Tabs`); the tabs in `src/pages/Admin/analytics/tabs.tsx` (`OverviewTab`, `NowTab`, `VideosTab`, `LiveTVTab`, `PeopleTab`, `AppsTab`); shared pieces in `src/pages/Admin/analytics/parts.tsx` (`useLoad`, `Kpi`, `Panel`, null guards); charts `LineChart`, `BarList`, `Heatmap` from `src/components/charts/Charts.tsx`; design-system `Tabs`, `Card`, `Select`, `Input`, `Button`, `EmptyState`
- **API calls** (all in `src/lib/analytics.ts`, `analyticsApi`; every one takes the range, `range=today|7d|30d|90d` or `from=&to=` dates, and the filters `library`, `user`, `client_app`, `surface`):
  - `GET /api/admin/analytics/summary` via `summary()` → `{ watch_sec, video_sec, tv_sec, live_tv_hours, plays, viewings, people, completion_rate, surfaces, apps, platforms, devices }` (`refresh=true` after Refresh)
  - `GET /api/admin/analytics/activity` via `activityStats()` → `{ bucket, series: [{ bucket, watch_sec, video_sec, tv_sec, plays, viewings }], heatmap: number[7][24] }` (row 0 = Sunday)
  - `GET /api/admin/analytics/now` via `now()` → `{ plays: NowPlay[], viewings: NowViewing[], counts, at }`, every 15 s while the Now tab is open
  - `GET /api/admin/analytics/top-videos?limit=20`, `libraries`, `genres` (Videos tab)
  - `GET /api/admin/analytics/top-channels?limit=20` → `{ channels, surfaces, peak_viewers, peak_at }`, `remote-tv` → `{ minutes_delivered, cost_usd, days, … }` (Live TV tab)
  - `GET /api/admin/analytics/people?limit=100` (People tab); `people?limit=500` and `libraries` for the range alone fill the Person and Library filter lists
  - `GET /api/admin/analytics/apps` → `{ apps, trend, platforms, devices }` (Apps tab)
  - Export links: `<a href="/api/admin/analytics/export?format=csv|json&…range and filters">` via `exportUrl()`
  - Only the open tab's endpoints are asked.
- **Access:** admin only. `RequireAuth` (signed out → `/login`), then `AdminGate` (non-admin → `common.forbidden.*`). The server 403s non-admins on every endpoint (`api/internal/handlers/analytics/roles_test.go`).
- **Existing tests:** `src/pages/Admin/Analytics.test.tsx` (every case below but ANL-24..26); `src/lib/analytics.test.ts` covers the formatters.

## Summary

| ID     | Title                                                              | Category      | Priority | Covered by existing test?                                             |
| ------ | ------------------------------------------------------------------ | ------------- | -------- | --------------------------------------------------------------------- |
| ANL-01 | Overview: numbers, both breakdowns, trend and heatmap              | Happy path    | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-02 | The range drives every call and the export; custom sends from/to   | Happy path    | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-03 | The filters reach every call and the export; Clear filters         | Happy path    | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-04 | Now lists plays and viewings with app, device and way of watching  | Happy path    | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-05 | Now asks every 15 s and keeps the last list on error               | Happy path    | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-06 | Now with nobody watching                                           | Empty state   | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-07 | Videos: plays, completion, top videos, libraries, genres           | Happy path    | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-08 | Live TV: channels, peak, ways of watching, remote TV minutes, cost | Happy path    | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-09 | People: watch time, apps, last activity                            | Happy path    | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-10 | Apps: people, requests, errors, speed, versions, by platform       | Happy path    | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-11 | Each tab with nothing in the range shows its empty message         | Empty state   | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-12 | Every tab survives null and missing lists                          | Empty state   | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-13 | One endpoint fails; the other cards still fill in                  | Error         | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-14 | Refresh asks again, the summary skipping the cache                 | Error         | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-15 | A late answer for an old range does not win; a failure clears      | Error         | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-16 | Non-admin sees "Admins only", nothing is asked                     | Permissions   | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-17 | Signed-out redirect to `/login`                                    | Permissions   | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-18 | Now stops asking when the tab or the page is left                  | Navigation    | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-19 | Render crash shows the route error fallback; Back works            | Error         | P1       | Yes (`Analytics.test.tsx`)                                            |
| ANL-20 | Arabic / RTL                                                       | RTL           | P3       | Yes (`Analytics.test.tsx`)                                            |
| ANL-21 | Every page string is in both catalogs                              | RTL           | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-22 | Accessibility: tabs, controls, charts and tables are labelled      | Accessibility | P2       | Yes (`Analytics.test.tsx`)                                            |
| ANL-23 | Chart maths: scaling, ticks, bar widths, heatmap opacity           | Happy path    | P2       | Partial (`Analytics.test.tsx`; negative-width clamp is it.fails: bug) |
| ANL-24 | Phone width (375 px)                                               | Responsive    | P3       | No (e2e-only)                                                         |
| ANL-25 | Light and dark theme for charts                                    | Theme         | P3       | No                                                                    |
| ANL-26 | Loading placeholders before data arrives                           | Loading       | P3       | No                                                                    |

## Cases

Shared fixtures (in `Analytics.test.tsx`): `SUMMARY` (2h 30m, 42 plays, 7 viewings, 5 people, 1 Live TV hour, completion 0.634, all four ways of watching, apps player and unknown), `ACT` (7 days, a heatmap with one cell), `NOW` (sara playing Omar E1 on player 1.3.0 / phone / ios; a tuner viewing of 1 Quran; lina on the remote TV), `TOP`, `LIBS` (Series), `GENRES` (Drama), `CHANNELS` (Quran: 3 viewings, 2h 19m, peak 2), `REMOTE` (180 min, $0.18), `PEOPLE` (sara), `APPS` (player: 150 requests, 5 errors, ios and android). Signed in as admin. Fake timers for the polling cases.

### ANL-01 — Overview: numbers, both breakdowns, trend and heatmap

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open `/admin/analytics`.
- **Expected:** Heading "Analytics"; the Overview tab selected. KPIs Watch time "2h 30m", Plays "42", Live TV viewings "7", People watching "5", Live TV hours "1". "Watch time by way of watching" lists Videos in apps, Live TV in apps, Plex Live TV, Remote TV; "Watch time by app" lists player and "Unknown app". The trend and heatmap charts render. Only summary and activity (and the filter lists) are asked: no now, top-videos or apps.

### ANL-02 — The range drives every call and the export; custom sends from/to

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Choose Today, Last 30 days, Last 90 days.
  2. Choose Custom; set From 2026-10-01 and To 2026-10-03.
- **Expected:** Each range is sent as `range=` to summary and activity, and the export links follow. Custom shows From and To (prefilled with the last week); the calls send `from` and `to` and no `range`; the CSV link is `…/export?format=csv&from=2026-10-01&to=2026-10-03`.

### ANL-03 — The filters reach every call and the export; Clear filters

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Pick Library Series, Person sara, App player, Way of watching Videos in apps.
  2. Open Apps. Click Clear filters.
- **Expected:** The Library and Person lists hold the range's libraries and people. Every call sends `library`, `user`, `client_app` and `surface`; so do the export links. The filter lists themselves are read for the range alone. Apps is asked with `client_app=player`, then without after Clear filters, which resets every select and hides itself.

### ANL-04 — Now lists plays and viewings with app, device and way of watching

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** `NOW`
- **Steps:**
  1. Open the Now tab.
- **Expected:** Table "Watching now" with Person, Watching, Way, App, Device, Elapsed, Progress. sara / Omar E1 / Videos in apps / player 1.3.0 / phone · ios / 25m / 42%; "Plex (tuner)" / 1 Quran / Plex Live TV / Unknown app; lina / Remote TV. KPIs per way of watching (1, 0, 1, 1). The note "Refreshes every 15 seconds.".

### ANL-05 — Now asks every 15 s and keeps the last list on error

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fake timers; now → `NOW`, then 500, then empty lists
- **Steps:**
  1. Open Now; advance 15 s twice.
- **Expected:** One ask on opening and one per 15 s. After the failed ask the table still shows sara, with no alert; the third ask shows "Nobody is watching right now.".

### ANL-06 — Now with nobody watching

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** now → empty lists
- **Steps:**
  1. Open Now.
- **Expected:** "Nobody is watching right now.", no table, the KPIs at 0.

### ANL-07 — Videos: plays, completion, top videos, libraries, genres

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open Videos.
- **Expected:** Table "Most watched videos": Omar E1, 10 plays, 1h, 3 viewers, 50%. KPIs Completion rate "63%", Video watch time "1h 30m". Lists Libraries (Series) and Genres (Drama). top-videos asked with `limit=20`.

### ANL-08 — Live TV: channels, peak, ways of watching, remote TV minutes, cost

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open Live TV.
- **Expected:** Table "Most watched channels": 1 Quran, 2h 19m, "Live TV in apps 1h · Remote TV 59m". KPIs Peak viewers "2", Remote TV minutes "180", Remote TV cost "$0.18"; "Busiest at …". Table "Remote TV" by day ($0.12). List "Live TV by way of watching"; chart "Live TV over time".

### ANL-09 — People: watch time, apps, last activity

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open People.
- **Expected:** Table "People": sara, 1h 35m, "player, tv, Unknown app", last active 2026-10-26.

### ANL-10 — Apps: people, requests, errors, speed, versions, by platform

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open Apps.
- **Expected:** Table "Apps": player, 150 requests, 3.3% errors, 83 ms, "1.3.0 (1), 1.2.0 (1)". Table "Apps by platform" with ios and android. KPIs Requests 150, Errors 5, People using apps 2. Chart "Requests per day"; list "Devices watched on" (phone).

### ANL-11 — Each tab with nothing in the range shows its empty message

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** each endpoint returns empty lists (or a zero summary)
- **Steps:**
  1. Open each tab.
- **Expected:** "Nothing was watched in this range." (Overview), "No video was played in this range.", "No channel was watched in this range.", "The remote TV delivered nothing in this range.", "Nobody watched or used an app in this range.", "No app called the server in this range.".

### ANL-12 — Every tab survives null and missing lists

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** every list field `null`
- **Steps:**
  1. Open each tab.
- **Expected:** The tab renders (empty lists, "—"), no crash, no alert.

### ANL-13 — One endpoint fails; the other cards still fill in

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** activity → 500 "activity is down"
- **Steps:**
  1. Open the page.
- **Expected:** An alert with the API's message; the KPIs from summary still show.

### ANL-14 — Refresh asks again, the summary skipping the cache

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Click Refresh.
- **Expected:** summary is asked again with `refresh=true`; activity is asked again.

### ANL-15 — A late answer for an old range does not win; a failure clears

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** a) the 7d summary answers late; b) the 30d summary fails
- **Steps:**
  1. Switch to Last 30 days.
- **Expected:** a) The 30d numbers stay after the late 7d answer. b) The alert shows and Plays reads "—", not the 7d number.

### ANL-16 — Non-admin sees "Admins only", nothing is asked

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** a member signed in
- **Steps:**
  1. Open the page; wait 30 s.
- **Expected:** "Admins only"; no request at all.

### ANL-17 — Signed-out redirect to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** signed out
- **Steps:**
  1. Open the page.
- **Expected:** The login page, from `/admin/analytics`; no request.

### ANL-18 — Now stops asking when the tab or the page is left

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fake timers
- **Steps:**
  1. Open Now; switch to People; wait 60 s. Back to Now; leave the page; wait 60 s.
- **Expected:** No now request while another tab is open or after leaving.

### ANL-19 — Render crash shows the route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** a top video whose title throws when read
- **Steps:**
  1. Open Videos; go elsewhere; fix the data; Back; open Videos.
- **Expected:** The route error fallback; after Back the table shows Omar E1 without a reload.

### ANL-20 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** locale `ar`
- **Steps:**
  1. Open the page.
- **Expected:** `dir="rtl"`; the heading, the six tabs and the filters in Arabic. Names, titles and versions are `dir="auto"`.

### ANL-21 — Every page string is in both catalogs

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** `en.json`, `ar.json`
- **Steps:**
  1. Compare the `analytics.*` keys.
- **Expected:** The same keys, none empty in Arabic.

### ANL-22 — Accessibility: tabs, controls, charts and tables are labelled

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Analytics.test.tsx`)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open the page and the Now tab.
- **Expected:** A tablist "Analytics sections" with six tabs; a group "Filters" with labelled selects (Range, Library, Person, App, Way of watching); charts are `role="img"` with names; the Now table is labelled with seven column headers.

### ANL-23 — Chart maths: scaling, ticks, bar widths, heatmap opacity

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial (`Analytics.test.tsx`; negative-width clamp is it.fails: bug)
- **Preconditions / data:** the chart components alone
- **Steps:**
  1. Render LineChart (30 points), BarList, Heatmap.
- **Expected:** Six ticks, finite coordinates, the top point at the top padding; bar widths relative to the max (a negative value should give 0%: not yet); heatmap opacity scales with the value.

### ANL-24 — Phone width (375 px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** fixtures
- **Steps:**
  1. Open the page at 375 px; open each tab.
- **Expected:** The filters wrap to one or two per row, the tabs scroll, tables scroll sideways inside their card; no page-wide horizontal scroll.

### ANL-25 — Light and dark theme for charts

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** fixtures
- **Steps:**
  1. Switch the theme.
- **Expected:** Charts, bars and heatmap cells use the theme's tokens and stay readable.

### ANL-26 — Loading placeholders before data arrives

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** endpoints that never answer
- **Steps:**
  1. Open each tab.
- **Expected:** KPIs read "—" and each card says "Loading…" until its data arrives.
