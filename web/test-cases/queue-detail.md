# Queue detail (one stage)

- **Route:** `/queue/:stage` (e.g. `/queue/fingerprint`, `/queue/probe`); optional `?host=<node id | home>` and `?name=` narrow "Running now" to one machine. Note: `/queue/reports` and `/queue/items/...` are matched by their own routes first (`App.tsx`), so a stage can never be called `reports`.
- **Component:** `src/pages/queue/QueueDetail.tsx`; children: `src/components/queue/PoolStepper.tsx`, `RingGauge.tsx`, `StatTile.tsx` (KPIs), `RateChart.tsx`, `RunningJobs.tsx`, `NextUp.tsx`, `IssueList.tsx`, `ActivityLog.tsx`, `RetriggerDialog.tsx`, `hostFilter.tsx` (`useHostName`, `hostWorkerIds`, `runsOnHost`), `useAction.ts`, `useThrottledReload.ts`.
- **API calls:** (`src/lib/queueApi.ts`)
  - `GET /api/queues/{stage}` → `QueueDetailData` `{ info, summary, kpis, running, next_up, failures, log }` via `queueApi.detail(stage)`; reloaded (throttled, 10 s) when a job of this stage finishes on the stream, and after every action.
  - `GET /api/queues/{stage}/rate?hours=6` → `{ bucket_sec, points: RatePoint[] }` via `queueApi.rate`.
  - `GET /api/queues/libraries` → `{ items: LibraryQueueRow[] }` (for the re-queue dialog).
  - `GET /api/queues` → `QueuesSnapshot` (`useQueuesSnapshot`; live `info`, `summary`, `pools` win over the REST detail), `GET /api/workers` (`useWorkers`), `GET /api/activity?stage={stage}&level=…&limit=100[&cursor=]` (log paging).
  - Mutations: `POST /api/queues/{stage}/pause[?interrupt=true]`, `POST /api/queues/{stage}/resume`, `POST /api/queues/{stage}/retry {cause}` (per retry group, sequential for "Retry failed (N)"), `POST /api/queues/{stage}/apply {all:true}`, `PUT /api/pools/{pool} {size}` (`null` = Auto), `PATCH /api/queues/{stage} {write_mode}`, `POST /api/queue/retrigger`.
  - Live: cluster socket topics `queues`, `jobs` (filtered to this stage), `workers`; SSE fallback (`src/lib/ws.ts`). There is no "cancel job" action on this page.
- **Access:** any signed-in user (`RequireAuth`); no `AdminGate`; no admin check in the page at all (every button is offered; the server enforces admin).
- **Existing tests:** `src/pages/queue/QueueDetail.test.tsx` covers: breadcrumb, description, KPIs, neighbours, chart alt text, running sub-steps, failures with retry, next up, shared pool note; pause with interrupt; Resume for a paused queue; pool stepper +/-/Auto and the 0/64 bounds; "Retry failed (7)" by cause; running jobs live from the stream; no-worker message; re-queue dialog prefilled. `src/pages/queue/hostFilter.test.tsx` "narrows a stage's running jobs to the machine". `src/pages/queue/a11y.test.tsx` "queue page has no violations". Gaps: unknown stage; load errors (all show "no queue"); loading; null lists; error boundary/Back; signed out; non-admin 403; failed actions; Apply; write mode; neighbour navigation and stale responses; log paging; phone; RTL; theme.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test?                                            |
| ------ | ----------------------------------------------------------- | ------------- | -------- | -------------------------------------------------------------------- |
| QDT-01 | Renders the stage page from detail + snapshot               | Happy path    | P1       | Yes (`QueueDetail.test.tsx`)                                         |
| QDT-02 | Shows Loading until detail and stage info are known         | Loading       | P2       | No                                                                   |
| QDT-03 | Unknown stage shows "There is no queue called …"            | Navigation    | P1       | No                                                                   |
| QDT-04 | 500 / network / 401 on detail is not reported as "no queue" | Error         | P1       | No                                                                   |
| QDT-05 | Stage ids with special characters are encoded               | Navigation    | P3       | No                                                                   |
| QDT-06 | Detail list fields `null` or missing                        | Empty state   | P1       | No                                                                   |
| QDT-07 | Rate `points` `null` / rate call fails                      | Empty state   | P1       | No                                                                   |
| QDT-08 | Snapshot / libraries list fields `null` or missing          | Empty state   | P1       | No                                                                   |
| QDT-09 | Empty queue: nothing running, waiting or failed             | Empty state   | P2       | No                                                                   |
| QDT-10 | Render crash shows route error fallback; Back works         | Error         | P1       | No                                                                   |
| QDT-11 | Signed-out user is sent to `/login`                         | Permissions   | P1       | No                                                                   |
| QDT-12 | Non-admin: actions answer 403 with a readable alert         | Permissions   | P1       | No                                                                   |
| QDT-13 | Pause (with interrupt) and Resume                           | Happy path    | P1       | Yes (`QueueDetail.test.tsx`)                                         |
| QDT-14 | Failed pause / resume / pool change shows an alert          | Error         | P2       | No                                                                   |
| QDT-15 | Pool stepper: bounds, Auto, shared note                     | Validation    | P2       | Yes (`QueueDetail.test.tsx`)                                         |
| QDT-16 | "Retry failed (N)" retries every retry group                | Happy path    | P1       | Partial (`QueueDetail.test.tsx`: one group, no failure, no disabled) |
| QDT-17 | Failure group Apply and per-group Retry                     | Happy path    | P2       | Partial (`QueueDetail.test.tsx`: retry label only)                   |
| QDT-18 | Write mode select for external-write stages                 | Happy path    | P2       | No                                                                   |
| QDT-19 | Running jobs follow the live job stream                     | Happy path    | P1       | Yes (`QueueDetail.test.tsx`)                                         |
| QDT-20 | No-worker banner and served-by note                         | Happy path    | P2       | Partial (`QueueDetail.test.tsx`: no-worker only)                     |
| QDT-21 | Re-queue dialog is prefilled with this stage                | Happy path    | P2       | Yes (`QueueDetail.test.tsx`)                                         |
| QDT-22 | Neighbour chips switch stage; no stale data; Back/Forward   | Navigation    | P1       | No                                                                   |
| QDT-23 | `?host=` narrows running jobs; "Show every machine"         | Navigation    | P2       | Partial (`hostFilter.test.tsx`: no link back / unknown host)         |
| QDT-24 | Log paging and Next up / running links                      | Navigation    | P2       | No                                                                   |
| QDT-25 | Phone width 375 px                                          | Responsive    | P3       | No                                                                   |
| QDT-26 | Arabic: RTL, Arabic labels, mirrored neighbours             | RTL           | P3       | No                                                                   |
| QDT-27 | Light and dark theme                                        | Theme         | P3       | No                                                                   |
| QDT-28 | Keyboard and accessibility basics                           | Accessibility | P3       | Partial (`a11y.test.tsx`: axe only)                                  |

## Cases

### QDT-01 — Renders the stage page from detail + snapshot

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `QueueDetail.test.tsx` "renders breadcrumb, description, KPIs, neighbours, chart, running and failures"
- **Preconditions / data:** `GET /api/queues/fingerprint` → `detailFixture("fingerprint")`; `/api/queues/fingerprint/rate` → `rateFixture`; `/api/queues` → `snapshotFixture`; `/api/queues/libraries` → `{ items: librariesFixture }`; `/api/activity` → `{ items: [], next: null }`.
- **Steps:**
  1. Open `/queue/fingerprint`.
- **Expected:** breadcrumb nav "Breadcrumb" with link "Processing queues" → `/queue` and current item "Fingerprint"; `h1` "Fingerprint"; description from `queue.stage.fingerprint.desc`; KPI groups Waiting, Running ("1 of 2"), Rate, Left, Failed, Duration; neighbours "Where work comes from and goes to"; chart `img` "Jobs finished per 10 minutes…"; "Running now", "Next up", "Failures by cause", "This queue's log" sections.

### QDT-02 — Shows Loading until detail and stage info are known

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/queues/fingerprint` never resolves; snapshot resolves.
- **Steps:**
  1. Open `/queue/fingerprint`.
- **Expected:** only `role="status"` "Loading…"; no controls. Resolving the detail shows the page.

### QDT-03 — Unknown stage shows "There is no queue called …"

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/queues/nope` → 404 `application/problem+json` `{ "title": "Not Found", "status": 404 }`; `/api/queues/nope/rate` → 404; snapshot normal.
- **Steps:**
  1. Open `/queue/nope` directly.
  2. Click "Back to the queues".
- **Expected:** `role="alert"` "There is no queue called nope." and link "Back to the queues" → `/queue`; no controls (no Pause button). Step 2 renders the overview. Browser Back returns to `/queue/nope` and shows the same message.

### QDT-04 — 500 / network / 401 on detail is not reported as "no queue"

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/queues/fingerprint` → (a) 500 `{ "title": "Internal Server Error" }`; (b) `fetch` rejects `TypeError("Failed to fetch")`; (c) 401; (d) 200 `text/html` body.
- **Steps:**
  1. Open `/queue/fingerprint`.
- **Expected:** a load error such as "Could not load this queue" with a "Try again" button that refetches; for 401 a redirect to `/login`. Not "There is no queue called fingerprint." (currently unhandled — likely bug: `.catch(() => setMissing(true))` in `QueueDetail.tsx` turns every failure into "no queue"; (d) resolves to a string and the page then throws on `detail.failures.filter`).

### QDT-05 — Stage ids with special characters are encoded

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** a stage `"a b/c"` in `snapshotFixture.stages`; route `/queue/a%20b%2Fc`.
- **Steps:**
  1. Open the route; click a neighbour chip pointing at it.
- **Expected:** API path `/api/queues/a%20b%2Fc` and `/api/queues/a%20b%2Fc/rate?hours=6`; links built with `encodeURIComponent`; the not-found text shows the decoded name.

### QDT-06 — Detail list fields `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/queues/fingerprint` → `detailFixture("fingerprint")` with, one at a time set to `null` and then deleted: `failures`, `next_up`, `log`, `running`, `info.edges`, `info.fed_by`, `summary.served_by`, `running[0].progress.steps`, `failures[0].sample_subjects`, `kpis` (whole object). Snapshot without this stage (so the REST `info` / `summary` are used).
- **Steps:**
  1. Open `/queue/fingerprint`.
- **Expected:** page renders; "Retry failed (0)" disabled and "Failures by cause" says "Nothing needs attention." for `failures`; "Next up" "Nothing is waiting."; log "No activity yet." (not "Loading…" forever); "Running now" "Nothing is running."; neighbours "Starts here" / "Last step"; KPIs show "–". (currently unhandled — likely bug: `detail.failures.filter`, `info.edges.map`, `info.fed_by.length`, `jobs.length` in `NextUp.tsx`, `p.steps.indexOf` in `RunningJobs.tsx`, `detail.kpis.done_24h` throw; `log: null` makes `ActivityLog` skip its fetch and show "Loading…" forever; `running` and `served_by` are already guarded with `??`.)

### QDT-07 — Rate `points` `null` / rate call fails

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `/api/queues/fingerprint/rate` → (a) `{ "bucket_sec": 600, "points": null }`; (b) `{}`; (c) 500; (d) `{ "points": [] }`.
- **Steps:**
  1. Open `/queue/fingerprint`.
- **Expected:** the chart section renders an empty chart whose text alternative reports 0 done, 0 failed, 0 timeouts; the rest of the page is fine. (currently unhandled — likely bug for (a)/(b): `setRate(r.points)` stores `null`/`undefined` and `points.map` in `RateChart.tsx` throws; (c) keeps `[]` and is fine.)

### QDT-08 — Snapshot / libraries list fields `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** (a) `GET /api/queues` → `{ ...snapshotFixture, stages: null }` (and `queues: null`, `pools: null` each); (b) `/api/queues/libraries` → `{ "items": null }` and `{}`; (c) `pools[n].stages: null` for this stage's pool.
- **Steps:**
  1. Open `/queue/fingerprint`; open "Re-queue a library or season…".
- **Expected:** falls back to the REST `info`/`summary`; no pool stepper when pools are missing; the dialog lists no libraries; nothing throws. (currently unhandled — likely bug: `snapshot?.stages.find` only guards `snapshot`, not `stages`; `pool.stages.filter` in `PoolStepper.tsx` and `libraries.filter` in `RetriggerDialog.tsx` throw.)

### QDT-09 — Empty queue: nothing running, waiting or failed

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** detail with `running: []`, `next_up: []`, `failures: []`, `log: []`, summary counts all 0, `waiting_reason: "idle"`, `eta_sec: null`.
- **Steps:**
  1. Open the stage page.
- **Expected:** "Nothing is running.", "Nothing is waiting.", "Nothing needs attention.", "No activity yet."; "Retry failed (0)" disabled; no "Also ask running jobs to pause" checkbox; Left KPI "–" with the idle reason.

### QDT-10 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** full app routes; start at `/queue`; `GET /api/queues/fingerprint` → detail with `failures: null` (QDT-06) or mock `RateChart` to throw.
- **Steps:**
  1. On `/queue`, click the "Fingerprint" card.
  2. See the fallback; press browser Back.
  3. Click the "Fingerprint" card again after fixing the mock; also try a nav link.
- **Expected:** step 2 shows the route error fallback inside the shell, not a blank page; Back renders the overview without a refresh; step 3 renders the stage page (the boundary resets on location change).

### QDT-11 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** no user in `AuthProvider`.
- **Steps:**
  1. Open `/queue/fingerprint`.
- **Expected:** redirect to `/login` with `state.from = "/queue/fingerprint"`; no `/api/queues/fingerprint` request.

### QDT-12 — Non-admin: actions answer 403 with a readable alert

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** non-admin user; all `POST`/`PUT`/`PATCH` → 403 `{ "title": "Forbidden", "status": 403, "detail": "admin only" }`.
- **Steps:**
  1. Click "Pause this queue", "More workers", "Retry failed (N)", change "Writes to other systems".
- **Expected:** each shows `role="alert"` "Forbidden: admin only"; nothing changes; buttons return from loading. Better: these controls hidden or disabled for non-admins (currently unhandled — likely bug: the page has no admin check).

### QDT-13 — Pause (with interrupt) and Resume

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `QueueDetail.test.tsx` "pauses the queue, optionally interrupting running jobs", "offers Resume for a paused queue"
- **Preconditions / data:** stage with `counts.running > 0`; then a stage with `info.paused: true`.
- **Steps:**
  1. Tick "Also ask running jobs to pause"; click "Pause this queue".
  2. On the paused stage click "Resume this queue".
- **Expected:** `POST /api/queues/fingerprint/pause?interrupt=true` (without the tick: no query); detail reloaded after; state line shows "· paused" when paused; `POST /api/queues/link/resume`. The interrupt checkbox only shows while something runs.

### QDT-14 — Failed pause / resume / pool change shows an alert

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST …/pause` → 500 `{ "title": "Internal Server Error", "detail": "db down" }`; `PUT /api/pools/nas_read` rejects `TypeError("Failed to fetch")`.
- **Steps:**
  1. Click "Pause this queue"; click "More workers".
- **Expected:** `role="alert"` "Internal Server Error: db down", then "Failed to fetch"; the stepper value stays as before; buttons enabled again.

### QDT-15 — Pool stepper: bounds, Auto, shared note

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes — `QueueDetail.test.tsx` "steps the shared pool size and respects the bounds", "disables Fewer workers at 0 and More workers at 64"
- **Preconditions / data:** pool `nas_read` `size: 2`, `effective_size: 2`, shared with other stages; pool with `size: null` (auto).
- **Steps:**
  1. Click "More workers", "Fewer workers", "Auto".
- **Expected:** `PUT /api/pools/nas_read` with `{ size: 3 }`, `{ size: 1 }`, `{ size: null }`; disabled at 0 / 64; "Auto" hidden when `size` is `null`; the note names the other stages; the value `output` is `aria-live="polite"`.

### QDT-16 — "Retry failed (N)" retries every retry group

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `QueueDetail.test.tsx` "retries failed groups by cause"
- **Preconditions / data:** detail `failures` with two `action: "retry"` groups (counts 3 and 4, causes `a`, `b`) and one `action: "apply"` group; second retry call rejects 500 in one run.
- **Steps:**
  1. Click "Retry failed (7)".
- **Expected:** posts `/api/queues/{stage}/retry` with `{ cause: "a" }` then `{ cause: "b" }` (not the apply group); detail reloads. When the second fails: alert with the message and the first stays done. With no retry groups the button reads "Retry failed (0)" and is disabled.

### QDT-17 — Failure group Apply and per-group Retry

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `QueueDetail.test.tsx` checks a "Retry 2" button exists
- **Preconditions / data:** external-write stage `plex_collection` with a failure group `action: "apply"`; another with `action: "retry"`.
- **Steps:**
  1. Click "Apply"; click "Retry N".
- **Expected:** `POST /api/queues/plex_collection/apply` `{ all: true }`; `POST /api/queues/plex_collection/retry` `{ cause }`; the clicked button shows loading only for its own group; no stage chip in groups (`hideStage`).

### QDT-18 — Write mode select for external-write stages

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** stage `info.external_write: true`, `write_mode: "auto"`; `PATCH` resolves; second detail fetch returns `write_mode: "review"`.
- **Steps:**
  1. Choose "Review first" in "Writes to other systems".
- **Expected:** `PATCH /api/queues/{stage}` with `{ write_mode: "review" }`, then the detail reloads and the select shows "Review first". Stages without `external_write` have no select.

### QDT-19 — Running jobs follow the live job stream

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `QueueDetail.test.tsx` "tracks running jobs live from the job stream"
- **Preconditions / data:** `events.jobStarted(jobRef({ id: 7777, stage: "probe" }))`, then `events.jobFinished(...)`; plus a job event for another stage.
- **Steps:**
  1. Emit the events on `jobs`.
- **Expected:** the job appears in "Running now" within ~1 s and leaves on finish; the other stage's job never shows; a finished job triggers a throttled detail reload.

### QDT-20 — No-worker banner and served-by note

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `QueueDetail.test.tsx` "explains when no live worker serves the queue"
- **Preconditions / data:** summary `no_worker: true`, `running: 0`, `pending: 5`; another run with `served_by: ["This PC", "Mac"]`.
- **Steps:**
  1. Open the stage page.
- **Expected:** `role="status"` "Waiting for a worker that serves Transcribe" banner and the same in the Left KPI; with `served_by` a note listing "This PC, Mac".

### QDT-21 — Re-queue dialog is prefilled with this stage

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `QueueDetail.test.tsx` "opens the re-queue dialog prefilled with this queue"
- **Preconditions / data:** stage `detect`.
- **Steps:**
  1. Click "Re-queue a library or season…"; pick a library; submit.
- **Expected:** dialog with checkbox "Place episodes" ticked; retrigger items carry `stage: "detect"`; the detail reloads on done.

### QDT-22 — Neighbour chips switch stage; no stale data; Back/Forward

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** app routes; `/queue/fingerprint` loaded; `GET /api/queues/detect` delayed 500 ms; `GET /api/queues/link` immediate.
- **Steps:**
  1. Click neighbour chip "Place episodes" (→ `/queue/detect`), then quickly another chip to `/queue/link`.
  2. Press Back, then Forward.
- **Expected:** step 1 ends on `/queue/link` with `h1` for link; the late `detect` response must not replace it (currently unhandled — likely bug: `load()` in `QueueDetail.tsx` has no stale-response guard). Each stage change shows "Loading…" first, not the old stage and not a stale "no queue" message from a previous unknown stage. Back shows the previous stage with its own data.

### QDT-23 — `?host=` narrows running jobs; "Show every machine"

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial — `hostFilter.test.tsx` "narrows a stage's running jobs to the machine"
- **Preconditions / data:** `/queue/probe?host=node-1&name=Studio%20Mac`; workers with `node_id: "node-1"`; one running job claimed by that worker, one by another; then `?host=node-zz` (unknown).
- **Steps:**
  1. Open the URL; click "Show every machine".
- **Expected:** note "Only the jobs on Studio Mac." and only the matching job; the link goes to `/queue/probe` and both jobs show. Unknown host: "Nothing is running." and the note names `node-zz`.

### QDT-24 — Log paging and Next up / running links

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** detail `log` with lines; `GET /api/activity?stage=fingerprint&level=info&cursor=c1&limit=100` → `{ items: [...], next: null }`; first problems request returns `next: "c1"`.
- **Steps:**
  1. Tick "Problems only"; click "Load older".
  2. Click a "Next up" item and a running job title.
- **Expected:** the log fetches `level=warn` for this stage only, then appends older lines; no queue select is shown (fixed stage). Links go to `/queue/items/<kind>/<id>`.

### QDT-25 — Phone width 375 px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e 375×812.
- **Steps:**
  1. Open `/queue/fingerprint`.
- **Expected:** no horizontal page scroll; controls wrap; KPI tiles stack; chart scales to width; neighbours wrap; bottom tab bar reachable.

### QDT-26 — Arabic: RTL, Arabic labels, mirrored neighbours

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; job labels like "حلقة 12".
- **Steps:**
  1. Open `/queue/fingerprint` and `/queue/nope`.
- **Expected:** `dir="rtl"`; Arabic button labels; not-found text "لا يوجد طابور باسم nope."; the "→" arrows read in the right direction for RTL (or are mirrored); Arabic labels isolated.

### QDT-27 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light / dark.
- **Steps:**
  1. Open the page in both.
- **Expected:** no inline colour styles (same check as the overview's token test); state chip, alert banner, chart bars and the current neighbour chip readable in both.

### QDT-28 — Keyboard and accessibility basics

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** Partial — `a11y.test.tsx` "queue page has no violations"
- **Preconditions / data:** full fixtures.
- **Steps:**
  1. Tab through breadcrumb, controls, stepper, failures, log.
- **Expected:** breadcrumb current item has `aria-current="page"`; stepper buttons named "Fewer workers" / "More workers"; current sub-step `aria-current="step"`; alerts `role="alert"`; focus visible; axe clean in the not-found state too.
