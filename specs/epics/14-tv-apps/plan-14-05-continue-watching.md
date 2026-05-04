# Implementation Plan — Story 14.5 Continue Watching row

> Companion to [story-14-05-continue-watching.md](story-14-05-continue-watching.md).
> The story states *what* and *why*; this plan states *how*.
> Schema reference: `playback_state` from Epic 7 Story 7.11; this story
> owns the **partial covering index** that makes the cross-device 5 s
> guarantee feasible (resolves [REVIEW §6.3](../../REVIEW.md)).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0040_playback_state_continue_idx.sql` (Postgres) and `0040_playback_state_continue_idx.sqlite.sql` (SQLite). Numbering picked to follow the existing pipeline range and Epic 7's `playback_state` table from `0030_playback_state.sql`. |
| Server query | New sqlc query `GetContinueWatching(user_id, limit)` in `shared/db/queries/playback_state.sql`. |
| GraphQL field | `continueWatching(limit: Int = 20): [VideoProgress!]!` on `Query`, in `shared/graphql/schema.graphql`. |
| TV row composition | tvOS in `apps/tvos/Sources/Features/Home/ContinueRow.swift`; AndroidTV in `apps/androidtv/.../home/ContinueRow.kt`. Reuses card primitives from [Story 14.3](story-14-03-10-foot-ui.md). |
| Live updates | Subscribes to `playback.changed` WS events from Epic 7 Story 7.16 and patches the row in place. |
| Out of scope | Top Shelf / Recommendations Channel surfacing (already covered by [Story 14.1](story-14-01-tvos.md) / [Story 14.2](story-14-02-android-tv.md)); the ranking algorithm beyond `ORDER BY updated_at DESC`. |

## 1. Database migration

`shared/db/migrations/0040_playback_state_continue_idx.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
-- Story 14.5: a partial covering index over playback_state for the
-- "Continue Watching" row. Partial because we only ever query the
-- in-progress slice (5%–95% of duration); covering because the row
-- card needs (video_id, position_sec, duration_sec, updated_at) and
-- a covering index keeps the query an index-only scan.
CREATE INDEX playback_state_user_updated_idx
    ON playback_state (user_id, updated_at DESC)
    INCLUDE (video_id, position_sec, duration_sec)
    WHERE position_sec >= duration_sec * 0.05
      AND position_sec <  duration_sec * 0.95;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS playback_state_user_updated_idx;
-- +goose StatementEnd
```

SQLite variant (no `INCLUDE`, no partial-index-with-expression on older versions; we use a partial index without INCLUDE — SQLite stores the rowid, so a small extra fetch is acceptable for the development/test path that uses SQLite):

```sql
-- +goose Up
-- +goose StatementBegin
CREATE INDEX playback_state_user_updated_idx
    ON playback_state (user_id, updated_at DESC)
    WHERE position_sec >= duration_sec * 0.05
      AND position_sec <  duration_sec * 0.95;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS playback_state_user_updated_idx;
-- +goose StatementEnd
```

Both forms ensure `EXPLAIN` on the row's query never reports a `Seq Scan` once 100k rows exist (TC). The migration test asserts this.

## 2. sqlc query

`shared/db/queries/playback_state.sql` (additions):

```sql
-- name: GetContinueWatching :many
SELECT
    ps.video_id,
    ps.position_sec,
    ps.duration_sec,
    ps.updated_at,
    v.title,
    v.poster_url,
    v.deleted_at
FROM playback_state ps
INNER JOIN videos v ON v.id = ps.video_id
WHERE ps.user_id = $1
  AND ps.position_sec >= ps.duration_sec * 0.05
  AND ps.position_sec <  ps.duration_sec * 0.95
  AND ps.duration_sec > 0
  AND v.deleted_at IS NULL
ORDER BY ps.updated_at DESC
LIMIT $2;
```

Notes:
- Filters out `duration_sec = 0` rows (probe pending — EC).
- Joins `videos` to filter deleted entries (EC).
- The cap of 20 (story AC) is the default `$2` from the GraphQL resolver.

## 3. Deduplication of "same video in two collections"

The story EC says: "Duplicate entries (same video in two collections): single entry only." The `playback_state` table is keyed `(user_id, video_id)` (Epic 7 Story 7.11), so a single row exists per (user, video) regardless of which collection the user originally watched it from. No extra deduplication is needed; we add an `assertion test` in §6 to pin the invariant.

## 4. GraphQL schema

`shared/graphql/schema.graphql`:

```graphql
extend type Query {
    "Up to `limit` in-progress videos for the current user, most recent first."
    continueWatching(limit: Int = 20): [VideoProgress!]!
        @auth(scope: "watch:read")
}

type VideoProgress {
    video: Video!
    positionSec: Int!
    durationSec: Int!
    updatedAt: DateTime!
}
```

The resolver sits in `api/internal/graphql/resolvers/continue_watching.go`:

```go
func (r *queryResolver) ContinueWatching(ctx context.Context, limit *int) ([]*model.VideoProgress, error) {
    user := auth.UserFromContext(ctx)
    n := 20
    if limit != nil && *limit > 0 && *limit <= 50 { n = *limit }
    rows, err := r.q.GetContinueWatching(ctx, db.GetContinueWatchingParams{
        UserID: user.ID, Limit: int32(n),
    })
    if err != nil { return nil, err }
    return mapToProgress(rows), nil
}
```

## 5. WebSocket live update

The TV apps subscribe to `playback.changed` (Epic 7 Story 7.16) and, on receipt, mutate the in-memory row:

- Find the entry by `video_id`.
- If new progress is in 5%–95% range: update `positionSec` + `updatedAt`; reorder the row by `updatedAt DESC`; if the entry didn't exist, insert at the head.
- If new progress < 5% or > 95%: remove the entry.

This is what gives the TC "watch 12 minutes of a 1-hour video on the phone: the row updates on the TV within 5 s." The 5 s budget is the WS round-trip + repaint, and it's well under the worst case (the Epic 7 contract is "<= 2 s end-to-end at the API; clients add <= 1 s repaint").

## 6. Native TV composition

### 6.1 tvOS

```swift
struct ContinueRow: View {
    @StateObject var model = ContinueRowModel()
    var body: some View {
        if model.items.isEmpty {
            EmptyState(reason: .firstRun)        // hidden on home, not "Nothing in progress"
        } else {
            VStack(alignment: .leading) {
                Text("Continue Watching").font(TVTokens.Type.rowLabel)
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(spacing: TVTokens.Spacing.md) {
                        ForEach(model.items) { item in
                            ContinueCard(item: item)
                                .buttonStyle(FocusableCardStyle())
                        }
                    }
                }.focusSection()
            }
        }
    }
}
```

`ContinueCard` overlays a progress bar (`ProgressView(value: position / duration)`) on the bottom 4 px of the poster.

### 6.2 AndroidTV

```kotlin
@Composable
fun ContinueRow(state: ContinueRowState) {
    if (state.items.isEmpty()) return        // EC: row hidden when empty
    Column {
        Text("Continue Watching", style = TvTokens.Type.rowLabel)
        LazyRow(modifier = Modifier.focusRestorer()) {
            items(state.items, key = { it.videoId }) { item ->
                ContinueCard(item, modifier = Modifier.focusableCard { onPlay(item) })
            }
        }
    }
}
```

## 7. Test plan

### 7.1 Migration

| Test | What it pins |
|---|---|
| `TestIndexPartialPredicate` | After migration, a row with `position_sec = 0` is not in the index; a row at 50% is. |
| `TestIndexCovering` | `EXPLAIN (ANALYZE, FORMAT JSON)` over the canonical query at 100k rows reports `Index Only Scan` on `playback_state_user_updated_idx`; no `Heap Fetches > 0`. |
| `TestIndexSQLiteVariant` | SQLite migration applies; `EXPLAIN QUERY PLAN` uses the index. |

### 7.2 sqlc query

| Test | What it pins |
|---|---|
| `TestContinueWatchingExcludesShortProgress` | Insert rows at 1%, 50%, 99%; only 50% returned. |
| `TestContinueWatchingExcludesZeroDuration` | A row with `duration_sec = 0` returns nothing. |
| `TestContinueWatchingExcludesDeleted` | A video with `deleted_at != NULL` is filtered. |
| `TestContinueWatchingDeduplicates` | Two collections containing the same video: still one row in `playback_state`, returned once. |
| `TestContinueWatchingCapsAt20` | 50 in-progress rows: exactly 20 returned. |
| `TestContinueWatchingOrderedByUpdatedAtDesc` | Rows updated at t1 < t2 < t3 → returned t3, t2, t1. |

### 7.3 GraphQL resolver

| Test | What it pins |
|---|---|
| `TestContinueWatchingRequiresAuth` | Unauthenticated → 401. |
| `TestContinueWatchingScopesToCaller` | User A's videos do not appear in user B's row. |
| `TestContinueWatchingLimitClamp` | `limit: 1000` → server clamps to 50. |

### 7.4 TV UI

| Test | What it pins |
|---|---|
| (tvOS) `testEmptyContinueRowHidden` | 0 items → row absent, not an empty placeholder. |
| (tvOS) `testWSPatchUpdatesProgress` | Inject a `playback.changed` event; the card's progress bar updates within 1 s. |
| (AndroidTV) `dpadAcrossContinueRow` | 20 items; D-pad right 19× lands on the last item. |
| (AndroidTV) `wsRemovalEjectsCard` | Inject a `playback.changed` with `position >= duration * 0.95`; the card animates out and the row reflows. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| `duration_sec = 0` (probe pending) | Excluded by query; row never appears. | `TestContinueWatchingExcludesZeroDuration` |
| > 50 in-progress videos | Server caps at 20. | `TestContinueWatchingCapsAt20` |
| Deleted underlying video | Excluded by `deleted_at IS NULL`; live WS removes the card. | `TestContinueWatchingExcludesDeleted` |
| Same video in two collections | One row in `playback_state` PK; one card only. | `TestContinueWatchingDeduplicates` |
| User with no history | Row hidden (not "Nothing in progress" — empty-state copy from the story applies in the dedicated detail/picker, not Home). | `testEmptyContinueRowHidden` |
| Cross-device propagation | WS event from Epic 7 Story 7.16 patches the row within 5 s. | `testWSPatchUpdatesProgress` |
| User marks watched on another device | WS event with progress > 95% removes the card. | `wsRemovalEjectsCard` |
| Index doesn't get used (regression) | CI test fails; deploy halted. | `TestIndexCovering` |

## 9. Acceptance checklist

**Schema**
- [ ] `playback_state_user_updated_idx` exists on Postgres and SQLite.
- [ ] `EXPLAIN` over the canonical query shows index-only scan on Postgres.

**Server**
- [ ] `Query.continueWatching` resolver shipped behind `@auth(scope: "watch:read")`.
- [ ] Returns ≤ `limit` items, sorted DESC by `updated_at`.

**Clients**
- [ ] tvOS `ContinueRow` renders, hides when empty.
- [ ] AndroidTV `ContinueRow` renders, hides when empty.
- [ ] Both update live via `playback.changed`.

**Tests**
- [ ] All §7 tests pass on Postgres + SQLite.

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.5.
