# Implementation Plan — Story 19.1 Single-Host Capacity Floor

> Companion to [story-19-01-single-host-capacity.md](story-19-01-single-host-capacity.md).
> Mac mini M2 16 GB / 30 TB SSD must hold 50 k videos, 1 M segments, mix
> playback + pipeline + search at the budget. `make capacity` asserts.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Reference profile | `mac-mini-m2-16gb-30tb` registered in `shared/perf_budgets.yaml`. |
| Capacity fixture | `tests/fixtures/cap-50k/` — 50 k synthetic videos, 1 M segments, 30 TB sparse files. |
| Workload mix | `tests/capacity/mix.go` — 8 direct-play + 4 transcoded + 1 transcribe + 4 indexers + 100 search qps. |
| Make target | `make capacity` — 30 min run with thresholds. |
| Out of scope | Multi-host scale (19.2-19.4); SQLite-only profile is documented separately. |

## 1. Project layout

```
tests/capacity/
├── main.go                       # `make capacity` driver
├── mix.go                        # workload generators
├── thresholds.yaml               # RSS, RPS, error rate thresholds
├── catalog_walk.go
├── playback.go
├── pipeline.go
└── search.go
tests/fixtures/cap-50k/
├── seed.sql
├── generate.go                   # synthesize sparse files
└── content/
    └── (sparse mp4s, hashed)
docs/runbooks/
└── capacity.md
scripts/
├── set-ulimit-mac.sh             # documents ulimit -n 4096
└── set-ulimit-linux.sh
```

## 2. Capacity workload mix

```go
// tests/capacity/mix.go
type Mix struct {
    DirectPlay         int            // 8
    Transcoded         int            // 4
    Transcribers       int            // 1
    Indexers           int            // 4
    SearchQPS          float64        // 100
    Duration           time.Duration  // 30 min
}

func Run(ctx context.Context, m Mix, rig *Rig) Result {
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { return runDirectPlay(gctx, rig, m.DirectPlay) })
    g.Go(func() error { return runTranscoded(gctx, rig, m.Transcoded) })
    g.Go(func() error { return runTranscribe(gctx, rig, m.Transcribers) })
    g.Go(func() error { return runIndex(gctx, rig, m.Indexers) })
    g.Go(func() error { return runSearch(gctx, rig, m.SearchQPS) })
    g.Go(func() error { return collectMetrics(gctx, rig) })
    deadline, _ := context.WithTimeout(gctx, m.Duration)
    <-deadline.Done()
    return rig.Snapshot()
}
```

## 3. Thresholds

```yaml
# tests/capacity/thresholds.yaml
rss_max_mib:
  api: 250
  streaming: 800       # parent only
  pipeline_total: 12000

error_rate_max: 0.001  # 0.1 %

rps_min:
  /api/libraries: 50
  /api/search: 100

ffd_min: 4096          # ulimit -n
buffer_underruns_max: 0

catalog_landing_p95_ms: 500
```

## 4. Fixture generator

```go
// tests/fixtures/cap-50k/generate.go
func main() {
    flag := parse()
    db := open(flag.DSN)
    seedSQL(db)             // libraries + users
    for i := 0; i < 50_000; i++ {
        path := fmt.Sprintf("%s/v%05d.mp4", flag.Root, i)
        // sparse file: write 4 MiB header + 4 MiB tail; size header to N MiB
        writeHashableSparse(path, sizeForBucket(i))
        hash := contentHash(path)
        insertVideo(db, hash, path, durationForBucket(i))
        for s := 0; s < 20; s++ {
            insertSegment(db, hash, s, fakeArabicText(i, s))
        }
    }
}
```

50 k videos × 20 segments avg = 1 M segments. Sparse files mean the actual disk usage is small (~few GB) while declared sizes sum to 30 TB.

## 5. Catalog walk benchmark

```go
// tests/capacity/catalog_walk.go
func WalkCatalog(ctx context.Context, c *http.Client, base string) (time.Duration, error) {
    t0 := time.Now()
    cur := 0
    for {
        url := fmt.Sprintf("%s/api/libraries/%s/videos?cursor=%d&limit=50", base, libID, cur)
        r, err := c.Get(url); if err != nil { return 0, err }
        var page Page; _ = json.NewDecoder(r.Body).Decode(&page); r.Body.Close()
        if len(page.Items) == 0 { break }
        cur = page.NextCursor
    }
    return time.Since(t0), nil
}
```

Asserts `walk ≤ 5 min` and observes RSS samples every 30 s for stability (slope < 1 MiB/h).

## 6. EC1 — slow USB direct-play cap

```go
// streaming/internal/quality/cap.go
func directPlayCap(media MediaInfo, storage StorageProfile) Rendition {
    if storage.SeqMBps < 60 {
        return Rendition{Name: "720p", Width: 1280, Height: 720}
    }
    return media.Native
}
```

Storage profile detected at boot:

```go
func ProbeSeqMBps(dir string) float64 {
    // write 256 MiB then read it; record both rates; take min.
    // result cached in /var/lib/maktaba/storage.json
}
```

Capacity test runs once with `MAKTABA_FORCE_STORAGE_MBPS=50` and asserts: every transcoded session uses 720p ladder.

## 7. EC2 — file-watch fallback

```python
# pipeline/maktaba_pipeline/watch/watcher.py
class Watcher:
    def start(self, root: Path):
        try:
            self._observer = Observer()
            self._observer.schedule(self._handler, str(root), recursive=True)
            self._observer.start()
        except OSError as e:
            if e.errno in (errno.ENOSPC, errno.EMFILE):
                log.warning("inotify exhausted (%s) — falling back to polling", e)
                self._start_polling(root)
            else: raise

    def _start_polling(self, root: Path):
        self._poll_task = asyncio.create_task(self._poll_loop(root, interval=30))
```

Test EC2: set `RLIMIT_NOFILE = 64`, start the watcher; assert `watch_mode == "poll"` metric and that a new file under `root/` is discovered ≤ 60 s.

## 8. EC3 — SQLite documented profile

`shared/perf_budgets.yaml` adds:

```yaml
profiles:
  - tag: mac-mini-m2-16gb-sqlite
    cpu: apple-m2
    mem_gb: 16
    db: sqlite
    capacity:
      videos_max: 12000
      segments_max: 250000
```

`make capacity-sqlite` runs the same mix scaled to 1/4 with explicit assertion that going beyond the documented numbers fails.

## 9. Test cases

### TC1 — Catalog walk
50 k videos, paginate 50/page; total ≤ 5 min; sample RSS every 30 s; slope < 1 MiB/h.

### TC2 — Concurrent playback
8 direct-play sessions, distinct videos, 10 min hold. Assert: `streaming_buffer_underruns_total == 0` for the run.

### TC3 — Mixed workload
Run `Mix{DirectPlay:8, Transcoded:0, Transcribers:1, Indexers:4, SearchQPS:100, Duration:30m}`. Assert search p95 ≤ 500 ms (Story 18.2 budget) holds throughout.

## 10. `make capacity` driver

```makefile
.PHONY: capacity
capacity:
	@./scripts/set-ulimit-mac.sh
	@./scripts/check-fd-limit.sh 4096
	@psql -c "SELECT pg_size_pretty(pg_database_size(current_database()))"
	@go run ./tests/capacity \
	    -profile mac-mini-m2-16gb-30tb \
	    -duration 30m \
	    -thresholds tests/capacity/thresholds.yaml \
	    -fixture cap-50k
```

Exit code != 0 on any threshold breach. Output JSON report → `tests/capacity/results.json`.

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 slow USB | story | `directPlayCap` caps at 720p when seq < 60 MB/s. |
| EC2 file-watch limit | story | Polling fallback at 30 s interval. |
| EC3 SQLite | story | Documented and tested as 1/4 profile. |
| 30 TB sparse fixture not really 30 TB | impl | Declared size in DB matches; on-disk sparseness is OK because tests don't read mid-bytes. |
| FFD count under load | impl | `ulimit -n 4096` checked at start; failure aborts. |

## 12. Runbook

`docs/runbooks/capacity.md`:

- How to launch `make capacity`.
- How to read `results.json`.
- What a breach looks like and what to suspect (RSS leak → 18.5; query plan regression → 18.7; cache cold → 18.8).

## 13. Dependencies

- Story 18.1, 18.2, 18.3 (budgets).
- Story 18.5 (RSS measurement).
- Epic 6 job queue (claim path).
- Epic 5 search (FTS+chroma).
