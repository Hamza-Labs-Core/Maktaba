# System health

- **Route:** `/admin/health` (no params, no query-string state)
- **Component:** `src/pages/Admin/SystemHealth.tsx` (`SystemHealth` -> `AdminGate` -> `HealthInner`); children: `ProcessesCard` in `src/pages/Admin/Processes.tsx`, DS `Card`, `Badge`
- **API calls** (all through `api.get` in `src/lib/api.ts`; all fired on mount and again every 30 s by `setInterval(refresh, 30_000)`; `ProcessesCard` polls on its own every 15 s):
  - `GET /api/system/health` -> `{ status, services: { [name]: { status, checks?: { [name]: { status, reason? } }, reason? } }, disk_free_bytes?, queue_depth?, transcribe_budget_usd_left? }`
  - `GET /api/system/version` -> `{ version, build_sha, build_time, go_version, schema_revision }`
  - `GET /api/admin/caches` -> `{ caches: string[] }`
  - `GET /api/admin/perf/budgets` -> `{ Version, Endpoints }` (PascalCase) or `{ endpoints: [] }`
  - `GET /api/tv/cluster` via `tvClusterApi.get()` (`src/lib/tvClusterApi.ts`) -> `TvClusterView` (`hosts: StreamingHost[]`, `on_air: OnAirChannel[]`, `failovers_24h?`)
  - `GET /api/tv/remote` via `tvRemoteApi.status()` (`src/lib/tvRemoteApi.ts`) -> `{ integration: { configured, enabled, ... }, remote?: { ready, ... } }`
  - `GET /api/system/processes` via `ProcessesCard` -> `{ available, reason?, processes: ProcessStatus[], events: ProcessEvent[] }`
- **Access:** signed-in admin only. `RequireAuth` (App.tsx) redirects signed-out users to `/login`; `AdminGate` shows `common.forbidden.title` "Admins only" / `common.forbidden.desc` for non-admins. Server also 403s.
- **Existing tests:** No test for `SystemHealth.tsx`. `src/pages/Admin/Processes.test.tsx` covers `ProcessesCard` alone: process rows with state/restarts/last crash/crash loop, recent crashes list (filters out shutdown), `available: false` message, request failure shows "Could not load this section.". Gaps: the whole page (overall badge, stats, services, caches, budgets, version, TV line), permissions, null fields, polling, crash recovery, RTL, theme, responsive.

Notes for the implementer: mock `../../lib/api` `api.get` with a URL switch (as `Processes.test.tsx` does) and `../../lib/auth` `useAuth`. Render inside `I18nProvider` (+ `MemoryRouter` for the navigation cases).

## Summary

| ID     | Title                                                         | Category      | Priority | Covered by existing test?   |
| ------ | ------------------------------------------------------------- | ------------- | -------- | --------------------------- |
| HLT-01 | Full dashboard renders when every endpoint answers            | Happy path    | P1       | Yes (SystemHealth.test.tsx) |
| HLT-02 | Non-admin sees "Admins only" and no admin calls are made      | Permissions   | P1       | Yes (SystemHealth.test.tsx) |
| HLT-03 | Signed-out user is redirected to /login                       | Permissions   | P1       | Yes (SystemHealth.test.tsx) |
| HLT-04 | Status words map to Operational / Down / Degraded             | Happy path    | P2       | Yes (SystemHealth.test.tsx) |
| HLT-05 | Loading state before health answers                           | Loading       | P2       | Yes (SystemHealth.test.tsx) |
| HLT-06 | Health endpoint fails: overall Down, services section error   | Error         | P1       | Yes (SystemHealth.test.tsx) |
| HLT-07 | One section failing does not break the others                 | Error         | P2       | Yes (SystemHealth.test.tsx) |
| HLT-08 | `services: null` / missing renders an empty Services section  | Empty state   | P1       | Yes (SystemHealth.test.tsx) |
| HLT-09 | Service or check with missing `status` does not crash         | Error         | P1       | Yes (SystemHealth.test.tsx) |
| HLT-10 | `/api/tv/cluster` with `hosts` / `on_air` null or missing     | Empty state   | P1       | Yes (SystemHealth.test.tsx) |
| HLT-11 | `/api/system/processes` with `processes` / `events` null      | Empty state   | P1       | Yes (SystemHealth.test.tsx) |
| HLT-12 | `caches: null`, missing, and `[]` show "No caches registered" | Empty state   | P1       | Yes (SystemHealth.test.tsx) |
| HLT-13 | Budgets: PascalCase, camelCase, object, and null shapes       | Happy path    | P2       | Yes (SystemHealth.test.tsx) |
| HLT-14 | Optional stats missing show a dash; budget card hidden        | Empty state   | P2       | Yes (SystemHealth.test.tsx) |
| HLT-15 | Version with empty build_sha / build_time shows a dash        | Empty state   | P3       | No                          |
| HLT-16 | TV remote line: none / off / problem / ready / failed         | Happy path    | P2       | Yes (SystemHealth.test.tsx) |
| HLT-17 | Auto-refresh every 30 s and stops on unmount                  | Happy path    | P2       | Yes (SystemHealth.test.tsx) |
| HLT-18 | Recovers from error on the next poll                          | Error         | P2       | Yes (SystemHealth.test.tsx) |
| HLT-19 | Render crash shows route error fallback; Back still works     | Error         | P1       | Yes (SystemHealth.test.tsx) |
| HLT-20 | Malformed JSON / HTML body from health                        | Error         | P2       | Yes (SystemHealth.test.tsx) |
| HLT-21 | Nav link reaches the page; Back/Forward work                  | Navigation    | P2       | No (e2e-only)               |
| HLT-22 | Arabic: RTL, Arabic strings and Arabic-Indic digits           | RTL           | P2       | Yes (SystemHealth.test.tsx) |
| HLT-23 | Phone width: cards stack, no horizontal scroll                | Responsive    | P3       | No                          |
| HLT-24 | Light and dark theme: badges and text readable                | Theme         | P3       | No                          |
| HLT-25 | Headings, badges and loading text are accessible              | Accessibility | P3       | No                          |

## Cases

### HLT-01 — Full dashboard renders when every endpoint answers

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin (`useAuth` -> `{ user: { is_admin: true } }`). Mocks:
  - `/api/system/health` -> `{ "status": "ok", "services": { "db": { "status": "up", "checks": { "ping": { "status": "pass" } } }, "pipeline": { "status": "degraded", "reason": "queue slow" } }, "disk_free_bytes": 53687091200, "queue_depth": 12, "transcribe_budget_usd_left": "4.20" }`
  - `/api/system/version` -> `{ "version": "1.5.0", "build_sha": "abcdef1234567890", "build_time": "2026-10-01T10:00:00Z", "go_version": "go1.23", "schema_revision": 124 }`
  - `/api/admin/caches` -> `{ "caches": ["thumbs", "posters"] }`
  - `/api/admin/perf/budgets` -> `{ "Version": 3, "Endpoints": [{}, {}] }`
  - `/api/tv/cluster` -> `{ "hosts": [{ "state": "online" }, { "state": "down" }], "on_air": [{ "stale": false }, { "stale": true }], "failovers_24h": 1 }`
  - `/api/tv/remote` -> `{ "integration": { "configured": true, "enabled": true }, "remote": { "ready": true } }`
  - `/api/system/processes` -> `{ "available": true, "processes": [], "events": [] }`
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** h1 "System health"; "Auto-refreshes every 30s". Overall card shows "Operational". "Disk free" shows "50.0 GB". "Queue depth" shows "12". "Transcribe budget left" shows "$4.20". Services: card "db" with "Operational" and check "ping" "Operational"; card "pipeline" with "Degraded" and reason "queue slow". TV line (`data-testid="health-tv"`) reads "TV machines online 1 of 2 · channels on air 1 · failovers (24 h) 1 · remote viewing ready". Caches list "thumbs", "posters". Budgets: "Budget version" 3, "Endpoint budgets" 2. Version card: "1.5.0", build "abcdef123456" (12 chars), formatted build time, "go1.23", schema "124". Processes card heading "Processes". No console errors.

### HLT-02 — Non-admin sees "Admins only" and no admin calls are made

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** `useAuth` -> `{ user: { username: "viewer", is_admin: false } }`.
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** ErrorState with "Admins only" and "You need administrator access to view this page.". No "System health" heading. `api.get` never called (no `/api/system/*`, `/api/admin/*`, `/api/tv/*` request). Same for `user: { is_admin: undefined }`.

### HLT-03 — Signed-out user is redirected to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** full `App` routing (or `RequireAuth` + `MemoryRouter initialEntries={["/admin/health"]}`); `GET /api/auth/me` (or the auth bootstrap call) -> 401.
- **Steps:**
  1. Open `/admin/health` signed out.
- **Expected:** Lands on `/login`; location state `from` is `/admin/health`. After signing in as admin, user returns to `/admin/health`.

### HLT-04 — Status words map to Operational / Down / Degraded

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; health `services` with one entry per status: `ok`, `UP`, `healthy`, `ready`, `pass` (expect "Operational", success tone); `down`, `fail`, `error`, `unhealthy` (expect "Down", error tone); `degraded`, `starting`, `""` (expect "Degraded", warn tone).
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** Each service card badge shows the mapped label; matching is case-insensitive (`UP` -> "Operational"). Overall `status: "unknown"` -> "Degraded".

### HLT-05 — Loading state before health answers

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `/api/system/health` returns a never-resolving promise; others resolve.
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** Under "Services", "Loading…" (`.mkt-loading`). Overall badge shows "Degraded" (status `unknown`), disk and queue show "—". Processes card shows "Loading…" until its own call resolves. Other cards render as soon as their calls resolve.

### HLT-06 — Health endpoint fails: overall Down, services section error

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `/api/system/health` rejects, run once each with: `ApiError` 500, `ApiError` 403, `ApiError` 401, `TypeError("Failed to fetch")` (network).
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** Overall badge "Down" (error tone). Under "Services": "Could not load this section.". Disk / queue show "—". Page stays usable; other sections still render from their own calls. 401 does not redirect (no handling for page-level 401; acceptable, but note it).

### HLT-07 — One section failing does not break the others

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; health OK; reject one at a time: `/api/system/version`, `/api/admin/caches`, `/api/admin/perf/budgets`, `/api/system/processes`, `/api/tv/cluster`, `/api/tv/remote`.
- **Steps:**
  1. Open `/admin/health` for each failure.
- **Expected:** The failing card alone shows "Could not load this section." (version, caches, budgets, processes). A failing `/api/tv/cluster` hides the TV line (`health-tv` absent). A failing `/api/tv/remote` keeps the TV line but without the " · remote viewing …" suffix. All other sections render normally.

### HLT-08 — `services: null` / missing renders an empty Services section

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `/api/system/health` -> (a) `{ "status": "ok", "services": null }`, (b) `{ "status": "ok" }` (key missing), (c) `{ "status": "ok", "services": {} }`. Also a service with `"checks": null`.
- **Steps:**
  1. Open `/admin/health` for each.
- **Expected:** Page renders; overall "Operational"; Services section shows an empty state (no cards) instead of a blank page. Today `Object.entries(health.services)` (SystemHealth.tsx:169) throws on `null`/missing and the whole page goes blank (currently unhandled — likely bug). `checks: null` is safe (guarded).

### HLT-09 — Service or check with missing `status` does not crash

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; health -> `{ "status": null, "services": { "db": {} , "api": { "status": "ok", "checks": { "x": {} } } } }`. Separately `{ "services": {} }` with no top-level `status`.
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** Missing status renders as "Degraded" (unknown), no crash. The top-level `status: null` / missing is safe (`health?.status ?? "unknown"`). A service or check without `status` reaches `StatusBadge`, which calls `status.toLowerCase()` (SystemHealth.tsx:268) and throws on `undefined` (currently unhandled — likely bug).

### HLT-10 — `/api/tv/cluster` with `hosts` / `on_air` null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; other calls OK. `/api/tv/cluster` -> each of: `{ "hosts": null, "on_air": [] }`, `{ "hosts": [], "on_air": null }`, `{ }` (both keys missing), `{ "hosts": [], "on_air": [], "failovers_24h": null }`.
- **Steps:**
  1. Open `/admin/health` for each.
- **Expected:** Page renders; TV line shows "machines online 0 of 0 · channels on air 0 · failovers (24 h) 0" (or is hidden), never a blank page. Today `tv.hosts.filter` / `tv.on_air.filter` (SystemHealth.tsx:152-154) throw during render on null/missing — same bug class as the Live TV page (currently unhandled — likely bug).

### HLT-11 — `/api/system/processes` with `processes` / `events` null

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `/api/system/processes` -> each of: `{ "available": true, "processes": null, "events": [] }`, `{ "available": true, "events": null, "processes": [] }`, `{ "available": true }` (both missing).
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** Processes card shows no rows and "No crashes recorded."; rest of page intact. `events: null` is safe (`doc?.events ?? []`). `processes: null`/missing throws at `doc.processes.map` (Processes.tsx:112) (currently unhandled — likely bug).

### HLT-12 — `caches: null`, missing, and `[]` show "No caches registered"

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `/api/admin/caches` -> `{ "caches": null }`, `{}`, `{ "caches": [] }`.
- **Steps:**
  1. Open `/admin/health` for each.
- **Expected:** Caches card shows "No caches registered." in all three cases (code does `r.caches ?? []`). No crash.

### HLT-13 — Budgets: PascalCase, camelCase, object, and null shapes

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `/api/admin/perf/budgets` -> (a) `{ "Version": 2, "Endpoints": [{}, {}, {}] }`, (b) `{ "version": 5, "endpoints": [] }`, (c) `{ "Endpoints": { "a": {}, "b": {} } }`, (d) `{ "Endpoints": null }`, (e) `{}`, (f) `{ "Version": "x" }`.
- **Steps:**
  1. Open `/admin/health` for each.
- **Expected:** Budget version / Endpoint budgets: (a) 2 / 3, (b) 5 / 0, (c) 0 / 2, (d) 0 / 0, (e) 0 / 0, (f) 0 / 0. No crash.

### HLT-14 — Optional stats missing show a dash; budget card hidden

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; health -> `{ "status": "ok", "services": {} }` (no disk, queue, budget). Then `{ ..., "disk_free_bytes": 0, "queue_depth": 0, "transcribe_budget_usd_left": "" }`.
- **Steps:**
  1. Open `/admin/health` for each.
- **Expected:** First: "Disk free" "—", "Queue depth" "—", no "Transcribe budget left" card. Second: "0 B", "0", and still no budget card (empty string is falsy). Also `disk_free_bytes: 1023` -> "1023 B"; `1099511627776` -> "1.0 TB".

### HLT-15 — Version with empty build_sha / build_time shows a dash

- **Priority:** P3
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** admin; `/api/system/version` -> `{ "version": "dev", "build_sha": "", "build_time": "", "go_version": "go1.23", "schema_revision": 0 }`; also `{ "build_sha": null, "build_time": "not-a-date" }`.
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** Build "—", Built "—", schema "0". For `build_time: "not-a-date"`, `formatDate` returns the raw string (no crash). `build_sha: null` shows "—".

### HLT-16 — TV remote line: none / off / problem / ready / failed

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `/api/tv/cluster` OK with empty lists. `/api/tv/remote` ->
  - `{ "integration": { "configured": false, "enabled": false } }` -> "remote viewing not set up"
  - `{ "integration": { "configured": true, "enabled": false } }` -> "remote viewing off"
  - `{ "integration": { "configured": true, "enabled": true }, "remote": { "ready": false } }` -> "remote viewing has a problem"
  - `{ "integration": { "configured": true, "enabled": true } }` -> "remote viewing ready"
  - `{}` (no `integration`) -> suffix absent (the throw inside `.then` is caught)
- **Steps:**
  1. Open `/admin/health` for each.
- **Expected:** TV line ends with " · " plus the listed phrase; for `{}` no suffix and no crash.

### HLT-17 — Auto-refresh every 30 s and stops on unmount

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; `vi.useFakeTimers({ shouldAdvanceTime: true })`; all calls OK.
- **Steps:**
  1. Open `/admin/health`; count calls to `/api/system/health`.
  2. Advance 30 s.
  3. Unmount (navigate away), advance 60 s.
- **Expected:** Step 2: each of the six page calls made once more; `/api/system/processes` is on its own 15 s timer (2 more calls per 30 s). Step 3: no further calls after unmount.

### HLT-18 — Recovers from error on the next poll

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; fake timers; `/api/system/health` rejects first, then resolves `{ "status": "ok", "services": { "db": { "status": "ok" } } }`.
- **Steps:**
  1. Open `/admin/health`; see "Could not load this section." and overall "Down".
  2. Advance 30 s.
- **Expected:** Overall "Operational"; "db" card appears; the error text is gone.

### HLT-19 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** full `App` (or `AppShell` with routes) in a `MemoryRouter` with history `["/library", "/admin/health"]`; admin. Force a render throw with `/api/tv/cluster` -> `{ "hosts": null }` (or mock `ProcessesCard` to throw).
- **Steps:**
  1. Open `/library`, then navigate to `/admin/health`.
  2. Observe the page.
  3. Press browser Back; then go Forward and click the "Machines" nav link.
- **Expected:** Step 2: a route-level error fallback (not a blank page); the app shell and nav stay visible. Step 3: `/library` renders without a reload; clicking another nav link renders that page. (Depends on the route error boundary being added in `AppShell.tsx`.)

### HLT-20 — Malformed JSON / HTML body from health

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** admin; stub `fetch` for `/api/system/health` to return 200 `Content-Type: application/json` body `{"status":` (truncated), then 200 `text/html` body `<html>…</html>`.
- **Steps:**
  1. Open `/admin/health` for each.
- **Expected:** Truncated JSON: `JSON.parse` rejects -> overall "Down", "Could not load this section.". HTML body: `request()` returns the raw string for a non-JSON content type, so `health` becomes a string, `health.services` is `undefined` and `Object.entries(undefined)` throws during render. Expected: treated as an error, "Could not load this section." (currently unhandled — likely bug: a proxy error page blanks the page).

### HLT-21 — Nav link reaches the page; Back/Forward work

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** full app, admin, desktop width.
- **Steps:**
  1. From `/library`, click sidebar "System health" (`nav.admin.health`).
  2. Press Back, then Forward.
  3. Deep link: open `/admin/health` directly in a new tab.
  4. From the queue page issue list, click the link to `/admin/health` (`IssueList.tsx`).
- **Expected:** URL `/admin/health`, h1 "System health"; Back returns to `/library`, Forward returns to the page; deep link loads the page directly; nav item is marked current (`aria-current="page"`).

### HLT-22 — Arabic: RTL, Arabic strings and Arabic-Indic digits

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (SystemHealth.test.tsx)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; data as HLT-01.
- **Steps:**
  1. Open `/admin/health`.
- **Expected:** `<html dir="rtl" lang="ar">`; h1 "حالة النظام"; queue depth and counts in Arabic-Indic digits (e.g. "١٢"); TV line uses the Arabic `health.tvLine` text with Arabic digits; service names and cache names (Latin) still read correctly; card layout mirrored (badge on the inline-end side). Mono values (version, SHA) stay left-to-right.

### HLT-23 — Phone width: cards stack, no horizontal scroll

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** browser e2e at 375x812; admin; a service with a long reason (200 chars, no spaces) and a long cache name.
- **Steps:**
  1. Open `/admin/health`; scroll the page.
  2. Open "More" in the bottom tab bar.
- **Expected:** `document.documentElement.scrollWidth <= 375`; stat cards and the health grid wrap to one column; long text wraps. The admin "System health" link is reachable from `/more`.

### HLT-24 — Light and dark theme: badges and text readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `<html data-theme="light">` then `"dark"`; data as HLT-01 with up/down/degraded badges.
- **Steps:**
  1. Open `/admin/health` in each theme.
- **Expected:** Text and badge labels meet contrast (axe `color-contrast` passes in e2e); no hardcoded white/black backgrounds in `.mkt-health*`, `.mkt-service-card*` styles.

### HLT-25 — Headings, badges and loading text are accessible

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** admin; data as HLT-01.
- **Steps:**
  1. Run `axeViolations(container)` (`src/test/axe`).
  2. Check heading order.
- **Expected:** No axe violations. One h1, h2 "Services", h3 "Recent crashes" inside Processes. The status is conveyed by text, not only colour. Suggest `role="status"`/`aria-live="polite"` on the loading text and on the overall badge so a refresh that flips status is announced (currently none — nice-to-have).
