# Implementation Plan — Story 29.6 Export, reports & retention

> Companion to [story-29-06-export-and-reports.md](story-29-06-export-and-reports.md).

## 0. Placement

- Export: `api/internal/handlers/analytics/export.go`, route
  `GET /api/admin/analytics/export` in `p29.go` (admin-gated).
- Retention purge: folded into `watch.Reaper` (Story 29.1 plan §5).

## 1. Export handler

- Params: `format` (`csv`|`json`, default `csv`), `range` (reuse
  `ParseRange`).
- Query: `SELECT id, user_id, video_id, started_at, ended_at,
  duration_sec, percent_complete, state, device_type, platform, quality
  FROM watch_sessions WHERE started_at >= $start ORDER BY started_at`
  — streamed with `rows.Next()`, never fully buffered.
- CSV: `encoding/csv` writer to `w` after setting
  `Content-Type: text/csv` and `Content-Disposition: attachment;
  filename="maktaba-analytics-<label>-<date>.csv"`. Header row then one
  record per session. `encoding/csv` handles RFC 4180 escaping.
- JSON: stream a `[`, comma-separated marshalled rows, `]` — or, given
  bounded self-hosted scale, encode a slice; the streaming shape is
  preferred to keep memory flat.
- `403` unless `p.IsAdmin`.

## 2. Retention purge

In `watch.Reaper.Run` (single goroutine, two cadences):

```
every 1m   → reapStale(now - staleTimeout)      // Story 29.1
every ~24h → if retentionDays>0:
                purgeOlderThan(now - retentionDays*24h)
```

`retentionDays` resolved at purge time from
`app_settings['analytics.retention_days']` (JSON number), falling back to
`MAKTABA_ANALYTICS_RETENTION_DAYS` env, then the `365` default. `0`
disables. The purge is `DELETE … WHERE started_at < $cutoff`, covered by
`watch_sessions_started_idx`, batched if a count threshold is exceeded
(LIMIT-loop) to avoid a long lock on first run after a big backlog.

## 3. Weekly e-mail report (future — spec only)

Not built this batch. The future job:
1. runs on a weekly cron,
2. calls the same aggregate repo used by 29.3 (`summary`, `top-videos`)
   for the trailing 7 days,
3. renders an HTML/text digest, and
4. sends to admin addresses via the platform mailer.

The **export endpoint + the analytics repo are the stable contract** it
will reuse; nothing here blocks it.

## 4. Tests

- CSV record shaping (pure): a `sessionRow` → `[]string` record, with a
  value containing a comma/quote/newline round-tripping through
  `encoding/csv` correctly.
- Retention cutoff math: `retentionDays=0` ⇒ skip; `>0` ⇒ correct
  cutoff.
