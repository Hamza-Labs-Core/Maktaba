# Login

- **Route:** `/login` (query `?next=<path>`; router state `{ from?: string }`)
- **Component:** `src/pages/Login.tsx`; children: `src/components/ClusterSignIn.tsx`
- **API calls:** `POST /api/auth/login` `{ username, password }` → `{ user: { id, username, is_admin } }` via `useAuth().login` in `src/lib/auth.tsx`; `GET /api/auth/me` → `{ user }` (session probe in `AuthProvider`, 401 = signed out); `GET /api/cluster/self` → `ClusterSelf` via `clusterApi.getSelf()` in `src/lib/clusterApi.ts` (ClusterSignIn prompt); full-page navigation via `assignLocation()` in `src/lib/navigation.ts` for `?next=/api/cluster/sso/authorize...` and the cluster "Continue with" button (`/api/cluster/sso/start?return=...`)
- **Access:** public (outside `RequireAuth` and outside `AppShell`). A signed-in user is sent on at once.
- **Existing tests:** `src/pages/Login.test.tsx` (mocks `useAuth`) — covers: `?next=` SSO authorize path does a full page load after sign-in; already signed in with `?next=` authorize continues once; `?next=/videos/42` opens with the router; unsafe `?next` values (`http://evil...`, `//evil`, `/\evil`, `javascript:`) fall back to `/library`. `src/components/ClusterSignIn.test.tsx` — covers the prompt itself (home name, generic name, return path, hidden on home / no sso / error). Gaps: `state.from` redirect, already-signed-in without `next`, all error messages, submitting state, links, RTL/theme/a11y, crash recovery.

## Summary

| ID     | Title                                                           | Category      | Priority | Covered by existing test?                                |
| ------ | --------------------------------------------------------------- | ------------- | -------- | -------------------------------------------------------- |
| LGN-01 | Sign in goes to /library by default                             | Happy path    | P1       | Yes (`Login.cases.test.tsx`)                             |
| LGN-02 | Sign in returns to `state.from`                                 | Navigation    | P1       | Yes (`Login.cases.test.tsx`)                             |
| LGN-03 | Signed-out deep link round trip keeps the page (and its query)  | Navigation    | P1       | Yes (`Login.cases.test.tsx`)                             |
| LGN-04 | `?next=` app path wins over `state.from`                        | Navigation    | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-05 | `?next=` SSO authorize path does a full page load               | Happy path    | P1       | Yes (`Login.test.tsx`)                                   |
| LGN-06 | Unsafe `?next=` falls back to /library                          | Permissions   | P1       | Yes (`Login.test.tsx`)                                   |
| LGN-07 | Unsafe `state.from` falls back to /library                      | Permissions   | P1       | Yes (`Login.cases.test.tsx`)                             |
| LGN-08 | Already signed in: /login redirects to /library at once         | Navigation    | P1       | Yes (`Login.cases.test.tsx`)                             |
| LGN-09 | Already signed in with `state.from`: goes there                 | Navigation    | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-10 | Session probe finishes after render: page moves on              | Loading       | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-11 | Wrong password (401) shows the invalid-credentials alert        | Error         | P1       | Yes (`Login.cases.test.tsx`)                             |
| LGN-12 | Server error (500) shows the generic alert                      | Error         | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-13 | Network failure shows the generic alert                         | Error         | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-14 | 200 with a body lacking `user` does not loop silently           | Error         | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-15 | Submitting state: button disabled, one request only             | Loading       | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-16 | Old error clears on the next attempt                            | Error         | P3       | No                                                       |
| LGN-17 | Empty username or password does not submit                      | Validation    | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-18 | Temporary password: sign-in shows the required change form      | Happy path    | P1       | Yes (`Login.cases.test.tsx`, `auth.mustChange.test.tsx`) |
| LGN-19 | Cluster prompt on a node; hidden while continuing SSO           | Happy path    | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-20 | `/api/cluster/self` with `home: null` / fails: form still works | Empty state   | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-21 | Register link; no forgot-password link                          | Navigation    | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-22 | Back after sign-in does not return to /login                    | Navigation    | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-23 | Crash during render shows a fallback; Back recovers             | Error         | P1       | Yes (`App.cases.test.tsx`)                               |
| LGN-24 | Arabic / RTL                                                    | RTL           | P2       | Yes (`Login.cases.test.tsx`)                             |
| LGN-25 | Light and dark theme                                            | Theme         | P3       | No                                                       |
| LGN-26 | Phone width 375px                                               | Responsive    | P3       | No                                                       |
| LGN-27 | Keyboard and labels                                             | Accessibility | P2       | Yes (`Login.cases.test.tsx`)                             |

## Cases

### LGN-01 — Sign in goes to /library by default

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** real `AuthProvider`; `GET /api/auth/me` → 401; `POST /api/auth/login` → `200 { "user": { "id": "u1", "username": "amal", "is_admin": false } }`; `GET /api/cluster/self` rejects.
- **Steps:**
  1. Open `/login`.
  2. Type `amal` in "Username" and `secret` in "Password".
  3. Click "Sign in".
- **Expected:** `POST /api/auth/login` sent with body `{ "username": "amal", "password": "secret" }`. Location becomes `/library`. The form is gone.

### LGN-02 — Sign in returns to `state.from`

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** MemoryRouter initial entry `{ pathname: "/login", state: { from: "/videos/42" } }`; login resolves.
- **Steps:**
  1. Sign in as in LGN-01.
- **Expected:** Location becomes `/videos/42`, not `/library`. `assignLocation` not called.

### LGN-03 — Signed-out deep link round trip keeps the page (and its query)

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** full `App` routes (RequireAuth + AppShell); `GET /api/auth/me` → 401; login resolves with a user.
- **Steps:**
  1. Open `/search?q=quran` signed out.
  2. Expect to land on `/login`.
  3. Sign in.
- **Expected:** Lands back on `/search?q=quran` with the query intact. (Currently `RequireAuth` stores only `location.pathname` in `state.from`, so the user lands on `/search` without `?q=` — currently unhandled — likely bug.) Same check with `/videos/42` (no query) should already pass.

### LGN-04 — `?next=` app path wins over `state.from`

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** entry `/login?next=%2Fqueue` with state `{ from: "/videos/42" }`.
- **Steps:**
  1. Sign in.
- **Expected:** Location becomes `/queue`.

### LGN-05 — `?next=` SSO authorize path does a full page load

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Login.test.tsx` "continues the cluster sign-in with a full page load"
- **Preconditions / data:** `next=/api/cluster/sso/authorize?node=n1&challenge=abc&state=s&return=%2Flibrary` (URL-encoded).
- **Steps:**
  1. Sign in.
- **Expected:** `assignLocation` called once with the authorize path (prefixed by `VITE_API_BASE` if set). Router location does not change. The ClusterSignIn prompt is not shown on this page.

### LGN-06 — Unsafe `?next=` falls back to /library

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes — `Login.test.tsx` "ignores %s and goes to the library"
- **Preconditions / data:** `next` values: `http://evil.example/x`, `//evil.example/x`, `/\evil.example/x`, `javascript:alert(1)`, and one with a newline (`/library%0A//evil`).
- **Steps:**
  1. Sign in.
- **Expected:** Location `/library`; `assignLocation` never called. Add the control-character value (not in the existing `it.each`).

### LGN-07 — Unsafe `state.from` falls back to /library

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** state `{ from: "//evil.example/x" }`, then `{ from: "https://evil.example" }`, then `{ from: 42 }` (non-string).
- **Steps:**
  1. Sign in.
- **Expected:** Location `/library` each time; no full-page navigation; no throw.

### LGN-08 — Already signed in: /login redirects to /library at once

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** `GET /api/auth/me` → `{ "user": { "id": "u1", "username": "amal", "is_admin": false } }`.
- **Steps:**
  1. Open `/login` with no query.
- **Expected:** Location becomes `/library` (replace). The login form is never visible (component returns `null` while `user` is set). Browser Back does not return to `/login`.

### LGN-09 — Already signed in with `state.from`: goes there

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** signed in; entry `/login` with state `{ from: "/series/7" }`.
- **Steps:**
  1. Render.
- **Expected:** Location `/series/7`.

### LGN-10 — Session probe finishes after render: page moves on

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** `GET /api/auth/me` held pending (deferred promise).
- **Steps:**
  1. Open `/login`. Form shows (user is still null while the probe runs).
  2. Resolve `/me` with a user.
- **Expected:** Page navigates to `/library` without the user doing anything; navigation happens once (the `left` ref guards a double `leave()`).

### LGN-11 — Wrong password (401) shows the invalid-credentials alert

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** `POST /api/auth/login` → `401` `application/problem+json` `{ "type": "about:blank", "title": "Unauthorized", "status": 401 }`.
- **Steps:**
  1. Sign in with a wrong password.
- **Expected:** `role="alert"` with "Invalid username or password." (`login.error`). Still on `/login`. Username and password fields keep their values. Button text back to "Sign in" and enabled.

### LGN-12 — Server error (500) shows the generic alert

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** login → `500` problem+json; repeat with `403` and with a `text/html` 502 body.
- **Steps:**
  1. Submit.
- **Expected:** `role="alert"` "Something went wrong." (`common.error`). No raw server text shown.

### LGN-13 — Network failure shows the generic alert

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** `fetch` rejects with `TypeError("Failed to fetch")` for `/api/auth/login`.
- **Steps:**
  1. Submit.
- **Expected:** "Something went wrong." alert; button re-enabled; no unhandled rejection.

### LGN-14 — 200 with a body lacking `user` does not loop silently

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** full App routes; login → `200` `application/json` `{}` (or `text/html` body).
- **Steps:**
  1. Submit.
- **Expected:** An error alert stays on `/login`. (Currently `setUser(undefined)` then `leave()` navigates to `/library`, `RequireAuth` bounces back to `/login`, and the user sees an empty form with no message — currently unhandled — likely bug.)

### LGN-15 — Submitting state: button disabled, one request only

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** login promise held pending.
- **Steps:**
  1. Submit, then click the button again and press Enter in the password field.
- **Expected:** Button shows "Loading…" and is `disabled`. Exactly one `POST /api/auth/login`.

### LGN-16 — Old error clears on the next attempt

- **Priority:** P3
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** first login → 401, second held pending.
- **Steps:**
  1. Submit (error shows). 2. Submit again.
- **Expected:** Alert disappears as soon as the second submit starts.

### LGN-17 — Empty username or password does not submit

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** none.
- **Steps:**
  1. Click "Sign in" with both fields empty. 2. Fill only username, click again.
- **Expected:** No `POST /api/auth/login` (inputs are `required`; browser validation blocks submit). In a browser e2e, the native "fill out this field" bubble appears on the first empty field.

### LGN-18 — Temporary password: sign-in shows the required change form

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Login.cases.test.tsx` for the sign-in, `src/lib/auth.mustChange.test.tsx` for the form).
- **Preconditions / data:** an admin reset amal's password on the Users page with "Make a temporary password" (there is no self-service reset: no mailer). `POST /api/auth/login` → `{ "user": { "id": "u1", "username": "amal", "is_admin": false, "must_change_password": true } }`.
- **Steps:**
  1. Sign in as amal with the temporary password. 2. Enter the temporary password, a new one and its confirmation; save.
- **Expected:** Step 1: only "Choose a new password" shows (no app shell); any other API call answers 403 `password-change-required`. Step 2: `POST /api/me/change-password` → 204 and the app opens. A wrong temporary password says "The temporary password is not right."; reusing it as the new one is refused before sending.

### LGN-19 — Cluster prompt on a node; hidden while continuing SSO

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** `GET /api/cluster/self` → `{ "role": "node", "id": "n1", "name": "Studio Mac", "version": "1.5.0", "schema_rev": 134, "sso": true, "home": { "name": "Home NAS", "web_url": "http://home.local:8780", "reachable": true } }`.
- **Steps:**
  1. Open `/login` with state `{ from: "/videos/42" }`; click "Continue with Home NAS".
  2. Open `/login?next=<authorize path>`.
- **Expected:** Step 1: button shown above the form with "or sign in here"; click calls `assignLocation("/api/cluster/sso/start?return=%2Fvideos%2F42")`. Step 2: no cluster button (only the form).

### LGN-20 — `/api/cluster/self` with `home: null` / fails: form still works

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** `/api/cluster/self` → node with `"home": null`; then node with no `home` key; then `500`; then malformed JSON (`text/html` 200). (No list fields on this page; this endpoint is the only fetched shape.)
- **Steps:**
  1. Open `/login`, sign in.
- **Expected:** No cluster prompt, no crash, no blank page; sign-in works as LGN-01.

### LGN-21 — Register link; no forgot-password link

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`).
- **Preconditions / data:** signed out.
- **Steps:**
  1. Look for "Forgot password?". 2. Click "Create an account". 3. Back.
- **Expected:** Step 1: no such link (an admin resets passwords on the Users page). Step 2: the link has `href="/register"` and the page renders. Step 3: Back returns to `/login`.

### LGN-22 — Back after sign-in does not return to /login

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** history `["/library", "/login"]` style e2e: open `/library` signed out (redirects to `/login`), sign in.
- **Steps:**
  1. After landing on `/library`, press browser Back.
- **Expected:** Does not show the login form again (navigation used `replace`). If it does land on `/login`, it immediately forwards because the user is signed in (LGN-08).

### LGN-23 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`App.cases.test.tsx`)
- **Preconditions / data:** mock `../components/ClusterSignIn` so `ClusterSignIn` throws during render. Note `/login` is outside `AppShell`, so the route boundary being added to `AppShell.tsx` will not cover it.
- **Steps:**
  1. Navigate `/register` → click "Back to sign in" (crashes on `/login`).
  2. Press browser Back.
- **Expected:** Step 1: an error fallback (not a blank page). Step 2: `/register` renders without a refresh. (Currently no boundary covers public routes — currently unhandled — likely bug.)

### LGN-24 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`.
- **Steps:**
  1. Open `/login`; submit a wrong password (401).
- **Expected:** `<html dir="rtl" lang="ar">`. Title "مكتبة", labels "اسم المستخدم" / "كلمة المرور", button "تسجيل الدخول", links "هل نسيت كلمة المرور؟" / "إنشاء حساب", error "اسم المستخدم أو كلمة المرور غير صحيحة.". Labels and inputs align right. Cluster prompt (LGN-19) home name stays bidi-isolated.

### LGN-25 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:theme"]` = `light`, then `dark`.
- **Steps:**
  1. Open `/login`, show an error alert.
- **Expected:** `<html data-theme>` matches. Text, inputs, alert and links readable in both; no hardcoded colours (styles use tokens).

### LGN-26 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e viewport 375x812.
- **Steps:**
  1. Open `/login` with the cluster prompt shown.
- **Expected:** No horizontal scroll; form fits; button and links reachable without zoom.

### LGN-27 — Keyboard and labels

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Login.cases.test.tsx`)
- **Preconditions / data:** signed out.
- **Steps:**
  1. Open `/login`; press Tab repeatedly; press Enter in the password field.
- **Expected:** Username is focused on load (`autoFocus`). `getByLabelText("Username")` / `("Password")` work. Form has accessible name "Sign in". Tab order: (cluster button if shown) → Username → Password → Sign in → Create an account. Enter submits. Focus ring visible. Errors use `role="alert"` so they are announced. Inputs have `autocomplete="username"` / `"current-password"`.
