# Admin Live TV changes

- **Route:** `/admin/live-tv/changes` (no params; the filter is React state only, not in the query string)
- **Component:** `src/pages/Admin/LiveTV/Changes.tsx` (`AdminTvChanges` → `ChangesInner`, in-file `ChangeTable`, `SettingsPanel`); children `SlotBar.tsx`, `HeldReleaseCard.tsx`, shared bits in `changeBits.tsx` (`StateChip`, `Kinds`, `ChecksSummary`, `Clock`, `userName`, `sourceLabel`, `actionError`, `useVisiblePoll`, `CHANGES_POLL_MS = 2000`).
- **API calls:** every 2 s while visible (`useVisiblePoll`): `GET /api/tv/changes?limit=100` → `ChangeList {items: TvChange[], in_flight?, config: StagingConfig, users: Record<id,name>}` via `tvChangesApi.list()`; `GET /api/tv/slots` → `SlotsView {runtime, slots: TvSlot[]}` (failure ignored); `GET /api/tv/releases?limit=20` → `{items: Release[]}` (failure ignored) — all in `src/lib/tvChanges.ts`. Once on mount: `GET /api/cluster/updates` → `UpdatesOverview` via `updatesApi.overview()`; `POST /api/cluster/updates/run` `{version}` and `POST /api/cluster/updates/skip` `{version}` from the held-release card (`src/lib/updatesApi.ts`). Updates are reconciled (`reconcile`, `reconcileList`) so unchanged rows keep their objects.
- **Access:** admin only via `AdminGate` (`common.forbidden.*`); signed out → `/login`.
- **Existing tests:** `src/pages/Admin/LiveTV/Changes.test.tsx` (mocks `api.get` with `answer()` from `changeFixtures.ts`) — covers: list columns newest first, filters + counts, slot bar + in-flight banner + settings panel, staging off (EC2) keeps release history, 2 s poll keeps unchanged rows, `reconcileList`, Arabic RTL, every key in en + ar. `HeldReleaseCard.test.tsx` — held release approve (Update now), skip, nothing held. Gaps: non-admin, load errors, null/missing arrays, slots/releases failures, empty states, links/back, crash recovery, responsive, theme, a11y details.

## Summary

| ID     | Title                                                         | Category      | Priority | Covered by existing test?                                           |
| ------ | ------------------------------------------------------------- | ------------- | -------- | ------------------------------------------------------------------- |
| LTC-01 | Lists changes with state, kinds, source, author, checks       | Happy path    | P1       | Yes (`Changes.test.tsx`)                                            |
| LTC-02 | Filters with counts                                           | Happy path    | P2       | Yes (`Changes.test.tsx`)                                            |
| LTC-03 | Empty list and empty filters                                  | Empty state   | P2       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-04 | `/api/tv/changes` arrays and objects null or missing          | Empty state   | P1       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-05 | `/api/tv/slots`, `/api/tv/releases`, updates null or missing  | Empty state   | P1       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-06 | Slots or releases endpoint fails                              | Error         | P2       | No (it.fails: bug)                                                  |
| LTC-07 | Changes list fails on first load                              | Error         | P1       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-08 | Changes list fails after a good load                          | Error         | P2       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-09 | Skeletons show on the first load only                         | Loading       | P3       | Partial (`Changes.test.tsx`: poll keeps rows, skeleton not checked) |
| LTC-10 | In-flight banner links to the change                          | Navigation    | P2       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-11 | Slot bar: hosts, roles, staging slot progress, single runtime | Happy path    | P2       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-12 | Staging off banner hides the list                             | Happy path    | P2       | Yes (`Changes.test.tsx`)                                            |
| LTC-13 | Staging settings panel                                        | Happy path    | P3       | Yes (`Changes.test.tsx`)                                            |
| LTC-14 | Polls every 2 s only while visible; stops on leave            | Happy path    | P2       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-15 | Held release: update now, skip, errors                        | Happy path    | P2       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-16 | Non-admin sees "Admins only" and nothing polls                | Permissions   | P1       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-17 | Signed-out user is sent to `/login`                           | Permissions   | P1       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-18 | Render crash shows the route error fallback; Back works       | Error         | P1       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-19 | Links and Back: Live TV, change detail, settings              | Navigation    | P2       | Yes (`Changes.cases.test.tsx`)                                      |
| LTC-20 | Phone width: table and slot bar fit                           | Responsive    | P3       | No (e2e-only)                                                       |
| LTC-21 | Arabic: RTL and Arabic strings                                | RTL           | P2       | Yes (`Changes.test.tsx`)                                            |
| LTC-22 | Light and dark theme                                          | Theme         | P3       | No (e2e-only)                                                       |
| LTC-23 | Keyboard and screen reader basics                             | Accessibility | P2       | Yes (`Changes.cases.test.tsx`)                                      |

## Cases

Shared data: `changeList`, `slots`, `releases` and `answer(path)` from `src/pages/Admin/LiveTV/changeFixtures.ts`; admin `{id:"aaaaaaaa-...", username:"mahmoud", is_admin:true}`.

### LTC-01 — Lists changes with state, kinds, source, author, checks

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Changes.test.tsx` "lists the changes with state, kinds, source, author and checks, newest first"
- **Preconditions / data:** `answer()` fixtures.
- **Steps:**
  1. Open `/admin/live-tv/changes`.
- **Expected:** Heading "TV changes"; `role="table"` named "TV changes" with header cells "#", change, state, kinds, source, checks, swap. One `data-testid="change-<number>"` row per item, newest first; title is a link (or "Untitled change"); "by <name> at <date>" using `users` (unknown id → first 8 chars, none → system); state chip; checks "N pass · N warn · N fail" or "—"; swap clock or "—".

### LTC-02 — Filters with counts

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `Changes.test.tsx` "filters by state group, the counts beside each filter"
- **Preconditions / data:** fixtures with drafts, in-flight, live and ended changes.
- **Steps:**
  1. Click "Drafts", "In flight", "Live", "Ended", "All".
- **Expected:** Buttons in that fixed order inside `role="group"` named "Show"; the active one has `aria-pressed="true"`; counts match; rows filter (`staged|verifying|ready|approved|swapping` = in flight; `superseded|failed|cancelled|rolled_back` = ended).

### LTC-03 — Empty list and empty filters

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** (a) `GET /api/tv/changes` → `{items:[], config:{...enabled:true}, users:{}}`; (b) fixtures with no drafts.
- **Steps:**
  1. Open the page; click each filter.
- **Expected:** (a) "No TV changes yet.", "No change is in flight." banner, every count 0. (b) "Drafts" shows "No drafts."; other empty filters show "No change is in flight.", "No change is live.", "No ended changes."

### LTC-04 — `/api/tv/changes` arrays and objects null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** `GET /api/tv/changes` → fixture with one change per run:
  - `items: null` and `items` missing (guarded `l.items ?? []`)
  - `users: null` and missing (guarded in `userName`)
  - `config: null` and missing (no settings panel, no staging-off banner)
  - `config.auto_promote: null` (guarded)
  - `items[0].kinds: null` and missing (`changeBits.tsx:54` `kinds.length` unguarded)
  - `items[0].verify_report: null`, and `verify_report.checks: null` (guarded)
  - `items[0].source: null` (guarded → "manual")
  - `items[0].state: "weird"` (unknown state)
  - `in_flight` set to an id not in `items`
  - body `null` (200 with JSON `null`)
- **Steps:**
  1. Open the page.
- **Expected:** No blank page. `items` null/missing → "No TV changes yet." `users` null → names fall back to id prefix. `config` null → list still shows, no settings panel. `kinds` null → "—" in the kinds cell. Checks "—". Unknown state → neutral chip with a fallback label (no throw); it is counted only under "All". Unknown `in_flight` → "No change is in flight." JSON `null` body → load error message, not a crash. Today `kinds: null` throws during render (currently unhandled — likely bug).

### LTC-05 — `/api/tv/slots`, `/api/tv/releases`, updates null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** one change per run:
  - `GET /api/tv/slots` → `{runtime:"slots", slots:null}`, `{}` (guarded `view?.slots ?? []`)
  - a slot with `release_id` missing and `version` missing
  - `GET /api/tv/releases` → `{items:null}`, `{}` (guarded)
  - a release with `kinds: null` (`Changes.tsx:207` → `Kinds` crash) and `streaming_version: ""`
  - `GET /api/cluster/updates` → `{plan:null}`, `{}`, `{plan:{held:true, target:null}}`
- **Steps:**
  1. Open the page.
- **Expected:** Slots null → empty slot bar (no skeleton forever once a response arrived); slot without release → "no release" text. Releases null → "No releases yet." Release `kinds` null → "—", not a crash (today it throws — likely bug). `streaming_version` empty → "—". Updates null fields → no held card, page fine.

### LTC-06 — Slots or releases endpoint fails

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No (it.fails: bug) — `Changes.cases.test.tsx`
- **Preconditions / data:** `GET /api/tv/slots` rejects 500; `GET /api/tv/releases` rejects 404; list fine.
- **Steps:**
  1. Open the page; wait for 2 polls.
- **Expected:** The list renders. Today the slot bar and the releases card keep their skeletons forever and say nothing because the failures are swallowed (`.catch(() => null)`) — should show a short "not available" note (currently unhandled — likely gap). No load-error alert for these.

### LTC-07 — Changes list fails on first load

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** `GET /api/tv/changes` rejects: `ApiError` 500 "Internal Server Error"; 403; 401; network `TypeError("Failed to fetch")`; 200 malformed JSON; `ApiError` 409 `{state:"swapping"}`; 501.
- **Steps:**
  1. Open the page; advance 2 s with the next call succeeding.
- **Expected:** `role="alert"` "Could not load the changes: <message>" inside the `aria-live="polite"` area (409 with state → "Not possible now: the change is Swapping."; 501 → "Not available yet."). Filters show counts 0 with the list skeleton. When the next poll succeeds the alert clears and the rows appear.

### LTC-08 — Changes list fails after a good load

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** fake timers; first read fixtures, second rejects 500 "boom".
- **Steps:**
  1. Open the page; advance 2 s.
- **Expected:** Rows stay as they were (no flicker, no skeleton) and the alert "Could not load the changes: boom" shows above; it clears on the next good read.

### LTC-09 — Skeletons show on the first load only

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** Partial — `Changes.test.tsx` "re-reads every 2 s and keeps unchanged rows in place"
- **Preconditions / data:** first `GET /api/tv/changes` pending; later reads resolve slowly (200 ms).
- **Steps:**
  1. Open the page; resolve; advance 2 s.
- **Expected:** First load: `.mkt-tvx-skeleton` (`aria-hidden="true"`) in the list, slots and releases; banner area blank. Later polls never bring the skeleton back; layout height stays stable.

### LTC-10 — In-flight banner links to the change

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** `in_flight` = id of change #7 (state `verifying`, title "Ramadan lineup"); second run with title "".
- **Steps:**
  1. Click `data-testid="in-flight"`.
- **Expected:** Text "Change #7 is Verifying: Ramadan lineup" (or "...: Untitled change"); navigates to `/admin/live-tv/changes/<id>`.

### LTC-11 — Slot bar: hosts, roles, staging slot progress, single runtime

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** `slots` with host `home` (live blue, standby green) and a node; in-flight change `verifying` with 3 checks (1 fail); second run `runtime:"single"`; third run in-flight `draft`.
- **Steps:**
  1. Open the page.
- **Expected:** `role="group"` named by `tvchanges.slots.title`; host "home" shows the translated home label; `data-testid="slot-home-blue"` etc. with role, colour, release id prefix, version, state. `slot-staging` shows the checks text (done 3, fail 1) while verifying, "change #N" otherwise, and is absent for a draft. Single runtime shows the `tvchanges.slots.single` hint.

### LTC-12 — Staging off banner hides the list

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `Changes.test.tsx` "explains staging off and keeps the release history only (EC2)"
- **Preconditions / data:** `config.enabled:false`.
- **Steps:**
  1. Open the page; click "Open settings" (`tvchanges.openSettings`).
- **Expected:** `data-testid="staging-off"` `role="note"` "TV staging is off. …"; no filters or table; releases card stays; link goes to `/settings`.

### LTC-13 — Staging settings panel

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Yes — `Changes.test.tsx` "shows the slot bar, the change in flight and the staging settings"
- **Preconditions / data:** config with `swap_wait:"boundary"`, `boundary_max_wait_minutes:30`, `auto_promote:{lineup:"manual", schedule:"auto"}`.
- **Steps:**
  1. Open `<details data-testid="staging-settings">`.
- **Expected:** Facts list: enabled, swap wait + max wait, quiet window (LTR), second admin, keep warm; auto-promote rules sorted by kind with translated rule names.

### LTC-14 — Polls every 2 s only while visible; stops on leave

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** fake timers.
- **Steps:**
  1. Open; advance 2 s; set `visibilityState="hidden"` and advance 10 s; set visible; navigate away; advance 10 s.
- **Expected:** 3 calls (changes, slots, releases) per tick while visible; none while hidden; one tick at once on show; none after unmount and no state-update warnings. The held-release card reads once only.

### LTC-15 — Held release: update now, skip, errors

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** `GET /api/cluster/updates` → `{plan:{held:true, target:{version:"1.9.0", release_url:"https://..."}}}`; `POST /api/cluster/updates/run` rejects `ApiError` 409 "busy" on one run, a plain `Error` on another; updates endpoint 404.
- **Steps:**
  1. Click approve; click skip.
- **Expected:** `role="region"` with the version in its name; release link opens in a new tab. Failure → `role="alert"` with "busy" (ApiError) or "Something went wrong." (other); buttons re-enabled. 404 → no card. After a success the card reloads and, when nothing is held any more, a `role="status"` note stays.

### LTC-16 — Non-admin sees "Admins only" and nothing polls

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** `useAuth` → `{user:{is_admin:false}}`; fake timers.
- **Steps:**
  1. Open the page; advance 6 s.
- **Expected:** "Admins only" / "You need administrator access to view this page." No request to `/api/tv/changes`, `/api/tv/slots`, `/api/tv/releases` or `/api/cluster/updates`.

### LTC-17 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** full `App`, `useAuth` → `{user:null, loading:false}`.
- **Steps:**
  1. Navigate to `/admin/live-tv/changes`.
- **Expected:** Redirect to `/login` with `state.from="/admin/live-tv/changes"`; no API calls.

### LTC-18 — Render crash shows the route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** full `App`, history `["/admin/live-tv", "/admin/live-tv/changes"]`; `GET /api/tv/changes` → fixtures with `items[0].kinds = null` (throws in `Kinds`), or mock `SlotBar` to throw.
- **Steps:**
  1. Render at `/admin/live-tv/changes`.
  2. Press Back; repeat and click an admin nav link instead.
- **Expected:** Route-level error fallback (boundary in `src/components/AppShell.tsx`), not a blank page. Back renders Live TV without a refresh; the nav link renders its page; the 2 s poll stops.

### LTC-19 — Links and Back: Live TV, change detail, settings

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** fixtures.
- **Steps:**
  1. Click "Back to Live TV"; press Back.
  2. Choose filter "Ended"; click a change title; press Back.
  3. Deep link `/admin/live-tv/changes?filter=live`.
- **Expected:** Links go to `/admin/live-tv` and `/admin/live-tv/changes/<id>`. Back returns to the list. The filter resets to "All" after Back and the query is ignored, because the filter is not stored in the URL (gap — consider `?filter=`).

### LTC-20 — Phone width: table and slot bar fit

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** viewport 375 × 812; 20 changes; 2 hosts × 2 slots.
- **Steps:**
  1. Open the page; scroll.
- **Expected:** `document.documentElement.scrollWidth <= 375`. Rows wrap or scroll inside the card; filter buttons wrap; the slot bar wraps; nav reachable.

### LTC-21 — Arabic: RTL and Arabic strings

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes — `Changes.test.tsx` "renders in Arabic, right to left" and "every Changes page and notification key exists in English and Arabic"
- **Preconditions / data:** `localStorage["mkt:locale"]="ar"`.
- **Steps:**
  1. Open the page.
- **Expected:** `dir="rtl"`; Arabic heading and labels; numbers formatted; clock times in `ar-EG`; quiet window stays LTR.

### LTC-22 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** `data-theme="light"` / `"dark"`.
- **Steps:**
  1. Open the page with every state chip, the staging-off banner, the in-flight banner and an error.
- **Expected:** Chips (`neutral/accent/warn/success/error`), banners, the active filter and error text are readable in both themes; `tvchanges.css` uses tokens only.

### LTC-23 — Keyboard and screen reader basics

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Changes.cases.test.tsx`)
- **Preconditions / data:** fixtures.
- **Steps:**
  1. Tab through filters, banner, rows, settings `<summary>`.
- **Expected:** Filters are buttons with `aria-pressed`; the table exposes `role="table"`, `row`, `columnheader`, `cell`; title links and the in-flight link are reachable with a visible focus ring; the settings `<details>` opens with Enter/Space; the error lives in an `aria-live="polite"` region; skeletons are `aria-hidden`; the checks summary has an `aria-label`.
