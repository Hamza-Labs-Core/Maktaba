# Implementation Plan — Story 8.14 Cache Layout, LRU GC, Cap Enforcement

> Companion to [story-08-14-cache-gc.md](story-08-14-cache-gc.md).
> The story states *what* and *why*; this plan states *how*. Architecture
> reference: [§4.8](../../architecture.md#48-cache-layout). Per-session
> HLS dirs are excluded from this cap and managed by [Story 8.9](plan-08-09-session-store.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/cache/gc/` — uses the existing `cache.Store` (introduced in Story 8.4) for paths and `.tmp` semantics. |
| Eviction trigger | Periodic (default 5 min) + on-demand (operator command, ENOSPC fallback). |
| Eviction signal | atime where supported; mtime + bbolt access counter where `noatime` is mounted. |
| Per-tier caps | Soft floor of 1 GiB for posters and sprites; remux is preferentially evicted because it regenerates in seconds. |
| Cap detection | `mountflag` probe at boot writes one file, reads its atime back; if atime didn't bump after the read, we mark the cache as `noatime` and switch to the bbolt sidecar. |
| Operator command | `maktaba-streaming gc` — same binary, subcommand. |
| Out of scope | `cache/hls/{session_id}/` — purged on CloseSession or reap (Story 8.9). `cache/direct/` — transient (Story 8.3 doesn't write here in v1; reserved). |

## 1. Architecture diagram

```
            ┌──────────────────────────────────────────────────────┐
            │ /var/maktaba/cache/streaming/                        │
            │   direct/                  ← reserved, not GC'd       │
            │   remux/{hash[:2]}/{hash}_{target}_a*_s*.{ext}        │
            │   hls/{session_id}/        ← excluded (Story 8.9)     │
            │   posters/{hash[:2]}/{hash}.jpg                       │
            │   sprites/{hash[:2]}/{hash}.webp                      │
            │   sprites/{hash[:2]}/{hash}.vtt                       │
            │   thumbs/{hash[:2]}/{hash}/chapter-N.jpg              │
            │   subs/{hash}/{lang}.vtt                              │
            └──────────────────────────────────────────────────────┘
                          │
                          ▼
            ┌──────────────────────────────────────────────────────┐
            │  gc.Worker                                           │
            │   - tick every gc_interval (5 min)                    │
            │   - scan: filepath.WalkDir under each managed tier    │
            │   - skip: hls/, .tmp.* < 1m old, current direct/      │
            │   - read access timestamp:                            │
            │       atime supported → file.AccessTime()             │
            │       atime missing   → bbolt[lookup(path)]           │
            │   - sort: oldest first within tier; tier priority     │
            │     remux (highest evict score) > thumbs > subs       │
            │     > sprites > posters (lowest)                      │
            │   - delete: until usage ≤ max_gib * 0.9               │
            └──────────────────────────────────────────────────────┘
                          │
                          ▼  os.Remove (file unlinked; open FDs unaffected)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/cache/gc/worker.go` | `Worker.Run(ctx)`, periodic + manual mode. |
| `streaming/internal/cache/gc/scan.go` | `scanTier(root, tier, accessFn) []entry` — `WalkDir` traversal. |
| `streaming/internal/cache/gc/atime.go` | `detectAtimeSupport(root)` + `atime.Stat(path)` helpers. |
| `streaming/internal/cache/gc/access_db.go` | bbolt fallback for filesystems without atime. |
| `streaming/internal/cache/gc/select.go` | `selectVictims(entries, target int64) []entry` — eviction policy. |
| `streaming/internal/cache/gc/cli.go` | `RunOnce(cfg) Report` — invoked from `maktaba-streaming gc`. |
| `streaming/internal/cache/gc/worker_test.go` | Unit tests. |
| `streaming/internal/cache/gc/select_test.go` | Eviction-policy tests. |
| `streaming/internal/cache/gc/atime_test.go` | atime detection. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/cmd/maktaba-streaming/main.go` | Add `gc` subcommand; wire periodic worker; subscribe to ENOSPC events from cache writers. |
| `streaming/internal/cache/store.go` | Surface `RecordAccess(path)` (called by every successful read); under bbolt mode, this updates the access counter. |
| `streaming/internal/observability/metrics.go` | `cache_gc_runs_total{trigger}`, `cache_gc_evicted_files_total{tier}`, `cache_gc_freed_bytes_total{tier}`, `cache_gc_duration_seconds`, `cache_used_bytes{tier}`. |
| `streaming/configs/streaming.toml.example` | `[cache] interval_sec = 300`, `headroom_pct = 10`, `posters_floor_gib = 1`. |
| `specs/epics/08-streaming/README.md` | Tick 8.14. |

### 2.3 Type definitions

```go
// streaming/internal/cache/gc/worker.go
package gc

import (
    "context"
    "errors"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
    "sync/atomic"
    "time"
)

type Tier string

const (
    TierRemux   Tier = "remux"
    TierThumbs  Tier = "thumbs"
    TierSubs    Tier = "subs"
    TierSprites Tier = "sprites"
    TierPosters Tier = "posters"
)

// EvictionPriority orders tiers from "evict first" to "evict last".
// Lower index = preferred to evict.
var EvictionPriority = []Tier{TierRemux, TierThumbs, TierSubs, TierSprites, TierPosters}

type Config struct {
    Root             string
    MaxBytes         int64           // bytes; 0 = unlimited
    HeadroomPct      int             // free 10% beyond cap when GC fires
    Interval         time.Duration   // 5m default
    PostersFloorBytes int64          // soft floor for posters; 1 GiB default
    SpritesFloorBytes int64          // ditto
    Tier             struct{}
    AtimeSupported   bool            // detected at boot
    AccessDB         *AccessDB       // bbolt fallback; nil when atime works
    NoOpTmpAge       time.Duration   // .tmp.* younger than this are skipped (1m default)
    Metrics          *Metrics
    Now              func() time.Time
}

type Report struct {
    EvictedFiles int
    FreedBytes   int64
    DurationMs   int64
    PerTier      map[Tier]struct{ Files int; Bytes int64 }
}

func RunOnce(ctx context.Context, cfg Config, trigger string) (Report, error)

type Worker struct {
    Cfg       Config
    InFlight  atomic.Bool
    onENOSPC  chan struct{}
}

func (w *Worker) Run(ctx context.Context) error
func (w *Worker) Trigger(reason string) // for ENOSPC fast path
```

### 2.4 Scan implementation

```go
// streaming/internal/cache/gc/scan.go
package gc

type entry struct {
    Path     string
    Tier     Tier
    Size     int64
    AccessAt time.Time
}

func scanTier(ctx context.Context, root string, tier Tier, atimeOf func(string, os.FileInfo) time.Time, noOpTmpAge time.Duration, now time.Time) ([]entry, int64, error) {
    base := filepath.Join(root, string(tier))
    info, err := os.Stat(base)
    if errors.Is(err, fs.ErrNotExist) {
        return nil, 0, nil
    } else if err != nil {
        return nil, 0, err
    }
    if !info.IsDir() {
        return nil, 0, nil
    }

    var out []entry
    var total int64
    err = filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
        if walkErr != nil {
            return walkErr
        }
        select { case <-ctx.Done(): return ctx.Err(); default: }
        if d.IsDir() {
            return nil
        }
        // Skip in-progress writes.
        if isTempName(d.Name()) {
            st, err := d.Info()
            if err != nil {
                return nil
            }
            if now.Sub(st.ModTime()) < noOpTmpAge {
                return nil
            }
            // Old .tmp files are safe to evict (FFmpeg crashed before rename).
        }
        st, err := d.Info()
        if err != nil {
            return nil
        }
        out = append(out, entry{
            Path: path, Tier: tier, Size: st.Size(),
            AccessAt: atimeOf(path, st),
        })
        total += st.Size()
        return nil
    })
    return out, total, err
}

func isTempName(n string) bool {
    return strings.Contains(n, ".tmp.")
}
```

### 2.5 Eviction policy

```go
// streaming/internal/cache/gc/select.go
package gc

// selectVictims picks files to evict to bring `total` down to `target`.
// Algorithm:
//   1. Bucket entries by tier.
//   2. Sort each bucket oldest-first by AccessAt.
//   3. Iterate tiers in EvictionPriority order; within each tier, take
//      oldest entries until either:
//        - we've freed enough (total - freed <= target), or
//        - we've reached the tier's soft floor (e.g. posters ≥ 1 GiB).
//   4. If after exhausting all tiers we're still over target, we
//      crossed the soft floors — evict from posters/sprites anyway.
func selectVictims(entries []entry, total, target int64, cfg Config) []entry {
    if total <= target {
        return nil
    }

    tiered := make(map[Tier][]entry)
    for _, e := range entries {
        tiered[e.Tier] = append(tiered[e.Tier], e)
    }
    for k := range tiered {
        sort.Slice(tiered[k], func(i, j int) bool {
            return tiered[k][i].AccessAt.Before(tiered[k][j].AccessAt)
        })
    }

    sizesByTier := tierSizes(entries)
    floors := map[Tier]int64{
        TierPosters: cfg.PostersFloorBytes,
        TierSprites: cfg.SpritesFloorBytes,
    }

    var victims []entry
    freed := int64(0)
    for _, tier := range EvictionPriority {
        if total-freed <= target {
            return victims
        }
        bucket := tiered[tier]
        floor := floors[tier]
        for _, e := range bucket {
            if total-freed <= target {
                return victims
            }
            if floor > 0 && (sizesByTier[tier]-evictedFromTier(victims, tier)) <= floor {
                break
            }
            victims = append(victims, e)
            freed += e.Size
        }
    }
    if total-freed <= target {
        return victims
    }
    // Crossed the floors — second pass without floor enforcement.
    for _, tier := range EvictionPriority {
        bucket := tiered[tier]
        for _, e := range bucket {
            if alreadyVictim(victims, e) {
                continue
            }
            if total-freed <= target {
                return victims
            }
            victims = append(victims, e)
            freed += e.Size
        }
    }
    return victims
}
```

### 2.6 atime detection

```go
// streaming/internal/cache/gc/atime.go
package gc

import (
    "os"
    "path/filepath"
    "time"
)

// detectAtimeSupport writes a temp file under root, reads it back, and
// checks whether the access time bumped. If atime tracking is disabled
// (noatime mount) we'll fall through to the bbolt sidecar.
func detectAtimeSupport(root string) (bool, error) {
    p := filepath.Join(root, ".gc-atime-probe")
    if err := os.WriteFile(p, []byte("probe"), 0o644); err != nil {
        return false, err
    }
    defer os.Remove(p)

    st1, err := os.Stat(p)
    if err != nil { return false, err }
    a1 := atimeOf(st1)

    time.Sleep(50 * time.Millisecond)

    if _, err := os.ReadFile(p); err != nil {
        return false, err
    }
    st2, err := os.Stat(p)
    if err != nil { return false, err }
    a2 := atimeOf(st2)

    return a2.After(a1), nil
}
```

`atimeOf` is OS-specific (build-tagged), reading `Stat_t.Atim`/`Atimespec`.
On Windows we always return false (use the bbolt fallback).

### 2.7 bbolt access DB

```go
// streaming/internal/cache/gc/access_db.go
package gc

import (
    "encoding/binary"
    "time"

    bbolt "go.etcd.io/bbolt"
)

const accessBucket = "access"

type AccessDB struct {
    db *bbolt.DB
}

func OpenAccessDB(path string) (*AccessDB, error)
func (a *AccessDB) Touch(relPath string) error    // store time.Now() unix-nanos
func (a *AccessDB) Lookup(relPath string) (time.Time, bool, error)
func (a *AccessDB) Close() error
```

`AccessDB.Touch` is called by `cache.Store.RecordAccess` after every
successful read. `Touch` is fast (one bbolt write) but we still batch it
across the request (single transaction at most once per 100 ms per
process) so that 100 sprite-tile fetches don't produce 100 transactions.

### 2.8 Worker.Run

```go
// streaming/internal/cache/gc/worker.go (continued)

func (w *Worker) Run(ctx context.Context) error {
    t := time.NewTicker(w.Cfg.Interval)
    defer t.Stop()

    for {
        select {
        case <-ctx.Done():
            return nil
        case <-t.C:
            w.runOnce(ctx, "tick")
        case <-w.onENOSPC:
            w.runOnce(ctx, "enospc")
        }
    }
}

func (w *Worker) Trigger(reason string) {
    select {
    case w.onENOSPC <- struct{}{}:
    default:
        // already scheduled
    }
}

func (w *Worker) runOnce(ctx context.Context, trigger string) {
    if !w.InFlight.CompareAndSwap(false, true) {
        return
    }
    defer w.InFlight.Store(false)

    rep, err := RunOnce(ctx, w.Cfg, trigger)
    if err != nil {
        slog.WarnContext(ctx, "cache.gc.run", "err", err, "trigger", trigger)
        return
    }
    w.Cfg.Metrics.RunsTotal.WithLabelValues(trigger).Inc()
    for tier, agg := range rep.PerTier {
        w.Cfg.Metrics.EvictedFiles.WithLabelValues(string(tier)).Add(float64(agg.Files))
        w.Cfg.Metrics.FreedBytes.WithLabelValues(string(tier)).Add(float64(agg.Bytes))
    }
    w.Cfg.Metrics.DurationSeconds.Observe(float64(rep.DurationMs) / 1000)
}
```

### 2.9 ENOSPC integration

`cache.Store.AtomicWrite` already returns errors from the inner write.
We wrap the `os.Rename` call:

```go
// streaming/internal/cache/store.go (modified)
func (s *Store) AtomicWrite(finalPath string, write func(io.Writer) error) error {
    ...
    if err := os.Rename(tmp, finalPath); err != nil {
        if errors.Is(err, syscall.ENOSPC) {
            s.GCTrigger("enospc") // hook installed by main.go
        }
        return err
    }
    ...
}
```

Callers convert `syscall.ENOSPC` into HTTP 507 `Insufficient Storage`
on the request path. The metric `cache_writes_enospc_total` ticks.

## 3. Test plan

### 3.1 Layout test (`scan_test.go`)

| Test | What it pins |
|---|---|
| `TestScan_TwoCharShard` | Files at `posters/ab/abcdef...jpg` are discovered; flat layout (`posters/abcdef.jpg`) is **not** picked up (we trust Pipeline + Story 8.13 to write under shards). AC-1. |
| `TestScan_SkipsTmpFilesUnderOneMin` | A `.tmp.{pid}` file with mtime now → skipped. |
| `TestScan_PicksUpStaleTmpFiles` | A `.tmp.{pid}` file with mtime now-10m → eligible for eviction. |
| `TestScan_ExcludesHLSDir` | `cache/hls/{sid}/seg-0.ts` exists → not in scan output (it's tier=hls, not in EvictionPriority). |

### 3.2 Selection (`select_test.go`)

| Test | What it pins |
|---|---|
| `TestSelect_BasicEviction` | total=60 GiB, target=27 GiB; entries cover all tiers; victims sum ≥ 33 GiB. |
| `TestSelect_RemuxEvictedFirst` | total=10 GiB (8 remux + 2 posters), target=5 GiB; only remux entries selected. AC-3. |
| `TestSelect_PostersFloorPreserved` | total=8 GiB (5 remux + 3 posters), target=4 GiB, posters_floor=1 GiB; victims=4 GiB of remux + 0 GiB posters; if more freeing needed, evict posters until they're at 1 GiB (still > floor) before crossing floor. |
| `TestSelect_FloorCrossedAsLastResort` | total=2 GiB (0 remux + 2 posters), target=1 GiB, posters_floor=1.5 GiB → second-pass evicts posters past the floor; victims=1 GiB. AC-3. |
| `TestSelect_OldestFirstWithinTier` | All same tier; access times spread; victims are the oldest first. |
| `TestSelect_NothingToEvict_NoOp` | total < target → empty victim list. |

### 3.3 atime detection (`atime_test.go`)

| Test | What it pins |
|---|---|
| `TestDetectAtimeSupport_Tmpfs` | Probe on tmpfs (typical CI) → result is environment-dependent; the test asserts the probe terminates and returns either true or false (no panic). |
| `TestDetectAtimeSupport_NoatimeFallback` | Mount fixture with `MS_NOATIME` (skipped on macOS/Windows) → probe returns false. |
| `TestAccessDB_TouchAndLookup` | Touch then Lookup → returns the stored time within 1 ms. |
| `TestAccessDB_BatchedTouch` | 1000 Touch calls per second on the same path → one bbolt update per 100 ms (debounced). |

### 3.4 Worker integration (`worker_test.go`)

Uses a real filesystem under `t.TempDir()`.

| Test | What it pins |
|---|---|
| `TestWorker_FillThenGC` | Write 60 GiB worth of fake files (tmpfs sparse files OK); `max_gib=30`; run RunOnce; usage drops to ≤ 27 GiB. AC-2 acceptance. |
| `TestWorker_TierPriority` | Same fixtures with mixed tiers; remux evicted before posters/sprites. |
| `TestWorker_RespectsInFlightReads` | Open a file FD, run GC that unlinks it, confirm the FD still reads (POSIX inode survival). AC story acceptance. |
| `TestWorker_ENOSPCFastPath` | Send ENOSPC trigger; the next runOnce triggers immediately, not on the timer; in-flight check prevents two concurrent runs. |
| `TestWorker_ManualGC` | `RunOnce(...)` returns a `Report` with EvictedFiles, FreedBytes, DurationMs; matches actual filesystem changes. AC-4. |

### 3.5 Stress

`TestStress_GCDuringConcurrentWrites` — 4 goroutines writing fresh
`.tmp` → atomic rename files at 1 MiB/s; GC ticks every 100 ms;
no file is incorrectly deleted (no `os.Remove` on a `.tmp` < 1 min); no
data loss (final renamed files all exist after the test).

## 4. Test code scaffolding

```go
// streaming/internal/cache/gc/select_test.go
package gc_test

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/cache/gc"
)

func newEntry(tier gc.Tier, size int64, age time.Duration) gc.Entry {
    return gc.Entry{
        Path: "/tmp/" + string(tier) + "/" + uuid.NewString(),
        Tier: tier, Size: size,
        AccessAt: time.Now().Add(-age),
    }
}

func TestSelect_RemuxEvictedFirst(t *testing.T) {
    entries := []gc.Entry{}
    for i := 0; i < 8; i++ {
        entries = append(entries, newEntry(gc.TierRemux, 1<<30, time.Duration(i)*time.Hour))
    }
    for i := 0; i < 2; i++ {
        entries = append(entries, newEntry(gc.TierPosters, 1<<30, time.Hour))
    }
    cfg := gc.Config{
        PostersFloorBytes: 1<<30,
    }
    victims := gc.SelectVictims(entries, 10<<30, 5<<30, cfg)
    for _, v := range victims {
        require.Equal(t, gc.TierRemux, v.Tier, "non-remux evicted before remux")
    }
    var freed int64
    for _, v := range victims { freed += v.Size }
    require.GreaterOrEqual(t, freed, int64(5<<30))
}

func TestSelect_FloorCrossedAsLastResort(t *testing.T) {
    entries := []gc.Entry{
        newEntry(gc.TierPosters, 2<<30, 24*time.Hour),
    }
    cfg := gc.Config{
        PostersFloorBytes: int64(1.5 * (1 << 30)),
    }
    victims := gc.SelectVictims(entries, 2<<30, 1<<30, cfg)
    var freed int64
    for _, v := range victims { freed += v.Size }
    require.GreaterOrEqual(t, freed, int64(1<<30))
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Cap lowered below current usage at runtime | GC catches up next tick. Aggressive single-pass eviction is gated on a `--aggressive` flag to avoid IO storms. | `TestWorker_FillThenGC` (configurable variant). |
| Concurrent writes during GC | Files written under `.tmp.<pid>.<rand>` and atomically renamed; GC ignores `.tmp.*` younger than 1 min. | `TestScan_SkipsTmpFilesUnderOneMin` |
| ENOSPC on cache write | Returns 507 `Insufficient Storage` to the client; counter ticks; `Worker.Trigger("enospc")` schedules an immediate run. | `TestWorker_ENOSPCFastPath` |
| Filesystem with `noatime` mount | atime probe at boot detects this; GC switches to bbolt access counter. | `TestDetectAtimeSupport_NoatimeFallback` |
| GC runs while a file is being read (open FD) | `os.Remove` unlinks the directory entry; the open FD continues serving (POSIX inode survival). | `TestWorker_RespectsInFlightReads` |
| Pipeline crashes mid-write of a poster (0-byte file) | Story 8.13 treats 0-byte as missing → 404; GC eventually evicts as a regular old file. | Cross-link to Story 8.13. |
| Two concurrent GC runs (operator + scheduler) | `Worker.InFlight` atomic CAS gates; the second is a no-op. | `TestWorker_NoConcurrentRuns` |
| GC selects a `.tmp` that's just before the 1m threshold | Slack: the file may be picked up; that's tolerable because Pipeline retries. (Worst case: one extra retry.) | Documented; covered by stress test. |
| Floor on posters but only posters present | Without a fallback path, GC would loop forever; the second-pass branch crosses the floor as last resort. | `TestSelect_FloorCrossedAsLastResort` |
| `cache_used_gib` exceeds cap by 100% | Same algorithm; just takes more iterations. We log a WARN if > 2× cap is observed. | Implicit. |
| Cache root mounted on multiple disks (symlinked tiers) | We use `filepath.WalkDir` per tier root; symlinks are followed if `Cfg.FollowSymlinks=true` (default false). Documented. | Implicit. |

## 6. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `go.etcd.io/bbolt` | ^1.3 | Tiny embedded KV used only when atime is unavailable. Single-process, no daemon, atomic. |
| Linux `Stat_t.Atim` (build-tagged) | stdlib | atime read. |
| `syscall.ENOSPC` | stdlib | ENOSPC sentinel. |

## 7. Acceptance checklist

**Layout (story ACs)**
- [ ] AC-1: Files live at the §4.8 paths exactly, with two-char hash shards.
- [ ] `cache/hls/{session_id}/` is excluded from this GC (owned by Story 8.9).

**LRU eviction**
- [ ] AC-2: After GC, total usage ≤ `max_gib * 0.9` (10% headroom).
- [ ] atime read where supported; bbolt fallback on noatime mounts.

**Per-tier soft caps**
- [ ] AC-3: Remux preferentially evicted before posters/sprites.
- [ ] Posters and sprites have a 1 GiB soft floor before being evicted.
- [ ] Floor crossed only as last resort.

**Manual command**
- [ ] AC-4: `maktaba-streaming gc` runs and prints `{evicted_files, freed_gib, duration_ms}` to stdout.

**Concurrency safety**
- [ ] In-flight reads survive GC (open FD continues).
- [ ] `.tmp.*` files younger than 1 min are not deleted.
- [ ] Two concurrent GC runs are coalesced via atomic CAS.

**ENOSPC**
- [ ] Cache writes return 507 on ENOSPC; counter ticks; GC fires immediately.

**Observability**
- [ ] `cache_gc_runs_total{trigger}`, `cache_gc_evicted_files_total{tier}`, `cache_gc_freed_bytes_total{tier}`, `cache_gc_duration_seconds`, `cache_used_bytes{tier}`.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[cache]` block.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.14.
