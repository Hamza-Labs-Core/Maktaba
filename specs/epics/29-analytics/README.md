# Epic 29 — Watch Analytics & Activity Tracking

> **Status:** spec + implementation (batch 1). **Source:** `specs/epics/29-analytics/`.
> **Anchors:** [`architecture.md` §9 (API)](../../architecture.md#9-api),
> Epic 7 (playback / resume — `playback_state`, slot 0038),
> Epic 26 (content-intelligence tags — the genre source),
> Epic 27 (live channels — `streaming_sessions`, the live-session source).

## Goal

Maktaba is a **multi-user** media server. An operator running it for a
family, a class, or a small organisation has no way to answer the
questions every such operator eventually asks: *what is being watched,
by whom, when, and how far through?* Epic 29 builds that visibility in —
"Tautulli for Plex, but built in" — without bolting on a second
analytics service.

The epic introduces one new fact table — **`watch_sessions`** (slot
0086): one row per play session, opened on play, advanced by a periodic
**heartbeat**, and closed on stop (or reaped as *interrupted* when a
client vanishes). Everything else is a read over that table joined to
the existing schema:

1. **Track every session** (29.1) — start/heartbeat/stop API, a
   30-second heartbeat from every client, and a reaper that closes
   abandoned sessions so "currently watching" never lies.
2. **Per-user history** (29.2) — paginated, date-filterable, with the
   resume position sourced from the existing `playback_state` so the
   "Continue Watching" rail and history agree.
3. **Admin dashboard** (29.3) — live sessions, most-watched, most-active
   users, watch-time-over-time, popular genres, a peak-hours heatmap,
   and device/library breakdowns.
4. **Activity feed** (29.4) — a per-user timeline (watches + searches +
   ratings) and a **privacy switch** that pauses tracking entirely.
5. **Per-video stats** (29.5) — views, unique viewers, average
   completion, average watch duration; admins additionally get the
   per-user breakdown.
6. **Export & retention** (29.6) — CSV/JSON export for admins, a
   configurable retention window (default 1 year) with an auto-purge,
   and a spec for the future weekly e-mail report.

## Design decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | **`watch_sessions` is a new append table, *not* an extension of `streaming_sessions` or `playback_state`.** | The three answer different questions: `streaming_sessions` is the transcode lifecycle (slot 0039), `playback_state` is the *current* resume point per (user, video) (slot 0038), and `watch_sessions` is the *historical log* of every play. Analytics needs the log; resume needs the point. Keeping them separate avoids overloading a hot single-row table with append traffic. |
| D2 | **Resume position is read from `playback_state`, not re-derived from sessions.** | "Continue Watching" (Epic 7/14, slot 0066 partial index) already owns the resume point and the cross-device guarantee. History (29.2) joins to it rather than computing a max over sessions, so the two surfaces never disagree. |
| D3 | **The heartbeat is the source of truth for "watched", not wall-clock.** | A client that plays for 2 minutes then pauses for an hour watched 2 minutes. `duration_sec` accumulates only across live heartbeats; the reaper closes the gap, it never credits it. |
| D4 | **`ip_addr_hash`, never a raw IP.** | The session row needs to distinguish devices/locations for the dashboard without becoming a PII liability on a self-hosted box. We store a salted SHA-256 of the client IP (truncated), never the address. |
| D5 | **A per-user `track_enabled` switch gates *writes* at the start endpoint.** | Privacy (29.4) must be honoured at the point of collection — an opted-out user produces no `watch_sessions` rows at all, so there is nothing to leak, redact, or purge later. |
| D6 | **Aggregations are computed on read, cached briefly in-memory.** | At self-hosted scale (hundreds of users, not millions) a covering-indexed `GROUP BY` over `watch_sessions` is cheap; a 30–60 s in-memory cache on the dashboard summary absorbs refresh spam without a materialised-view maintenance burden. The retention purge keeps the table bounded. |
| D7 | **Genres come from Epic 26 tags (`video_tags`/`tags`), not a new taxonomy.** | The content-intelligence pipeline already tags videos; the "popular genres" card is a join, not a new column. |
| D8 | **Interrupted ≠ stopped.** | A session with no heartbeat for 5 minutes is marked `interrupted` (distinct from `completed`/`stopped`) so the dashboard can show abandonment honestly and the live view drops it promptly. |

## Stories

| Story | Title | Surface |
|---|---|---|
| [29.1](story-29-01-watch-session-tracking.md) | Watch session tracking | `POST /api/watch/{start,heartbeat,stop}`; slot 0086; reaper |
| [29.2](story-29-02-watch-history.md) | Watch history | `GET/DELETE /api/me/history…` |
| [29.3](story-29-03-analytics-dashboard.md) | Analytics dashboard (admin) | `GET /api/admin/analytics/*`; `Admin/Analytics.tsx` |
| [29.4](story-29-04-user-activity-feed.md) | User activity feed + privacy | `GET /api/me/activity`; settings toggle |
| [29.5](story-29-05-playback-statistics.md) | Playback statistics per video | `GET /api/videos/{id}/stats` |
| [29.6](story-29-06-export-and-reports.md) | Export, reports & retention | `GET /api/admin/analytics/export`; purge |

## Data model (slot 0086)

```
watch_sessions
  id              uuid pk
  user_id         uuid → users(id)            on delete cascade
  video_id        uuid → videos(id)           on delete cascade
  started_at      timestamptz not null
  ended_at        timestamptz                 null until stop/reap
  last_heartbeat  timestamptz not null        advanced by heartbeat
  duration_sec    integer not null default 0  accumulated watched time
  percent_complete real   not null default 0  0..100
  state           text    not null            active|completed|stopped|interrupted
  device_type     text                        web|desktop|mobile|tv|other
  platform        text                        ios|android|macos|windows|linux|web|…
  quality         text                        e.g. 1080p / direct / 720p
  ip_addr_hash    text                        salted, truncated sha-256

user_analytics_prefs
  user_id      uuid pk → users(id) on delete cascade
  track_enabled boolean not null default true
  updated_at    timestamptz not null
```

Indexes (parity in `.sqlite.sql`): `(user_id, started_at DESC)` for
history; partial `(state) WHERE state='active'` for the live view +
reaper scan; `(video_id)` for per-video stats; `(started_at)` for the
time-series + retention purge.

## Out of scope (this batch)

- Weekly e-mail reports are **specced** (29.6) but only the export
  endpoint ships; the scheduler/mailer is a follow-up slot.
- No per-second telemetry: the heartbeat granularity is 30 s by design.
- No cross-server roll-up (single home server only).
