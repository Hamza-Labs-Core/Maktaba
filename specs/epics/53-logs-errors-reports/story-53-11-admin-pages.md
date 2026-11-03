# Story 53.11 — Admin Logs and Errors pages

> Epic 53 · Phase 2 · Depends on: 53.08, 53.09, 53.10 · Needed by: —

## Why

`/admin/logs` (`web/src/pages/Admin/Logs.tsx`) polls the rings and shows log files of a window.
With the index (53.09) and error groups (53.10), the admin can search, tail live, follow one
request through every component, and see what keeps failing, all from the web app, the Remote
apps and the desktop window.

## Description

### 1. Logs (`/admin/logs`, rebuilt)

- **Search bar:** words, phrases, `-word`; a reference (8 characters) or a full request id jumps
  to "Follow a request".
- **Filters:** component (multi), level (≥), machine, time (last 15 min, 1 h, 24 h, 7 d, 14 d,
  custom). All filters go in the address, as today (`?services=&level=&since=&until=&node=`), so
  other pages keep linking here (`update-attempts.md` does).
- **Live:** a toggle that switches the list to the SSE tail (53.09); pauses when the admin
  scrolls up; "N new lines" to jump back.
- **A line:** time, level colour, component, message, and the ids as chips (request, job, video,
  session, user name resolved from `user_h`). Expand shows every field and "Lines around this"
  (53.09 `/around`).
- **Follow a request:** every line with that request id from every component and machine, in
  time order, as a timeline (web → API → pipeline job → streaming), with gaps over 1 s marked.
- **Download:** the existing export, with the current filters (and `request_id`).
- **When the `logs` role is down:** a notice "Log search is restarting; showing recent lines from
  the files" and the 53.02 segment reader behind the same list (filters, no full text).

### 2. Errors (`/admin/errors`, new)

- A list of groups: title, component, count (24 h and total), a 14-day sparkline, users, first
  and last seen, status chip; filter by status (default: new + seen), component, time; sort by
  last seen, count, users.
- A group page: the chart, the samples (each opens its line in Logs and "Follow this request"),
  app versions and machines affected, the stack (own frames first), and actions **Mark seen**,
  **Resolve**, **Mute** (24 h, 7 days, until resolved).
- A badge on the admin navigation with the number of `new` groups (the "Needs you" pattern of
  Epic 40).

### 3. Settings (`/admin/logs/settings`)

Retention, disk caps, debug level per component for 1 h (the missing `POST /admin/log/level` of
Epic 21.1, done through the supervisor so it reaches every child, returning to `info` after the
hour).

### 4. Native admin apps

The Remote apps (Apple, Android) get **Errors** (list, group, status actions) and **Logs**
(search and follow a request; no live tail on phones) through the same API. The TV apps don't
(viewers only).

### 5. Roles

Every route is admin-only in the API (role tests); the navigation hides the pages from viewers,
but hiding is not the rule (owner, 2026-10-25). A home admin signed in through Maktaba Cloud gets
these only if granted manage, as today.

### 6. Strings (in addition to 53.10's)

| Key | English | Arabic |
|---|---|---|
| `logs.search` | Search logs | البحث في السجلات |
| `logs.live` | Live | مباشر |
| `logs.newLines` | {count} new lines | {count} سطرًا جديدًا |
| `logs.follow` | Follow this request | تتبّع هذا الطلب |
| `logs.around` | Lines around this | الأسطر المحيطة |
| `logs.filesOnly` | Log search is restarting; showing recent lines from the files. | البحث في السجلات يُعاد تشغيله؛ تُعرض الأسطر الأخيرة من الملفات. |
| `logs.unknownUser` | Unknown user | مستخدم غير معروف |
| `logs.settings.debug` | Detailed logs for one hour | سجلات مفصّلة لمدة ساعة |
| `nav.admin.errors` | Errors | الأخطاء |

## Acceptance criteria

- **Given** the admin types a reference from a user's error screen,
  **then** "Follow a request" opens with every line of that request across components.
- **Given** Live is on,
  **when** a warning is logged,
  **then** it appears in ≤ 2 s; scrolling up pauses and counts new lines.
- **Given** the `logs` role is down,
  **then** the page still lists recent lines from the files with the notice.
- **Given** a group is resolved on the Errors page,
  **then** its status changes, `audit_log` has the change, and the nav badge drops.
- **Given** "Detailed logs for one hour" for the pipeline,
  **then** pipeline debug lines appear and stop after an hour without anyone acting.
- **Given** a viewer opens `/admin/errors` by address,
  **then** the page shows "admins only" and the API answered 403.
- **Given** Arabic,
  **then** the pages are right to left, times in the Arabic locale; log lines themselves stay as written (left to right, isolated with `dir="ltr"`).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (vitest) | mocked API | search, filters in the URL, follow, around | right calls, rendering |
| T02 | unit | SSE mock | tail, pause, resume | counts right |
| T03 | unit | `logs` down (503) | open | files fallback with notice |
| T04 | unit | strings | en/ar parity | pass |
| T05 | role test (API) | debug-level route | nobody, viewer, admin | 401, 403, 200 |
| T06 | unit (Swift, Kotlin) | Remote Errors screens with mock | list, resolve | right calls; en/ar |
| T07 | UI (**CI only**) | web Playwright, dark theme | Logs and Errors in en and ar | screenshots, RTL |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/src/pages/Admin/Logs.tsx` (rewrite), `LogLine.tsx`, `FollowRequest.tsx`, `LogSettings.tsx` (new) | §1, §3. |
| `web/src/pages/Admin/Errors.tsx`, `ErrorGroup.tsx` (new); `web/src/serverRoutes.tsx`; `web/src/components/AppShell.tsx` | §2, nav and badge. |
| `web/src/lib/logFilters.ts` | New filters. |
| `api/internal/handlers/logs/`, `api/internal/handlers/errors/` | Level route (via the supervisor), user name resolution. |
| `cmd/maktaba-server/internal/supervisor/` | Set a child's level for 1 h (SIGUSR1 already toggles debug in Go; the pipeline gets the same signal handler). |
| `apps/apple/Remote/` (Errors, Logs screens), `apps/android/remote/` | §4. |
| `web/src/help/content/{en,ar}/logs.md`, `errors.md` | Help (Epic 41). |

### Tests

T01–T06 in unit jobs; T07 in CI UI jobs only.

### Rollout

Behind nothing: the pages replace the old one in one release; the old `/stream` route stays for a
release for older Remote apps.

### Rollback

Revert the pages; the API routes stay harmless.

### Dependencies

53.08, 53.09, 53.10.
