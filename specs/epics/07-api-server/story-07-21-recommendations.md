# Story 7.21 — Recommendations endpoint

`GET /api/recommendations` from the clients epic (Epic 14.6 TV
"Continue Watching" + "For You" rails). Owns the API-side
implementation that REVIEW.md §3.2 flagged as a high-impact gap.

**AC-1 — Personalized rails.**
- **Given** an authenticated user with watch history,
- **When** `GET /api/recommendations?surface=tv-home&limit=20` is called,
- **Then** the response is
  ```
  {
    rails: [
      {id: "continue", title: "Continue Watching",
       items: [{video_id, position_sec, duration_sec, last_watched_at, poster_url}]},
      {id: "next-up",  title: "Next Up", items: [...]},
      {id: "for-you",  title: "For You", items: [...]},
      {id: "library", title: "By Topic — <topic>", items: [...]}
    ],
    generated_at,
    cache_hit
  }
  ```
  Each rail's items are filtered to videos the user has access to via
  Epic 10's permission check.

**AC-2 — Rail composition.**
- `continue`: `playback_state` rows for the user where
  `0.05 ≤ position_sec/duration_sec ≤ 0.95`, ordered by
  `updated_at DESC` (requires the `playback_state(user_id, updated_at)`
  index introduced by this story).
- `next-up`: from `continue`, when a watched video is part of a manual
  collection (Story 9.13), the next unwatched item in the collection.
- `for-you`: top-K nearest videos to the user's mean watched-segment
  embedding, computed nightly into a `user_recs (user_id, video_id,
  score, computed_at)` table; this story owns the schema and the
  generation job (a daily Pipeline-driven aggregation).
- `library`: per `video_topics` (Epic 9 Story 9.9) — pick the user's
  top-3 topics by watch time, then surface the most-watched-by-others
  videos under each topic the user hasn't seen.

**AC-3 — Caching.**
- **Given** any user,
- **When** the endpoint is hit more than once within
  `recs_cache_ttl_sec` (default 60 s),
- **Then** subsequent responses are served from a per-user in-memory
  cache; `cache_hit: true` is set in the response.

**AC-4 — `surface` parameter.**
- **Given** `?surface=mobile-home`, the response omits the `library` rail
  (mobile UI doesn't render it).
- **Given** `?surface=tv-home`, all rails are included.
- **Given** any unknown `surface`, defaults to `web-home` (all rails,
  ordered identically).

**AC-5 — `user_recs` schema.**
- The `user_recs` table is owned by this story:
  ```
  CREATE TABLE user_recs (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id     UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    rail_kind    TEXT NOT NULL,             -- 'for-you' | 'library' | ...
    score        REAL NOT NULL,
    computed_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, video_id, rail_kind)
  );
  CREATE INDEX user_recs_lookup ON user_recs (user_id, rail_kind, score DESC);
  ```

**Test cases:**
- Unit: `continue` rail excludes a video at `position_sec/duration_sec >
  0.95`.
- Integration: a user with no watch history gets only `for-you` and
  `library` rails populated; `continue` and `next-up` are empty arrays.
- Integration: cache TTL respected — second call within 60 s returns
  `cache_hit: true`.
- Integration: an admin viewing a different user via
  `?as_user_id=<uuid>` returns the target user's recs (admin-only;
  non-admin gets 403).

**Edge cases:**
- A new user (no `user_recs` row yet) — the `for-you` rail returns an
  empty array; the UI falls back to the `library` rail (which is purely
  catalog-driven). Test case: brand-new user → 200 with empty for-you.
- A video that was in a recommendation but has since been deleted —
  filtered out at request time via FK existence check (no 404 leak).
- A user whose access was revoked from a library overnight — the
  recommendation list filters out videos in revoked libraries.
