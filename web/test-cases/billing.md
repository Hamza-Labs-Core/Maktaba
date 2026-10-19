# Billing & license

- **Route:** `/billing` (no params, no query state)
- **Component:** `src/pages/Cloud/Billing.tsx` (single component + `licenseError()` helper); design system `Card`, `Badge`, `Button`, `Textarea`, `ErrorState`, `Toast`; `useAuth()` from `src/lib/auth.tsx` for `user.is_admin`
- **API calls:** `GET /api/entitlements` → `{ tier: string, license_id?: string, seats?: number, features: Record<string, boolean> }` (server `api/internal/handlers/subscriptions/subscriptions.go`; free tier today returns `"features": {}`; `license_id`/`seats` omitted when empty); `POST /api/admin/license` body = the pasted license JSON parsed (`{ license: {...}, signature }`) → 204, 400 invalid, 503 no verifier key in this build; `DELETE /api/admin/license` → 204 (back to free). Via `api` in `src/lib/api.ts`. No polling; entitlements refetched after apply/revoke.
- **Access:** signed-in user (`RequireAuth` + `AppShell`). Plan and entitlements visible to everyone; the license editor is rendered only when `useAuth().user?.is_admin` (no `AdminGate`); the server re-checks admin on `/api/admin/license`. Sidebar link `nav.billing` ("Billing & license").
- **Existing tests:** none. Gaps: everything.

## Summary

| ID     | Title                                                      | Category      | Priority | Covered by existing test? |
| ------ | ---------------------------------------------------------- | ------------- | -------- | ------------------------- |
| BIL-01 | Paid plan: tier, seats, license id, sorted features        | Happy path    | P1       | No                        |
| BIL-02 | Free tier with `features: {}` (the real default)           | Empty state   | P1       | No                        |
| BIL-03 | Non-admin sees no license editor                           | Permissions   | P1       | No                        |
| BIL-04 | Admin applies a valid license                              | Happy path    | P1       | No                        |
| BIL-05 | Pasted text is not JSON                                    | Validation    | P2       | No                        |
| BIL-06 | Server rejects the license (400)                           | Error         | P2       | No                        |
| BIL-07 | License management disabled in this build (503)            | Error         | P2       | No                        |
| BIL-08 | Apply/revoke 403 or network failure                        | Permissions   | P1       | No                        |
| BIL-09 | Admin revokes the license                                  | Happy path    | P1       | No                        |
| BIL-10 | Revoke has no confirmation and is enabled on the free tier | Validation    | P2       | No                        |
| BIL-11 | Apply disabled for empty / whitespace input                | Validation    | P2       | No                        |
| BIL-12 | Busy state: one request per click                          | Loading       | P2       | No                        |
| BIL-13 | `features: null` / missing `features`                      | Empty state   | P1       | No                        |
| BIL-14 | Missing / odd `tier`, `seats`, `license_id`                | Empty state   | P2       | No                        |
| BIL-15 | Loading state                                              | Loading       | P3       | No                        |
| BIL-16 | Load errors (500, 404, 403, network, malformed) and Retry  | Error         | P2       | No                        |
| BIL-17 | 401 mid-session; signed-out deep link                      | Permissions   | P1       | No                        |
| BIL-18 | Crash during render shows a fallback; Back recovers        | Error         | P1       | No                        |
| BIL-19 | Nav link, Back and Forward                                 | Navigation    | P2       | No                        |
| BIL-20 | Entitlements table column header says "Actions"            | Accessibility | P3       | No                        |
| BIL-21 | Arabic / RTL                                               | RTL           | P2       | No                        |
| BIL-22 | Light and dark theme                                       | Theme         | P3       | No                        |
| BIL-23 | Phone width 375px                                          | Responsive    | P3       | No                        |
| BIL-24 | Keyboard and labels                                        | Accessibility | P2       | No                        |

## Cases

### BIL-01 — Paid plan: tier, seats, license id, sorted features

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** signed in as normal user; `GET /api/entitlements` → `{ "tier": "pro", "license_id": "lic_0123456789", "seats": 5, "features": { "transcode_hw": true, "cloud_relay": false, "analytics": true } }`
- **Steps:**
  1. Open `/billing`.
- **Expected:** Heading "Billing & license". Card "Current plan" with an accent badge `pro`, "Seats" `5`, "License ID" `lic_0123456789` (monospace). Heading "Entitlements" and a table named "Entitlements" whose rows are sorted by name: `analytics` "Enabled" (success badge), `cloud_relay` "Disabled" (neutral badge), `transcode_hw` "Enabled".

### BIL-02 — Free tier with `features: {}` (the real default)

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/entitlements` → `{ "tier": "free", "features": {} }` (what the server returns with no license)
- **Steps:**
  1. Open `/billing`.
- **Expected:** Neutral badge `free`; no "Seats" or "License ID" rows. The entitlements section should show an empty message (e.g. "No extra features on this plan"); today it renders a table with only the header row and no body rows (currently unhandled — likely UX bug; no i18n key exists for it).

### BIL-03 — Non-admin sees no license editor

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/auth/me` → `{ "user": { "id": "u2", "username": "sam", "is_admin": false } }`; entitlements as BIL-01
- **Steps:**
  1. Open `/billing`.
- **Expected:** No "License key" heading, no textarea, no "Apply license" or "Revoke license" buttons. Plan and entitlements still shown. No request to `/api/admin/license`.

### BIL-04 — Admin applies a valid license

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** admin user (`is_admin: true`); first `GET /api/entitlements` → BIL-02; `POST /api/admin/license` → 204; second `GET` → BIL-01
- **Steps:**
  1. Open `/billing`.
  2. Paste `{"license":{"license_id":"lic_0123456789","tier":"pro","seats":5},"signature":"abc"}` into "License key".
  3. Click "Apply license".
- **Expected:** Editor shows heading "License key", textarea labelled "License key" with description "Paste a signed license document (JSON) to upgrade this instance." and placeholder `{ "license_id": "…", "tier": "pro", … }`. One `POST /api/admin/license` whose JSON body equals the parsed object (not a string), with the CSRF header. Toast "License applied." Textarea cleared. Entitlements refetched; badge now `pro`.

### BIL-05 — Pasted text is not JSON

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** admin; entitlements loaded
- **Steps:**
  1. Paste `not json {` and click "Apply license".
- **Expected:** No request is sent. Error toast "The license document was rejected." (`billing.license.invalid`, from the `SyntaxError` branch). The pasted text stays in the textarea for fixing.

### BIL-06 — Server rejects the license (400)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** admin; `POST /api/admin/license` → (a) 400 `{ "title": "Bad Request", "status": 400, "detail": "signature mismatch" }`; (b) 400 `{ "title": "Bad Request", "status": 400 }`
- **Steps:**
  1. Paste valid JSON and apply.
- **Expected:** (a) toast "signature mismatch"; (b) toast "The license document was rejected.". Textarea keeps the text; entitlements not refetched; buttons usable again.

### BIL-07 — License management disabled in this build (503)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** admin; `POST /api/admin/license` → 503 `{ "title": "Service Unavailable", "status": 503, "detail": "no verifier key" }`; same for `DELETE`
- **Steps:**
  1. Apply a license; then click "Revoke license".
- **Expected:** Both show toast "License management is disabled in this build." (`billing.license.disabled`), not the raw detail. The app must NOT switch to the paused-node page (the 503 body has no `code: "paused"|"home_unreachable"`).

### BIL-08 — Apply/revoke 403 or network failure

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** UI thinks the user is admin, server disagrees: `POST`/`DELETE /api/admin/license` → 403 `{ "title": "Forbidden", "status": 403, "detail": "admin only" }`; second run: fetch rejects
- **Steps:**
  1. Apply a license; click "Revoke license".
- **Expected:** Toast "admin only" (403) or "Something went wrong." (network). Plan unchanged; no refetch; page still usable.

### BIL-09 — Admin revokes the license

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** admin; first `GET` → BIL-01; `DELETE /api/admin/license` → 204; second `GET` → BIL-02
- **Steps:**
  1. Click "Revoke license".
- **Expected:** One `DELETE /api/admin/license` with the CSRF header. Toast "Reverted to free tier." Entitlements refetched; badge `free`; seats and license id rows gone.

### BIL-10 — Revoke has no confirmation and is enabled on the free tier

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** admin; (a) paid plan as BIL-01; (b) free plan as BIL-02
- **Steps:**
  1. Click "Revoke license" once.
- **Expected:** Revoking downgrades the whole instance, so a confirm dialog (like `tokens.revoke.*`) should appear first; today the DELETE goes out on the first click (currently no confirm — likely UX bug). In (b) there is no license to revoke, so the button should be hidden or disabled; today it is enabled and sends a DELETE.

### BIL-11 — Apply disabled for empty / whitespace input

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** admin
- **Steps:**
  1. Leave the textarea empty; then type spaces and newlines only.
- **Expected:** "Apply license" disabled in both; no request.

### BIL-12 — Busy state: one request per click

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** admin; `POST` / `DELETE` resolve after a delay
- **Steps:**
  1. Click "Apply license" twice quickly.
  2. While a request is pending, try "Revoke license".
- **Expected:** One POST. Both buttons share `busy`, so both show `aria-busy="true"` and are disabled until it finishes (assert this; it prevents apply and revoke racing). After resolve both return to normal.

### BIL-13 — `features: null` / missing `features`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** variants of `GET /api/entitlements`: (a) `{ "tier": "free", "features": null }`; (b) `{ "tier": "free" }`; (c) 200 `text/html` body (api returns a string); (d) 200 `application/json` body `null`.
- **Steps:**
  1. Open `/billing`.
- **Expected:** Page renders the plan card and an empty entitlements section (see BIL-02); no blank screen. Today (a)-(c) throw `TypeError` in `Object.entries(ent.features)` during render and the page crashes (currently unhandled — likely bug, `Billing.tsx` line ~114). (d) leaves `ent` null, so the page stays on "Loading…" forever (likely bug: should show ErrorState).

### BIL-14 — Missing / odd `tier`, `seats`, `license_id`

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** variants: (a) `tier` missing; (b) `"tier": ""`; (c) `"tier": "enterprise"`; (d) `"seats": 0`; (e) `"seats": null`; (f) `"license_id": ""`; each with `"features": {}`
- **Steps:**
  1. Open `/billing`.
- **Expected:** No crash. (a)/(b): badge should show a fallback (e.g. "free" or "—"); today an empty accent badge. (c): accent badge `enterprise`. (d)/(e): no "Seats" row. (f): no "License ID" row. The tier badge shows the raw value in every language (key `billing.tier` exists but is unused; tier names are not translated — minor i18n gap).

### BIL-15 — Loading state

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/entitlements` pending
- **Steps:**
  1. Open `/billing`.
- **Expected:** Heading and "Loading…"; no editor, no table.

### BIL-16 — Load errors (500, 404, 403, network, malformed) and Retry

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** first `GET /api/entitlements`: (a) 500 `{ "title": "Internal Server Error", "status": 500, "detail": "db down" }`; (b) 404 `{ "title": "Not Found", "status": 404 }` (subscriptions store not wired); (c) 403 `{ "title": "Forbidden", "status": 403, "detail": "auth required" }`; (d) fetch rejects; (e) 200 `application/json` `{bad`. Second call → BIL-01.
- **Steps:**
  1. Open `/billing`; click "Try again".
- **Expected:** Heading plus ErrorState "Something went wrong." with (a) "db down", (b) "Not Found", (c) "auth required", (d)/(e) "Something went wrong.". No license editor while in error, even for admins. Retry renders the plan.

### BIL-17 — 401 mid-session; signed-out deep link

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** (a) auth probe OK, `GET /api/entitlements` → 401; (b) `GET /api/auth/me` → 401
- **Steps:**
  1. Open `/billing` directly.
- **Expected:** (a) no blank page; today ErrorState "Unauthorized"; desired redirect to `/login` (currently no 401 redirect — likely bug). (b) redirect to `/login` with `state.from = "/billing"`; no entitlements request.

### BIL-18 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** start on `/account/tokens`; `GET /api/entitlements` → `{ "tier": "free", "features": null }` (crashes today, see BIL-13)
- **Steps:**
  1. From `/account/tokens` click "Billing & license".
  2. Press browser Back.
  3. Click "Billing & license" again, then "Cloud status".
- **Expected:** Route-level error fallback, not a blank page; nav still visible. Back renders `/account/tokens` without a refresh. "Cloud status" renders `/cloud/status`.

### BIL-19 — Nav link, Back and Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** admin; entitlements as BIL-01
- **Steps:**
  1. From `/library` click "Billing & license"; type text into the license box; click "Cloud status"; press Back.
- **Expected:** "Billing & license" link has `aria-current="page"`. Back re-fetches entitlements; the license box is empty again (draft not kept — acceptable, assert it so a change is deliberate).

### BIL-20 — Entitlements table column header says "Actions"

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** entitlements as BIL-01
- **Steps:**
  1. Read the table headers.
- **Expected:** Second column header should describe the status (e.g. "Status"); today it uses `common.actions` "Actions" while the cells hold Enabled/Disabled badges (label bug, `Billing.tsx` line ~147).

### BIL-21 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; admin; entitlements as BIL-01
- **Steps:**
  1. Open `/billing`; trigger a 503 apply.
- **Expected:** `<html dir="rtl" lang="ar">`. Heading "الفوترة والترخيص", "الخطة الحالية", "المقاعد" with the number in Arabic-Indic digits (`n()` uses `ar-EG`, so `٥`), "معرّف الترخيص", "الصلاحيات", badges "مفعّلة"/"معطّلة", editor "مفتاح الترخيص", buttons "تطبيق الترخيص"/"إلغاء الترخيص", toast "إدارة الترخيص معطّلة في هذا البناء.". Feature names and the JSON textarea content stay left-to-right and readable (monospace) inside the RTL layout — ideally the textarea has `dir="ltr"` (today not set).

### BIL-22 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light, then dark
- **Steps:**
  1. View plan, table and editor in each theme.
- **Expected:** Badges (neutral, accent, success), monospace text and textarea readable; tokens only.

### BIL-23 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812; 40-char `license_id`; 30-char feature names
- **Steps:**
  1. Reach `/billing` via "More".
- **Expected:** No horizontal page scroll; license id truncates; table scrolls inside itself; Apply/Revoke buttons wrap and stay reachable.

### BIL-24 — Keyboard and labels

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** admin
- **Steps:**
  1. Tab to the textarea, paste, Tab to "Apply license", press Enter.
- **Expected:** `getByLabelText("License key")` finds the textarea; its description is linked via `aria-describedby`. Focus visible on textarea and both buttons. Toasts announced (`role="status"` / `role="alert"`). ErrorState has `role="alert"`.
