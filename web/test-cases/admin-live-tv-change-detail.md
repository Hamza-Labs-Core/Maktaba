# Admin Live TV change detail

- **Route:** `/admin/live-tv/changes/:id` (deep link by change id; tab is React state only, not in the URL)
- **Component:** `src/pages/Admin/LiveTV/ChangeDetail.tsx` (`AdminTvChangeDetail` → `DetailInner`, in-file `History`, `rollbackHint`, `progressText`, `channelNames`); children `ApproveDialog.tsx` (approve / schedule / promote "Swap now"), `ConfirmDialog.tsx` (cancel, roll back), `ChecksTable.tsx`, `PreviewPanel.tsx` (`PreviewPlayers`, `LivePane`, `GuideStrip`, uses `useTvSwitcher` from `@maktaba/tvplayer`), `diff/LineupDiff.tsx`, `diff/ScheduleDiff.tsx`, `diff/SettingsDiff.tsx`, `diff/ArtDiff.tsx`, `changeBits.tsx`. (`MoveDialog.tsx` is not used here; it belongs to the On air table, see `admin-live-tv.md`.)
- **API calls (all `tvChangesApi` in `src/lib/tvChanges.ts` unless noted):**
  - `GET /api/tv/changes/{id}` → `TvChangeDetail` every 2 s while visible
  - `GET /api/tv/changes?state=draft&limit=1` → `ChangeList` (only `config` is used) every 60 s
  - `GET /api/tv/rollback` → `RollbackPoint` every 2 s, only while `allowed_actions` includes `rollback`
  - `POST /api/tv/changes/{id}/stage|verify|cancel|promote`, `POST .../approve` `{swap_at?, ack_param_change?}`, `PATCH /api/tv/changes/{id}` `{swap_at, swap_wait}`, `POST /api/tv/rollback` `{}`
  - `GET /api/tv/changes/{id}/boundaries?at=&swap_wait=` (dialog, 150 ms debounce)
  - `GET /api/tv/changes/{id}/schedule-diff?page=N` → `SchedulePage`, `?channel=<id>` → `ScheduleChannelDiff`
  - `POST /api/tv/changes/{id}/preview` → `Preview` (re-minted a minute before `expires_at`); staging HLS URLs from it
  - `GET /api/rules` → `{sections}` via `rulesApi.list()` (labels on the Settings tab)
  - Art thumbs `GET /api/channels/{id}/art/{kind}[?release=<id>]`
- **Access:** admin only via `AdminGate`; signed out → `/login`. Second-admin rule (`config.require_second_admin`) blocks the author's own approve in the dialog; server answers 409 `second_admin_required`.
- **Existing tests:** `src/pages/Admin/LiveTV/ChangeDetail.test.tsx` — header + checks summary + actions in fixed order; Lineup tab (added, removed, renumbered 105 → 115, renamed, changed fields); Schedules tab open before/after; Settings tab `tv_output` diff; Art tab thumbnails; Checks tab order; History events + audit; draft with Stage; live change warm rollback; rollback disabled with reason; failed change no actions; cancel asks first; Approve dialog ASAP with switch times; param-change ack; second-admin rule; schedule a time. `PreviewPanel.test.tsx` — mint + play staging with token; switch channels + live side by side; why no preview outside verifying/ready/approved. **Gaps:** unknown id / load errors, error after load, null/missing arrays in the change and sub-endpoints, 409 action errors, promote flow, cancel/rollback errors, past swap time, preview errors and bad expiry, schedule paging, permissions, crash recovery, navigation, responsive, theme, a11y.

## Summary

| ID     | Title                                                      | Category      | Priority | Covered by existing test?                                     |
| ------ | ---------------------------------------------------------- | ------------- | -------- | ------------------------------------------------------------- |
| LCD-01 | Header, facts and actions in fixed order                   | Happy path    | P1       | Yes (`ChangeDetail.test.tsx`)                                 |
| LCD-02 | Lineup tab                                                 | Happy path    | P2       | Yes (`ChangeDetail.test.tsx`)                                 |
| LCD-03 | Schedules tab: open a channel, paging, errors              | Happy path    | P2       | Partial (`ChangeDetail.test.tsx`: open only; no paging/error) |
| LCD-04 | Settings tab                                               | Happy path    | P3       | Yes (`ChangeDetail.test.tsx`)                                 |
| LCD-05 | Binary & art tab, thumbnail fails                          | Happy path    | P3       | Partial (`ChangeDetail.test.tsx`: thumb error not)            |
| LCD-06 | Checks tab order, reasons, errors, slot line               | Happy path    | P2       | Partial (`ChangeDetail.test.tsx`: order only)                 |
| LCD-07 | History tab                                                | Happy path    | P3       | Yes (`ChangeDetail.test.tsx`)                                 |
| LCD-08 | Draft: what it holds, Stage                                | Happy path    | P2       | Partial (`ChangeDetail.test.tsx`: Stage click result not)     |
| LCD-09 | Unknown change id                                          | Navigation    | P1       | No                                                            |
| LCD-10 | Load errors on first load and after a good load            | Error         | P2       | No                                                            |
| LCD-11 | Change top-level arrays null or missing                    | Empty state   | P1       | No                                                            |
| LCD-12 | Change `diff` and `pending_summary` arrays null or missing | Empty state   | P1       | No                                                            |
| LCD-13 | Sub-endpoint arrays null or missing                        | Empty state   | P1       | No                                                            |
| LCD-14 | Stage / Re-verify success and refusal                      | Error         | P2       | No                                                            |
| LCD-15 | Approve for as soon as possible                            | Happy path    | P1       | Yes (`ChangeDetail.test.tsx`)                                 |
| LCD-16 | Parameter-change acknowledgement                           | Validation    | P1       | Yes (`ChangeDetail.test.tsx`)                                 |
| LCD-17 | Second-admin rule                                          | Permissions   | P1       | Yes (`ChangeDetail.test.tsx`)                                 |
| LCD-18 | Schedule a swap time; past time blocked                    | Validation    | P2       | Partial (`ChangeDetail.test.tsx`: past time not)              |
| LCD-19 | Swap now (promote) flow                                    | Happy path    | P1       | No                                                            |
| LCD-20 | Approve / promote conflicts (409)                          | Error         | P1       | No                                                            |
| LCD-21 | Cancel (reject) flow and its errors                        | Happy path    | P1       | Partial (`ChangeDetail.test.tsx`: "cancel asks first")        |
| LCD-22 | Roll back warm, cold, none, and errors                     | Happy path    | P1       | Partial (`ChangeDetail.test.tsx`: warm + none)                |
| LCD-23 | Preview tab: mint, switch, side by side, errors            | Happy path    | P2       | Partial (`PreviewPanel.test.tsx`: no error/retry)             |
| LCD-24 | Preview with a missing or bad `expires_at`                 | Error         | P2       | No                                                            |
| LCD-25 | Polling rules and stopping on leave                        | Happy path    | P2       | No                                                            |
| LCD-26 | Non-admin and signed-out access                            | Permissions   | P1       | No                                                            |
| LCD-27 | Render crash shows the route error fallback; Back works    | Error         | P1       | No                                                            |
| LCD-28 | Links, Back and switching between changes                  | Navigation    | P2       | No                                                            |
| LCD-29 | Phone width                                                | Responsive    | P3       | No                                                            |
| LCD-30 | Arabic: RTL and Arabic strings                             | RTL           | P2       | No                                                            |
| LCD-31 | Light and dark theme                                       | Theme         | P3       | No                                                            |
| LCD-32 | Keyboard, tabs, dialogs and live regions                   | Accessibility | P2       | No                                                            |

## Cases

Shared data: `changeList`, `boundaries`, `schedulePage`, `quranSchedule`, `rollbackPoint`, `answer(path)` from `src/pages/Admin/LiveTV/changeFixtures.ts`; preview fixtures from `PreviewPanel.test.tsx`. Admin `{id:"aaaaaaaa-...", username:"mahmoud", is_admin:true}`.

### LCD-01 — Header, facts and actions in fixed order

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `ChangeDetail.test.tsx` "shows the change, its checks summary and the actions of its state in a fixed order"
- **Preconditions / data:** a `ready` change with `allowed_actions:["cancel","approve","verify"]`.
- **Steps:**
  1. Open `/admin/live-tv/changes/<id>`.
- **Expected:** `data-testid="change-detail"`; heading "#N <title>" (or "Untitled change"); "All changes" link; facts: state chip + `data-testid="progress"`, kinds, source, author, checks, swap ("as soon as possible" / clock / swapped at) · swap wait, approved by (or "—"). Toolbar `role="toolbar"` shows "Re-verify", "Approve", "Cancel" in `ACTION_ORDER` regardless of the server's order.

### LCD-02 — Lineup tab

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `ChangeDetail.test.tsx` "the Lineup tab: added, removed, renumbered 105 → 115, renamed, definition changes"
- **Preconditions / data:** fixture with a full `diff.lineup`.
- **Steps:**
  1. Open the page (Lineup is the default tab).
- **Expected:** `data-testid="lineup-diff"` groups Added / Removed / Renumbered / Renamed / Changed; empty groups say `tvchanges.none`; numbers LTR "105 → 115".

### LCD-03 — Schedules tab: open a channel, paging, errors

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `ChangeDetail.test.tsx` "the Schedules tab: the regenerated channels; a channel opens before / after side by side"
- **Preconditions / data:** staged change (`release_id` set). (a) `schedule-diff?page=1` → `{channels:[10 items], page:1, pages:3, total:25}`; `page=2` → next 10. (b) `?channel=c1` rejects 500. (c) `?page=1` rejects 500. (d) `{channels:[], total:0}`. (e) not staged (`release_id` missing). (f) `?channel=c2` → `{unchanged:true, before:[...], after:[...]}`.
- **Steps:**
  1. Open the "Schedules" tab; click Next, Prev; click a channel twice.
- **Expected:** Intro with total 25; pager "Page 1 of 3", Prev disabled on 1, Next disabled on 3; buttons expose `aria-expanded`. (b) row shows `role="alert"` "Could not load this schedule." (c) `role="alert"` with the error. (d) "No schedule is regenerated." (e) "The schedules are built when the change is staged." (f) "unchanged" chip and no side-by-side. Clicking an open row folds it.

### LCD-04 — Settings tab

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Yes — `ChangeDetail.test.tsx` "the Settings tab: the key diff of tv_output"
- **Preconditions / data:** `diff.settings = {tv_output:[{key:"bitrate", from:4000, to:6000}]}`; `GET /api/rules` rejects on a second run; `diff.settings` missing on a third.
- **Steps:**
  1. Open "Settings".
- **Expected:** `data-testid="settings-diff"` with the section title, key label (from rules, else the raw key), from/to with `dir="auto"`, null → "—". Rules failure → raw keys only. Missing → "No settings change."

### LCD-05 — Binary & art tab, thumbnail fails

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Partial — `ChangeDetail.test.tsx` "the Binary & art tab: before / after thumbnails from the live channel and the release"
- **Preconditions / data:** `diff.art=[{id:"c1", number:101, name:"Quran", what:["look"]}]`, `diff.binary={from:"1.4.0", to:"1.5.0"}`, `release` set; second run `release` missing and `binary:null`.
- **Steps:**
  1. Open "Binary & art"; fire `error` on a thumbnail.
- **Expected:** Binary "1.4.0 → 1.5.0" with release notes link to `/settings`; rows for loading + watermark; before `src` `/api/channels/c1/art/loading`, after with `?release=<id>`. A failed image shows `tvchanges.art.none` text. Without release: "same version —" and the after side shows "not staged". No art → "No art change."

### LCD-06 — Checks tab order, reasons, errors, slot line

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `ChangeDetail.test.tsx` "the Checks tab: failures first, then warnings, passes, skipped"
- **Preconditions / data:** `verify_report = {checks:[...], at:"<iso>", interim:true, runtime:"slots", slot:"green", status:"soaking", budget:{staging:2, capacity:8, live:4, standby:2}, reasons:["black frames on 101"], error:"probe failed"}`; second run `verify_report: {}`.
- **Steps:**
  1. Open "Checks".
- **Expected:** `data-testid="checks"`: summary + "at" clock; interim hint; `data-testid="checks-slot"` with slot, status and budget; `data-testid="checks-reasons"` `role="alert"`; error `role="alert"`; rows fail → warn → pass → skip; channel ids resolved through `names` else 8-char prefix. Empty report → "The checks run once the change is staged."

### LCD-07 — History tab

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Yes — `ChangeDetail.test.tsx` "the History tab: the events with their actors and the audit rows"
- **Preconditions / data:** fixture events + audit; second run `events:[]`, `audit:[]`.
- **Steps:**
  1. Open "History".
- **Expected:** Timeline with date, translated action, "(state → state)" when it changed, actor name or source. Audit table, or "No audit rows." when empty.

### LCD-08 — Draft: what it holds, Stage

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `ChangeDetail.test.tsx` "a draft: what it holds, Stage among its actions"
- **Preconditions / data:** draft with `pending_summary` and no `diff.lineup`; `POST .../stage` → the change with `state:"staged"`; second run → 204 (empty body).
- **Steps:**
  1. Click "Stage".
- **Expected:** `data-testid="pending-summary"` lists channels with op and settings sections. Stage: button disabled while busy, then `role="status"` "Staging: the release and the new schedules are being built.", chip "Staged". 204 → same message, the change updates on the next poll. Draft with no `pending_summary` → "The diff is computed when the change is staged."

### LCD-09 — Unknown change id

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/tv/changes/does-not-exist` rejects `ApiError` 404 "Not Found"; also an id with spaces/slashes `/admin/live-tv/changes/a%2Fb`.
- **Steps:**
  1. Open `/admin/live-tv/changes/does-not-exist`; advance 6 s.
  2. Click "All changes".
- **Expected:** Heading "TV changes", `role="alert"` "Could not load the changes: Not Found", link "All changes" → `/admin/live-tv/changes`. The id is URL-encoded in the request. A friendlier "This change does not exist" would be better (gap). Polling continues every 2 s against the 404 (wasteful — consider stopping on 404).

### LCD-10 — Load errors on first load and after a good load

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** (a) first `GET` rejects 500 / 403 / network / malformed JSON. (b) first `GET` good, later rejects 500 "boom". Fake timers.
- **Steps:**
  1. Open the page; advance 2 s.
- **Expected:** (a) the error section with the message; it switches to the change once a later poll succeeds. (b) the change stays visible, but the error is not shown anywhere because `error` is only rendered when there is no change (currently unhandled — likely gap: show a stale hint).

### LCD-11 — Change top-level arrays null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/tv/changes/{id}` → a `ready` fixture with one change per run:
  - `allowed_actions: null` / missing (`ChangeDetail.tsx:120` `.includes` unguarded; `:213` `new Set(...)`)
  - `kinds: null` / missing (`changeBits.tsx:54`)
  - `approved_by: null` / missing (`:277` `.length`)
  - `audit: null` / missing (`:525,537`, History tab)
  - `events: null` / missing (guarded)
  - `users: null` / missing (guarded)
  - `verify_report: null`, `verify_report.checks: null`, `verify_report.reasons: null` (guarded)
  - `source: null` (guarded)
- **Steps:**
  1. Open the page; open each tab.
- **Expected:** No blank page. Actions null → "Nothing to do in this state." Kinds → "—". Approved by → "—". Audit → "No audit rows." Events → empty timeline. Checks → "—" / "The checks run once the change is staged." Today `allowed_actions`, `kinds`, `approved_by` and `audit` null each throw (currently unhandled — likely bug).

### LCD-12 — Change `diff` and `pending_summary` arrays null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** one change per run:
  - `diff: null` / missing (guarded with `?.`)
  - `diff.lineup.added|removed|renumbered|renamed|changed: null` and each missing (`LineupDiff.tsx:64-97` `.length`/`.map` unguarded; `channelNames` guards added/removed/changed)
  - `diff.lineup.changed[0].fields: null` (`:100`)
  - `diff.schedules: null` (guarded)
  - `diff.art: null` (guarded), `diff.art[0].what: null` (`ArtDiff.tsx:71`)
  - `diff.settings.tv_output: null` (guarded)
  - draft with `pending_summary.channels: null` / missing and `pending_summary.settings: null` / missing (`LineupDiff.tsx:41-54`)
- **Steps:**
  1. Open Lineup, Binary & art, Settings tabs.
- **Expected:** Each null group shows `tvchanges.none` (or the "not yet" text); a changed channel without fields shows its name only; art without `what` shows the channel heading with no rows; no thrown errors. Today the lineup groups, `fields`, `what` and `pending_summary` arrays each throw (currently unhandled — likely bug).

### LCD-13 — Sub-endpoint arrays null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** one change per run:
  - `schedule-diff?page=1` → `{channels:null, page:1, pages:1, total:3}` and `channels` missing (`ScheduleDiff.tsx:56`)
  - `schedule-diff?channel=c1` → `{before:null, after:null, unchanged:false, ...}` (`ScheduleDiff.tsx:117-118`, `PreviewPanel.tsx` `GuideStrip` `d.after.slice`)
  - `boundaries` → `{channels:null, ...}` / missing (`ApproveDialog.tsx:193`)
  - `preview` → `{channels:null, ...}` / missing (`PreviewPanel.tsx:126,132`)
  - `GET /api/tv/changes?state=draft&limit=1` → `{}` (no `config`)
  - `GET /api/tv/rollback` → `{}` (no `mode`)
- **Steps:**
  1. Open Schedules and click a channel; open Approve; open Preview (state `ready`); show Roll back on a live change.
- **Expected:** Schedules → "No schedule is regenerated." or an empty list; side-by-side → "empty" text in both columns; dialog → "no switches" text; preview → no players, a "no channels" hint; missing config → the dialog uses the change's own swap wait; rollback without mode → treated as none/unknown, button not crashing. Today `channels`, `before`/`after` and preview `channels` null throw (currently unhandled — likely bug).

### LCD-14 — Stage / Re-verify success and refusal

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST .../verify` → change; then rejects `ApiError` 409 `{state:"swapping"}`; then 409 `{error:"change_in_flight"}`; then a 500 with a message; then network error.
- **Steps:**
  1. Click "Re-verify" each time.
- **Expected:** Success `role="status"` "The checks run again." 409 state → `role="alert"` "Not possible now: the change is Swapping." 409 in flight → "Another change is in flight; this one waits for its turn." 500 → the server's message. Network → its message. All action buttons are disabled while busy and re-enabled after.

### LCD-15 — Approve for as soon as possible

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `ChangeDetail.test.tsx` "shows the expected switch times and approves for as soon as possible"
- **Preconditions / data:** `ready` change; `boundaries` fixture; `POST .../approve` → change `approved`.
- **Steps:**
  1. Click "Approve"; confirm with `data-testid="dialog-confirm"`.
- **Expected:** Dialog "Approve change #N"; `data-testid="boundaries"` table of channel, on air, switch time (capped note when `capped`); body `{swap_at:null}`; dialog closes; status "Approved."; chip "Approved".

### LCD-16 — Parameter-change acknowledgement

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes — `ChangeDetail.test.tsx` "asks for the parameter-change acknowledgement when verification flags it"
- **Preconditions / data:** `verify_report.checks` includes `{check:"push.continuity", result:"fail"}`.
- **Steps:**
  1. Open Approve; try confirm; tick `data-testid="ack-param"`; confirm.
- **Expected:** Confirm disabled until ticked; body includes `ack_param_change:true`. The Schedule dialog never asks for it.

### LCD-17 — Second-admin rule

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes — `ChangeDetail.test.tsx` "the second-admin rule: the author cannot approve; another admin can"
- **Preconditions / data:** `config.require_second_admin:true`; `created_by` = current user id, then another id.
- **Steps:**
  1. Open Approve.
- **Expected:** Author: `data-testid="second-admin"` error "A second admin approves this change: you made it." and confirm disabled. Other admin: hint and confirm enabled. If the config call failed (cfg null) the dialog does not block and the server's 409 `second_admin_required` shows that same text (see LCD-20).

### LCD-18 — Schedule a swap time; past time blocked

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Partial — `ChangeDetail.test.tsx` "schedules a swap time"
- **Preconditions / data:** `approved` change with `allowed_actions:["schedule", ...]`.
- **Steps:**
  1. Click "Schedule"; pick "At a time"; type a past datetime; then empty; then +2 h; switch mode to "immediate"; confirm.
- **Expected:** Past or empty → "Choose a time in the future." with `aria-invalid="true"` and confirm disabled. Valid → `PATCH /api/tv/changes/{id}` `{swap_at:<iso>, swap_wait:"immediate"}`; boundaries re-read with `at` and `swap_wait` after 150 ms; status "Swap time saved."

### LCD-19 — Swap now (promote) flow

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `approved` change with `swap_wait:"boundary"`, `allowed_actions` includes `promote`; `POST .../promote` → change `swapping`.
- **Steps:**
  1. Click "Swap now"; click the backdrop; press Escape; reopen.
  2. Switch to "immediate"; confirm.
- **Expected:** Dialog "Swap change #N now" with destructive confirm "Swap now" and no "when" fieldset. Backdrop click does not close it (`dismissable={false}`); Escape still closes it (check that is intended). With a changed mode: first `PATCH {swap_at:<existing or null>, swap_wait:"immediate"}`, then `POST .../promote`. Status "Swapping: the channels switch now."; chip "Swapping".

### LCD-20 — Approve / promote conflicts (409)

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST .../approve` rejects `ApiError` 409 with, per run: `{error:"second_admin_required"}`, `{error:"ack_param_change_required"}`, `{error:"change_in_flight"}`, `{state:"cancelled"}`, a bare 409 "Conflict". `POST .../promote` 409 `{state:"live"}`. And the pre-PATCH failing with 409 when the mode changed.
- **Steps:**
  1. Open the dialog; confirm.
- **Expected:** Dialog stays open; `role="alert"` inside it with: "A second admin approves this change: you made it." / "This change changes the picture of a pushed channel: acknowledge it first." / "Another change is in flight; this one waits for its turn." / "Not possible now: the change is Cancelled." / "Conflict". Confirm re-enabled. A failed pre-PATCH stops before approve/promote is sent. The page re-reads on the next poll and shows the real state.

### LCD-21 — Cancel (reject) flow and its errors

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `ChangeDetail.test.tsx` "cancel asks first"
- **Preconditions / data:** `ready` change; `POST .../cancel` → change `cancelled`; second run rejects 409 `{state:"swapping"}`.
- **Steps:**
  1. Click "Cancel"; in the dialog click its "Cancel" (`common.cancel`) button.
  2. Click "Cancel" again; click "Cancel the change".
- **Expected:** Dialog "Cancel change #N"; the dismiss button closes it with no request. Confirm: busy-disabled, `POST .../cancel`, dialog closes, status "Cancelled.", chip "Cancelled", actions now "Nothing to do in this state." 409: the dialog stays open and the page shows `role="alert"` "Not possible now: the change is Swapping." (behind the dialog — check it is visible/announced; currently the message renders outside the modal).

### LCD-22 — Roll back warm, cold, none, and errors

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `ChangeDetail.test.tsx` "a live change: Roll back says warm (instant) and the minutes left, and rolls back", "Roll back is disabled with the reason when there is no rollback point"
- **Preconditions / data:** live change with `rollback` allowed. `GET /api/tv/rollback` → (a) `{mode:"warm", until:+12 min}`, (b) `{mode:"warm"}` (no until), (c) `{mode:"cold"}`, (d) `{mode:"none", reason:"no_rollback_point"}`, (e) rejects. `POST /api/tv/rollback` → `{mode:"cold", change_id, rollback_change_id}`; another run rejects 409.
- **Steps:**
  1. Read `data-testid="rollback-mode"`; click "Roll back"; confirm.
- **Expected:** (a) warm with 12 min left; (b) warm-now text; (c) cold text and dialog body `tvchanges.dialog.rollback.cold`; (d) button disabled inside a focusable wrapper `data-testid="action-rollback-wrap"` with a tooltip giving the reason; (e) button stays enabled with no hint. Confirm → status "Rolling back: the channels switch back now." or "Rollback change created: it is staged and swapped when ready."; the change reloads. 409 → alert; dialog stays.

### LCD-23 — Preview tab: mint, switch, side by side, errors

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `PreviewPanel.test.tsx` "mints the preview and plays the changed channel from staging, with the token", "switches channels and plays live side by side, following the staging channel", "says why there is no preview outside verifying / ready / approved"
- **Preconditions / data:** `ready` change; `POST .../preview` → fixture; second run rejects 500 "boom" then succeeds.
- **Steps:**
  1. Open "Preview"; tick side by side; click another channel.
  2. Failing run: click "Try again".
- **Expected:** "Preparing the preview…" then expiry clock; first changed/added channel plays from its staging `url`; `preview-m3u` / `preview-xmltv` links. Failure: `role="status"` area shows "The preview could not be prepared: boom" with "Try again"; it does not re-mint on its own; Try again mints and plays. Draft/live: `data-testid="preview-off"` "A change can be previewed while it is verifying, ready or approved; this one is <state>."

### LCD-24 — Preview with a missing or bad `expires_at`

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; `POST .../preview` → fixture with `expires_at` missing, then `"not-a-date"`, then a time already in the past.
- **Steps:**
  1. Open "Preview"; advance 10 s; count preview calls.
- **Expected:** At most one re-mint per 5 s (the `Math.max(left, 5_000)` floor). Today a missing/invalid `expires_at` gives `NaN`, `Math.max(NaN, 5000)` is `NaN`, and `setTimeout(fn, NaN)` fires at once, so the panel re-mints in a tight loop and hammers `POST /preview` (currently unhandled — likely bug).

### LCD-25 — Polling rules and stopping on leave

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; live change with `rollback` allowed, then a `ready` change without it.
- **Steps:**
  1. Open; advance 2 s, 60 s; hide the tab 10 s; show; navigate away; advance 10 s.
- **Expected:** Change every 2 s; config list every 60 s; rollback point every 2 s only when rollback is allowed (none for the `ready` change). Nothing while hidden; one immediate read on show. Nothing after unmount (incl. ScheduleDiff, ApproveDialog boundaries timer, preview re-mint timer); no state-update warnings. A poll that brings the same data does not re-render rows (reconcile).

### LCD-26 — Non-admin and signed-out access

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** (a) `useAuth` → `{user:{is_admin:false}}`; (b) full `App` with `{user:null, loading:false}`; (c) admin but the server answers 403 on `GET /api/tv/changes/{id}`.
- **Steps:**
  1. Open `/admin/live-tv/changes/<id>`.
- **Expected:** (a) "Admins only" ErrorState, no requests, no polling. (b) redirect to `/login` with `state.from` = the path. (c) the error section with the server message and "All changes" link.

### LCD-27 — Render crash shows the route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** full `App`, history `["/admin/live-tv/changes", "/admin/live-tv/changes/<id>"]`; `GET /api/tv/changes/<id>` → fixture with `allowed_actions: null` (throws at `ChangeDetail.tsx:120`), or mock `ChecksTable` to throw.
- **Steps:**
  1. Render the detail URL.
  2. Press Back; repeat and click an admin nav link instead.
- **Expected:** Route-level error fallback (boundary in `src/components/AppShell.tsx`), not a blank page. Back renders the Changes list without a refresh; the nav link renders its page; the 2 s poll of the crashed page stops.

### LCD-28 — Links, Back and switching between changes

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** change with `source.stage:"tv_plan"`; two changes A and B.
- **Steps:**
  1. Click "All changes"; Back.
  2. Click the job link.
  3. Open "Checks" tab on A, then navigate (same mounted route) to `/admin/live-tv/changes/B`.
- **Expected:** "All changes" → `/admin/live-tv/changes`; Back shows the detail again. Job link → `/queue/tv_plan`. Moving A → B shows B's data (not A's for a poll cycle), closes any open dialog and clears A's message; note the tab (and messages) are kept across ids today because state lives in the same component instance — consider keying the page by id. Back/Forward do not change the tab (tab not in the URL).

### LCD-29 — Phone width

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (browser e2e)
- **Preconditions / data:** viewport 375 × 812; change with many actions, checks and a preview.
- **Steps:**
  1. Open the page; switch tabs; open the Approve dialog.
- **Expected:** `document.documentElement.scrollWidth <= 375`. Action toolbar wraps; the tab list scrolls or wraps; check and boundaries tables scroll inside their box; side-by-side schedule stacks; preview videos fit; dialogs fit and the datetime input is usable.

### LCD-30 — Arabic: RTL and Arabic strings

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** No (keys only: `Changes.test.tsx` "every Changes page and notification key exists in English and Arabic")
- **Preconditions / data:** `localStorage["mkt:locale"]="ar"`.
- **Steps:**
  1. Open the page; each tab; the Approve dialog.
- **Expected:** `dir="rtl"`; Arabic headings, actions, state chips, dialog text; numbers in `ar-EG`; "105 → 115" stays LTR; settings values and check values use `dir="auto"`; tabs and toolbar order mirror.

### LCD-31 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No (visual)
- **Preconditions / data:** `data-theme="light"` / `"dark"`.
- **Steps:**
  1. View add/del diff colours, check result chips, warn banner, errors.
- **Expected:** `mkt-tvx-add` / `mkt-tvx-del`, result chips and banners readable in both themes; tokens only.

### LCD-32 — Keyboard, tabs, dialogs and live regions

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** `ready` change.
- **Steps:**
  1. Tab through the toolbar; move between tabs with arrow keys; open Approve; Tab through it; press Escape.
- **Expected:** Toolbar named "Actions" (`tvchanges.actions`); disabled roll back is reachable through its wrapper (`tabIndex=0`) and the tooltip text is announced. Tabs use the design-system `Tabs` roles (`tablist`/`tab`/`tabpanel`, named "tvchanges.tabs"). Dialog `role="dialog" aria-modal="true"`, focus trapped, Escape closes, focus returns to the button. Radios grouped in `fieldset` with `legend`; the datetime input has a label. Action results are in an `aria-live="polite"` region with `role="status"` / `role="alert"`.
