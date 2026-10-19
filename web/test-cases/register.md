# Register

- **Route:** `/register`
- **Component:** `src/pages/Auth/Register.tsx`; no child components
- **API calls:** `POST /api/auth/register` `{ username, email, password }` → `200 { user: { id, username, is_admin } }` (cookie set, user signed in) via `useAuth().register` in `src/lib/auth.tsx`; errors: `403` problem `type` containing `registration-closed`, `409` problem `type` `username-exists` / `email-exists`. `GET /api/auth/me` (AuthProvider probe).
- **Access:** public (outside `RequireAuth` and `AppShell`). A signed-in user is sent to `/library`.
- **Existing tests:** none (no `Register.test.tsx`). Gaps: everything.

## Summary

| ID     | Title                                                    | Category      | Priority | Covered by existing test? |
| ------ | -------------------------------------------------------- | ------------- | -------- | ------------------------- |
| REG-01 | Create account signs in and goes to /library             | Happy path    | P1       | No                        |
| REG-02 | Submit disabled until every field is filled              | Validation    | P2       | No                        |
| REG-03 | Whitespace-only username or email keeps submit disabled  | Validation    | P2       | No                        |
| REG-04 | Password mismatch alert and disabled submit              | Validation    | P2       | No                        |
| REG-05 | Password shorter than 8 characters is not sent           | Validation    | P2       | No                        |
| REG-06 | Invalid email format is not sent                         | Validation    | P3       | No                        |
| REG-07 | Registration closed (403) replaces the form              | Permissions   | P1       | No                        |
| REG-08 | Username or email taken (409) shows server detail        | Error         | P1       | No                        |
| REG-09 | 409 without detail shows the generic conflict text       | Error         | P2       | No                        |
| REG-10 | 500 shows detail or generic error                        | Error         | P2       | No                        |
| REG-11 | Non-JSON error body (HTML 502) does not show raw HTML    | Error         | P2       | No                        |
| REG-12 | JSON error without `type` still shows an error           | Error         | P2       | No                        |
| REG-13 | Network failure shows the generic error                  | Error         | P2       | No                        |
| REG-14 | 200 with a body lacking `user` does not bounce silently  | Error         | P2       | No                        |
| REG-15 | Submitting state: one request, button shows Loading      | Loading       | P2       | No                        |
| REG-16 | Already signed in: /register goes to /library            | Navigation    | P2       | No                        |
| REG-17 | Links to sign in (form and closed state)                 | Navigation    | P2       | No                        |
| REG-18 | Back after creating the account does not reopen the form | Navigation    | P3       | No                        |
| REG-19 | Crash during render shows a fallback; Back recovers      | Error         | P1       | No                        |
| REG-20 | Arabic / RTL                                             | RTL           | P2       | No                        |
| REG-21 | Light and dark theme                                     | Theme         | P3       | No                        |
| REG-22 | Phone width 375px                                        | Responsive    | P3       | No                        |
| REG-23 | Keyboard, labels and autocomplete                        | Accessibility | P2       | No                        |

## Cases

### REG-01 — Create account signs in and goes to /library

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** real `AuthProvider`; `/api/auth/me` → 401; `POST /api/auth/register` → `200 { "user": { "id": "u9", "username": "salma", "is_admin": false } }`.
- **Steps:**
  1. Open `/register`.
  2. Fill "Username" `salma`, "Email" `salma@example.com`, "Password" `longpass1`, "Confirm password" `longpass1`.
  3. Click "Create account".
- **Expected:** One POST with body `{ "username": "salma", "email": "salma@example.com", "password": "longpass1" }` (no confirm field). Location `/library` (replace). The shell shows `salma` in the header.

### REG-02 — Submit disabled until every field is filled

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** none.
- **Steps:**
  1. Fill fields one at a time, checking the button after each.
- **Expected:** "Create account" is `disabled` until username, email and password are non-empty (confirm may be empty or equal). Pressing Enter in a field while invalid sends no request.

### REG-03 — Whitespace-only username or email keeps submit disabled

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** username `"   "`, other fields valid.
- **Steps:**
  1. Fill the form.
- **Expected:** Button disabled; no POST. Note: a username with leading/trailing spaces (`" salma "`) is sent untrimmed — expected trimmed or rejected (currently sent as typed — likely bug, confirm with server rules).

### REG-04 — Password mismatch alert and disabled submit

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** password `longpass1`, confirm `longpass2`.
- **Steps:**
  1. Type both. 2. Fix confirm to `longpass1`.
- **Expected:** Step 1: `role="alert"` "Passwords do not match." and button disabled. Step 2: alert gone, button enabled. No alert while confirm is still empty.

### REG-05 — Password shorter than 8 characters is not sent

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** password and confirm `short`.
- **Steps:**
  1. Fill all fields; click "Create account".
- **Expected:** No POST; the user is told the password must be at least 8 characters. (Only the native `minLength={8}` guards this; the component's `valid` check does not, so the button is enabled and in jsdom/tests the request is sent — currently unhandled in code — likely bug. `ResetPassword` checks `password.length >= 8` in JS; Register should match.)

### REG-06 — Invalid email format is not sent

- **Priority:** P3
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** email `not-an-email`.
- **Steps:**
  1. Fill the form; submit (browser e2e).
- **Expected:** Native `type="email"` validation blocks submit; no POST.

### REG-07 — Registration closed (403) replaces the form

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** POST → `403` problem+json `{ "type": "https://maktaba/problems/registration-closed", "title": "Forbidden", "status": 403 }`.
- **Steps:**
  1. Submit a valid form.
- **Expected:** Inputs and button disappear. `role="alert"` "Registration is currently closed. Ask an administrator for an invite." and a "Back to sign in" link to `/login`. A `403` without `registration-closed` in `type` shows the detail/generic error instead and keeps the form.

### REG-08 — Username or email taken (409) shows server detail

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** POST → `409` `{ "type": "username-exists", "title": "Conflict", "status": 409, "detail": "username already exists" }`; repeat with `email-exists`.
- **Steps:**
  1. Submit.
- **Expected:** `role="alert"` with the `detail` text. Form stays filled; button enabled again; still on `/register`.

### REG-09 — 409 without detail shows the generic conflict text

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `409 { "type": "username-exists", "title": "Conflict", "status": 409 }`.
- **Steps:**
  1. Submit.
- **Expected:** "That username or email is already taken." (`register.conflict`).

### REG-10 — 500 shows detail or generic error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `500 { "type": "about:blank", "title": "Internal", "status": 500, "detail": "db down" }`; then the same without `detail`.
- **Steps:**
  1. Submit each.
- **Expected:** First shows "db down"; second shows "Something went wrong.". Form kept.

### REG-11 — Non-JSON error body (HTML 502) does not show raw HTML

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** POST → `502` `Content-Type: text/html` body `<html><body>Bad Gateway</body></html>`.
- **Steps:**
  1. Submit.
- **Expected:** "Something went wrong." (`common.error`). (Currently `api.ts` puts the first 256 chars of the body in `problem.detail` and Register shows `detail`, so the raw HTML text appears in the alert — currently unhandled — likely bug.)

### REG-12 — JSON error without `type` still shows an error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** POST → `400` `application/json` `{ "title": "Bad Request", "status": 400, "detail": "username too short" }` (no `type`).
- **Steps:**
  1. Submit.
- **Expected:** Alert "username too short"; button re-enabled. (Currently `err.problem.type.includes(...)` throws `TypeError` on `undefined`, so no message shows and an unhandled rejection is logged — currently unhandled — likely bug.)

### REG-13 — Network failure shows the generic error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `fetch` rejects.
- **Steps:**
  1. Submit.
- **Expected:** "Something went wrong."; button enabled; form kept.

### REG-14 — 200 with a body lacking `user` does not bounce silently

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** full App routes; POST → `200 {}`.
- **Steps:**
  1. Submit.
- **Expected:** An error is shown on `/register`. (Currently `setUser(undefined)` then `nav("/library")`, `RequireAuth` redirects to `/login` with no message — currently unhandled — likely bug.)

### REG-15 — Submitting state: one request, button shows Loading

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** POST held pending.
- **Steps:**
  1. Submit, then click again.
- **Expected:** Button text "Loading…", `disabled`; exactly one POST. Old error cleared when the new submit starts.

### REG-16 — Already signed in: /register goes to /library

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** `/api/auth/me` → user.
- **Steps:**
  1. Open `/register`.
- **Expected:** Location `/library`; form never shown; no React "Cannot update a component while rendering" / react-router "navigate() in useEffect" warning. (Currently `nav()` is called during render — likely bug; expect a warning in the console spy.)

### REG-17 — Links to sign in (form and closed state)

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** none; closed state from REG-07.
- **Steps:**
  1. Click "Already have an account? Sign in". 2. Back. 3. Trigger the closed state, click "Back to sign in".
- **Expected:** Both go to `/login`; Back from `/login` returns to `/register` (fresh form).

### REG-18 — Back after creating the account does not reopen the form

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** e2e: `/login` → "Create an account" → register succeeds.
- **Steps:**
  1. Press browser Back on `/library`.
- **Expected:** Goes to `/login`, which forwards to `/library` (signed in); the register form is not shown again.

### REG-19 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** mock `useI18n().t` (or wrap with a mocked `../../lib/i18n`) so `t("register.title")` throws. `/register` is outside `AppShell`, so the AppShell route boundary does not cover it.
- **Steps:**
  1. From `/login`, click "Create an account".
  2. Press browser Back.
- **Expected:** Error fallback instead of a blank page; Back renders `/login` without a refresh. (No boundary on public routes today — currently unhandled — likely bug.)

### REG-20 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`.
- **Steps:**
  1. Open `/register`; make a mismatch; then trigger REG-07.
- **Expected:** `dir="rtl"`. Title "أنشئ حسابك", labels "اسم المستخدم", "البريد الإلكتروني", "كلمة المرور", "تأكيد كلمة المرور"; button "إنشاء الحساب"; mismatch "كلمتا المرور غير متطابقتين."; closed "التسجيل مغلق حالياً. اطلب دعوة من المسؤول.". Email and password inputs remain usable (typed text LTR is fine).

### REG-21 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light / dark.
- **Steps:**
  1. Show mismatch and error alerts.
- **Expected:** All text and alerts readable in both; colours come from tokens.

### REG-22 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812.
- **Steps:**
  1. Open `/register`, show both alerts.
- **Expected:** No horizontal scroll; all four fields, button and link visible by scrolling vertically only.

### REG-23 — Keyboard, labels and autocomplete

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** none.
- **Steps:**
  1. Tab through the form; submit with Enter.
- **Expected:** Username focused on load. Each input found by its label text. Form accessible name "Create account". Autocomplete: `username`, `email`, `new-password`, `new-password`. Mismatch and error alerts are `role="alert"`. Focus visible on every control.
