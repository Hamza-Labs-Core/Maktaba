# Queue reports

- **Route:** `/queue/reports`, with optional `?open=<file name>` (e.g. `?open=channel-plan.html`, `?open=lib-1.html`). Linked from the overview header ("Reports"), the item page ("Review page") and the preview panel ("Open the preview").
- **Component:** `src/pages/queue/Reports.tsx`; children: `src/components/queue/PlanPreviewPanel.tsx`, `useIsAdmin.ts`, `format.ts`.
- **API calls:** (`src/lib/reportsApi.ts`)
  - `GET /api/reports` → `{ reports: Report[] }` (`Report = { name, kind: "review"|"plan"|"preview"|"audit"|"other", title, library_id, size, updated_at }`) via `reportsApi.list()`.
  - The page itself: `<iframe src={reportsApi.pageUrl(name, version)}>` → `GET /api/reports/{encodeURIComponent(name)}[?v=N]`, `sandbox="allow-same-origin"`, `referrerPolicy="no-referrer"`; plus an "Open in a new tab" link to the same URL.
  - `GET /api/reports/channel-plan/preview` → `PlanPreviewStatus` `{ running, kind, phase, error?, started_at, finished_at, summary, log: string[], ready: { preview, plan } }`; polled every 2 s while `running`.
  - `POST /api/reports/channel-plan/preview` `{ kind: "preview" | "stored" }` → `{ started, kind }`; 409 when one is already running.
- **Access:** any signed-in user (`RequireAuth`); preview buttons only when `useIsAdmin()` is true (`canRun`); list and pages visible to all signed-in users.
- **Existing tests:** `src/pages/queue/Reports.test.tsx` covers: grouping by kind (plan first) and review page named after its library; sandboxed frame without scripts, `src`, `referrerpolicy`, new-tab link; `?open=` opens and encodes; missing report message; filter; empty folder; retry when the list fails; start preview with progress and reload of list and frame on finish; 409 busy message; stored plan on request; failed preview reason and dead pipeline; pipeline not answering. `src/pages/queue/a11y.test.tsx` "reports page has no violations". Gaps: loading; `null` / missing `reports`; `null` preview fields; malformed responses; error boundary/Back; signed out; non-admin; filter with no match; history behaviour of picking a report; path-like names; phone; RTL; theme.

## Summary

| ID     | Title                                                      | Category      | Priority | Covered by existing test?                              |
| ------ | ---------------------------------------------------------- | ------------- | -------- | ------------------------------------------------------ |
| QRP-01 | Lists reports grouped by kind                              | Happy path    | P1       | Yes (`Reports.test.tsx`)                               |
| QRP-02 | Opens a report in a sandboxed frame                        | Happy path    | P1       | Yes (`Reports.test.tsx`)                               |
| QRP-03 | Deep link `?open=` opens that report                       | Navigation    | P2       | Yes (`Reports.test.tsx`)                               |
| QRP-04 | Deep link to a missing report says so                      | Navigation    | P2       | Yes (`Reports.test.tsx`)                               |
| QRP-05 | Path-like names in `?open=` stay inside `/api/reports/`    | Permissions   | P1       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-06 | Shows Loading while the list is pending                    | Loading       | P2       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-07 | List fails, Try again recovers                             | Error         | P1       | Yes (`Reports.test.tsx`)                               |
| QRP-08 | 401 / 403 / malformed list response                        | Error         | P2       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-09 | `reports` `null` or missing                                | Empty state   | P1       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-10 | Preview status list fields `null` or missing               | Empty state   | P1       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-11 | Empty folder and filter with no match                      | Empty state   | P2       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-12 | Render crash shows route error fallback; Back works        | Error         | P1       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-13 | Signed-out user is sent to `/login`                        | Permissions   | P1       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-14 | Non-admin sees reports but no preview buttons              | Permissions   | P1       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-15 | Start a preview, follow progress, reload on finish         | Happy path    | P1       | Yes (`Reports.test.tsx`)                               |
| QRP-16 | Second start while running answers "already being planned" | Validation    | P2       | Yes (`Reports.test.tsx`)                               |
| QRP-17 | Preview failed / pipeline offline                          | Error         | P2       | Yes (`Reports.test.tsx`)                               |
| QRP-18 | Picking a report updates the URL; Back / Forward / links   | Navigation    | P2       | Yes (`Reports.cases.test.tsx`)                         |
| QRP-19 | Report list changes while one is open                      | Happy path    | P3       | No                                                     |
| QRP-20 | Phone width 375 px                                         | Responsive    | P3       | No (e2e-only)                                          |
| QRP-21 | Arabic: RTL, Arabic report titles                          | RTL           | P3       | Partial (`Reports.test.tsx`: Arabic title button only) |
| QRP-22 | Light and dark theme                                       | Theme         | P3       | No                                                     |
| QRP-23 | Keyboard and accessibility basics                          | Accessibility | P3       | Partial (`a11y.test.tsx`: axe only)                    |

## Cases

### QRP-01 — Lists reports grouped by kind

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Reports.test.tsx` "groups the reports by kind, plan first, and names a review page after its library"
- **Preconditions / data:** `GET /api/reports` → `{ reports: [plan "channel-plan.html", audit "channel-audit.html", review "<lib>.html" title "قناة الاختبار", preview "channel-plan-preview.html"] }`; preview status idle (`running: false`, `log: []`, `ready: { preview: true, plan: true }`).
- **Steps:**
  1. Open `/queue/reports`.
- **Expected:** `h1` "Reports"; link "Back to the queues" → `/queue`; nav "Report pages" with headings in order "Channel plan", "Plan preview", "Channel audit", "Library review pages (1)"; each item a button with title and age; right side "Pick a report to read it here.".

### QRP-02 — Opens a report in a sandboxed frame

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Reports.test.tsx` "opens a report in a frame that runs no script…"
- **Preconditions / data:** as QRP-01.
- **Steps:**
  1. Click "Channel audit".
- **Expected:** URL becomes `/queue/reports?open=channel-audit.html`; the button has `aria-current="page"`; `iframe` titled "Channel audit" with `sandbox="allow-same-origin"` (no `allow-scripts`), `referrerpolicy="no-referrer"`, `src="/api/reports/channel-audit.html"`; "Open in a new tab" link with `target="_blank"` and `rel="noopener noreferrer"`; "Updated <time>".

### QRP-03 — Deep link `?open=` opens that report

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes — `Reports.test.tsx` "opens the report named in the address, encoding the name"
- **Preconditions / data:** as QRP-01.
- **Steps:**
  1. Open `/queue/reports?open=<lib>.html` directly.
- **Expected:** frame titled "قناة الاختبار" with `src="/api/reports/<lib>.html"`.

### QRP-04 — Deep link to a missing report says so

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes — `Reports.test.tsx` "says so when the report in the address is not there"
- **Preconditions / data:** as QRP-01.
- **Steps:**
  1. Open `/queue/reports?open=gone.html`.
- **Expected:** "The report gone.html does not exist (any more)."; no iframe. While the list is still loading it says "Pick a report to read it here." (not the missing text).

### QRP-05 — Path-like names in `?open=` stay inside `/api/reports/`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** `/api/reports` includes a report named `../secret.html` (and a second run where it is not listed); also `?open=javascript:alert(1)`.
- **Steps:**
  1. Open `/queue/reports?open=..%2Fsecret.html`; then `?open=javascript:alert(1)`.
- **Expected:** a listed name gives `src="/api/reports/..%2Fsecret.html"` (encoded, never `/api/secret.html`); an unlisted name shows the missing message and no frame; `javascript:` never becomes a frame `src` or link `href`; the shown name is plain text (no HTML injection).

### QRP-06 — Shows Loading while the list is pending

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** `/api/reports` never resolves; preview status idle.
- **Steps:**
  1. Open `/queue/reports`.
- **Expected:** "Loading…" inside the list nav; filter box present; no "No reports yet" text; preview panel already shows.

### QRP-07 — List fails, Try again recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes — `Reports.test.tsx` "offers a retry when the list cannot be loaded"
- **Preconditions / data:** `/api/reports` rejects (500 and network); then resolves.
- **Steps:**
  1. Open the page; click "Try again".
- **Expected:** `role="alert"` "Could not load the reports." with "Try again"; no "Loading…" in the list; after retry the list renders and the alert is gone.

### QRP-08 — 401 / 403 / malformed list response

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** `/api/reports` → (a) 401; (b) 403 `{ "title": "Forbidden" }`; (c) 200 `application/json` body `{"reports": [` (truncated); (d) 200 `text/html` body.
- **Steps:**
  1. Open the page.
- **Expected:** (a)–(c) the "Could not load the reports." alert (401 ideally sends to `/login`). (d) must not crash: treat as a load error. (currently unhandled — likely bug: a `text/html` 200 resolves to a string, `r.reports` is `undefined` and `reports.length` throws, see QRP-09.)

### QRP-09 — `reports` `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** `/api/reports` → (a) `{ "reports": null }`; (b) `{}`; also `?open=channel-plan.html` with each.
- **Steps:**
  1. Open the page.
- **Expected:** "No reports yet. Review pages appear once a library has been processed."; with `?open=` the missing message; no blank page. (currently unhandled — likely bug: (a) leaves `reports` `null` so the list shows "Loading…" forever; (b) sets `undefined`, passes `reports !== null`, and `reports.length` in `Reports.tsx` throws.)

### QRP-10 — Preview status list fields `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** `GET /api/reports/channel-plan/preview` → idle status with, one at a time: `log: null`, `log` missing, `ready: null`, `ready` missing, `summary: null`, `summary.added: null`, `summary.changes` missing.
- **Steps:**
  1. Open the page (admin).
- **Expected:** the preview panel renders its title and buttons; no "Progress" log details; no "Open the preview" link without `ready`; result line only when `summary.channels` is a number. (currently unhandled — likely bug: `status?.log[status.log.length - 1]` and `status?.ready.preview` in `PlanPreviewPanel.tsx` throw on `null`/missing.)

### QRP-11 — Empty folder and filter with no match

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** (a) `{ reports: [] }`; (b) the QRP-01 list.
- **Steps:**
  1. (a) open the page. (b) type "zzz" in "Filter reports".
- **Expected:** (a) "No reports yet…". (b) no groups shown; there should be a "no report matches" line (currently the list is just empty — minor gap). Clearing the filter brings all groups back. Filter matches title or file name, case-insensitive, Arabic too.

### QRP-12 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** app routes; start at `/queue`; `/api/reports` → `{}` (QRP-09) or `PlanPreviewPanel` mocked to throw.
- **Steps:**
  1. Click "Reports" in the overview header.
  2. See the fallback; press Back; click "Processing" in the nav.
- **Expected:** route error fallback inside the shell; Back renders `/queue` without a refresh; nav works.

### QRP-13 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** no user.
- **Steps:**
  1. Open `/queue/reports?open=channel-plan.html`.
- **Expected:** redirect to `/login` with `state.from = "/queue/reports"`; no iframe request.

### QRP-14 — Non-admin sees reports but no preview buttons

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** `AuthProvider` user `is_admin: false`; list as QRP-01; status idle with `ready.preview: true`.
- **Steps:**
  1. Open the page; open a report.
- **Expected:** no "Preview channels" / "Show the stored plan" buttons; intro text and "Open the preview" link still show; reports open normally. If a non-admin forces `POST /api/reports/channel-plan/preview`, the server answers 403.

### QRP-15 — Start a preview, follow progress, reload on finish

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Reports.test.tsx` "starts a preview, shows its progress and reloads the list and frame when it ends", "renders the stored plan on request"
- **Preconditions / data:** admin; status sequence: idle → `running: true, phase: "planning lanes"` → `running: false, summary: { channels: 12, lanes: 30, changes: { added: 2, removed: 1, changed: 3, reordered: 0 }, added: [...] }`; preview report open.
- **Steps:**
  1. Click "Preview channels"; advance 2 s timers.
- **Expected:** `POST` with `{ kind: "preview" }`; both buttons disabled while running; `role="status"` "Planning: planning lanes"; on finish "12 channels and 30 slot queues planned." with added/removed/changed lines; `/api/reports` refetched and the frame `src` gains `?v=1`; polling stops.

### QRP-16 — Second start while running answers "already being planned"

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes — `Reports.test.tsx` "a second click while one runs says so (409)"
- **Preconditions / data:** `POST` → 409 `{ "title": "Conflict", "status": 409 }`.
- **Steps:**
  1. Click "Preview channels".
- **Expected:** `role="alert"` "A preview is already being planned."; other errors show their message.

### QRP-17 — Preview failed / pipeline offline

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes — `Reports.test.tsx` "shows a failed preview's reason and a dead pipeline", "tells when the pipeline does not answer"
- **Preconditions / data:** status `running: false, error: "lane rules invalid"`; separately status request rejects.
- **Steps:**
  1. Open the page.
- **Expected:** `role="alert"` "The preview failed: lane rules invalid"; offline: `role="alert"` "The pipeline does not answer, so no preview can be made."; the report list still works.

### QRP-18 — Picking a report updates the URL; Back / Forward / links

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Reports.cases.test.tsx`)
- **Preconditions / data:** app routes; start at `/queue`, click "Reports".
- **Steps:**
  1. Pick "Channel plan", then "Channel audit".
  2. Press Back.
  3. Click "Open the preview" (a `Link`), then Back.
  4. From an item page click "Review page", then Back.
- **Expected:** picking uses `replace`, so step 2 goes back to `/queue` (not to the plan) — confirm this is the intended behaviour; step 3 pushes `?open=channel-plan-preview.html` and Back returns to the previous report; step 4 opens the library's review page and Back returns to the item page. No refresh needed anywhere.

### QRP-19 — Report list changes while one is open

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `?open=lib-1.html` open; a preview finishes and the reloaded list no longer contains `lib-1.html`.
- **Steps:**
  1. Finish a preview (QRP-15).
- **Expected:** the frame is replaced by "The report lib-1.html does not exist (any more)."; no stale frame.

### QRP-20 — Phone width 375 px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e 375×812; list as QRP-01; a report open.
- **Steps:**
  1. Open `/queue/reports?open=channel-plan.html`.
- **Expected:** list and frame stack; frame fits the width (no horizontal page scroll); "Open in a new tab" reachable; preview buttons wrap.

### QRP-21 — Arabic: RTL, Arabic report titles

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** Partial — `Reports.test.tsx` finds the Arabic title button
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`.
- **Steps:**
  1. Open the page; type an Arabic filter.
- **Expected:** `dir="rtl"`; `h1` "التقارير"; group headings in Arabic; Arabic titles `dir="auto"` and isolated; filter matches Arabic text; list/frame order mirrored.

### QRP-22 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light / dark.
- **Steps:**
  1. Open a report in both themes.
- **Expected:** list, current item and alerts readable; no inline colour styles; the frame area has a token background (the report page itself keeps its own colours).

### QRP-23 — Keyboard and accessibility basics

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** Partial — `a11y.test.tsx` "reports page has no violations"
- **Preconditions / data:** list as QRP-01.
- **Steps:**
  1. Tab to the filter, then through the report buttons; press Enter on one.
- **Expected:** filter named "Filter reports"; buttons reachable and opened by Enter/Space; current one `aria-current="page"`; iframe has a `title`; preview status `role="status"`, errors `role="alert"`; focus visible.
