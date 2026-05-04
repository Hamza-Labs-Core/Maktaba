# Implementation Plan — Story 7.21 Recommendations Endpoint

> Companion to [story-07-21-recommendations.md](story-07-21-recommendations.md).
> Owns the `user_recs` schema + nightly aggregation + read API.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Route | `GET /api/recommendations?surface={web-home|tv-home|mobile-home}&limit=N` (canonical per architecture §9.7.2). |
| Storage | New `user_recs` table + index. Nightly Pipeline job populates it; this story owns the SQL. |
| Cache | Per-user in-memory, default 60 s. Single-process — replicas don't share cache. |
| Surfaces | `tv-home` includes `library` rail; `mobile-home` omits it; `web-home` is the default. |
| Out of scope | The actual nightly aggregator (Pipeline-side; this story commits the SQL it needs and the schema). |

## 1. Architecture diagram

```
   GET /api/recommendations?surface=tv-home&limit=20
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ Cache check: per-user, 60s TTL                              │
   │   hit → return cached with cache_hit:true                  │
   │                                                            │
   │ miss → compose 4 rails in parallel:                        │
   │                                                            │
   │  continue:  SELECT * FROM playback_state                   │
   │              WHERE user_id=$u                              │
   │                AND position_sec/duration_sec BETWEEN .05  │
   │                                                  AND .95  │
   │              ORDER BY updated_at DESC LIMIT $limit         │
   │              JOIN videos / media_info for poster, duration │
   │                                                            │
   │  next-up:   for each `continue` item, find its parent      │
   │             collection (Story 9.13); next unwatched item  │
   │                                                            │
   │  for-you:   SELECT v.*                                     │
   │              FROM user_recs r JOIN videos v ON v.id=r.video_id │
   │              WHERE r.user_id=$u AND r.rail_kind='for-you'  │
   │              ORDER BY r.score DESC LIMIT $limit            │
   │                                                            │
   │  library:   pick top-3 user_topics by watch time           │
   │             for each topic, pick most-watched-by-others    │
   │             unwatched videos.                              │
   │                                                            │
   │ Filter all rails through perms.AllowedLibraries()          │
   │ Cache result; return                                       │
   └────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/recs/handler.go` | Route. |
| `api/internal/recs/compose.go` | Rail composition. |
| `api/internal/recs/cache.go` | Per-user in-memory cache with TTL. |
| `api/internal/recs/types.go` | DTO. |
| `api/internal/recs/handler_test.go` | Integration. |
| `api/internal/recs/compose_test.go` | Unit. |
| `shared/db/queries/recs.sql` | sqlc inputs. |
| `shared/db/migrations/0021_user_recs.sql` | Schema. |

## 3. SQL — schema

`shared/db/migrations/0021_user_recs.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE user_recs (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id     UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    rail_kind    TEXT NOT NULL,
    score        REAL NOT NULL,
    computed_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, video_id, rail_kind),
    CHECK (rail_kind IN ('for-you','library'))
);

CREATE INDEX user_recs_lookup
    ON user_recs (user_id, rail_kind, score DESC);

-- The continue rail relies on this index introduced in Story 7.11:
-- CREATE INDEX playback_state_user_updated_idx
--    ON playback_state (user_id, updated_at DESC);
-- This story does not duplicate it; it is added in 0017_playback_state_indexes.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_recs;
-- +goose StatementEnd
```

## 4. Type definitions

```go
// api/internal/recs/types.go
package recs

import (
    "time"
    "github.com/google/uuid"
)

// PosterPath sourced from videos.poster_path (architecture §8). The wire
// shape uses `poster_path` for parity with the videos list endpoint.
type Item struct {
    VideoID       uuid.UUID  `json:"video_id"`
    Title         string     `json:"title"`
    PositionSec   *float64   `json:"position_sec,omitempty"`
    DurationSec   float64    `json:"duration_sec"`
    LastWatchedAt *time.Time `json:"last_watched_at,omitempty"`
    PosterPath    string     `json:"poster_path,omitempty"`
    Score         *float64   `json:"score,omitempty"`
}

type Rail struct {
    ID    string `json:"id"`
    Title string `json:"title"`
    Items []Item `json:"items"`
}

type Response struct {
    Rails       []Rail    `json:"rails"`
    GeneratedAt time.Time `json:"generated_at"`
    CacheHit    bool      `json:"cache_hit"`
}
```

## 5. Compose

```go
// api/internal/recs/compose.go
package recs

import (
    "context"
    "sync"
    "time"
)

type Surface string

const (
    SurfaceWebHome    Surface = "web-home"
    SurfaceTVHome     Surface = "tv-home"
    SurfaceMobileHome Surface = "mobile-home"
)

func resolveSurface(s string) Surface {
    switch Surface(s) {
    case SurfaceTVHome, SurfaceMobileHome, SurfaceWebHome:
        return Surface(s)
    default:
        return SurfaceWebHome
    }
}

func (s *service) compose(ctx context.Context, user User, surface Surface, limit int) (*Response, error) {
    if c, ok := s.cache.Get(user.ID); ok {
        out := *c
        out.CacheHit = true
        return &out, nil
    }

    libs, err := s.perms.AllowedLibraries(ctx, user)
    if err != nil { return nil, err }

    var (
        cont, nextUp, forYou, library []Item
        contErr, nextErr, fyErr, libErr error
        wg sync.WaitGroup
    )

    wg.Add(3)
    go func() { defer wg.Done(); cont, contErr = s.continueRail(ctx, user, libs, limit) }()
    go func() { defer wg.Done(); forYou, fyErr = s.forYouRail(ctx, user, libs, limit) }()
    go func() { defer wg.Done(); library, libErr = s.libraryRail(ctx, user, libs, limit) }()
    wg.Wait()

    // next-up depends on continue → run after.
    nextUp, nextErr = s.nextUpRail(ctx, user, cont, libs)

    // Errors degrade gracefully: an empty rail is acceptable, never 500.
    if contErr != nil  { s.log.Warn("rec_continue_err",  "err", contErr.Error()) }
    if nextErr != nil  { s.log.Warn("rec_nextup_err",    "err", nextErr.Error()) }
    if fyErr != nil    { s.log.Warn("rec_foryou_err",    "err", fyErr.Error()) }
    if libErr != nil   { s.log.Warn("rec_library_err",   "err", libErr.Error()) }

    rails := []Rail{
        {ID: "continue", Title: "Continue Watching", Items: cont},
        {ID: "next-up",  Title: "Next Up",           Items: nextUp},
        {ID: "for-you",  Title: "For You",           Items: forYou},
    }
    if surface == SurfaceTVHome || surface == SurfaceWebHome {
        rails = append(rails, Rail{ID: "library", Title: "By Topic", Items: library})
    }

    out := Response{Rails: rails, GeneratedAt: time.Now()}
    s.cache.Set(user.ID, &out, 60*time.Second)
    return &out, nil
}
```

## 6. Cache

```go
// api/internal/recs/cache.go
package recs

import (
    "sync"
    "time"

    "github.com/google/uuid"
)

type entry struct {
    val *Response
    exp time.Time
}

type Cache struct {
    mu sync.Mutex
    m  map[uuid.UUID]entry
}

func NewCache() *Cache { return &Cache{m: map[uuid.UUID]entry{}} }

func (c *Cache) Get(uid uuid.UUID) (*Response, bool) {
    c.mu.Lock(); defer c.mu.Unlock()
    e, ok := c.m[uid]
    if !ok || time.Now().After(e.exp) { return nil, false }
    return e.val, true
}

func (c *Cache) Set(uid uuid.UUID, v *Response, ttl time.Duration) {
    c.mu.Lock(); defer c.mu.Unlock()
    c.m[uid] = entry{val: v, exp: time.Now().Add(ttl)}
}

func (c *Cache) Invalidate(uid uuid.UUID) {
    c.mu.Lock(); defer c.mu.Unlock()
    delete(c.m, uid)
}
```

## 7. SQL — sqlc inputs

`shared/db/queries/recs.sql`:

```sql
-- duration / poster columns live on `videos` directly (architecture §8).
-- name: ContinueRail :many
SELECT v.id, v.title,
       ps.position_sec, v.duration_sec,
       ps.updated_at,
       v.poster_path
  FROM playback_state ps
  JOIN videos v ON v.id = ps.video_id
 WHERE ps.user_id = $1
   AND v.library_id = ANY($2::uuid[])
   AND v.deleted_at IS NULL
   AND v.duration_sec > 0
   AND ps.position_sec / v.duration_sec BETWEEN 0.05 AND 0.95
 ORDER BY ps.updated_at DESC
 LIMIT $3;

-- name: ForYouRail :many
SELECT v.id, v.title, v.poster_path, v.duration_sec, r.score
  FROM user_recs r
  JOIN videos v ON v.id = r.video_id
 WHERE r.user_id = $1
   AND r.rail_kind = 'for-you'
   AND v.library_id = ANY($2::uuid[])
   AND v.deleted_at IS NULL
 ORDER BY r.score DESC
 LIMIT $3;

-- name: LibraryRail :many
SELECT v.id, v.title, v.poster_path, v.duration_sec, r.score
  FROM user_recs r
  JOIN videos v ON v.id = r.video_id
 WHERE r.user_id = $1
   AND r.rail_kind = 'library'
   AND v.library_id = ANY($2::uuid[])
   AND v.deleted_at IS NULL
 ORDER BY r.score DESC
 LIMIT $3;

-- name: NextUpForVideo :one
SELECT v.id, v.title, v.poster_path, v.duration_sec
  FROM collection_items ci
  JOIN videos v ON v.id = ci.video_id
  LEFT JOIN playback_state ps
         ON ps.video_id = v.id AND ps.user_id = $1
 WHERE ci.collection_id IN (
        SELECT collection_id FROM collection_items WHERE video_id = $2
       )
   AND ci.position > (SELECT position FROM collection_items WHERE collection_id = ci.collection_id AND video_id = $2)
   AND ps.video_id IS NULL
   AND v.deleted_at IS NULL
 ORDER BY ci.position ASC
 LIMIT 1;
```

## 8. Handler scaffolding

```go
// api/internal/recs/handler.go
package recs

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
)

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
    user := userFromCtx(r.Context())
    if asUser := r.URL.Query().Get("as_user_id"); asUser != "" {
        if !user.Admin {
            httperror.Write(w, r, httperror.Forbidden(TypeAdminOnly, "as_user_id requires admin")); return
        }
        if id, err := uuid.Parse(asUser); err == nil { user.ID = id }
    }

    limit := 20
    if l := r.URL.Query().Get("limit"); l != "" {
        if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50 { limit = n }
    }

    surface := resolveSurface(r.URL.Query().Get("surface"))
    out, err := h.svc.compose(r.Context(), user, surface, limit)
    if err != nil { httperror.Write(w, r, httperror.Internal("recs")); return }
    json.NewEncoder(w).Encode(out)
}
```

## 9. Test plan

### 9.1 Unit (`compose_test.go`)

| Test | What it pins |
|---|---|
| `TestSurfaceResolveDefault` | Unknown surface → `web-home`. |
| `TestContinueExcludesNearComplete` | `position/duration > 0.95` → not in `continue`. |
| `TestContinueExcludesBarelyStarted` | `position/duration < 0.05` → not in `continue`. |
| `TestForYouRespectsLibraryFilter` | User without access to library X → no items from X. |
| `TestNewUserEmptyForYou` | No `user_recs` rows → empty `for-you`. |

### 9.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestNewUserNoHistory` | Empty playback_state, no user_recs → `continue/next-up` empty; `library` populated from catalog. |
| `TestCacheHit` | Two calls within 60 s → second has `cache_hit: true`. |
| `TestCacheExpiry` | Advance clock past 60 s → fresh compose. |
| `TestSurfaceTVHomeIncludesLibrary` | TV → 4 rails. |
| `TestSurfaceMobileHomeOmitsLibrary` | mobile → 3 rails (no `library`). |
| `TestAdminAsUser` | Admin with `?as_user_id=<uuid>` → response is the target user's recs. |
| `TestNonAdminAsUserForbidden` | Non-admin with `?as_user_id` → 403. |
| `TestDeletedVideoFiltered` | `user_recs` row pointing at a deleted video → response excludes it (FK ON DELETE CASCADE makes this automatic). |
| `TestRevokedLibraryFiltered` | User loses access overnight → next call excludes those videos. |

## 10. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| New user with no recs | `for-you` empty; UI falls back to `library` rail (catalog-driven). | `TestNewUserNoHistory` |
| User watches a video to completion (>0.95) | `continue` excludes; `next-up` may still surface it via the collection sequencing. | Unit |
| User watches the only video in a collection | `next-up` returns nothing for that collection; the rail filters out unmatched entries. | Integration |
| `limit > 50` | Clamped to 50. | Unit |
| Cache stale during a deploy | Each replica's cache is in-memory; restarts naturally invalidate. Cross-replica drift is acceptable. | Documented |
| Aggregator hasn't run yet (fresh DB) | `for-you` and `library` empty; `continue`/`next-up` still functional. | Integration |
| User profile change (e.g. language preference) | Cache invalidation is by-user; we expose a hook to call `cache.Invalidate(user_id)` from a future settings endpoint. | Stub |
| Admin calls `?as_user_id` for a deleted user | The composed response is empty; no 404 leak. | Integration |

## 11. Acceptance checklist

- [ ] `user_recs` schema + index land in `0021`.
- [ ] Response shape matches AC-1 verbatim.
- [ ] Cache TTL respected; hit reflected in `cache_hit`.
- [ ] Surface parameter controls rail set.
- [ ] Admin `?as_user_id` works; non-admin gets 403.
- [ ] Deleted/revoked videos filtered.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.21.
