# App shell (signed-in layout and global parts)

- **Route:** every signed-in route (layout route in `src/App.tsx`); index `/` → `<Navigate to="/library" replace />`; unknown `*` → `NotFound` (inside the shell, so signed-out users go to `/login` first)
- **Component:** `src/components/AppShell.tsx` (`AppShell`); children: `src/components/ThemeToggle.tsx`, `src/components/LangToggle.tsx`, `src/components/NotificationsBell.tsx`, `src/pages/NotFound.tsx`. Global wrappers in `src/App.tsx`: `RequireAuth` / `AuthProvider` (`src/lib/auth.tsx`), `NodeGuard` (`src/components/NodeGuard.tsx`) + `src/pages/Paused.tsx`, `DesktopBridge` (`src/components/DesktopBridge.tsx`) + `src/components/DropOverlay.tsx`, `ShortcutProvider` (`src/lib/keyboard/shortcuts.tsx`), `ThemeSync` (`src/lib/theme.ts`), `I18nProvider` (`src/lib/i18n.tsx`), `ToastProvider`. A route error boundary is being added in `AppShell.tsx` by another session.
- **API calls:** `GET /api/auth/me` → `{ user: { id, username, is_admin } }` (401 = signed out); `POST /api/auth/logout`; `GET /api/notifications?limit=30` → `{ items: AppNotification[], unread: number }`, `POST /api/notifications/{id}/read` → `{ unread }`, `POST /api/notifications/read-all` → `{ unread }` (`src/lib/notificationsApi.ts`; polled every 30 s while visible); `GET /api/cluster/self` → `ClusterSelf` (`clusterApi.getSelf()` in NodeGuard at start and in Paused every 15 s); any API `503 { code: "paused" | "home_unreachable" }` fires `maktaba:node-unavailable`. Window events: `mkt:sw-update` (update toast). Tauri events via `src/lib/desktop.ts`: `menu`, `drop-files`.
- **Access:** signed-in user (`RequireAuth`); admin nav group only when `user.is_admin` (pages re-check with `AdminGate` + server 403).
- **Existing tests:** `src/components/AppShell.test.tsx` — SW-update toast + Reload posts `SKIP_WAITING`; admin sees the Extensions link; Transcription in sidebar, tab bar has 6 links ending in `/more`. `src/components/NotificationsBell.test.tsx` — unread count, list localised, open marks read + follows link, mark all read, Escape closes + empty text, Arabic, `notificationVars`. `src/components/NodeGuard.test.tsx` — app on home, paused page on 503 paused, home_unreachable from event, other 503s ignored. `src/pages/Paused.test.tsx` — reason + home link, unreachable text, reload on version change / condition clear, waits while restarting, Arabic. `src/components/DesktopBridge.test.tsx` — subscribe/dispose, navigate action, scan and server-picker events. `src/components/DropOverlay.test.tsx` — inactive, library name, generic label, status role, rejected count, no stray 0. `src/lib/theme.test.ts`, `src/lib/i18n.test.tsx` — theme/locale libs. Gaps: index redirect, NotFound, RequireAuth states, 401 mid-use, admin vs user nav lists, sign out, toggles in the header, keyboard shortcuts (no tests at all), notification null fields, drop overlay stuck, error boundary, responsive, RTL shell.

## Summary

| ID     | Title                                                                       | Category      | Priority | Covered by existing test?                                                                          |
| ------ | --------------------------------------------------------------------------- | ------------- | -------- | -------------------------------------------------------------------------------------------------- |
| SHL-01 | `/` redirects to `/library`                                                 | Navigation    | P1       | Yes (`App.cases.test.tsx`)                                                                         |
| SHL-02 | Unknown route shows NotFound inside the shell                               | Navigation    | P2       | Yes (`AppShell.cases.test.tsx`; it.fails: bug, Arabic text)                                        |
| SHL-03 | Unknown route while signed out goes to /login                               | Permissions   | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-04 | Signed out on any shell route redirects to /login with `from`               | Permissions   | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-05 | Auth loading shows a status, not the app or the login form                  | Loading       | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-06 | `/api/auth/me` 500 / network / malformed treated as signed out              | Error         | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-07 | Session expires mid-use (401) sends the user to /login                      | Permissions   | P1       | No (it.fails: bug, `AppShell.cases.test.tsx`)                                                      |
| SHL-08 | Normal user sees no admin links                                             | Permissions   | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-09 | Admin sees the Administration group with every admin link                   | Permissions   | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-10 | Non-admin deep link to an admin page shows "Admins only"                    | Permissions   | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-11 | Active nav link marks only the current page                                 | Navigation    | P3       | No                                                                                                 |
| SHL-12 | Header search submits to /search                                            | Happy path    | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-13 | Sign out clears the session and blocks Back                                 | Happy path    | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-14 | Sign out still leaves when the server call fails                            | Error         | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-15 | ThemeToggle cycles light → dark → system                                    | Theme         | P2       | Yes (`AppShell.cases.test.tsx`; OS-change step in `theme.test.ts`)                                 |
| SHL-16 | LangToggle switches to Arabic and mirrors the shell                         | RTL           | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-17 | Notifications: `items: null` / missing / `unread: null`                     | Empty state   | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-18 | Notifications: load error and read-call responses missing fields            | Error         | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-19 | Notifications panel keyboard and focus                                      | Accessibility | P3       | Partial (`NotificationsBell.test.tsx`: Escape closes)                                              |
| SHL-20 | Paused node replaces the whole app                                          | Error         | P1       | Yes (`NodeGuard.test.tsx`, `Paused.test.tsx`)                                                      |
| SHL-21 | Paused page with `home: null` / unsafe `web_url`                            | Empty state   | P2       | Yes (`Paused.test.tsx`)                                                                            |
| SHL-22 | Desktop drop overlay closes after a clean drop                              | Error         | P2       | No (it.fails: bug, `AppShell.cases.test.tsx`; rejected-file path passes)                           |
| SHL-23 | Desktop menu actions and drop import event                                  | Happy path    | P3       | Partial (`DesktopBridge.test.tsx`: navigate/scan/picker; not library slot, external, import-files) |
| SHL-24 | Keyboard shortcuts `g l` / `g s` / `g q` / `g c` and `/`                    | Accessibility | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-25 | Shortcut help `?` opens, Escape closes, ignored while typing                | Accessibility | P2       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-26 | Update toast with Reload                                                    | Happy path    | P3       | Yes (`AppShell.test.tsx`)                                                                          |
| SHL-27 | New page scrolls to top; Back keeps the scroll                              | Navigation    | P3       | No                                                                                                 |
| SHL-28 | Page crash shows a fallback inside the shell                                | Error         | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-29 | After a crash, Back and nav links recover without a refresh                 | Error         | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-30 | Fallback resets when the route changes (and on the same route via a new id) | Error         | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-31 | Crash in a header part (NotificationsBell) does not blank the app           | Error         | P1       | Yes (`AppShell.cases.test.tsx`)                                                                    |
| SHL-32 | Phone width 375px: tab bar, no sidebar, no horizontal scroll                | Responsive    | P2       | No (e2e-only)                                                                                      |
| SHL-33 | Light and dark theme across the shell                                       | Theme         | P3       | No                                                                                                 |
| SHL-34 | Landmarks, skip link and labels                                             | Accessibility | P2       | Yes (`AppShell.cases.test.tsx`; skip-link focus move on Enter: e2e-only)                           |

## Cases

### SHL-01 — `/` redirects to `/library`

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`App.cases.test.tsx`)
- **Preconditions / data:** full `App` routes (or MemoryRouter with the same route tree); `/api/auth/me` → `{ "user": { "id": "u1", "username": "amal", "is_admin": false } }`; library APIs mocked to empty.
- **Steps:**
  1. Open `/`.
  2. Press browser Back.
- **Expected:** Location `/library`; the library page renders inside the shell. Back does not land on `/` again (redirect used `replace`).

### SHL-02 — Unknown route shows NotFound inside the shell

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`; it.fails: bug, Arabic text)
- **Preconditions / data:** signed in.
- **Steps:**
  1. Open `/does-not-exist/42`.
  2. Click "Back to library".
- **Expected:** Heading "404", text "This route doesn't exist.", link "Back to library" (`href="/library"`). Header and nav still visible. Click goes to `/library`. In Arabic the NotFound text should be translated (currently hardcoded English — likely bug).

### SHL-03 — Unknown route while signed out goes to /login

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/api/auth/me` → 401.
- **Steps:**
  1. Open `/does-not-exist`.
- **Expected:** Location `/login` (the `*` route sits inside `RequireAuth`), state `{ from: "/does-not-exist" }`. After sign-in the user sees the 404 page.

### SHL-04 — Signed out on any shell route redirects to /login with `from`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/api/auth/me` → 401. Routes: `/library`, `/videos/42`, `/admin/users`, `/more`, `/search?q=x`.
- **Steps:**
  1. Open each.
- **Expected:** Each redirects (replace) to `/login` with `state.from` = the path. No shell content, no page API calls (assert e.g. `/api/notifications` never requested). For `/search?q=x`, `from` should keep `?q=x` (currently only `pathname` is kept — likely bug; see LGN-03).

### SHL-05 — Auth loading shows a status, not the app or the login form

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/api/auth/me` held pending.
- **Steps:**
  1. Open `/library`. 2. Resolve with a user.
- **Expected:** Step 1: `role="status"` "Loading…"; no redirect to `/login`; no nav. Step 2: shell renders at `/library`. In Arabic the loading text should be "جارٍ التحميل…" (currently hardcoded English "Loading…" in `RequireAuth` — likely bug).

### SHL-06 — `/api/auth/me` 500 / network / malformed treated as signed out

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** three runs: `/me` → `500`; `fetch` rejects; `/me` → `200 {}` (no `user`).
- **Steps:**
  1. Open `/library`.
- **Expected:** Each ends on `/login`, no blank page, no thrown error. 500 and network log `auth: /me probe failed` via `console.warn`; 401 does not warn.

### SHL-07 — Session expires mid-use (401) sends the user to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No (it.fails: bug, `AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in at `/library`; then the next library or notifications call returns `401` problem+json.
- **Steps:**
  1. Trigger a refetch (navigate to `/series`, or advance the 30 s notifications poll).
- **Expected:** User is sent to `/login` with `from` set to the current page, local user cleared; after signing in they return. (Currently nothing reacts to a 401 after start: the shell stays with the old username and pages show their own error states — currently unhandled — likely bug.)

### SHL-08 — Normal user sees no admin links

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/me` → `{ "user": { "id": "u2", "username": "viewer", "is_admin": false } }`.
- **Steps:**
  1. Open `/library`; read the sidebar (first `navigation` named "Primary").
- **Expected:** Sidebar links, in order: Library `/library`, Series `/series`, TV Guide `/guide`, Search `/search`, Processing `/queue`, Transcription `/transcription`, Settings `/settings`, Account `/account`, Devices `/account/devices`, Sessions `/account/sessions`, API tokens `/account/tokens`, Billing & license `/billing`, Cloud status `/cloud/status`. No "Administration" text and no link starting with `/admin/`. Same when `is_admin` is missing from the user object.

### SHL-09 — Admin sees the Administration group with every admin link

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `/me` → `{ "user": { "id": "u1", "username": "root", "is_admin": true } }`.
- **Steps:**
  1. Open `/library`.
- **Expected:** After the SHL-08 links, group label "Administration" then: Users `/admin/users`, Library access `/admin/library-acl`, Channels `/admin/channels`, Filler pools `/admin/filler`, Live TV access `/admin/live-tv`, Analytics `/admin/analytics`, Audit log `/admin/audit`, System health `/admin/health`, Machines `/admin/machines`, Logs `/admin/logs`, All devices `/admin/devices`, Extensions `/admin/extensions`. Note `/admin/enrichment` is routed but not linked anywhere in the nav (likely gap — confirm whether it should be listed).

### SHL-10 — Non-admin deep link to an admin page shows "Admins only"

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** non-admin; admin APIs → `403`.
- **Steps:**
  1. Open `/admin/users`, `/admin/live-tv`, `/admin/logs` directly.
- **Expected:** Shell renders; page shows the `common.forbidden` ErrorState "Admins only" / "You need administrator access to view this page." (for pages wrapped in `AdminGate`). Never a blank page. The nav still has no admin links.

### SHL-11 — Active nav link marks only the current page

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** signed in.
- **Steps:**
  1. Open `/account/devices`. 2. Open `/library/abc`.
- **Expected:** Step 1: only "Devices" has `aria-current="page"`. (Currently `NavLink` to `/account` has no `end`, so "Account" is marked active too — likely bug.) Step 2: "Library" is active in both sidebar and tab bar.

### SHL-12 — Header search submits to /search

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in at `/library`.
- **Steps:**
  1. Type ` سورة البقرة & co` in the search box (`role="searchbox"`, name "Search") and press Enter.
  2. Clear the box and press Enter.
- **Expected:** Step 1: location `/search?q=` + `encodeURIComponent("سورة البقرة & co")` (trimmed). Step 2: `/search` with no query. Placeholder "Search transcripts…".

### SHL-13 — Sign out clears the session and blocks Back

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in as `amal` at `/library`; `POST /api/auth/logout` → `204`; mock `purgeApiCacheOnLogout` in `src/lib/sw.ts`.
- **Steps:**
  1. Click the header button showing `amal` (accessible name "Sign out").
  2. Press browser Back.
- **Expected:** POST `/api/auth/logout` sent; `purgeApiCacheOnLogout` called; location `/login` (replace). Back does not show the old page content; any shell route redirects to `/login`. Note: the button's visible text (username) does not match its accessible name "Sign out" (WCAG label-in-name — P3 polish).

### SHL-14 — Sign out still leaves when the server call fails

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `POST /api/auth/logout` → `500`, then a run where `fetch` rejects.
- **Steps:**
  1. Click sign out.
- **Expected:** Still ends on `/login` with local user cleared; `console.warn("auth: logout call failed", ...)`; no unhandled rejection.

### SHL-15 — ThemeToggle cycles light → dark → system

- **Priority:** P2
- **Category:** Theme
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`; OS-change step in `theme.test.ts`)
- **Preconditions / data:** `localStorage["mkt:theme"] = "light"`; `matchMedia("(prefers-color-scheme: light)")` stubbed `matches: false`.
- **Steps:**
  1. Click the theme button three times, checking after each.
  2. While in system mode, fire the media-query `change` with `matches: true`.
- **Expected:** Before: glyph "☀", accessible name "Theme: Dark". Click 1: `<html data-theme="dark">`, storage `dark`, name "Theme: System". Click 2: storage `system`, `data-theme="dark"` (OS not light), name "Theme: Light". Click 3: `light`. Step 2: `data-theme` flips to `light` without a reload.

### SHL-16 — LangToggle switches to Arabic and mirrors the shell

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in, English.
- **Steps:**
  1. Click "AR" (accessible name "Language: العربية").
  2. Reload (re-render with same storage).
- **Expected:** `<html dir="rtl" lang="ar">`; button now "EN" (name "اللغة: English"); nav labels "المكتبة", "المسلسلات", "دليل القنوات", "البحث", "قائمة المعالجة"; sidebar on the right (`[dir="rtl"] .mkt-shell__nav`). `localStorage["mkt:locale"] = "ar"` and the choice survives the reload.

### SHL-17 — Notifications: `items: null` / missing / `unread: null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** `GET /api/notifications?limit=30` returns in turn: `{ "items": null, "unread": 0 }`; `{ "unread": 0 }` (no `items`); `{ "items": [], "unread": null }`; `{}`; and one item with `"params": null` and one with `params` missing (`{ "id": "n1", "kind": "x", "severity": "info", "title_key": "notify.tv.slot.failover", "link": "", "audience": "all", "created_at": "2026-10-01T10:00:00Z", "read": false }`).
- **Steps:**
  1. Render the shell; open the bell.
- **Expected:** Fields checked: `items`, `items[].params`. Null/missing `items` → panel shows "Nothing new."; bell label "Notifications", count hidden. Null `unread` → treated as 0. Item with null/missing `params` renders its title ("Live TV failed over to the hot standby"); no crash, shell stays.

### SHL-18 — Notifications: load error and read-call responses missing fields

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** (a) list → `500` on first load; (b) list OK with one unread item, `POST /api/notifications/{id}/read` → `200 {}`; (c) `POST /api/notifications/read-all` → `500`; (d) list → `401`.
- **Steps:**
  1. Open the bell in each case; click the item / "Mark all read".
- **Expected:** (a) "Could not load the notifications."; (b) count shows 0 or stays numeric — never "NaN" (currently `setUnread(r.unread)` sets `undefined` and the badge renders `n(undefined)` — likely bug); (c) items stay marked read locally, corrected on next poll, no alert spam; (d) see SHL-07.

### SHL-19 — Notifications panel keyboard and focus

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** Partial — `NotificationsBell.test.tsx` "closes on Escape".
- **Preconditions / data:** list with 2 items, 1 unread.
- **Steps:**
  1. Tab to the bell; press Enter; Tab into the list; press Escape. 2. Click outside the panel.
- **Expected:** Bell name "Notifications, 1 unread"; `aria-expanded` toggles; panel `role="dialog"` named "Notifications"; Escape closes and returns focus to the bell; outside click closes. Unread items include visually hidden "unread". Polling stops when `document.visibilityState` is `hidden` and resumes (with an immediate load) when visible.

### SHL-20 — Paused node replaces the whole app

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes — `NodeGuard.test.tsx` "shows the paused page when an API call answers 503 paused", "swaps the app for the paused page when the node cannot serve even reads", "keeps the app readable under a notice when the home is unreachable", "ignores other 503s"; `Paused.test.tsx`; `Login.outage.test.tsx`.
- **Preconditions / data:** any API → `503 { "code": "paused", "reason": "schema 134 > 130" }`; or `/api/cluster/self` → `{ "role": "node", "paused": "schema ...", ... }`.
- **Steps:**
  1. Render the full App signed in.
- **Expected:** "This machine is paused" page replaces shell and login (NodeGuard wraps AuthProvider). "Try again" button present. Other 503s leave the app alone. A `503 { "code": "home_unreachable" }`, or a read marked `X-Maktaba-Home: unreachable`, keeps the app under the "This machine can't reach its home." notice (every request is still sent: the node decides; a refused sign-out keeps the user signed in and says so; /login says signing in needs the home and probes the session again once it answers); `home_unreachable` with `"readable": false` shows the "This machine cannot reach its home" page instead.

### SHL-21 — Paused page with `home: null` / unsafe `web_url`

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Paused.test.tsx`)
- **Preconditions / data:** `/api/cluster/self` → node with `"home": null`; then `"home": { "web_url": "javascript:alert(1)", "reachable": false }`; then no `name`.
- **Steps:**
  1. Render `Paused code="home_unreachable"`.
- **Expected:** No crash. `home: null` → "The home's address is not known yet." and no reload loop. Unsafe URL → no link rendered (`safeHttpUrl`). No `name` → no "This is …" line.

### SHL-22 — Desktop drop overlay closes after a clean drop

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No (it.fails: bug, `AppShell.cases.test.tsx`; rejected-file path passes)
- **Preconditions / data:** mock `registerDropHandler` in `src/lib/desktop.ts` to capture the callback; fire it with `{ accepted: ["/v/a.mp4"], rejected: [] }`.
- **Steps:**
  1. Render `DesktopBridge` inside the app; fire the drop.
- **Expected:** `maktaba:import-files` dispatched with `{ paths: ["/v/a.mp4"] }`; the full-screen overlay then goes away (timeout or import result) and the app is usable. (Currently `drop` is set and only cleared by "Dismiss", which renders only when `rejectedCount > 0`; the `position: fixed; inset: 0` overlay stays over the app forever — currently unhandled — likely bug.) With `rejected: ["/x.txt"]`: "1 file(s) skipped (unsupported)" and "Dismiss" clears it. Overlay text is hardcoded English — should be translated in Arabic (likely bug).

### SHL-23 — Desktop menu actions and drop import event

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Partial — `DesktopBridge.test.tsx` covers navigate, scan, server picker.
- **Preconditions / data:** mock `registerMenuRouter` to capture `apply`.
- **Steps:**
  1. Fire `{ kind: "navigate-library", slot: 3 }`, `{ kind: "new-window", private: true }`, `{ kind: "external", url: "https://example.com" }`.
- **Expected:** Location `/library?slot=3`; `maktaba:new-window` with `{ private: true }`; `window.open("https://example.com", "_blank", "noopener,noreferrer")`. In a plain browser (not desktop) nothing subscribes and nothing renders.

### SHL-24 — Keyboard shortcuts `g l` / `g s` / `g q` / `g c` and `/`

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in at `/series`; focus on `body`; fake timers.
- **Steps:**
  1. Press `g` then `l`; `g` then `s`; `g` then `q`; `g` then `c`.
  2. Press `g`, wait 1300 ms, press `l`.
  3. Press `/`.
  4. Focus a page input, type `g` then `l`.
  5. Press `g` `l` with `isComposing: true`, and `ctrl+g`.
- **Expected:** 1: `/library`, `/search`, `/queue`, `/settings`. 2: no navigation (leader expired). 3: header search box gets focus. 4: letters typed into the input, no navigation. 5: nothing happens.

### SHL-25 — Shortcut help `?` opens, Escape closes, ignored while typing

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in.
- **Steps:**
  1. Press `?`. 2. Press Tab several times. 3. Press Escape. 4. Focus the search box, type `?`.
- **Expected:** 1: dialog "Keyboard shortcuts" with rows `g l` "Go to Library", `g s` "Go to Search", `g q` "Go to Processing", `g c` "Go to Settings", `/` "Focus search", `?` "Show this help", `Esc` "Close dialogs". 2: focus stays inside the dialog. 3: dialog closes, focus returns to where it was. 4: `?` is typed into the box; no dialog. In Arabic the rows are "الذهاب إلى المكتبة" etc.

### SHL-26 — Update toast with Reload

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** Yes — `AppShell.test.tsx` "shows the real update copy + a Reload button", "clicking Reload posts SKIP_WAITING"
- **Preconditions / data:** dispatch `mkt:sw-update` with `{ registration: { waiting: { postMessage } } }`.
- **Steps:**
  1. Click "Reload".
- **Expected:** Toast "A new version is available."; click posts `{ type: "SKIP_WAITING" }`. Without a registration, Reload calls `window.location.reload()` (not yet tested).

### SHL-27 — New page scrolls to top; Back keeps the scroll

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** spy `window.scrollTo`.
- **Steps:**
  1. Click a nav link (PUSH). 2. Press Back (POP).
- **Expected:** 1: `scrollTo(0, 0)` called and `#mkt-main` scrolled to top. 2: not called.

### SHL-28 — Page crash shows a fallback inside the shell

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** signed in; mock `../pages/Search` so `Search` throws `new Error("boom")` during render (any page works; the per-page files list real API shapes that throw).
- **Steps:**
  1. Open `/library`, then click "Search" in the nav.
- **Expected:** An error fallback renders in `#mkt-main` (not a blank screen); header, sidebar and tab bar remain and work. Error logged to the console once.

### SHL-29 — After a crash, Back and nav links recover without a refresh

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** as SHL-28.
- **Steps:**
  1. On the crashed `/search`, press browser Back.
  2. Crash again, then click "Series" in the sidebar.
  3. Crash again, then press `g l`.
- **Expected:** 1: `/library` renders normally. 2: `/series` renders. 3: `/library` renders. No full page reload in any step (assert `window.location.reload` not called; React tree not remounted from root).

### SHL-30 — Fallback resets when the route changes (and on the same route via a new id)

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** mock `VideoDetail` to throw only when `videoId === "bad"`.
- **Steps:**
  1. Open `/videos/bad` (fallback). 2. Navigate to `/videos/good` (same route pattern, new param). 3. Press Back to `/videos/bad`, then Forward.
- **Expected:** 2: the good video page renders (boundary keyed on location, not only on route pattern). 3: fallback on `bad`, good page on Forward.

### SHL-31 — Crash in a header part (NotificationsBell) does not blank the app

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`)
- **Preconditions / data:** mock `./NotificationsBell` to throw during render.
- **Steps:**
  1. Open `/library`.
- **Expected:** The page content and nav still render (bell missing or a small fallback). (A boundary only around `<Outlet />` will not catch this; there is no app-level boundary today — currently unhandled — likely bug.)

### SHL-32 — Phone width 375px: tab bar, no sidebar, no horizontal scroll

- **Priority:** P2
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e viewport 375x812, signed in as `a_very_long_username_here`, 120 unread notifications.
- **Steps:**
  1. Open `/library`; scroll to the bottom of the page.
- **Expected:** Sidebar hidden (`@media (max-width: 640px)`), tab bar visible with Library, Series, TV Guide, Search, Processing, More. `document.documentElement.scrollWidth <= 375`. Header fits (search, bell "99+", theme, lang, username truncated). Page content not hidden behind the tab bar. Same in Arabic (tab order mirrored).

### SHL-33 — Light and dark theme across the shell

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light / dark; bell panel open; help dialog open.
- **Steps:**
  1. View header, sidebar, active link, tab bar, bell panel, help dialog, update toast.
- **Expected:** All readable in both; active link distinguishable; colours from tokens (no hardcoded hex in shell CSS).

### SHL-34 — Landmarks, skip link and labels

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`AppShell.cases.test.tsx`; skip-link focus move on Enter: e2e-only)
- **Preconditions / data:** signed in.
- **Steps:**
  1. Press Tab once from page load; press Enter.
  2. Query landmarks.
- **Expected:** First Tab focuses "Skip to main content"; Enter moves focus to `#mkt-main` (`tabIndex=-1`). One `banner`, one `main`, one `search`, two `navigation` landmarks (both named "Primary" — give them distinct names, e.g. sidebar vs tab bar; P3 polish). Icon spans are `aria-hidden`; each sidebar link's name equals its label. Focus ring visible on links and header buttons.
