# Transcription report

- **Route:** `/transcription` (no params, no query string). In the nav as "Transcription"; on phones it lives behind "More" (`MORE_ONLY` in `src/components/AppShell.tsx`).
- **Component:** `src/pages/transcription/Transcription.tsx` (inner `Progress`, `Speed`/`SpeedRow`/`LiveRow`, `TimeLeft`, `Stages`, `Libraries`, `Failures`); uses `ProgressBar` from `@ds/components/ProgressBar/ProgressBar` and `useFmt` from `src/components/queue/format.ts`.
- **API calls:**
  - `GET /api/transcription/summary` → `TranscriptionSummary` `{ generated_at, totals, stages: TranscriptionStageCounts[], rate: { last_10m, last_1h, live? }, eta: { seconds, at, basis }, libraries: TranscriptionLibrary[], failures: TranscriptionFailure[] }` via `transcriptionApi.summary()` (`src/lib/transcriptionApi.ts`).
  - Live: SSE `subscribe("queues", …)` (`src/lib/ws.ts`, `/ws/queues`) in `useTranscriptionSummary` (`src/lib/useTranscriptionSummary.ts`): a `queues.snapshot` whose `stagesKey` (transcribe / subtitle_gen / index counts) changes triggers a refetch, throttled to one per 5 s (`RELOAD_MIN_MS`). After 10 s without a snapshot it polls the summary every 5 s until a snapshot arrives. This page does not use the cluster WebSocket.
- **Access:** any signed-in user (`RequireAuth`); no `AdminGate`; no actions on the page (read-only).
- **Existing tests:** `src/pages/transcription/Transcription.test.tsx` covers: progress, speed, time left and stage counters; live decoding row and ETA by it; failure links to the item page; library table sort by column; unknown ETA and empty `libraries: []` / `failures: []`; refetch only when the transcription queues move, with throttling; 5 s polling after 10 s of silence and stop on snapshot; retry when the first load fails; Arabic RTL; axe; `stagesKey` fingerprint. Gaps: loading; refresh failure after a good load ("stale"); 401/403/malformed; `null`/missing lists and objects; `name: null` library; unknown failure stage; error boundary/Back; signed out; ETA done / basis variants; sort select; navigation; phone; theme; keyboard.

## Summary

| ID     | Title                                                      | Category      | Priority | Covered by existing test?                                    |
| ------ | ---------------------------------------------------------- | ------------- | -------- | ------------------------------------------------------------ |
| TRN-01 | Renders progress, speed, time left, stages, libraries      | Happy path    | P1       | Yes (`Transcription.test.tsx`)                               |
| TRN-02 | Shows Loading under the title while pending                | Loading       | P2       | No                                                           |
| TRN-03 | First load fails, Try again recovers                       | Error         | P1       | Yes (`Transcription.test.tsx`)                               |
| TRN-04 | Refresh fails after a good load: stale note, data kept     | Error         | P2       | No                                                           |
| TRN-05 | 401 / 403 / malformed summary                              | Error         | P2       | No                                                           |
| TRN-06 | `stages` / `libraries` / `failures` `null` or missing      | Empty state   | P1       | No                                                           |
| TRN-07 | `totals` / `rate` / `eta` objects `null` or missing        | Empty state   | P1       | No                                                           |
| TRN-08 | Library with `name: null` or `""`, missing `library_id`    | Empty state   | P1       | No                                                           |
| TRN-09 | Empty lists and zero totals                                | Empty state   | P2       | Partial (`Transcription.test.tsx`: empty lists, unknown ETA) |
| TRN-10 | Render crash shows route error fallback; Back works        | Error         | P1       | No                                                           |
| TRN-11 | Signed-out user is sent to `/login`                        | Permissions   | P1       | No                                                           |
| TRN-12 | Non-admin user sees the full report                        | Permissions   | P2       | No                                                           |
| TRN-13 | Time left: done, live / 10 min / 1 h basis                 | Happy path    | P2       | Partial (`Transcription.test.tsx`: live basis, unknown)      |
| TRN-14 | Speed: live row only with workers; lost hours              | Happy path    | P2       | Partial (`Transcription.test.tsx`: live row only)            |
| TRN-15 | Library table sorts by header and by the select            | Happy path    | P2       | Partial (`Transcription.test.tsx`: header buttons only)      |
| TRN-16 | Failures: cause, unknown cause, unknown stage, links       | Happy path    | P2       | Partial (`Transcription.test.tsx`: link only)                |
| TRN-17 | Live refetch only when transcription queues move           | Happy path    | P1       | Yes (`Transcription.test.tsx`)                               |
| TRN-18 | Stream silent: polls every 5 s with a note; stops on event | Error         | P1       | Yes (`Transcription.test.tsx`)                               |
| TRN-19 | Malformed SSE snapshot does not crash or refetch           | Error         | P2       | Partial (`Transcription.test.tsx`: `stagesKey` unit only)    |
| TRN-20 | Leaving the page stops polling and the stream              | Navigation    | P2       | No                                                           |
| TRN-21 | Links: stage cards, failures, nav; Back / Forward          | Navigation    | P2       | Partial (`Transcription.test.tsx`: hrefs only)               |
| TRN-22 | Phone width 375 px; reached through "More"                 | Responsive    | P3       | No                                                           |
| TRN-23 | Arabic: RTL, Arabic numbers and names                      | RTL           | P3       | Yes (`Transcription.test.tsx`)                               |
| TRN-24 | Light and dark theme                                       | Theme         | P3       | No                                                           |
| TRN-25 | Keyboard and accessibility basics                          | Accessibility | P3       | Partial (`Transcription.test.tsx`: axe only)                 |

## Cases

### TRN-01 — Renders progress, speed, time left, stages, libraries

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Transcription.test.tsx` "shows progress, speed, time left and the stage counters"
- **Preconditions / data:** `GET /api/transcription/summary` → the test's `summaryFixture` (totals with `hours_remaining` 110, three stages, libraries, failures); SSE mocked via `vi.mock("../../lib/ws")`.
- **Steps:**
  1. Open `/transcription`.
- **Expected:** `h1` "Transcription"; `role="status"` "Live"; cards "Audio hours transcribed" (percent, "X h of Y h", progress bar, "110 h left") and "Videos transcribed" ("N subtitle segments"); "Speed" with "Last 10 minutes" and "Last hour" rows ("N× realtime"); "Time left"; "Stages" with Transcribe / Subtitles / Search index cards, each with Waiting / Running / Done / Failed; "Libraries" table; "Recent failures".

### TRN-02 — Shows Loading under the title while pending

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** summary request never resolves.
- **Steps:**
  1. Open `/transcription`.
- **Expected:** `h1` "Transcription" and `role="status"` "Loading…"; no alert.

### TRN-03 — First load fails, Try again recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes — `Transcription.test.tsx` "offers a retry when the first load fails"
- **Preconditions / data:** first summary call rejects (`new Error("down")`; also 500 and `TypeError("Failed to fetch")`); then the fixture.
- **Steps:**
  1. Open the page; click "Try again".
- **Expected:** `role="alert"` "Could not load the transcription report." with button "Try again"; after the click the report renders.

### TRN-04 — Refresh fails after a good load: stale note, data kept

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; first call OK; then the stream is silent so polling starts, and the next summary call returns 500; then OK again.
- **Steps:**
  1. Load; advance 15 s; advance 5 s more.
- **Expected:** after the failed poll the header status reads "Could not refresh; showing the last report" and the dot is off; all numbers stay; after a successful poll it reads "Live updates paused, refreshing every 5 s" (or "Live" once a snapshot arrives).

### TRN-05 — 401 / 403 / malformed summary

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** summary → (a) 401; (b) 403; (c) 200 `application/json` truncated body; (d) 200 `text/html` (proxy fallback page).
- **Steps:**
  1. Open the page.
- **Expected:** (a)–(c) the load error alert with "Try again" (401 ideally sends to `/login`). (d) the same alert, not a crash. (currently unhandled — likely bug: a `text/html` 200 resolves to a string, `summary` is truthy and `s.totals.hours_transcribed` throws.)

### TRN-06 — `stages` / `libraries` / `failures` `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** summary fixture with, one at a time, `stages: null`, `stages` missing, `libraries: null`, `libraries` missing, `failures: null`, `failures` missing.
- **Steps:**
  1. Open the page.
- **Expected:** stages section shows the three cards with zeros; "No libraries yet."; "No open failures."; the rest of the report renders. (currently unhandled — likely bug: `s.stages.map` in `Stages`, `[...rows]` in `Libraries` and `s.failures.length` in `Failures` throw on `null`/`undefined`.)

### TRN-07 — `totals` / `rate` / `eta` objects `null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** one at a time: `totals: null`; `rate: null`; `rate.last_10m: null`; `rate.live` missing (older API); `eta: null`; `totals.segments` missing.
- **Steps:**
  1. Open the page.
- **Expected:** each card shows "–" / 0 or hides, the page does not go blank; `rate.live` missing simply hides the "Decoding now" row (already supported). (currently unhandled — likely bug: `s.totals`, `s.rate.last_10m.realtime_factor` and `const { seconds } = s.eta` throw on `null`.)

### TRN-08 — Library with `name: null` or `""`, missing `library_id`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `libraries: [{ library_id: "", name: "", videos: 3, videos_done: 1, hours: 2, hours_done: 1 }, { library_id: "l2", name: null, ... }, { library_id: "l3", name: "Fiqh", ... }]`.
- **Steps:**
  1. Open the page; sort by "Library".
- **Expected:** empty or `null` name rows show "No library"; rows have unique keys; sorting by name works and puts them together. (currently unhandled — likely bug: `r.name.toLocaleLowerCase()` in `libValue` and `a.name.localeCompare` in the sort tie-break throw on `null`; the default hours sort also hits the tie-break when hours are equal.)

### TRN-09 — Empty lists and zero totals

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Partial — `Transcription.test.tsx` "explains an unknown ETA and empty sections"
- **Preconditions / data:** `totals` all 0; `libraries: []`; `failures: []`; `stages: []`; `eta: { seconds: null, at: null, basis: null }`; `rate.last_10m` and `last_1h` with `jobs: 0`, `realtime_factor: 0`, `avg_job_rtf: null`.
- **Steps:**
  1. Open the page.
- **Expected:** "0%" with "0 h of 0 h" (no NaN); "Not known yet: nothing was transcribed in the last hour."; "No libraries yet."; "No open failures."; stage cards with zeros; no "× per video" part.

### TRN-10 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** app routes; start at `/queue`; summary → `{ ...summaryFixture, failures: null }` (TRN-06).
- **Steps:**
  1. Click "Transcription" in the nav.
  2. See the fallback; press Back; then click "Library" in the nav.
- **Expected:** route error fallback inside the shell, not blank; Back shows `/queue` without a refresh; nav links work.

### TRN-11 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** no user.
- **Steps:**
  1. Open `/transcription`.
- **Expected:** redirect to `/login` with `state.from = "/transcription"`; no summary request; no SSE subscription.

### TRN-12 — Non-admin user sees the full report

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** user `is_admin: false`; fixture as TRN-01.
- **Steps:**
  1. Open `/transcription`.
- **Expected:** the whole report renders (it has no actions); failure links lead to `/queue/items/video/<id>`. If the API ever answers 403 for non-admins, the load error alert shows (not a blank page).

### TRN-13 — Time left: done, live / 10 min / 1 h basis

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `Transcription.test.tsx` "shows what the workers decode now and times the rest by it", "explains an unknown ETA…"
- **Preconditions / data:** `eta` variants: `{ seconds: 0, at: null, basis: null }`; `{ seconds: 7200, at: "2026-05-18T14:00:00Z", basis: "10m" }`; `basis: "1h"`; `basis: "live"`; `at: null` with seconds > 0.
- **Steps:**
  1. Open the page for each.
- **Expected:** 0 → "Everything is transcribed"; others show the duration, "Done around <date>" when `at` is set, and "At the speed of the last 10 minutes" / "At the speed of the last hour" / "At the speed the workers decode now".

### TRN-14 — Speed: live row only with workers; lost hours

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `Transcription.test.tsx` "shows what the workers decode now…"
- **Preconditions / data:** `rate.live` with `workers: 0`; then `workers: 2, lost_hours: 0.5`; then `lost_hours` missing.
- **Steps:**
  1. Open the page for each.
- **Expected:** `workers: 0` → no "Decoding now" row; 2 workers → "Decoding now" with "… of audio in 10 minutes, 2 workers" and "· 0.5 h redone after a restart or failure"; missing `lost_hours` → no "redone" part.

### TRN-15 — Library table sorts by header and by the select

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `Transcription.test.tsx` "sorts the library table by column"
- **Preconditions / data:** three libraries with different videos, hours and progress.
- **Steps:**
  1. Check the default order; use the "Sort by" select ("Library, ascending"); click the "Videos" header twice.
- **Expected:** default hours descending (`aria-sort="descending"` on Hours, "▼"); the select reorders rows and the matching header gets `aria-sort`; clicking a header toggles direction; ties fall back to name; the select value follows header clicks.

### TRN-16 — Failures: cause, unknown cause, unknown stage, links

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `Transcription.test.tsx` "links failures to the item history page"
- **Preconditions / data:** failures: one with `error_cause: "decode_error"`, `error: "ffmpeg exit 1"`, `library_name: "Fiqh"`, `attempts: 3`; one with `error_cause: null`, `error: null`, `library_name: null`, `finished_at: null`; one with `stage: "diarize"` (not one of the three).
- **Steps:**
  1. Open the page.
- **Expected:** first: link to `/queue/items/video/<id>`, chip "Transcribe", "decode_error · ffmpeg exit 1", "Fiqh · attempt 3 · <ago>"; second: "unknown cause" and no empty separators; third: chip shows "diarize", not the raw key `transcription.stage.diarize` (currently unhandled — likely bug: no fallback for unknown stages).

### TRN-17 — Live refetch only when transcription queues move

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Transcription.test.tsx` "refreshes when the transcription queues move on the snapshot stream"
- **Preconditions / data:** fake timers; SSE `queues.snapshot` events built with the test's `snapshot(n)` helper.
- **Steps:**
  1. Emit a first snapshot, one where only other stages change, then transcribe pending changes, then a burst of two changes.
- **Expected:** first snapshot sets the baseline (no fetch); other stages do not refetch; a transcribe change refetches at once; a burst within 5 s gives one trailing fetch.

### TRN-18 — Stream silent: polls every 5 s with a note; stops on event

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes — `Transcription.test.tsx` "polls every 5 s once the stream has been silent for 10 s"
- **Preconditions / data:** fake timers; no SSE events.
- **Steps:**
  1. Advance 9 s, then 7 s; then emit a snapshot; advance 6 s.
- **Expected:** no extra call before 10 s; then one poll and "Live updates paused, refreshing every 5 s"; after the snapshot "Live" and no more polls.

### TRN-19 — Malformed SSE snapshot does not crash or refetch

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Partial — `Transcription.test.tsx` `stagesKey` unit test
- **Preconditions / data:** emit `{ type: "queues.snapshot", at: "x", payload: { queues: null } }`, then `payload: {}`, then an event with no `payload`, then `{ type: "other" }`, then `payload.queues[0].counts: null`.
- **Steps:**
  1. Emit each after the baseline snapshot.
- **Expected:** no throw; events without `payload` or of another type are ignored; an empty key counts as a change at most once (one refetch, not one per event). (currently unhandled — likely bug: `q.counts.pending` in `stagesKey` throws when `counts` is `null`, inside the SSE listener.)

### TRN-20 — Leaving the page stops polling and the stream

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; page in polling mode (TRN-18); then unmount (navigate to `/queue`).
- **Steps:**
  1. Navigate away; advance 20 s.
- **Expected:** no more `/api/transcription/summary` calls; the `queues` SSE listener is removed (`hub` count 0 for this page); a late response does not cause a React state warning.

### TRN-21 — Links: stage cards, failures, nav; Back / Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial — `Transcription.test.tsx` checks `href="/queue/transcribe"` and failure hrefs
- **Preconditions / data:** app routes; fixtures for `/queue/transcribe` and an item page.
- **Steps:**
  1. Click "Transcribe" → `/queue/transcribe`; Back.
  2. Click a failure title → `/queue/items/video/<id>`; Back; Forward.
- **Expected:** each target renders; Back returns to `/transcription` and the report is shown again (refetched or from state), no stuck "Loading…".

### TRN-22 — Phone width 375 px; reached through "More"

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e 375×812.
- **Steps:**
  1. Open "More" in the bottom tab bar; tap "Transcription".
  2. Scroll the page.
- **Expected:** the page opens from `/more`; no horizontal scroll; cards stack; the library table switches to its stacked layout (cells carry `data-label`) or scrolls inside its wrapper; the sort select fits.

### TRN-23 — Arabic: RTL, Arabic numbers and names

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** Yes — `Transcription.test.tsx` "renders in Arabic, right to left"
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; an Arabic library name and failure title.
- **Steps:**
  1. Open the page.
- **Expected:** `<html dir="rtl">`; `h1` "التفريغ النصي"; stage link "التفريغ" → `/queue/transcribe`; "آخر الإخفاقات"; error text "تعذّر تحميل تقرير التفريغ النصي." in the error state; progress bars fill from the right; names isolated.

### TRN-24 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light / dark.
- **Steps:**
  1. Open the page in both.
- **Expected:** no inline colour styles; the live dot "off" state, failed counters and progress bars readable in both themes.

### TRN-25 — Keyboard and accessibility basics

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** Partial — `Transcription.test.tsx` "has no axe violations"
- **Preconditions / data:** fixture as TRN-01.
- **Steps:**
  1. Tab through stage links, the sort select, header sort buttons, failure links.
- **Expected:** focus visible; sort buttons announce state through `aria-sort` on the `th`; the "▲/▼" glyph is `aria-hidden`; progress bars have names ("Audio hours transcribed", "<name>: hours transcribed"); the live status is `role="status"`; the error is `role="alert"`; axe clean in the error state too.
