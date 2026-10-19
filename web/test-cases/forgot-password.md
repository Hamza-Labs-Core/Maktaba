# Forgot password

- **Route:** `/forgot-password`
- **Component:** `src/pages/Auth/ForgotPassword.tsx`; no child components
- **API calls:** `POST /api/auth/forgot-password` `{ email }` → `200 { ok: true }` always (anti-enumeration) via `api.post` in `src/lib/api.ts`. `GET /api/auth/me` (AuthProvider probe, not used by the page).
- **Access:** public (outside `RequireAuth` and `AppShell`). The page does not check auth.
- **Existing tests:** none (no `ForgotPassword.test.tsx`). Gaps: everything.

## Summary

| ID     | Title                                                | Category      | Priority | Covered by existing test? |
| ------ | ---------------------------------------------------- | ------------- | -------- | ------------------------- |
| FPW-01 | Sending the link shows the neutral confirmation      | Happy path    | P1       | No                        |
| FPW-02 | Same confirmation for a known and an unknown address | Permissions   | P1       | No                        |
| FPW-03 | Submit disabled while email is empty or spaces       | Validation    | P2       | No                        |
| FPW-04 | Invalid email format is not sent                     | Validation    | P3       | No                        |
| FPW-05 | Server error still shows the neutral confirmation    | Error         | P2       | No                        |
| FPW-06 | Network failure still shows the neutral confirmation | Error         | P2       | No                        |
| FPW-07 | Submitting state: one request, Loading label         | Loading       | P2       | No                        |
| FPW-08 | "Back to sign in" link before and after sending      | Navigation    | P2       | No                        |
| FPW-09 | Deep link and Back/Forward                           | Navigation    | P3       | No                        |
| FPW-10 | Signed-in user can still open the page               | Permissions   | P3       | No                        |
| FPW-11 | Confirmation is announced to screen readers          | Accessibility | P2       | No                        |
| FPW-12 | Crash during render shows a fallback; Back recovers  | Error         | P1       | No                        |
| FPW-13 | Arabic / RTL                                         | RTL           | P2       | No                        |
| FPW-14 | Light and dark theme                                 | Theme         | P3       | No                        |
| FPW-15 | Phone width 375px                                    | Responsive    | P3       | No                        |
| FPW-16 | Keyboard and labels                                  | Accessibility | P2       | No                        |

## Cases

### FPW-01 — Sending the link shows the neutral confirmation

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `POST /api/auth/forgot-password` → `200 { "ok": true }`.
- **Steps:**
  1. Open `/forgot-password`. Heading "Reset your password", hint "Enter your email and we'll send a reset link if an account exists.".
  2. Type `amal@example.com` in "Email"; click "Send reset link".
- **Expected:** One POST with body `{ "email": "amal@example.com" }`. Input and button disappear; text "If an account matches that email, a reset link is on its way." and a "Back to sign in" link to `/login`.

### FPW-02 — Same confirmation for a known and an unknown address

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** two runs: response `200 { "ok": true }` for `known@example.com`, and `200 { "ok": true }` (or `404`) for `nobody@example.com`.
- **Steps:**
  1. Submit each in a fresh render.
- **Expected:** Rendered DOM after submit is identical in both runs (same text, no extra alert). Nothing on screen says whether the account exists.

### FPW-03 — Submit disabled while email is empty or spaces

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** none.
- **Steps:**
  1. Check the button empty. 2. Type `"   "`. 3. Type a real address.
- **Expected:** Disabled for 1 and 2 (`email.trim() === ""`), enabled for 3. Enter in an empty field sends no POST.

### FPW-04 — Invalid email format is not sent

- **Priority:** P3
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** email `amal`.
- **Steps:**
  1. Submit (browser e2e).
- **Expected:** Native `type="email"` validation blocks submit; no POST; the form stays.

### FPW-05 — Server error still shows the neutral confirmation

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** POST → `500` problem+json; repeat with `429` and `403` (missing CSRF).
- **Steps:**
  1. Submit.
- **Expected:** Same confirmation as FPW-01 (by design: errors are swallowed so nothing leaks). No error alert. Console shows the `[api] POST /api/auth/forgot-password failed` log. Note: on `429` the user is told a link is coming when none was sent — product decision, flag for review.

### FPW-06 — Network failure still shows the neutral confirmation

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `fetch` rejects.
- **Steps:**
  1. Submit.
- **Expected:** Confirmation shown; no unhandled rejection.

### FPW-07 — Submitting state: one request, Loading label

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** POST held pending.
- **Steps:**
  1. Submit; click again.
- **Expected:** Button "Loading…" and `disabled`; exactly one POST; confirmation appears after the promise settles.

### FPW-08 — "Back to sign in" link before and after sending

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** none.
- **Steps:**
  1. Click "Back to sign in" on the form. 2. Return, send, click "Back to sign in" again.
- **Expected:** Both links `href="/login"`; login page renders.

### FPW-09 — Deep link and Back/Forward

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** history `/login` → `/forgot-password`.
- **Steps:**
  1. Send the link. 2. Browser Back. 3. Browser Forward.
- **Expected:** Back shows `/login`. Forward shows the fresh form again (state is not kept) — no crash.

### FPW-10 — Signed-in user can still open the page

- **Priority:** P3
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `/api/auth/me` → user.
- **Steps:**
  1. Open `/forgot-password`.
- **Expected:** The form renders (the page does not redirect signed-in users). The "Back to sign in" link then forwards to `/library` via Login.

### FPW-11 — Confirmation is announced to screen readers

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** as FPW-01.
- **Steps:**
  1. Submit; check the confirmation element and focus.
- **Expected:** Confirmation text is in a live region (`role="status"`) or focus moves to it. (Currently a plain `<p>` and the focused input is removed, so focus drops to `body` and nothing is announced — currently unhandled — likely bug.)

### FPW-12 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** mock `../../lib/i18n` so `t("forgot.title")` throws. Page is outside `AppShell`.
- **Steps:**
  1. From `/login`, click "Forgot password?".
  2. Press browser Back.
- **Expected:** Error fallback instead of a blank page; Back renders `/login` without a refresh. (No boundary on public routes today — currently unhandled — likely bug.)

### FPW-13 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`.
- **Steps:**
  1. Open the page; send.
- **Expected:** `dir="rtl"`. Title "إعادة تعيين كلمة المرور", label "البريد الإلكتروني", button "إرسال رابط إعادة التعيين", confirmation "إن كان هناك حساب مطابق لهذا البريد، فإن رابط إعادة التعيين في طريقه إليك.", link "العودة لتسجيل الدخول". Layout right-aligned.

### FPW-14 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light / dark.
- **Steps:**
  1. View form and confirmation.
- **Expected:** Readable in both; hint text has enough contrast.

### FPW-15 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812.
- **Steps:**
  1. Open the page; send.
- **Expected:** No horizontal scroll; long confirmation text wraps.

### FPW-16 — Keyboard and labels

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** none.
- **Steps:**
  1. Tab through; submit with Enter.
- **Expected:** Email focused on load; `getByLabelText("Email")` works; `autocomplete="email"`; form accessible name "Send reset link"; Tab order Email → Send reset link → Back to sign in (the button is skipped while disabled); focus visible.
