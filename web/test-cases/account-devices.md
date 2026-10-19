# Connected devices (my devices)

- **Route:** `/account/devices` (no params, no query state)
- **Component:** `src/pages/Profile/ConnectedDevices.tsx`; uses `platformIcon` and the `Device` type from `src/pages/Cloud/Devices.tsx`; design system `Card`, `Button`, `Modal` (`design-system/components/Modal/Modal.tsx`, focus trap in `design-system/components/internal/useFocusTrap.ts`), `EmptyState`, `ErrorState`, `Toast`
- **API calls:** `GET /api/me/devices` → `{ items: Device[] }` where `Device = { id, user_id, platform, bundle_id, app_version?, os_version?, locale?, categories?, registered_at, last_seen_at }`; `DELETE /api/devices/{id}` (id URL-encoded) → 204. Via `api` in `src/lib/api.ts`. No polling. After a delete the list is fetched again.
- **Access:** signed-in user (`RequireAuth` + `AppShell`); server scopes both calls to the caller. No admin check. Sidebar link `nav.devices` ("Devices").
- **Existing tests:** none. Gaps: everything.

## Summary

| ID     | Title                                                     | Category      | Priority | Covered by existing test?                                                                                          |
| ------ | --------------------------------------------------------- | ------------- | -------- | ------------------------------------------------------------------------------------------------------------------ |
| ADV-01 | Device cards render                                       | Happy path    | P1       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-02 | Remove a device through the confirm dialog                | Happy path    | P1       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-03 | Cancel / Escape / backdrop on the confirm dialog          | Accessibility | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-04 | Remove fails: error toast, dialog stays, device stays     | Error         | P1       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-05 | Double click on confirm "Remove" sends one DELETE         | Loading       | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-06 | Removing the last device shows the empty state            | Empty state   | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-07 | `items: []` shows the empty state                         | Empty state   | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-08 | `items: null` / missing `items` shows the empty state     | Empty state   | P1       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-09 | Null / missing optional device fields                     | Empty state   | P1       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-10 | Unknown platform value                                    | Empty state   | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-11 | Id with special characters is URL-encoded                 | Happy path    | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-12 | Loading state                                             | Loading       | P3       | No                                                                                                                 |
| ADV-13 | List load errors (500, 404, network, malformed) and Retry | Error         | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-14 | 401 mid-session                                           | Permissions   | P1       | Partial (ConnectedDevices.test.tsx: no blank page passes; 401 redirect is it.fails: bug)                           |
| ADV-15 | Signed out deep link redirects to /login                  | Permissions   | P1       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-16 | Crash during render shows a fallback; Back recovers       | Error         | P1       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-17 | Nav link, Back and Forward                                | Navigation    | P2       | Partial (ConnectedDevices.test.tsx: dialog/scroll cleanup on leave; sidebar aria-current is AppShell, not covered) |
| ADV-18 | Arabic / RTL                                              | RTL           | P2       | Yes (ConnectedDevices.test.tsx)                                                                                    |
| ADV-19 | Light and dark theme                                      | Theme         | P3       | No                                                                                                                 |
| ADV-20 | Phone width 375px                                         | Responsive    | P3       | No                                                                                                                 |
| ADV-21 | Keyboard and screen reader basics                         | Accessibility | P2       | Partial (ConnectedDevices.test.tsx: dialog labelling, live region; per-device button names it.todo: bug)           |

## Cases

### ADV-01 — Device cards render

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** signed in; `GET /api/me/devices` → `{ "items": [ { "id": "d1", "user_id": "u1", "platform": "ios", "bundle_id": "app.maktaba.ios", "app_version": "1.4.0", "registered_at": "2026-09-01T10:00:00Z", "last_seen_at": "2026-10-18T08:30:00Z" }, { "id": "d2", "user_id": "u1", "platform": "android", "bundle_id": "app.maktaba.android", "registered_at": "2026-09-02T10:00:00Z", "last_seen_at": "2026-10-17T08:30:00Z" } ] }`
- **Steps:**
  1. Open `/account/devices`.
- **Expected:** Heading "Devices" (`devices.title`). Two cards in API order. Card 1: "iOS" (`devices.platform.ios`), `app.maktaba.ios`, "Last seen" with the formatted date, "App version" `1.4.0`, "Remove" button. Card 2: "Android", no "App version" row (field absent). Platform icon is `aria-hidden`.

### ADV-02 — Remove a device through the confirm dialog

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** as ADV-01; `DELETE /api/devices/d1` → 204; second `GET /api/me/devices` → `{ "items": [ <d2 only> ] }`
- **Steps:**
  1. Click "Remove" on the iOS card.
  2. In the dialog, click "Remove".
- **Expected:** Dialog `role="dialog"` named "Remove device" (`devices.remove.title`) with text "Remove this device? It will stop receiving notifications." and buttons "Cancel" and "Remove". Confirm sends one `DELETE /api/devices/d1` with the CSRF header. Toast "Device removed." Dialog closes, list refetches, only the Android card remains.

### ADV-03 — Cancel / Escape / backdrop on the confirm dialog

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** as ADV-01
- **Steps:**
  1. Open the dialog; click "Cancel".
  2. Open again; press Escape.
  3. Open again; click the backdrop (outside the dialog).
  4. Open again; click the header close button (`aria-label="Close"`).
- **Expected:** Cancel, Escape and the close button close the dialog with no DELETE. Backdrop click does NOT close it (`dismissable={false}`). While open, focus is inside the dialog, Tab cycles within it, and the background is inert. On close, focus returns to the "Remove" button that opened it. The close button's `aria-label` is hardcoded English "Close" (not translated — minor bug in `Modal.tsx`).

### ADV-04 — Remove fails: error toast, dialog stays, device stays

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** variants for `DELETE /api/devices/d1`: (a) 404 `{ "title": "Not Found", "status": 404, "detail": "device not found" }`; (b) 403 `{ "title": "Forbidden", "status": 403 }`; (c) 500 problem+json without detail; (d) fetch rejects.
- **Steps:**
  1. Click "Remove" on the iOS card, then confirm.
- **Expected:** Error toast (`role="alert"`) with (a) "device not found", (b) "Forbidden", (c) the problem title, (d) "Something went wrong.". The dialog stays open so the user can retry or cancel; the list is not refetched; the iOS card is still shown.

### ADV-05 — Double click on confirm "Remove" sends one DELETE

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** `DELETE /api/devices/d1` resolves after a delay
- **Steps:**
  1. Open the dialog and click the dialog's "Remove" twice quickly.
- **Expected:** Exactly one DELETE; the button shows a busy state while pending. Currently the dialog's "Remove" has no `loading`/`disabled` state, so two DELETEs go out and the second may show a 404 error toast after a success toast (currently unhandled — likely bug).

### ADV-06 — Removing the last device shows the empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** first `GET` → one device; `DELETE` → 204; second `GET` → `{ "items": [] }`
- **Steps:**
  1. Remove the only device.
- **Expected:** EmptyState "No devices" (`devices.empty.title`) with "Register a device to receive push notifications." (`devices.empty.desc`). No stale card left.

### ADV-07 — `items: []` shows the empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** `GET /api/me/devices` → `{ "items": [] }`
- **Steps:**
  1. Open `/account/devices`.
- **Expected:** EmptyState "No devices" with its description; no cards, no ErrorState.

### ADV-08 — `items: null` / missing `items` shows the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** variants of `GET /api/me/devices`: (a) `{ "items": null }`; (b) `{}`; (c) 200 `text/html` body (api client returns a string, so `r.items` is `undefined`); (d) 200 `application/json` body `null`.
- **Steps:**
  1. Open `/account/devices`.
- **Expected:** (a)-(c): EmptyState "No devices", no blank screen, no thrown error (code uses `r.items ?? []`). (d): `r` is `null`, so `r.items` throws inside `.then`, which lands in `.catch` → ErrorState "Something went wrong." with Retry; must not be a blank page.

### ADV-09 — Null / missing optional device fields

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** one device each with: (a) `"app_version": null`; (b) `"app_version": ""`; (c) `"bundle_id": null`; (d) `"last_seen_at": null`; (e) `last_seen_at` missing; (f) `"categories": null` (not read by this page, must not matter).
- **Steps:**
  1. Open `/account/devices`.
- **Expected:** Page renders all cards. (a)/(b): no "App version" row. (c): empty muted line, no crash. (d): "Last seen" should show a dash or "Never" — today `formatDate(null)` turns `new Date(null)` into 1 January 1970 (likely bug in `formatDate`, `src/lib/i18n.tsx`). (e): today shows the literal text `undefined` (likely bug). "Remove" still works for every card.

### ADV-10 — Unknown platform value

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** device with `"platform": "windows"`; another with `"platform": ""`; another with `"platform": null`
- **Steps:**
  1. Open `/account/devices`.
- **Expected:** Generic icon (`platformIcon` default). The title should be a readable fallback (e.g. the raw platform or "Unknown"); today `t("devices.platform.windows")` returns the raw key `devices.platform.windows` (and `devices.platform.null`) on screen (likely bug). No crash.

### ADV-11 — Id with special characters is URL-encoded

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** device `"id": "a/b c?#"`
- **Steps:**
  1. Remove that device.
- **Expected:** Request path is `/api/devices/a%2Fb%20c%3F%23` (via `encodeURIComponent`), never a different path.

### ADV-12 — Loading state

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/me/devices` pending
- **Steps:**
  1. Open `/account/devices`.
- **Expected:** "Loading…" (`common.loading`) under the heading; no EmptyState flash before data arrives.

### ADV-13 — List load errors (500, 404, network, malformed) and Retry

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** variants of the first `GET /api/me/devices`: (a) 500 `{ "title": "Internal Server Error", "status": 500, "detail": "db down" }`; (b) 404 `{ "title": "Not Found", "status": 404 }`; (c) fetch rejects; (d) 200 `application/json` body `{bad`. Second call → list as ADV-01.
- **Steps:**
  1. Open `/account/devices`.
  2. Click "Try again".
- **Expected:** ErrorState (`role="alert"`) "Something went wrong." with description (a) "db down", (b) "Not Found", (c)/(d) "Something went wrong.". No EmptyState shown together with the error. Retry clears the error and shows the cards.

### ADV-14 — 401 mid-session

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Partial (ConnectedDevices.test.tsx: no blank page passes; 401 redirect is it.fails: bug)
- **Preconditions / data:** auth probe returns a user; `GET /api/me/devices` → 401 `{ "title": "Unauthorized", "status": 401 }`; also a 401 on `DELETE`
- **Steps:**
  1. Open `/account/devices`; then try a remove.
- **Expected:** No blank page. Today: ErrorState "Unauthorized" / error toast "Unauthorized". Desired: redirect to `/login` with `state.from` (currently no 401 redirect — likely bug).

### ADV-15 — Signed out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** `GET /api/auth/me` → 401
- **Steps:**
  1. Open `/account/devices` directly.
- **Expected:** Redirect to `/login` with `state.from = "/account/devices"`; no `GET /api/me/devices` sent.

### ADV-16 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** start on `/account`. Make the page throw during render, e.g. `GET /api/me/devices` → `{ "items": [ { "id": "d1", "platform": "ios", "bundle_id": { "x": 1 }, "last_seen_at": "2026-10-18T08:30:00Z" } ] }` (an object rendered as a React child throws "Objects are not valid as a React child"); or mock `Card` to throw.
- **Steps:**
  1. From `/account`, click sidebar "Devices".
  2. Press browser Back.
  3. Click "Devices" again, then click "Sessions".
- **Expected:** Route-level error fallback (AppShell error boundary), not a blank page; nav still visible. Back renders `/account` without a refresh. Clicking "Sessions" renders `/account/sessions`.

### ADV-17 — Nav link, Back and Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial (ConnectedDevices.test.tsx: dialog/scroll cleanup on leave; sidebar aria-current is AppShell, not covered)
- **Preconditions / data:** as ADV-01
- **Steps:**
  1. From `/library` click sidebar "Devices"; open the remove dialog.
  2. Press browser Back while the dialog is open.
  3. Press Forward.
- **Expected:** "Devices" link has `aria-current="page"` (and "Account" should not — see ACC-20). Back leaves the page and the dialog is gone (portal removed, body scroll unlocked: `document.body.style.overflow` restored). Forward shows the list again with no dialog.

### ADV-18 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (ConnectedDevices.test.tsx)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; list as ADV-01
- **Steps:**
  1. Open `/account/devices`; open the remove dialog.
- **Expected:** `<html dir="rtl" lang="ar">`. Heading "الأجهزة", "آخر ظهور", "إصدار التطبيق", button "إزالة"; dialog "إزالة الجهاز" with "إزالة هذا الجهاز؟ سيتوقف عن تلقّي الإشعارات." and buttons "إلغاء" / "إزالة". Dates in Arabic locale (`ar-EG`). Card content right-aligned; bundle id (Latin, monospace) still readable. Empty state "لا أجهزة".

### ADV-19 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light, then dark
- **Steps:**
  1. Open the list, the dialog and the empty state in each theme.
- **Expected:** Text, muted text, destructive buttons and dialog backdrop readable in both themes; colors come from tokens.

### ADV-20 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812; long `bundle_id` (80 chars)
- **Steps:**
  1. Reach the page via "More" → "Devices"; open the dialog.
- **Expected:** One card per row (`minmax(260px, 1fr)`), no horizontal page scroll; long bundle id truncates with ellipsis. Dialog fits the screen, buttons reachable.

### ADV-21 — Keyboard and screen reader basics

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Partial (ConnectedDevices.test.tsx: dialog labelling, live region; per-device button names it.todo: bug)
- **Preconditions / data:** as ADV-01
- **Steps:**
  1. Tab to a "Remove" button, press Enter, Tab through the dialog, press Escape.
- **Expected:** Each "Remove" button is reachable and visibly focused. Buttons are identical text "Remove" per card — expect each to have an accessible name that says which device (e.g. `aria-label` with platform/bundle id) (currently ambiguous — minor a11y bug). Dialog has `aria-modal="true"` and `aria-labelledby` pointing at its title. Toasts announced via live region.
