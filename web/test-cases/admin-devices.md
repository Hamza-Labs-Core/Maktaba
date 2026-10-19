# Admin devices

- **Route:** `/admin/devices` (no params, no query-string state). Not to be confused with `/account/devices` (`src/pages/Profile/ConnectedDevices.tsx`).
- **Component:** `src/pages/Cloud/Devices.tsx` (`CloudDevices` -> `AdminGate` -> `DevicesInner`; `RegisterModal`, `EditModal`, `CategoryPicker`, remove confirm `Modal`); DS `Button`, `Modal`, `Input`, `Select`, `Checkbox`, `EmptyState`, `ErrorState`, `useToast`
- **API calls** (all via `api` in `src/lib/api.ts`):
  - `GET /api/devices` -> `{ items: Device[] }` where `Device = { id, user_id, platform, bundle_id, app_version?, os_version?, locale?, categories?: string[], registered_at, last_seen_at }`. Called on mount and after every successful register / edit / remove. No polling.
  - `POST /api/devices/register` body `{ platform, token, bundle_id, categories }`
  - `PATCH /api/devices/{id}` body `{ categories, locale? }` (`locale` omitted when blank)
  - `DELETE /api/devices/{id}` (id URL-encoded)
- **Access:** signed-in admin only (`RequireAuth` -> `/login`; `AdminGate` -> "Admins only"). The data itself is scoped to the signed-in account (see `devices.note`), so it lists the admin's own devices.
- **Existing tests:** None (`Devices.test.tsx` does not exist). Gaps: everything.

Notes for the implementer: mock `../../lib/api` `api.get/post/patch/delete` and `../../lib/auth` `useAuth`; render inside `I18nProvider` + `ToastProvider` (+ `MemoryRouter` for navigation).

## Summary

| ID     | Title                                                          | Category      | Priority | Covered by existing test? |
| ------ | -------------------------------------------------------------- | ------------- | -------- | ------------------------- |
| DEV-01 | Lists devices in a table with all columns                      | Happy path    | P1       | Yes (Devices.test.tsx)    |
| DEV-02 | Non-admin sees "Admins only"; no request                       | Permissions   | P1       | Yes (Devices.test.tsx)    |
| DEV-03 | Signed-out user is redirected to /login                        | Permissions   | P1       | Yes (Devices.test.tsx)    |
| DEV-04 | Loading text before the list arrives                           | Loading       | P2       | Yes (Devices.test.tsx)    |
| DEV-05 | `items: []` shows the empty state                              | Empty state   | P2       | Yes (Devices.test.tsx)    |
| DEV-06 | `items: null` / missing, `categories: null` render safely      | Empty state   | P1       | Yes (Devices.test.tsx)    |
| DEV-07 | Missing optional / date fields and unknown platform            | Empty state   | P2       | Yes (Devices.test.tsx)    |
| DEV-08 | Load fails: error state with detail and Try again              | Error         | P1       | Yes (Devices.test.tsx)    |
| DEV-09 | Try again reloads and shows the list                           | Error         | P2       | Yes (Devices.test.tsx)    |
| DEV-10 | Register: button disabled until token and bundle ID are set    | Validation    | P1       | Yes (Devices.test.tsx)    |
| DEV-11 | Register success: POST body, toast, modal closes, list reloads | Happy path    | P1       | Yes (Devices.test.tsx)    |
| DEV-12 | Register failure keeps the modal open with an error toast      | Error         | P2       | Yes (Devices.test.tsx)    |
| DEV-13 | Register with Enter key submits only when valid                | Validation    | P2       | Yes (Devices.test.tsx)    |
| DEV-14 | Edit: pre-filled categories/locale, PATCH body, reload         | Happy path    | P1       | Yes (Devices.test.tsx)    |
| DEV-15 | Edit: clearing locale                                          | Validation    | P2       | Yes (Devices.test.tsx)    |
| DEV-16 | Edit failure shows an error toast                              | Error         | P2       | Yes (Devices.test.tsx)    |
| DEV-17 | Remove asks first; Cancel does nothing                         | Happy path    | P1       | Yes (Devices.test.tsx)    |
| DEV-18 | Remove confirm: DELETE, toast, reload; double click            | Happy path    | P1       | Yes (Devices.test.tsx)    |
| DEV-19 | Remove failure keeps the dialog open with an error toast       | Error         | P2       | Yes (Devices.test.tsx)    |
| DEV-20 | Ids with special characters are URL-encoded                    | Validation    | P2       | Yes (Devices.test.tsx)    |
| DEV-21 | Render crash shows route error fallback; Back still works      | Error         | P1       | Yes (Devices.test.tsx)    |
| DEV-22 | Nav link, Back/Forward, deep link                              | Navigation    | P2       | Yes (Devices.test.tsx)    |
| DEV-23 | Arabic: RTL table and modals                                   | RTL           | P2       | Yes (Devices.test.tsx)    |
| DEV-24 | Phone width: table does not cause page scroll                  | Responsive    | P3       | No                        |
| DEV-25 | Light and dark theme                                           | Theme         | P3       | No                        |
| DEV-26 | Dialogs trap focus, close on Escape, return focus              | Accessibility | P2       | Yes (Devices.test.tsx)    |
| DEV-27 | Table, buttons and fields have accessible names                | Accessibility | P3       | No                        |

## Cases

### DEV-01 — Lists devices in a table with all columns

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `GET /api/devices` -> `{ "items": [ { "id": "d1", "user_id": "u1", "platform": "ios", "bundle_id": "com.maktaba.app", "app_version": "2.1.0", "locale": "en", "categories": ["job"], "registered_at": "2026-09-01T10:00:00Z", "last_seen_at": "2026-10-18T09:00:00Z" }, { "id": "d2", "user_id": "u1", "platform": "android", "bundle_id": "com.maktaba.android", "registered_at": "2026-09-02T10:00:00Z", "last_seen_at": "2026-10-17T09:00:00Z" }, { "id": "d3", "user_id": "u1", "platform": "web", "bundle_id": "web", "registered_at": "2026-09-03T10:00:00Z", "last_seen_at": "2026-10-16T09:00:00Z" } ] }`
- **Steps:**
  1. Open `/admin/devices`.
- **Expected:** h1 "Devices"; "Register device" button; note "The device API is scoped to the signed-in account; this lists devices registered to you.". Table `aria-label="Devices"` with headers "Platform", "App", "App version", "Last seen", "Registered", "Actions". Rows: "iOS", "Android" (with robot icon), "Web" (globe icon); bundle IDs; app version "2.1.0" and "—" for the others; formatted dates. Each row has "Edit" and "Remove" buttons.

### DEV-02 — Non-admin sees "Admins only"; no request

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** `useAuth` -> `{ user: { is_admin: false } }`.
- **Steps:**
  1. Open `/admin/devices`.
- **Expected:** "Admins only" ErrorState; no table; no "Register device" button; `api.get` not called. Non-admins should use `/account/devices` instead.

### DEV-03 — Signed-out user is redirected to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** full app routing; auth bootstrap 401.
- **Steps:**
  1. Open `/admin/devices`.
- **Expected:** Redirected to `/login` with `state.from = "/admin/devices"`.

### DEV-04 — Loading text before the list arrives

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `GET /api/devices` never resolves.
- **Steps:**
  1. Open `/admin/devices`.
- **Expected:** "Loading…" (`.mkt-loading`); header and Register button already usable.

### DEV-05 — `items: []` shows the empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `GET /api/devices` -> `{ "items": [] }`.
- **Steps:**
  1. Open `/admin/devices`.
- **Expected:** EmptyState "No devices" / "Register a device to receive push notifications."; no table.

### DEV-06 — `items: null` / missing, `categories: null` render safely

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `GET /api/devices` -> (a) `{ "items": null }`, (b) `{}`, (c) `null` body, (d) `{ "items": [ { ...d1, "categories": null, "locale": null } ] }`.
- **Steps:**
  1. Open `/admin/devices` for each; for (d) click "Edit".
- **Expected:** (a), (b): "No devices" empty state (code does `r.items ?? []`). (c): `r.items` on `null` throws inside `.then` and is caught, so an ErrorState "Something went wrong." shows — acceptable; better would be the empty state. (d): table renders; Edit modal opens with no category ticked and an empty Locale field (`?? []`, `?? ""`). No blank page in any case.

### DEV-07 — Missing optional / date fields and unknown platform

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; items `[ { "id": "d9", "user_id": "u1", "platform": "windows", "bundle_id": "" } ]` (no dates, no app_version).
- **Steps:**
  1. Open `/admin/devices`.
- **Expected:** Row renders. Platform shows the phone icon plus a readable label (today it shows the raw key "devices.platform.windows" — currently unhandled — likely bug; expected the raw platform name or "Other"). App version "—". Last seen / Registered: `formatDate(undefined)` returns the string "undefined" — expected "—" (currently unhandled — likely bug).

### DEV-08 — Load fails: error state with detail and Try again

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `GET /api/devices` rejects with (a) `ApiError` 500 `{ title: "Internal Server Error", detail: "db down" }`, (b) `ApiError` 403 `{ title: "Forbidden" }` (no detail), (c) `TypeError("Failed to fetch")`, (d) 200 with truncated JSON body.
- **Steps:**
  1. Open `/admin/devices`.
- **Expected:** ErrorState (kind server) titled "Something went wrong." with description (a) "db down", (b) "Forbidden", (c) and (d) "Something went wrong."; a "Try again" button. No table, no empty state.

### DEV-09 — Try again reloads and shows the list

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; first `GET /api/devices` rejects, second resolves with one device (hold it pending at first).
- **Steps:**
  1. Open `/admin/devices`; click "Try again".
  2. Resolve the second call.
- **Expected:** While pending: today the error clears and `devices` is still `[]`, so "No devices" flashes instead of "Loading…" (likely bug: `load()` does not reset `devices` to `null`). After resolve: the table with one row.

### DEV-10 — Register: button disabled until token and bundle ID are set

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; list OK.
- **Steps:**
  1. Click "Register device"; dialog "Register device" opens.
  2. Check the footer "Register device" button.
  3. Type "tok" in "Push token" only; then " " (spaces) in "Bundle ID"; then "com.x" in "Bundle ID".
- **Expected:** Platform select defaults to "Web" (options Web, iOS, Android). Button disabled at step 2, still disabled with only a token or a whitespace bundle ID, enabled when both have non-blank text. Categories fieldset "Notification categories" with Jobs, Library, Subscription, System, all unticked.

### DEV-11 — Register success: POST body, toast, modal closes, list reloads

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `POST /api/devices/register` resolves `{}`; second `GET /api/devices` returns the new device.
- **Steps:**
  1. Open the Register dialog; choose "iOS"; token "abc123"; bundle "com.maktaba.app"; tick "Jobs" and "System".
  2. Click "Register device" in the footer.
- **Expected:** `api.post("/api/devices/register", { platform: "ios", token: "abc123", bundle_id: "com.maktaba.app", categories: ["job", "system"] })`. Button shows loading while pending. Success toast "Device registered."; dialog closes; `GET /api/devices` called again; new row visible. Note: token and bundle are sent untrimmed (`" abc "` stays `" abc "`) — expected trimmed (minor bug).

### DEV-12 — Register failure keeps the modal open with an error toast

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `POST` rejects with `ApiError` 400 `{ title: "Bad Request", detail: "token already registered" }`; then with a network `TypeError`.
- **Steps:**
  1. Fill the form validly; click "Register device".
- **Expected:** Error toast "token already registered" (or "Something went wrong." for network); dialog stays open with the typed values; button enabled again; list not reloaded.

### DEV-13 — Register with Enter key submits only when valid

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin.
- **Steps:**
  1. Open Register; type only a token; press Enter in the token field.
  2. Type a bundle ID; press Enter.
- **Expected:** Step 1: no POST. Step 2: one POST. Pressing Enter twice quickly should not send two POSTs (busy guards only the button; the form handler calls `submit()` again — check for a double register; likely bug if two requests go out).

### DEV-14 — Edit: pre-filled categories/locale, PATCH body, reload

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; device d1 from DEV-01 (`categories: ["job"]`, `locale: "en"`); `PATCH` resolves.
- **Steps:**
  1. Click "Edit" on the iOS row; dialog "Edit device".
  2. Untick "Jobs", tick "Library"; set Locale to " ar ".
  3. Click "Save".
- **Expected:** Step 1: "Jobs" ticked, Locale "en". Step 3: `api.patch("/api/devices/d1", { categories: ["library"], locale: "ar" })`; toast "Device updated."; dialog closes; list reloads.

### DEV-15 — Edit: clearing locale

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; device with `locale: "en"`.
- **Steps:**
  1. Edit; clear Locale; Save.
- **Expected:** The admin expects the locale removed. Today the body is `{ categories: [...], locale: undefined }`, so `locale` is dropped from the JSON and the server keeps "en" — clearing is impossible (currently unhandled — likely bug; expected `locale: ""` or `null`).

### DEV-16 — Edit failure shows an error toast

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `PATCH` rejects `ApiError` 404 `{ title: "Not Found" }` (device removed elsewhere).
- **Steps:**
  1. Edit; Save.
- **Expected:** Error toast "Not Found"; dialog stays open; Save re-enabled.

### DEV-17 — Remove asks first; Cancel does nothing

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; list with d1.
- **Steps:**
  1. Click "Remove" on d1.
  2. Click the backdrop; then click "Cancel".
- **Expected:** Dialog "Remove device" with "Remove this device? It will stop receiving notifications.", "Cancel" and a destructive "Remove". Backdrop click does not close it (`dismissable={false}`); Cancel closes it; no DELETE sent.

### DEV-18 — Remove confirm: DELETE, toast, reload; double click

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `DELETE` resolves after a delay; reload returns `{ "items": [] }`.
- **Steps:**
  1. Remove d1; click the dialog "Remove".
  2. Click it again before the request resolves.
- **Expected:** `api.delete("/api/devices/d1")`; toast "Device removed."; dialog closes; list reloads and shows "No devices". The confirm button has no busy/disabled state, so step 2 sends a second DELETE (currently unhandled — likely bug: expected one request and a loading button).

### DEV-19 — Remove failure keeps the dialog open with an error toast

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; `DELETE` rejects `ApiError` 500 `{ title: "Internal Server Error", detail: "boom" }`.
- **Steps:**
  1. Remove d1; confirm.
- **Expected:** Error toast "boom"; dialog stays open; row still listed.

### DEV-20 — Ids with special characters are URL-encoded

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; device `id: "a/b?c#d"`.
- **Steps:**
  1. Edit and Save; then Remove and confirm.
- **Expected:** `PATCH /api/devices/a%2Fb%3Fc%23d` and `DELETE /api/devices/a%2Fb%3Fc%23d`.

### DEV-21 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** full app in `MemoryRouter` with history `["/admin/logs", "/admin/devices"]`; admin. Force a render throw: `GET /api/devices` -> `{ "items": [ { "id": "d1", "platform": {} } ] }` is not enough (renders "[object Object]"), so mock `platformIcon` or `@ds/components/EmptyState/EmptyState` to throw, or return `{ "items": "oops" }` (string: `devices.map` is not a function -> throws).
- **Steps:**
  1. Navigate to `/admin/devices`.
  2. Press browser Back; click the "All devices" nav link again with good data; click "Logs".
- **Expected:** Route-level error fallback with shell and nav still shown, not a blank page. Back shows `/admin/logs` without a reload; nav links work afterwards. (`{ "items": "oops" }` crashing is itself a bug — expected an error/empty state.)

### DEV-22 — Nav link, Back/Forward, deep link

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** full app, admin.
- **Steps:**
  1. Click sidebar "All devices" (`nav.admin.devices`).
  2. Back, Forward.
  3. Open `/admin/devices` directly.
  4. Open the Register dialog and press browser Back.
- **Expected:** Steps 1-3: page loads; history works. Step 4: the route changes and the dialog unmounts with the page (no stuck overlay, body scroll restored — `document.body.style.overflow` back to ""). Note: the nav label "All devices" contradicts the page note (account-scoped list) — copy bug worth flagging.

### DEV-23 — Arabic: RTL table and modals

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; data as DEV-01.
- **Steps:**
  1. Open `/admin/devices`; open Register, Edit and Remove dialogs.
- **Expected:** `<html dir="rtl">`; h1 "الأجهزة"; column headers, buttons, platform names ("الويب"), category labels and toasts in Arabic; table columns run right to left; bundle IDs (mono) stay readable LTR; dates in the Arabic locale. Note: the Machines page h1 is also "الأجهزة" — two different admin pages share one Arabic title (copy issue).

### DEV-24 — Phone width: table does not cause page scroll

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** browser e2e at 375x812; 3 devices with a 60-char bundle ID.
- **Steps:**
  1. Open `/admin/devices` (reach it via "More").
  2. Open the Register dialog.
- **Expected:** No horizontal page scroll (table scrolls in a wrapper or reflows; bundle cell truncates with `mkt-truncate`); Edit/Remove buttons reachable; dialog fits the screen with footer buttons visible.

### DEV-25 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme="light"` / `"dark"`; data as DEV-01; open each dialog.
- **Steps:**
  1. Open the page and dialogs in each theme.
- **Expected:** Table text, muted note, destructive button and dialog surfaces readable; axe contrast passes.

### DEV-26 — Dialogs trap focus, close on Escape, return focus

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (Devices.test.tsx)
- **Preconditions / data:** admin; list with d1.
- **Steps:**
  1. Click "Register device"; press Tab repeatedly; press Escape.
  2. Click "Remove" on d1; press Escape.
- **Expected:** `role="dialog"` with `aria-modal="true"` and name from the title; focus stays inside; Escape closes (also for the non-dismissable remove dialog, which only blocks the backdrop); focus returns to the button that opened it. The close button is labelled "Close" in English even in Arabic (DS Modal hardcodes it — minor).

### DEV-27 — Table, buttons and fields have accessible names

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** admin; data as DEV-01.
- **Steps:**
  1. Run `axeViolations(container)` on the page and on each open dialog.
- **Expected:** No violations. Each row's "Edit"/"Remove" button is identical text — expected to include the device (e.g. via `aria-label` "Edit iOS com.maktaba.app") so screen-reader users can tell rows apart (currently not — nice-to-have). Platform emoji icons should be `aria-hidden`.
