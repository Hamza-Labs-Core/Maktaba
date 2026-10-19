# Admin: Library access control

- **Route:** `/admin/library-acl` (no params, no query string)
- **Component:** `src/pages/Admin/LibraryACL.tsx` (`LibraryACL` → `AdminGate` → `LibraryACLInner`). Uses `@ds` `Select`, `Button`, `EmptyState`, `ErrorState`, `useToast`.
- **API calls:** via `api` in `src/lib/api.ts`:
  - `GET /api/admin/library-acl` → `{ users: [{id, username}], libraries: [{id, name}], grants: [{user_id, library_id, role}] }` (server: `api/internal/handlers/libraryacl/libraryacl.go`; `users`/`libraries` are always arrays server side, `grants` can be `null` when there are none).
  - `PUT /api/admin/library-acl` body `{ grants: [{user_id, library_id, role}] }` (only changed cells; `role:"none"` revokes) → `{ grants: [...] }` (all grants after the change).
  - Roles: `none`, `read`, `write`, `admin` (labels `acl.role.*`: None / Read / Write / Admin).
- **Access:** admin only. `RequireAuth` → `AdminGate` ("Admins only"). Server returns 403 for non-admins.
- **Existing tests:** none (`LibraryACL.test.tsx` does not exist). Gaps: everything below.

## Summary

| ID     | Title                                                 | Category      | Priority | Covered by existing test? |
| ------ | ----------------------------------------------------- | ------------- | -------- | ------------------------- |
| ACL-01 | Matrix renders users x libraries with current roles   | Happy path    | P1       | No                        |
| ACL-02 | Loading state before GET resolves                     | Loading       | P3       | No                        |
| ACL-03 | Change cells and save sends only the diff             | Happy path    | P1       | No                        |
| ACL-04 | Setting a cell to None revokes it                     | Happy path    | P1       | No                        |
| ACL-05 | Changing a cell back removes it from the diff         | Validation    | P2       | No                        |
| ACL-06 | Save button state and count label                     | Validation    | P2       | No                        |
| ACL-07 | Save failure keeps edits and shows error toast        | Error         | P1       | No                        |
| ACL-08 | Save response `grants: null` / missing                | Empty state   | P1       | No                        |
| ACL-09 | No users or no libraries shows empty state            | Empty state   | P2       | No                        |
| ACL-10 | GET list fields `null` or missing do not crash        | Empty state   | P1       | No                        |
| ACL-11 | GET errors (403, 404, 500, network, non-JSON) + Retry | Error         | P2       | No                        |
| ACL-12 | Unknown role in a grant                               | Error         | P3       | No                        |
| ACL-13 | Grants for unknown user/library ids                   | Empty state   | P3       | No                        |
| ACL-14 | Leaving with unsaved changes                          | Navigation    | P2       | No                        |
| ACL-15 | Page crash shows route error fallback; Back works     | Navigation    | P1       | No                        |
| ACL-16 | Non-admin sees "Admins only" and no request           | Permissions   | P1       | No                        |
| ACL-17 | Signed-out user redirected to /login                  | Permissions   | P1       | No                        |
| ACL-18 | Deep link, Back/Forward, nav link                     | Navigation    | P2       | No                        |
| ACL-19 | Wide matrix at 375px scrolls inside its box           | Responsive    | P3       | No                        |
| ACL-20 | Arabic / RTL                                          | RTL           | P3       | No                        |
| ACL-21 | Light and dark theme                                  | Theme         | P3       | No                        |
| ACL-22 | Keyboard and labels on every cell                     | Accessibility | P2       | No                        |

## Cases

### ACL-01 — Matrix renders users x libraries with current roles

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** admin; `GET /api/admin/library-acl` →
  `{ "users":[{"id":"u1","username":"alice"},{"id":"u2","username":"bob"}], "libraries":[{"id":"l1","name":"Lectures"},{"id":"l2","name":"Kids"}], "grants":[{"user_id":"u1","library_id":"l1","role":"admin"},{"user_id":"u2","library_id":"l2","role":"read"}] }`
- **Steps:**
  1. Open `/admin/library-acl`.
- **Expected:** h1 "Library access control", description `acl.desc`. Table named "Library access control"; header cells "User", "Lectures", "Kids"; row headers alice, bob. Select "alice access to Lectures" = Admin, "bob access to Kids" = Read, the other two = None. Save button reads "Save" and is disabled.

### ACL-02 — Loading state before GET resolves

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** GET never resolves.
- **Steps:**
  1. Open page.
- **Expected:** "Loading…" text; no table, no EmptyState; Save disabled.

### ACL-03 — Change cells and save sends only the diff

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** ACL-01 data; `PUT /api/admin/library-acl` → `{ "grants":[{"user_id":"u1","library_id":"l1","role":"admin"},{"user_id":"u2","library_id":"l2","role":"read"},{"user_id":"u2","library_id":"l1","role":"write"}] }`
- **Steps:**
  1. Set "bob access to Lectures" to Write.
  2. Click "Save 1 change(s)".
- **Expected:** PUT body exactly `{ "grants":[{"user_id":"u2","library_id":"l1","role":"write"}] }`. Button shows loading while pending. Toast "Access permissions saved.". Cells reflect the response; Save goes back to "Save" disabled.

### ACL-04 — Setting a cell to None revokes it

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** ACL-01 data; PUT → grants without the u1/l1 row.
- **Steps:**
  1. Set "alice access to Lectures" to None. Save.
- **Expected:** PUT body `{ "grants":[{"user_id":"u1","library_id":"l1","role":"none"}] }`; after save the cell shows None.

### ACL-05 — Changing a cell back removes it from the diff

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** ACL-01 data.
- **Steps:**
  1. Set bob/Lectures None → Read (label "Save 1 change(s)").
  2. Set it back to None.
- **Expected:** label returns to "Save" and the button is disabled; nothing is sent.

### ACL-06 — Save button state and count label

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** ACL-01 data.
- **Steps:**
  1. Change 3 different cells.
  2. Double-click Save quickly.
- **Expected:** label "Save 3 change(s)". Only one PUT is sent (button is loading/disabled while saving). (Wanted: pluralised copy instead of "change(s)" — polish.)

### ACL-07 — Save failure keeps edits and shows error toast

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** PUT → (a) 400 problem `{ "title":"bad request","status":400,"detail":"unknown role" }`; (b) 403 problem `{ "title":"forbidden","status":403 }`; (c) 500 `text/plain`; (d) fetch rejects.
- **Steps:**
  1. Change a cell, Save.
- **Expected:** error toast: (a) "unknown role", (b) "forbidden", (c) the response text, (d) "Something went wrong.". Edited cells keep their new values; Save label still shows the count and can be retried.

### ACL-08 — Save response `grants: null` / missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** revoke the last grant; PUT → `{ "grants": null }`; also `{}`.
- **Steps:**
  1. Set every granted cell to None. Save.
- **Expected:** no crash; all cells show None; Save disabled; success toast. (Code uses `res.grants ?? []`.)

### ACL-09 — No users or no libraries shows empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** GET → `{ "users":[], "libraries":[{"id":"l1","name":"Lectures"}], "grants":[] }`; then `{ "users":[{"id":"u1","username":"alice"}], "libraries":[], "grants":[] }`.
- **Steps:**
  1. Open page.
- **Expected:** EmptyState "Nothing to map" / "Add at least one user and one library to manage access."; no table.

### ACL-10 — GET list fields `null` or missing do not crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** fields read from `GET /api/admin/library-acl`: `users`, `libraries`, `grants`. Run each separately:
  - `users: null`; `users` key missing
  - `libraries: null`; `libraries` key missing
  - `grants: null`; `grants` key missing (users and libraries non-empty)
  - whole body `{}`
- **Steps:**
  1. Open page.
- **Expected:** `users`/`libraries` null or missing → EmptyState "Nothing to map", not a blank screen. `grants` null/missing → full matrix with every cell None. Currently `grants` is guarded (`?? []`) but `users`/`libraries` are not: `matrix.users.length` throws (currently unhandled — likely bug).

### ACL-11 — GET errors (403, 404, 500, network, non-JSON) + Retry

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** GET → (a) 403 problem `{ "title":"forbidden","status":403,"detail":"admin only" }`; (b) 404 `text/plain` "404 page not found"; (c) 500 problem without detail `{ "title":"internal","status":500 }`; (d) fetch rejects; (e) 200 with `Content-Type: application/json` body `{bad json`.
- **Steps:**
  1. Open page. 2. Make GET succeed with ACL-01 data, click "Try again".
- **Expected:** ErrorState (kind server) title "Something went wrong.", description (a) "admin only", (b) "404 page not found", (c) "internal", (d)/(e) "Something went wrong.". Save disabled. After Try again: matrix renders. Wanted while retrying: "Loading…" — currently shows "Nothing to map" for a moment because the matrix was set to empty on error (minor).

### ACL-12 — Unknown role in a grant

- **Priority:** P3
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** grants include `{ "user_id":"u1","library_id":"l1","role":"owner" }`.
- **Steps:**
  1. Open page; then Save after changing some other cell.
- **Expected:** page renders; the cell does not silently show a wrong role that then gets saved. Wanted: show the raw role or "Unknown". The PUT must only contain the cell the user changed (not u1/l1).

### ACL-13 — Grants for unknown user/library ids

- **Priority:** P3
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** grants include `{ "user_id":"ghost","library_id":"l1","role":"read" }` where `ghost` is not in `users`.
- **Steps:**
  1. Open page, change another cell, Save.
- **Expected:** no extra row for ghost; the PUT body does not include ghost.

### ACL-14 — Leaving with unsaved changes

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** ACL-01 data.
- **Steps:**
  1. Change 2 cells, do not save.
  2. Click another sidebar link, or press browser Back, or reload.
- **Expected:** wanted: a "you have unsaved changes" prompt (currently unhandled — edits are lost silently; likely gap). Coming back shows the server state.

### ACL-15 — Page crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** full app router; GET → `{ "users": null, "libraries": [...], "grants": [] }` (throws today) or mock `Select` to throw.
- **Steps:**
  1. Start at `/library`, click sidebar "Library access".
  2. Press browser Back; then click another nav link.
- **Expected:** route-level error fallback in the content area (shell visible), not a blank page. Back shows `/library` without a reload; other nav links work.

### ACL-16 — Non-admin sees "Admins only" and no request

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `/api/auth/me` → user with `is_admin:false`.
- **Steps:**
  1. Open `/admin/library-acl`.
- **Expected:** ErrorState "Admins only" / "You need administrator access to view this page."; no GET to `/api/admin/library-acl`; no Save button; "Library access" not in sidebar.

### ACL-17 — Signed-out user redirected to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** `/api/auth/me` → 401.
- **Steps:**
  1. Open `/admin/library-acl`.
- **Expected:** redirect to `/login` with `state.from === "/admin/library-acl"`.

### ACL-18 — Deep link, Back/Forward, nav link

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** admin.
- **Steps:**
  1. Open `/admin/library-acl` directly. 2. Go to `/admin/users` via nav, then Back, then Forward.
- **Expected:** page loads on direct open; Back re-renders the matrix (refetches); Forward returns to Users. Sidebar item "Library access" is marked current.

### ACL-19 — Wide matrix at 375px scrolls inside its box

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** 375x812; 3 users x 8 libraries with long names.
- **Steps:**
  1. Open page, scroll the matrix sideways.
- **Expected:** only `.mkt-acl__scroll` scrolls horizontally; the page body has no horizontal scroll; Save button visible in the header; nav reachable.

### ACL-20 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** locale `ar`; libraries named "محاضرات", users "أحمد".
- **Steps:**
  1. Open page, change a cell.
- **Expected:** `<html dir="rtl">`; h1 "التحكم في صلاحيات المكتبات"; role options and Save count label in Arabic (no raw keys); user column on the right; row header text aligned to start; select aria-labels use the Arabic `acl.cellLabel`.

### ACL-21 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light and dark.
- **Steps:**
  1. View matrix, selects, ErrorState, EmptyState.
- **Expected:** readable text, visible select borders and focus rings in both; no hardcoded colors.

### ACL-22 — Keyboard and labels on every cell

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** ACL-01 data.
- **Steps:**
  1. Tab from Save through the matrix; change a cell with the keyboard (arrow keys / Enter).
  2. Run axe.
- **Expected:** tab order is row by row; every select has an accessible name like "alice access to Lectures"; row headers use `<th scope="row">`; focus visible; success/error toasts announced. Axe: no violations.
