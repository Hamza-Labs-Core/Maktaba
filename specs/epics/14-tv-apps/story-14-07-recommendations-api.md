# Story 14.7 — API: recommendations endpoint

**Status:** **NEW** — added in response to
[REVIEW §3.2](../../REVIEW.md): the recommendations row
([Story 14.6](story-14-06-recommendations-ui.md)) referenced
`GET /api/recommendations` with no implementation owner. This story
owns it.

A nightly batch (or on-demand) recommender that produces 0–5 ranked
content rows per user using semantic similarity over watched videos,
followed-speaker heuristics, library affinity, and a cold-start fallback.

**Anchors:** [`architecture.md` §4.6 (semantic features)](../../architecture.md),
§9 (REST). Depends on Epic 1 Stories 5.1, 5.2 (embeddings); Epic 2
Story 9.10 (`media_features` table — also fills
[REVIEW §1.1.h](../../REVIEW.md)); Epic 7 Story 7.11 (`playback_state`).

## AC

### Schema

- New table `recommendation_runs`:
  - `user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE`
  - `computed_at TIMESTAMPTZ NOT NULL`
  - `expires_at TIMESTAMPTZ NOT NULL` (default `computed_at + 24h`)
  - `rows JSONB NOT NULL` — array of
    `{title, reason_kind, reason_args, item_ids: UUID[]}` (max 5 rows ×
    max 20 ids each)
- New table `recommendation_dismissals`:
  - `user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE`
  - `kind TEXT NOT NULL` (`row` | `item`)
  - `key TEXT NOT NULL` (e.g., `"reason:more_from_speaker:abc"` or
    `"item:def-uuid"`)
  - `dismissed_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `PRIMARY KEY (user_id, kind, key)`
- Migrations owned by this story.

### Endpoints

- `GET /api/recommendations` →
  ```
  200 {
    computed_at,
    expires_at,
    rows: [
      {
        title: string,            # localized server-side using user's locale
        reason_kind: "more_from_speaker" | "similar_to_video" |
                     "newly_added" | "editor_picks" | "library_recap" |
                     "speakers_you_follow",
        reason_args: { ... },     # kind-dependent
        items: [{video_id, poster_url, title, duration_sec, ...}]
      }
    ]
  }
  ```
  - Returns the cached row from `recommendation_runs` if not expired.
  - On cache miss, recomputes inline (≤ 1 s budget; otherwise returns
    the stale row and schedules an async refresh).
- `DELETE /api/recommendations/rows/{reason_kind}` and
  `DELETE /api/recommendations/items/{video_id}` write to
  `recommendation_dismissals`; the next computation excludes them.
- `POST /api/recommendations/refresh` (admin only) — force-recompute the
  caller's row and (optionally with `?user_id=<id>`) any user's row.

### Computation

- A nightly job (Epic 22 schedule, 03:00 server local time per user
  cluster) recomputes every active user's row.
- Inputs:
  - Last 30 days of `playback_state` rows where `position_sec >=
    duration_sec * 0.05` (i.e., "actually watched").
  - `media_features` embeddings for those videos.
  - Speakers whose videos appear ≥ 3 times in the watched set
    (heuristic for "followed").
  - Library membership (per-user library ACL).
- Output rows:
  - "Because you watched X" — top semantic neighbors of each of the
    user's last 5 watched videos, deduplicated and dismissals filtered.
  - "More from {speaker}" — videos by the heuristic-followed speaker
    not yet watched.
  - "Newly added in your favorite library" — most-watched library's
    last 7 days of additions.
  - "Editor's picks" — admin-curated set (separate `editor_picks` table
    out of v1 scope; falls back to "most-played overall" until that
    lands).
- Cold-start (no watch history): only "Newly added" and "Editor's picks"
  rows; documented client expectation.
- Determinism: same inputs → same output across runs (no random tie
  breaks; sort by `(score DESC, video_id ASC)` to stabilize).

### Performance & cost

- Per-user computation budget: ≤ 200 ms wall on a 100k-segment library;
  uses precomputed `media_features` embeddings (dot-product is cheap).
- The whole nightly batch is bounded: O(users × 5 watched × top-k
  neighbors). For 1k users this is < 60 s on a single CPU core.
- Cache TTL is 24 h; on-demand refresh is rate-limited to 1 per user per
  hour to discourage abuse.

### Security & privacy

- The endpoint requires authentication; never returns another user's
  recommendations.
- `reason_args` never includes raw watch history (just IDs that the user
  already has access to) — protects against side-channel inference if
  the caller's session is shared.
- Telemetry: opt-in click-through metrics ([Epic 16
  Story 16.5](../16-subscriptions/story-16-05-telemetry-opt-in.md));
  off by default.

## TC

- New user with zero watch history: `GET /api/recommendations` returns
  `rows = [{reason_kind: "newly_added", ...}, {reason_kind:
  "editor_picks", ...}]`.
- User with 10 sermons by speaker A: a "More from A" row appears with
  unwatched A videos.
- Dismiss "More from A": next call omits the row entirely.
- Force `expires_at < now()`: next call recomputes and returns within
  1 s.
- Recompute the same user twice with no input changes: byte-identical
  `rows` JSONB.

## EC

- A user whose entire watched set was deleted (videos purged): falls
  back to cold-start.
- A user with > 1k watched videos: only the last 30-day window is
  considered; deeper history is ignored.
- All candidate items are dismissed: returns an empty `rows: []`; client
  shows "No recommendations right now" empty state.
- The recommender depends on `media_features`, which is built by Epic 2
  Story 9.10. If that table is empty (fresh install), the endpoint
  returns only "Newly added" and "Editor's picks" with a 200 response —
  never an error.
