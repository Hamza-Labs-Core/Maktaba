# Active sessions

- **Route:** `/account/sessions` (no params, no query state)
- **Component:** `src/pages/Profile/Sessions.tsx` (single component); design system `Button`, `Badge`, `EmptyState`, `ErrorState`, `Toast`
- **API calls:** `GET /api/me/sessions` → `{ items: [{ id, created_at, last_seen_at, expires_at, ip: string|null, user_agent: string|null, current: boolean }] }` (server: `api/internal/handlers/auth/selfservice.go`, always a non-null `items` array); `DELETE /api/me/sessions/{id}` (id URL-encoded) → 204. Via `api` in `src/lib/api.ts`. No polling; the list is refetched after a revoke.
- **Access:** signed-in user (`RequireAuth` + `AppShell`); server is owner-scoped. No admin check. Sidebar link `nav.sessions` ("Sessions").
- **Existing tests:** none. Gaps: everything.

## Summary

| ID     | Title                                                                | Category      | Priority | Covered by existing test?                                                                 |
| ------ | -------------------------------------------------------------------- | ------------- | -------- | ----------------------------------------------------------------------------------------- |
| ASE-01 | Sessions table renders with the current session badged               | Happy path    | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-02 | Current session cannot be revoked                                    | Permissions   | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-03 | Revoke another session                                               | Happy path    | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-04 | Revoke has no confirmation step                                      | Validation    | P2       | Yes (Sessions.test.tsx)                                                                   |
| ASE-05 | Revoke fails: error toast, row stays, button usable                  | Error         | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-06 | Revoke returns 401 because the current session was revoked elsewhere | Permissions   | P1       | Partial (Sessions.test.tsx: no blank page passes; 401 redirect is it.fails: bug)          |
| ASE-07 | Busy state on the row being revoked; two quick revokes               | Loading       | P2       | Yes (Sessions.test.tsx)                                                                   |
| ASE-08 | `items: []` shows the empty state                                    | Empty state   | P2       | Yes (Sessions.test.tsx)                                                                   |
| ASE-09 | `items: null` / missing `items` shows the empty state                | Empty state   | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-10 | Null / empty `ip` and `user_agent`; null dates                       | Empty state   | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-11 | No row has `current: true`                                           | Permissions   | P2       | Yes (Sessions.test.tsx)                                                                   |
| ASE-12 | Loading state                                                        | Loading       | P3       | No                                                                                        |
| ASE-13 | List load errors and Retry                                           | Error         | P2       | Yes (Sessions.test.tsx)                                                                   |
| ASE-14 | 401 on list load                                                     | Permissions   | P1       | Partial (Sessions.test.tsx: no blank page passes; 401 redirect is it.fails: bug)          |
| ASE-15 | Signed out deep link redirects to /login                             | Permissions   | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-16 | Crash during render shows a fallback; Back recovers                  | Error         | P1       | Yes (Sessions.test.tsx)                                                                   |
| ASE-17 | Nav link, Back and Forward                                           | Navigation    | P2       | Partial (Sessions.test.tsx: Back re-fetch; sidebar aria-current is AppShell, not covered) |
| ASE-18 | Arabic / RTL                                                         | RTL           | P2       | Yes (Sessions.test.tsx)                                                                   |
| ASE-19 | Light and dark theme                                                 | Theme         | P3       | No                                                                                        |
| ASE-20 | Phone width 375px                                                    | Responsive    | P3       | No                                                                                        |
| ASE-21 | Keyboard and screen reader basics                                    | Accessibility | P2       | No (it.todo: bug)                                                                         |

## Cases

### ASE-01 — Sessions table renders with the current session badged

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** signed in; `GET /api/me/sessions` → `{ "items": [ { "id": "s1", "created_at": "2026-10-01T09:00:00Z", "last_seen_at": "2026-10-19T07:00:00Z", "expires_at": "2026-11-01T09:00:00Z", "ip": "192.168.1.20", "user_agent": "Mozilla/5.0 (Macintosh) Firefox/131", "current": true }, { "id": "s2", "created_at": "2026-10-10T09:00:00Z", "last_seen_at": "2026-10-18T07:00:00Z", "expires_at": "2026-11-10T09:00:00Z", "ip": "10.0.0.5", "user_agent": "Maktaba-iOS/1.4", "current": false } ] }`
- **Steps:**
  1. Open `/account/sessions`.
- **Expected:** Heading "Active sessions" (`sessions.title`) and the line "Devices and browsers currently signed in to your account." Table `role="table"` named "Active sessions" with headers "Device", "IP address", "Signed in", "Last active", "Actions". Row s1 shows the user agent, an accent badge "This session" (`sessions.current`), IP in monospace, two formatted dates, and the muted text "Current" (`sessions.thisDevice`) in Actions. Row s2 shows a "Revoke" button. `expires_at` is not shown.

### ASE-02 — Current session cannot be revoked

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** as ASE-01
- **Steps:**
  1. Look at row s1's Actions cell; try to find any button in it.
- **Expected:** No "Revoke" button in the current row (`queryByRole("button")` within the row is null), so the user cannot sign themselves out from here. No `DELETE /api/me/sessions/s1` can be triggered from the UI.

### ASE-03 — Revoke another session

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** as ASE-01; `DELETE /api/me/sessions/s2` → 204; second `GET` → `{ "items": [ <s1 only> ] }`
- **Steps:**
  1. Click "Revoke" in row s2.
- **Expected:** One `DELETE /api/me/sessions/s2` with the CSRF header. Toast "Session revoked." (`sessions.revoked`). List refetched; only s1 remains. User stays signed in and on the page.

### ASE-04 — Revoke has no confirmation step

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** as ASE-01
- **Steps:**
  1. Click "Revoke" in row s2.
- **Expected:** Today the DELETE goes out immediately (no dialog), unlike Devices and API tokens which confirm first. Decide and lock in: either keep one-click (assert no dialog) or add a confirm dialog like `devices.remove.*` (inconsistent with sibling pages — UX question for the lead).

### ASE-05 — Revoke fails: error toast, row stays, button usable

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** variants for `DELETE /api/me/sessions/s2`: (a) 404 `{ "title": "Not Found", "status": 404, "detail": "session not found" }`; (b) 403 `{ "title": "Forbidden", "status": 403 }`; (c) 500 problem+json without detail; (d) fetch rejects.
- **Steps:**
  1. Click "Revoke" in row s2.
- **Expected:** Error toast (`role="alert"`) with (a) "session not found", (b) "Forbidden", (c) the problem title, (d) "Something went wrong.". Row s2 is still shown; its "Revoke" button is no longer busy and can be clicked again. No refetch on failure.

### ASE-06 — Revoke returns 401 because the current session was revoked elsewhere

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Partial (Sessions.test.tsx: no blank page passes; 401 redirect is it.fails: bug)
- **Preconditions / data:** as ASE-01; `DELETE /api/me/sessions/s2` → 401 `{ "title": "Unauthorized", "status": 401 }` (this browser's session was revoked from another device)
- **Steps:**
  1. Click "Revoke" in row s2.
- **Expected:** No blank page. Today only an error toast "Unauthorized" and the stale table stays. Desired: send the user to `/login` (currently no 401 redirect — likely bug).

### ASE-07 — Busy state on the row being revoked; two quick revokes

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** three sessions s1 (current), s2, s3; each DELETE resolves after a delay
- **Steps:**
  1. Click "Revoke" on s2 twice quickly.
  2. While s2 is pending, click "Revoke" on s3.
- **Expected:** s2's button is `aria-busy="true"` and disabled, so one DELETE for s2. Clicking s3 starts its own DELETE; both rows should show busy until each finishes. Today `revoking` holds a single id, so starting s3 drops s2's busy state and s2's `finally` clears s3's (minor bug: busy state is wrong with two revokes in flight). Final list reflects both removals.

### ASE-08 — `items: []` shows the empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** `GET /api/me/sessions` → `{ "items": [] }`
- **Steps:**
  1. Open `/account/sessions`.
- **Expected:** EmptyState "No active sessions" (`sessions.empty.title`) with "You have no other active sessions." No table, no ErrorState.

### ASE-09 — `items: null` / missing `items` shows the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** variants: (a) `{ "items": null }`; (b) `{}`; (c) 200 `text/html` body (api returns a string); (d) 200 `application/json` body `null`.
- **Steps:**
  1. Open `/account/sessions`.
- **Expected:** (a)-(c): EmptyState "No active sessions", no crash (`r.items ?? []`). (d): `r.items` throws inside `.then` → caught → ErrorState "Something went wrong." with Retry; never a blank page.

### ASE-10 — Null / empty `ip` and `user_agent`; null dates

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** rows with: (a) `"user_agent": null`; (b) `"user_agent": ""`; (c) `"ip": null`; (d) `"ip": ""`; (e) `"last_seen_at": null`; (f) `created_at` missing; (g) `current` missing.
- **Steps:**
  1. Open `/account/sessions`.
- **Expected:** All rows render. (a)/(b): "Unknown device" (`sessions.unknownDevice`). (c)/(d): an em dash "—". (e): should show a dash; today `formatDate(null)` shows 1 January 1970 (likely bug in `src/lib/i18n.tsx` `formatDate`). (f): today the literal `undefined` (likely bug). (g): treated as not current, "Revoke" shown.

### ASE-11 — No row has `current: true`

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** two sessions, both `"current": false` (e.g. server could not match the cookie, or the request came via a token)
- **Steps:**
  1. Open the page; click "Revoke" on the row whose IP/UA match this browser.
- **Expected:** Both rows show "Revoke" and no badge. If the DELETE succeeds and the next `GET` returns 401, the user is sent to sign in (see ASE-14) rather than seeing a broken page. Record the actual behaviour.

### ASE-12 — Loading state

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/me/sessions` pending
- **Steps:**
  1. Open `/account/sessions`.
- **Expected:** Heading, description line, and "Loading…"; no EmptyState flash.

### ASE-13 — List load errors and Retry

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** first `GET`: (a) 500 `{ "title": "Internal Server Error", "status": 500, "detail": "db down" }`; (b) 404 `{ "title": "Not Found", "status": 404 }`; (c) fetch rejects; (d) 200 `application/json` body `{bad`. Second `GET` → list as ASE-01.
- **Steps:**
  1. Open `/account/sessions`.
  2. Click "Try again".
- **Expected:** ErrorState "Something went wrong." with description (a) "db down", (b) "Not Found", (c)/(d) "Something went wrong.". Retry clears the error and renders the table.

### ASE-14 — 401 on list load

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Partial (Sessions.test.tsx: no blank page passes; 401 redirect is it.fails: bug)
- **Preconditions / data:** auth probe OK; `GET /api/me/sessions` → 401 `{ "title": "Unauthorized", "status": 401 }`
- **Steps:**
  1. Open `/account/sessions`.
- **Expected:** No blank page. Today ErrorState "Unauthorized". Desired: redirect to `/login` with `state.from` (currently no 401 redirect — likely bug).

### ASE-15 — Signed out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** `GET /api/auth/me` → 401
- **Steps:**
  1. Open `/account/sessions` directly.
- **Expected:** Redirect to `/login` with `state.from = "/account/sessions"`; no sessions request.

### ASE-16 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** start on `/account/devices`. `GET /api/me/sessions` → `{ "items": [ { "id": "s2", "user_agent": { "ua": "x" }, "ip": "1.2.3.4", "created_at": "2026-10-10T09:00:00Z", "last_seen_at": "2026-10-18T07:00:00Z", "current": false } ] }` (object as React child throws); or mock `Badge` to throw.
- **Steps:**
  1. From `/account/devices` click sidebar "Sessions".
  2. Press browser Back.
  3. Click "Sessions" again, then "API tokens".
- **Expected:** Route-level error fallback, not a blank page; nav still visible. Back renders `/account/devices` without a refresh. "API tokens" link renders `/account/tokens`.

### ASE-17 — Nav link, Back and Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial (Sessions.test.tsx: Back re-fetch; sidebar aria-current is AppShell, not covered)
- **Preconditions / data:** as ASE-01
- **Steps:**
  1. From `/library` click "Sessions"; revoke s2; press Back; press Forward.
- **Expected:** "Sessions" link `aria-current="page"` (and "Account" should not also be current — see ACC-20). Back goes to `/library`; Forward re-fetches and shows the current server list (s2 absent).

### ASE-18 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (Sessions.test.tsx)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; data as ASE-01
- **Steps:**
  1. Open `/account/sessions`.
- **Expected:** `<html dir="rtl" lang="ar">`. Heading "الجلسات النشطة", headers "الجهاز", "عنوان IP", "وقت الدخول", "آخر نشاط", "إجراءات", badge "هذه الجلسة", "الحالية". Dates in `ar-EG`. Table columns run right to left; IPs and user agents (Latin) stay readable. Note the revoke button reads "إلغاء", which in Arabic is also the word used for "Cancel" (`common.cancel`) — ambiguous wording (minor i18n bug; "إنهاء" or "إبطال" would be clearer).

### ASE-19 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light, then dark
- **Steps:**
  1. Open the table and the empty state in each theme.
- **Expected:** Table borders, accent badge, muted text and destructive buttons readable in both; tokens only.

### ASE-20 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812; long user agent (200 chars)
- **Steps:**
  1. Reach the page via "More" → "Sessions".
- **Expected:** No horizontal page scroll: the table scrolls inside itself (`.mkt-table { display: block; overflow-x: auto }` under the phone breakpoint). Revoke buttons reachable by scrolling the table. Long user agent truncated.

### ASE-21 — Keyboard and screen reader basics

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No (it.todo: bug)
- **Preconditions / data:** as ASE-01 plus a third session
- **Steps:**
  1. Tab through the page; press Enter on a "Revoke".
- **Expected:** Only non-current rows have focusable "Revoke" buttons, with visible focus. Every "Revoke" button has the same name; expect a name that identifies the session (e.g. includes the device) (currently ambiguous — minor a11y bug). After a revoke the row is removed; focus should move somewhere sensible (next row's button or the table), not to `<body>`. Toast announced politely.
