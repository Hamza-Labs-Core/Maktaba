# Story 53.10 — Error groups

> Epic 53 · Phase 2 · Depends on: 53.09 · Needed by: 53.11, 53.14, 53.17

## Why

The diagnostics bundle already dedups errors into `error-summary.json` (`api/internal/handlers/logs/helpers.go`)
because "a retry storm drowns the signal". The cloud has error groups for anonymous usage data
(`telemetry_error_groups`). The home has neither as something an admin can look at: the same
failure 10,000 times is 10,000 lines.

## Description

### 1. What makes a group

Every `error` and `fatal` line, and every crash (53.14), gets a **fingerprint** (README D8):

`fp = sha256(source | service | err.code or msg | scrubbed err.msg with numbers, ids and quoted values replaced | first own frame)[:16]`

- `source` is `server` or `app`, as in the cloud's table; the scrubbing is the cloud's
  `cloud/internal/telemetry/scrub.go` rules, moved to `shared/redact` so the home and the cloud
  compute the same fingerprint.
- Lines with the same `fp` are one group; `warn` lines are not grouped (they are searchable).

### 2. Tables (in `logs.db`)

```sql
CREATE TABLE error_groups (
  fp TEXT PRIMARY KEY, source TEXT, service TEXT, code TEXT,
  title TEXT,                         -- the first scrubbed message
  first_seen INTEGER, last_seen INTEGER,
  count INTEGER, count_24h INTEGER,   -- count_24h refreshed every minute from hourly buckets
  users INTEGER,                      -- distinct user_h, capped at 1,000
  status TEXT CHECK (status IN ('new','seen','resolved','muted')),
  resolved_in TEXT,                   -- version at resolve
  muted_until INTEGER,
  last_request_id TEXT, sample_line_ids TEXT  -- up to 20 recent lines (id list)
);
CREATE TABLE error_hours (fp TEXT, hour INTEGER, count INTEGER, PRIMARY KEY (fp, hour));
```

- A new group is visible ≤ 10 s after its first line.
- **Status:** `new` → `seen` when an admin opens it; `resolved` by an admin (records the current
  version); a resolved group that happens again **on a later version** goes back to `new` and is
  marked "came back"; `muted` for 24 h, 7 days or until resolved.
- Retention: groups not seen for **30 days** (owner's decision, 2026-11-03; admin-adjustable,
  53.11) are deleted with their hours; hourly counts older than 30 days are dropped; samples point at lines
  that may have aged out (the UI says "line no longer kept").

### 3. Notifications

A group that is **new** and has ≥ 3 occurrences in 10 minutes, or **came back**, raises one
admin notification (existing `notifications`), at most one per group per day, at most 10 a day
in total ("and N more" in the last one). Muted groups never notify.

### 4. API

Admin only: `GET /api/admin/errors?status=&service=&since=&q=`, `GET /api/admin/errors/{fp}`
(the group, its hourly chart for 30 days, samples with their lines, affected app versions and
users count), `POST /api/admin/errors/{fp}/status` (`seen|resolved|muted`, `muted_until`). Each
status change is written to `audit_log`.

### 5. Strings

| Key | English | Arabic |
|---|---|---|
| `errors.title` | Errors | الأخطاء |
| `errors.status.new` | New | جديد |
| `errors.status.seen` | Seen | تمت رؤيته |
| `errors.status.resolved` | Resolved | تم الحل |
| `errors.status.muted` | Muted | مكتوم |
| `errors.cameBack` | Came back in {version} | عاد في الإصدار {version} |
| `errors.firstSeen` | First seen {time} | أول ظهور {time} |
| `errors.lastSeen` | Last seen {time} | آخر ظهور {time} |
| `errors.count` | {count} times | {count} مرة |
| `errors.notify.new` | A new error in {component}: {title} | خطأ جديد في {component}: {title} |

## Acceptance criteria

- **Given** the same failure logged 10,000 times with different video ids,
  **then** there is one group with count 10,000, first and last seen, and up to 20 samples.
- **Given** a group resolved on 0.3.44,
  **when** it happens on 0.3.45,
  **then** it is `new` again, marked "came back in 0.3.45", and admins get one notification.
- **Given** it happens again on 0.3.44 (a machine not yet updated),
  **then** it stays resolved and its count rises.
- **Given** a muted group,
  **then** no notification, whatever happens.
- **Given** the same error at the home and in the cloud's anonymous data,
  **then** both have the same fingerprint.
- **Given** a viewer,
  **when** they call any `/api/admin/errors` route,
  **then** 403.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | messages differing in ids, numbers, quoted values | fingerprint | equal |
| T02 | unit (Go home vs cloud) | shared cases | fingerprint | equal in both |
| T03 | unit | status transitions incl. version compare | events | as §2 |
| T04 | unit | notification limits | 30 new groups | 10 notifications, last says "and 20 more" |
| T05 | role test | nobody, viewer, admin | routes | 401, 403, 200 |
| T06 | integration | ingest stream | first line | group visible ≤ 10 s |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/redact/go/fingerprint.go` (new; moved from `cloud/internal/telemetry/scrub.go`) | §1. |
| `cmd/maktaba-server/internal/logindex/errors.go` (new) | Grouping at ingest, hours, status, retention. |
| `api/internal/handlers/errors/` (new) | §4, audit. |
| `api/internal/notify/` | §3. |
| `cloud/internal/telemetry/scrub.go` | Use the shared fingerprint. |

### Tests

T01–T06 in unit and integration jobs.

### Rollout

Grouping starts with the release; old lines are not grouped back (the index may group the last
7 days once at first start, in the background).

### Rollback

Revert; the tables stay unused in `logs.db`.

### Dependencies

53.09. Needed by 53.11, 53.14, 53.17.
