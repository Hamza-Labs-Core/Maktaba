# Settings: Account tab and tab nav

- **Route:** `/settings` (index tab, Account). This file also covers the shared Settings tab nav for every sub-route: `/settings/sessions`, `/settings/appearance`, `/settings/privacy`, `/settings/models`, `/settings/rules`, `/settings/about`, and unknown sub-routes such as `/settings/nope`.
- **Component:** `src/pages/Settings.tsx` (`Settings`, `AccountTab`, `DiagnosticsSection`); mounted in `src/App.tsx` as `<Route path="/settings/*" element={<Settings />} />` inside `RequireAuth` + `AppShell`.
- **API calls:** `POST /api/auth/logout-all` via `useAuth().logoutAll()` in `src/lib/auth.tsx` (errors are caught and only `console.warn`ed; the local user is always cleared); `GET /api/diagnostics/export` via `downloadBlob(path, "maktaba-diagnostics.tar.gz")` in `src/lib/api.ts` (binary download; filename from `Content-Disposition` when present; non-2xx throws `ApiError` with `detail` = first 256 chars of the body; network failure rethrows the fetch error).
- **Access:** signed-in user (`RequireAuth`, signed-out redirects to `/login` with `state.from`). No `AdminGate` on any Settings tab. Account shows "(admin)" after the username when `user.is_admin`.
- **Existing tests:** none for `Settings.tsx` (no `Settings.test.tsx`). Gaps: everything below.

Test harness hint: render `<I18nProvider><ToastProvider><MemoryRouter initialEntries={[path]}><Routes><Route path="/settings/*" element={<Settings />} /><Route path="/login" element={<p>login page</p>} /></Routes></MemoryRouter></ToastProvider></I18nProvider>` with `vi.mock("../lib/auth")` returning `{ user, logoutAll }`. Mock child sections (`./Settings/ModelsSection`, `./Settings/RulesSection`, `./Settings/UpdatesSettings`) when testing only the nav.

## Summary

| ID     | Title                                                     | Category      | Priority | Covered by existing test? |
| ------ | --------------------------------------------------------- | ------------- | -------- | ------------------------- |
| SAC-01 | Account tab shows the signed-in username                  | Happy path    | P1       | Yes (`Settings.test.tsx`) |
| SAC-02 | Admin user sees "(admin)" after the name                  | Permissions   | P2       | Yes (`Settings.test.tsx`) |
| SAC-03 | Signed-out visit redirects to /login                      | Permissions   | P1       | Yes (`Settings.test.tsx`) |
| SAC-04 | Sign out everywhere calls logout-all and goes to /login   | Happy path    | P1       | Yes (`Settings.test.tsx`) |
| SAC-05 | Sign out everywhere when logout-all fails                 | Error         | P1       | No (it.fails: bug)        |
| SAC-06 | Back after sign out everywhere does not reopen Settings   | Navigation    | P1       | Yes (`Settings.test.tsx`) |
| SAC-07 | Collect diagnostics downloads the bundle                  | Happy path    | P2       | Yes (`Settings.test.tsx`) |
| SAC-08 | Diagnostics button is busy while collecting               | Loading       | P2       | Yes (`Settings.test.tsx`) |
| SAC-09 | Diagnostics error toast on 500 / 403 / 401                | Error         | P2       | Yes (`Settings.test.tsx`) |
| SAC-10 | Diagnostics error toast on network failure                | Error         | P2       | Yes (`Settings.test.tsx`) |
| SAC-11 | Personal access tokens text is stale                      | Navigation    | P3       | No                        |
| SAC-12 | Tab nav lists all seven tabs with correct hrefs           | Navigation    | P1       | Yes (`Settings.test.tsx`) |
| SAC-13 | Deep link to each sub-route renders that tab              | Navigation    | P1       | Yes (`Settings.test.tsx`) |
| SAC-14 | Clicking a tab link switches the panel                    | Navigation    | P1       | Yes (`Settings.test.tsx`) |
| SAC-15 | Active tab is marked (aria-current)                       | Accessibility | P2       | Yes (`Settings.test.tsx`) |
| SAC-16 | Unknown sub-route `/settings/nope`                        | Navigation    | P2       | Yes (`Settings.test.tsx`) |
| SAC-17 | Back / Forward move between tabs                          | Navigation    | P2       | Yes (`Settings.test.tsx`) |
| SAC-18 | Render crash shows route error fallback; Back still works | Error         | P1       | Yes (`Settings.test.tsx`) |
| SAC-19 | Phone width: tab nav fits, no horizontal scroll           | Responsive    | P2       | No (e2e-only)             |
| SAC-20 | Arabic: RTL layout and Arabic strings                     | RTL           | P2       | Yes (`Settings.test.tsx`) |
| SAC-21 | Light and dark theme readable                             | Theme         | P3       | No                        |
| SAC-22 | Keyboard: tab order through nav and actions               | Accessibility | P3       | No                        |

## Cases

### SAC-01 — Account tab shows the signed-in username

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `useAuth()` → `{ user: { id: "u1", username: "maha", is_admin: false }, logoutAll }`.
- **Steps:**
  1. Open `/settings`.
- **Expected:** `h1` "Settings" (`nav.settings`). Text "Signed in as" (`settings.signedInAs`) followed by bold "maha". No "(admin)". A destructive button "Sign out everywhere". The muted text `settings.tokens.deferred`. A "Diagnostics" `h2` with hint text and a "Collect diagnostics" button.

### SAC-02 — Admin user sees "(admin)" after the name

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `user: { id: "u1", username: "root", is_admin: true }`.
- **Steps:**
  1. Open `/settings`.
- **Expected:** "Signed in as root (admin)" (`settings.admin`). Same buttons as a normal user (no admin-only controls on this tab).

### SAC-03 — Signed-out visit redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full `App` (or `RequireAuth` wrapper); `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/settings`, then repeat with `/settings/privacy` and `/settings/rules`.
- **Expected:** Redirect to `/login` (replace) with `state.from` = the requested path. No Settings content and no Settings API calls (`/api/me/history`, `/api/rules`, `/api/models`, `/api/system/version`) are made.

### SAC-04 — Sign out everywhere calls logout-all and goes to /login

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `logoutAll` mock resolves; `POST /api/auth/logout-all` → 204.
- **Steps:**
  1. Open `/settings`.
  2. Click "Sign out everywhere".
- **Expected:** `logoutAll` called once. Router lands on `/login` (replace). With the real `AuthProvider`, the request is `POST /api/auth/logout-all` with the `X-Maktaba-CSRF` header when the `mkt_csrf` cookie is set. Note: there is no confirmation step for this destructive action; if a confirm dialog is added, assert it here.

### SAC-05 — Sign out everywhere when logout-all fails

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No (it.fails: bug)
- **Preconditions / data:** real `AuthProvider`; `POST /api/auth/logout-all` → 500 problem+json, then repeat with fetch rejecting (network).
- **Steps:**
  1. Open `/settings`, click "Sign out everywhere".
- **Expected (wanted):** the user is told that other devices may still be signed in (error toast), since the server did not revoke them. **Current code:** the error is only `console.warn("auth: logout-all call failed", ...)`, the local user is cleared and the app goes to `/login` as if it worked (currently unhandled — likely bug: silent failure of a security action).

### SAC-06 — Back after sign out everywhere does not reopen Settings

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full app; start at `/library`, then go to `/settings`.
- **Steps:**
  1. Click "Sign out everywhere".
  2. Press browser Back.
- **Expected:** Still on `/login` (or redirected back to it by `RequireAuth`). No Settings content or user data is shown from cache.

### SAC-07 — Collect diagnostics downloads the bundle

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `fetch("/api/diagnostics/export")` → 200, body a Blob, header `Content-Disposition: attachment; filename="diag-u1.tar.gz"`. Stub `URL.createObjectURL` / `revokeObjectURL`; spy on `HTMLAnchorElement.prototype.click`.
- **Steps:**
  1. Open `/settings`, click "Collect diagnostics".
- **Expected:** One GET with `credentials: "include"`. An anchor with `download="diag-u1.tar.gz"` is clicked and removed. Success toast "Diagnostics bundle downloaded." (`settings.diagnostics.success`, role `status`). Without `Content-Disposition`, the filename is `maktaba-diagnostics.tar.gz`.

### SAC-08 — Diagnostics button is busy while collecting

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** fetch returns a never-resolving promise, then resolve it.
- **Steps:**
  1. Click "Collect diagnostics".
  2. Click it again while pending.
- **Expected:** Button text becomes "Collecting…" (`settings.diagnostics.running`) and is disabled; the second click makes no second request. After resolve, text goes back to "Collect diagnostics" and the button is enabled.

### SAC-09 — Diagnostics error toast on 500 / 403 / 401

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** fetch → 500 with body `boom`, statusText `Internal Server Error`; repeat with 403 and 401.
- **Steps:**
  1. Click "Collect diagnostics".
- **Expected:** Error toast (role `alert`) "Could not collect diagnostics: Internal Server Error: boom". Button back to enabled. No download triggered. `console.error` line starting `[api] GET /api/diagnostics/export failed (500)`.

### SAC-10 — Diagnostics error toast on network failure

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** fetch rejects `new TypeError("Failed to fetch")`.
- **Steps:**
  1. Click "Collect diagnostics".
- **Expected:** Error toast "Could not collect diagnostics: Something went wrong." (`common.error` used for non-ApiError). Button re-enabled.

### SAC-11 — Personal access tokens text is stale

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** any user.
- **Steps:**
  1. Open `/settings`.
- **Expected (wanted):** a link to the real tokens page `/account/tokens` (route exists in `App.tsx`, `APITokens`). **Current:** the text "Personal access token management requires a backend endpoint that is not yet available." (`settings.tokens.deferred`) with no link (currently unhandled — likely bug: stale copy).

### SAC-12 — Tab nav lists all seven tabs with correct hrefs

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** any user; open `/settings`.
- **Steps:**
  1. Get `navigation` named "Settings".
- **Expected:** Seven links in order: Account → `/settings`, Sessions → `/settings/sessions`, Appearance → `/settings/appearance`, Privacy → `/settings/privacy`, Models → `/settings/models`, Rules → `/settings/rules`, About → `/settings/about`. Hrefs are the same when the current URL is a sub-route (e.g. from `/settings/rules`, "Account" still points to `/settings`, not `/settings/rules`).

### SAC-13 — Deep link to each sub-route renders that tab

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** mock the APIs each tab needs (see the per-tab files).
- **Steps:**
  1. Open each URL directly: `/settings`, `/settings/sessions`, `/settings/appearance`, `/settings/privacy`, `/settings/models`, `/settings/rules`, `/settings/about`. Also `/settings/` (trailing slash).
- **Expected:** The matching panel renders (Account text; `settings.sessions.deferred`; "Theme" radiogroup; "Activity tracking" heading; "Model Management" heading; "Rules" heading; "Current version" term). Only that tab's API calls are made. `/settings/` shows the Account tab.

### SAC-14 — Clicking a tab link switches the panel

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** start at `/settings`.
- **Steps:**
  1. Click "Appearance", then "About", then "Account".
- **Expected:** URL and panel change each time without a full reload; the `h1` and nav stay mounted. The previous panel is gone.

### SAC-15 — Active tab is marked (aria-current)

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** open `/settings/privacy`.
- **Steps:**
  1. Inspect the "Privacy" link.
- **Expected (wanted):** the current tab link has `aria-current="page"` and a visible active style; only one link is current, and "Account" is not current on sub-routes. **Current:** plain `<Link>`s, no active state at all (currently unhandled — likely bug).

### SAC-16 — Unknown sub-route `/settings/nope`

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** any user.
- **Steps:**
  1. Open `/settings/nope`; also `/settings/rules/extra`.
- **Expected (wanted):** a not-found message inside Settings (or redirect to `/settings`), with the nav still usable. **Current:** the nested `<Routes>` has no `*` route, so only the `h1` and nav render with an empty panel area (currently unhandled — likely bug). Clicking any tab link from there works.

### SAC-17 — Back / Forward move between tabs

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** start at `/settings`.
- **Steps:**
  1. Click "Appearance", then "Privacy".
  2. Press Back twice, then Forward once.
- **Expected:** Back → `/settings/appearance` (Appearance panel), Back → `/settings` (Account panel), Forward → `/settings/appearance`. Each panel renders fully with no stale content from the other tab.

### SAC-18 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full app with `AppShell`. Start at `/library`, then go to `/settings`. Make `AccountTab` throw while rendering: `useAuth()` returns `user: { id: "u1", username: { bad: true }, is_admin: false }` (React throws "Objects are not valid as a React child"). For the shared nav, also try a crashing tab: `/settings/privacy` with `GET /api/me/history` → `{}` (see SPR).
- **Steps:**
  1. Open `/settings`.
  2. Press browser Back.
  3. Go forward to `/settings` again, then click a sidebar link (e.g. Library).
- **Expected:** A route-level error fallback (not a blank page) inside the shell; sidebar still visible. Back renders `/library` without a reload. Clicking a nav link renders that page. The error is logged once.

### SAC-19 — Phone width: tab nav fits, no horizontal scroll

- **Priority:** P2
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** browser e2e at 375x812, English and Arabic.
- **Steps:**
  1. Open `/settings`.
  2. Check `document.documentElement.scrollWidth <= innerWidth`.
- **Expected:** All seven tab links are reachable (wrap or scroll inside the nav only); the page itself has no horizontal scroll; buttons are full width or fit. Note: `.mkt-settings__nav` is `display:flex` with no `flex-wrap` (`src/styles/app.css` ~line 399), so seven links likely overflow at 375px (likely bug).

### SAC-20 — Arabic: RTL layout and Arabic strings

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"` before render; username "مها".
- **Steps:**
  1. Open `/settings`.
- **Expected:** `<html dir="rtl" lang="ar">`. `h1` "الإعدادات"; tabs "الحساب", "الجلسات", "المظهر", "الخصوصية", "النماذج", "القواعد", "حول"; "تسجيل الدخول باسم مها"; button "تسجيل الخروج من كل الأجهزة"; "جمع التشخيص". Nav order is mirrored (Account at the right). No raw i18n keys visible.

### SAC-21 — Light and dark theme readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `<html data-theme="light">`, then `"dark"`.
- **Steps:**
  1. Open `/settings` in each theme (e2e screenshot or axe contrast).
- **Expected:** Text, muted text, the panel border and the destructive button meet contrast in both themes; no hardcoded colours.

### SAC-22 — Keyboard: tab order through nav and actions

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** open `/settings`.
- **Steps:**
  1. Press Tab from the top of the main content.
  2. Press Enter on "Privacy".
- **Expected:** Focus goes through the seven tab links in order, then "Sign out everywhere", then "Collect diagnostics", each with a visible focus ring. Enter on a link navigates. The toast after diagnostics is announced (status/alert live region). `axe` (`src/test/axe.ts`) reports no violations.
