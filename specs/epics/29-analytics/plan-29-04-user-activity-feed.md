# Implementation Plan — Story 29.4 User activity feed & privacy

> Companion to [story-29-04-user-activity-feed.md](story-29-04-user-activity-feed.md).

## 0. Placement

`api/internal/handlers/watch/activity.go` (same package, owner-scoped):

```
GET /api/me/activity?limit=&offset=&types=
GET /api/me/activity/settings
PUT /api/me/activity/settings   {track_enabled}
```

## 1. Feed assembly

Each source is a small query returning a common `ActivityItem{Kind, At
time.Time, …}`:

- **watched** — `SELECT video_id, started_at, percent_complete FROM
  watch_sessions WHERE user_id=$1 ORDER BY started_at DESC LIMIT n`.
- **searched** — `SELECT query, last_used_at FROM search_history WHERE
  user_id=$1 ORDER BY last_used_at DESC LIMIT n`.
- **rated** — guarded: probe for a ratings table; if absent, skip
  (the feed degrades, never errors — AC). Detection via a
  `to_regclass`/`sqlite_master` check cached per-process.

Merge the three slices, sort by `At` desc, apply `limit`/`offset` in Go.
`types` filters which sources run. Each kind carries its specifics
(`video_id`+`percent`, `query`, `score`) under a `meta` field so the UI
has one shape.

> Cap each source fetch at `offset+limit` rows so the in-Go merge stays
> bounded regardless of history size.

## 2. Privacy settings

`user_analytics_prefs` upsert:

```sql
INSERT INTO user_analytics_prefs (user_id, track_enabled, updated_at)
VALUES ($1,$2,$3)
ON CONFLICT (user_id) DO UPDATE SET track_enabled=EXCLUDED.track_enabled,
                                    updated_at=EXCLUDED.updated_at;
```

`GET` returns `{track_enabled}` (absent row ⇒ `true`). The
`watch.Start` handler already consults `trackingEnabled` (Story 29.1).

## 3. Web

- Settings gains a **"Watch history & privacy"** section
  (`web/src/pages/Settings/PrivacySection.tsx` or inline in
  `Settings.tsx`): a `Toggle` bound to `track_enabled`, and a link to the
  user's history. Copy via i18n (`settings.privacy.*`).
- Optional: a compact activity list on the profile/account page reusing
  `analyticsApi.activity` — minimally, the settings section explains and
  links.

## 4. Tests

- Merge+sort pure function: given the three typed slices, the merged
  output is newest-first and `types`-filtered.
- `track_enabled` absent-row default resolves to true.
