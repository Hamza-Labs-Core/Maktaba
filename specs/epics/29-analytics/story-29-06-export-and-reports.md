# Story 29.6 — Export, reports & retention

> Epic 29 · Watch Analytics · Phase 6 (export + lifecycle)

## Description

Admins can export the analytics data, and the system bounds its own
growth with a configurable retention window.

- **Export.** `GET /api/admin/analytics/export?format=csv|json&range=30d`
  streams the watch-session records in range. CSV emits a header row and
  one line per session (session id, user, video, start, end, duration,
  percent, state, device, platform, quality); JSON emits an array of the
  same. `Content-Disposition: attachment` with a dated filename. Admin
  only.
- **Retention.** A configurable window (`analytics.retention_days` in
  `app_settings`, default **365**) bounds the table. The reaper
  goroutine (Story 29.1) also runs a daily **purge** that deletes
  `watch_sessions` rows whose `started_at` is older than the window.
  `0` disables the purge (keep forever).
- **Weekly e-mail report (future).** Specced here, not implemented this
  batch: a scheduled job composes a per-week summary (top videos, total
  hours, new content watched) and e-mails admins. The **export endpoint
  is the data contract** that job will reuse; only the endpoint ships
  now.

## Acceptance criteria

- **Given** sessions in the last 30 days,
  **when** an admin calls `export?format=csv&range=30d`,
  **then** a CSV downloads with a header and one row per in-range
  session, and `format=json` returns the equivalent array.

- **Given** a non-admin principal,
  **when** the export endpoint is called,
  **then** it returns `403`.

- **Given** `analytics.retention_days=365` and sessions older than a
  year,
  **when** the daily purge runs,
  **then** rows older than 365 days are deleted and newer ones are kept.

- **Given** `analytics.retention_days=0`,
  **when** the purge runs,
  **then** nothing is deleted.

## Notes

- The purge and the interrupted-session reap share one goroutine with
  two cadences (reap: every minute; purge: once per ~24 h of ticks) so
  startup wires a single background loop (mirrors `runPairingSweep`).
- Export streams rather than buffering the whole result set, so a large
  range does not balloon memory.
- The CSV writer escapes per RFC 4180 (quotes, commas, newlines).
