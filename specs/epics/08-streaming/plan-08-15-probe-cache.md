# Implementation Plan — Story 8.15 Probe Cache

> Companion to [story-08-15-probe-cache.md](story-08-15-probe-cache.md).
> The story states *what* and *why*; this plan states *how*. Architecture
> reference: [§4 intro](../../architecture.md#4-streaming-service).
> Source of truth: `videos`, `media_info`, `audio_tracks` tables (Pipeline writes).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/probe/` — typed read-only access to media metadata. |
| Cache | `hashicorp/golang-lru/v2` (LRU, generics-friendly, well-maintained) at default 10 000 entries. |
| Single-flight | `golang.org/x/sync/singleflight` keyed on `video_id` to coalesce concurrent first-fetches. |
| On-disk re-probe | Forbidden. Streaming never invokes ffprobe. AC-3. |
| Eviction | Triggered by `streaming.EvictHashCache` (Story 8.8); subscribes via the in-process `evict.Bus`. |
| Hot-reload of media_info | Pipeline calls EvictHashCache on its Streaming peer when it re-probes; we drop the entry. |
| Out of scope | The `media_info` schema (Pipeline owns it). Re-probing (Pipeline's `probe` stage). |

## 1. Architecture diagram

```
       OpenSession (8.8)            other handlers (8.3, 8.4, 8.13, ...)
                  │                                       │
                  ▼                                       ▼
        ┌────────────────────────────────────────────────────────────┐
        │ probe.Lookup interface                                      │
        │   LookupVideo(ctx, video_id) (*Row, error)                  │
        └────────────────────────────┬───────────────────────────────┘
                                     │
                                     ▼
        ┌────────────────────────────────────────────────────────────┐
        │ probe.Cache (LRU 10 000)                                   │
        │   .Get(video_id)   → *Row                                  │
        │   .Set(video_id, row)                                      │
        │   .Evict(video_id)                                         │
        │   .EvictByHash(content_hash)                               │
        └────────────────────────────┬───────────────────────────────┘
                                     │ miss
                                     ▼
        ┌────────────────────────────────────────────────────────────┐
        │ singleflight.Group keyed on video_id                       │
        │   only one DB query in flight per video                    │
        └────────────────────────────┬───────────────────────────────┘
                                     │
                                     ▼
        ┌────────────────────────────────────────────────────────────┐
        │ probe.DB.Fetch(ctx, video_id)                              │
        │   SELECT … FROM videos                                     │
        │     LEFT JOIN media_info  ON media_info.video_id  = …      │
        │     LEFT JOIN audio_tracks ON audio_tracks.video_id = …    │
        │   WHERE videos.id = $1                                     │
        │                                                            │
        │   - missing media_info  → ErrNotProbed                     │
        │   - missing video       → ErrNotFound                      │
        └────────────────────────────────────────────────────────────┘

        ┌────────────────────────────────────────────────────────────┐
        │ evict.Bus (in-process)                                     │
        │   subscribers receive Event{Hash, VideoID}                 │
        │   probe.Cache subscribes; Pipeline → grpc EvictHashCache   │
        │     translates to bus.Publish                              │
        └────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/probe/types.go` | `Row`, `MediaInfo`, `AudioTrack`, `SubtitleTrack` structs. |
| `streaming/internal/probe/cache.go` | `Cache` (LRU + by-hash secondary index). |
| `streaming/internal/probe/db.go` | sqlc-backed `Fetch`. |
| `streaming/internal/probe/lookup.go` | `Lookup` interface used everywhere; default impl wraps cache+singleflight+db. |
| `streaming/internal/probe/lookup_test.go` | All test cases. |
| `streaming/internal/probe/cache_test.go` | LRU semantics. |
| `streaming/internal/evict/bus.go` | Tiny in-process pub/sub; subscribers register a callback. |
| `streaming/internal/evict/bus_test.go` | Concurrency tests. |
| `shared/db/queries/probe.sql` | sqlc input. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/cmd/maktaba-streaming/main.go` | Wire `probe.Lookup` and `evict.Bus`; subscribe the cache. |
| `streaming/internal/grpcserver/evict_cache.go` | Calls `probe.Lookup.EvictByHash` (already publishes through bus). |
| `streaming/internal/observability/metrics.go` | `probe_cache_hit_total`, `probe_cache_miss_total`, `probe_cache_evict_total{reason}`, `probe_db_query_duration_seconds`. |
| `streaming/configs/streaming.toml.example` | `[probe] cache_size = 10000`. |
| `specs/epics/08-streaming/README.md` | Tick 8.15. |

### 2.3 Type definitions

```go
// streaming/internal/probe/types.go
package probe

import (
    "errors"
    "time"

    "github.com/google/uuid"
)

var (
    ErrNotFound   = errors.New("probe: video not found")
    ErrNotProbed  = errors.New("probe: video not probed")
)

type Row struct {
    VideoID       uuid.UUID
    LibraryID     uuid.UUID
    Path          string
    Container     string
    ContentHash   string       // BLAKE3 hex
    DurationSec   float64
    SizeBytes     int64
    MIME          string
    UpdatedAt     time.Time

    MediaInfo     MediaInfo
    AudioTracks   []AudioTrack
    SubtitleTracks []SubtitleTrack
}

type MediaInfo struct {
    DurationSec    float64
    BitrateKbps    int

    VideoStreamIdx int
    VideoCodec     string
    VideoProfile   string
    VideoLevel     string
    Width, Height  int
    HDR            string
    HasBFrames     bool

    AudioStreamIdx int   // default audio
    AudioCodec     string
    AudioChannels  int
    AudioBitrateKbps int
}

type AudioTrack struct {
    StreamIndex int
    Codec       string
    Channels    int
    BitrateKbps int
    Lang        string
    Title       string
    IsDefault   bool
}

type SubtitleTrack struct {
    StreamIndex int
    Codec       string
    Lang        string
    Title       string
    IsDefault   bool
    IsForced    bool
}
```

```go
// streaming/internal/probe/lookup.go
package probe

import (
    "context"

    "github.com/google/uuid"
    "golang.org/x/sync/singleflight"
)

type Lookup interface {
    LookupVideo(ctx context.Context, videoID uuid.UUID) (*Row, error)
    EvictByHash(hash string) int
}

type cachedLookup struct {
    Cache *Cache
    DB    *DB
    SF    singleflight.Group
}

func New(cache *Cache, db *DB, bus *evict.Bus) Lookup {
    l := &cachedLookup{Cache: cache, DB: db}
    bus.Subscribe(func(e evict.Event) {
        if e.Hash != "" {
            l.Cache.EvictByHash(e.Hash)
        }
        if e.VideoID != uuid.Nil {
            l.Cache.Evict(e.VideoID)
        }
    })
    return l
}

func (l *cachedLookup) LookupVideo(ctx context.Context, videoID uuid.UUID) (*Row, error) {
    if row, ok := l.Cache.Get(videoID); ok {
        metricsHit.Inc()
        return row, nil
    }
    metricsMiss.Inc()

    res, err, _ := l.SF.Do(videoID.String(), func() (any, error) {
        // Re-check cache after acquiring single-flight slot.
        if row, ok := l.Cache.Get(videoID); ok {
            return row, nil
        }
        row, err := l.DB.Fetch(ctx, videoID)
        if err != nil {
            return nil, err
        }
        l.Cache.Set(videoID, row)
        return row, nil
    })
    if err != nil {
        return nil, err
    }
    return res.(*Row), nil
}

func (l *cachedLookup) EvictByHash(hash string) int {
    return l.Cache.EvictByHash(hash)
}
```

### 2.4 Cache implementation

```go
// streaming/internal/probe/cache.go
package probe

import (
    "sync"

    "github.com/google/uuid"
    lru "github.com/hashicorp/golang-lru/v2"
)

type Cache struct {
    mu       sync.RWMutex
    byID     *lru.Cache[uuid.UUID, *Row]
    byHash   map[string]map[uuid.UUID]struct{} // hash → set of video ids holding it
}

func NewCache(size int) (*Cache, error) {
    if size <= 0 { size = 10_000 }
    inner, err := lru.New[uuid.UUID, *Row](size)
    if err != nil { return nil, err }
    return &Cache{byID: inner, byHash: map[string]map[uuid.UUID]struct{}{}}, nil
}

func (c *Cache) Get(videoID uuid.UUID) (*Row, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.byID.Get(videoID)
}

func (c *Cache) Set(videoID uuid.UUID, row *Row) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if old, ok := c.byID.Peek(videoID); ok {
        // Remove old hash mapping if hash changed.
        if old.ContentHash != row.ContentHash {
            c.removeHashMappingLocked(old.ContentHash, videoID)
        }
    }
    c.byID.Add(videoID, row)
    if row.ContentHash != "" {
        set, ok := c.byHash[row.ContentHash]
        if !ok {
            set = map[uuid.UUID]struct{}{}
            c.byHash[row.ContentHash] = set
        }
        set[videoID] = struct{}{}
    }
}

func (c *Cache) Evict(videoID uuid.UUID) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if old, ok := c.byID.Peek(videoID); ok {
        c.removeHashMappingLocked(old.ContentHash, videoID)
    }
    c.byID.Remove(videoID)
}

func (c *Cache) EvictByHash(hash string) int {
    c.mu.Lock()
    defer c.mu.Unlock()
    set, ok := c.byHash[hash]
    if !ok {
        return 0
    }
    n := 0
    for id := range set {
        if c.byID.Remove(id) {
            n++
        }
    }
    delete(c.byHash, hash)
    return n
}

func (c *Cache) removeHashMappingLocked(hash string, vid uuid.UUID) {
    if hash == "" { return }
    set, ok := c.byHash[hash]
    if !ok { return }
    delete(set, vid)
    if len(set) == 0 {
        delete(c.byHash, hash)
    }
}

// LRU eviction is not aware of the byHash index; we'd leak a small
// amount of map-key garbage on natural eviction. Accept that drift —
// the entry would simply never resolve and would be re-added on next
// Set. (Could be tightened with a custom OnEvict callback; lru/v2 supports it.)
```

We use the `OnEvict` hook to keep the index tidy:

```go
// In NewCache:
inner, err := lru.NewWithEvict[uuid.UUID, *Row](size, func(id uuid.UUID, row *Row) {
    if row != nil && row.ContentHash != "" {
        c.removeHashMappingLocked(row.ContentHash, id)
    }
})
```

### 2.5 DB fetch

```sql
-- shared/db/queries/probe.sql

-- name: FetchVideoBundle :one
-- Returns the join of videos, media_info, and a JSON aggregate of
-- audio_tracks and subtitle_tracks. The JSON aggregate keeps the query
-- to one round trip; sqlc's Go side decodes via jsoniter.
SELECT
    v.id              AS video_id,
    v.library_id      AS library_id,
    v.path            AS path,
    v.container       AS container,
    v.content_hash    AS content_hash,
    v.duration_sec    AS duration_sec,
    v.size_bytes      AS size_bytes,
    v.mime            AS mime,
    v.updated_at      AS updated_at,
    mi.bitrate_kbps   AS bitrate_kbps,
    mi.video_stream_idx AS video_stream_idx,
    mi.video_codec    AS video_codec,
    mi.video_profile  AS video_profile,
    mi.video_level    AS video_level,
    mi.width          AS width,
    mi.height         AS height,
    mi.hdr            AS hdr,
    mi.has_b_frames   AS has_b_frames,
    mi.audio_stream_idx AS audio_stream_idx,
    mi.audio_codec    AS audio_codec,
    mi.audio_channels AS audio_channels,
    mi.audio_bitrate_kbps AS audio_bitrate_kbps,
    COALESCE((
      SELECT json_agg(jsonb_build_object(
                'stream_index', a.stream_index,
                'codec', a.codec,
                'channels', a.channels,
                'bitrate_kbps', a.bitrate_kbps,
                'lang', a.lang,
                'title', a.title,
                'is_default', a.is_default))
        FROM audio_tracks a
       WHERE a.video_id = v.id
    ), '[]'::json) AS audio_json,
    COALESCE((
      SELECT json_agg(jsonb_build_object(
                'stream_index', s.stream_index,
                'codec', s.codec,
                'lang', s.lang,
                'title', s.title,
                'is_default', s.is_default,
                'is_forced', s.is_forced))
        FROM subtitle_tracks s
       WHERE s.video_id = v.id
    ), '[]'::json) AS subtitle_json
  FROM videos v
  LEFT JOIN media_info mi ON mi.video_id = v.id
 WHERE v.id = $1;
```

```go
// streaming/internal/probe/db.go
package probe

func (d *DB) Fetch(ctx context.Context, videoID uuid.UUID) (*Row, error) {
    start := time.Now()
    defer func() { metricsQueryDuration.Observe(time.Since(start).Seconds()) }()

    rec, err := d.q.FetchVideoBundle(ctx, videoID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrNotFound
        }
        return nil, err
    }
    if !rec.VideoCodec.Valid { // media_info missing
        return nil, ErrNotProbed
    }

    audio, err := decodeAudioTracks(rec.AudioJSON)
    if err != nil { return nil, err }
    subs, err := decodeSubtitleTracks(rec.SubtitleJSON)
    if err != nil { return nil, err }

    return &Row{
        VideoID: videoID, LibraryID: rec.LibraryID,
        Path: rec.Path, Container: rec.Container,
        ContentHash: rec.ContentHash, DurationSec: rec.DurationSec.Float64,
        SizeBytes: rec.SizeBytes, MIME: rec.Mime,
        UpdatedAt: rec.UpdatedAt.Time,
        MediaInfo: MediaInfo{
            DurationSec:      rec.DurationSec.Float64,
            BitrateKbps:      int(rec.BitrateKbps.Int32),
            VideoStreamIdx:   int(rec.VideoStreamIdx.Int32),
            VideoCodec:       rec.VideoCodec.String,
            VideoProfile:     rec.VideoProfile.String,
            VideoLevel:       rec.VideoLevel.String,
            Width:            int(rec.Width.Int32),
            Height:           int(rec.Height.Int32),
            HDR:              rec.Hdr.String,
            HasBFrames:       rec.HasBFrames.Bool,
            AudioStreamIdx:   int(rec.AudioStreamIdx.Int32),
            AudioCodec:       rec.AudioCodec.String,
            AudioChannels:    int(rec.AudioChannels.Int32),
            AudioBitrateKbps: int(rec.AudioBitrateKbps.Int32),
        },
        AudioTracks: audio, SubtitleTracks: subs,
    }, nil
}
```

### 2.6 Evict bus

```go
// streaming/internal/evict/bus.go
package evict

import (
    "sync"

    "github.com/google/uuid"
)

type Event struct {
    Hash    string
    VideoID uuid.UUID
}

type Bus struct {
    mu   sync.RWMutex
    subs []func(Event)
}

func NewBus() *Bus { return &Bus{} }

func (b *Bus) Subscribe(fn func(Event)) {
    b.mu.Lock()
    b.subs = append(b.subs, fn)
    b.mu.Unlock()
}

// Publish synchronously fans out. Subscribers must not block.
func (b *Bus) Publish(e Event) {
    b.mu.RLock()
    snap := append([]func(Event){}, b.subs...)
    b.mu.RUnlock()
    for _, fn := range snap {
        fn(e)
    }
}
```

The bus is in-process — it doesn't persist or fan out across hosts.
Cross-host eviction is the caller's job: the API issues
`streaming.EvictHashCache` to **every** Streaming binary in the cluster
(visible via service discovery / static config).

## 3. Test plan

### 3.1 Cache (`cache_test.go`)

| Test | What it pins |
|---|---|
| `TestCache_GetMiss` | Empty cache → miss. |
| `TestCache_SetThenGet` | After Set, Get returns the same row. |
| `TestCache_LRUEviction` | size=2; insert 3; oldest evicted. |
| `TestCache_EvictByHash_RemovesAllVideosWithThatHash` | Two videos sharing the same content_hash → both evicted. |
| `TestCache_SetWithChangedHashUpdatesIndex` | Same video_id, different hash → old hash mapping removed; EvictByHash(oldHash) returns 0. |
| `TestCache_OnEvictPrunesByHashIndex` | Force LRU eviction → byHash entry pruned via OnEvict. |
| `TestCache_ConcurrentSetGet` | 100 goroutines → no torn reads (verified with `-race`). |

### 3.2 Lookup (`lookup_test.go`)

| Test | What it pins |
|---|---|
| `TestLookup_HitNoDBQuery` | Pre-populate cache; LookupVideo → no DB call (asserted via DB spy). AC-1. |
| `TestLookup_MissTriggersDBOnce` | Cold cache; 1000 goroutines call LookupVideo → exactly 1 DB call. AC + edge case (single-flight). |
| `TestLookup_MissingMediaInfo_FAILED_PRECONDITION` | DB returns row with NULL media_info → ErrNotProbed; gRPC layer maps to FAILED_PRECONDITION. AC-3. |
| `TestLookup_VideoNotFound_NOT_FOUND` | DB returns ErrNoRows → ErrNotFound. |
| `TestLookup_EvictByHashInvalidates` | Insert into cache; Bus.Publish({Hash}); next LookupVideo issues a DB query (cache miss). AC-4. |
| `TestLookup_EvictByHashTwoSubscribersBothNotified` | Story 8.8's EvictHashCache also invalidates remux/posters; the bus delivers to all subscribers. |
| `TestLookup_PerformanceUnder1000Calls` | 1000 LookupVideo on the same id → 1 DB query (cache hits 999×). AC-1. |

### 3.3 Bus (`bus_test.go`)

| Test | What it pins |
|---|---|
| `TestBus_PublishFanout` | 5 subscribers; Publish → all 5 invoked exactly once. |
| `TestBus_SubscribeDuringPublishSafe` | Subscribe in one goroutine while Publish runs in another → no panic; new subscriber doesn't see the in-flight event. |

### 3.4 Integration (`integration_test.go`)

Uses Postgres test DB seeded with one library, one video, media_info,
two audio tracks, one subtitle track.

| Test | What it pins |
|---|---|
| `TestIntegration_FetchPopulatesAllFields` | LookupVideo → row carries all expected fields, including audio_tracks slice of length 2 and subtitle_tracks length 1. |
| `TestIntegration_NoMediaInfo_ErrNotProbed` | Insert video without media_info → ErrNotProbed. |
| `TestIntegration_QueryUnderLoad` | Postgres EXPLAIN shows the plan uses the `videos.id` PK + `media_info.video_id` index; latency p99 < 5 ms on local. |

## 4. Test code scaffolding

```go
// streaming/internal/probe/lookup_test.go
package probe_test

import (
    "context"
    "errors"
    "sync/atomic"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/evict"
    "maktaba/streaming/internal/probe"
)

type spyDB struct {
    calls atomic.Int64
    row   *probe.Row
    err   error
}

func (s *spyDB) Fetch(_ context.Context, _ uuid.UUID) (*probe.Row, error) {
    s.calls.Add(1)
    return s.row, s.err
}

func TestLookup_MissTriggersDBOnce(t *testing.T) {
    cache, _ := probe.NewCache(100)
    bus := evict.NewBus()
    db := &spyDB{row: &probe.Row{VideoID: uuid.New(), ContentHash: "abc"}}

    look := probe.NewWithDB(cache, db, bus)
    sid := db.row.VideoID

    var wg sync.WaitGroup
    for i := 0; i < 1000; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, err := look.LookupVideo(context.Background(), sid)
            require.NoError(t, err)
        }()
    }
    wg.Wait()
    require.Equal(t, int64(1), db.calls.Load())
}

func TestLookup_EvictByHashInvalidates(t *testing.T) {
    cache, _ := probe.NewCache(100)
    bus := evict.NewBus()
    db := &spyDB{row: &probe.Row{VideoID: uuid.New(), ContentHash: "abc"}}
    look := probe.NewWithDB(cache, db, bus)

    _, err := look.LookupVideo(context.Background(), db.row.VideoID)
    require.NoError(t, err)
    require.Equal(t, int64(1), db.calls.Load())

    bus.Publish(evict.Event{Hash: "abc"})

    _, err = look.LookupVideo(context.Background(), db.row.VideoID)
    require.NoError(t, err)
    require.Equal(t, int64(2), db.calls.Load(), "evict didn't invalidate")
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| `media_info` updated by Pipeline (re-probe after file change) | Pipeline calls `streaming.EvictHashCache(content_hash)` simultaneously with the remux cache invalidation. The bus event drops the probe entry; the next LookupVideo sees the new media_info. | `TestLookup_EvictByHashInvalidates` + cross-link to Story 8.8. |
| Concurrent OpenSession for the same uncached video | Single-flight ensures one DB query; all callers wait on the in-flight result. | `TestLookup_MissTriggersDBOnce` |
| `media_info` missing entirely (video added but probe not run) | `ErrNotProbed`; the Manager (Story 8.8) maps this to `FAILED_PRECONDITION code='video-not-probed'`. The API enqueues a probe job. | `TestLookup_MissingMediaInfo_FAILED_PRECONDITION` |
| `videos` deleted between cache-set and Lookup | Cache still serves the stale row until the API cancels sessions for that video. The cascade in Story 8.9's schema removes streaming_sessions, but probe entries are LRU-evicted naturally. We don't preemptively scrub — Pipeline + API are responsible for issuing EvictHashCache on delete. | Documented; no test. |
| Two videos with the same `content_hash` | `byHash[hash]` is a set of `video_id`s; EvictByHash drops all of them. | `TestCache_EvictByHash_RemovesAllVideosWithThatHash` |
| LRU evicts an entry whose hash appears in `byHash` | OnEvict callback prunes the byHash mapping. | `TestCache_OnEvictPrunesByHashIndex` |
| Subscriber callback panics | We `recover` inside the bus's loop so other subscribers still fire. (Defensive: subscribers should not panic.) | Implicit (recover in bus.Publish). |
| Subscriber added during Publish | The snapshot is taken under RLock; a new subscriber added after the snapshot doesn't receive the in-flight event. They'll get the next one. | `TestBus_SubscribeDuringPublishSafe` |
| Cache size = 0 (operator misconfiguration) | NewCache normalizes to 10 000. | `TestCache_DefaultSize`. |
| Unknown video_id requested 1000 times | Each request is a DB miss; single-flight collapses to one DB call per concurrent batch (so a stampede is bounded). After the negative result, we don't cache `ErrNotFound`/`ErrNotProbed` — the next request will retry. The cost is acceptable because such requests should be rare. | Implicit; `TestLookup_VideoNotFound_NOT_FOUND` does not measure repeat queries. |

## 6. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `github.com/hashicorp/golang-lru/v2` | ^2.0 | Generics-friendly, OnEvict callback, well-maintained. |
| `golang.org/x/sync/singleflight` | latest | Already a dep from Story 8.4. |

## 7. Acceptance checklist

**Cache hit (story ACs)**
- [ ] AC-1: 1000 OpenSessions for the same video → 1 DB query (cache hits 999×).

**DB fallback**
- [ ] AC-2: Cold cache produces one `videos JOIN media_info LEFT JOIN audio_tracks` query that populates the row and the cache.

**No on-disk re-probe**
- [ ] AC-3: Missing media_info → `FAILED_PRECONDITION code='video-not-probed'` (mapped by Manager). Streaming never invokes ffprobe.

**Eviction (cross-link to 8.8)**
- [ ] AC-4: After `EvictHashCache`, the next OpenSession for that hash issues a fresh DB query. Verified by spy.

**Robustness**
- [ ] LRU OnEvict prunes byHash index.
- [ ] Single-flight collapses concurrent first-fetches.
- [ ] Subscriber panic in bus does not break other subscribers.

**Observability**
- [ ] `probe_cache_hit_total`, `probe_cache_miss_total`, `probe_cache_evict_total{reason}`, `probe_db_query_duration_seconds`.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[probe] cache_size`.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.15.
