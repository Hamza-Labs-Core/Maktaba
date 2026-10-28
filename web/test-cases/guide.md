# TV Guide (EPG)

- **Route:** `/guide` (no params; no query-string state)
- **Component:** `src/pages/LiveTV/Guide.tsx`; children in the same file: `WhatsOnNowList` (compact "What's on now" cards), `ChannelLogo`; design system `Modal`, `EmptyState`, `ErrorState`, `ProgressBar`, `Card`, `Badge`, `Button`.
- **API calls:** `GET /api/channels/guide?start=<iso>&end=<iso>[&category=<c>]` via `channelsApi.guide()` in `src/lib/channels.ts`. The wire shape is `{ server_time?, channels?: [{ channel: {id, number, name, slug?, logo?, logo_url?}, blocks?: [{channel_id, kind, start, stop, title, sub_title?, desc?, poster?, series?, season?, episode?}] }] }`; `guideFrom()` maps it to `GuideResponse { server_time, channels: [{ channel: Channel, programs: Program[] }] }` (`channels ?? []`, `blocks ?? []`). Window is now − 30 min to now + 90 min (two hours), spread across the full width beside the channel column; programmes are clipped to it. Re-fetched every 30 s (`REFRESH_MS`); a 1 s tick moves the now-line and progress without a refetch.
- **Access:** any signed-in user (`RequireAuth` in `src/lib/auth.tsx` around `AppShell`); not admin-gated. Signed out → `/login`.
- **Existing tests:** `src/pages/LiveTV/Guide.test.tsx` (mocks `api.get` from `src/lib/api`, so `channels.ts` mapping runs for real) — covers: proportional cell widths (30 vs 120 min = 1:4), logo shown and hidden on `error`, now-line present, airing cell click navigates to `/live/1`, future cell opens details without tuning, What's On Now toggle shows the current title, empty `channels: []` shows "No channels yet" + "Create a channel". Gaps: error/loading states, null and missing lists, filler collapse, category filter, mobile default, refresh/stale behaviour, server clock offset, keyboard/a11y, RTL, theme, crash recovery.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test?                                                               |
| ------ | ----------------------------------------------------------- | ------------- | -------- | --------------------------------------------------------------------------------------- |
| GDE-01 | Grid renders one row per channel, cells sized by duration   | Happy path    | P1       | Yes (`Guide.test.tsx`)                                                                  |
| GDE-02 | Now-line is drawn at the current time                       | Happy path    | P2       | Yes (`Guide.test.tsx`)                                                                  |
| GDE-03 | Clicking the airing cell tunes the channel                  | Happy path    | P1       | Yes (`Guide.test.tsx`)                                                                  |
| GDE-04 | Clicking a future cell opens the details dialog             | Happy path    | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-05 | Filler runs collapse into one cell with the filler title    | Happy path    | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-06 | Channel logo shows, and hides when it fails to load         | Happy path    | P3       | Yes (`Guide.test.tsx`)                                                                  |
| GDE-07 | What's on now view lists current and next, tapping tunes    | Happy path    | P1       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-08 | Compact view is the default on a phone                      | Responsive    | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-09 | No channels shows the empty state with a working CTA        | Empty state   | P2       | Yes (`Guide.cases.test.tsx`; CTA now admin-only)                                        |
| GDE-10 | `channels` / `blocks` null or missing render empty states   | Empty state   | P1       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-11 | A channel entry with a null `channel` header                | Error         | P1       | Yes (`Guide.cases.test.tsx`: error state); skipping the bad entry is No (it.fails: bug) |
| GDE-12 | Loading text before the first response                      | Loading       | P3       | No                                                                                      |
| GDE-13 | First load fails (500 / 401 / 403) shows the error state    | Error         | P1       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-14 | Network failure and malformed JSON show the error state     | Error         | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-15 | A failed refresh keeps the last grid                        | Error         | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-16 | Auto refresh every 30 s and 1 s now-line tick               | Happy path    | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-17 | Now-line follows `server_time`, not the device clock        | Happy path    | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-18 | Category filter lists categories and refetches              | Happy path    | P2       | No (it.fails: bug; refetch path covered in `Guide.cases.test.tsx`)                      |
| GDE-19 | Bad timestamps in a block do not break the grid             | Error         | P3       | Yes (`Guide.cases.test.tsx`; NaN width fixed)                                           |
| GDE-20 | Signed-out user is sent to `/login`; normal user sees guide | Permissions   | P1       | Yes (`Guide.cases.test.tsx`: a, b; c is RequireAuth)                                    |
| GDE-21 | Render crash shows the route error fallback; Back works     | Error         | P1       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-22 | Back from the live player returns to the guide              | Navigation    | P2       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-23 | Phone width: the grid fits the screen, nothing scrolls      | Responsive    | P3       | No (e2e-only)                                                                           |
| GDE-24 | Arabic: RTL layout, Arabic strings, time axis mirrored      | RTL           | P2       | Yes (`Guide.cases.test.tsx`; visual mirroring e2e-only)                                 |
| GDE-25 | Light and dark theme keep cells readable                    | Theme         | P3       | No (e2e-only)                                                                           |
| GDE-26 | Keyboard: cells, filter and toggle reachable; dialog traps  | Accessibility | P2       | Yes (`Guide.cases.test.tsx`; focus ring e2e-only)                                       |
| GDE-27 | Programmes are cut at both window edges                     | Layout        | P1       | Yes (`Guide.cases.test.tsx`)                                                            |
| GDE-28 | The two hours fill the width, with half-hour ticks          | Layout        | P1       | Yes (`Guide.cases.test.tsx`; real widths e2e-only)                                      |

## Cases

### GDE-01 — Grid renders one row per channel, cells sized by duration

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Guide.test.tsx` "sizes cells proportional to duration"
- **Preconditions / data:** signed in; `GET /api/channels/guide?...` → `guidePayload()` from the test: one channel `{id:"c1", number:1, name:"Movies"}` with blocks "Now Playing" (−10 min → +20 min) and "Later Show" (+20 min → +140 min).
- **Steps:**
  1. Open `/guide`.
- **Expected:** Heading "TV Guide". `data-testid="guide-grid"` present. Row shows number `1` and name "Movies". Two `data-testid="guide-cell"` buttons; widths are % of the two-hour window, ratio 1:4 for a 30-min vs a 120-min programme (the test puts them on two rows with a frozen clock so neither is cut). The airing cell has class `mkt-guide__cell--airing` and a progress bar span.

### GDE-02 — Now-line is drawn at the current time

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `Guide.test.tsx` "renders a now-line"
- **Preconditions / data:** as GDE-01.
- **Steps:**
  1. Open `/guide`.
- **Expected:** `data-testid="now-line"` exists with `inset-inline-start: 25%` (30 of the 120 min). It is `aria-hidden="true"`.

### GDE-03 — Clicking the airing cell tunes the channel

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Guide.test.tsx` "tunes when the airing cell is clicked"
- **Preconditions / data:** as GDE-01; route `/live/:number` mounted.
- **Steps:**
  1. Open `/guide`.
  2. Click the cell "Now Playing".
- **Expected:** URL becomes `/live/1`. No details dialog opens.

### GDE-04 — Clicking a future cell opens the details dialog

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** as GDE-01, but give "Later Show" `desc:"A long story"`, `poster:"/p.jpg"`, `series:"Seerah"`, `season:1`, `episode:3`, `sub_title:"Badr"`.
- **Steps:**
  1. Open `/guide`, click "Later Show".
  2. Press Escape.
- **Expected:** `role="dialog"` titled "Later Show" shows "Starts at" with a formatted date, "Series" with "Seerah · S1E3 · Badr", "Duration" "120 min", the description, the poster `<img alt="">` and a badge "program". URL stays `/guide`. Escape closes the dialog and focus returns to the cell.

### GDE-05 — Filler runs collapse into one cell titled as the API says

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** one channel with blocks: program (−10 → +20 min), `kind:"filler"` titled «فواصل» (+20 → +25), `kind:"bumper"` (+25 → +27), `kind:"station_id"` (+27 → +30), program (+30 → +90). Also a second channel whose airing block is `kind:"filler"` with no title.
- **Steps:**
  1. Open `/guide`.
  2. Click the filler cell.
  3. On the second channel, click its airing filler cell.
- **Expected:** First channel shows 3 cells, the middle one labelled «فواصل» (the API's filler title, rules `tv.guide_filler_title`; "Up next" when a filler block has no title) with class `mkt-guide__cell--filler` and width 40 px (10 min). Clicking it opens details, it does not tune. The airing filler on the second channel also opens details instead of navigating to `/live/<n>`.

### GDE-06 — Channel logo shows, and hides when it fails to load

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Yes — `Guide.test.tsx` "shows channel logos and hides a logo that fails to load"
- **Preconditions / data:** channel header with `logo_url:"/api/channels/c1/logo"`; a second channel with only `logo:"https://cdn.example/x.png"`; a third with `logo:"local/path.png"`.
- **Steps:**
  1. Open `/guide`; fire `error` on the first logo.
- **Expected:** First logo `src="/api/channels/c1/logo"` then removed after error; number and name stay. Second channel shows the https logo. Third shows no `<img>` (non-URL path).

### GDE-07 — What's on now view lists current and next, tapping tunes

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** as GDE-01 plus a second channel `number:2` with no airing block and one future block "Tonight".
- **Steps:**
  1. Open `/guide`, click "What's on now" (button has `aria-pressed="false"`).
  2. Click the card for channel 1.
- **Expected:** `data-testid="whats-on-now"` shows a card per channel. Channel 1: "Now Playing", a progress bar labelled "Duration", "Next: Later Show". Channel 2: "No content", no progress bar, "Next: Tonight". The toggle now reads "Grid view" with `aria-pressed="true"`. Clicking card 1 navigates to `/live/1` (even channel 2's card tunes, since the whole card is the button).

### GDE-08 — Compact view is the default on a phone

- **Priority:** P2
- **Category:** Responsive
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** stub `window.matchMedia("(max-width: 640px)")` → `matches: true`; payload as GDE-01. Second run: `window.matchMedia` undefined.
- **Steps:**
  1. Open `/guide`.
- **Expected:** With the phone match, `whats-on-now` renders first and the toggle says "Grid view". With `matchMedia` missing, the grid renders and nothing throws.

### GDE-09 — No channels shows the empty state with a working CTA

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`; CTA now admin-only)
- **Preconditions / data:** `GET /api/channels/guide` → `{ "server_time": "<now>", "channels": [] }`.
- **Steps:**
  1. Open `/guide` (both grid and compact mode).
  2. Click "Create a channel".
- **Expected:** "No channels yet" / "There are no live channels to show in the guide." in both modes; the category select shows only "All categories". Click navigates to `/admin/channels`. As a non-admin that page shows "Admins only" — consider hiding the CTA for non-admins (currently shown to everyone — likely bug, UX).

### GDE-10 — `channels` / `blocks` null or missing render empty states

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** run once per payload for `GET /api/channels/guide`:
  - `{ "server_time": "<now>", "channels": null }`
  - `{ "server_time": "<now>" }` (key missing)
  - `{}` (no `server_time` either)
  - `{ "channels": [{ "channel": {"id":"c1","number":1,"name":"Movies"}, "blocks": null }] }`
  - `{ "channels": [{ "channel": {"id":"c1","number":1,"name":"Movies"} }] }` (`blocks` missing)
- **Steps:**
  1. Open `/guide`; for the last two also switch to "What's on now".
- **Expected:** First three: "No channels yet" empty state, no thrown error, no blank page. Last two: the row "1 Movies" renders with one full-width cell "No content"; the compact card shows "No content" and no "Next" line. Fields: `channels`, `channels[].blocks`.

### GDE-11 — A channel entry with a null `channel` header

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`: error state); skipping the bad entry is No (it.fails: bug)
- **Preconditions / data:** `GET /api/channels/guide` → `{ "server_time":"<now>", "channels": [ null ] }` and `{ "channels": [ { "channel": null, "blocks": [] } ] }`.
- **Steps:**
  1. Open `/guide`.
- **Expected:** No blank page. Today `fromHeader()` throws inside `guideFrom`, the promise rejects and the page shows the error state "Something went wrong." / "Could not load the guide." Preferred: skip the bad entry and render the other channels (currently one bad entry hides the whole guide — likely bug).

### GDE-12 — Loading text before the first response

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `api.get` returns a promise that never resolves.
- **Steps:**
  1. Open `/guide`.
- **Expected:** "Loading…" (`common.loading`) is shown with class `mkt-loading`. It should be announced (`role="status"`); currently a plain `<p>` with no role (a11y gap).

### GDE-13 — First load fails (500 / 401 / 403) shows the error state

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** `api.get` rejects with `new ApiError({type:"about:blank", title:"Internal Server Error", status:500})`; repeat with status 401 and 403.
- **Steps:**
  1. Open `/guide`.
- **Expected:** `ErrorState` with title "Something went wrong." and description "Could not load the guide." No toolbar, no grid. There is no "Try again" button; the 30 s refresh retries silently and the grid appears once a later call succeeds (assert with fake timers). Adding a retry button would help (gap).

### GDE-14 — Network failure and malformed JSON show the error state

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** (a) stub `fetch` to reject with `TypeError("Failed to fetch")`; (b) stub `fetch` to return 200 with `Content-Type: application/json` and body `{"channels": [` (do not mock `api`).
- **Steps:**
  1. Open `/guide`.
- **Expected:** Both show the same error state as GDE-13. No uncaught promise rejection. `console.error` logs `[api] GET /api/channels/guide?... failed (0): ...` for (a).

### GDE-15 — A failed refresh keeps the last grid

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** fake timers; first `GET` → GDE-01 payload; second → reject 500.
- **Steps:**
  1. Open `/guide`, wait for the grid.
  2. Advance 30 s.
- **Expected:** The grid stays with the last data (no error page, no blank). There is no stale-data hint today (gap: consider a small "could not refresh" note).

### GDE-16 — Auto refresh every 30 s and 1 s now-line tick

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** fake timers; payload as GDE-01, airing block ends at now + 2 s.
- **Steps:**
  1. Open `/guide`; count `api.get` calls.
  2. Advance 3 s, then 30 s.
  3. Unmount.
- **Expected:** After 3 s the first cell loses `mkt-guide__cell--airing` without a new request. After 30 s exactly one more `GET /api/channels/guide` with fresh `start`/`end`. After unmount no more calls and no React state-update warnings.

### GDE-17 — Now-line follows `server_time`, not the device clock

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** `server_time` = device now + 60 min; blocks: A (now → now + 30 min), B (now + 50 → now + 90 min). Second run: `server_time: "not-a-date"`.
- **Steps:**
  1. Open `/guide`.
- **Expected:** B is marked airing, A is not; clicking B tunes. With an unparseable `server_time` the device clock is used (A airing) and nothing throws.

### GDE-18 — Category filter lists categories and refetches

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No (it.fails: bug; refetch path covered in `Guide.cases.test.tsx`)
- **Preconditions / data:** two channels whose wire headers carry `category:"movies"` and `category:"kids"`.
- **Steps:**
  1. Open `/guide`; open the "Category" select.
  2. Choose "kids".
- **Expected:** Options "All categories", "kids", "movies" (sorted). Choosing "kids" calls `GET /api/channels/guide?start=...&end=...&category=kids` and restarts the 30 s timer. Note: `fromHeader()` in `src/lib/channels.ts` does not copy `category` (and `WireHeader` has no such field), so today the select only ever has "All categories" (currently unhandled — likely bug). Choosing a category is also not kept in the URL, so Back/refresh resets it.

### GDE-19 — Bad timestamps in a block do not break the grid

- **Priority:** P3
- **Category:** Error
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`; NaN width fixed)
- **Preconditions / data:** a block with `start:"bad"`, `stop:"bad"`; a block with `stop` before `start`.
- **Steps:**
  1. Open `/guide`.
- **Expected:** Page renders; bad blocks are skipped or drawn at the minimum 2 px width, never with `width: NaN`. No progress bar for them (`progressFraction` returns null). Today the width becomes `NaN` (style dropped) — currently unhandled, minor.

### GDE-20 — Signed-out user is sent to `/login`; normal user sees guide

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`: a, b; c is RequireAuth)
- **Preconditions / data:** full `App` routes. (a) `useAuth` → `{user:null, loading:false}`; (b) `{user:{username:"u", is_admin:false}}`; (c) `{loading:true}`.
- **Steps:**
  1. Navigate to `/guide`.
- **Expected:** (a) redirect to `/login` with `state.from="/guide"`, no guide request. (b) guide renders normally (not admin-gated). (c) "Loading…" with `role="status"`.

### GDE-21 — Render crash shows the route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** full `App` with a `MemoryRouter` history `["/library", "/guide"]`. Mock `channelsApi.guide` directly (bypassing `guideFrom`) to resolve `{ server_time:"<now>", channels:[{ channel:{id:"c1",number:1,name:"X"}, programs:null }] }` so `collapseFiller(null)` throws during render.
- **Steps:**
  1. Render at `/guide`.
  2. Press browser Back (`history.back()` / `navigate(-1)`).
  3. Repeat, then click a nav link (e.g. "Library") instead of Back.
- **Expected:** A route-level error fallback (from the error boundary in `src/components/AppShell.tsx`) is shown, not a blank page; app nav stays visible. Back shows `/library` content without a reload; clicking a nav link renders that page.

### GDE-22 — Back from the live player returns to the guide

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`)
- **Preconditions / data:** routes `/guide` and `/live/:number`; payload as GDE-01.
- **Steps:**
  1. Open `/guide`, click "Now Playing".
  2. Press Back.
  3. Deep link `/guide?foo=1`.
- **Expected:** Back renders the guide again (fresh fetch) with the grid. Unknown query params are ignored.

### GDE-23 — Phone width: the grid fits the screen, nothing scrolls

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** viewport 375 × 812, grid view forced (click "Grid view").
- **Steps:**
  1. Open `/guide`; try to scroll the grid sideways.
- **Expected:** `document.documentElement.scrollWidth <= 375`. `.mkt-guide__scroll` does not scroll: the two hours fit beside a 96 px channel column (number and name, no logo), with hour labels only; channel heads stay readable. Toolbar wraps; the select and toggle are reachable. App nav reachable.

### GDE-24 — Arabic: RTL layout, Arabic strings, time axis mirrored

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`; visual mirroring e2e-only)
- **Preconditions / data:** `localStorage["mkt:locale"]="ar"`; payload as GDE-01 with an Arabic channel name "قناة القرآن" and title "تلاوة".
- **Steps:**
  1. Open `/guide`; toggle compact view; open a details dialog.
- **Expected:** `<html dir="rtl" lang="ar">`. Heading "دليل القنوات"; empty title (with `channels: []`) "لا توجد قنوات بعد". Cells use `inset-inline-start` so time runs right to left and channel heads sit on the right; the second now-line (past the channel column, 25% of the rest) lines up with the axis now-line in RTL. Arabic titles render unclipped.

### GDE-25 — Light and dark theme keep cells readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** `<html data-theme="light">` then `"dark"`.
- **Steps:**
  1. Open `/guide` in grid and compact views.
- **Expected:** Airing, filler and empty cells, the now-line and progress bars stay visible with readable text in both themes; no hardcoded colours in the guide CSS (only tokens).

### GDE-26 — Keyboard: cells, filter and toggle reachable; dialog traps

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Guide.cases.test.tsx`; focus ring e2e-only)
- **Preconditions / data:** payload as GDE-01.
- **Steps:**
  1. Tab from the page start through the toolbar and cells.
  2. Press Enter on the future cell; Tab inside the dialog; press Escape.
- **Expected:** Toolbar has `role="toolbar"` and name "Guide controls"; the select has name "Category"; the toggle exposes `aria-pressed`. Every cell is a `<button>` with an accessible name (its title, or "Up next" for filler) and a visible focus ring. Enter on the airing cell tunes. The dialog traps focus, Escape closes it and focus returns to the cell. The now-lines and progress spans are `aria-hidden`.

### GDE-27 — Programmes are cut at both window edges

- **Priority:** P1
- **Category:** Layout
- **Covered by existing test?** Yes — `Guide.cases.test.tsx`
- **Preconditions / data:** clock frozen; one channel with "Gone" (−100 → −40 min), "Early" (−60 → +10), "Middle" (+10 → +60), "Late" (+60 → +150), "Far" (+100 → +130).
- **Steps:**
  1. Open `/guide`.
- **Expected:** Only "Early", "Middle" and "Late" render. "Early" starts at 0 % with its 40 visible minutes, class `mkt-guide__cell--cut-start` and a start chevron (`aria-hidden`); its title still reads and is its accessible name; its progress bar covers 30 of the 40 visible minutes, ending on the now-line. "Late" starts at 75 %, is 25 % wide, has class `mkt-guide__cell--cut-end` and an end chevron. Every cell ends at or before 100 %; the now-line is at 25 %. Under RTL the chevrons mirror and point out of the window.

### GDE-28 — The two hours fill the width, with half-hour ticks

- **Priority:** P1
- **Category:** Layout
- **Covered by existing test?** Yes — `Guide.cases.test.tsx` (real widths e2e-only)
- **Preconditions / data:** clock frozen at 10:10 local (window 09:40 → 11:40); a channel with "Whole" (−30 → +90 min) and an empty channel.
- **Steps:**
  1. Open `/guide` at desktop and phone width.
- **Expected:** No pixel widths in the grid's inline styles; "Whole" is 0 % → 100 %, uncut; the empty row's "No content" is 100 %. Ticks at 10:00, 10:30, 11:00, 11:30 (16.7 %, 41.7 %, 66.7 %, 91.7 %), the half-hours marked `mkt-guide__tick--half` (labels hidden on a phone). A tick within 8 min of the window's end is left out. In a browser the window spans from the channel column to the guide's edge at any width, and the page never scrolls sideways.
