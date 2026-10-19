# More menu

- **Route:** `/more` (inside `RequireAuth` + `AppShell`; reached from the "More" tab on the phone tab bar)
- **Component:** `MoreMenu` exported from `src/components/AppShell.tsx`; no child components besides `NavLink`
- **API calls:** none of its own. Reads `useAuth()` (`GET /api/auth/me` → `{ user: { id, username, is_admin } }`), sign out calls `POST /api/auth/logout` and `purgeApiCacheOnLogout()` (`src/lib/sw.ts`) via `useAuth().logout` in `src/lib/auth.tsx`.
- **Access:** signed-in user (`RequireAuth`); the "Administration" group only when `user.is_admin`.
- **Existing tests:** `src/components/AppShell.test.tsx` "puts Transcription on the More page" — a non-admin sees the Transcription link at `/transcription`; "lists Transcription in the sidebar, behind More on the six-item tab bar" — last tab is `/more`. Gaps: the rest of the list, admin group, sign out, signed-out deep link, layout, RTL, a11y, crash recovery.

## Summary

| ID     | Title                                                      | Category      | Priority | Covered by existing test?       |
| ------ | ---------------------------------------------------------- | ------------- | -------- | ------------------------------- |
| MOR-01 | Normal user sees Settings, Transcription and account links | Happy path    | P1       | Yes (`AppShell.cases.test.tsx`) |
| MOR-02 | Normal user sees no Administration group                   | Permissions   | P1       | Yes (`AppShell.cases.test.tsx`) |
| MOR-03 | Admin sees the Administration group with every admin link  | Permissions   | P1       | Yes (`AppShell.cases.test.tsx`) |
| MOR-04 | User object without `is_admin` shows no admin group        | Empty state   | P2       | Yes (`AppShell.cases.test.tsx`) |
| MOR-05 | "More" tab opens /more and is marked active                | Navigation    | P2       | Yes (`AppShell.cases.test.tsx`) |
| MOR-06 | Each link opens its page; Back returns to /more            | Navigation    | P2       | Yes (`AppShell.cases.test.tsx`) |
| MOR-07 | Sign out goes to /login and Back does not return           | Happy path    | P1       | Yes (`AppShell.cases.test.tsx`) |
| MOR-08 | Sign out still leaves when the server call fails           | Error         | P2       | Yes (`AppShell.cases.test.tsx`) |
| MOR-09 | Double-clicking Sign out sends one request                 | Validation    | P3       | No                              |
| MOR-10 | Signed-out deep link to /more, then back after sign-in     | Permissions   | P1       | Yes (`AppShell.cases.test.tsx`) |
| MOR-11 | Auth still loading on /more                                | Loading       | P3       | No                              |
| MOR-12 | Crash during render shows a fallback; Back recovers        | Error         | P1       | Yes (`AppShell.cases.test.tsx`) |
| MOR-13 | Phone width 375px                                          | Responsive    | P2       | No (e2e-only)                   |
| MOR-14 | Wide screen /more still works                              | Responsive    | P3       | No                              |
| MOR-15 | Arabic / RTL                                               | RTL           | P2       | Yes (`AppShell.cases.test.tsx`) |
| MOR-16 | Light and dark theme                                       | Theme         | P3       | No                              |
| MOR-17 | Headings, list semantics and keyboard                      | Accessibility | P2       | Yes (`AppShell.cases.test.tsx`) |

## Cases

### MOR-01 — Normal user sees Settings, Transcription and account links

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/api/auth/me` → `{ "user": { "id": "u2", "username": "viewer", "is_admin": false } }`.
- **Steps:**
  1. Open `/more`.
- **Expected:** `h1` "More". One list with links in this order: Transcription `/transcription`, Settings `/settings`, Account `/account`, Devices `/account/devices`, Sessions `/account/sessions`, API tokens `/account/tokens`, Billing & license `/billing`, Cloud status `/cloud/status`. (Order follows `NAV` then `SECONDARY` — Transcription comes before Settings.) A "Sign out" button below.

### MOR-02 — Normal user sees no Administration group

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** as MOR-01.
- **Steps:**
  1. Open `/more`.
- **Expected:** No `h2` "Administration"; no link whose `href` starts with `/admin/`; exactly one `list`.

### MOR-03 — Admin sees the Administration group with every admin link

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/me` → `{ "user": { "id": "u1", "username": "root", "is_admin": true } }`.
- **Steps:**
  1. Open `/more`.
- **Expected:** After the MOR-01 list, `h2` "Administration" and a second list: Users `/admin/users`, Library access `/admin/library-acl`, Channels `/admin/channels`, Filler pools `/admin/filler`, Live TV access `/admin/live-tv`, Analytics `/admin/analytics`, Audit log `/admin/audit`, System health `/admin/health`, Machines `/admin/machines`, Logs `/admin/logs`, All devices `/admin/devices`, Extensions `/admin/extensions` (12). `/admin/enrichment` is not listed — on a phone there is no way to reach it except typing the URL (likely gap).

### MOR-04 — User object without `is_admin` shows no admin group

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/me` → `{ "user": { "id": "u3", "username": "x" } }` (no `is_admin`); then `"is_admin": null`. (MoreMenu reads no list fields from an API; the only fetched shape is the user.)
- **Steps:**
  1. Open `/more`.
- **Expected:** Renders like MOR-01; no admin group; no crash.

### MOR-05 — "More" tab opens /more and is marked active

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** full shell at `/library`.
- **Steps:**
  1. Click the "More" tab in the tab bar (second `navigation` named "Primary").
- **Expected:** Location `/more`; the More tab has `aria-current="page"`, Library tab does not. No sidebar link is active (there is no `/more` sidebar link).

### MOR-06 — Each link opens its page; Back returns to /more

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in as admin; target pages' APIs mocked to empty.
- **Steps:**
  1. From `/more`, click "Settings"; press Back. 2. Click "Users"; press Back. 3. Click "Sessions"; press Back.
- **Expected:** Each page renders at its path; Back returns to `/more` with the list visible. (Page scrolls to top on PUSH, not on POP.)

### MOR-07 — Sign out goes to /login and Back does not return

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in; `POST /api/auth/logout` → `204`; `purgeApiCacheOnLogout` mocked.
- **Steps:**
  1. Click "Sign out". 2. Press browser Back.
- **Expected:** POST `/api/auth/logout` sent; cache purge called; location `/login` (replace). Back does not show `/more` with the old user; shell routes redirect to `/login`.

### MOR-08 — Sign out still leaves when the server call fails

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** logout → `500`; then `fetch` rejects.
- **Steps:**
  1. Click "Sign out".
- **Expected:** Ends on `/login` with the local user cleared; `console.warn("auth: logout call failed", ...)`; no unhandled rejection.

### MOR-09 — Double-clicking Sign out sends one request

- **Priority:** P3
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** logout held pending.
- **Steps:**
  1. Click "Sign out" twice quickly.
- **Expected:** One `POST /api/auth/logout`; button disabled or busy while pending. (Currently the button has no pending state, so two POSTs are sent — minor, likely bug.)

### MOR-10 — Signed-out deep link to /more, then back after sign-in

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/me` → 401; login → user.
- **Steps:**
  1. Open `/more`. 2. Sign in.
- **Expected:** 1: redirected to `/login` with `state.from = "/more"`; no menu shown. 2: lands on `/more`.

### MOR-11 — Auth still loading on /more

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `/me` pending.
- **Steps:**
  1. Open `/more`; then resolve as admin.
- **Expected:** `role="status"` "Loading…" first (from `RequireAuth`); then the full menu including the admin group — the admin group never flashes in late after a non-admin render.

### MOR-12 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/me` → `{ "user": { "id": "u1", "username": "root", "is_admin": true } }`; mock `../lib/i18n` so `t("nav.admin")` throws (only MoreMenu and the sidebar admin group call it — or wrap `MoreMenu` route element in a test component that throws).
- **Steps:**
  1. Open `/library`, click the "More" tab.
  2. Press browser Back. 3. Crash again, then tap the "Library" tab.
- **Expected:** 1: route error fallback in the main area, tab bar still usable. 2 and 3: `/library` renders without a refresh. (Route boundary is being added to `AppShell.tsx`; none today.)

### MOR-13 — Phone width 375px

- **Priority:** P2
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e viewport 375x812, admin (longest list).
- **Steps:**
  1. Open `/more`; scroll to the bottom.
- **Expected:** No horizontal scroll (`scrollWidth <= 375`). Every link and "Sign out" can be scrolled into view above the fixed tab bar (not covered by it). Tap targets at least 44px tall.

### MOR-14 — Wide screen /more still works

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 1280x800.
- **Steps:**
  1. Open `/more` directly.
- **Expected:** Menu renders in the main area next to the sidebar; links work. No layout break.

### MOR-15 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`, admin.
- **Steps:**
  1. Open `/more`.
- **Expected:** `dir="rtl"`. `h1` "المزيد"; links "التفريغ النصي", "الإعدادات", "الحساب", "الأجهزة", "الجلسات", "رموز الـ API", "الفوترة والترخيص", "حالة السحابة"; `h2` "الإدارة"; button "تسجيل الخروج". Icons sit on the right of each label.

### MOR-16 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light / dark.
- **Steps:**
  1. Open `/more`, hover and focus a link.
- **Expected:** Text, dividers, hover/focus and active states readable in both; colours from tokens.

### MOR-17 — Headings, list semantics and keyboard

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** admin.
- **Steps:**
  1. Tab through the page.
- **Expected:** One `h1` "More", `h2` "Administration"; links inside `list`/`listitem`; icon spans `aria-hidden`, so link names equal labels (e.g. `getByRole("link", { name: "Sessions" })`). Tab order follows visual order and ends on "Sign out"; Enter/Space activate it; focus ring visible.
