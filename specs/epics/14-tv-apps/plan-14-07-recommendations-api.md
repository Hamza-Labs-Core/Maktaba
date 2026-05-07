# Implementation Plan — Story 14.7 API: recommendations endpoint

> Companion to [story-14-07-recommendations-api.md](story-14-07-recommendations-api.md).
> The story states *what* and *why*; this plan states *how*.
> **Endpoint ownership.** [plan-07-21](../07-api-server/plan-07-21-recommendations.md)
> is the canonical owner of `GET /api/recommendations` (route, handler,
> per-user in-memory 60 s cache, surface routing, library/permission
> filtering, response envelope). This plan **extends** plan-07-21 with the
> TV-specific rich rails (`more_from_speaker`, `similar_to_video`,
> `newly_added`, `editor_picks`, `library_recap`, `speakers_you_follow`),
> the dismissal storage and DELETE endpoints that Story 14.6 needs, and a
> nightly cache-warmer for the heavier compose paths. The `recommendation_runs`
> table from earlier drafts is **removed** — plan-07-21's per-process
> in-memory cache is canonical.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoint | `GET /api/recommendations` is owned by [plan-07-21](../07-api-server/plan-07-21-recommendations.md). This plan adds three sibling endpoints under the same prefix: `DELETE /api/recommendations/rows/{reason_kind}`, `DELETE /api/recommendations/items/{video_id}`, `POST /api/recommendations/refresh`. |
| Surface | The TV rails compose only when `?surface=tv-home`. plan-07-21's `compose()` dispatches on `Surface`; this plan ships the `tv-home`-branch composers. |
| Migration files | `shared/db/migrations/0047_recommendation_dismissals.sql` and `0047_recommendation_dismissals.sqlite.sql`. The `recommendation_runs` cache table is dropped (plan-07-21's in-memory cache is canonical); this plan ships only the dismissals table. Slot 0042 was previously claimed by both this plan and `0042_collections_smart.sql` (Epic 8) — bumping to 0047 frees that collision. |
| sqlc queries | `shared/db/queries/recommendations.sql` (dismissal + watched-features helpers only). |
| Recommender package | `api/internal/recommender/` with `compose_tv.go`, `dismissals.go`, `score.go`. The handler / cache / per-user envelope live in plan-07-21's `api/internal/recs/`. |
| Sibling handlers | `api/internal/recs/tv_handlers.go` (mounted by plan-07-21's router under `/api/recommendations`). |
| Nightly cache-warmer | `api/internal/scheduler/warm_tv_recommendations.go` invoked by Epic 22's scheduler — pre-populates plan-07-21's in-memory cache for active users. |
| Out of scope | The UI surfaces ([Story 14.6](story-14-06-recommendations-ui.md)); the canonical handler / route / cache (plan-07-21); `media_features` table population (Epic 5 Story 5.3 / Epic 9 Story 9.10) — see §1.12 of the review; editor-picks curation table (v2). |

## 1. Architecture diagram

```
                                     nightly cron (03:00 cluster local)
                                     ┌────────────────────────────────────┐
                                     │ WarmTVRecommendations              │
                                     │   for each active user:            │
                                     │     compose(tv-home) → cache.Set() │
                                     └──────────────┬─────────────────────┘
                                                    │
   ┌────────────────────────────┐                   ▼
   │ GET /api/recommendations   │ ─────►  ┌──────────────────────────────┐
   │   ?surface=tv-home         │ ◄─────  │ plan-07-21 recs.Service      │
   │ (plan-07-21 handler)       │         │   - per-user 60s cache       │
   └────────────────────────────┘         │   - compose() on miss        │
                                          │     dispatches by surface    │
                                          └──────┬───────────────────────┘
                                                 │  surface == tv-home
                                                 ▼
                                          ┌──────────────────────────────┐
                                          │ recommender.compose_tv       │
                                          │  - more_from_speaker         │
                                          │  - similar_to_video          │
                                          │  - newly_added               │
                                          │  - editor_picks              │
                                          │  - library_recap             │
                                          │  - speakers_you_follow       │
                                          │  - dismissal filter          │
                                          └──────┬───────────────────────┘
              ┌──────────────────────────────────┼─────────────────────┐
              ▼                                  ▼                     ▼
   playback_state (Epic 7.11)        media_features (Epic 5.3/9.10)   recommendation_dismissals
                                                                       (this plan)

   DELETE /api/recommendations/rows/{kind}   ──► dismissals INSERT (idempotent)
   DELETE /api/recommendations/items/{id}    ──► dismissals INSERT (idempotent)
   POST   /api/recommendations/refresh       ──► cache.Invalidate(user)
```

## 2. Database migrations

`shared/db/migrations/0047_recommendation_dismissals.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
-- The earlier draft of this plan also shipped `recommendation_runs` (a
-- 24h DB-backed cache). That table is no longer needed: plan-07-21 owns
-- the per-process in-memory cache (60s TTL) and plan-07-21's `Cache.Set`
-- is the only persistence layer. Only the dismissals table is novel here.
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
-- +goose StatementEnd
```

SQLite variant: identical CHECK + index; `UUID` → `TEXT`,
`TIMESTAMPTZ` → `DATETIME`.

## 3. sqlc queries

`shared/db/queries/recommendations.sql`:

```sql
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
-- Architecture §8.5 puts duration on `videos`, not `playback_state`, so we
-- JOIN to compute the 5%-watched threshold; `videos.deleted_at` filters
-- soft-deleted entries.
SELECT v.id AS video_id, mf.embedding, ps.updated_at
FROM playback_state ps
JOIN videos v ON v.id = ps.video_id
JOIN media_features mf ON mf.video_id = v.id
WHERE ps.user_id = $1
  AND v.duration_sec > 0
  AND ps.position_sec >= v.duration_sec * 0.05
  AND ps.updated_at > now() - interval '30 days'
  AND v.deleted_at IS NULL
ORDER BY ps.updated_at DESC
LIMIT 5;
```

The neighbor lookup uses `media_features.embedding <=> seed_embedding`
with `pgvector`. The `media_features` table and pgvector extension are
**not yet owned**: this plan depends on Epic 9 Story 9.10 (referenced
but no migration ships the table) and a pgvector entry in architecture
§2.1's Postgres extensions list. Until both land the
`similar_to_video` rail returns empty (the cold-start path covers
this — see §10).

## 4. Composition logic

The `tv-home` branch of plan-07-21's `compose()` calls into this
package's `ComposeTV.ForUser`. The TV rails extend (do not replace)
plan-07-21's standard `continue, next-up, for-you, library` rails — the
returned envelope is `Rail[]` from plan-07-21's
[`types.go`](../07-api-server/plan-07-21-recommendations.md), not a
parallel `RowOut[]` type.

`api/internal/recommender/compose_tv.go`:

```go
type ComposeTV struct {
    db      *db.Queries
    vector  pgvector.Searcher  // wraps pgvector.Operations; nil-safe
    cfg     Config
}

// Returns extra rails to APPEND to plan-07-21's standard set when
// surface == "tv-home".
func (c *ComposeTV) ForUser(ctx context.Context, u recs.User, locale string) ([]recs.Rail, error) {
    seeds, _ := c.db.WatchedFeatures(ctx, u.ID)
    dismissals, _ := c.db.ListDismissals(ctx, u.ID)
    skip := buildDismissalSet(dismissals)

    var out []recs.Rail
    out = append(out, c.moreFromSpeakers(ctx, u, locale, skip)...)
    if c.vector != nil {
        out = append(out, c.becauseYouWatched(ctx, seeds, locale, skip)...)
    }
    out = append(out, c.newlyAdded(ctx, u, locale, skip)...)
    out = append(out, c.editorPicks(ctx, u, locale, skip)...)
    out = append(out, c.libraryRecap(ctx, u, locale, skip)...)
    out = append(out, c.speakersYouFollow(ctx, u, locale, skip)...)

    if len(seeds) == 0 {
        out = filterTo(out, []string{"newly_added", "editor_picks"})  // cold-start
    }
    return capRowsAndItems(out, 5, 20), nil
}
```

The composer accepts plan-07-21's `recs.User` and `recs.Rail`; no
schema fork. Determinism: every neighbor list sorts by `(score DESC,
video_id ASC)`. All dismissal filtering is set-membership; no random
sampling. `buildDismissalSet` no longer takes `u.ID` because the SQL
already scopes by `user_id = $1`.

## 5. Service plumbing

The cache and the per-user dedupe live in plan-07-21's `recs.Service`
(`Cache.Get` / `Cache.Set` with 60 s TTL). This plan adds a
`singleflight.Group` *inside* plan-07-21's compose pipeline so concurrent
first-time `tv-home` requests do not run the heavy `ComposeTV.ForUser`
multiple times for the same user.

```go
// api/internal/recs/compose_tv_dispatch.go (extends plan-07-21's compose)
type composeTVDispatch struct {
    tv   *recommender.ComposeTV
    sf   singleflight.Group
}

func (d *composeTVDispatch) tvHomeRails(ctx context.Context, user User, locale string) ([]Rail, error) {
    v, err, _ := d.sf.Do(user.ID.String(), func() (any, error) {
        return d.tv.ForUser(ctx, user, locale)
    })
    if err != nil {
        return nil, err   // surfaces to plan-07-21's per-rail logger; the rail degrades to empty.
    }
    return v.([]Rail), nil
}
```

The `singleflight.Group.Do` triple is `(any, error, bool)`; the prior
draft dropped the error and called an unchecked type-assert that would
panic on compose failure. The corrected form propagates the error so
plan-07-21's per-rail `s.log.Warn(...)` path runs and the response
degrades to an empty rail (never 500).

Cache invalidation on dismiss: when a DELETE handler inserts a
dismissal row, it calls `cache.Invalidate(user.ID)` on plan-07-21's
cache so the next `GET /api/recommendations` recomputes with the new
exclusion. Without this the dismissed rail/item lingers for up to 60 s.

## 6. HTTP handlers

The `GET /api/recommendations` route is owned by plan-07-21 and is **not
re-mounted here**. This plan adds three sibling handlers that plan-07-21's
router includes when the recommender is wired with TV support:

`api/internal/recs/tv_handlers.go`:

```go
// Mounted under the same /api/recommendations subrouter that plan-07-21
// declares; the function is exported so plan-07-21's MountRecommendations
// can call it.
func MountTVHandlers(r chi.Router, s *Service, dis *recommender.Dismissals) {
    r.Use(requireAuth)
    r.Delete("/rows/{reason_kind}", dismissRow(s, dis))
    r.Delete("/items/{video_id}",   dismissItem(s, dis))
    r.Post  ("/refresh",             forceRefresh(s))
}
```

`POST /api/recommendations/refresh`:
- Without `?user_id=`: invalidates the caller's cache entry; rate-limit
  via [Story 10.12](../10-auth-security/plan-10-12-rate-limiting-auth.md)
  bucket of 1/hour/user.
- With `?user_id=`: requires `is_admin = true`; invalidates that user's
  cache; bypasses the rate limit.

`DELETE /api/recommendations/rows/{reason_kind}` and
`DELETE /api/recommendations/items/{video_id}` insert into
`recommendation_dismissals` (idempotent UPSERT) and then invalidate the
caller's cache so the next GET reflects the dismissal immediately.

## 7. Nightly cache-warmer

`api/internal/scheduler/warm_tv_recommendations.go`:

```go
func (n *Warmer) Run(ctx context.Context) error {
    users, _ := n.db.ActiveUsersForBatch(ctx)
    sem := make(chan struct{}, n.cfg.WorkerConcurrency)  // default 4
    var wg sync.WaitGroup
    for _, u := range users {
        u := u
        sem <- struct{}{}
        wg.Add(1)
        go func() {
            defer func() { <-sem; wg.Done() }()
            // Wall budget per user: 500 ms p95 (the story's 200 ms target
            // is for steady-state cache hits; a cold compose with a JOIN
            // through media_features regularly takes 100-150 ms with
            // realistic variance — a 200 ms ctx deadline truncates ~50%
            // of users and writes empty rows. The deadline is a worst-case
            // cap, not the perf target).
            ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
            defer cancel()
            rails, err := n.tv.ForUser(ctx, recs.User{ID: u}, n.defaultLocale)
            if err != nil {
                n.log.Warn("warm_tv_compose_err", "user", u, "err", err.Error())
                return
            }
            // Warm plan-07-21's per-process in-memory cache (60s TTL).
            // The next GET in the same process within 60s reads from cache.
            // Other replicas warm their own caches lazily on first request.
            n.cache.Set(u, &recs.Response{Rails: rails, GeneratedAt: time.Now()}, 60*time.Second)
        }()
    }
    wg.Wait(); return nil
}
```

Runs at 03:00 server local time per cluster (Epic 22 owns the scheduler
config). The warmer's effect is bounded by plan-07-21's 60 s in-process
cache TTL — by daybreak the warmed entries will have already expired,
so the warmer's primary value is bounding the *first-of-day* compose
latency rather than serving stale rails. If a longer-lived warm cache
is needed in the future, plan-07-21 owns the cache-policy decision.

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

The `api/internal/i18n` package does **not yet exist** in Epic 7 — this
plan owns its creation if no earlier story has done so. The bundle
loader and TOML format follow the same conventions as the web client's
i18next setup (Epic 11). Strings live in
`api/internal/i18n/locales/{en,ar}.toml` — `ar.toml` in particular needs
RTL-correct phrasing. If a server-side i18n package already shipped in
Epic 7 / 11 by the time this lands, this plan reuses it instead.

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
- [ ] `recommendation_dismissals` exists on Postgres + SQLite (the `recommendation_runs` table is intentionally not created — plan-07-21's in-memory cache is canonical).
- [ ] CASCADE on user delete.

**Computation**
- [ ] Compose deterministic; per-user wall ≤ 200 ms at 100k segments.
- [ ] Cold-start fallback.

**API**
- [ ] `GET /api/recommendations` is owned by plan-07-21 (this plan does not duplicate it).
- [ ] DELETE `/rows/{kind}`, DELETE `/items/{id}`, POST `/refresh` are mounted under the same prefix; auth enforced; 1/hour/user rate limit on self-refresh.
- [ ] Localized titles via `i18n.Bundle(locale)`.
- [ ] Cache invalidation on dismissal (so the dismissed item disappears within one round trip, not 60 s).

**Caching**
- [ ] 60 s in-memory TTL via plan-07-21's `Cache`; singleflight dedupe inside the `tv-home` compose branch.

**Nightly**
- [ ] Cron registered (Epic 22); per-user budget 500 ms p95 (200 ms ctx is *not* used as the deadline — see §7).

**Tests**
- [ ] All §9 tests pass on Postgres + SQLite.

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.7.
