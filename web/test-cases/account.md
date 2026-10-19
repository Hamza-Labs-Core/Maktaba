# Account

- **Route:** `/account` (no params, no query state)
- **Component:** `src/pages/Profile/Account.tsx`; children (same file): `ProfileForm`, `PasswordForm`; design system `@ds/components/Card`, `Input`, `Button`, `ErrorState`, `Toast`
- **API calls:** `GET /api/me` → `{ id, username, email, display_name, is_admin }` (profile object at top level, not wrapped in `{ user }`); `PATCH /api/me` body `{ display_name, email }` → updated profile object; `POST /api/me/change-password` body `{ current_password, new_password }` → 204. All via `api` in `src/lib/api.ts`. No polling.
- **Access:** signed-in user (inside `RequireAuth` + `AppShell` in `src/App.tsx`). No admin check. Sidebar link `nav.account` ("Account") in `SECONDARY` in `src/components/AppShell.tsx`; on phone reached through `/more`.
- **Existing tests:** none (`src/pages/Profile/` has no `*.test.tsx`). Gaps: everything.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test?                                                                |
| ------ | ----------------------------------------------------------- | ------------- | -------- | ---------------------------------------------------------------------------------------- |
| ACC-01 | Profile loads and fills both forms                          | Happy path    | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-02 | Save display name and email                                 | Happy path    | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-03 | Save is disabled until something changes                    | Validation    | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-04 | Profile save error shows an error toast and keeps the edits | Error         | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-05 | Change password happy path                                  | Happy path    | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-06 | Wrong current password shows the server message             | Error         | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-07 | New password shorter than 8 keeps the button disabled       | Validation    | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-08 | Confirm mismatch shows "Passwords do not match."            | Validation    | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-09 | Empty confirm field must not allow submit                   | Validation    | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-10 | Loading state while `/api/me` is pending                    | Loading       | P3       | No                                                                                       |
| ACC-11 | `/api/me` 500 shows ErrorState and Retry works              | Error         | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-12 | `/api/me` network failure / malformed JSON                  | Error         | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-13 | `/api/me` 401 mid-session                                   | Permissions   | P1       | Partial (Account.test.tsx: no blank page passes; 401 redirect is it.fails: bug)          |
| ACC-14 | Null or missing optional fields in the profile              | Empty state   | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-15 | Signed out deep link redirects to /login                    | Permissions   | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-16 | Username field is read-only and not sent                    | Validation    | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-17 | Double submit sends one request                             | Loading       | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-18 | Header and nav reflect the new display name after save      | Happy path    | P3       | No                                                                                       |
| ACC-19 | Crash during render shows a fallback; Back recovers         | Error         | P1       | Yes (Account.test.tsx)                                                                   |
| ACC-20 | Nav link, Back and Forward                                  | Navigation    | P2       | Partial (Account.test.tsx: Back re-fetch; sidebar aria-current is AppShell, not covered) |
| ACC-21 | Arabic / RTL                                                | RTL           | P2       | Yes (Account.test.tsx)                                                                   |
| ACC-22 | Light and dark theme                                        | Theme         | P3       | No                                                                                       |
| ACC-23 | Phone width 375px                                           | Responsive    | P3       | No                                                                                       |
| ACC-24 | Keyboard, labels, live regions                              | Accessibility | P2       | Yes (Account.test.tsx)                                                                   |

## Cases

### ACC-01 — Profile loads and fills both forms

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** signed in; `GET /api/me` → `{ "id": "u1", "username": "mona", "email": "mona@example.com", "display_name": "Mona", "is_admin": false }`
- **Steps:**
  1. Open `/account`.
- **Expected:** Heading `account.title` "Account". Card "Profile" (`account.profile`) with inputs labelled "Username" (value `mona`, disabled), "Display name" (value `Mona`), "Email" (value `mona@example.com`, `type="email"`), and a "Save" button (disabled). Card "Change password" (`account.changePassword`) with "Current password", "New password", "Confirm password" (all `type="password"`, empty) and a disabled "Change password" button. Exactly one `GET /api/me`.

### ACC-02 — Save display name and email

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01; `PATCH /api/me` → `{ "id": "u1", "username": "mona", "email": "m2@example.com", "display_name": "Mona D", "is_admin": false }`
- **Steps:**
  1. Open `/account`.
  2. Change "Display name" to `Mona D` and "Email" to `m2@example.com`.
  3. Click "Save".
- **Expected:** One `PATCH /api/me` with JSON body `{ "display_name": "Mona D", "email": "m2@example.com" }` and the `X-Maktaba-CSRF` header when the `mkt_csrf` cookie is set. Success toast "Profile updated." (`account.saved`, `role="status"`). Inputs keep the new values; "Save" becomes disabled again (no longer dirty).

### ACC-03 — Save is disabled until something changes

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01
- **Steps:**
  1. Open `/account`. Check "Save".
  2. Type `x` into "Display name", then delete it.
  3. Press Enter in "Display name" while unchanged.
- **Expected:** "Save" disabled at load; enabled after the change; disabled again once the value equals `Mona`. Enter on an unchanged form sends no `PATCH`.

### ACC-04 — Profile save error shows an error toast and keeps the edits

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01; variants for `PATCH /api/me`: (a) 409 `application/problem+json` `{ "title": "Conflict", "status": 409, "detail": "email already in use" }`; (b) 400 `{ "title": "Bad Request", "status": 400 }` (no detail); (c) fetch rejects (network); (d) 500 `text/plain` body `boom`.
- **Steps:**
  1. Change "Email" and click "Save".
- **Expected:** Error toast (`role="alert"`) with (a) "email already in use", (b) "Bad Request", (c) "Something went wrong." (`common.error`), (d) `boom` (the api client puts the text body in `detail`). The typed values stay in the inputs, "Save" is enabled again (not stuck loading). No success toast.

### ACC-05 — Change password happy path

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01; `POST /api/me/change-password` → 204
- **Steps:**
  1. Fill "Current password" `oldpass12`, "New password" `newpass12`, "Confirm password" `newpass12`.
  2. Click "Change password".
- **Expected:** One `POST /api/me/change-password` with body `{ "current_password": "oldpass12", "new_password": "newpass12" }` (no confirm field sent). Toast "Password changed." (`account.passwordChanged`). All three password inputs cleared; button disabled again. User stays on `/account` and stays signed in.

### ACC-06 — Wrong current password shows the server message

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** `POST /api/me/change-password` → 403 (or 400) `application/problem+json` `{ "title": "Forbidden", "status": 403, "detail": "current password is incorrect" }`
- **Steps:**
  1. Fill the three fields validly and submit.
- **Expected:** Error toast "current password is incorrect". The fields are NOT cleared (user can fix and retry). The user is not signed out and not redirected. Button enabled again.

### ACC-07 — New password shorter than 8 keeps the button disabled

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01
- **Steps:**
  1. Fill current `oldpass12`, new `short7x` (7 chars), confirm `short7x`.
  2. Then change both to `exactly8`.
- **Expected:** With 7 chars "Change password" is disabled and no request is sent. With 8 chars it is enabled. The page shows no visible hint about the 8-character minimum (only the `minLength` attribute) — expect a hint near "New password" (currently missing — likely UX bug).

### ACC-08 — Confirm mismatch shows "Passwords do not match."

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01
- **Steps:**
  1. Fill current `oldpass12`, new `newpass12`, confirm `newpass13`.
  2. Fix confirm to `newpass12`.
- **Expected:** Under "Confirm password" the error "Passwords do not match." (`register.mismatch`) is shown and wired to the input (`aria-invalid="true"`, `aria-describedby` points at the message). Button disabled. After fixing, the message disappears and the button is enabled.

### ACC-09 — Empty confirm field must not allow submit

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01
- **Steps:**
  1. Fill current `oldpass12`, new `newpass12`, leave confirm empty.
  2. Click "Change password" (also try `form.requestSubmit()` / Enter, and a run with native validation bypassed, e.g. `noValidate` in a browser e2e).
- **Expected:** The button stays disabled and no `POST /api/me/change-password` is sent while confirm is empty. Currently `mismatch` is `confirm !== "" && next !== confirm`, so an empty confirm counts as valid and the button is enabled; only the native `required` attribute stops it (currently unhandled in code — likely bug).

### ACC-10 — Loading state while `/api/me` is pending

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/me` never resolves (pending promise)
- **Steps:**
  1. Open `/account`.
- **Expected:** Heading "Account" and the text "Loading…" (`common.loading`). No forms, no ErrorState.

### ACC-11 — `/api/me` 500 shows ErrorState and Retry works

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** first `GET /api/me` → 500 `application/problem+json` `{ "title": "Internal Server Error", "status": 500, "detail": "db down" }`; second call → profile as ACC-01
- **Steps:**
  1. Open `/account`.
  2. Click "Try again" (`common.retry`).
- **Expected:** ErrorState (`role="alert"`) titled "Something went wrong." with description "db down" and a "Try again" button. After Retry, a second `GET /api/me` runs and both forms render. Also 404 `{ "title": "Not Found", "status": 404 }` shows "Not Found".

### ACC-12 — `/api/me` network failure / malformed JSON

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** variants: (a) fetch rejects `TypeError("Failed to fetch")`; (b) 200 `application/json` body `{not json`; (c) 200 `text/html` body `<html>…</html>` (SPA fallback by a misrouted proxy).
- **Steps:**
  1. Open `/account`.
- **Expected:** (a), (b): ErrorState with "Something went wrong." and Retry. (c): the api client returns the raw string as the profile; the page must not render forms full of `undefined` or crash — expect an ErrorState (currently the forms render with empty/undefined values and React warns about uncontrolled inputs — likely bug).

### ACC-13 — `/api/me` 401 mid-session

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Partial (Account.test.tsx: no blank page passes; 401 redirect is it.fails: bug)
- **Preconditions / data:** `AuthProvider` probe `GET /api/auth/me` → `{ "user": { "id": "u1", "username": "mona", "is_admin": false } }`; then `GET /api/me` → 401 `{ "title": "Unauthorized", "status": 401 }` (session expired)
- **Steps:**
  1. Open `/account`.
- **Expected:** No blank page. Today it shows ErrorState with "Unauthorized" and Retry. Desired: send the user to `/login` with `state.from = "/account"` (currently no 401 redirect — likely bug). Same for a 401 from `PATCH /api/me` or `POST /api/me/change-password` (currently only a toast).

### ACC-14 — Null or missing optional fields in the profile

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** variants of `GET /api/me`: (a) `"display_name": null`; (b) `"email": null`; (c) `display_name` and `email` keys missing; (d) `"display_name": ""`, `"email": ""`. (The page reads no list fields.)
- **Steps:**
  1. Open `/account`.
  2. Type a display name and save.
- **Expected:** Page renders; inputs show empty, no React "uncontrolled to controlled" warning, "Save" disabled until a real change. After typing, "Save" enables and `PATCH` sends the typed values. Currently `useState(profile.display_name)` stores `null`/`undefined`, making the input uncontrolled (likely bug: should default to `""`).

### ACC-15 — Signed out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** `GET /api/auth/me` → 401
- **Steps:**
  1. Open `/account` directly.
- **Expected:** `RequireAuth` shows "Loading…" (`role="status"`) then redirects to `/login` with `state.from = "/account"`. No `GET /api/me` is sent. After signing in, the user lands back on `/account`.

### ACC-16 — Username field is read-only and not sent

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-02
- **Steps:**
  1. Try to type into "Username".
  2. Change "Display name" and save.
- **Expected:** "Username" is `disabled` and `readOnly`; typing does nothing. The `PATCH` body has only `display_name` and `email` (no `username`, `is_admin`, `id`).

### ACC-17 — Double submit sends one request

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** `PATCH /api/me` and `POST /api/me/change-password` resolve after a delay
- **Steps:**
  1. Change display name, click "Save" twice quickly.
  2. Fill a valid password change, click "Change password" twice quickly.
- **Expected:** While pending the button has `aria-busy="true"` and is disabled; exactly one `PATCH` and one `POST`. After resolve, buttons return to normal.

### ACC-18 — Header and nav reflect the new display name after save

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** as ACC-02
- **Steps:**
  1. Save a new display name.
  2. Look at the AppShell header (`src/components/AppShell.tsx` shows `user.username`).
- **Expected:** Nothing else on the page goes stale. Note: `useAuth().user` is not refreshed after `PATCH /api/me`; today the header shows `username`, so no visible change. If a later version shows display name or email anywhere from auth state, it must update without a reload (currently not refreshed — watch item).

### ACC-19 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** signed in; start on `/library`. Make `Account` throw during render, e.g. mock `ProfileForm`'s `Input` (or `@ds/components/Card/Card`) to throw, or mock `GET /api/me` to return a value whose getter throws.
- **Steps:**
  1. From `/library`, click the sidebar link "Account".
  2. Press browser Back.
  3. Click the sidebar link "Account" again, then a different nav link (e.g. "Search").
- **Expected:** A route-level error fallback (from the route error boundary in `AppShell`) instead of a blank page; the sidebar stays usable. Back shows `/library` content without a page refresh. Clicking another nav link renders that page. The fallback resets when the location changes.

### ACC-20 — Nav link, Back and Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial (Account.test.tsx: Back re-fetch; sidebar aria-current is AppShell, not covered)
- **Preconditions / data:** signed in; profile as ACC-01
- **Steps:**
  1. Start on `/library`, click sidebar "Account".
  2. Click sidebar "Devices" (`/account/devices`).
  3. Press Back, then Forward.
- **Expected:** `/account` renders the forms; the "Account" link has `aria-current="page"`. On `/account/devices` only the "Devices" link should be current — today the `/account` NavLink has no `end` prop, so "Account" is also marked current on every `/account/*` page (likely bug, `AppShell.tsx` SECONDARY NavLink). Back returns to `/account` (fresh `GET /api/me`), Forward to `/account/devices`.

### ACC-21 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"` (or switch language in Settings); profile as ACC-01
- **Steps:**
  1. Open `/account`. Trigger the mismatch error and a save toast.
- **Expected:** `<html dir="rtl" lang="ar">`. Heading "الحساب", cards "الملف الشخصي" and "تغيير كلمة المرور", labels "اسم المستخدم", "الاسم المعروض", "البريد الإلكتروني", "كلمة المرور الحالية", "كلمة المرور الجديدة", "تأكيد كلمة المرور", button "حفظ". Mismatch "كلمتا المرور غير متطابقتين."; toast "تم تحديث الملف الشخصي.". Labels and buttons aligned to the right; email and Latin values still readable.

### ACC-22 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:theme"]` = `light`, then `dark`
- **Steps:**
  1. Open `/account` in each theme; show the mismatch error and the ErrorState.
- **Expected:** `<html data-theme>` matches. Card, inputs, disabled username, error text and toasts are readable in both; no hardcoded colors in `.mkt-account__grid` (uses tokens).

### ACC-23 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812
- **Steps:**
  1. Open `/account`; reach it from the bottom tab bar via "More" (`/more`).
- **Expected:** The two cards stack (grid `minmax(280px, 1fr)`); no horizontal page scroll; long emails do not overflow the input. "Account" link is reachable from `/more`.

### ACC-24 — Keyboard, labels, live regions

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (Account.test.tsx)
- **Preconditions / data:** as ACC-01
- **Steps:**
  1. Tab through the page from the top.
  2. Fill the password form with keyboard only and press Enter.
- **Expected:** Every input is found by `getByLabelText` (label `htmlFor` wiring). Tab order: Display name, Email, Save, Current password, New password, Confirm password, Change password (disabled controls skipped). Focus ring visible. Enter submits the form it is in. Toasts announce (`role="status"` polite for success, `role="alert"` for errors). Password inputs have `autocomplete` `current-password` / `new-password`.
