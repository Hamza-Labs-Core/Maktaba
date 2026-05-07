# Implementation Plan — Story 18.3 Streaming Hot-Path Performance

> Companion to [story-18-03-streaming-hot-path.md](story-18-03-streaming-hot-path.md).
> `OpenSession` p95 ≤ 80 ms, manifest ≤ 30 ms, segment first-byte ≤ 100 ms warm.
> Single-flight cold transcodes. Crossing-boundary range serves from two cached segments.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Session manager | `streaming/internal/session/` — gRPC `OpenSession` returns sessionID + manifest URL. |
| Probe cache | `streaming/internal/probe/` — keyed by `(path, size, mtime)`, in-process LRU. Persisted to `videos.probe_data` JSONB on first probe. |
| Manifest builder | `streaming/internal/hls/manifest.go` — pure function over probe data. No FFmpeg call. |
| Segment cache | `streaming/internal/segments/` — disk-backed LRU under `cache_dir/segments/{video_hash}/{rendition}/{seg}.ts`. |
| Single-flight | `golang.org/x/sync/singleflight` keyed by `video_hash:rendition:seg`. |
| Out of scope | HLS spec details (Epic 8); cache size policy (Story 18.8). |

## 1. Project layout

```
streaming/internal/
├── session/
│   ├── manager.go
│   ├── manager_test.go
│   └── grpc.go              # OpenSession handler
├── probe/
│   ├── cache.go
│   ├── ffprobe.go
│   └── cache_test.go
├── hls/
│   ├── manifest.go          # master + media .m3u8
│   ├── manifest_test.go
│   └── media_playlist.go
├── segments/
│   ├── cache.go             # LRU on disk
│   ├── reader.go            # range-aware open
│   ├── transcoder.go        # FFmpeg subprocess
│   ├── singleflight.go
│   └── cache_test.go
├── range/
│   ├── parser.go
│   └── parser_test.go
└── perf_test.go             # 18.3 budgets
```

## 2. OpenSession (gRPC)

```go
// session/grpc.go
func (s *Service) OpenSession(ctx context.Context, in *streamingv1.OpenSessionRequest) (*streamingv1.OpenSessionResponse, error) {
    t := metrics.Time(metrics.OpenSessionDuration)
    defer t.Done()

    v, err := s.videos.GetByID(ctx, in.VideoId)        // covered index
    if err != nil { return nil, err }

    probe, err := s.probeCache.GetOrLoad(ctx, v)       // hot path: in-mem hit
    if err != nil { return nil, err }

    sid := s.sessions.Create(ctx, in.UserId, v, probe)
    return &streamingv1.OpenSessionResponse{
        SessionId:  sid.String(),
        ManifestUrl: s.urls.Manifest(v.Hash, sid),
        Probe:      probe.Wire(),
    }, nil
}
```

Probed previously ⇒ `probe.GetOrLoad` returns from in-memory LRU; only DB hit is the videos lookup. Budget headroom: 80 ms p95 includes the ~15 ms RTT and ~5 ms DB.

## 3. Probe cache

```go
// probe/cache.go
type Probe struct {
    Streams []Stream `json:"streams"`
    Format  Format   `json:"format"`
    Hash    string   `json:"-"`
}

type cacheKey struct{ Path string; Size int64; ModTime int64 }

type Cache struct {
    mu      sync.Mutex
    inmem   *lru.Cache[cacheKey, *Probe]   // 4096 entries
    db      DBProbe                          // videos.probe_data column
    metrics ProbeMetrics
}

func (c *Cache) GetOrLoad(ctx context.Context, v Video) (*Probe, error) {
    key := cacheKey{v.Path, v.Size, v.ModTime.Unix()}
    if p, ok := c.inmem.Get(key); ok { c.metrics.Hits.Inc(); return p, nil }
    c.metrics.Misses.Inc()

    if p, ok, _ := c.db.Load(ctx, v.ID); ok {
        c.inmem.Add(key, p)
        return p, nil
    }
    p, err := c.runFFprobe(ctx, v.Path)
    if err != nil { return nil, err }
    _ = c.db.Save(ctx, v.ID, p)        // best-effort; in-mem authoritative
    c.inmem.Add(key, p)
    return p, nil
}
```

EC3 mapping: `(size, mtime)` change → key change → cache miss → re-probe. The `db.Save` uses `INSERT … ON CONFLICT DO UPDATE` so the entry is overwritten atomically.

## 4. Manifest builder (no FFmpeg)

```go
// hls/manifest.go
type RenditionTier struct {
    Name       string  // "1080p", "720p", "480p", "audio"
    Bandwidth  int
    Resolution string
    Codecs     string
}

func BuildMaster(probe *Probe, tiers []RenditionTier, base string) []byte {
    var b bytes.Buffer
    b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
    for _, r := range tiers {
        if !canSatisfy(probe, r) { continue }
        fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s,CODECS=\"%s\"\n",
            r.Bandwidth, r.Resolution, r.Codecs)
        fmt.Fprintf(&b, "%s/%s/index.m3u8\n", base, r.Name)
    }
    return b.Bytes()
}
```

`canSatisfy` reads `probe.Streams[0].Width/Height`; pure function; benched < 0.1 ms for 5 tiers.

## 5. Segment serve with range

```go
// segments/reader.go
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
    key := keyFromRequest(r)
    f, size, err := h.cache.OpenOrTranscode(r.Context(), key)
    if err != nil { writeErr(w, err); return }
    defer f.Close()

    rng, err := rangepkg.Parse(r.Header.Get("Range"), size)
    switch {
    case err != nil:
        http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable); return
    case rng == nil:
        w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
        w.Header().Set("Content-Type", "video/mp2t")
        _, _ = io.Copy(w, f)
    default:
        w.Header().Set("Content-Length", strconv.FormatInt(rng.Length, 10))
        w.Header().Set("Content-Range", rng.HeaderValue())
        w.Header().Set("Content-Type", "video/mp2t")
        w.WriteHeader(http.StatusPartialContent)
        _, _ = io.CopyN(w, f.SeekTo(rng.Start), rng.Length)
    }
}
```

No chunked encoding (`Content-Length` always set); `io.Copy` zero-copies on Linux via `sendfile` when both sides support it.

## 6. Cross-segment range (EC1)

```go
// segments/multi.go
type spanReader struct {
    parts []*os.File   // ordered cached segments
    rngs  []rangePart  // per-segment offset/length
    cur   int
}
```

Manifest URL pattern: `/stream/{hash}/{rend}/range?start=...&end=...`. Handler parses range, computes covered segment indices via media-playlist offsets, ensures all are present in cache (single-flight transcode each missing one), composes a `spanReader`, serves single response with sum-Content-Length.

## 7. Single-flight cold transcode

```go
// segments/singleflight.go
var sf singleflight.Group

func (c *Cache) OpenOrTranscode(ctx context.Context, k Key) (*os.File, int64, error) {
    if f, sz, ok := c.openCached(k); ok { return f, sz, nil }
    res, err, _ := sf.Do(k.String(), func() (any, error) {
        if f, sz, ok := c.openCached(k); ok { return cached{f, sz}, nil }
        return c.transcode(ctx, k)            // writes atomically: tmp → rename
    })
    if err != nil { return nil, 0, err }
    cv := res.(cached)
    return cv.dup()        // each waiter gets its own *os.File handle
}
```

`transcode` writes to `tmp/{rand}.ts.partial` then `os.Rename` to final path. EC3 mapping: in-progress transcode is in tmp dir, not cache dir, so LRU never sees it.

## 8. Metrics (publishes `transcode_queue_depth`)

| Metric | Description |
|---|---|
| `streaming_open_session_duration_seconds` | histogram |
| `streaming_manifest_duration_seconds` | histogram |
| `streaming_segment_first_byte_seconds{state="warm","cold"}` | histogram |
| `streaming_segment_cache_hits_total` / `_misses_total` | counter |
| `transcode_queue_depth` | gauge — current FFmpeg subprocess count |
| `transcode_in_flight_singleflights` | gauge |
| `probe_cache_hits_total` / `_misses_total` | counter |

## 9. Test cases

### TC1 — 50 concurrent OpenSession
`tests/streaming/session_concurrency_test.go`. Probe DB once via direct SQL pre-test. Then `errgroup` 50 goroutines × `OpenSession`. Assert all p95 ≤ 80 ms, second-onwards p50 < first p50 (probe LRU warm).

### TC2 — 500 segment ranges all warm
Pre-warm cache by transcoding 1 hour worth of segments (~720 segs at 5s). Request 500 with `curl --range`. Assert no FFmpeg subprocess via `pgrep -c ffmpeg | grep -E '^0$'` during loop. Assert p95 ≤ 100 ms.

### TC3 — Forced cold transcode
Hit `POST /admin/cache/segments/evict?hash=…&rendition=720p&seg=000010`. Then GET that segment. Assert first p95 ≤ 6 s; subsequent re-fetch p95 ≤ 100 ms.

### Single-flight TC (EC2)
50 goroutines simultaneously request the same uncached segment. Assert: 1 FFmpeg subprocess (instrument via `transcode_in_flight_singleflights`); all 50 receive identical SHA-256 of payload bytes.

### Cross-boundary TC (EC1)
Cache segments 5 and 6 only; request range that crosses 5↔6 boundary. Assert 1 response, correct total length, no FFmpeg invocation.

### LRU edge TC (EC3)
Set `cache.max_gib=2`. Start 4 cold transcodes serially writing 600 MiB each. Assert: every `tmp/*.partial` survives until rename; final cache contains last 3 (or 4 if quota allows); no truncated `.ts` files.

## 10. Configuration

```yaml
streaming:
  cache_dir: /var/cache/maktaba/streaming
  cache_max_gib: 50
  probe_lru_size: 4096
  ffmpeg:
    bin: ffmpeg
    nice: 5
    threads: 0          # auto
  renditions:
    - name: 1080p
      width: 1920
      height: 1080
      bandwidth: 5_000_000
      codecs: "avc1.640028,mp4a.40.2"
    - name: 720p
      width: 1280
      height: 720
      bandwidth: 2_500_000
      codecs: "avc1.4d4020,mp4a.40.2"
    - name: 480p
      width: 854
      height: 480
      bandwidth: 1_000_000
      codecs: "avc1.4d401f,mp4a.40.2"
```

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 cross-boundary range | story | `spanReader` over two cached files; no re-transcode. |
| EC2 thundering-herd cold | story | `singleflight.Group`; 1 FFmpeg per key. |
| EC3 LRU mid-write | story | tmp dir outside LRU, `os.Rename` at end. |
| Client closes mid-segment | impl | `ctx.Done()` plumbed to `io.CopyN`; FFmpeg killed if no other waiters. |
| Probe DB row stale (file replaced) | impl | `(size, mtime)` cache key invalidates; re-probe. |

## 12. Dependencies

- Epic 8 streaming epic (segment/manifest spec).
- Story 18.8 (cache flush admin endpoint).
- Story 18.1 (budget assertions).
- Story 21.2 (metrics).
