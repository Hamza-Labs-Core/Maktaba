# Admin Channels

- **Route:** `/admin/channels` (no params, no query-string state; the editor is a modal, not a route)
- **Component:** `src/pages/Admin/Channels.tsx`; children: `src/pages/Admin/channels/ChannelList.tsx`, `ChannelEditor.tsx`, `ArtSection.tsx` (+`PendingArtSection`, watermark settings), `StripSection.tsx`, `StripColors.tsx`, `StripPreview.tsx`, `ScheduleSection.tsx`, `ScheduleGrid.tsx`, `SlotEditor.tsx`, `schedule.ts`, `RuleSection.tsx`, `FillerSection.tsx`, `LiveSection.tsx`, `PreviewSection.tsx`, `PreferHost.tsx`, `pickers.tsx` (`SourceSearch`, `DayChips`); libs `src/lib/channels.ts`, `src/lib/strip.ts`, `src/lib/rulesApi.ts`, `src/lib/tvClusterApi.ts`
- **API calls:**
  - `GET /api/channels` via `channelsApi.list()` → `{ items: Channel[] }` (reloaded after every mutation; a 30 s timer re-renders the guide-hours column)
  - `GET /api/tv/info` via `tvApi.info()` → `TvInfo` (only `xmltv_url` is read)
  - `PATCH /api/channels/{id}` via `channelsApi.update()` (only `PATCH_FIELDS`); `POST /api/channels` via `channelsApi.create()`; `DELETE /api/channels/{id}`; `POST /api/channels/reorder` body `[{id, number}]`; `POST /api/channels/{id}/follow-plan`; `POST /api/channels/{id}/regenerate`
  - `GET /api/channels/{id}/guide?start&end` via `channelsApi.schedulePreview()` → `{ server_time, channel, blocks: WireBlock[] }`
  - `GET /api/channels/sources?kind&q&limit` via `channelsApi.sources()` → `{ kind, items: SourceHit[] }` (debounced 250 ms)
  - `GET /api/channels/now` via `channelsApi.now()` → `{ server_time, channels: [{ channel, current, next }] }` (strip preview lines)
  - `GET /api/filler/pools` via `fillerApi.pools()` → `{ items: FillerPool[] }`
  - `GET /api/rules` via `rulesApi.list()` → `{ sections: [{ key, fields: [{ name, value }] }] }` (reads `tv.live`, `dizquetv.live`, `tv_strip.*`); `PATCH /api/rules/tv` with `{ live: rows }` via `rulesApi.patch()`
  - `POST /api/channels/{id}/art/{logo|watermark|loading}` (multipart, progress) via `channelArtApi.upload()`; `DELETE /api/channels/{id}/art/{kind}`; `PATCH /api/channels/{id}/art/watermark`
  - `PATCH /api/channels/{id}/strip` / `DELETE /api/channels/{id}/strip` via `channelStripApi`
  - `GET /api/tv/cluster` via `tvClusterApi.get()` → `{ hosts: [{ name, ... }], settings: { pins } }`; `PATCH /api/rules/tv_cluster` via `tvClusterApi.setPin()`
- **Access:** admin only. `RequireAuth` (signed out → `/login`), then `AdminGate` (non-admin → `common.forbidden.*` ErrorState). Server re-checks every mutation.
- **Existing tests:**
  - `src/pages/Admin/Channels.test.tsx` — covers: list renders, XMLTV guide copy field, empty state for `items: []`, admin gate, opening the form with a mode-aware rule builder, duplicate number error.
  - `src/pages/Admin/channels/ChannelBuilder.test.tsx` — covers: origin / now playing / guide coverage columns, search filter disables reorder, keyboard reorder with plan warning, grid draw + slot time/source edit, split/add/delete slots, weekly days, filler weights + add pool, live broadcasts saved through rules without a channel PATCH, back to plan on an edited channel, 48 h preview + regenerate confirm, new channel needs a slot and free number and uploads art after first save.
  - `src/pages/Admin/ChannelArt.test.tsx` — covers: tiles and their origin badge, upload with progress, size/type refusal, revert to plan, watermark save, pending art for a new channel.
  - `StripPreview.test.tsx`, `StripColors.test.tsx`, `StripPaging.test.tsx` — strip helpers, preview rendering, StripSection saves only changes, bad time zone, colours/palettes/hex validation.
  - `PreferHost.test.tsx` — pin / unpin keeping other pins; hidden when the cluster view fails.
  - `schedule.test.ts`, `src/lib/channels.test.ts` — pure helpers and client paths/bodies.
  - Gaps: null / missing list fields, error states for list load and every mutation, delete, toggle, drag reorder, save errors (409 / 400 / network), unsaved-changes behaviour (Cancel, Escape, backdrop, tab switch, navigation), filler max-gap not blocking save, live save failing after create, error boundary, RTL, theme, phone width, modal keyboard behaviour.

## Summary

| ID     | Title                                                        | Category      | Priority | Covered by existing test?                                                             |
| ------ | ------------------------------------------------------------ | ------------- | -------- | ------------------------------------------------------------------------------------- |
| CHN-01 | Lists channels sorted by number with all columns             | Happy path    | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-02 | XMLTV guide field shown, hidden when `/api/tv/info` fails    | Happy path    | P3       | Partial (`Channels.test.tsx`: failure / empty `xmltv_url` not tested)                 |
| CHN-03 | `items: []` shows "No channels yet"                          | Empty state   | P2       | Yes (`Channels.test.tsx`)                                                             |
| CHN-04 | `items: null` or missing key shows empty state, no crash     | Empty state   | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-05 | Nullable channel fields render fallbacks                     | Empty state   | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-06 | Channel with `name: null` does not blank the page            | Error         | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-07 | List load fails (500, network, malformed JSON)               | Error         | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-08 | Loading text while the list is pending                       | Loading       | P3       | No                                                                                    |
| CHN-09 | Non-admin sees "Admins only" and no channel requests fire    | Permissions   | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-10 | Signed-out user is redirected to `/login`                    | Permissions   | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-11 | Search, category and origin filters; "no match" text         | Happy path    | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-12 | On/off toggle: optimistic, plan warning, error toast         | Happy path    | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-13 | Reorder by drag and keyboard; reorder error                  | Happy path    | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-14 | Delete a channel: confirm, cancel, error                     | Happy path    | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-15 | New channel form validation blocks Save                      | Validation    | P1       | Yes (`Channels.cases.test.tsx`; the 2.5 refusal is it.fails: bug)                     |
| CHN-16 | Create a channel; pending art uploads after create           | Happy path    | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-17 | Edit sends only changed fields; Save disabled when unchanged | Happy path    | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-18 | Plan / edited banners and Back to plan                       | Happy path    | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-19 | Save errors: 409 stale, 400 detail, network                  | Error         | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-20 | Unsaved changes on Cancel / Escape / backdrop / X            | Validation    | P1       | No (it.fails: bug) — `Channels.cases.test.tsx`; the no-changes close passes           |
| CHN-21 | Unsaved section drafts lost on tab switch                    | Validation    | P2       | No (it.fails: bug) — `Channels.cases.test.tsx`                                        |
| CHN-22 | Schedule slot validation and the cross-tab warning           | Validation    | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-23 | `mode_config.slots` / `ramadan` null or wrong type           | Empty state   | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-24 | Non-grid modes: marathon needs a series, shuffle, smart mix  | Validation    | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-25 | Filler section: weights, missing pool, max gap validation    | Validation    | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-26 | Filler pools and source search: null lists and errors        | Empty state   | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-27 | Live broadcasts: validation, renumbering, rules load failure | Validation    | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-28 | Live save fails after a new channel was created              | Error         | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-29 | Art upload / revert / watermark errors                       | Error         | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-30 | Strip settings: validation, save, reset, errors              | Validation    | P2       | Partial (`StripPreview.test.tsx`: range/colour errors and API error alert not tested) |
| CHN-31 | Prefer host select: null hosts, pin failure                  | Error         | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-32 | Preview tab: loading, empty, null blocks, regenerate         | Empty state   | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-33 | Links: Filler pools, TV draft change; Back returns to list   | Navigation    | P2       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-34 | Render crash shows route error fallback; Back still works    | Error         | P1       | Yes (`Channels.cases.test.tsx`)                                                       |
| CHN-35 | Phone width (375 px)                                         | Responsive    | P3       | No                                                                                    |
| CHN-36 | Arabic / RTL                                                 | RTL           | P3       | No                                                                                    |
| CHN-37 | Light and dark theme                                         | Theme         | P3       | No                                                                                    |
| CHN-38 | Keyboard and screen reader basics (list, modal, grid)        | Accessibility | P2       | Yes (`Channels.cases.test.tsx`)                                                       |

## Cases

Shared fixture (`C1`): `{ "id": "c1", "number": 1, "name": "Quran", "slug": "quran", "category": "Religion", "mode": "schedule", "enabled": true, "sort_order": 1, "transition": "cut", "mode_config": { "slots": [{ "start": "00:00", "end": "24:00", "source": { "type": "filler" } }] }, "plan": { "owned": true, "edited": false }, "now_playing": { "title": "Al-Baqara", "start_at": "<now-30m>", "end_at": "<now+30m>", "progress": 0.5 }, "guide_until": "<now+48h>" }`. `C2`: same shape, `id: "c2"`, `number: 2`, `name: "Kids"`, `category: "Family"`, `plan: null` (manual), `enabled: false`. Default mocks: `GET /api/tv/info` → `{ "xmltv_url": "http://tv.local/xmltv.xml" }`, `GET /api/filler/pools` → `{ "items": [] }`, `GET /api/rules` → `{ "sections": [] }`, `GET /api/tv/cluster` rejects. Signed in as `{ username: "admin", is_admin: true }`.

### CHN-01 — Lists channels sorted by number with all columns

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `GET /api/channels` → `{ "items": [C2, C1] }` (out of order)
- **Steps:**
  1. Open `/admin/channels`.
- **Expected:** Heading "Channels". Table labelled "Channels" with columns #, Name, Category, Now, Guide, Enabled, Actions. Row 1 is Quran (number 1), row 2 is Kids. Quran shows badge "From plan" (`channels.origin.plan`), mode label, "Al-Baqara" with a `progress` labelled with 50 %, guide "48 h". Kids shows the badge "Made by hand" and its toggle off. Each row has Edit, Preview, Delete buttons. "New channel" button and "Filler pools" link in the header.

### CHN-02 — XMLTV guide field shown, hidden when `/api/tv/info` fails

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Partial — `Channels.test.tsx` "shows the XMLTV guide address..."; failure path not tested
- **Preconditions / data:** a) default `/api/tv/info`; b) `/api/tv/info` → 500; c) → `{ "xmltv_url": "" }`; d) → `{ "xmltv_url": null }`
- **Steps:**
  1. Open `/admin/channels` for each case.
- **Expected:** a) Copy field labelled "XMLTV guide" with the URL and a copy button. b, c, d) No guide field, no error toast, the rest of the page renders normally.

### CHN-03 — `items: []` shows "No channels yet"

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes — `Channels.test.tsx` "shows an empty state when there are no channels"
- **Preconditions / data:** `GET /api/channels` → `{ "items": [] }`
- **Steps:**
  1. Open `/admin/channels`.
- **Expected:** EmptyState "No channels yet" / "Create a channel to start building a live lineup." No filters, no table. "New channel" still works and opens the editor with number 1.

### CHN-04 — `items: null` or missing key shows empty state, no crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** a) `GET /api/channels` → `{ "items": null }`; b) → `{}`
- **Steps:**
  1. Open `/admin/channels` for each case.
  2. Click "New channel".
- **Expected:** "No channels yet" empty state, no thrown error, no blank page. The editor opens with number 1 (`nextNumber` handles the empty list).

### CHN-05 — Nullable channel fields render fallbacks

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `GET /api/channels` → one channel `C1` with `now_playing: null`, `plan: null`, `category: null`, `guide_until: null`, `logo_url: null`, `logo_path: null`, `mode_config: null`, `art: null`, `draft: null`; second case with `now_playing: { "title": "", "progress": null, ... }` and `guide_until: "not-a-date"`.
- **Steps:**
  1. Open `/admin/channels`.
  2. Click Edit on the row, visit every tab (Basics, Art, Schedule, Filler, Live broadcasts, Preview).
- **Expected:** Row shows the first-letter logo placeholder, manual origin badge, category "—", "Nothing scheduled", guide "0 h" with the low-coverage style. Empty now-title falls back to the filler label (`channels.previewFiller`). The editor opens; Schedule shows "The schedule has no slots yet: add at least one."; Art tab renders tiles with "none" and no watermark settings block (it needs `art`). No console errors.

### CHN-06 — Channel with `name: null` does not blank the page

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `GET /api/channels` → `{ "items": [{ ...C1, "name": null, "logo_url": null }] }`
- **Steps:**
  1. Open `/admin/channels`.
  2. Type "q" in "Search channels".
- **Expected:** The list renders (empty name or a placeholder) and searching does not throw. (currently unhandled — likely bug: `ChannelList.tsx:165` calls `c.name.slice(0, 1)` and `Channels.tsx:96` calls `c.name.toLowerCase()`, both throw on null and blank the route.)

### CHN-07 — List load fails (500, network, malformed JSON)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `GET /api/channels` → a) 500 `application/problem+json` `{ "title": "Internal error", "status": 500 }`; b) fetch rejects (`TypeError: Failed to fetch`); c) 200 with body `{not json` and `Content-Type: application/json`; d) 403 problem (session lost admin)
- **Steps:**
  1. Open `/admin/channels` for each case.
- **Expected:** An error state with the problem title (or "Something went wrong.") and a "Try again" action that refetches. (currently unhandled — likely bug: `Channels.tsx:71` catches every failure as `setChannels([])`, so the admin sees "No channels yet" and may create duplicates thinking the lineup is empty.)

### CHN-08 — Loading text while the list is pending

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/channels` returns a never-resolving promise
- **Steps:**
  1. Open `/admin/channels`.
- **Expected:** "Loading…" (`common.loading`) only; no header buttons yet; no crash when the promise later resolves after unmount.

### CHN-09 — Non-admin sees "Admins only" and no channel requests fire

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** signed in as `{ username: "bob", is_admin: false }`
- **Steps:**
  1. Open `/admin/channels`.
- **Expected:** ErrorState "Admins only" / "You need administrator access to view this page." No request to `/api/channels`, `/api/tv/info`, `/api/rules`. Sidebar does not show the admin "Channels" link.

### CHN-10 — Signed-out user is redirected to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** no session (`useAuth` → `user: null`, not loading)
- **Steps:**
  1. Open `/admin/channels` directly.
- **Expected:** Redirect to `/login` (with the return path kept as `RequireAuth` does). After signing in as admin, the user lands back on `/admin/channels`.

### CHN-11 — Search, category and origin filters; "no match" text

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `{ "items": [C1, C2, { ...C1, "id": "c3", "number": 12, "name": "News", "category": "News", "plan": { "owned": true, "edited": true } }] }`
- **Steps:**
  1. Type "12" in "Search channels"; then "baqara"; then "zzz".
  2. Clear search; pick category "Family" in the Category select.
  3. Reset category; pick each origin in the Origin select.
- **Expected:** Number search keeps News; now-title search keeps Quran; "zzz" shows "No channel matches these filters." While any filter is on, the hint reads "Showing {n} of 3 channels. Clear the filters to reorder." and every row handle is disabled with the clear-filter title. Category options are the sorted distinct non-empty categories plus "all". Origin "plan" shows Quran, "edited" shows News, "manual" shows Kids. Clearing all filters restores the reorder hint and enables handles.

### CHN-12 — On/off toggle: optimistic, plan warning, error toast

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `{ "items": [C1, C2] }`; `PATCH /api/channels/c2` → 200; `window.confirm` mocked
- **Steps:**
  1. Toggle Kids (manual) on.
  2. Toggle Quran (plan) off; answer Cancel on the confirm; repeat and answer OK.
  3. Make `PATCH /api/channels/c1` → 500 `{ "title": "Boom" }` and toggle again.
- **Expected:** 1) No confirm; switch flips at once; `PATCH /api/channels/c2` body exactly `{ "enabled": true }`; list reloads. 2) Confirm text "Quran will stop following the lineup plan. Continue?"; Cancel sends nothing and the switch stays; OK sends the PATCH. 3) Error toast "Boom"; after the reload the switch shows the server's value.

### CHN-13 — Reorder by drag and keyboard; reorder error

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `{ "items": [C2(number 1, manual), {...C2, id: "c3", number: 5, name: "Docs"}] }`; `POST /api/channels/reorder` → 204
- **Steps:**
  1. Focus the handle "Move Kids" (`channels.list.handle`) and press ArrowUp (first row), then ArrowDown.
  2. Drag the Docs handle onto row 1.
  3. Make reorder → 409 `{ "title": "Conflict" }` and press ArrowDown again.
- **Expected:** ArrowUp on the first row does nothing. ArrowDown posts `[{ "id": "c3", "number": 1 }, { "id": "c2", "number": 5 }]` (same set of numbers, only changed ones), the live region says "Kids moved to position 2.", focus stays on the moved handle. Drag posts the same kind of body. On error a toast shows "Conflict" and the list reloads to the server order. Plan channels in the moved set trigger the plan confirm first.

### CHN-14 — Delete a channel: confirm, cancel, error

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `{ "items": [C1, C2] }`; `DELETE /api/channels/c2` → 204
- **Steps:**
  1. Click Delete on Kids, Cancel the confirm.
  2. Click Delete on Kids, OK.
  3. Make DELETE → 404 `{ "title": "Not found" }` and delete Quran.
- **Expected:** Confirm text `Delete channel "Kids"?`. Cancel sends nothing. OK sends `DELETE /api/channels/c2` and the list reloads without Kids. 404 shows a toast "Not found" and the row stays.

### CHN-15 — New channel form validation blocks Save

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`; the 2.5 refusal is it.fails: bug)
- **Preconditions / data:** `{ "items": [C1, C2] }`
- **Steps:**
  1. Click "New channel" (dialog "New channel", Number prefilled 3, Enabled off).
  2. Leave Name empty; type only spaces.
  3. Set Number to 0, then empty, then 2, then 3.
  4. Open the Schedule tab with no slots; add a slot.
- **Expected:** Name shows "Name is required." while empty or whitespace. Number 0 / empty shows "Enter a channel number above 0."; 2 shows "Number already used by Kids."; 3 is clean. The Basics tab label carries " •" while either error is present; the Schedule tab carries " •" and the warning "The schedule has no slots yet: add at least one." Save stays disabled until all are fixed; Save becomes enabled after a name, a free number and one valid slot. A fractional number (2.5) should be refused too (currently accepted client-side — likely bug).

### CHN-16 — Create a channel; pending art uploads after create

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `POST /api/channels` → `{ ...C2, "id": "c9" }`; `POST /api/channels/c9/art/logo` → 200 first run, 500 `{ "title": "Too big" }` second run
- **Steps:**
  1. New channel, fill Name "Docs", add a slot, pick a 10 KB PNG on the Art tab Logo tile ("Choose …").
  2. Save.
- **Expected:** Body of `POST /api/channels` contains only create fields (`name`, `number`, `category`, `mode`, `mode_config`, `transition`, `enabled`, no `id`, `art`, `now_playing`). Then the logo uploads to `/api/channels/c9/art/logo`. Dialog closes and the list reloads with Docs. When the upload fails, toast "The Logo could not be uploaded: Too big" (kind label from `channels.art.kind.logo`) and the channel is still created.

### CHN-17 — Edit sends only changed fields; Save disabled when unchanged

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `{ "items": [C2] }`; `PATCH /api/channels/c2` → 200
- **Steps:**
  1. Click Edit on Kids. Dialog title "Edit Kids".
  2. Check Save without changes.
  3. Change Category to "Cartoons", change it back, then change Transition to crossfade.
  4. Save.
- **Expected:** Save is disabled while nothing differs (including after changing a value back). Body is exactly `{ "transition": "crossfade" }`. Dialog closes, list reloads. The "changes take effect at the next boundary" note (`channels.nextBoundaryNote`) is shown for an existing channel.

### CHN-18 — Plan / edited banners and Back to plan

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** C1 (plan); C3 = `{ ...C1, id: "c3", plan: { owned: true, edited: true } }`; `POST /api/channels/c3/follow-plan` → 202 `{ "queued": true }`, then 500 `{ "title": "Down" }`
- **Steps:**
  1. Edit Quran: read the banner, change its name, read the banner and the Save label.
  2. Close, edit C3, click "Back to plan", Cancel then OK; repeat with the 500.
- **Expected:** Quran banner "This channel follows the lineup plan..." turns into the warning "You changed this channel's settings..." after the edit and Save reads "Save and stop following the plan". Changing only art or live rows keeps the plain banner. C3 shows "You edited this channel..." with "Back to plan"; Cancel sends nothing; OK posts follow-plan, toasts "The channel follows the plan again...", closes and reloads. On 500 a toast "Down" and the dialog stays.

### CHN-19 — Save errors: 409 stale, 400 detail, network

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** edit C2, change name; `PATCH /api/channels/c2` → a) 409 problem; b) 400 `{ "title": "Bad Request", "detail": "number taken" }`; c) 400 with no detail; d) fetch rejects
- **Steps:**
  1. Click Save for each case.
- **Expected:** a) toast "Your copy is stale — reload and try again."; b) toast "number taken"; c) toast "Bad Request"; d) toast "Something went wrong.". In every case the dialog stays open with the typed values, Save is enabled again (not stuck in saving), and the list is not reloaded.

### CHN-20 — Unsaved changes on Cancel / Escape / backdrop / X

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** No (it.fails: bug) — `Channels.cases.test.tsx`; the no-changes close passes
- **Preconditions / data:** edit C2; change Name to "Kids 2" and add a slot
- **Steps:**
  1. Press Cancel. Reopen, change again, press Escape. Repeat with a click on the backdrop and with the dialog's "Close" (X) button.
  2. Repeat with no changes.
- **Expected:** With changes: a "discard changes?" confirm appears; Cancel on it keeps the dialog and the values; OK closes without any request. Without changes: closes immediately. (currently unhandled — likely bug: `ChannelEditor.tsx:379` passes `onClose` straight to `Modal`, which is `dismissable` by default, so a stray backdrop click or Escape silently drops every edit including grid work.)

### CHN-21 — Unsaved section drafts lost on tab switch

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No (it.fails: bug) — `Channels.cases.test.tsx`
- **Preconditions / data:** edit C1 with `art` set (watermark `{ position: "top-right", width_pct: 10, margin_pct: 2 }`)
- **Steps:**
  1. Art tab: change the watermark width to 20 (do not press Apply); change the strip height (do not press "Save strip" / `channels.strip.save`).
  2. Switch to Basics and back to Art.
  3. Schedule tab: select a slot, type in the source search; switch tabs and back.
- **Expected:** The section drafts survive a tab switch, or the user is warned before they are dropped. (currently unhandled — likely bug: `Tabs` renders only the active panel, so `WatermarkSettings`, `StripSection` and `SlotEditor` local state is thrown away silently; the main Save does not include them either.)

### CHN-22 — Schedule slot validation and the cross-tab warning

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** edit C2 with `mode_config.slots: [{ start: "08:00", end: "10:00", source: { type: "series" } }, { start: "09:00", end: "11:00", source: { type: "filler" } }]`
- **Steps:**
  1. Schedule tab: click the 08:00 block (button labelled via `channels.grid.blockLabel`).
  2. Read the slot problems; pick a series from the search; set End to 07:00; set it back.
  3. Switch to Basics.
- **Expected:** Slot problems list (`role="alert"`) shows "Pick what the slot plays." and the overlap warning "This slot overlaps another on the same day: the earlier one wins."; both blocks have the clash style. While the source is missing or End is before Start ("The slot must end after it starts."), Save is disabled, the Schedule tab label has " •", and on other tabs the warning "A slot of the schedule needs fixing." shows. An overlap alone does not disable Save. Split is disabled for a slot shorter than 2 minutes.

### CHN-23 — `mode_config.slots` / `ramadan` null or wrong type

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** edit a schedule channel with each `mode_config`: a) `{ "slots": null }`; b) `{}`; c) `{ "slots": [], "ramadan": null }`; d) `{ "slots": [], "ramadan": { "slots": null } }`; e) `{ "slots": [{ "start": "08:00", "end": "09:00", "days": null, "source": null }] }`; f) `{ "slots": {} }` (object, not array)
- **Steps:**
  1. Open the Schedule tab; click "Add slot"; in d) switch to the Ramadan grid.
- **Expected:** a–d) Empty grid with "The schedule has no slots yet: add at least one.", "Add slot" works, Ramadan tab shows an empty grid. e) The slot is drawn on all seven days as filler. f) No crash; treated as no slots. (f is currently unhandled — likely bug: `ScheduleSection.tsx:51` casts any non-null value to `Slot[]` and `isWeekly` calls `.some` on it.)

### CHN-24 — Non-grid modes: marathon needs a series, shuffle, smart mix

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** new channel; `GET /api/channels/sources?kind=series...` → `{ "items": [{ "kind": "series", "id": "s1", "name": "Omar", "count": 30, "duration_ms": 108000000 }] }`
- **Steps:**
  1. Set Mode to marathon; open Schedule tab; pick "Omar".
  2. Switch Mode to shuffle, then smart mix.
- **Expected:** Marathon without a series shows "Pick the series this channel plays." and Save is disabled; after picking, "Picked: Omar" and Save enables (once name/number are fine). Shuffle shows its hint; smart mix shows a daypart profile input. The mode help text under the Mode select changes with the mode.

### CHN-25 — Filler section: weights, missing pool, max gap validation

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** pools `{ "items": [{ "id": "p1", "name": "News clips", "items": [] }] }`; channel `mode_config.filler: { "lists": [{ "pool": "gone", "weight": 50 }, { "pool": "p1", "weight": 150 }] }`
- **Steps:**
  1. Filler tab. Read the lists.
  2. Type 5000 in the weight number for News clips.
  3. Set "Longest filler gap (minutes)" (`channels.filler.maxGap`) to 300, then -1.
  4. Try Save.
- **Expected:** "gone" shows "Pool not found"; shares read 25 % and 75 %. Weight clamps to 1000. Max gap shows "Between 0 and 240 minutes." and Save is disabled while it is invalid. (currently unhandled — likely bug: `maxGapError` in `FillerSection.tsx:91` is not part of `blocked` in `ChannelEditor.tsx:165`, so the bad value is saved.) With no lists: "No filler lists: gaps use the slots' pools, then the slate."

### CHN-26 — Filler pools and source search: null lists and errors

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** a) `GET /api/filler/pools` → `{ "items": null }`, `{}`, and `{ "items": [{ "id": "p1", "name": "A", "items": null }] }`, and 500; b) `GET /api/channels/sources` → `{ "items": null }`, `{}`, 500, fetch rejects; hit with `"detail": null`
- **Steps:**
  1. Open the editor, Filler tab; type in "Search pools".
  2. Schedule tab, select a slot, choose "Series"; type "zz" in the search.
- **Expected:** a) "No more pools to add." for null/missing/500 with no crash; pool with `items: null` shows 0 clips; the slot "filler pool" select still renders with only "none". b) Null/missing items show "Nothing to pick from yet." (or "Nothing matches “zz”." after typing); 500 / reject shows "Something went wrong."; loading shows "Loading…" between requests. A pool with `name: null` must not crash the search (currently `FillerSection.tsx:76` calls `p.name.toLowerCase()` — likely bug).

### CHN-27 — Live broadcasts: validation, renumbering, rules load failure

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** C2 (number 2); `GET /api/rules` → `{ "sections": [{ "key": "tv", "fields": [{ "name": "live", "value": [{ "channel": 2, "url": "https://x/live", "title": "News" }, { "channel": 7, "url": "https://y" }] }] }] }`; `PATCH /api/rules/tv` → 200
- **Steps:**
  1. Live tab: add a row; leave the address empty; type "ftp://a"; type a valid https URL.
  2. Set "minutes" to 0.
  3. Basics: change Number to 3; Save.
  4. Variants: `GET /api/rules` → 500; → `{ "sections": null }`; → tv.live empty and `dizquetv.live` has rows.
- **Expected:** Only channel 2's rows are shown. Empty address "Enter the stream address."; bad scheme "Enter a full address, starting with https://"; minutes 0 shows the minutes error; the Live tab label carries " •" and Save is disabled while any of this channel's rows is invalid. Saving sends the PATCH channel body and then `PATCH /api/rules/tv` with all rows, this channel's rows renumbered to 3 and channel 7's row untouched. Rules 500 or `sections: null` shows "Live broadcasts could not be loaded." and the rest of the editor works. Inherited case shows the `channels.live.inherited` note.

### CHN-28 — Live save fails after a new channel was created

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** new channel with a valid slot and one live row; `POST /api/channels` → `{ "id": "c9", ... }`; `PATCH /api/rules/tv` → 500 `{ "title": "Rules locked" }`
- **Steps:**
  1. Save. Then press Save again.
- **Expected:** Toast "The live broadcasts could not be saved: Rules locked". The second Save must not create a second channel; it should retry only the live rows (or the dialog should switch to editing c9). (currently unhandled — likely bug: `ChannelEditor.tsx:192-208` returns after the toast with the dialog still in "new" mode, so the next Save posts `/api/channels` again and either duplicates the channel or fails on the number; the list is also not reloaded, so c9 is invisible until refresh.)

### CHN-29 — Art upload / revert / watermark errors

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** edit C1 with `art: { logo: { url: "/a.png", overridden: true }, watermark: { position: "top-right", width_pct: 10, margin_pct: 2, overridden: false }, loading: { overridden: false } }`; upload → 413 `{ "title": "Payload Too Large", "detail": "max 5 MB" }`; revert → 500 `{ "title": "Down" }`; watermark PATCH → network reject
- **Steps:**
  1. Logo: choose a 1 MB PNG. Then "Back to plan" (`channels.art.remove`).
  2. Watermark: set width 0, then 60, then 20; margin 30, then 5; Apply.
- **Expected:** Upload error alert under the tile reads "max 5 MB"; progress bar disappears; Choose is enabled again. Revert alert "Down". Width 0/60 shows "Between 1 and 50."; margin 30 "Between 0 and 25."; Apply disabled while invalid or unchanged. Network failure alert "Something went wrong.". A loading-screen SVG is refused with "That file type is not accepted here." (SVG is logo-only).

### CHN-30 — Strip settings: validation, save, reset, errors

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Partial — `StripPreview.test.tsx` "previews the channel's real now / next and saves only the changes", "refuses an unknown time zone..."; `StripColors.test.tsx` hex refusal
- **Preconditions / data:** edit C1 (art present, `art.strip: null`); `GET /api/channels/now` → `{ "channels": null }` in one run; `PATCH /api/channels/c1/strip` → 422 `{ "title": "Invalid", "detail": "height_pct out of range" }`
- **Steps:**
  1. Art tab, strip area: check "Save strip" is disabled before any change.
  2. Height 30; font size empty; colour field "#12"; clock zone "Mars/Base"; add clocks until 8.
  3. Fix all, change height to 10, Save; then Reset.
- **Expected:** Range errors "From 4 to 25." and the font one; colour "A colour as #RRGGBB, such as #0b5d3b."; zone "Not a time zone (use names such as Africa/Cairo)."; Save disabled while any error. "Add clock" disappears at 8 clocks. `channels: null` from `/now` falls back to the sample lines without a crash. API 422 shows an alert "height_pct out of range". Reset is disabled when the channel has no own strip and nothing was touched; Reset sends `DELETE /api/channels/c1/strip`.

### CHN-31 — Prefer host select: null hosts, pin failure

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** edit C1 (has `slug`); `GET /api/tv/cluster` → a) `{ "hosts": null, "settings": { "pins": null } }`; b) `{ "hosts": [{ "name": "box1" }], "settings": null }`; c) `{ "hosts": [{ "name": "box1" }], "settings": { "pins": { "quran": ["gone-host"] } } }`; `PATCH /api/rules/tv_cluster` → 500
- **Steps:**
  1. Basics tab for each case; in c) pick "box1".
- **Expected:** a, b) No crash; either the select is hidden or shows only "Automatic". c) Select "Prefer host" lists Automatic, box1 and gone-host (selected). Pin failure shows the toast "Could not save the preferred host: …" (`tv.cluster.pin.failed`) and the select is enabled again. A new channel (no slug) shows no select.

### CHN-32 — Preview tab: loading, empty, null blocks, regenerate

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** click "Preview" on Quran's row (opens editor on the Preview tab); `GET /api/channels/c1/guide?...` → a) pending; b) `{ "channel": {...}, "blocks": [] }`; c) `{ "channel": {...}, "blocks": null }`; d) 500; e) two consecutive filler blocks then a program; `POST /api/channels/c1/regenerate` → 500 `{ "title": "Busy" }`
- **Steps:**
  1. Open each case; click "Refresh"; click "Regenerate", Cancel, then OK.
- **Expected:** a) "Loading…"; b, c, d) "No programs scheduled in this window." (d should ideally say the load failed — currently it shows the empty text). e) One filler row for the two blocks, grouped under a day heading. Regenerate Cancel sends nothing; OK with 500 toasts "Busy" and the button is enabled again; success toasts "The schedule will be rebuilt within a few minutes.". New channels have no Preview tab.

### CHN-33 — Links: Filler pools, TV draft change; Back returns to list

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `{ "items": [{ ...C1, "draft": { "change_id": "ch7", "number": 4 } }] }`
- **Steps:**
  1. Click "Filler pools" in the header; press browser Back.
  2. Edit Quran; read the draft banner (`data-testid="tv-draft-banner"`); click its link.
  3. Press browser Back; press Forward.
  4. Reload `/admin/channels` with a filter typed.
- **Expected:** 1) `/admin/filler` renders; Back shows the channel list again. 2) Banner "Editing draft change #4: ..." with link to `/admin/live-tv/changes/ch7`. 3) Back returns to `/admin/channels` (the dialog is closed, list loaded); Forward goes to the change page again. 4) Filters reset after reload (they are not in the URL).

### CHN-34 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** start on `/admin/filler` (or `/`); `GET /api/channels` → `{ "items": [{ ...C1, "name": null, "logo_url": null }] }` (throws in `ChannelList`), or mock `ChannelList` to throw
- **Steps:**
  1. Navigate to `/admin/channels` via the sidebar.
  2. Press browser Back; then click the "Channels" sidebar link again after fixing the mock; also click another nav link from the fallback.
- **Expected:** A route-level error fallback (not a blank page) with the shell and nav still visible. Back renders the previous page without a reload; nav links work from the fallback; returning to `/admin/channels` with good data renders the list.

### CHN-35 — Phone width (375 px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375 × 812; three channels; edit one with slots
- **Steps:**
  1. Open `/admin/channels`; scroll the list; open the editor and every tab.
- **Expected:** No horizontal page scroll (`document.documentElement.scrollWidth <= 375`); the table scrolls inside its own container if wide; header buttons wrap; the dialog fits the screen, its tabs scroll or wrap, Save/Cancel are reachable; the 7-day grid is usable (scrolls inside its box); mobile nav reaches the admin pages.

### CHN-36 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** language Arabic; channels with Arabic names (`"name": "القرآن الكريم"`)
- **Steps:**
  1. Open `/admin/channels`; open the editor.
- **Expected:** `<html dir="rtl" lang="ar">`; heading "القنوات", button "قناة جديدة"; numbers formatted through `n()` (Arabic digits if the locale formats so) without grouping for channel numbers; Arabic names render correctly; table and dialog mirrored; the grid hour gutter and blocks use logical insets; ArrowUp/ArrowDown still move rows up/down. Confirm texts are Arabic (`channels.confirmDelete`).

### CHN-37 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `<html data-theme="light">` then `"dark"`
- **Steps:**
  1. View the list, the editor, grid blocks of each source kind, the low-coverage guide badge, the plan warning banner.
- **Expected:** All text readable in both themes; block colours, banners and badges use theme tokens (no hardcoded black/white text on same-colour backgrounds); the strip preview's own colours are the strip's settings, not the theme.

### CHN-38 — Keyboard and screen reader basics (list, modal, grid)

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Channels.cases.test.tsx`)
- **Preconditions / data:** `{ "items": [C1, C2] }`
- **Steps:**
  1. Tab through the page: search, selects, row handles, toggles, Edit/Preview/Delete.
  2. Open the editor with Enter; Tab through; press Escape.
  3. In Tabs use ArrowRight/ArrowLeft; on the grid Tab to a block and press Enter.
- **Expected:** Visible focus on every control. Handles have names "Move Quran" (`channels.list.handle`), toggles have `channels.list.enable` names, the order column header has an aria-label. Dialog has `role="dialog"`, `aria-modal`, its title, traps focus and returns focus to the Edit button on close. Tabs move with arrows. Grid blocks are buttons with full labels (day, start, end, what) and `aria-pressed`. Slot problems and art errors are `role="alert"`; reorder announcements go to the polite live region.
