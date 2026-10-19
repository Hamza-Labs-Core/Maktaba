# Queue overview (Processing queues)

- **Route:** `/queue`, with optional query string `?host=<node id | home>` and `?name=<machine name>` (the Machines page's "View in queues" link).
- **Component:** `src/pages/queue/QueueOverview.tsx`; children: `src/components/queue/QueueHeader.tsx`, `HostJobs` in `hostFilter.tsx` (+ `RunningJobs.tsx`), `PulseBanner.tsx`, `RunPanel.tsx`, `HealthStrip.tsx` (+ `StatTile.tsx`, `Sparkline.tsx`), `WorkersStrip.tsx`, `InsightsPanel.tsx`, `FlowLanes.tsx` (+ `QueueCard.tsx`, `FlowArrow.tsx`, `RingGauge.tsx`), `IssueList.tsx`, `ActivityLog.tsx`, `LibrariesTable.tsx`, `ExplainPanel.tsx`, `StylePanel.tsx`, `RetriggerDialog.tsx`; hooks `useAction.ts`, `useIsAdmin.ts`, `useThrottledReload.ts`.
- **API calls:** (all in `src/lib/queueApi.ts`, through `api` in `src/lib/api.ts`)
  - `GET /api/queues` → `QueuesSnapshot` via `queueApi.snapshot()` (in `useQueuesSnapshot`, `src/lib/useQueueStream.ts`); polled every 5 s only after 10 s of stream silence.
  - `GET /api/issues` → `{ groups: FailureGroup[] }` via `queueApi.issues()`.
  - `GET /api/queues/libraries` → `{ items: LibraryQueueRow[] }` via `queueApi.libraries()`.
  - `GET /api/extensions` → `{ extensions: Extension[] }` via `queueApi.extensions()`.
  - `GET /api/queues/health?hours=4` → `{ series: HealthSeries[] }` via `queueApi.health(4)`, every 60 s.
  - `GET /api/workers` → `{ workers: WorkerHost[] }` via `queueApi.workers()` (in `useWorkers`, every 30 s while the socket is not open).
  - `GET /api/queues/pulse` → `Pulse` (PulseBanner, every 5 s); `GET /api/queues/insights` → `Insights` (InsightsPanel, every 60 s).
  - `GET /api/activity?level=info&limit=100[&stage=&cursor=]` → `{ items: LogLine[], next: string | null }` (ActivityLog).
  - `GET /api/queues/running?host=<host>` → `{ host, jobs: JobRef[] }` (HostJobs, only with `?host=`, every 10 s).
  - `GET /api/explain?q=` (ExplainPanel), `GET /api/seasons/styles` (StylePanel), `GET /api/items/library/{id}/history` (dialog seasons).
  - Mutations: `POST /api/queues/pause-all`, `POST /api/queues/resume-all`, `POST /api/queues/{stage}/retry {cause}`, `POST /api/queues/{stage}/apply {all:true}`, `POST /api/queue/retrigger {items, mode, priority:50}`, `PATCH /api/queues/{stage}`, `PUT /api/extensions/{name}`, `PUT /api/seasons/{id}/style`, `PUT /api/series/{id}/style`.
  - Live: cluster WebSocket `/api/cluster/ws` (`src/lib/clusterSocket.ts`), topics `queues`, `jobs`, `workers`. Fallback while the socket status is `reconnecting` or `unavailable`: SSE via `subscribe("queues" | "jobs")` in `src/lib/ws.ts` (`queues.snapshot`, `job.*` events).
- **Access:** any signed-in user (`RequireAuth` in `src/lib/auth.tsx`); no `AdminGate`. Admin-only controls are switched off with `useIsAdmin()` only in `RunPanel` (`canRun`) and `StylePanel` (`canEdit`); the server enforces admin on mutations.
- **Test harness:** `routeGet` / `renderAt` in `src/test/queueHarness.tsx`; fixtures in `src/test/fixtures/queues.ts` (`snapshotFixture`, `failureGroupsFixture`, `librariesFixture`, `workersFixture`, `extensionsFixture`, `healthSeriesFixture`, `logFixture`, `events.*`); socket via `FakeSocket` (`src/test/fakeSocket.ts`: `open()`, `recv()`, `drop()`) and `clusterSocket.reset()`; SSE via `vi.mock("../../lib/ws")`.
- **Existing tests:**
  - `src/pages/queue/QueueOverview.test.tsx` covers: full render from fixtures; extensions lane and badge; special queues as own cards; no-worker waiting reason; live `queues.snapshot` (paused_all flips the button); Pause all posts; retry issue group by cause; live `job.log` batching and "Problems only"; Queue work dialog modes and queue limits; bulk re-queue from the table; library name filter; no inline colour styles in light/dark; Arabic waiting reason; polling only after 10 s of silence and stop on snapshot; control-centre panels present; Start a run.
  - `src/pages/queue/QueueOverview.socket.test.tsx` covers: subscribes to queues/jobs/workers and renders the hub snapshot; live jobs with machine; jobs running at load (snapshot `running`); workers from the socket; SSE fallback when the socket drops; SSE when there is no WebSocket.
  - `src/pages/queue/hostFilter.test.tsx` covers: host helpers; `?host=` panel with progress; title from `?name=` until workers know it; no panel without `?host=`.
  - `src/pages/queue/a11y.test.tsx` "queue overview has no violations" (axe).
  - Component tests: `RunPanel.test.tsx` (non-admin cannot start), `parityPanels.test.tsx` (Explain, Style, Pulse, Insights), `QueueCard.test.tsx`, `StatTile.test.tsx`, `queueParts.test.tsx`.
  - Gaps: snapshot load error and retry; loading state; malformed responses; every `null` / missing list field; error boundary and Back; signed-out redirect; non-admin mutations returning 403; failed pause/retry/apply; issue `apply` and link actions; socket reconnect (back to `open`); host filter edge cases; Load older; phone width; keyboard in the dialog.

## Summary

| ID     | Title                                                        | Category      | Priority | Covered by existing test?                                                                          |
| ------ | ------------------------------------------------------------ | ------------- | -------- | -------------------------------------------------------------------------------------------------- |
| QOV-01 | Renders every section from a full snapshot                   | Happy path    | P1       | Yes (`QueueOverview.test.tsx`)                                                                     |
| QOV-02 | Shows Loading while the first snapshot is pending            | Loading       | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-03 | Snapshot load fails, then Try again recovers                 | Error         | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-04 | 401 / 403 on the snapshot shows the error, not a blank page  | Error         | P2       | Yes (`QueueOverview.cases.test.tsx`; 401 does not redirect to /login (not checked))                |
| QOV-05 | Malformed or non-JSON snapshot does not crash the page       | Error         | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-06 | `/api/queues` list fields `null` or missing                  | Empty state   | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-07 | `/api/issues` `groups` / `sample_subjects` `null` or missing | Empty state   | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-08 | `/api/queues/libraries` `items` / `strip` `null` or missing  | Empty state   | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-09 | `/api/workers` `workers` `null` or missing                   | Empty state   | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-10 | Side panels' list fields `null` or missing                   | Empty state   | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-11 | Hub / SSE snapshot with `null` lists does not crash          | Empty state   | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-12 | Empty but valid snapshot shows empty states                  | Empty state   | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-13 | Render crash shows route error fallback; Back works          | Error         | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-14 | Signed-out user is sent to `/login`                          | Permissions   | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-15 | Non-admin: run and style controls off; 403 on other actions  | Permissions   | P1       | Yes (`QueueOverview.cases.test.tsx`; it.fails: Pause all / Queue work still offered to non-admins) |
| QOV-16 | Pause all / Resume all toggles and refreshes                 | Happy path    | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-17 | Failed mutation shows the API message in an alert            | Error         | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-18 | Issue actions: Retry, Apply, Open settings, Show workers     | Happy path    | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-19 | Queue work dialog: validation, close, keyboard               | Validation    | P2       | Partial (`QueueOverview.cases.test.tsx`: no focus-return / focus-trap / Seasons error check)       |
| QOV-20 | Live updates over the cluster socket                         | Happy path    | P1       | Yes (`QueueOverview.socket.test.tsx`)                                                              |
| QOV-21 | Socket drops: SSE takes over, then polling with a note       | Error         | P1       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-22 | Socket reconnects: back on the hub, no SSE, no duplicates    | Happy path    | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-23 | `?host=` filter: panel, unknown host, failures, way back     | Navigation    | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-24 | Links: cards, Reports, Extensions, libraries; Back/Forward   | Navigation    | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-25 | Libraries table: filter, select all, no match                | Happy path    | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-26 | Activity log: filters, Load older, failure                   | Error         | P2       | Yes (`QueueOverview.cases.test.tsx`)                                                               |
| QOV-27 | Phone width 375 px: no horizontal scroll                     | Responsive    | P3       | No (e2e-only)                                                                                      |
| QOV-28 | Arabic: RTL, Arabic strings, mirrored layout                 | RTL           | P3       | Partial (`QueueOverview.test.tsx`: one string)                                                     |
| QOV-29 | Light and dark theme use tokens only                         | Theme         | P3       | Yes (`QueueOverview.test.tsx`)                                                                     |
| QOV-30 | Keyboard and accessibility basics                            | Accessibility | P3       | Partial (`a11y.test.tsx`: axe only)                                                                |

## Cases

### QOV-01 — Renders every section from a full snapshot

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `QueueOverview.test.tsx` "renders health, workers, lanes, cards, issues, log and libraries from fixtures"
- **Preconditions / data:** signed in; `GET /api/queues` → `snapshotFixture`; `/api/issues` → `{ groups: failureGroupsFixture }`; `/api/queues/libraries` → `{ items: librariesFixture }`; `/api/workers` → `{ workers: workersFixture }`; `/api/extensions` → `{ extensions: extensionsFixture }`; `/api/queues/health` → `{ series: healthSeriesFixture }`; `/api/activity` → `{ items: logFixture, next: null }`; pulse and insights as in the test's `setup()`.
- **Steps:**
  1. Open `/queue`.
- **Expected:** `h1` "Processing queues"; a status pill (one of "Paused", "Needs attention", "Working", "All caught up"); buttons "Pause all" and "Queue work…"; links "Reports" and "Extensions"; the health tiles group "…" with NAS, CPU, Network, Backlog; "Workers" section; one `article` card per visible stage with a link to `/queue/<stage>`; "Needs attention" list; "Activity" log; "Libraries" table. No console errors.

### QOV-02 — Shows Loading while the first snapshot is pending

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `GET /api/queues` returns a promise that never resolves; other routes as QOV-01.
- **Steps:**
  1. Open `/queue`.
- **Expected:** only `role="status"` "Loading…" inside the page section; no header buttons yet; no alert. When the promise then resolves with `snapshotFixture`, the full page replaces it.

### QOV-03 — Snapshot load fails, then Try again recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** first `GET /api/queues` rejects (repeat for: `ApiError` 500 `{ "title": "Internal Server Error" }`, and `fetch` rejecting with `TypeError("Failed to fetch")`); SSE silent.
- **Steps:**
  1. Open `/queue`.
  2. Make `/api/queues` return `snapshotFixture`.
  3. Click "Try again".
- **Expected:** step 1 shows `role="alert"` "The queues could not be loaded." with a "Try again" button (not a blank page, not "Loading…" forever). Step 3 calls `/api/queues` again and renders the page as in QOV-01; the alert is gone.

### QOV-04 — 401 / 403 on the snapshot shows the error, not a blank page

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`; 401 does not redirect to /login (not checked))
- **Preconditions / data:** signed in, but `GET /api/queues` → 401 `application/problem+json` `{ "title": "Unauthorized", "status": 401 }` (session expired); second run with 403 `{ "title": "Forbidden", "status": 403 }`.
- **Steps:**
  1. Open `/queue`.
- **Expected:** the "The queues could not be loaded." alert with "Try again". For 401 the app should send the user to `/login` or say the session ended (currently unhandled — likely bug: a 401 looks like a generic load failure and nothing redirects).

### QOV-05 — Malformed or non-JSON snapshot does not crash the page

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** mock `fetch` (not `api.get`) so that `GET /api/queues` returns (a) status 200, `Content-Type: application/json`, body `{"queues": [` (truncated); (b) status 200, `Content-Type: text/html`, body `<!doctype html><html>…</html>` (SPA fallback from a proxy).
- **Steps:**
  1. Open `/queue` for each response.
- **Expected:** (a) JSON parse error → the "The queues could not be loaded." alert. (b) the page must not try to draw a string as a snapshot: expect the same alert. (currently unhandled — likely bug: `request()` in `src/lib/api.ts` returns the HTML text for a non-JSON content type, `useQueuesSnapshot` stores it, and `snapshot.queues.reduce` in `QueueOverview.tsx` throws.)

### QOV-06 — `/api/queues` list fields `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `GET /api/queues` → `snapshotFixture` with, one at a time, each field set to `null` and then deleted: `stages`, `queues`, `pools`, `flows`, `health.spark.nas_open_ms`, `health.spark.cpu_pct`, `health.spark.backlog_ready`; and nested: `queues[0].running_jobs`, `queues[0].served_by`, `stages[0].edges`, `stages[0].subject_kinds`, `pools[0].stages`. Other routes as QOV-01.
- **Steps:**
  1. Open `/queue`.
- **Expected:** the page renders (header, health tiles, sections) and treats each list as empty: no lanes / cards when `stages` is empty, cards show 0 of 0 when `queues` is empty, no pool chip without `pools`, no flow arrows' rate without `flows`, a flat sparkline without `spark` data. No thrown error, no blank page. (currently unhandled — likely bug: `snapshot.queues.reduce`, `stages.filter`, `pools.find`, `flows.find` and `Sparkline` `values.filter` all throw on `null`; `served_by` alone is guarded with `?? []`.)

### QOV-07 — `/api/issues` `groups` / `sample_subjects` `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `GET /api/issues` → (a) `{ "groups": null }`; (b) `{}`; (c) `{ "groups": [ { ...failureGroupsFixture[0], "sample_subjects": null } ] }`.
- **Steps:**
  1. Open `/queue`.
- **Expected:** (a)/(b) "Needs attention" shows "Nothing needs attention."; the status pill uses `snapshot.issues_count`. (c) the group renders without the subjects line. (currently unhandled — likely bug: `issues.length` in `QueueOverview.tsx` and `groups.length` / `g.sample_subjects.length` in `IssueList.tsx` throw.)

### QOV-08 — `/api/queues/libraries` `items` / `strip` `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `GET /api/queues/libraries` → (a) `{ "items": null }`; (b) `{}`; (c) `{ "items": [ { ...librariesFixture[0], "strip": null } ] }`.
- **Steps:**
  1. Open `/queue`.
  2. Open "Queue work…".
- **Expected:** (a)/(b) the Libraries table shows "No libraries match."; Run panel, Explain library select, Style library select and the dialog list show no libraries; nothing throws. (c) the row renders with an empty queues strip. (currently unhandled — likely bug: `rows.filter` in `LibrariesTable.tsx`, `libraries.filter` in `RetriggerDialog.tsx` / `RunPanel.tsx`, `libraries.map` in `ExplainPanel.tsx`, and `r.strip.map` throw.)

### QOV-09 — `/api/workers` `workers` `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** no WebSocket (`vi.stubGlobal("WebSocket", undefined)`); `GET /api/workers` → (a) `{ "workers": null }`; (b) `{}`; (c) `{ "workers": [] }`. Also hub message `{ type: "workers", data: { workers: null } }` with the socket open.
- **Steps:**
  1. Open `/queue` and `/queue?host=home`.
- **Expected:** (a) no Workers section, page fine. (b) same as (a). (c) "Workers" with "No workers have reported in.". Hub `workers: null` is ignored. (currently unhandled — likely bug for (b): `setWorkers(undefined)` passes the `workers === null` check in `WorkersStrip.tsx` and `workers.length` throws.)

### QOV-10 — Side panels' list fields `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** one at a time, `null` then key missing:
  - `GET /api/queues/health` → `series: null`; `series: [{ "metric": "cpu.pct", "points": null }]`.
  - `GET /api/queues/pulse` → `stuck: null` (state `"stalled"`).
  - `GET /api/queues/insights` → `genres: null`; `styles: null`; `totals: null`.
  - `GET /api/activity` → `items: null`.
  - `GET /api/extensions` → `extensions: null`.
  - `GET /api/queues/running?host=home` (with `?host=home`) → `jobs: null`.
  - `GET /api/explain?q=lecture` → `videos: null`, `remote: null` (after typing "lecture" and submitting the Explain search).
  - `GET /api/seasons/styles` → `items: null` (after clicking the Style panel's find button).
- **Steps:**
  1. Open `/queue` (or `/queue?host=home`) and trigger each panel.
- **Expected:** each panel shows its empty state or hides itself: health tiles keep the snapshot spark; pulse text without a stuck list; insights without the bars; Activity "No activity yet." (not "Loading…" forever); no extension badges; host panel "Nothing is running on this home right now." (not "Loading…" forever); Explain "nothing matches" text; Style "none" text. Nothing throws. (currently unhandled — likely bugs: `s.points.length` in `HealthStrip.tsx`, `pulse.stuck.length` in `PulseBanner.tsx`, `data.genres.map` / `data.styles[s]` in `InsightsPanel.tsx`, `result.remote.length` in `ExplainPanel.tsx` throw; Activity and host panel stay on "Loading…".)

### QOV-11 — Hub / SSE snapshot with `null` lists does not crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** page loaded with `snapshotFixture`. Then (a) socket open, `FakeSocket.last.recv({ type: "queues", data: { ...snapshotFixture, stages: null, pools: null, flows: null } })`; (b) no socket, SSE `hub.emit("queues", { type: "queues.snapshot", at: "x", payload: { ...snapshotFixture, queues: null } })`; (c) a hub `snapshot` on `jobs` with `{ jobs: null, running: null }`.
- **Steps:**
  1. Send each message.
- **Expected:** the page keeps rendering (empty lanes or the previous data) and does not go blank. (currently unhandled — likely bug: `isSnapshot` only checks that `queues` is an array, so (a) is accepted and `stages.filter` throws; the SSE path in (b) is not validated at all.)

### QOV-12 — Empty but valid snapshot shows empty states

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `GET /api/queues` → `{ ...snapshotFixture, stages: [], queues: [], pools: [], flows: [], issues_count: 0, paused_all: false }`; `/api/issues` → `{ groups: [] }`; `/api/queues/libraries` → `{ items: [] }`; `/api/workers` → `{ workers: [] }`; `/api/activity` → `{ items: [], next: null }`.
- **Steps:**
  1. Open `/queue`.
- **Expected:** pill "All caught up"; no lane sections; "Nothing needs attention."; "No activity yet."; "No libraries match."; "No workers have reported in."; Queue work dialog opens with no queues to tick.

### QOV-13 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** full app routes (`App.tsx` routes inside `MemoryRouter` or a browser e2e), signed in. Start at `/library`, then navigate to `/queue`. Force a render throw, e.g. `GET /api/issues` → `{ "groups": null }` (QOV-07) or `vi.mock` `FlowLanes` to throw.
- **Steps:**
  1. Open `/library`, click the "Processing" nav link.
  2. See the fallback.
  3. Press browser Back; then go Forward and click another nav link (e.g. "Series").
- **Expected:** step 2 shows the route-level error fallback inside the app shell (nav still visible), not a blank screen. Step 3: Back shows `/library` content without a refresh; clicking "Series" renders `/series`. The fallback resets on navigation.

### QOV-14 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `AuthProvider` with no user (`GET /api/auth/me` → 401).
- **Steps:**
  1. Open `/queue` and `/queue?host=home` directly.
- **Expected:** redirect to `/login` with `state.from = "/queue"`; no `/api/queues` call; no socket opened.

### QOV-15 — Non-admin: run and style controls off; 403 on other actions

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`; it.fails: Pause all / Queue work still offered to non-admins)
- **Preconditions / data:** signed in as a non-admin (`user.is_admin: false`) via `AuthProvider`; routes as QOV-01; every `POST` → 403 `{ "title": "Forbidden", "status": 403, "detail": "admin only" }`.
- **Steps:**
  1. Open `/queue`.
  2. Check the Run panel start button and the Style panel pin buttons.
  3. Click "Pause all"; click "Retry N" on an issue; open "Queue work…", tick a library, submit.
- **Expected:** step 2: start and pin controls are disabled or absent. Step 3: each failed call shows `role="alert"` "Forbidden: admin only" (page banner or dialog error); buttons leave the busy state; nothing changes. Workers strip shows no "Manage machines" link. Better: Pause all, Retry, Apply and Queue work should be hidden or disabled for non-admins (currently unhandled — likely bug: they are always offered).

### QOV-16 — Pause all / Resume all toggles and refreshes

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** admin; snapshot with `paused_all: false`, then a second `GET /api/queues` returning `paused_all: true`; `POST` resolves `undefined` (204).
- **Steps:**
  1. Click "Pause all".
  2. Click "Resume all".
- **Expected:** step 1 posts `/api/queues/pause-all` once, the button is disabled while busy, then `/api/queues` is fetched again and the button reads "Resume all", pill "Paused". Step 2 posts `/api/queues/resume-all`.

### QOV-17 — Failed mutation shows the API message in an alert

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** admin; `POST /api/queues/pause-all` → 500 `{ "title": "Internal Server Error", "status": 500, "detail": "db down" }`; separately `POST /api/queues/{stage}/retry` rejects with `TypeError("Failed to fetch")`.
- **Steps:**
  1. Click "Pause all".
  2. Click an issue's "Retry N".
- **Expected:** `role="alert"` banner under the header with "Internal Server Error: db down", then "Failed to fetch"; the buttons are enabled again; the page is still usable. The issue list is not reloaded after a failed retry.

### QOV-18 — Issue actions: Retry, Apply, Open settings, Show workers

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `/api/issues` → groups with `action` `"retry"` (cause `nas_timeout`, stage `probe`), `"apply"` (stage `plex_collection`), `"open_settings"`, `"show_workers"`; snapshot `health.nas.open_ms_p95: 850`.
- **Steps:**
  1. Read the retry button label.
  2. Click "Apply".
  3. Click "Open settings"; go back; click "Show workers".
  4. Click a sample subject link.
- **Expected:** step 1 label "Retry N (NAS open p95 850 ms)" for `nas_*` causes. Step 2 posts `/api/queues/plex_collection/apply` with `{ "all": true }`, then reloads `/api/queues`, `/api/issues`, `/api/queues/libraries`. Step 3 goes to `/admin/extensions` then `/admin/health`. Step 4 goes to `/queue/items/<kind>/<id>` (encoded).

### QOV-19 — Queue work dialog: validation, close, keyboard

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Partial (`QueueOverview.cases.test.tsx`: no focus-return / focus-trap / Seasons error check)
- **Preconditions / data:** admin; routes as QOV-01; `GET /api/items/library/<id>/history` rejects for one library; `POST /api/queue/retrigger` → `{ "results": null }` for one run.
- **Steps:**
  1. Click "Queue work…" (dialog title "Queue work").
  2. With nothing ticked, check "Queue work (0)".
  3. Tick a library and a queue whose `subject_kinds` lacks `"library"`; click submit.
  4. Click "Seasons…" on the failing library.
  5. Press Escape; reopen; Tab through.
  6. Submit a valid choice that gets `results: null`.
- **Expected:** step 2 the submit button is disabled. Step 3 shows `role="alert"` "None of the chosen queues work on the chosen items." and posts nothing. Step 4 shows "The seasons could not be loaded.". Step 5 the dialog closes on Escape and focus returns to "Queue work…"; on reopen the choices are reset and focus stays inside the dialog while tabbing. Step 6 shows a result or error, not a crash (currently unhandled — likely bug: `summarize()` reads `res.results` without a guard).

### QOV-20 — Live updates over the cluster socket

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `QueueOverview.socket.test.tsx` "subscribes to queues, jobs and workers…", "shows each queue's jobs live…", "shows a job already running…", "takes the workers from the socket"
- **Preconditions / data:** `vi.stubGlobal("WebSocket", FakeSocket)`; `FakeSocket.last.open()`; `recv({ type: "queues", data: snapshotFixture })`; a `job` message with `event: "progress"`, `node_name: "Studio Mac"`.
- **Steps:**
  1. Open `/queue` and send the messages.
- **Expected:** the socket subscribes to `queues`, `jobs`, `workers`; the card for the job's stage lists the job with "on Studio Mac" and a progress bar; a `done` event removes it and adds an Activity line; at most 3 live jobs per card plus "+N more".

### QOV-21 — Socket drops: SSE takes over, then polling with a note

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** fake timers; socket open and a hub snapshot shown; then `FakeSocket.last.drop()`; SSE silent.
- **Steps:**
  1. Drop the socket.
  2. Advance 10 s, then 5 s.
  3. Emit SSE `queues.snapshot`.
- **Expected:** step 1: SSE subscriptions for `queues` and `jobs` exist (`hub.count(...) === 1`), page keeps the last data. Step 2: `role="status"` "Live updates are paused; refreshing every few seconds instead." and `/api/queues` polled every 5 s. Step 3: the note disappears and polling stops.

### QOV-22 — Socket reconnects: back on the hub, no SSE, no duplicates

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** fake timers; socket open; `drop()`; advance past `BACKOFF_START_MS` (1 s) so a new `FakeSocket` is created; `FakeSocket.last.open()`; it receives `{ type: "snapshot", topic: "jobs", data: { jobs: [], running: [] } }` and a `queues` snapshot.
- **Steps:**
  1. Reconnect as above.
  2. Emit a `job.log` on SSE.
- **Expected:** the new socket re-subscribes `queues`, `jobs`, `workers`; SSE listeners are removed (`hub.count("queues") === 0`); the polling note is gone; the step 2 SSE line does not appear; the running map is reset by the jobs snapshot (no ghost jobs on cards); no duplicate Activity lines.

### QOV-23 — `?host=` filter: panel, unknown host, failures, way back

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** (a) `/queue?host=node-zz` where no worker has `node_id: "node-zz"`, `GET /api/queues/running?host=node-zz` → `{ host: "node-zz", jobs: [] }`; (b) `/queue?host=home` with `/api/queues/running` → 500; (c) `/queue?host=` (empty).
- **Steps:**
  1. Open each URL.
  2. In (a) click "Show every machine".
  3. Press browser Back.
- **Expected:** (a) heading "Running on node-zz", text "Nothing is running on node-zz right now."; card live jobs are filtered to that host (none). (b) heading "Running on this home", empty text (error is treated as none). (c) no host panel. Step 2 goes to `/queue` without the panel and all live jobs show again. Step 3 restores `?host=node-zz` and the panel.

### QOV-24 — Links: cards, Reports, Extensions, libraries; Back/Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** app routes in a `MemoryRouter` (or e2e); routes as QOV-01.
- **Steps:**
  1. Click a queue card title (e.g. "Length") → `/queue/<stage>`.
  2. Back. Click "Reports" → `/queue/reports`. Back. Click "Extensions" → `/admin/extensions`.
  3. Back. Click a library name in the table → `/queue/items/library/<id>`.
  4. Back, then Forward.
- **Expected:** each link goes to the right route (ids URL-encoded); Back returns to `/queue` with the overview rendered (no stale "Loading…" stuck); Forward goes to the item page again.

### QOV-25 — Libraries table: filter, select all, no match

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `librariesFixture` (2+ rows).
- **Steps:**
  1. Type "zzz" in "Search libraries".
  2. Clear it; tick "Select all libraries".
  3. Click "Re-queue selected (N)".
  4. Click a row's "Re-queue".
- **Expected:** step 1 "No libraries match.". Step 2 every row ticked, button "Re-queue selected (2)" enabled (disabled at 0). Step 3 opens "Queue work" with those libraries ticked. Step 4 opens it with only that library.

### QOV-26 — Activity log: filters, Load older, failure

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`QueueOverview.cases.test.tsx`)
- **Preconditions / data:** `/api/activity` → `{ items: logFixture, next: "c1" }`; with `cursor=c1` → first reject (500), then `{ items: [...], next: null }`; separately the first `/api/activity` call rejects.
- **Steps:**
  1. Pick a queue in the "Queue" select.
  2. Click "Load older" (fails), click again (works).
  3. Reload with the first call failing.
- **Expected:** step 1 requests `/api/activity?stage=<stage>&level=info&limit=100` and shows "Loading…" then that queue's lines. Step 2: failure keeps the lines and the button (ideally with an error note — currently the failure is silent); success appends lines and hides "Load older". Step 3 shows "The activity log could not be loaded.". More than 200 lines shows "Show all loaded lines".

### QOV-27 — Phone width 375 px: no horizontal scroll

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** browser e2e at 375×812; full fixtures.
- **Steps:**
  1. Open `/queue`; scroll to the bottom.
  2. Open "Queue work…".
- **Expected:** `document.documentElement.scrollWidth <= 375`; health tiles and lanes wrap or stack; the Libraries table scrolls inside `.mkt-q-tablewrap` only; header buttons wrap; the bottom tab bar shows "Processing" as current; the dialog fits the screen and its footer buttons are reachable.

### QOV-28 — Arabic: RTL, Arabic strings, mirrored layout

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** Partial — `QueueOverview.test.tsx` "renders in Arabic with the localised waiting reason"
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; fixtures with an Arabic library name and an Arabic job title.
- **Steps:**
  1. Open `/queue`.
- **Expected:** `<html dir="rtl">`; `h1` "طوابير المعالجة"; error text (QOV-03) "تعذّر تحميل الطوابير."; numbers via the Arabic formatter; Arabic names isolated (`bidiIsolate`) so "· on Studio Mac" does not reorder; flow arrows and progress bars run right to left.

### QOV-29 — Light and dark theme use tokens only

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** Yes — `QueueOverview.test.tsx` "uses only token colours (no inline colour styles) in light and dark"
- **Preconditions / data:** `<html data-theme="light">` then `"dark"`.
- **Steps:**
  1. Open `/queue` in each theme.
- **Expected:** no inline `color`/`background`/`fill`/`stroke` styles; text, pills, error banner and sparkline readable in both (contrast checked in e2e screenshot).

### QOV-30 — Keyboard and accessibility basics

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** Partial — `a11y.test.tsx` "queue overview has no violations"
- **Preconditions / data:** full fixtures.
- **Steps:**
  1. Tab from the top of the page through header, cards, issues, log filters, table.
  2. Check names and live regions.
- **Expected:** visible focus on every control; tab order follows the visual order; each card has one link (the title); the status pill, polling note and dialog result are `role="status"`; errors are `role="alert"`; table checkboxes have names "Select all libraries" / "Select <name>"; the pool and progress bars have text names; axe has no violations, also in the error state (QOV-03).
