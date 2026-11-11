# Story 57.08 — Activity for guardians

> Epic 57 · Phase 2 (API), 3 (iOS) · Depends on: 57.02, 57.06, 57.07 · Needed by: 57.13

## Why

Screen Time reports how long each app was used, not what was watched in it. A parent wants to see
what their child watched, what was blocked and what they asked for, without that record leaving
the home, and without keeping more than is needed.

## Description

### 1. What is recorded

`parental_events` (slot 0222), only for restricted accounts:

| Kind | When | Subject |
|---|---|---|
| `watched` | a watch session reaches 60 s or 20 % (from `watch_sessions`, not a second tracker) | video |
| `blocked` | a by-ID request answered `NotFound` because of the filter (one per subject per day) | the subject asked for |
| `asked`, `approved`, `denied` | 57.06 | the subject |
| `pin_locked` | 57.06 | the guardian whose PIN locked |
| `signal` | 57.04: a looser device signal waiting | the sign-in's device name |

Not recorded: search terms, browsing, position within a video, IP addresses. For accounts with
only a device signal (no policy), nothing is recorded.

### 2. Reading it

`GET /api/users/{id}/parental/activity?cursor=&kind=` (admin, guardian of the child): newest first,
50 per page, each with the subject's title, poster and tier as the guardian may see them. A
subject deleted since shows "Removed".

### 3. Keeping it

Kept 90 days; the API's maintenance loop deletes older rows daily. Deleting the child's account or
lifting the restriction deletes their events (lifting asks the admin first, and says so).
Activity is never copied to Maktaba Cloud, never in crash reports or problem reports (the redact
rules drop `parental_events` content), and never in analytics (Epic 29's usage data is not fed
from it).

### 4. In the Player

Family > child > **Activity**: a list grouped by day ("Today", "Yesterday", dates in the app's
calendar and digits), each row an icon for the kind, the title, the time. A filter row: All,
Watched, Blocked, Requests.

## Acceptance criteria

- **Given** a child watches 2 minutes of a video, **then** one `watched` event appears in the
  guardian's Activity.
- **Given** the child opens a blocked link three times in a day, **then** one `blocked` event.
- **Given** events 91 days old, **when** maintenance runs, **then** they are gone.
- **Given** another guardian or a viewer, **then** 404 and 403.
- **Given** a problem report from the child's device, **then** it holds no activity.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go + Postgres | watch session 60 s / 10 s | record | one event / none |
| T02 | Go unit | 3 blocked hits, one day | record | one event |
| T03 | Go unit (fake clock) | 91-day-old rows | maintenance | deleted |
| T04 | Go role test | six principals | activity route | 57.02's table |
| T05 | Go unit | unrestricted signal-only account | watch | nothing recorded |
| T06 | MaktabaKit XCTest | events across days, Arabic locale | group | day headers in Arabic digits and calendar |
| T07 | Player UI test (iPhone sim, ar) | `MockHome` activity | open, filter | mirrored; filters work |

## Strings

| Key | English | Arabic |
|---|---|---|
| `activity.title` | Activity | النشاط |
| `activity.filter.all` | All | الكل |
| `activity.filter.watched` | Watched | شوهد |
| `activity.filter.blocked` | Blocked | محظور |
| `activity.filter.requests` | Requests | الطلبات |
| `activity.kind.watched` | Watched %@ | شاهد %@ |
| `activity.kind.blocked` | Tried to open something blocked | حاول فتح محتوى محظور |
| `activity.kind.asked` | Asked for %@ | طلب %@ |
| `activity.kind.approved` | %1$@ allowed %2$@ | سمح %1$@ بـ %2$@ |
| `activity.kind.denied` | %1$@ said no to %2$@ | رفض %1$@ %2$@ |
| `activity.kind.pinLocked` | %@'s PIN locked after wrong tries | قُفل رمز %@ بعد محاولات خاطئة |
| `activity.kind.signal` | %@ asks for a looser age limit | يطلب %@ حدًّا عمريًّا أوسع |
| `activity.removed` | Removed | محذوف |
| `activity.kept` | Kept on your home for 90 days. | يُحفظ على جهازك الرئيسي مدة 90 يومًا. |

## Edge cases

- **A blocked subject's title** is shown to the guardian (they may see it), never to the child.
- **Time zones**: days follow the home's time zone, as the rest of the Player's history does.

## Build plan

| File | Change |
|---|---|
| `api/internal/parental/events.go` (new), tests | record, read, sweep |
| `api/internal/handlers/watch/` | `watched` events |
| `api/internal/handlers/parental/activity.go` (new) | route |
| `api/main.go` maintenance loop | daily sweep |
| `apps/apple/Player/Family/ActivityView.swift` (new), `Family.xcstrings` | screen |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/RedactRules.swift`, Go log redaction | drop activity fields |
