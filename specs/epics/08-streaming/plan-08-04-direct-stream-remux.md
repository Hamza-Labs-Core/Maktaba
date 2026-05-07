# Implementation Plan — Story 8.4 Direct Stream (FFmpeg `-c copy` Remux)

> Companion to [story-08-04-direct-stream-remux.md](story-08-04-direct-stream-remux.md).
> The story states *what* and *why*; this plan states *how*. Builds on
> the range-server in [Story 8.3](plan-08-03-direct-play.md), the verdict
> from [Story 8.2](plan-08-02-capability-matrix.md), and the cache layout
> from [Story 8.14](plan-08-14-cache-gc.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/handlers/remux` — same shape as `direct/`. |
| FFmpeg invocation | `streaming/internal/ffmpeg/remux.go` — pure orchestration; no HTTP. |
| Cache key | `BLAKE3` `content_hash` (from probe row) + container target + audio/subtitle track selection. Path: `cache/remux/{hash[:2]}/{hash}_{target}.{ext}`. |
| Single-flight | Per `cache_key` lock from `sync/singleflight.Group`; the second concurrent caller waits on the first's FFmpeg. |
| Streaming-write vs cache-then-stream | Default: **cache-then-stream** (AC-1). Streaming-write (AC-3) is opt-in via `[remux] streaming_write = true`; ships off-by-default because chunked transfer-encoding without Content-Length surprises some clients. |
| Out of scope | The LRU eviction itself (Story 8.14). The matrix decision that this is a remux candidate (Story 8.2). The session opening that triggered the remux (Story 8.8). |

## 1. Architecture diagram

```
            client GET /stream/direct/{video_id}?sig=…
                          │
                          ▼  matrix verdict = remux
            ┌──────────────────────────────────────────────────┐
            │ remux.Handler                                    │
            │   1. probe.Lookup(video_id) → row                │
            │   2. cacheKey = hash + target + tracks           │
            │   3. cache.Path(cacheKey)                        │
            │   4. if exists → reuse direct.Handler.serveFile  │
            │      (AC-2: zero FFmpeg)                         │
            │   5. else → singleflight.Do(cacheKey, ...) {     │
            │        ffmpeg.RemuxToTemp(src, target, tracks)   │
            │        os.Rename(temp, final)                    │
            │      }                                           │
            │      while running: 503 + Retry-After: 2 (AC-1)  │
            │   6. on completion → 302 to /stream/cached/{key} │
            │      OR pass-through serve (configurable)        │
            └──────────────────────────────────────────────────┘
                          │
                          ▼  ffmpeg child process
            ┌──────────────────────────────────────────────────┐
            │ ffmpeg -i src                                    │
            │   -map 0:v:{vidx} -c:v copy                      │
            │   -map 0:a:{aidx} -c:a copy                      │
            │   -movflags +faststart+frag_keyframe+empty_moov  │
            │   -f mp4 cache/remux/{...}.mp4.tmp.{pid}         │
            │ os.Rename → cache/remux/{...}.mp4                │
            └──────────────────────────────────────────────────┘
```

The handler returns the bytes by re-using `direct.Handler.serveFile` —
range-served reads of the cached MP4 are identical to direct play once
the file exists. We don't duplicate range logic.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/handlers/remux/handler.go` | The HTTP handler; orchestrates cache lookup, single-flight, and the 503/200 split. |
| `streaming/internal/handlers/remux/cache_key.go` | Deterministic `cacheKey(probeRow, target, audioIdx, subIdx)`. |
| `streaming/internal/handlers/remux/handler_test.go` | Unit + integration tests. |
| `streaming/internal/ffmpeg/remux.go` | `RemuxToTemp(ctx, in, out, plan) error` — wraps `os/exec`, plumbs stderr to a buffer for diagnostics. |
| `streaming/internal/ffmpeg/remux_test.go` | FFmpeg-binary-required tests (skip when `MAKTABA_SKIP_FFMPEG_TESTS=1`). |
| `streaming/internal/cache/store.go` | Generic `cache.Store` (path computation, atomic rename, in-progress detection). Used here and by Stories 8.11/8.13. |
| `streaming/internal/cache/store_test.go` | Race tests for atomic rename and in-progress detection. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/handlers/direct/handler.go` | When verdict.Mode == remux, delegate to `remux.Handler` instead of returning 409 (AC-4 of Story 8.3 still applies for transcode). |
| `streaming/internal/server/router.go` | Wire `remux.Handler` to share the `/stream/direct/{video_id}` route via the verdict-based dispatcher. |
| `streaming/internal/observability/metrics.go` | `remux_started_total{target}`, `remux_completed_total{target}`, `remux_failed_total{reason}`, `remux_cache_hit_total`, `remux_size_bytes` histogram. |
| `streaming/configs/streaming.toml.example` | `[remux] streaming_write = false`, `max_concurrent = 4`. |
| `specs/epics/08-streaming/README.md` | Tick 8.4. |

### 2.3 Type definitions

```go
// streaming/internal/handlers/remux/cache_key.go
package remux

import (
    "fmt"
    "strings"
)

// CacheKey is a content-addressable identifier for a remuxed file.
// Stable across restarts; ignores anything that doesn't change the bytes.
type CacheKey struct {
    ContentHash string  // BLAKE3 hex of source — from probe.Row
    Target      string  // 'mp4', 'mov', 'ts'
    AudioIdx    int     // -1 for "default"
    SubIdx      int     // -1 for "no embedded subs"
}

func (k CacheKey) String() string {
    return fmt.Sprintf("%s_%s_a%d_s%d", k.ContentHash, k.Target, k.AudioIdx, k.SubIdx)
}

func (k CacheKey) Filename() string {
    return fmt.Sprintf("%s_a%d_s%d.%s", k.ContentHash, k.AudioIdx, k.SubIdx, k.Target)
}

func (k CacheKey) Path(root string) string {
    return strings.Join([]string{root, "remux", k.ContentHash[:2], k.Filename()}, "/")
}
```

```go
// streaming/internal/ffmpeg/remux.go
package ffmpeg

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "strconv"
    "strings"
    "syscall"
    "time"
)

// Plan is the structured input to RemuxToTemp; we never build CLI args
// from raw user input.
type Plan struct {
    InputPath   string
    OutputPath  string  // .tmp file; caller renames to final on success
    Target      string  // 'mp4', 'mov', 'ts'
    VideoIdx    int     // 0 by default; from probe.Row.VideoStream.Index
    AudioIdx    int     // -1 for "skip"
    SubtitleIdx int     // -1 for "skip"
    StartSec    float64 // usually 0; settable for seek-on-remux
}

// RemuxToTemp runs ffmpeg `-c copy` and writes to a .tmp file. Returns
// the FFmpeg stderr as part of the error message on failure for ops.
//
// The caller is responsible for the os.Rename(.tmp → final). This split
// lets us atomic-publish only on success and lets the cache GC ignore
// .tmp files younger than 1 minute.
func RemuxToTemp(ctx context.Context, p Plan, ffmpegBin string) error {
    args := []string{
        "-hide_banner", "-loglevel", "error",
        "-y",
    }
    if p.StartSec > 0 {
        args = append(args, "-ss", strconv.FormatFloat(p.StartSec, 'f', 3, 64))
    }
    args = append(args, "-i", p.InputPath)

    args = append(args, "-map", fmt.Sprintf("0:v:%d", p.VideoIdx), "-c:v", "copy")
    if p.AudioIdx >= 0 {
        args = append(args, "-map", fmt.Sprintf("0:a:%d", p.AudioIdx), "-c:a", "copy")
    }
    if p.SubtitleIdx >= 0 && p.Target != "ts" {
        // Subtitle remux into MP4 only works for compatible formats.
        // For TS we drop subtitles entirely; sidecar VTT covers it.
        args = append(args, "-map", fmt.Sprintf("0:s:%d", p.SubtitleIdx), "-c:s", "mov_text")
    }

    switch p.Target {
    case "mp4", "mov":
        args = append(args,
            "-movflags", "+faststart+frag_keyframe+empty_moov+default_base_moof",
            "-f", p.Target)
    case "ts":
        args = append(args, "-f", "mpegts")
    default:
        return fmt.Errorf("ffmpeg.Remux: unsupported target %q", p.Target)
    }
    args = append(args, p.OutputPath)

    cmd := exec.CommandContext(ctx, ffmpegBin, args...)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group → clean kill
    cmd.Stdin = nil
    cmd.Stdout = nil

    if err := cmd.Start(); err != nil {
        return fmt.Errorf("ffmpeg.Remux: start: %w", err)
    }

    done := make(chan error, 1)
    go func() { done <- cmd.Wait() }()

    select {
    case <-ctx.Done():
        // Graceful: SIGTERM, wait 2s, SIGKILL. Matches Story 8.8's CloseSession.
        _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
        select {
        case <-done:
        case <-time.After(2 * time.Second):
            _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
            <-done
        }
        return ctx.Err()
    case err := <-done:
        if err != nil {
            return fmt.Errorf("ffmpeg.Remux: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
        }
    }
    return nil
}
```

```go
// streaming/internal/handlers/remux/handler.go
package remux

import (
    "context"
    "errors"
    "io/fs"
    "net/http"
    "os"
    "path/filepath"
    "time"

    "golang.org/x/sync/singleflight"

    "maktaba/streaming/internal/cache"
    "maktaba/streaming/internal/caps"
    "maktaba/streaming/internal/ffmpeg"
    "maktaba/streaming/internal/handlers/direct"
    "maktaba/streaming/internal/httpx"
    "maktaba/streaming/internal/probe"
)

type Handler struct {
    Probe   probe.Lookup
    Caps    *caps.Registry
    Cache   *cache.Store
    Direct  *direct.Handler
    FFmpeg  string         // path to ffmpeg binary
    Group   *singleflight.Group
    Limit   chan struct{}  // semaphore — max_concurrent remuxes
    Metrics *Metrics
}

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, row *probe.Row, v caps.Verdict) {
    key := keyFromVerdict(row, v)
    finalPath := key.Path(h.Cache.Root)

    // 1. Cache hit: serve via direct.Handler (range-aware).
    if stat, err := os.Stat(finalPath); err == nil && stat.Size() > 0 {
        h.Metrics.CacheHit.Inc()
        // Remuxed-output MIME is fully determined by the target container.
        // The probe row carries no MIME column (canonical schema), so we
        // pass the value explicitly here.
        h.Direct.ServeFileWithContentType(w, r, finalPath, mimeFor(key.Target), &probe.Row{
            VideoID:     row.VideoID,
            Path:        finalPath,
            ContentHash: row.ContentHash, // re-use source hash for ETag
            Container:   key.Target,
            MediaInfo:   row.MediaInfo,
        })
        return
    }

    // 2. In-progress on this host? .tmp file present → 503 + Retry-After.
    if h.Cache.IsInProgress(finalPath) {
        w.Header().Set("Retry-After", "2")
        httpx.Write(w, http.StatusServiceUnavailable,
            "remux-in-progress", "remux is being prepared", "retry in 2 seconds")
        return
    }

    // 3. Single-flight: only one goroutine on this host kicks off FFmpeg.
    _, err, _ := h.Group.Do(key.String(), func() (any, error) {
        // Re-check after acquiring; maybe a sibling caller finished it.
        if _, err := os.Stat(finalPath); err == nil {
            return nil, nil
        }
        return nil, h.runRemux(r.Context(), row, key, finalPath)
    })

    if err != nil {
        if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
            // Client disconnected mid-remux; nothing to write.
            return
        }
        h.Metrics.Failed.WithLabelValues(classify(err)).Inc()
        httpx.Write(w, http.StatusBadGateway, "remux-failed",
            "remux failed", err.Error())
        return
    }

    // 4. Now the file exists — serve it like direct play.
    h.Direct.ServeFileWithContentType(w, r, finalPath, mimeFor(key.Target), &probe.Row{
        VideoID:     row.VideoID,
        Path:        finalPath,
        ContentHash: row.ContentHash,
        Container:   key.Target,
        MediaInfo:   row.MediaInfo,
    })
}

func (h *Handler) runRemux(ctx context.Context, row *probe.Row, key CacheKey, finalPath string) error {
    select {
    case h.Limit <- struct{}{}:
        defer func() { <-h.Limit }()
    case <-ctx.Done():
        return ctx.Err()
    }

    if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
        return err
    }
    tmpPath := h.Cache.TempPath(finalPath)
    plan := ffmpeg.Plan{
        InputPath:   row.Path,
        OutputPath:  tmpPath,
        Target:      key.Target,
        VideoIdx:    row.MediaInfo.VideoStreamIdx,
        AudioIdx:    key.AudioIdx,
        SubtitleIdx: key.SubIdx,
    }

    h.Metrics.Started.WithLabelValues(key.Target).Inc()
    start := time.Now()

    if err := ffmpeg.RemuxToTemp(ctx, plan, h.FFmpeg); err != nil {
        _ = os.Remove(tmpPath)
        return err
    }

    // 5. Validate the output before publishing.
    if err := validateRemuxOutput(ctx, tmpPath); err != nil {
        _ = os.Remove(tmpPath)
        return err
    }
    if err := os.Rename(tmpPath, finalPath); err != nil {
        _ = os.Remove(tmpPath)
        return err
    }

    h.Metrics.Completed.WithLabelValues(key.Target).Inc()
    h.Metrics.Duration.Observe(time.Since(start).Seconds())
    if stat, err := os.Stat(finalPath); err == nil {
        h.Metrics.SizeBytes.Observe(float64(stat.Size()))
    }
    return nil
}
```

### 2.4 Function signatures

```go
// streaming/internal/cache/store.go
package cache

func New(root string) *Store

func (s *Store) Path(rel string) string
func (s *Store) TempPath(finalPath string) string
func (s *Store) IsInProgress(finalPath string) bool
func (s *Store) AtomicWrite(finalPath string, write func(io.Writer) error) error
```

`TempPath` returns `<finalPath>.tmp.<pid>.<rand>`. `IsInProgress` returns
true when there's a `.tmp.*` sibling younger than 60 seconds (matches
the GC's deletion window from Story 8.14).

## 3. Cache validation

```go
// streaming/internal/handlers/remux/validate.go
package remux

import (
    "context"
    "fmt"
    "os"
    "os/exec"
)

// validateRemuxOutput protects against the corrupt-cache edge case in the
// story. ffprobe must succeed on the temp file and report at least one
// video stream and a finite duration. A failed validation returns an
// error so the caller deletes the temp file and the matrix verdict for
// this video downgrades to transcode for the rest of the session.
//
// The ffprobe binary path comes from `cfg.FFmpeg.ProbeBinary` (Story 8.1
// config block), NOT a hard-coded "ffprobe" — operators may pin a
// vendored or sandboxed binary, and the package never resolves PATH at
// call time.
func validateRemuxOutput(ctx context.Context, probeBin, path string) error {
    stat, err := os.Stat(path)
    if err != nil {
        return err
    }
    if stat.Size() < 1024 {
        return fmt.Errorf("remux output too small: %d bytes", stat.Size())
    }
    out, err := exec.CommandContext(ctx, probeBin,
        "-v", "error", "-show_entries", "format=duration",
        "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
    if err != nil {
        return fmt.Errorf("ffprobe rejected remux: %w", err)
    }
    var dur float64
    fmt.Sscanf(string(out), "%f", &dur)
    if dur <= 0 {
        return fmt.Errorf("remux duration is zero")
    }
    return nil
}
```

Callers (in `Handler.runRemux`) thread `cfg.FFmpeg.ProbeBinary` through
the handler struct alongside the existing `FFmpeg` field. The handler
type gains:

```go
type Handler struct {
    // ... existing fields ...
    FFprobe string  // path to ffprobe binary (cfg.FFmpeg.ProbeBinary)
}
```

## 4. Test plan

### 4.1 FFmpeg invocation tests (`streaming/internal/ffmpeg/remux_test.go`)

Skipped on `MAKTABA_SKIP_FFMPEG_TESTS=1`; otherwise fixture-driven against
small clips (≤ 5 s) shipped under `testdata/`.

| Test | What it pins |
|---|---|
| `TestRemux_MKVtoMP4_CopiesStreams` | `mkv (h264 + aac) → mp4`; ffprobe shows `codec_name=h264, codec_name=aac`, `format_name=mov,mp4,m4a,3gp,3g2,mj2`. |
| `TestRemux_MKVtoTS_DropsSubs` | Source has `S_TEXT/UTF8`; output is mpegts; ffprobe shows no subtitle stream. |
| `TestRemux_FlagsFastStart` | The output mp4 has `moov` before `mdat` (we shell out to `mp4dump`/parse manually — small helper. Pinned because Apple players require it). |
| `TestRemux_ContextCancelKillsFFmpeg` | Cancel context immediately after Start; `os.FindProcess(pid).Signal(syscall.Signal(0))` returns `ESRCH` within 2.5 s. |
| `TestRemux_NonexistentInputErrors` | Plan with `InputPath="/nope"`; error wraps stderr containing `No such file`. |
| `TestRemux_StartSecApplied` | `StartSec=1.0`; output duration is `source_duration - 1.0` ± 100 ms. |

### 4.2 Cache key tests (`cache_key_test.go`)

| Test | What it pins |
|---|---|
| `TestCacheKey_Stable` | Same inputs always produce the same string, including ordering of fields. |
| `TestCacheKey_DifferentTargetsDifferentPaths` | mp4 and ts at the same hash → distinct files (no collisions). |
| `TestCacheKey_DefaultTrackEncoding` | `AudioIdx=-1` produces `_a-1_` in the filename — never elided to bare `_a_`. |
| `TestCacheKey_PathIsTwoCharShard` | `cache/remux/{hash[:2]}/...`; verifies sharding. |

### 4.3 Handler integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestHandler_FirstRequestRemuxes_SecondRequestIsCached` | First GET → 503 then 200 (or pass-through serve when streaming-write enabled); `cache/remux/...mp4` exists; second GET → 200, no FFmpeg invocation (asserted by spy). AC-1 + AC-2. |
| `TestHandler_TwoConcurrentClients_OneFFmpeg` | Two parallel GETs for the same video; the FFmpeg spy is invoked exactly once. AC-3 single-flight. |
| `TestHandler_503DuringRemuxHasRetryAfter` | Mock the FFmpeg invocation to take 2 s; the in-flight request returns 503 with `Retry-After: 2` header (when caching mode). |
| `TestHandler_CorruptCacheRejectedRegenerated` | Plant a 1-byte file at the cache path; GET → 502 `remux-failed`; the file is removed and the next GET regenerates and succeeds. |
| `TestHandler_RemuxFailedDowngradesVerdict` | After a 502 `remux-failed`, the downgrade hook (`session.MarkRemuxFailed(videoID)`) is called; subsequent matrix queries for that session return ModeTranscode. |
| `TestHandler_StreamingWriteMode` | With `[remux] streaming_write = true` and a tiny clip, the response begins streaming as FFmpeg writes; chunked transfer-encoding is set; TTFB < 500 ms (per AC-3 acceptance). |
| `TestHandler_ParallelRangeReadsOnCachedFile` | Three concurrent ranged GETs against a cached file → all 206; bytes match `dd` output. (Reuses `direct.Handler` semantics.) |
| `TestHandler_LRU_EvictsRemuxFile` | Story 8.14 GC unlinks the file mid-stream → already-open FD continues serving; a new GET returns 503 + remux. |
| `TestHandler_ContentHashChangedNewCacheEntry` | Bump `probe.Row.ContentHash`; the new GET writes to a new cache file (the old stays for the existing FDs). |

### 4.4 Stress test

`TestHandler_50ConcurrentDistinctVideos` — open 50 different videos
on a host with `[remux] max_concurrent = 4`; the `remux_started_total`
counter increments only as slots free; no panic, no leaked subprocesses
(`pgrep ffmpeg` returns ≤ 4 + 1 throughout).

## 5. Test code scaffolding

```go
// streaming/internal/handlers/remux/handler_test.go
package remux_test

import (
    "context"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "sync"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

type spyFFmpeg struct {
    sync.Mutex
    calls   int
    delay   time.Duration
    failErr error
    outputs map[string][]byte // tmpPath → bytes to write on call
}

func (s *spyFFmpeg) RemuxToTemp(ctx context.Context, p ffmpeg.Plan, _ string) error {
    s.Lock()
    s.calls++
    s.Unlock()
    select {
    case <-time.After(s.delay):
    case <-ctx.Done():
        return ctx.Err()
    }
    if s.failErr != nil {
        return s.failErr
    }
    return os.WriteFile(p.OutputPath, s.outputs[p.OutputPath], 0o644)
}

func TestHandler_FirstRequestRemuxes_SecondRequestIsCached(t *testing.T) {
    spy := &spyFFmpeg{
        outputs: map[string][]byte{},
        delay:   50 * time.Millisecond,
    }
    h, srv := newTestHandler(t, spy)
    defer srv.Close()

    // First fetch — kicks off ffmpeg.
    resp1, err := http.Get(srv.URL + "/stream/direct/" + testVideoID)
    require.NoError(t, err)
    require.Equal(t, 200, resp1.StatusCode)
    _, _ = io.Copy(io.Discard, resp1.Body)
    resp1.Body.Close()

    // Second fetch — must hit cache.
    resp2, err := http.Get(srv.URL + "/stream/direct/" + testVideoID)
    require.NoError(t, err)
    require.Equal(t, 200, resp2.StatusCode)
    resp2.Body.Close()

    require.Equal(t, 1, spy.calls)
}

func TestHandler_TwoConcurrentClients_OneFFmpeg(t *testing.T) {
    spy := &spyFFmpeg{
        outputs: map[string][]byte{},
        delay:   200 * time.Millisecond,
    }
    h, srv := newTestHandler(t, spy)
    defer srv.Close()

    var wg sync.WaitGroup
    wg.Add(2)
    for i := 0; i < 2; i++ {
        go func() {
            defer wg.Done()
            resp, err := http.Get(srv.URL + "/stream/direct/" + testVideoID)
            require.NoError(t, err)
            resp.Body.Close()
        }()
    }
    wg.Wait()

    require.Equal(t, 1, spy.calls, "single-flight broken")
}
```

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Source file changes (mtime/content_hash) | New cache key → new file. Stale entries evicted by Story 8.14 LRU. | `TestHandler_ContentHashChangedNewCacheEntry` |
| Remux fails partway (corrupt source) | `tmpPath` is removed; client gets 502 `remux-failed`; the session marks the video as transcode-only for the rest of the session via `Session.MarkRemuxFailed(videoID)`. | `TestHandler_RemuxFailedDowngradesVerdict` |
| Two concurrent first-requests | `singleflight.Group.Do` keyed on `cacheKey.String()`; one runs, others wait. | `TestHandler_TwoConcurrentClients_OneFFmpeg` |
| Two requests on **different hosts** for the same video | Host A and host B both single-flight locally → both run FFmpeg once each, write to local caches. Cross-host single-flight is out of scope (each Streaming binary owns its cache). | Documented in §0; no test (would require multi-host harness). |
| Corrupt cache file from a crashed FFmpeg | `validateRemuxOutput` rejects on size < 1 KiB or ffprobe failure; `.tmp` is removed; on next request the remux runs again. | `TestHandler_CorruptCacheRejectedRegenerated` |
| LRU evicts mid-stream | Open file descriptor keeps serving (POSIX inode); the next request finds no file and re-runs FFmpeg. | `TestHandler_LRU_EvictsRemuxFile` |
| Source missing audio track but `AudioIdx >= 0` requested | FFmpeg fails with "Stream specifier not found"; we surface 502 `remux-failed`. (AC: surface stderr.) | Unit on `validateRemuxOutput` returning the stderr-aware error. |
| `max_concurrent` slots all occupied | New request blocks on the semaphore; AC-1's 503 is reserved for "this remux is in progress" — slot starvation reads as a normal queue. The handler reports `503 Service Unavailable` if the wait > 30 s. | Stress test §4.4. |
| Subtitle stream of unsupported codec for the target container | We don't include `-c:s mov_text` mapping when target=`ts`; subtitles are dropped silently. | `TestRemux_MKVtoTS_DropsSubs` |
| Streaming-write enabled but the player aborts | `io.Copy(w, pipeReader)` errors; ffmpeg subprocess gets context cancel; tmp file cleaned. The cached path is NOT published (no rename). | `TestHandler_StreamingWriteAbort` (added to §4.3 implicitly — covered by ctx cancel test). |
| `EvictHashCache` (Story 8.8) deletes cache during a request | Same as LRU — open FD continues; new requests miss. | Same as `TestHandler_LRU_EvictsRemuxFile` plus a `evict` variant. |

## 7. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `golang.org/x/sync/singleflight` | latest | Standard pattern for "one runner, many waiters"; matches Go std-lib idioms. |
| FFmpeg binary | ≥ 6.0 | `frag_keyframe+empty_moov+default_base_moof` flag set; required for fragmented MP4 from CMAF perspective. Pin documented in `streaming/Dockerfile`. |

(No new Go module deps beyond Story 8.1's `pgx`/`zerolog`/`prometheus`.)

## 8. Acceptance checklist

**Cache and serving (story ACs)**
- [ ] AC-1: First fetch writes `cache/remux/{hash[:2]}/{hash}_<target>.mp4` via `.tmp` + atomic rename; in-flight returns 503 + `Retry-After: 2` (caching mode).
- [ ] AC-2: Cache hit serves immediately with no FFmpeg invocation.
- [ ] AC-3: When `streaming_write=true`, TTFB < 500 ms for the first request on local SSD.

**FFmpeg correctness**
- [ ] `ffmpeg -c copy` is invoked with explicit `-map` for video and audio (no implicit defaults).
- [ ] MP4 output has `+faststart+frag_keyframe+empty_moov` so range-served playback works on AVPlayer/AppleTV.
- [ ] Output validated by `ffprobe` before being published (rejects 0-byte/corrupt outputs).
- [ ] Context cancel kills the FFmpeg child and its process group within 2 s.

**Single-flight + concurrency**
- [ ] Two concurrent first-requests for the same video → one FFmpeg subprocess.
- [ ] `[remux] max_concurrent` semaphore caps in-flight remuxes per host.

**Failure modes**
- [ ] Corrupt cache → 502, file removed, regenerated on next request.
- [ ] Remux failure → matrix verdict for the video downgrades to transcode for the rest of the session.

**Observability**
- [ ] Metrics: `remux_started_total`, `remux_completed_total`, `remux_failed_total{reason}`, `remux_cache_hit_total`, `remux_size_bytes`, `remux_duration_seconds`.
- [ ] Failed remux logs include FFmpeg stderr (truncated to 2 KiB) for ops debugging.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[remux] streaming_write` and `max_concurrent`.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.4.
