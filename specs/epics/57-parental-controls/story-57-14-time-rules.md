# Story 57.14 — Time limits and time slots, kept by the home

> Epic 57 · Phase 2b · Depends on: 57.02, 57.03, 57.06 · Needed by: 57.15, 57.16, 57.13
> **Status:** built (checked against the code 2026-11-12). What differs from the text is corrected in place (§3 to §5).

## Why

Screen Time limits an app on Apple devices only. A child also watches on an Android tablet, a
browser and a TV, and a limit that one device keeps is passed by picking up another. The home is
the one place every client plays from, so the home counts the time and decides (README
decision 1).

## Description

### 1. Rules

A guardian or an admin sets rules for a restricted account. Each rule (`parental_time_rules`,
slot 0223) has:

- **days**: any of the seven, as a bitmask;
- **a time slot** (`slot_start`, `slot_end`, optional): watching is allowed only inside it. A
  slot may cross midnight;
- **a daily limit** in minutes (optional);
- **a library** (optional): the rule covers only that library. With no library it covers the
  account.

Several rules combine by the strictest: a moment is allowed only if every rule that covers it
allows it. A library rule can only narrow the account's rules. An account with no rules has no
time limit.

Which libraries a child's Player opens at all stays `library_acl` and the `library` rules of
57.02; the Family screen (57.07) shows them beside the time rules.

### 2. The clock and the count

- **The clock is the home's**, in the home's time zone (`app_settings` `home.timezone`, falling
  back to the server's zone). A device's clock is never read.
- **Usage** is counted by the home from playback it serves: each progress post and each stream
  heartbeat adds the seconds since the last one, capped at the heartbeat interval, to
  `parental_time_usage` for that account, day and library. Paused time is not counted. Live TV
  counts the same way.
- Two devices playing at once both count.
- A day ends at 04:00 home time, so a late evening is one day.

### 3. Enforcement

The time keeper (`api/internal/parental/timekeeper.go`) answers `allowed`, `remaining` and
`reason` (`outside_slot`, `limit_reached`). It is asked at the four places a stream session
passes through the API:

- `POST /api/stream/sessions`: before a session is opened, and again each time a client opens
  one again for fresh URLs. There is no separate renewal route;
- once the session has its id, it gets a **lease** of 60 seconds on the streaming service;
- `POST /api/stream/sessions/{id}/progress`: the stream's heartbeat. It counts the time since
  the call before (never more than 60 seconds) and renews the lease. A client posts it at least
  every 60 seconds;
- `DELETE /api/stream/sessions/{id}`: counted up to the close, lease ended.

The streaming service serves a kept account's segments only under that lease and up to the end
time the API pushed (no database read per segment). Every stream URL carries a signed `tk`
claim (`1`: this account's time is kept; `0`: it is not), and a kept account's URL also carries
`tls`, the stream session whose lease it needs. A URL that says neither is refused, so missing
state never means unlimited.

When time is up: no new stream starts, and a running one ends within 60 seconds. The API
answers `403` with problem type `parental-time` (`code: parental_time`), carrying `reason`
(`outside_slot`, `limit_reached`, or `no_heartbeat` when no counted call came for the session
within 60 seconds) and, when the rules allow watching again, `next_allowed_at`. When the time
cannot be read or counted the answer is `503` with `Retry-After`: never free time on an error.
This is not a 404: the child is meant to know. Browsing stays open, so a child can still look
and ask. A client stops on the refusal and does not ask again by itself.

`GET /api/me/parental/time` gives the child's own state (`allowed`, `limited`, `reason`,
`remaining_seconds`, `slot_ends_at`, `next_allowed_at`, `now`); clients use it for the countdown
(57.15). It only reads: it counts nothing and keeps no stream running.

### 4. More time

Ask a parent (57.06) gains the subject `time`: `subject_kind` is `time` and `subject_id` is the
minutes as text, a whole number from 1 to 480 (the apps offer 15, 30 and 60). A guardian
approves in their Player or on the web (`POST /api/parental/requests/{id}/approve`, with an
optional `minutes`; the answer is `{request, grant}`), or types the parent PIN on the child's
device (`POST /api/me/parental/unlock` with kind `time`; the answer is `{grant}`). A guardian
can also give time unasked: `POST /api/parental/users/{id}/time-grants {minutes}` (1 to 480,
201). The grant (`parental_time_grants`) adds minutes to that day only and may also open a
closed slot until the minutes run out. A running or refused stream learns of a grant at once.

### 5. Routes and roles

| Route | Who |
|---|---|
| `GET/PUT /api/parental/users/{id}/time-rules` | admin; a guardian of that child |
| `GET /api/parental/users/{id}/time-usage?from&to` | admin; a guardian of that child |
| `POST /api/parental/users/{id}/time-grants` | admin; a guardian of that child |
| `GET /api/me/parental/time` | the signed-in user |

A viewer and the child get 403 on the first three; another child's guardian gets 404, as on
the other routes of a child. An account that is not restricted is 404. A play-only sign-in through
Maktaba Cloud reads and does not write. The role table of 57.13 holds each cell.

## Acceptance criteria

- **Given** a rule of 60 minutes a day, **when** the child has watched 60 minutes on an iPad,
  **then** a new stream on an Android tablet answers `parental-time` with `limit_reached`.
- **Given** a slot of 07:00 to 20:00, **when** a stream is running at 20:00 home time, **then** it
  ends by 20:01 and its position is saved.
- **Given** a library rule of 30 minutes inside an account rule of 120, **then** that library
  stops at 30 and the others go on to 120.
- **Given** a device clock set back two hours, **then** nothing changes.
- **Given** a 30 minute grant after the limit, **then** playback works for 30 more minutes that day.
- **Given** a viewer or the child, **when** they change the rules, **then** 403; **given** another
  child's guardian, **then** 404.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | rule sets and clock times, with midnight slots and the 04:00 day line | `Time` | allowed, remaining, reason |
| T02 | Go Postgres | heartbeats from two sessions | count | usage sums both; paused adds none |
| T03 | Go Postgres | limit reached | open a session, open it again, post progress | `parental-time` |
| T04 | streaming Go test | session with an end time | segments after it | refused within 60 s |
| T05 | Go Postgres | grant | play | allowed for the granted minutes |
| T06 | Go role table | eight callers | each route | 200, 403 or 404 as the table says |
| T07 | Go Postgres and SQLite | migrations 0223 up and down | — | clean |

## Edge cases

- **The home restarts mid-day**: usage is in the database, so the count goes on.
- **A relay sign-in**: the same check runs, since the relay forwards to the home's API.
- **Home time zone changed**: the current day's count stays; the new zone applies from the next
  day line.
- **An unrestricted account**: `Time` returns allowed at once, with no query.

## Build plan

| File | Change |
|---|---|
| `api/internal/parental/time.go` (new), tests | rules, clock, count, grants |
| `api/internal/handlers/parental/time.go` (new) | routes |
| `streaming/internal/…` session record | end time, pushed by the API |
| `shared/db/migrations/0223_parental_time{,.sqlite}.sql`, `MANIFEST.md` | tables |
| `shared/api/openapi.yaml`, `openapi.ar.json` | routes, English and Arabic |
