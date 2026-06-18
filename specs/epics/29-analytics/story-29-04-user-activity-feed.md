# Story 29.4 — User activity feed & privacy

> Epic 29 · Watch Analytics · Phase 4 (self-view + privacy)

## Description

A per-user timeline of the user's own activity, and a switch that pauses
all tracking.

- **Feed.** `GET /api/me/activity?limit=&offset=&types=` returns a
  merged, reverse-chronological timeline of the caller's activity:
  - **watched** — from `watch_sessions` (video, when, percent),
  - **searched** — from `search_history` (query, when),
  - **rated** — from the ratings surface if present (video, score,
    when); gracefully omitted when the ratings table is absent.

  `types` optionally filters (`watched,searched,rated`). Each item has a
  stable `{kind, at, …}` shape so the UI renders one timeline.
- **Privacy switch.** `GET /api/me/activity/settings` →
  `{track_enabled}`; `PUT /api/me/activity/settings {track_enabled}`
  upserts `user_analytics_prefs`. When `track_enabled=false`:
  - `POST /api/watch/start` writes nothing (Story 29.1),
  - search/rating logging is unaffected by this epic (owned elsewhere),
    but the activity feed still reflects only what was actually
    recorded.
  Re-enabling resumes collection going forward; it never back-fills.

## Acceptance criteria

- **Given** a user who has watched two videos and run one search,
  **when** they `GET /api/me/activity`,
  **then** three items are returned newest-first, each tagged with its
  `kind`.

- **Given** `?types=watched`,
  **when** the feed is fetched,
  **then** only `watched` items appear.

- **Given** a user toggles `track_enabled=false`,
  **when** they then start playback,
  **then** no new `watch_sessions` row is created, and
  `GET /api/me/activity/settings` reports `track_enabled=false`.

- **Given** the ratings table does not exist in this deployment,
  **when** the feed is fetched,
  **then** the endpoint still succeeds, simply omitting `rated` items.

## Notes

- The feed is owner-scoped; there is no admin variant here (admins use
  29.3/29.5).
- Settings live in the new `user_analytics_prefs` table (slot 0086),
  defaulting to `track_enabled=true` (absent row ⇒ tracking on).
