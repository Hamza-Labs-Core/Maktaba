# Reset password

- **Route:** `/reset-password?token=<token>` (token in the query string; missing / empty / expired / used)
- **Component:** `src/pages/Auth/ResetPassword.tsx`; no child components. On success navigates to `/login` with state `{ reset: true }` (`src/pages/Login.tsx` shows the banner).
- **API calls:** `POST /api/auth/reset-password` `{ token, password }` → `204 No Content` (server revokes sessions + refresh tokens); `400` problem when the token is invalid/expired/used or the password is weak. Via `api.post` in `src/lib/api.ts`.
- **Access:** public (outside `RequireAuth` and `AppShell`). The page does not check auth.
- **Existing tests:** none (no `ResetPassword.test.tsx`). Gaps: everything.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test?                         |
| ------ | ----------------------------------------------------------- | ------------- | -------- | ------------------------------------------------- |
| RPW-01 | Valid token resets and lands on /login with the banner      | Happy path    | P1       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-02 | Missing token shows the no-token alert and request link     | Empty state   | P1       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-03 | Empty `?token=` behaves like missing                        | Empty state   | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-04 | Expired token (400 with detail) shows the server detail     | Error         | P1       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-05 | Used token (400 without detail) shows the invalid-link text | Error         | P1       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-06 | After a token error the user can request a new link         | Navigation    | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-07 | Password shorter than 8 keeps submit disabled               | Validation    | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-08 | Password mismatch alert                                     | Validation    | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-09 | Server error (500) is not reported as a bad link            | Error         | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-10 | Non-JSON error body does not show raw HTML                  | Error         | P2       | Yes (`ResetPassword.test.tsx`, `lib/api.test.ts`) |
| RPW-11 | Network failure shows the generic error                     | Error         | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-12 | Submitting state: one request, Loading label                | Loading       | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-13 | Token with special characters is sent exactly               | Validation    | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-14 | Back after a successful reset does not reopen the form      | Navigation    | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-15 | Reset while signed in still shows the sign-in banner        | Permissions   | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-16 | Crash during render shows a fallback; Back recovers         | Error         | P1       | Yes (`App.cases.test.tsx`)                        |
| RPW-17 | Arabic / RTL                                                | RTL           | P2       | Yes (`ResetPassword.test.tsx`)                    |
| RPW-18 | Light and dark theme                                        | Theme         | P3       | No                                                |
| RPW-19 | Phone width 375px                                           | Responsive    | P3       | No                                                |
| RPW-20 | Keyboard and labels                                         | Accessibility | P2       | Yes (`ResetPassword.test.tsx`)                    |

## Cases

### RPW-01 — Valid token resets and lands on /login with the banner

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** signed out; `POST /api/auth/reset-password` → `204`.
- **Steps:**
  1. Open `/reset-password?token=tok123`. Heading "Choose a new password".
  2. Type `newpass12` in "New password" and "Confirm password"; click "Reset password".
- **Expected:** One POST with body `{ "token": "tok123", "password": "newpass12" }`. Location `/login` (replace). `role="status"` "Your password was reset. Please sign in." shown on the login page.

### RPW-02 — Missing token shows the no-token alert and request link

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** none.
- **Steps:**
  1. Open `/reset-password` (no query).
- **Expected:** `role="alert"` "This reset link is missing its token."; link "Request a new reset link" with `href="/forgot-password"`. No password inputs, no submit button. No POST.

### RPW-03 — Empty `?token=` behaves like missing

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** URLs `/reset-password?token=` and `/reset-password?other=1`.
- **Steps:**
  1. Open each.
- **Expected:** Same as RPW-02. A token of only spaces (`?token=%20%20`) currently counts as present and shows the form — expected the no-token state (currently not trimmed — minor, likely bug).

### RPW-04 — Expired token (400 with detail) shows the server detail

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** POST → `400` problem+json `{ "type": "about:blank", "title": "Bad Request", "status": 400, "detail": "reset token expired" }`.
- **Steps:**
  1. Submit a valid password.
- **Expected:** `role="alert"` "reset token expired". Still on `/reset-password?token=...`. Fields keep values; button enabled again.

### RPW-05 — Used token (400 without detail) shows the invalid-link text

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** POST → `400 { "type": "about:blank", "title": "Bad Request", "status": 400 }` (simulate a token already used).
- **Steps:**
  1. Submit.
- **Expected:** "This reset link is invalid or has expired." (`reset.invalid`).

### RPW-06 — After a token error the user can request a new link

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** as RPW-05.
- **Steps:**
  1. After the error, look for a way to get a new link.
- **Expected:** A "Request a new reset link" link to `/forgot-password` is offered next to the error. (Currently only "Back to sign in" is shown in the form state; the user must go to `/login` → "Forgot password?" — currently unhandled — minor UX gap.)

### RPW-07 — Password shorter than 8 keeps submit disabled

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** token present.
- **Steps:**
  1. Type `1234567` in both fields. 2. Add one character to both.
- **Expected:** Step 1: button `disabled`, no POST on Enter. Step 2: enabled. Note: there is no visible hint saying "at least 8 characters" — expected a hint (currently none — minor UX gap).

### RPW-08 — Password mismatch alert

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** new `newpass12`, confirm `newpass13`.
- **Steps:**
  1. Type both. 2. Fix confirm.
- **Expected:** `role="alert"` "Passwords do not match." and button disabled; both clear after the fix.

### RPW-09 — Server error (500) is not reported as a bad link

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** POST → `500 { "type": "about:blank", "title": "Internal", "status": 500 }` (no detail).
- **Steps:**
  1. Submit.
- **Expected:** "Something went wrong." (`common.error`). (Currently any `ApiError` without detail shows "This reset link is invalid or has expired.", which tells the user their good link is bad — currently unhandled — likely bug.)

### RPW-10 — Non-JSON error body does not show raw HTML

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`, `lib/api.test.ts`)
- **Preconditions / data:** POST → `502` `text/html` `<html><body>Bad Gateway</body></html>`.
- **Steps:**
  1. Submit.
- **Expected:** "Something went wrong.". (Currently `api.ts` sets `detail` to the raw body slice and the page shows it — currently unhandled — likely bug.)

### RPW-11 — Network failure shows the generic error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** `fetch` rejects.
- **Steps:**
  1. Submit.
- **Expected:** "Something went wrong."; button enabled; form kept.

### RPW-12 — Submitting state: one request, Loading label

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** POST held pending.
- **Steps:**
  1. Submit; click again.
- **Expected:** Button "Loading…", disabled; one POST; old error cleared when a new submit starts.

### RPW-13 — Token with special characters is sent exactly

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** URLs `/reset-password?token=a%2Bb%2Fc%3D` and `/reset-password?token=a+b`.
- **Steps:**
  1. Submit from each.
- **Expected:** First sends `"token": "a+b/c="`. Second sends `"token": "a b"` (URLSearchParams turns `+` into a space) — confirm the server mints URL-safe tokens or the email link encodes `+`; flag if tokens can contain `+`.

### RPW-14 — Back after a successful reset does not reopen the form

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** history `/forgot-password` → `/reset-password?token=tok123`; reset succeeds.
- **Steps:**
  1. On `/login`, press browser Back.
- **Expected:** Goes to `/forgot-password` (the reset page was replaced), not back to the used token form.

### RPW-15 — Reset while signed in still shows the sign-in banner

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** `/api/auth/me` → user (another tab is signed in); POST → `204` (server revokes all sessions).
- **Steps:**
  1. Open `/reset-password?token=tok123`; submit.
- **Expected:** User ends on `/login` with the reset banner and the local user cleared. (Currently Login sees the stale `user`, forwards to `/library`, the banner is never seen and the next API call 401s — currently unhandled — likely bug.)

### RPW-16 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`App.cases.test.tsx`)
- **Preconditions / data:** mock `../../lib/i18n` so `t("reset.title")` throws. Page is outside `AppShell`.
- **Steps:**
  1. From `/forgot-password`, navigate to `/reset-password?token=x`.
  2. Press browser Back.
- **Expected:** Error fallback instead of a blank page; Back renders `/forgot-password` without a refresh. (No boundary on public routes today — currently unhandled — likely bug.)

### RPW-17 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`.
- **Steps:**
  1. Open without a token; then with a token and trigger RPW-05.
- **Expected:** `dir="rtl"`. "اختر كلمة مرور جديدة", "رابط إعادة التعيين يفتقد الرمز.", "اطلب رابط إعادة تعيين جديد", "كلمة المرور الجديدة", "إعادة تعيين كلمة المرور", "رابط إعادة التعيين غير صالح أو منتهي الصلاحية.". After success, login banner "تمت إعادة تعيين كلمة مرورك. الرجاء تسجيل الدخول.".

### RPW-18 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light / dark.
- **Steps:**
  1. View the no-token alert and the form with an error.
- **Expected:** Readable in both; alerts use tokens.

### RPW-19 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812; a very long token (500 chars) in the URL.
- **Steps:**
  1. Open the page.
- **Expected:** No horizontal scroll (the token is never rendered, so it cannot widen the page).

### RPW-20 — Keyboard and labels

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`ResetPassword.test.tsx`)
- **Preconditions / data:** token present.
- **Steps:**
  1. Tab through; submit with Enter.
- **Expected:** "New password" focused on load; inputs found by label "New password" / "Confirm password"; both `autocomplete="new-password"`; form accessible name "Reset password"; alerts `role="alert"`; focus visible.
