# Settings: Rules tab

- **Route:** `/settings/rules` (section tabs are local state, not in the URL)
- **Component:** `src/pages/Settings/RulesSection.tsx` (`RulesSection`, `RuleRow`, `localizeSections`); children: `src/pages/Settings/RuleEditors.tsx` (`Editor`, `NumberEditor`, `TimeInput`, `LinesEditor`, `ChipsEditor`, `DayChips`, `TableEditor`), `src/pages/Settings/ruleKinds.ts` (`controlOf`, `valueProblem`, `cellProblems`, `sliderRange`, `subGroups`, `weekdayNames`), `src/pages/Settings/StripRulesTools.tsx` (section `tv_strip`), `src/pages/Admin/RemoteUsage.tsx` (section `tv_remote`), `src/components/queue/PlanPreviewPanel.tsx` (section `planner`), `@ds/components/Tabs/Tabs`.
- **API calls (`src/lib/rulesApi.ts`, server `api/internal/handlers/rules/rules.go`):**
  - `GET /api/rules` via `rulesApi.list()` → `{ sections: RuleSection[] }`, `RuleSection = { key, title, what, unknown?: string[], fields: RuleField[] }`, `RuleField = { name, kind, group, label, help, default, value, changed, invalid?, minimum?, maximum?, choices?: string[], columns?: string[], column_kinds?: string[], time_columns?: number[], column_optional?: boolean[] }`.
  - `PATCH /api/rules/{section}` via `rulesApi.patch(section, set, reset)` body `{ set: {name: value}, reset: [names] }` → `{ section, changed: string[], draft_change?: { id, number } }`; 400 `{ error: "<field>: <message>", field }`; 403 for non-admins. `rulesError()` maps the error to `{ message, field }`.
  - Children: `PlanPreviewPanel` uses `GET /api/reports/channel-plan/preview` and `POST` same path `{ kind: "preview" }`; `RemoteUsage` uses `tvRemoteApi` (own tests).
- **Access:** signed-in user (`RequireAuth`). Everyone can view; editing only when `user.is_admin` (`canEdit`). Non-admins see "Only admins can change the rules. You can view them here." (`settings.rules.readOnly`). Server re-checks (403).
- **Existing tests:** `src/pages/Settings/RulesSection.test.tsx` (fixture `src/test/fixtures/rules.json`: sections `planner` "Channels", `groups` "Grouping", `detect` "Detection"; helper `withTv()` adds a `tv` "Maktaba TV" section) — covers: tab per section and grouped fields with label/name/help/default; saving only the edited field and keeping other-tab edits; draft link (Epic 33); Back to default of a saved change; 400 shown under its field and cleared on edit; number minimum check blocks save; adding a row to an empty table; one-per-line list (trailing space kept); search by name/label/help/group and "No rule matches"; invalid saved value banner and "Remove them" for unknown names; read-only for non-admin (spinbutton readonly, switch/select disabled, no Save/Add row); plan preview only on Channels and not for non-admin; time picker for HH:MM text; weekday chips; slider with unit; table cell checks, url error, "Runs past midnight", duplicate/remove row, optional cells left out; required time in a table; unsaved edit undo without request; folding groups and sub-groups. `src/pages/Settings/localizeRules.test.ts` — covers `localizeSections` Arabic for TV output, English fallback, catalog English equals schema, every tv_cluster/tv_remote field has en+ar copy. Gaps: load errors and empty/null shapes, Discard, PATCH 403/500/network, non-field errors, chips editor, url/colour/select/whole/required validation outside tables, Expand all, render crash, Arabic UI on the page, tab keyboard/RTL, unsaved-edit loss on leave.

Use the existing harness: `setup(data)` / `renderAt(<RulesSection />, "/settings/rules")` with `routeGet(get, { "/api/rules": () => data, "/api/reports/channel-plan/preview": () => idleStatus })`, and `rule(label)` = `findByRole("group", { name: label })`.

## Summary

| ID     | Title                                                         | Category      | Priority | Covered by existing test?                                             |
| ------ | ------------------------------------------------------------- | ------------- | -------- | --------------------------------------------------------------------- |
| SRL-01 | Renders a tab per section, first tab selected, grouped fields | Happy path    | P1       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-02 | Save sends only edited fields, shows saved notice             | Happy path    | P1       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-03 | Save going to a TV change draft links to it                   | Happy path    | P2       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-04 | Back to default for a saved change                            | Happy path    | P1       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-05 | Discard changes drops all edits in the section only           | Happy path    | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-06 | Loading state while rules load                                | Loading       | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-07 | Load fails (401 / 500 / network / bad JSON)                   | Error         | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-08 | `sections: []`                                                | Empty state   | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-09 | `sections: null` or missing                                   | Empty state   | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-10 | A section with `fields: null` or missing: crash               | Empty state   | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-11 | Null / missing nested lists on a field                        | Empty state   | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-12 | Server 400 shown under its field                              | Validation    | P1       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-13 | PATCH 403 / 500 / network without a field                     | Error         | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-14 | Reload after a successful save fails                          | Error         | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-15 | Number validation: required, whole, min, max                  | Validation    | P1       | Yes (`RulesSection.test.tsx`, `RulesSection.cases.test.tsx`)          |
| SRL-16 | Time, URL, colour and select validation on single fields      | Validation    | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-17 | Table cells: typed checks, add/duplicate/remove rows          | Validation    | P1       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-18 | Map table (key / values) edit                                 | Validation    | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-19 | Chips editor: add by Enter/comma, dedupe, Backspace, remove   | Validation    | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-20 | Save disabled with nothing to save or while busy              | Validation    | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-21 | Invalid saved values and unknown names banners                | Happy path    | P2       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-22 | Search filters fields and opens folded groups                 | Navigation    | P2       | Yes (`RulesSection.test.tsx`)                                         |
| SRL-23 | Fold, Expand all / Collapse all                               | Navigation    | P3       | Partial (`RulesSection.test.tsx`: single fold only)                   |
| SRL-24 | Non-admin is read-only everywhere                             | Permissions   | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-25 | Signed-out deep link redirects to /login                      | Permissions   | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-26 | Section tab and unsaved edits on navigation / reload          | Navigation    | P2       | No (it.fails: bug)                                                    |
| SRL-27 | Render crash shows route error fallback; Back works           | Error         | P1       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-28 | Arabic: localized schema, RTL layout, Arabic weekdays         | RTL           | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-29 | Tabs keyboard: arrows, Home/End, mirrored in RTL              | Accessibility | P2       | Yes (`RulesSection.cases.test.tsx`; it.fails: RTL arrows in @ds Tabs) |
| SRL-30 | Labels, descriptions and live regions                         | Accessibility | P2       | Yes (`RulesSection.cases.test.tsx`)                                   |
| SRL-31 | Phone width: tables and bar fit                               | Responsive    | P2       | No (e2e-only)                                                         |
| SRL-32 | Both themes: changed/unsaved/error marks readable             | Theme         | P3       | No                                                                    |

## Cases

### SRL-01 — Renders a tab per section, first tab selected, grouped fields

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `RulesSection.test.tsx` "renders a tab per section and the fields grouped, with label, name, help and default"
- **Preconditions / data:** admin; fixture.
- **Steps:**
  1. Open `/settings/rules`.
- **Expected:** `h2` "Rules" and intro text. Tabs "Channels" (selected), "Grouping", "Detection". Group heading "Grid and slots". Row group "Shortest slot (min)" shows `min_slot_minutes`, "Default: 30", spinbutton value 30 with `min="5"`.

### SRL-02 — Save sends only edited fields, shows saved notice

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — "saves only the edited field and keeps edits made in another tab"
- **Preconditions / data:** PATCH → `{ "section": "planner", "changed": ["min_slot_minutes"] }`.
- **Steps:**
  1. Edit "Shortest slot (min)" to 45; click "Save".
- **Expected:** `PATCH /api/rules/planner` `{ set: { min_slot_minutes: 45 }, reset: [] }`. `role="status"` "Saved. 1 rule(s) differ from the defaults." Tab label "1 changed". Edits in other tabs stay pending.

### SRL-03 — Save going to a TV change draft links to it

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — "a save that went to a TV change draft links to it (Epic 33)"
- **Preconditions / data:** PATCH → `{ ..., "draft_change": { "id": "c0000000-0000-4000-8000-000000000043", "number": 43 } }`.
- **Steps:**
  1. Save an edit.
- **Expected:** "Saved to draft change #43: …" and link "Open the change" (`data-testid="rules-draft-link"`) to `/admin/live-tv/changes/c0000000-...043`. It is a plain `<a>` (full page load by design).

### SRL-04 — Back to default for a saved change

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — "puts a changed field back to its default"
- **Preconditions / data:** `min_slot_minutes` `value: 45, changed: true`.
- **Steps:**
  1. Click "Back to default" in that row.
- **Expected:** `PATCH /api/rules/planner` `{ set: {}, reset: ["min_slot_minutes"] }`; notice "Back to the default."; value 30; button gone.

### SRL-05 — Discard changes drops all edits in the section only

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** admin; fixture.
- **Steps:**
  1. On "Channels" set "Shortest slot (min)" to 3 (invalid, below 5) and "Slot step (min)" to 15; on "Grouping" set "Playlist group from (videos)" to 7.
  2. Back on "Channels" click "Discard changes".
- **Expected:** No request. Channels fields back to saved values (30, 30), "unsaved" marks gone, live counter text empty, Save and Discard disabled, any "Fix N setting(s)" banner and server errors for that section cleared. The "Grouping" tab still says "1 unsaved". A half-typed value (e.g. `1.` in a float field) is also cleared (editors remount via `epoch`).

### SRL-06 — Loading state while rules load

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `GET /api/rules` pending.
- **Steps:**
  1. Open `/settings/rules`.
- **Expected:** `h2` "Rules" and `role="status"` "Loading…". No tabs, no Save button.

### SRL-07 — Load fails (401 / 500 / network / bad JSON)

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `GET /api/rules` → 500 problem+json `{ "title": "Internal Server Error", "status": 500, "detail": "db" }`; 401 `{ "title": "Unauthorized", "status": 401 }`; fetch rejects `TypeError("Failed to fetch")`; 200 JSON content-type with body `{bad`.
- **Steps:**
  1. Open `/settings/rules`.
- **Expected:** `role="alert"` "Could not load the rules. Internal Server Error: db" (with `ApiError` the message is `title: detail`; for network "Failed to fetch"; for bad JSON the SyntaxError message). No tabs. No crash. Wanted: a retry button (none today).

### SRL-08 — `sections: []`

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `GET /api/rules` → `{ "sections": [] }`.
- **Steps:**
  1. Open `/settings/rules`.
- **Expected (wanted):** heading, intro and an empty message ("No rules"). **Current:** heading, intro and an empty tab list with no message; no crash. Record the behaviour.

### SRL-09 — `sections: null` or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `GET /api/rules` → `{ "sections": null }`; then `{}`.
- **Steps:**
  1. Open `/settings/rules`.
- **Expected (wanted):** same as `[]` (SRL-08). **Current:** `[...r.sections]` throws inside `load()`, caught, so the page shows the load error alert "Could not load the rules. r.sections is not iterable" (or similar). No blank page. Wanted: treat null as empty, not as a technical error (likely bug, low).

### SRL-10 — A section with `fields: null` or missing: crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `{ "sections": [{ "key": "planner", "title": "Channels", "what": "x", "fields": null }] }`; then the same without `fields`; then one good section plus one with `fields: null`.
- **Steps:**
  1. Open `/settings/rules`.
- **Expected (wanted):** the section tab renders with an empty message; other sections work. **Current:** `localizeSections` runs `s.fields.map` in `useMemo` during render and throws `TypeError`: whole page blank (currently unhandled — likely bug).

### SRL-11 — Null / missing nested lists on a field

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** one section with these fields, each tested alone:
  - `unknown: null` on the section.
  - `choices: null` on a `str` field (`khutbah_day`).
  - `kind: "rows"` field with `columns: null`, `column_kinds: null`, `time_columns: null`, `column_optional: null`, `default: null`, `value: null`.
  - `kind: "map"` field with `default: null`, `value: null`.
  - `kind: "list"` field (`weekend_days`) with `default: null`, `value: null`.
  - `kind: "list"` field with `value: ["a", null]`.
- **Steps:**
  1. Open `/settings/rules`; then edit each field (admin).
- **Expected:** No crash. `unknown: null` → no unknown banner. `choices: null` → shown as a text input. Rows with `columns: null` → empty table "No rows yet." and "Add row" adds an empty row without crashing. Map/list null → "No rows yet." / "none". Defaults show "Default: empty" or "Default: none".

### SRL-12 — Server 400 shown under its field

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes — "shows a 400 under its field and keeps the edit"
- **Preconditions / data:** PATCH rejects `ApiError` 400 `{ error: "min_slot_minutes: must divide the slot step", field: "min_slot_minutes" }`. Extra (not covered): no `field` but message `themes[0].number: must be > 0` → field `themes`; and a cell message `row 2 url: must be …` in a table → error under row 2, url cell.
- **Steps:**
  1. Edit, Save.
- **Expected:** Message under the field (`role="alert"`), notice "Not saved: …", the edit kept, Save enabled; editing the field clears the message. For `themes[0].number` the message is under "Themed channels". For `row 2 url:` the url cell of row 2 shows it.

### SRL-13 — PATCH 403 / 500 / network without a field

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** admin in UI but server says 403 `{ "title": "Forbidden", "status": 403, "detail": "admin only" }`; then 500 `{ "title": "Internal Server Error", "status": 500 }`; then fetch rejects.
- **Steps:**
  1. Edit "Shortest slot (min)" to 45; Save.
- **Expected:** `role="alert"` "Not saved: Forbidden: admin only" (or "Not saved: Internal Server Error" / "Not saved: Failed to fetch"). No message under any field. Edit kept, still "unsaved". Save enabled again, loading spinner gone. No reload GET.

### SRL-14 — Reload after a successful save fails

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** PATCH → 200; the next `GET /api/rules` → 500.
- **Steps:**
  1. Edit, Save.
- **Expected:** Saved notice is shown and the load-error alert "Could not load the rules. …" appears above the tabs; the form stays usable with the old values; the sent edit is cleared from "unsaved".

### SRL-15 — Number validation: required, whole, min, max

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (`RulesSection.test.tsx`, `RulesSection.cases.test.tsx`)
- **Preconditions / data:** fixture; for max use `withTv()` "Volume" (0–10).
- **Steps:**
  1. Clear "Shortest slot (min)" (int, required).
  2. Type `30.5`.
  3. Type `3`.
  4. On TV, type `11` into "Volume".
  5. Clear a `float?` (optional) field.
- **Expected:** 1: "Required." 2: "Enter a whole number." 3: "Must be at least 5." 4: "Must be at most 10." Each sets `aria-invalid="true"`, shows "Fix 1 setting(s) marked below before saving." and disables Save; the group shows a "1 to fix" badge. 5: no error, value sent as `null`. Fixing the value clears all of it.

### SRL-16 — Time, URL, colour and select validation on single fields

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `withTv()` (fields `news_start` time-as-text, `dz_art_url`); add a `tv_strip` section field `{ name: "bg_color", kind: "str", default: "#0b5d3b" }`; a `select` field whose saved value is not in `choices` (`quran_rotation` value `"random"`).
- **Steps:**
  1. Type `25:00` into "News from" (text fallback when value is `24:00`; otherwise time input).
  2. Type `aljazeera` into "Logos web address"; then clear it.
  3. Type `#12345` into the colour field.
  4. Open "Reciter rotation".
- **Expected:** 1: "Enter a time as HH:MM, from 00:00 to 24:00." `24:00` is accepted and shown as a text input. 2: "Enter a full address, such as https://…"; empty is accepted. 3: "Enter a colour as #RRGGBB, such as #0b5d3b." 4: the select lists `random` first (kept so it is visible) and shows "Choose one of the options." until a listed choice is picked.

### SRL-17 — Table cells: typed checks, add/duplicate/remove rows

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes — "adds a row to an empty table with typed cells", "checks table cells, blocks the save while one is wrong, and leaves empty optional cells out", "requires a time in a table column that cannot be left out"
- **Preconditions / data:** fixture "Themed channels"; `withTv()` "Live broadcasts".
- **Steps:**
  1. Add row, type cells; enter a bad url; fix; duplicate; remove; Save.
- **Expected:** As the existing tests: "Row 1, url: Enter a full address…", Save blocked while wrong, empty optional cells left out of the row object, "Runs past midnight" hint for end < start. Focus moves to the first input of a new row.

### SRL-18 — Map table (key / values) edit

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** fixture "Mix channel: topics per daypart" (`mix_daypart_topics`, map, default `{ "morning": ["raqaiq", "tafsir", ...], ... }`).
- **Steps:**
  1. Change the "Values (comma separated)" cell of `morning` to `tafsir, , fiqh`.
  2. Add row, leave "Key" empty, type values `x`.
  3. Add row with key `night` and values `a،b` (Arabic comma).
  4. Save.
- **Expected:** PATCH `set.mix_daypart_topics` has `morning: ["tafsir", "fiqh"]` (empty item dropped), no entry for the empty key (silently dropped — wanted: an error "Required." on the key), `night: ["a", "b"]` (key trimmed, Arabic comma splits). Columns headed "Key" and "Values (comma separated)".

### SRL-19 — Chips editor: add by Enter/comma, dedupe, Backspace, remove

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** fixture "Topics kept off the mix channel" (`mix_exclude_topics`, default `["current_affairs"]`).
- **Steps:**
  1. Type `fiqh` and press Enter.
  2. Type `hadith,` (comma adds at once).
  3. Type `fiqh` + Enter (duplicate).
  4. With the input empty press Backspace.
  5. Click the chip remove button "Remove current_affairs".
  6. Type `tafsir` and blur.
- **Expected:** Chips after each step: `current_affairs, fiqh` → `+ hadith` → unchanged (no duplicate) → `hadith` removed → `fiqh` only → `fiqh, tafsir`. Hint "Press Enter or type a comma to add. …" is the input's description. Enter does not submit anything else. Save sends `["fiqh", "tafsir"]`.

### SRL-20 — Save disabled with nothing to save or while busy

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** PATCH pending.
- **Steps:**
  1. Open the tab: check Save and Discard.
  2. Edit, click Save, click Save again and Discard and a row's "Back to default" while pending.
  3. Edit a field back to its saved value.
- **Expected:** 1: both disabled. 2: one PATCH only; Save shows loading; Discard and "Back to default" disabled while busy. 3: the field is no longer "unsaved" and Save goes back to disabled.

### SRL-21 — Invalid saved values and unknown names banners

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — "flags invalid saved values and removes unknown names"
- **Preconditions / data:** `max_slot_minutes` `invalid: "must be at least 5"`; section `unknown: ["old_rule"]`.
- **Steps:**
  1. Open the tab; click "Remove them".
- **Expected:** Row message "The saved value cannot be used, so the default applies: must be at least 5"; banner "1 saved value(s) cannot be used…"; banner "…no longer exist: old_rule." PATCH `{ set: {}, reset: ["old_rule"] }`.

### SRL-22 — Search filters fields and opens folded groups

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes — "filters fields by name, label, help or group" and the fold test
- **Preconditions / data:** fixture.
- **Steps:**
  1. Type `khutbah`, `dz_pad`, `zzz-nothing` in "Find a rule". Also type ` KHUTBAH` (case and spaces).
- **Expected:** Only matching rows; "No rule matches “zzz-nothing”."; matching is case-insensitive and trimmed. While searching, fold buttons are disabled and every group is open; "Expand all/Collapse all" is hidden. The query is kept when switching section tabs (it is shared state; note).

### SRL-23 — Fold, Expand all / Collapse all

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** Partial — "folds groups and splits a long group…" (single fold only)
- **Preconditions / data:** fixture (Channels has several groups).
- **Steps:**
  1. Click "Collapse all"; then "Expand all".
- **Expected:** All group and sub-group buttons go `aria-expanded="false"`, bodies `hidden`; button text becomes "Expand all"; then everything opens. Fold state survives switching tabs and back.

### SRL-24 — Non-admin is read-only everywhere

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `user.is_admin = false`; fixture + `withTv()` + a `tv_strip` section + section `unknown: ["old_rule"]` + a field with `changed: true`.
- **Steps:**
  1. Open `/settings/rules`; try to edit each control kind.
- **Expected:** Banner "Only admins can change the rules. You can view them here." No Save, Discard, unsaved counter, "Back to default", "Remove them", row duplicate/remove, or chip input. Chips have no remove buttons. Day chips are disabled buttons. Time and text inputs are `readonly`. Slider disabled. Strip colour editor read-only. No PATCH can be sent from the UI.

### SRL-25 — Signed-out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/settings/rules`.
- **Expected:** Redirect to `/login`; no `GET /api/rules`.

### SRL-26 — Section tab and unsaved edits on navigation / reload

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No (it.fails: bug)
- **Preconditions / data:** admin; fixture.
- **Steps:**
  1. Click the "Grouping" tab; edit "Playlist group from (videos)" to 7.
  2. Click "About" in the Settings nav, then press Back.
  3. Reload `/settings/rules`.
- **Expected:** After Back: the page reloads data and opens on "Channels" (section tab is not in the URL); the unsaved edit is lost. Wanted: warn before leaving with unsaved edits (e.g. `beforeunload` and a router blocker), and optionally keep the section in the URL (`?section=groups`). **Current:** no warning (currently unhandled — likely bug: silent loss of edits). Switching section tabs does not change the URL or history.

### SRL-27 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** full app with `AppShell`; start at `/settings/appearance`. `GET /api/rules` → `{ "sections": [{ "key": "planner", "title": "Channels", "what": "", "fields": null }] }` (see SRL-10).
- **Steps:**
  1. Click "Rules".
  2. Press browser Back; then click "Rules" again after fixing the mock; click a sidebar link.
- **Expected:** A route-level error fallback (not blank). Back shows Appearance without a reload. Re-entering Rules with good data renders the page (the boundary resets on navigation).

### SRL-28 — Arabic: localized schema, RTL layout, Arabic weekdays

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** `mkt:locale=ar`; fixture plus a `tv_output` section from the schema.
- **Steps:**
  1. Open `/settings/rules`; open the TV output tab; switch language to English from Appearance and come back.
- **Expected:** `dir="rtl"`; "القواعد", "حفظ", "تجاهل التغييرات", "ابحث عن قاعدة"; TV output title "إخراج تلفزيون مكتبة", field "مُرمِّز الفيديو" (names in `<code>` and choice values stay English). Weekday chips and "Khutbah day" options show Arabic day names; default text for day lists joined with "، ". Unit labels "دقيقة", "ثانية". Number input stays LTR-readable; Arabic text inputs use `dir="auto"`. Switching locale re-localizes without a reload and without losing unsaved edits.

### SRL-29 — Tabs keyboard: arrows, Home/End, mirrored in RTL

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`; it.fails: RTL arrows in @ds Tabs)
- **Preconditions / data:** fixture (3 tabs).
- **Steps:**
  1. Focus "Channels" tab; press ArrowRight, ArrowRight, ArrowRight, End, Home.
  2. Repeat in Arabic.
- **Expected:** LTR: Grouping → Detection → wraps to Channels; End → Detection; Home → Channels; focus follows and the panel switches; notice is cleared. RTL (wanted): ArrowLeft moves to the next tab (visual order). **Current:** `Tabs` maps ArrowRight to next regardless of `dir` (currently unhandled — likely bug in `@ds` Tabs).

### SRL-30 — Labels, descriptions and live regions

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`RulesSection.cases.test.tsx`)
- **Preconditions / data:** fixture; one invalid edit; one server error.
- **Steps:**
  1. Run axe on the page; inspect a row.
- **Expected:** Each row is `role="group"` named by its label; the control is `aria-labelledby` the label and `aria-describedby` the help and error ids. Field errors are `role="alert"`; the unsaved counter is `aria-live="polite"`; saved notice `role="status"`. Fold buttons have `aria-expanded`/`aria-controls`. Day chips have `aria-pressed` and full day names. Row action buttons are named "Duplicate row" / "Remove row". Search box named "Find a rule". No axe violations.

### SRL-31 — Phone width: tables and bar fit

- **Priority:** P2
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e 375x812; `withTv()` "Live broadcasts" with 2 rows (8 columns).
- **Steps:**
  1. Open the TV tab.
- **Expected:** Wide tables scroll inside `.mkt-rules__tablewrap` or stack cells (cells carry `data-label`); the page has no horizontal scroll; search, Discard and Save wrap; tab list scrolls or wraps.

### SRL-32 — Both themes: changed/unsaved/error marks readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** a changed field, an unsaved edit, an invalid edit; light and dark.
- **Steps:**
  1. Screenshot each theme.
- **Expected:** "changed", "unsaved", error text, warn/bad banners and badges readable in both themes; strip preview colours come from data, not theme.
