# Admin: User management

- **Route:** `/admin/users` (no params, no query string)
- **Component:** `src/pages/Admin/Users.tsx` (`AdminUsers` → `AdminGate` → `UsersInner`); children in the same file: `UserFormModal` (create/edit), `DeleteModal`, `RevokeModal`. Uses `@ds` `Modal`, `Input`, `Checkbox`, `Badge`, `EmptyState`, `useToast`.
- **API calls:** there is NO list endpoint. All via `api` in `src/lib/api.ts`:
  - `POST /api/users` body `{ username, password, is_admin }` → `{ id, username, is_admin }`
  - `PATCH /api/users/{id}` body with only changed fields `{ username?, password?, is_admin? }` → `{ id, username, is_admin }`
  - `DELETE /api/users/{id}` → 204
  - `POST /api/users/{id}/unlock` → 204
  - `DELETE /api/users/{id}/sessions/{session_id}` → 204
  - `DELETE /api/users/{id}/refresh-tokens/{family_id}` → 204
  - Server errors are RFC 9457 problems; relevant ones from `api/internal/handlers/auth/usersadmin.go`: 409 `{type:".../problems/last-admin", title:"conflict", detail:"operation would leave the system with no admin"}`, 409 username exists (`detail:"username already exists"`), 404 user not found.
  - Roster: the table is read from / written to `localStorage["mkt:admin:knownUsers"]` (JSON array of `{id, username, is_admin}`).
- **Access:** admin only. `RequireAuth` (redirect to `/login`) then `AdminGate` (shows `common.forbidden.title` "Admins only" / `common.forbidden.desc`). Server re-checks and returns 403.
- **Existing tests:** none (`Users.test.tsx` does not exist). Gaps: everything below.

## Summary

| ID     | Title                                                        | Category      | Priority | Covered by existing test? |
| ------ | ------------------------------------------------------------ | ------------- | -------- | ------------------------- |
| USR-01 | Empty roster shows empty state                               | Empty state   | P2       | No                        |
| USR-02 | Roster loads from localStorage                               | Happy path    | P1       | No                        |
| USR-03 | Create user (happy path) adds row and persists               | Happy path    | P1       | No                        |
| USR-04 | Create form validation (username/password required)          | Validation    | P2       | No                        |
| USR-05 | Create fails with 409 username exists                        | Error         | P2       | No                        |
| USR-06 | Create/edit fails with network error or non-JSON 500         | Error         | P2       | No                        |
| USR-07 | Edit sends only changed fields                               | Happy path    | P1       | No                        |
| USR-08 | Promote user to admin / demote to user                       | Happy path    | P1       | No                        |
| USR-09 | Last-admin protection on demote (409)                        | Permissions   | P1       | No                        |
| USR-10 | Last-admin protection on delete (409)                        | Permissions   | P1       | No                        |
| USR-11 | Admin demotes themself                                       | Permissions   | P1       | No                        |
| USR-12 | Admin deletes themself                                       | Permissions   | P1       | No                        |
| USR-13 | Delete user (confirm, cancel, error)                         | Happy path    | P1       | No                        |
| USR-14 | Unlock account (success, 404)                                | Happy path    | P2       | No                        |
| USR-15 | Revoke session / token family                                | Happy path    | P2       | No                        |
| USR-16 | Session expired (401) during a mutation                      | Error         | P2       | No                        |
| USR-17 | Corrupt or `null` roster in localStorage does not blank page | Empty state   | P1       | No                        |
| USR-18 | Create response missing / malformed                          | Error         | P2       | No                        |
| USR-19 | Page crash shows route error fallback; Back works            | Navigation    | P1       | No                        |
| USR-20 | Non-admin sees "Admins only"                                 | Permissions   | P1       | No                        |
| USR-21 | Signed-out user redirected to /login                         | Permissions   | P1       | No                        |
| USR-22 | Deep link and Back/Forward                                   | Navigation    | P2       | No                        |
| USR-23 | localStorage unavailable                                     | Error         | P3       | No                        |
| USR-24 | Phone width (375px)                                          | Responsive    | P3       | No                        |
| USR-25 | Arabic / RTL                                                 | RTL           | P3       | No                        |
| USR-26 | Light and dark theme                                         | Theme         | P3       | No                        |
| USR-27 | Modals: focus trap, Escape, labels, toasts announced         | Accessibility | P2       | No                        |

## Cases

### USR-01 — Empty roster shows empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** signed in as admin; `localStorage["mkt:admin:knownUsers"]` absent. Also run with value `"[]"`.
- **Steps:**
  1. Open `/admin/users`.
- **Expected:** h1 "User management"; the note `admin.users.note` ("The server has no user-listing endpoint..."); EmptyState title "No tracked users", description "Create a user to start managing accounts here."; no table; "Add user" button visible. No network calls.

### USR-02 — Roster loads from localStorage

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:admin:knownUsers"] = '[{"id":"u1","username":"alice","is_admin":true},{"id":"u2","username":"bob","is_admin":false}]'`
- **Steps:**
  1. Open `/admin/users`.
- **Expected:** table with accessible name "User management", headers Username / Role / ID / Actions. Row alice shows Badge "Administrator"; row bob shows "User"; ID column shows `u1`/`u2`. Each row has buttons Edit, Unlock, Revoke access, Delete.

### USR-03 — Create user (happy path) adds row and persists

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** empty roster; `POST /api/users` → 201 `{ "id": "u9", "username": "carol", "is_admin": false }`
- **Steps:**
  1. Click "Add user". Dialog "Create user" opens.
  2. Type "carol" in "Username", "s3cret-pass" in "Password". Leave "Administrator" unchecked.
  3. Click "Save" (also try pressing Enter inside the form).
- **Expected:** `POST /api/users` with body `{ "username": "carol", "password": "s3cret-pass", "is_admin": false }`. Save shows loading while pending. Success toast "User created.". Dialog closes. Table shows row carol / User / u9. `localStorage["mkt:admin:knownUsers"]` contains the user. Reload: row still shown. Enter submits exactly once.

### USR-04 — Create form validation (username/password required)

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** create dialog open.
- **Steps:**
  1. Leave both empty. 2. Type only username. 3. Type username " " (spaces) and a password. 4. Press Enter with empty fields.
- **Expected:** "Save" is disabled in 1-3; Enter does not send a request. No POST is made. Both inputs are labelled and marked required. In edit mode the password field is labelled "New password" with description "optional" and Save is enabled with an empty password.

### USR-05 — Create fails with 409 username exists

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST /api/users` → 409, `Content-Type: application/problem+json`, `{ "type": "https://maktaba.dev/problems/username-exists", "title": "conflict", "status": 409, "detail": "username already exists" }`
- **Steps:**
  1. Create user "alice".
- **Expected:** error toast "username already exists". Dialog stays open with values kept; Save no longer loading. Roster unchanged.

### USR-06 — Create/edit fails with network error or non-JSON 500

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** (a) `fetch` rejects (`TypeError: Failed to fetch`); (b) `POST /api/users` → 500 `text/plain` "boom"; (c) 500 problem+json `{ "title": "internal", "status": 500 }` with no detail.
- **Steps:**
  1. Submit the create form in each setup. Repeat for edit (PATCH).
- **Expected:** (a) toast "Something went wrong." (`common.error`); (b) toast text "boom" (detail is the body text); (c) toast "internal" (falls back to title). Dialog stays open, no row added, button usable again.

### USR-07 — Edit sends only changed fields

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** roster has `{id:"u2",username:"bob",is_admin:false}`; `PATCH /api/users/u2` → `{ "id":"u2","username":"robert","is_admin":false }`
- **Steps:**
  1. Click Edit on bob. Dialog "Edit user" opens with Username "bob", password empty, Administrator unchecked.
  2. Change username to "robert". Save.
  3. Edit again, type only a new password, Save.
  4. Edit again, change nothing, Save.
- **Expected:** 2 sends `{ "username": "robert" }`; 3 sends `{ "password": "..." }` only; 4 sends `{}` (Expected: Save should be disabled or no request sent when nothing changed — currently sends an empty PATCH, likely minor bug). Toast "User updated."; row updates in place (not duplicated); localStorage updated.

### USR-08 — Promote user to admin / demote to user

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** roster has bob (user) and alice (admin); PATCH returns the updated user.
- **Steps:**
  1. Edit bob, check "Administrator", Save.
  2. Edit alice, uncheck "Administrator", Save (alice is not the last admin).
- **Expected:** body `{ "is_admin": true }` then `{ "is_admin": false }`. Badges flip to "Administrator" / "User". Create with "Administrator" checked sends `is_admin: true`.

### USR-09 — Last-admin protection on demote (409)

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** roster has alice (only admin). `PATCH /api/users/u1` → 409 problem `{ "type": "https://maktaba.dev/problems/last-admin", "title": "conflict", "status": 409, "detail": "operation would leave the system with no admin" }`
- **Steps:**
  1. Edit alice, uncheck "Administrator", Save.
- **Expected:** error toast "operation would leave the system with no admin". Dialog stays open. Roster badge still "Administrator"; localStorage not changed.

### USR-10 — Last-admin protection on delete (409)

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `DELETE /api/users/u1` → same 409 last-admin problem.
- **Steps:**
  1. Click Delete on alice. Dialog "Delete user" with text "Delete user “alice”? This cannot be undone."
  2. Click "Delete".
- **Expected:** error toast with the last-admin detail; dialog stays open, Delete button usable again; row stays in the table and in localStorage.

### USR-11 — Admin demotes themself

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `/api/auth/me` → `{ user: { id: "u1", username: "alice", is_admin: true } }`; roster includes u1 and another admin; `PATCH /api/users/u1` → `{ id:"u1", username:"alice", is_admin:false }`.
- **Steps:**
  1. Edit alice (yourself), uncheck Administrator, Save.
  2. Click any admin nav link or action afterwards.
- **Expected:** request succeeds; row shows "User". Wanted: a warning before saving ("you will lose admin access") and, after save, the auth state refreshes so admin nav disappears and the page shows "Admins only" (currently unhandled — likely bug: `useAuth().user.is_admin` stays true until reload, so the admin UI stays visible and later calls 403).

### USR-12 — Admin deletes themself

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** signed in as u1; another admin exists; `DELETE /api/users/u1` → 204; any next call → 401.
- **Steps:**
  1. Delete yourself (u1) and confirm.
- **Expected:** wanted: a stronger warning naming that this is your own account, then a redirect to `/login` once the session is gone (currently unhandled — likely bug: toast "User deleted." and the page stays as if signed in).

### USR-13 — Delete user (confirm, cancel, error)

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** roster has bob; `DELETE /api/users/u2` → 204.
- **Steps:**
  1. Delete → Cancel. 2. Delete → click the backdrop. 3. Delete → "Delete". 4. Repeat with DELETE → 404 problem `{ "title":"not found","status":404,"detail":"user not found" }`.
- **Expected:** 1: dialog closes, no request. 2: backdrop click does NOT close (dialog is `dismissable={false}`). 3: DELETE sent once, toast "User deleted.", row removed from table and localStorage; if last row, EmptyState shows. 4: error toast with detail; row kept. (Wanted for 404: offer to remove the stale row from the roster — currently the row stays forever.)

### USR-14 — Unlock account (success, 404)

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `POST /api/users/u2/unlock` → 204; then → 404 problem.
- **Steps:**
  1. Click Unlock on bob.
- **Expected:** success toast "Account unlocked."; on 404 an error toast with the problem detail. ID with special chars (e.g. `a/b`) is sent as `/api/users/a%2Fb/unlock`.

### USR-15 — Revoke session / token family

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `DELETE /api/users/u2/sessions/s-1` → 204; `DELETE /api/users/u2/refresh-tokens/f-1` → 204; then each → 404.
- **Steps:**
  1. Click "Revoke access" on bob. Dialog "Revoke sessions & tokens" with description.
  2. With both fields empty or spaces only: check buttons.
  3. Type "s-1" in "Session ID", click "Revoke session".
  4. Type "f-1" in "Refresh-token family ID", click "Revoke token family".
- **Expected:** 2: both buttons disabled. 3/4: correct DELETE path (id URL-encoded), toast "Revoked.". While one request runs both buttons show loading. On 404, error toast with detail; dialog stays open.

### USR-16 — Session expired (401) during a mutation

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** any mutation → 401 problem `{ "title":"unauthorized","status":401 }`.
- **Steps:**
  1. Try create, edit, delete, unlock.
- **Expected:** wanted: redirect to `/login` (with `state.from = "/admin/users"`) or a clear "sign in again" message. Currently only a toast "unauthorized" is shown (currently unhandled — likely gap). No roster change.

### USR-17 — Corrupt or `null` roster in localStorage does not blank page

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** this page has no fetched list; the only list is the localStorage roster. Run each: `"null"`, `"{}"`, `"\"text\""`, `"not json"`, `'[{"username":"x"}]'` (missing `id`/`is_admin`), `'[null]'`.
- **Steps:**
  1. Open `/admin/users`.
- **Expected:** page renders the EmptyState (or the valid rows) and never a blank screen or thrown error. `"not json"` already falls back to `[]`. Currently `"null"`, `"{}"`, `"\"text\""` and `'[null]'` parse without error and then crash on `users.length` / `users.map` / `u.id` (currently unhandled — likely bug). Missing `is_admin` should show "User".

### USR-18 — Create response missing / malformed

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST /api/users` → 204 empty; and → 200 `text/html` body `<html>`.
- **Steps:**
  1. Create a user.
- **Expected:** no crash. Wanted: user is shown as created (or a clear error) — currently `onSaved(undefined)` throws inside `upsert`, which is caught and shows "Something went wrong." after the "User created." toast, and no row is added (likely bug, low risk while the server always returns JSON).

### USR-19 — Page crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** full app router (`App.tsx` routes inside `AppShell`). Start on `/library`, then navigate to `/admin/users` with `localStorage["mkt:admin:knownUsers"] = "null"` (throws on render), or mock `Badge` to throw.
- **Steps:**
  1. Go to `/library`, click sidebar "Users" (`nav.admin.users`).
  2. Observe; then press browser Back; then click another nav link (e.g. "Audit log").
- **Expected:** a route-level error fallback is shown in the content area (shell and nav still visible), not a blank page. Back renders `/library` without a reload; clicking another nav link renders that page.

### USR-20 — Non-admin sees "Admins only"

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `/api/auth/me` → `{ user: { id:"u2", username:"bob", is_admin:false } }`; roster in localStorage has entries.
- **Steps:**
  1. Open `/admin/users` directly.
- **Expected:** ErrorState (kind permission) "Admins only" / "You need administrator access to view this page."; no table, no "Add user" button; no admin nav group in the sidebar. No `/api/users*` request.

### USR-21 — Signed-out user redirected to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `/api/auth/me` → 401.
- **Steps:**
  1. Open `/admin/users`.
- **Expected:** redirect to `/login` with `state.from === "/admin/users"`; after login as admin, user lands back on `/admin/users`.

### USR-22 — Deep link and Back/Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** admin.
- **Steps:**
  1. Open `/admin/users` directly in a new tab. 2. Open the Create dialog, press browser Back. 3. Forward.
- **Expected:** page renders on direct load. Back leaves the page (dialog is not in history) and the dialog does not stay open on top of the previous page; body scroll is not left locked (`body.style.overflow` restored). Forward shows the page with no dialog open.

### USR-23 — localStorage unavailable

- **Priority:** P3
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage.getItem` / `setItem` throw (`SecurityError`).
- **Steps:**
  1. Open page, create a user.
- **Expected:** page renders EmptyState; create works and the row shows for this visit; no error thrown. Row is gone after reload (expected, roster not stored).

### USR-24 — Phone width (375px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812, roster with 3 users incl. a long id (36-char UUID) and long username.
- **Steps:**
  1. Open page; scroll the table; open each dialog.
- **Expected:** no horizontal scroll of the page itself (table scrolls inside itself — `.mkt-table` becomes `display:block; overflow-x:auto` on small screens). All four row buttons reachable. Dialogs fit the screen; nav reachable via tab bar / More.

### USR-25 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** locale `ar` (`localStorage["mkt:locale"]="ar"`); roster with username "أحمد".
- **Steps:**
  1. Open page, open each dialog, trigger a success toast.
- **Expected:** `<html dir="rtl" lang="ar">`; h1 "إدارة المستخدمين"; all labels/buttons from `ar.json` (no raw keys like `admin.users.add`); table headers aligned to start (right); Arabic username renders correctly; Latin IDs stay readable.

### USR-26 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `<html data-theme="light">` and `"dark"`.
- **Steps:**
  1. View the table, badges, dialogs, toasts in both.
- **Expected:** text and badges readable in both; destructive buttons distinct; no hardcoded colors (all from tokens).

### USR-27 — Modals: focus trap, Escape, labels, toasts announced

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** admin; roster with one user.
- **Steps:**
  1. Tab to "Add user", press Enter. Tab through the dialog; Shift+Tab from first element.
  2. Press Escape. 3. Open Delete dialog, press Escape.
  3. Trigger a success and an error toast.
- **Expected:** `role="dialog"` `aria-modal="true"` named by its title; focus moves into the dialog and stays trapped; Escape closes and focus returns to the button that opened it. Delete dialog also closes on Escape (only the backdrop is disabled) — confirm this is wanted. Inputs have labels ("Username", "Password", "Administrator"); close button labelled "Close". Toasts are announced (success polite, error assertive). Axe reports no violations.
