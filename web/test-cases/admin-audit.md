# Admin: Audit log

- **Route:** `/admin/audit` (no params; source, library id and filters are component state only, not in the URL)
- **Component:** `src/pages/Admin/AuditLog.tsx` (`AdminAuditLog` → `AdminGate` → `AuditInner`; helpers `nextCursor`, `payloadText`, `csvCell` in the same file). Uses `@ds` `Select`, `Input`, `Button`, `EmptyState`, `ErrorState`; dates via `useI18n().formatDate`.
- **API calls:** via `api.get` in `src/lib/api.ts`, page size 50:
  - Security feed (default): `GET /api/security/audit?limit=50[&cursor=<ts of last row, RFC3339Nano>]` → `{ items: Entry[] }`
  - Library feed: `GET /api/libraries/{id}/audit?limit=50[&cursor=<id of last row>]` → `{ items: Entry[], next_cursor? }` (`next_cursor` is ignored by the client)
  - `Entry = { id: number, event: string, actor_user_id?: string, target_id?: string, payload?: unknown, ts: string }`
  - "Load more" shown while the last page had 50 rows. Filters (event, actor substring, from/to `datetime-local`) are client-side over loaded rows. "Export CSV" downloads `audit-<source>.csv` of the filtered rows via a Blob link.
- **Access:** admin only. `RequireAuth` → `AdminGate` ("Admins only"). `/api/security/audit` is admin-only on the server (`api/internal/handlers/auth/auth.go`).
- **Existing tests:** none (`AuditLog.test.tsx` does not exist). Gaps: everything below.

## Summary

| ID     | Title                                                       | Category      | Priority | Covered by existing test? |
| ------ | ----------------------------------------------------------- | ------------- | -------- | ------------------------- |
| AUD-01 | Security feed loads and renders rows                        | Happy path    | P1       | Yes (AuditLog.test.tsx)   |
| AUD-02 | Loading state                                               | Loading       | P3       | No                        |
| AUD-03 | Empty feed shows empty state                                | Empty state   | P2       | Yes (AuditLog.test.tsx)   |
| AUD-04 | `items` null or missing                                     | Empty state   | P1       | Yes (AuditLog.test.tsx)   |
| AUD-05 | Null / missing optional fields in an entry                  | Empty state   | P1       | Yes (AuditLog.test.tsx)   |
| AUD-06 | Load more pages with the timestamp cursor                   | Happy path    | P1       | Yes (AuditLog.test.tsx)   |
| AUD-07 | Load more hidden when a page is short                       | Happy path    | P2       | Yes (AuditLog.test.tsx)   |
| AUD-08 | Load more failure                                           | Error         | P2       | Yes (AuditLog.test.tsx)   |
| AUD-09 | Switch to Library source needs an id                        | Validation    | P2       | Yes (AuditLog.test.tsx)   |
| AUD-10 | Library feed loads and pages by integer id                  | Happy path    | P1       | Yes (AuditLog.test.tsx)   |
| AUD-11 | Typing a library id fires one request per keystroke / races | Error         | P2       | Yes (AuditLog.test.tsx)   |
| AUD-12 | Library feed 404 for unknown id                             | Error         | P2       | Yes (AuditLog.test.tsx)   |
| AUD-13 | Initial load errors + Try again                             | Error         | P2       | Yes (AuditLog.test.tsx)   |
| AUD-14 | Event filter                                                | Happy path    | P2       | Yes (AuditLog.test.tsx)   |
| AUD-15 | Actor filter                                                | Happy path    | P2       | Yes (AuditLog.test.tsx)   |
| AUD-16 | Date range filter                                           | Happy path    | P2       | Yes (AuditLog.test.tsx)   |
| AUD-17 | Filters match nothing                                       | Empty state   | P2       | Yes (AuditLog.test.tsx)   |
| AUD-18 | Refresh reloads from the first page                         | Happy path    | P3       | No                        |
| AUD-19 | Export CSV content and escaping                             | Happy path    | P2       | Yes (AuditLog.test.tsx)   |
| AUD-20 | CSV export does not allow formula injection                 | Permissions   | P1       | Yes (AuditLog.test.tsx)   |
| AUD-21 | Page crash shows route error fallback; Back works           | Navigation    | P1       | Yes (AuditLog.test.tsx)   |
| AUD-22 | Non-admin sees "Admins only"                                | Permissions   | P1       | Yes (AuditLog.test.tsx)   |
| AUD-23 | Signed-out user redirected to /login                        | Permissions   | P1       | Yes (AuditLog.test.tsx)   |
| AUD-24 | Deep link, Back/Forward, state not kept                     | Navigation    | P3       | No                        |
| AUD-25 | Phone width (375px)                                         | Responsive    | P3       | No                        |
| AUD-26 | Arabic / RTL                                                | RTL           | P3       | No                        |
| AUD-27 | Light and dark theme                                        | Theme         | P3       | No                        |
| AUD-28 | Keyboard and labels                                         | Accessibility | P2       | Yes (AuditLog.test.tsx)   |

## Cases

### AUD-01 — Security feed loads and renders rows

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** admin; `GET /api/security/audit?limit=50` → `{ "items":[{ "id":2,"event":"login.failed","actor_user_id":"u1","target_id":"u1","payload":{"ip":"10.0.0.2"},"ts":"2026-10-18T10:00:00.123456789Z" },{ "id":1,"event":"user.created","actor_user_id":"u1","target_id":"u9","payload":"note","ts":"2026-10-17T09:00:00Z" }] }`
- **Steps:**
  1. Open `/admin/audit`.
- **Expected:** h1 "Audit log"; toolbar with "Refresh" and "Export CSV" (enabled). Source select "Source" = "Security events"; no Library ID input. Table named "Audit log" with headers Timestamp / Event / Actor / Target / Details. Row 1: formatted date (e.g. "Oct 18, 2026, 10:00 AM" in local tz), "login.failed", "u1", "u1", `{"ip":"10.0.0.2"}`. Row 2 Details "note" (string payload shown as is). Exactly one GET with `limit=50` and no cursor. No "Load more" (2 < 50).

### AUD-02 — Loading state

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** GET never resolves.
- **Steps:**
  1. Open page.
- **Expected:** "Loading…"; filters visible; Export disabled.

### AUD-03 — Empty feed shows empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** GET → `{ "items": [] }`.
- **Steps:**
  1. Open page.
- **Expected:** EmptyState "No audit events" / "No events match the current source and filters."; no table; no "Load more"; Export disabled; Event select has only "All events".

### AUD-04 — `items` null or missing

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** list field read from both endpoints: `items`. Run: `{ "items": null }`, `{}`, and the same for the library feed and for a "Load more" page.
- **Steps:**
  1. Open page (and switch to Library with id "l1"; and click Load more after a full first page).
- **Expected:** EmptyState "No audit events" (or the existing rows stay for Load more), no crash; Load more hides after an empty page. (Code uses `res.items ?? []` — should pass.)

### AUD-05 — Null / missing optional fields in an entry

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** items:
  - `{ "id":3,"event":"x","actor_user_id":null,"target_id":null,"payload":null,"ts":"2026-10-18T10:00:00Z" }`
  - `{ "id":4,"event":"y","ts":"2026-10-18T09:00:00Z" }` (optional keys missing)
  - `{ "id":5,"event":"z","payload":{},"ts":"" }` and one with `"ts": null`
  - `{ "id":6,"event":null,"ts":"2026-10-18T08:00:00Z" }`
- **Steps:**
  1. Open page; open the Event select; type in Actor filter; Export CSV.
- **Expected:** Actor and Target show "—" for null/missing; Details empty for null/missing, `{}` for empty object. No crash. Bad `ts` should show something clear (e.g. "—"); currently `""` shows the raw empty string and `null` shows 1 Jan 1970 because `new Date(null)` is epoch (likely bug). `event: null` should not add a blank/"null" option that breaks the select. Actor filter treats missing actor as "" (rows hidden when filter is non-empty).

### AUD-06 — Load more pages with the timestamp cursor

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** first GET → 50 items, last has `ts:"2026-10-01T00:00:00.5Z"`; second GET (`cursor=2026-10-01T00%3A00%3A00.5Z`) → 10 items.
- **Steps:**
  1. Open page; click "Load more".
- **Expected:** second request `GET /api/security/audit?limit=50&cursor=2026-10-01T00%3A00%3A00.5Z` (URL-encoded). Button loading while pending. Table shows 60 rows in order, no duplicates, no React key warnings. "Load more" disappears (10 < 50). Filters still apply to all 60.

### AUD-07 — Load more hidden when a page is short

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** (a) first page 49 items; (b) first page exactly 50 then second page `[]`.
- **Steps:**
  1. Open page; in (b) click Load more.
- **Expected:** (a) no Load more. (b) Load more shown after page 1; after the empty page 2 it disappears and the 50 rows stay.

### AUD-08 — Load more failure

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** page 1 → 50 items; page 2 → 500 problem `{ "title":"internal","status":500,"detail":"db down" }` or fetch rejects.
- **Steps:**
  1. Click Load more.
- **Expected:** wanted: the 50 loaded rows stay visible with an inline error and a retry on the Load more button. Currently the whole table is replaced by the ErrorState "Something went wrong." / "db down" and "Try again" reloads from page 1, dropping loaded rows (likely bug / poor UX). No crash.

### AUD-09 — Switch to Library source needs an id

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** security feed loaded.
- **Steps:**
  1. Set Source to "Library".
  2. Type spaces only in "Library ID".
- **Expected:** "Library ID" input appears; EmptyState "Enter a library ID to load its audit feed."; no request to `/api/libraries/...`; previous security rows not shown; Export disabled. Spaces-only behaves as empty.

### AUD-10 — Library feed loads and pages by integer id

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** `GET /api/libraries/lib%201/audit?limit=50` → 50 items with last `id: 120`, `next_cursor: 120`; page 2 (`cursor=120`) → 5 items.
- **Steps:**
  1. Source = Library; type "lib 1"; wait. Click Load more.
- **Expected:** id is URL-encoded in the path; page 2 uses `cursor=120` (the last row id, not ts). 55 rows. Switching back to "Security events" reloads the security feed from page 1 and resets "Load more".

### AUD-11 — Typing a library id fires one request per keystroke / races

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** GET for `/api/libraries/l/audit` resolves slowly (500 ms) with rows A; `/api/libraries/l1/audit` resolves fast (50 ms) with rows B.
- **Steps:**
  1. Source = Library; type "l1" quickly.
- **Expected:** wanted: requests debounced, and only the response for the current id ("l1") is shown. Currently one request per keystroke and no stale-response guard, so rows A from "l" can overwrite rows B (currently unhandled — likely bug). Same race when switching Source quickly.

### AUD-12 — Library feed 404 for unknown id

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** `GET /api/libraries/nope/audit?limit=50` → 404 problem `{ "title":"not found","status":404,"detail":"library not found" }`.
- **Steps:**
  1. Source = Library, id "nope".
- **Expected:** ErrorState "Something went wrong." with description "library not found" and "Try again". Fixing the id loads rows and clears the error.

### AUD-13 — Initial load errors + Try again

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** GET → (a) 403 problem `{ "title":"forbidden","status":403 }`; (b) 500 `text/plain` "boom"; (c) fetch rejects; (d) 200 `application/json` body `{oops`; (e) 401.
- **Steps:**
  1. Open page. 2. Make GET succeed, click "Try again".
- **Expected:** ErrorState kind server, description (a) "forbidden", (b) "boom", (c)/(d) "Something went wrong."; Export disabled. Try again shows rows. (e) wanted: redirect to `/login` (currently only the error state).

### AUD-14 — Event filter

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** rows with events `login.failed` x2, `user.created` x1, `token.revoked` x1.
- **Steps:**
  1. Open "Event" select; pick "login.failed". 2. Pick "All events". 3. Pick "user.created", then switch Source to Library and enter an id whose feed has no `user.created`.
- **Expected:** options: "All events" then the distinct events sorted alphabetically. 1: only the 2 login.failed rows. 2: all rows. 3: wanted: the event filter resets when the source changes (currently it keeps the old value, which may not be in the new options, so the list looks empty with no visible reason — minor bug).

### AUD-15 — Actor filter

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** actors `u1-alice`, `u2-bob`, missing.
- **Steps:**
  1. Type "u1" in "Actor". 2. Type "U1". 3. Clear.
- **Expected:** 1: only u1 rows (substring match). 2: no rows — match is case-sensitive (confirm that is wanted). 3: all rows including those with no actor.

### AUD-16 — Date range filter

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** rows at 2026-10-17T09:00Z, 2026-10-18T10:00:30Z, 2026-10-19T12:00Z; test in a fixed TZ (e.g. `TZ=UTC`).
- **Steps:**
  1. From = 2026-10-18T00:00. 2. To = 2026-10-18T10:00. 3. From later than To.
- **Expected:** inputs are `datetime-local` and interpreted in local time. 1: hides the 17th. 2: with To = 10:00 the 10:00:30 row is hidden — wanted: "To" includes the whole minute (currently excluded; minor bug). 3: no rows → EmptyState (wanted: a hint that From is after To).

### AUD-17 — Filters match nothing

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** rows loaded; full page of 50.
- **Steps:**
  1. Type actor "zzz".
- **Expected:** EmptyState "No audit events" / "No events match the current source and filters."; Export disabled. Note: "Load more" is hidden too, so older matching rows cannot be reached (wanted: keep Load more visible while filters hide all rows — likely gap).

### AUD-18 — Refresh reloads from the first page

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** 2 pages loaded; filters set.
- **Steps:**
  1. Click "Refresh".
- **Expected:** GET without cursor; "Loading…" then only page 1 rows; filters kept and applied; Load more state recomputed.

### AUD-19 — Export CSV content and escaping

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** spy on `URL.createObjectURL` / anchor click; rows incl. payload `{"msg":"a, \"b\"\nc"}`, actor missing, event filter set to one event.
- **Steps:**
  1. Click "Export CSV".
- **Expected:** file name `audit-security.csv` (or `audit-library.csv`); MIME `text/csv;charset=utf-8`. First line `id,ts,event,actor_user_id,target_id,payload`. Only filtered rows. Cells with comma, quote or newline are wrapped in quotes with `""` doubling; missing actor/target empty; missing payload `{}`. `URL.revokeObjectURL` called. Arabic text in payload survives (UTF-8; wanted: a BOM so Excel opens it correctly).

### AUD-20 — CSV export does not allow formula injection

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** an entry whose user-controlled fields start with `=`, `+`, `-`, `@` (e.g. `target_id: "=HYPERLINK(\"http://evil\")"`, or a payload string `"+cmd"`); audit events can carry usernames chosen by other users.
- **Steps:**
  1. Export CSV.
- **Expected:** such cells are neutralised (e.g. prefixed with `'`) so a spreadsheet does not run them. Currently `csvCell` only quotes `"`, `,` and newline (currently unhandled — likely bug).

### AUD-21 — Page crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** full app router; GET → `{ "items": {} }` (non-array object: `rows.forEach` throws in the events memo) or mock `Select` to throw.
- **Steps:**
  1. Start on `/admin/users`, click sidebar "Audit log".
  2. Press browser Back; then click another nav link.
- **Expected:** route-level error fallback in the content area (shell visible), not a blank page. Back shows `/admin/users` without reload; other links work. Wanted also: non-array `items` treated as empty instead of throwing.

### AUD-22 — Non-admin sees "Admins only"

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** `/api/auth/me` → `is_admin:false`.
- **Steps:**
  1. Open `/admin/audit` directly.
- **Expected:** ErrorState "Admins only" / "You need administrator access to view this page."; no request to `/api/security/audit`; no filters or Export; "Audit log" not in sidebar.

### AUD-23 — Signed-out user redirected to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** `/api/auth/me` → 401.
- **Steps:**
  1. Open `/admin/audit`.
- **Expected:** redirect to `/login` with `state.from === "/admin/audit"`.

### AUD-24 — Deep link, Back/Forward, state not kept

- **Priority:** P3
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** admin.
- **Steps:**
  1. Open `/admin/audit` directly. 2. Set Source Library, id "l1", event filter. 3. Navigate away and press Back.
- **Expected:** direct load works. After Back the page is reset to Security events with no filters (state is not in the URL). Wanted: source, library id and filters in the query string so links and Back keep them (enhancement; record current behaviour).

### AUD-25 — Phone width (375px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** 375x812; rows with long ids and long payloads.
- **Steps:**
  1. Open page; scroll table.
- **Expected:** filter grid stacks to one column; no horizontal page scroll (table scrolls inside itself; payload cell truncated with ellipsis, max 360px); toolbar buttons reachable; nav reachable.

### AUD-26 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** locale `ar`; payload with Arabic text.
- **Steps:**
  1. Open page.
- **Expected:** `<html dir="rtl">`; h1 "سجل التدقيق"; all `audit.*` labels Arabic; dates via `ar-EG` with Arabic-Indic digits; table mirrored; mono ids/payload still readable (LTR inside the cell).

### AUD-27 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light / dark.
- **Steps:**
  1. View table, filters, ErrorState, EmptyState.
- **Expected:** text, borders, date inputs and their picker icons readable in both; no hardcoded colors.

### AUD-28 — Keyboard and labels

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (AuditLog.test.tsx)
- **Preconditions / data:** rows loaded.
- **Steps:**
  1. Tab through toolbar, filters, table, Load more.
  2. Run axe.
- **Expected:** toolbar has `role="toolbar"` (wanted: an accessible name; arrow-key navigation if keeping the role); every select/input labelled (Source, Library ID, Event, Actor, From, To); focus visible; changing filters updates the row count for screen readers (wanted: a polite live region with "N events"). Axe: no violations.
