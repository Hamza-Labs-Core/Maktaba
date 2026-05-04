# Implementation Plan — Story 18.8 Cache Layout & Hit-Rate Floors

> Companion to [story-18-08-cache-layout-hit-rates.md](story-18-08-cache-layout-hit-rates.md).
> Every cache exposes hits/misses/size, has named eviction, can be flushed, and
> meets a documented hit-rate floor after warm-up.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Caches in scope | HLS segments (disk LRU); embedding (in-mem LRU); probe (in-mem LRU + DB-backed); JWKS (TTL); FTS prepared statements (Go `sql.Stmt` LRU). |
| Metrics naming | **Single metric name with a `cache` label** is the canonical convention: `cache_hits_total{cache="hls_segment"}`, `cache_misses_total{cache="…"}`, `cache_size_bytes{cache="…"}`, `cache_size_entries{cache="…"}`. We do NOT publish per-cache prefixed names (e.g. `embedding_cache_hits_total`) — they cause cardinality double-counting and break shared dashboards. |
| Admin endpoints | **Whole-cache flush** is owned here: `POST /admin/cache/{name}/flush` is the canonical URL. Per-key segment eviction (`POST /admin/cache/segments/evict?hash=…&rendition=…&seg=…`) is a different operation owned by plan-18-03. |
| Eviction policy | Each cache instance carries `Policy{Kind: "lru" | "ttl" | "lru_size_singleflight", Capacity: ...}`. |
| Admin endpoint shape | `POST /admin/cache/{name}/flush` on the owning service (this plan owns it). CLI alias: `maktaba-streaming gc`. |
| Out of scope | Cache size tuning (Epic 19); persistent on-disk index for embeddings (out of v1). |

## 1. Project layout

```
shared/cache/
├── policy.go                # Policy enum + descriptor
├── reporter.go              # registers metrics for any cache
├── reporter_test.go
└── flusher.go               # interface { Flush(ctx) error }

api/internal/cache/
├── jwks.go                  # TTL-cached jwks
└── jwks_test.go

streaming/internal/segments/cache.go    # disk LRU (Story 18.3)
streaming/internal/probe/cache.go       # in-mem LRU
api/internal/search/embed_cache.go      # in-mem LRU (Story 18.2)

cmd/maktaba-streaming/cli.go            # `gc` subcommand

tests/cache/
├── replay_session_test.go    # TC1
├── eviction_test.go          # TC2
├── singleflight_test.go      # TC3
└── jwks_rotation_test.go     # EC1
```

## 2. Policy descriptor

```go
// shared/cache/policy.go
type Kind string
const (
    LRU                Kind = "lru"
    TTL                Kind = "ttl"
    LRUSizeSingleFlt   Kind = "lru_size_singleflight"
)

type Policy struct {
    Kind     Kind
    Capacity int64           // entries or bytes depending on Kind
    TTL      time.Duration   // for TTL kind
}

type Cache interface {
    Name() string
    Policy() Policy
    Stats() Stats             // hits, misses, size
    Flush(ctx context.Context) error
}

type Stats struct {
    Hits      uint64
    Misses    uint64
    SizeBytes int64           // -1 if entries-bounded
    Entries   int64
}
```

## 3. Metrics reporter

```go
// shared/cache/reporter.go
type Reporter struct {
    caches []Cache
    hits   *prometheus.CounterVec
    misses *prometheus.CounterVec
    size   *prometheus.GaugeVec
    items  *prometheus.GaugeVec
}

func NewReporter(caches []Cache, reg prometheus.Registerer) *Reporter {
    r := &Reporter{caches: caches,
        hits:   prom.NewCounterVec("cache_hits_total",   []string{"cache"}),
        misses: prom.NewCounterVec("cache_misses_total", []string{"cache"}),
        size:   prom.NewGaugeVec  ("cache_size_bytes",   []string{"cache"}),
        items:  prom.NewGaugeVec  ("cache_size_entries", []string{"cache"}),
    }
    reg.MustRegister(r.hits, r.misses, r.size, r.items)
    return r
}

func (r *Reporter) Tick() {
    for _, c := range r.caches {
        s := c.Stats()
        // Counters never reset to zero on Tick — Prometheus counters are
        // monotonic. Each Cache implementation owns a monotonic uint64 for
        // hits/misses; the Reporter publishes the absolute value via Add()
        // on the delta since the last observation. The "last observed"
        // value is held in `r.lastObserved[c.Name()]` (a map of struct{
        // hits, misses uint64 }), and on Tick we compute
        // delta = current - last, call hits.WithLabelValues(name).Add(delta),
        // and update last = current. Process restart resets last to zero,
        // which matches Prometheus' counter-restart detection.
        r.size.WithLabelValues(c.Name()).Set(float64(s.SizeBytes))
        r.items.WithLabelValues(c.Name()).Set(float64(s.Entries))
    }
}
```

## 4. Admin flush endpoints

```go
// api/internal/admin/cache.go
func (h *Handler) Flush(w http.ResponseWriter, r *http.Request) {
    name := chi.URLParam(r, "name")
    c, ok := h.registry[name]
    if !ok { http.Error(w, "unknown cache", 404); return }
    if err := c.Flush(r.Context()); err != nil {
        http.Error(w, err.Error(), 500); return
    }
    json.NewEncoder(w).Encode(map[string]any{"flushed": name})
}

// router:
// Canonical whole-cache flush — owned by this plan. Per-key segment
// eviction lives at POST /admin/cache/segments/evict (plan-18-03).
r.With(adminAuthOnly).Post("/admin/cache/{name}/flush", h.Flush)
```

`adminAuthOnly` checks for the admin role. CLI counterpart:

```go
// cmd/maktaba-streaming/cli.go
func gcCmd() *cobra.Command {
    return &cobra.Command{
        Use: "gc [cache]",
        RunE: func(cmd *cobra.Command, args []string) error {
            target := "all"
            if len(args) > 0 { target = args[0] }
            return rpcClient.FlushCache(cmd.Context(), target)
        },
    }
}
```

## 5. HLS segment cache (LRU, size-bounded)

```go
// streaming/internal/segments/cache.go (excerpt; primary source Story 18.3)
type SegmentCache struct {
    dir       string
    maxBytes  int64
    list      *lru.Cache[Key, *Entry]
    mu        sync.Mutex
    bytesUsed atomic.Int64
    sf        singleflight.Group
    metrics   cache.Stats
}

func (c *SegmentCache) evictIfOver() {
    target := int64(float64(c.maxBytes) * 0.95)
    for c.bytesUsed.Load() > c.maxBytes {
        _, ent, ok := c.list.RemoveOldest()
        if !ok { break }
        os.Remove(ent.Path)
        c.bytesUsed.Add(-ent.Bytes)
    }
    _ = target // 95 % low-water mark; eviction continues until <= target on burst
}
```

Eviction kind: **`lru_size_singleflight`** — exposed via `Policy()`.

## 6. JWKS TTL cache

```go
// api/internal/cache/jwks.go
//
// JWT/JWKS validation uses github.com/lestrrat-go/jwx/v2/jwk for parsing
// the JWKS document and exposing JOSE-friendly public keys. We do NOT use
// golang.org/x/crypto/ssh — that package speaks SSH wire format, not JWS,
// and its PublicKey type is unrelated to JOSE verification.
import (
    "github.com/lestrrat-go/jwx/v2/jwk"
)

type JWKS struct {
    issuer string
    ttl    time.Duration
    mu     sync.RWMutex
    set    jwk.Set            // parsed JWKS; per-kid lookup via set.LookupKeyID
    expiry time.Time
}

func (c *JWKS) Get(ctx context.Context, kid string) (jwk.Key, error) {
    c.mu.RLock()
    if time.Now().Before(c.expiry) && c.set != nil {
        if k, ok := c.set.LookupKeyID(kid); ok { c.mu.RUnlock(); return k, nil }
    }
    c.mu.RUnlock()
    return c.refresh(ctx, kid)
}

func (c *JWKS) refresh(ctx context.Context, kid string) (jwk.Key, error) {
    c.mu.Lock(); defer c.mu.Unlock()
    if time.Now().Before(c.expiry) && c.set != nil {
        if k, ok := c.set.LookupKeyID(kid); ok { return k, nil }
    }
    set, err := jwk.Fetch(ctx, c.issuer+"/.well-known/jwks.json")
    if err != nil { return nil, err }
    c.set = set
    c.expiry = time.Now().Add(c.ttl)
    if k, ok := set.LookupKeyID(kid); ok { return k, nil }
    return nil, errKidMissing
}
```

EC1 mapping: TTL = 5 min default → new keys picked up within ≤ 5 min.

## 7. Probe cache invalidation (EC3)

Already covered in Story 18.3 plan; the cache key is `content_hash` (architecture §1.5 canonical identity). When file bytes change, the scanner recomputes `content_hash` → cache miss → re-probe. Renames and moves leave `content_hash` unchanged and therefore reuse the cached entry. The DB row update writes to `media_info.raw_ffprobe` JSONB (architecture line 1335) via `INSERT … ON CONFLICT (video_id) DO UPDATE SET raw_ffprobe = EXCLUDED.raw_ffprobe` (atomic).

## 8. Test cases

### TC1 — Replay real-shape session log
```go
func TestHitRateFloors_Replay(t *testing.T) {
    log := loadSessionLog("tests/fixtures/session_replay.jsonl")
    rig := newServiceRig(t)
    rig.WarmUp(log[:len(log)/10])           // 10 % warmup
    rig.Replay(log[len(log)/10:])           // measured

    floors := map[string]float64{
        "hls_segment": 0.70, "embedding": 0.90, "probe": 0.99, "jwks": 0.99,
    }
    for name, floor := range floors {
        s := rig.Stats(name)
        ratio := float64(s.Hits) / float64(s.Hits+s.Misses)
        require.GreaterOrEqual(t, ratio, floor, "%s hit-rate %.3f < %.3f", name, ratio, floor)
    }
}
```

### TC2 — Forced eviction
Fill HLS cache to `max_gib + 5 %` by streaming until `bytes_used > max`. Trigger one more insert; verify eviction loop ran until `bytes_used ≤ max × 0.95`. Subsequent serve continues.

### TC3 — Single-flight
50 goroutines `cache.OpenOrTranscode(sameKey)` where key is uncached. Spy on the FFmpeg invocation count via `transcode_in_flight_singleflights` gauge (instrument inside the singleflight callback). Assert exactly 1; assert all 50 receive identical `sha256(payload)`.

### EC1 — JWKS rotation
Boot rig with TTL=10 s. Issuer publishes new key. Assert: requests with old kid keep validating until TTL elapses; new kid fails until refresh; after 10 s (+ jitter), new kid validates.

### EC2 — Embedding key collision
Test asserts cache.Get keys by full text:
```go
c.Put("foo", v1); c.Put("bar", v2)
got, _ := c.Get("foo"); require.Equal(t, v1, got)
got, _ = c.Get("bar"); require.Equal(t, v2, got)
// adversarial: even if a hypothetical hash collided, full-text key prevents mix-up
```

### EC3 — Probe invalidation on content_hash change
- Probe video → cached (key = `content_hash`).
- Modify file bytes; scanner recomputes `content_hash`.
- Next `OpenSession` sees a new key → re-probes; `media_info.raw_ffprobe` updated atomically; in-mem entry under the new key.
- Pure rename/move (bytes unchanged) keeps `content_hash` and therefore the cached entry — no re-probe.

## 9. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 JWKS rotation | story | TTL refresh; configurable. |
| EC2 embed key collision | story | Cache keys are full text, not hash. |
| EC3 probe `content_hash` | story | Canonical identity key per architecture §1.5; survives moves/renames. |
| Flush concurrent with read | impl | `sync.RWMutex`; readers hold RLock; flush takes Lock. |
| Disk-cache flush partial fail | impl | Walks dir; collects errors; returns `errors.Join` so partial success is still reported. |
| Metric counter wrap | impl | `uint64`; never resets except on process restart. |

## 10. Configuration

This block **extends** architecture §11.3 — the canonical TOML keys
`[cache] root` and `[cache] max_gib` continue to govern on-disk root and
total-budget; the per-cache settings below add policy details that §11.3
does not enumerate. Implementations should merge this block under the
existing `[cache]` table rather than introduce a parallel top-level key.

```toml
# Extends architecture §11.3 [cache] section
[cache]
root    = "/var/cache/maktaba"   # canonical (arch §11.3)
max_gib = 50                      # canonical (arch §11.3)

[cache.hls_segment]
kind          = "lru_size_singleflight"
max_gib       = 50                # may equal or be < cache.max_gib
low_water_pct = 95

[cache.probe]
kind        = "lru"
max_entries = 4096

[cache.embedding]
kind        = "lru"
max_entries = 10000

[cache.jwks]
kind        = "ttl"
ttl_seconds = 300

[cache.fts_stmt]
kind        = "lru"
max_entries = 256
```

## 11. Runbook

`docs/runbooks/cache-flush.md` — when, why, and how:

- Symptom: stale segments after switching codecs → `maktaba-streaming gc hls_segment`.
- Symptom: 401s after JWT issuer key rotation → `curl -X POST /admin/cache/jwks/flush`.

## 12. Dependencies

- Story 18.2 (embedding cache).
- Story 18.3 (segment cache, probe cache).
- Story 18.5 (RSS envelopes verify cache sizing).
- Story 21.2 (metrics).
- Epic 10 (admin auth role for `/admin/cache/...`).
