# Implementation Plan — Story 18.2 Search End-to-End Performance

> Companion to [story-18-02-search-performance.md](story-18-02-search-performance.md).
> Hit warm p95 ≤ 500 ms / cold p95 ≤ 1.5 s on a 100 k-segment fixture; degrade
> gracefully when the Pipeline `Embed` call breaches its 200 ms budget.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Owner | `api/internal/search/`. |
| Embedding cache | In-process LRU, 10 k entries, full-text key (not hash). Per-process. |
| Fusion | RRF (Reciprocal Rank Fusion) with k=60 default. Allocation-free pre-sized result slice. |
| Embed gRPC | Hard 200 ms server-side budget (`grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize, …)` + per-call ctx deadline). On breach → FTS-only and `degraded: true` in response. |
| Out of scope | FTS schema/tokenizer — owned by Epic 5 (search-indexing). |

## 1. Project layout

```
api/internal/search/
├── handler.go                # POST /api/search
├── orchestrator.go           # FTS + Embed + Chroma + Fuse
├── embed_cache.go            # LRU
├── embed_cache_test.go
├── fusion.go                 # RRF
├── fusion_test.go
├── degradation.go            # circuit + fallback
├── degradation_test.go
├── perf_test.go              # 18.2 budgets
└── fixtures/
    ├── queries_top100.txt    # warm/cold corpus
    └── queries_unique_500.txt
```

## 2. Embedding cache

```go
// embed_cache.go
type embedCache struct {
    mu   sync.Mutex
    lru  *lru.Cache[string, []float32]   // hashicorp/golang-lru/v2
    hits prometheus.Counter
    miss prometheus.Counter
}

func newEmbedCache(size int) *embedCache {
    c, _ := lru.New[string, []float32](size)
    return &embedCache{
        lru:  c,
        hits: metrics.SearchEmbedCacheHits,
        miss: metrics.SearchEmbedCacheMisses,
    }
}

func (c *embedCache) Get(query string) ([]float32, bool) {
    if v, ok := c.lru.Get(query); ok { c.hits.Inc(); return v, true }
    c.miss.Inc()
    return nil, false
}

func (c *embedCache) Put(query string, vec []float32) {
    c.lru.Add(query, vec)
}
```

**Why full text and not hash:** EC2 of the story prohibits hash collisions. 10 k entries × ~80 bytes avg query ≈ 800 KiB — negligible.

## 3. Embed gRPC call with hard deadline

```go
// orchestrator.go (excerpt)
func (o *Orchestrator) embed(ctx context.Context, q string) ([]float32, bool, error) {
    if v, ok := o.embedCache.Get(q); ok { return v, false, nil }
    callCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
    defer cancel()
    res, err := o.pipeline.Embed(callCtx, &pipelinev1.EmbedRequest{Text: q})
    if err != nil {
        if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
            metrics.SearchEmbedDeadlineExceeded.Inc()
            o.log.Warn("embed deadline exceeded", "q", q)
            return nil, true, nil
        }
        return nil, true, err
    }
    o.embedCache.Put(q, res.Vector)
    return res.Vector, false, nil
}
```

## 4. Orchestrator

```go
func (o *Orchestrator) Search(ctx context.Context, in SearchIn) (SearchOut, error) {
    if strings.TrimSpace(in.Query) == "" {
        return SearchOut{}, ErrEmptyQuery        // 400
    }

    // Fan-out: FTS in parallel with Embed.
    var (
        ftsHits []FTSHit
        vecHits []VecHit
        degraded bool
    )
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error {
        // FTS5 virtual table is `transcripts_fts`; tokenizer is
        // `unicode61 remove_diacritics 2` (canonical per architecture line 1469).
        h, err := o.fts.Query(gctx, in.Query, in.TopK*3)
        ftsHits = h
        return err
    })
    g.Go(func() error {
        vec, deg, err := o.embed(gctx, in.Query)
        if err != nil { return err }
        if deg { degraded = true; return nil }
        h, err := o.chroma.Query(gctx, vec, in.TopK*3)
        vecHits = h
        return err
    })
    if err := g.Wait(); err != nil { return SearchOut{}, err }

    fused := Fuse(ftsHits, vecHits, in.TopK)
    return SearchOut{Hits: fused, Degraded: degraded}, nil
}
```

## 5. RRF fusion (allocation-bounded)

```go
// fusion.go
// SegmentID is int64 because transcript_segments.id is BIGSERIAL
// (architecture line 1369). String-keyed maps would cost an
// strconv.FormatInt per hit and lose type safety.
func Fuse(fts []FTSHit, vec []VecHit, topK int) []Hit {
    const k = 60.0
    // segment_id → score
    score := make(map[int64]float64, len(fts)+len(vec))
    meta  := make(map[int64]Hit, len(fts)+len(vec))

    for i, h := range fts {
        score[h.SegmentID] += 1.0 / (k + float64(i+1))
        if _, ok := meta[h.SegmentID]; !ok { meta[h.SegmentID] = h.AsHit() }
    }
    for i, h := range vec {
        score[h.SegmentID] += 1.0 / (k + float64(i+1))
        if _, ok := meta[h.SegmentID]; !ok { meta[h.SegmentID] = h.AsHit() }
    }
    out := make([]Hit, 0, topK)        // bounded
    keys := make([]int64, 0, len(score))
    for k := range score { keys = append(keys, k) }
    sort.Slice(keys, func(i, j int) bool { return score[keys[i]] > score[keys[j]] })
    for i := 0; i < topK && i < len(keys); i++ {
        h := meta[keys[i]]; h.Score = score[keys[i]]
        out = append(out, h)
    }
    return out
}
```

Allocation test (TC AC3) asserts via `testing.AllocsPerRun` that Fuse runs in ≤ `cap(out)+small_const` allocations.

## 6. Response shape

REST surface is camelCase per architecture §9 (cross-checked with plan-07-08).

```jsonc
{
  "hits": [{ "segmentId": 1234, "videoId": "01H…", "startSec": 12.34, "endSec": 14.10, "snippet": "...", "score": 0.123 }],
  "degraded": false,
  "tookMs": 312
}
```

## 7. Test cases

### TC1 — Load (warm)
`tests/perf/search/load_warm_test.go`. 200 concurrent goroutines × 10 queries each from `queries_top100.txt`. Warm-up: replay corpus once, assert cache size ≥ 100. Then measure: throughput ≥ 50 qps, p95 ≤ 500 ms.

### TC2 — Cache repeat
Replay 100 queries twice. Second pass p95 must be ≥ 30 % faster than first. Assert `search_embed_cache_hit_ratio ≥ 0.90` after second pass.

### TC3 — Degradation
Stop pipeline container mid-test (`docker compose stop pipeline`). Subsequent searches:
- Return non-zero hits.
- Each carries `degraded: true`.
- p95 ≤ 250 ms (FTS-only is faster).
- Counter `search_embed_deadline_exceeded_total` increments.
- After pipeline returns, requests return `degraded: false` again within 30 s.

### TC4 — Cold budget
Wipe embed cache via admin `POST /admin/cache/embed/flush`. Run 100 unique queries (`queries_unique_500.txt[:100]`); assert p95 ≤ 1.5 s and `search_embed_cache_hits_total` == 0 over the run.

### Unit tests
- `fusion_test.go`: identical-rank lists, disjoint lists, empty-list edge, allocations.
- `embed_cache_test.go`: hit/miss counters, eviction at 10 k+1.
- `degradation_test.go`: timeout returns `degraded=true` and FTS hits intact.

## 8. Edge cases

| Case | Handling |
|---|---|
| EC1 — Empty query | Return 400 `EMPTY_QUERY` before any DB hit. Unit test asserts no DB call. |
| EC2 — Single Arabic char | Pass-through to FTS5 `unicode61 remove_diacritics 2` tokenizer (architecture line 1469); assert hits possible (fixture seeded with relevant content). |
| EC3 — RTL+LTR mixed | Highlighter operates on UTF-8 byte ranges; `bidi.Reorder` runs only at render. Test compares snippet bytes pre/post highlight. |
| EC4 — Cache full (10 k unique) | LRU eviction is O(1); benchmark asserts no spike > 2× baseline at insertion 10 001. |
| Pipeline up but slow (180 ms) | Within budget; not degraded. |
| Pipeline up but at 250 ms steady | Each call hits the 200 ms deadline → `degraded: true`. Add warning log throttled to 1/min. |

## 9. Metrics

| Metric | Type | Notes |
|---|---|---|
| `search_request_duration_seconds{cache="hot"\|"warm"\|"cold"}` | histogram | Label values declared explicitly: `hot` = embed-cache hit, `warm` = miss-but-FTS-cached, `cold` = full miss. |
| `search_embed_cache_hits_total` | counter | |
| `search_embed_cache_misses_total` | counter | |
| `search_embed_cache_size` | gauge | |
| `search_embed_deadline_exceeded_total` | counter | |
| `search_degraded_total` | counter | |
| `search_fts_query_duration_seconds` | histogram | sub-budget per Story 5.4. |

## 10. Configuration

```yaml
# api/config.yaml (defaults)
search:
  topk_default: 20
  embed_cache_size: 10000
  embed_deadline_ms: 200
  fusion_k: 60
```

## 11. Dependencies

- Pipeline `Embed` gRPC (Epic 1.3 / pipeline epic).
- ChromaDB query (Epic 5).
- FTS5 schema (Epic 5).
- Story 18.1 budgets (this story registers numbers there).
- Story 21.2 metrics surface.
