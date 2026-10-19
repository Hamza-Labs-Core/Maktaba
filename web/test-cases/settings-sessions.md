# Settings: Sessions tab

- **Route:** `/settings/sessions`
- **Component:** `src/pages/Settings.tsx` → `SessionsTab` (static). Related page: `src/pages/Profile/Sessions.tsx` at `/account/sessions`.
- **API calls:** none. The tab only shows `settings.sessions.deferred`. Note: the comment in `Settings.tsx` says there is no `GET /api/me/sessions`, but `src/pages/Profile/Sessions.tsx` already uses `GET /api/me/sessions` → `{ items: [{ id, created_at, last_seen_at, expires_at, ip, user_agent, current }] }` and `DELETE /api/me/sessions/{id}` (204), routed at `/account/sessions`.
- **Access:** signed-in user (`RequireAuth`). No admin gate.
- **Existing tests:** none for this tab. Gaps: all below. (`/account/sessions` has its own page and is out of scope here.)

The tab is a single static paragraph, so this file is short on purpose.

## Summary

| ID     | Title                                               | Category    | Priority | Covered by existing test? |
| ------ | --------------------------------------------------- | ----------- | -------- | ------------------------- |
| SSE-01 | Shows the sessions panel text                       | Happy path  | P2       | Yes (`Settings.test.tsx`) |
| SSE-02 | Makes no API request                                | Happy path  | P3       | No                        |
| SSE-03 | Stale text: should point to the real sessions page  | Navigation  | P2       | No (it.fails: bug)        |
| SSE-04 | Signed-out deep link redirects to /login            | Permissions | P1       | Yes (`Settings.test.tsx`) |
| SSE-05 | Deep link and Back/Forward to and from the tab      | Navigation  | P2       | Yes (`Settings.test.tsx`) |
| SSE-06 | Render crash shows route error fallback; Back works | Error       | P1       | Yes (`Settings.test.tsx`) |
| SSE-07 | Arabic: RTL and Arabic text                         | RTL         | P3       | No                        |
| SSE-08 | Phone width: no horizontal scroll                   | Responsive  | P3       | No (e2e-only)             |
| SSE-09 | Light and dark theme readable                       | Theme       | P3       | No                        |

## Cases

### SSE-01 — Shows the sessions panel text

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** signed in as normal user `{ id: "u1", username: "maha", is_admin: false }`.
- **Steps:**
  1. Open `/settings/sessions`.
- **Expected:** `h1` "Settings", the Settings nav, and one muted paragraph "Per-session listing requires a backend endpoint that is not yet available." (`settings.sessions.deferred`). Same for an admin user.

### SSE-02 — Makes no API request

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `api.get` / `fetch` spies.
- **Steps:**
  1. Open `/settings/sessions`.
- **Expected:** No call to `/api/me/sessions` or any other endpoint from the tab.

### SSE-03 — Stale text: should point to the real sessions page

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No (it.fails: bug)
- **Preconditions / data:** `GET /api/me/sessions` → `{ items: [{ id: "s1", created_at: "2026-10-01T10:00:00Z", last_seen_at: "2026-10-19T09:00:00Z", expires_at: "2026-11-01T10:00:00Z", ip: "10.0.0.2", user_agent: "Firefox", current: true }] }`.
- **Steps:**
  1. Open `/settings/sessions`.
- **Expected (wanted):** the tab lists the sessions (or links to `/account/sessions`, which already does). **Current:** says the endpoint is not available, though `GET /api/me/sessions` exists and `/account/sessions` uses it (currently unhandled — likely bug: stale tab). When fixed, add cases for `items: []`, `items: null`, missing `items`, 401/500 and revoke.

### SSE-04 — Signed-out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full app; `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/settings/sessions`.
- **Expected:** Redirect to `/login` with `state.from = "/settings/sessions"`. No Settings content.

### SSE-05 — Deep link and Back/Forward to and from the tab

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** start at `/settings`.
- **Steps:**
  1. Click "Sessions"; then click "Appearance".
  2. Press Back, then Forward.
- **Expected:** Back shows the sessions text at `/settings/sessions`; Forward shows Appearance. Opening `/settings/sessions` directly shows the same panel.

### SSE-06 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full app with `AppShell`; start at `/settings`. Force a throw in the tab: mock `useI18n` so `t("settings.sessions.deferred")` throws `new Error("boom")` (other keys pass through).
- **Steps:**
  1. Click "Sessions".
  2. Press browser Back; then click a sidebar link.
- **Expected:** A route-level error fallback (not a blank page), shell still visible. Back renders the Account tab at `/settings` without a reload; the sidebar link renders its page.

### SSE-07 — Arabic: RTL and Arabic text

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`.
- **Steps:**
  1. Open `/settings/sessions`.
- **Expected:** `<html dir="rtl">`; text starts "يتطلب سرد الجلسات نقطة نهاية"; text aligned to the right.

### SSE-08 — Phone width: no horizontal scroll

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e at 375x812.
- **Steps:**
  1. Open `/settings/sessions`.
- **Expected:** The paragraph wraps; `scrollWidth <= innerWidth`.

### SSE-09 — Light and dark theme readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light, then dark.
- **Steps:**
  1. Open `/settings/sessions`.
- **Expected:** Muted text readable (contrast passes) in both themes.
