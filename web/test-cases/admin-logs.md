# Admin logs

- **Route:** `/admin/logs` (no params; filters are component state, not in the query string)
- **Component:** `src/pages/Admin/Logs.tsx` (`AdminLogs` -> `AdminGate` -> `LogsInner`, `LogRow`); DS `Card`, `Badge`, `Button`, `Input`, `Select`, `useToast`
- **API calls:**
  - `GET /api/admin/logs/stream?level=<lv>&services=<svc>&q=<text>&limit=1000` via `api.get` (`src/lib/api.ts`) -> `{ entries: Record<string, unknown>[], count: number }`. Each entry is a raw structured log line; the page reads `ts`, `level`, `service`, `msg` and shows every other key except `version`/`env` in an expandable `<details>`. Polled every 3 s (`POLL_MS`) unless paused; any filter change refetches at once.
  - `GET /api/admin/logs/export?level=<lv>&services=<svc>` via `downloadBlob(path, "maktaba-diagnostics.tar.gz")` (`src/lib/api.ts`) -> `.tar.gz` download.
- **Access:** signed-in admin only (`RequireAuth` -> `/login`; `AdminGate` -> "Admins only"). Server 403s non-admins.
- **Existing tests:** `src/pages/Admin/Logs.test.tsx` — covers: first poll hits `/api/admin/logs/stream` and renders two entries' `msg`; Export button calls `downloadBlob` with an `/api/admin/logs/export` URL and a `.tar.gz` name. Gaps: the file comment claims the level filter is tested but no test checks it; no tests for filters, search, pause/resume, polling, auto-scroll, error, empty, null entries, export failure, permissions, long lines, Arabic, a11y.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test? |
| ------ | ----------------------------------------------------------- | ------------- | -------- | ------------------------- |
| LOG-01 | Renders entries with time, level badge, service, message    | Happy path    | P1       | Yes (Logs.cases.test.tsx) |
| LOG-02 | Non-admin sees "Admins only"; no log request                | Permissions   | P1       | Yes (Logs.cases.test.tsx) |
| LOG-03 | Signed-out user is redirected to /login                     | Permissions   | P1       | Yes (Logs.cases.test.tsx) |
| LOG-04 | Empty list shows "No log entries match the current filters" | Empty state   | P2       | Yes (Logs.cases.test.tsx) |
| LOG-05 | `entries: null` / missing renders the empty state           | Empty state   | P1       | Yes (Logs.cases.test.tsx) |
| LOG-06 | Entry that is `null` or not an object does not crash        | Error         | P1       | Yes (Logs.cases.test.tsx) |
| LOG-07 | Entries missing ts / level / service / msg                  | Empty state   | P2       | Yes (Logs.cases.test.tsx) |
| LOG-08 | Stream fails: "Could not load logs.", old lines kept        | Error         | P1       | Yes (Logs.cases.test.tsx) |
| LOG-09 | Recovers on the next poll after an error                    | Error         | P2       | Yes (Logs.cases.test.tsx) |
| LOG-10 | Level filter changes the query and refetches                | Happy path    | P1       | Yes (Logs.cases.test.tsx) |
| LOG-11 | Service filter options come from the current lines          | Happy path    | P2       | Yes (Logs.cases.test.tsx) |
| LOG-12 | Search sends trimmed `q`; whitespace-only sends none        | Validation    | P2       | Yes (Logs.cases.test.tsx) |
| LOG-13 | Polls every 3 s; Pause stops and Resume restarts            | Happy path    | P1       | Yes (Logs.cases.test.tsx) |
| LOG-14 | Auto-scroll pins to the newest line; off keeps position     | Happy path    | P2       | No (e2e-only)             |
| LOG-15 | Extra fields open in the details row                        | Happy path    | P2       | Yes (Logs.cases.test.tsx) |
| LOG-16 | Very long lines wrap; no horizontal page scroll             | Responsive    | P2       | No (e2e-only)             |
| LOG-17 | 1000 lines render and stay responsive                       | Loading       | P3       | No                        |
| LOG-18 | Export success toast and button busy state                  | Happy path    | P2       | Yes (Logs.cases.test.tsx) |
| LOG-19 | Export failure shows an error toast                         | Error         | P2       | Yes (Logs.cases.test.tsx) |
| LOG-20 | Export uses the current level and service filters           | Happy path    | P3       | No                        |
| LOG-21 | Level colour/tone mapping incl. "warning" and unknown       | Happy path    | P3       | No                        |
| LOG-22 | Render crash shows route error fallback; Back still works   | Error         | P1       | Yes (Logs.cases.test.tsx) |
| LOG-23 | Nav link, Back/Forward, deep link                           | Navigation    | P2       | Yes (Logs.cases.test.tsx) |
| LOG-24 | Arabic UI and Arabic log text                               | RTL           | P2       | Yes (Logs.cases.test.tsx) |
| LOG-25 | Phone width: toolbar wraps, list scrolls inside the card    | Responsive    | P3       | No                        |
| LOG-26 | Light and dark theme: row tints and badges readable         | Theme         | P3       | No                        |
| LOG-27 | Filters labelled; details toggle has a localised name       | Accessibility | P3       | No                        |

## Cases

### LOG-01 — Renders entries with time, level badge, service, message

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin (`useAuth` -> `{ user: { is_admin: true } }`); `/api/admin/logs/stream?...` -> `{ "entries": [ { "ts": "2026-06-10T00:00:00Z", "level": "info", "service": "api", "msg": "started" }, { "ts": "2026-06-10T00:00:01Z", "level": "error", "service": "streaming", "msg": "boom", "err": "x" } ], "count": 2 }`. Render in `I18nProvider` + `ToastProvider`.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** h1 "Logs", subtitle "Live service logs. Filter, search, and export a diagnostics bundle.". First request URL is `/api/admin/logs/stream?level=debug&limit=1000`. Two `li.mkt-logs__row` rows in order; each shows the formatted time, a level badge ("info", "error"), the service ("api", "streaming") and the message. Row 2 has class `mkt-logs__row--error`; row 1 `mkt-logs__row--accent`. Only row 2 has a details toggle.

### LOG-02 — Non-admin sees "Admins only"; no log request

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** `useAuth` -> `{ user: { username: "viewer", is_admin: false } }`.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** "Admins only" / "You need administrator access to view this page."; no toolbar; `api.get` and `downloadBlob` never called; no polling timer.

### LOG-03 — Signed-out user is redirected to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** full app routing; auth bootstrap returns 401.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** Redirected to `/login` with `state.from = "/admin/logs"`; no `/api/admin/logs/*` request.

### LOG-04 — Empty list shows "No log entries match the current filters"

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; stream -> `{ "entries": [], "count": 0 }`.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** "No log entries match the current filters." inside the viewport card; Service select has only "All services". Note: this same text also shows before the first response (there is no loading state) — expected: a loading indicator first (currently unhandled — minor).

### LOG-05 — `entries: null` / missing renders the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; stream -> (a) `{ "entries": null, "count": 0 }`, (b) `{ "count": 0 }`, (c) `null` (body `null`).
- **Steps:**
  1. Open `/admin/logs` for each.
- **Expected:** (a) and (b): empty-state text, no error. (c): no blank screen; today `r.entries` on `null` throws inside `.then`, is caught, and shows "Could not load logs." — acceptable, but an empty state would be better.

### LOG-06 — Entry that is `null` or not an object does not crash

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; stream -> `{ "entries": [ { "level": "info", "msg": "ok" }, null, "plain text line", 42 ], "count": 4 }`.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** The page stays up; the valid entry shows; bad entries are skipped or shown as raw text. Today `parseLine(null)` throws in `Object.entries(null)` (Logs.tsx:254), the whole batch is dropped and "Could not load logs." shows; a string entry is split into one attr per character (currently unhandled — likely bug: one bad line hides all logs).

### LOG-07 — Entries missing ts / level / service / msg

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; stream -> `{ "entries": [ {}, { "ts": "garbage", "level": "INFO" }, { "msg": { "nested": true } }, { "ts": 1760000000, "level": 3 } ] }`.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** Four rows, no crash. Row 1: time "—", badge "?", no service span, empty message. Row 2: time shows the raw "garbage" (`formatDate` falls back to the string), badge "INFO" with accent tone. Row 3: message "[object Object]" (expected: JSON text — minor bug). Row 4: the numeric ts is turned into the string "1760000000" before `new Date(...)`, so it shows the raw number or a wrong date, not 2025-10-09 (expected: epoch seconds read as a date — minor bug), badge "3".

### LOG-08 — Stream fails: "Could not load logs.", old lines kept

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; fake timers. First poll OK with 2 entries; second poll rejects. Repeat with `ApiError` 500, 403, 401, and `TypeError("Failed to fetch")`.
- **Steps:**
  1. Open `/admin/logs`; wait for rows.
  2. Advance 3 s.
- **Expected:** "Could not load logs." appears above the viewport; the two earlier rows stay visible; polling continues. A first-load failure shows the error text plus the empty-state text.

### LOG-09 — Recovers on the next poll after an error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; fake timers; first poll rejects, second resolves with entries.
- **Steps:**
  1. Open `/admin/logs`; see "Could not load logs.".
  2. Advance 3 s.
- **Expected:** Error text disappears; rows render.

### LOG-10 — Level filter changes the query and refetches

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; stream OK.
- **Steps:**
  1. Open `/admin/logs`.
  2. In the "Level" select (options "Debug", "Info", "Warning", "Error"; default "Debug"), choose "Error".
- **Expected:** A new request at once with `level=error` in the URL; the next polls keep `level=error`. Choosing "Warning" sends `level=warn`.

### LOG-11 — Service filter options come from the current lines

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; stream returns entries with services `"streaming"`, `"api"`, `"api"`, `""`. When the URL has `services=api`, return only `api` entries.
- **Steps:**
  1. Open `/admin/logs`; open the "Service" select.
  2. Choose "api".
  3. Open the "Service" select again.
- **Expected:** Step 1: options "All services", "api", "streaming" (sorted, deduped, empty service skipped). Step 2: request has `services=api`. Step 3: only "All services" and "api" remain, so the admin must go back to "All services" to pick another service (works as coded, but a UX trap — consider keeping known services).

### LOG-12 — Search sends trimmed `q`; whitespace-only sends none

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; stream OK.
- **Steps:**
  1. Type " timeout " in the "Search" input (placeholder "Filter by text…").
  2. Clear it and type three spaces.
  3. Type Arabic text "خطأ" and a string with `&` and `#`: `a&b#c`.
- **Expected:** Step 1: last request has `q=timeout`. Step 2: no `q` param. Step 3: `q` is URL-encoded (`q=%D8%AE%D8%B7%D8%A3`, `q=a%26b%23c`). Note: every keystroke fires a request (no debounce) — expected: debounced (currently unhandled — minor).

### LOG-13 — Polls every 3 s; Pause stops and Resume restarts

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; `vi.useFakeTimers({ shouldAdvanceTime: true })`; stream OK.
- **Steps:**
  1. Open `/admin/logs`; advance 9 s; count calls.
  2. Click "Pause"; advance 9 s.
  3. Click "Resume"; advance 3 s.
  4. Unmount; advance 9 s.
- **Expected:** Step 1: 1 + 3 calls. Step 2: button text becomes "Resume"; one immediate refresh from the effect re-run, then no more calls. Step 3: button "Pause"; polling resumes. Step 4: no calls after unmount.

### LOG-14 — Auto-scroll pins to the newest line; off keeps position

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** browser e2e (jsdom has no layout); stream returns 200 entries, then 210 on the next poll.
- **Steps:**
  1. Open `/admin/logs`; check `.mkt-logs__scroll` `scrollTop`.
  2. Untick "Auto-scroll"; scroll to the middle; wait one poll.
- **Expected:** Step 1: scrolled to the bottom (`scrollTop + clientHeight == scrollHeight`). Step 2: position stays where the user left it.

### LOG-15 — Extra fields open in the details row

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; entry `{ "ts": "...", "level": "error", "service": "api", "msg": "boom", "version": "1.5", "env": "prod", "err": "x", "req_id": "r1" }`.
- **Steps:**
  1. Click the row's details summary.
- **Expected:** A `<pre>` with `{ "err": "x", "req_id": "r1" }` pretty-printed; `version`, `env`, `ts`, `level`, `service`, `msg` are not in it. The `<details>` stays open across the next poll only if the row index is unchanged (rows use index keys; a new line at the top would move the open state to another row — note).

### LOG-16 — Very long lines wrap; no horizontal page scroll

- **Priority:** P2
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** browser e2e at 1280 and 375 widths; entries with a 5,000-char `msg` with no spaces, a long URL, and an attr value of 2,000 chars.
- **Steps:**
  1. Open `/admin/logs`; open the details of the long-attr row.
- **Expected:** Message wraps inside its column (`overflow-wrap: anywhere`); `document.documentElement.scrollWidth` equals the viewport width; the `<pre>` scrolls horizontally inside itself. Watch for: `pre` has `grid-column: 1 / -1` but it sits inside `<details>`, so it is not a grid item and stays in the last `auto` column, which may squeeze the message column (likely layout bug).

### LOG-17 — 1000 lines render and stay responsive

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** stream returns 1000 entries every poll.
- **Steps:**
  1. Open `/admin/logs`; type in Search while polling runs.
- **Expected:** All 1000 rows render; typing stays responsive (no visible lag over ~100 ms per keystroke in e2e).

### LOG-18 — Export success toast and button busy state

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; `downloadBlob` returns a promise resolved later by the test.
- **Steps:**
  1. Click "Export bundle".
  2. Resolve the promise.
- **Expected:** While pending: button text "Exporting…" and disabled (second click does nothing). After: success toast "Diagnostics bundle downloaded."; button back to "Export bundle", enabled.

### LOG-19 — Export failure shows an error toast

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** admin; `downloadBlob` rejects with (a) `new ApiError({ type: "about:blank", title: "Forbidden", status: 403 })`, (b) `new TypeError("Failed to fetch")`.
- **Steps:**
  1. Click "Export bundle".
- **Expected:** (a) error toast "Export failed: Forbidden". (b) "Export failed: Something went wrong.". Button re-enabled in both.

### LOG-20 — Export uses the current level and service filters

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** admin; level "Error", service "api", search "timeout".
- **Steps:**
  1. Click "Export bundle".
- **Expected:** `downloadBlob` path is `/api/admin/logs/export?level=error&services=api`. The search text is not sent (by design per the contract; note it in case the admin expects it).

### LOG-21 — Level colour/tone mapping incl. "warning" and unknown

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** entries with levels `"debug"`, `"info"`, `"warn"`, `"WARNING"`, `"error"`, `"ERROR"`, `"trace"`, `""`.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** Row classes: `--neutral`, `--accent`, `--warn`, `--warn`, `--error`, `--error`, `--accent`, `--accent`. Badge text is the raw level (not translated); empty shows "?".

### LOG-22 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** full app in `MemoryRouter` with history `["/admin/health", "/admin/logs"]`; admin. Force a render throw: mock `@ds/components/Badge/Badge` (or `LogRow`) to throw for this page, or mock `useI18n().formatDate` to throw.
- **Steps:**
  1. Navigate from `/admin/health` to `/admin/logs`.
  2. Press browser Back; then click the "Logs" nav link again after removing the throw, and click "Machines".
- **Expected:** A route-level error fallback (shell and nav visible), not a blank page. Back renders `/admin/health` without a reload; nav links work. Polling timer is cleared (no `/api/admin/logs/stream` calls after leaving).

### LOG-23 — Nav link, Back/Forward, deep link

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** full app, admin.
- **Steps:**
  1. Click sidebar "Logs" (`nav.admin.logs`).
  2. Change level to "Error"; press Back, then Forward.
  3. Open `/admin/logs` directly.
- **Expected:** Page loads at `/admin/logs`. Filters are not in the URL, so Back leaves the page and Forward returns with the default "Debug" level (by design; note if query-string state is wanted). Deep link loads directly.

### LOG-24 — Arabic UI and Arabic log text

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (Logs.cases.test.tsx)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; entries `{ "level": "info", "service": "api", "msg": "تم تحميل المكتبة: ١٢ ملف" }` and a mixed one `{ "msg": "فشل GET /api/videos/123 بعد 3s" }`.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** `<html dir="rtl">`; h1 "السجلات"; labels and buttons in Arabic; timestamps in the Arabic locale; Arabic messages render right-to-left and the mixed line keeps the path readable; the row's inline-start border is on the right; toolbar actions sit at the inline end (left).

### LOG-25 — Phone width: toolbar wraps, list scrolls inside the card

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** browser e2e at 375x812; 100 entries.
- **Steps:**
  1. Open `/admin/logs`.
- **Expected:** Toolbar wraps onto several lines; all controls reachable; no horizontal page scroll; the list scrolls inside `.mkt-logs__scroll` (max 60vh); timestamp column does not push the message off-screen (5-column grid at 375px: check it does not overflow).

### LOG-26 — Light and dark theme: row tints and badges readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** one row per level; `data-theme="light"` then `"dark"`.
- **Steps:**
  1. Open `/admin/logs` in each theme.
- **Expected:** Error/warn tinted rows and muted debug rows keep readable text (axe contrast passes); colours come from tokens (`--color-error`, `--color-warn`, `--color-muted`), no hardcoded values.

### LOG-27 — Filters labelled; details toggle has a localised name

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** admin; entries with attrs.
- **Steps:**
  1. Run axe; tab through the toolbar.
  2. Query the details summary by role.
- **Expected:** Level and Service selects and the Search input are reachable by label ("Level", "Service", "Search"); "Auto-scroll" checkbox labelled; tab order Level -> Service -> Search -> Pause -> Auto-scroll -> Export; focus ring visible. The details summary has `aria-label="details"` hardcoded in English (Logs.tsx:223) — expected a translated label (currently unhandled — likely bug in Arabic). Consider `aria-live="polite"` on "Could not load logs.".
