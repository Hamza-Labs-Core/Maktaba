# Implementation Plan — Story 14.7 API: recommendations endpoint

> Companion to [story-14-07-recommendations-api.md](story-14-07-recommendations-api.md).
> The story states *what* and *why*; this plan states *how*.
> Schema, ranking, performance budget, and security come from the story
> AC. This plan resolves the file-by-file how.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration files | `shared/db/migrations/0042_recommendation_runs.sql` and `0042_recommendation_runs.sqlite.sql` (`recommendation_runs` and `recommendation_dismissals`). Numbering follows `0040 / 0041` from Story 14.5 / Epic 9 indexes. |
| sqlc queries | `shared/db/queries/recommendations.sql`. |
| Recommender package | `api/internal/recommender/` with `service.go`, `score.go`, `compose.go`, `cache.go`. |
| HTTP handlers | `api/internal/http/recommendations.go`, mounted at `/api/recommendations`. |
| Nightly job | `api/internal/scheduler/nightly_recommendations.go` invoked by Epic 22's scheduler. |
| Out of scope | The UI surfaces ([Story 14.6](story-14-06-recommendations-ui.md)); `media_features` table population (Epic 5 Story 5.3 / Epic 9 Story 9.10); editor-picks curation table (v2). |

## 1. Architecture diagram

```
                                     nightly cron (03:00 cluster local)
                                     ┌──────────────────────────────┐
                                     │ NightlyRecommendations        │
                                     │   for each user:              │
                                     │     compose() → cache row     │
                                     └──────────────┬────────────────┘
                                                    │
   ┌────────────────────────┐                       ▼
   │ GET /api/recommendations│ ──────►  ┌────────────────────────────┐
   │  (handler)              │ ◄────────│ recommender.Service        │
   └────────────────────────┘           │  - cache(read/write)       │
                                        │  - compose() on miss       │
                                        │  - dismiss read            │
                                        └──────┬─────────────────────┘
                                               │
              ┌────────────────────────────────┼─────────────────────┐
              ▼                                ▼                     ▼
   playback_state (Epic 7.11)      media_features (Epic 5.3/9.10)   recommendation_dismissals
                                                                    + recommendation_runs
```

## 2. Database migrations

`shared/db/migrations/0042_recommendation_runs.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE recommendation_runs (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    computed_at TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    rows        JSONB NOT NULL,
    CHECK (jsonb_typeof(rows) = 'array'),
    CHECK (expires_at > computed_at)
);

CREATE INDEX recommendation_runs_expires_at_idx
    ON recommendation_runs (expires_at);

CREATE TABLE recommendation_dismissals (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('row','item')),
    key          TEXT NOT NULL,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind, key)
);

CREATE INDEX recommendation_dismissals_user_idx
    ON recommendation_dismissals (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS recommendation_dismissals;
DROP TABLE IF EXISTS recommendation_runs;
-- +goose StatementEnd
```

SQLite variant: drop `jsonb_typeof` check; treat `rows` as TEXT JSON; everything else identical.

## 3. sqlc queries

`shared/db/queries/recommendations.sql`:

```sql
-- name: GetRecommendationRun :one
SELECT user_id, computed_at, expires_at, rows
FROM recommendation_runs WHERE user_id = $1;

-- name: UpsertRecommendationRun :exec
INSERT INTO recommendation_runs (user_id, computed_at, expires_at, rows)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE
   SET computed_at = EXCLUDED.computed_at,
       expires_at  = EXCLUDED.expires_at,
       rows        = EXCLUDED.rows;

-- name: ListDismissals :many
SELECT kind, key FROM recommendation_dismissals WHERE user_id = $1;

-- name: InsertDismissalIdempotent :exec
INSERT INTO recommendation_dismissals (user_id, kind, key)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ActiveUsersForBatch :many
-- Active = at least one playback_state row in the last 30 days.
SELECT DISTINCT user_id FROM playback_state
WHERE updated_at > now() - interval '30 days';

-- name: WatchedFeatures :many
-- Returns the feature vectors of the user's last 5 watched-past-5pct videos.
SELECT v.id AS video_id, mf.embedding, ps.updated_at
FROM playback_state ps
JOIN videos v ON v.id = ps.video_id
JOIN media_features mf ON mf.video_id = v.id
WHERE ps.user_id = $1
  AND ps.position_sec >= ps.duration_sec * 0.05
  AND ps.updated_at > now() - interval '30 days'
  AND v.deleted_at IS NULL
ORDER BY ps.updated_at DESC
LIMIT 5;
```

The neighbor lookup uses `media_features.embedding <=> seed_embedding` with `pgvector` — the `media_features` table and the index are owned by Epic 9 Story 9.10.

## 4. Composition logic

`api/internal/recommender/compose.go`:

```go
type Compose struct {
    db      *db.Queries
    vector  pgvector.Searcher  // wraps pgvector.Operations
    cfg     Config             // tunables
}

type RowOut struct {
    Title      string                 `json:"title"`
    ReasonKind string                 `json:"reason_kind"`
    ReasonArgs map[string]any         `json:"reason_args"`
    ItemIDs    []uuid.UUID            `json:"item_ids"`
}

func (c *Compose) ForUser(ctx context.Context, u User) ([]RowOut, error) {
    seeds, _ := c.db.WatchedFeatures(ctx, u.ID)
    dismissals, _ := c.db.ListDismissals(ctx, u.ID)
    skip := buildDismissalSet(dismissals, u.ID)

    var out []RowOut
    out = append(out, c.becauseYouWatched(ctx, seeds, skip)...)
    out = append(out, c.moreFromSpeakers(ctx, u, skip)...)
    out = append(out, c.newlyAddedFavorite(ctx, u, skip)...)
    out = append(out, c.editorPicksFallback(ctx, u, skip)...)

    if len(seeds) == 0 {
        out = filterTo(out, []string{"newly_added", "editor_picks"})  // cold-start
    }
    return capRowsAndItems(out, 5, 20), nil
}
```

Determinism: every neighbor list sorts by `(score DESC, video_id ASC)` (per the AC). All dismissal filtering is set-membership; no random sampling.

## 5. Service + cache

`api/internal/recommender/service.go`:

```go
type Service struct {
    db      *db.Queries
    compose *Compose
    sf      singleflight.Group   // dedupes concurrent compose() per user
}

func (s *Service) GetForUser(ctx context.Context, u User) (Run, error) {
    row, err := s.db.GetRecommendationRun(ctx, u.ID)
    if err == nil && row.ExpiresAt.After(time.Now()) {
        return decodeRun(row), nil
    }
    // Cache miss: compose inline, but bound at 1 s. If we exceed the
    // budget, return the stale row and schedule an async refresh.
    if err == nil {  // stale present
        s.scheduleAsyncRefresh(u.ID)
        return decodeRun(row), nil
    }
    rowOut, _, _ := s.sf.Do(u.ID.String(), func() (any, error) {
        return s.compose.ForUser(ctx, u)
    })
    rs := encodeRun(u.ID, rowOut.([]RowOut), time.Now())
    _ = s.db.UpsertRecommendationRun(ctx, rs)
    return rs, nil
}
```

The `singleflight.Group` collapses concurrent first-time computations so a thundering herd at cold start does not run the compose 100 times.

## 6. HTTP handlers

`api/internal/http/recommendations.go`:

```go
func MountRecommendations(r chi.Router, s *recommender.Service) {
    r.Route("/recommendations", func(r chi.Router) {
        r.Use(requireAuth)
        r.Get("/", get(s))
        r.Delete("/rows/{reason_kind}", dismissRow(s))
        r.Delete("/items/{video_id}", dismissItem(s))
        r.Post("/refresh", forceRefresh(s))   // admin or rate-limited self-refresh
    })
}
```

`POST /api/recommendations/refresh`:
- Without `?user_id=`: refreshes the caller's row; rate-limit via [Story 10.12](../10-auth-security/plan-10-12-rate-limiting-auth.md) bucket of 1/hour/user.
- With `?user_id=`: requires `is_admin = true`; bypasses the rate limit.

## 7. Nightly batch

`api/internal/scheduler/nightly_recommendations.go`:

```go
func (n *Nightly) Run(ctx context.Context) error {
    users, _ := n.db.ActiveUsersForBatch(ctx)
    sem := make(chan struct{}, n.cfg.WorkerConcurrency)  // default 4
    var wg sync.WaitGroup
    for _, u := range users {
        u := u
        sem <- struct{}{}
        wg.Add(1)
        go func() {
            defer func() { <-sem; wg.Done() }()
            ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
            defer cancel()
            rows, _ := n.compose.ForUser(ctx, u)
            run := encodeRun(u.ID, rows, time.Now())
            _ = n.db.UpsertRecommendationRun(ctx, run)
        }()
    }
    wg.Wait(); return nil
}
```

Runs at 03:00 server local time per cluster (Epic 22 owns the scheduler config).

## 8. Localization

`title` is composed server-side based on the caller's `Accept-Language`:

```go
func titleFor(kind string, args map[string]any, locale string) string {
    bundle := i18n.Bundle(locale)
    switch kind {
    case "more_from_speaker":
        return bundle.T("rec.more_from_speaker", "speaker", args["speaker_name"])
    case "similar_to_video":
        return bundle.T("rec.similar_to", "title", args["video_title"])
    // ...
    }
}
```

Strings live in `api/internal/i18n/locales/{en,ar}.toml` — `ar.toml` in particular needs RTL-correct phrasing.

## 9. Test plan

### 9.1 Migration

| Test | What it pins |
|---|---|
| `TestMigrationApplies` | Both tables created on Postgres + SQLite. |
| `TestMigrationCascadesOnUserDelete` | Deleting a user removes rows from both tables. |
| `TestExpiresAfterComputed` | Inserting `expires_at < computed_at` violates the CHECK. |

### 9.2 Compose

| Test | What it pins |
|---|---|
| `TestColdStartReturnsOnlyNewlyAndEditor` | User with 0 watch history → only those two rows. |
| `TestBecauseYouWatchedTopK` | 5 watched seeds + 50 candidates → top-K neighbors per seed; deduplicated; capped at 20. |
| `TestMoreFromSpeakerHeuristic` | Speaker A appears 3× in watch history, B appears 2× → only A's row generated. |
| `TestDismissalsExcluded` | `recommendation_dismissals` row for `reason:more_from_speaker:A` → row absent in next compose. |
| `TestDeterminism` | Same inputs, two compose calls → byte-identical JSONB. |
| `TestRowsCappedAt5AndItemsAt20` | Inputs that would produce 8 rows / 50 items → 5/20. |

### 9.3 Service & HTTP

| Test | What it pins |
|---|---|
| `TestGetReturnsCachedRunIfFresh` | `expires_at > now()` → no compose call. |
| `TestGetMissComputesAndCaches` | First call computes; second within 24 h hits the cache row. |
| `TestGetStaleScheduleAsyncRefresh` | `expires_at < now()` AND compose budget exceeded → stale row returned; async refresh runs. |
| `TestSingleFlightDedupes` | 50 concurrent first-time GETs → 1 compose execution. |
| `TestForceRefreshSelfRateLimited` | 2nd self-refresh within an hour → 429. |
| `TestForceRefreshAdminBypassesLimit` | Admin can force any user; no rate limit. |
| `TestDeleteRowDismissesPersistently` | DELETE `/rows/more_from_speaker` → idempotent insert into dismissals; next compose excludes the row. |

### 9.4 Nightly

| Test | What it pins |
|---|---|
| `TestNightlyComputesEveryActiveUser` | 100 active users → 100 cache rows after run. |
| `TestNightlyBudgetPerUser` | Synthetic compose that sleeps 300 ms → context deadline exceeded; the user's row is not overwritten with a half-result. |
| `TestNightlyConcurrencyCap` | Worker semaphore = 4 → never more than 4 in-flight compose calls. |

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| `media_features` empty (fresh install) | All semantic rows skipped; only `newly_added`/`editor_picks` returned with 200 (never 500). | `TestComposeWithoutMediaFeatures` |
| User's entire watched set deleted | Falls back to cold-start. | `TestDeletedHistoryColdStart` |
| User with > 1k watched | Last 30-day window only; query's `interval '30 days'` enforces. | `TestThirtyDayWindow` |
| All candidate items dismissed | Returns `rows: []` 200; client renders empty state. | `TestAllDismissedReturnsEmpty` |
| Same `video_id` selected by two reasons | Deduplicated globally to keep row item lists distinct (ranked: more recent reason wins). | `TestCrossRowDedup` |
| Cache JSON corrupted (manual DB edit) | Decode error → recompute inline; never panic. | `TestCorruptCacheRecomputed` |
| Locale changes mid-day | Cache row's `title` is in the locale at compose time; client re-fetches on locale change (force refresh). | `TestLocaleSwitchForcesRefresh` |
| Side-channel: another user's IDs | Compose only ever queries scoped to caller; integration test asserts. | `TestNoCrossUserLeakage` |
| Editor-picks table missing (v1 fallback) | Falls back to "most-played overall last 30 days". | `TestEditorPicksFallback` |
| Active user count: 50k | Nightly bounded at O(users * 5 * top-k); test against synthetic 50k completes < 30 min. | `BenchmarkNightlyAt50k` |

## 11. Acceptance checklist

**Schema**
- [ ] `recommendation_runs` and `recommendation_dismissals` exist on Postgres + SQLite.
- [ ] CASCADE on user delete.

**Computation**
- [ ] Compose deterministic; per-user wall ≤ 200 ms at 100k segments.
- [ ] Cold-start fallback.

**API**
- [ ] All four endpoints exist; auth enforced; rate limit on self-refresh.
- [ ] Localized titles.

**Caching**
- [ ] 24h TTL; singleflight dedupe; async refresh on stale-with-budget-exceed.

**Nightly**
- [ ] Cron registered (Epic 22); per-user budget enforced.

**Tests**
- [ ] All §9 tests pass on Postgres + SQLite.

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.7.
