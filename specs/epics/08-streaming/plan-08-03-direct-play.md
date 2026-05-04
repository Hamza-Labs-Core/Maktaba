# Implementation Plan — Story 8.3 Direct Play (Range-Served `206 Partial Content`)

> Companion to [story-08-03-direct-play.md](story-08-03-direct-play.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on the JWT middleware in [Story 8.1](plan-08-01-server-skeleton.md)
> and the capability verdict from [Story 8.2](plan-08-02-capability-matrix.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/handlers/direct` — one file per concern (handler, ranges, conditional). |
| Range parser | Custom — `net/http`'s `ServeContent` won't do, because we need to integrate with the verdict (409 fall-through), the probe row (Content-Type), and our problem+json envelope. We re-use `io.SectionReader` but write the parser ourselves. |
| File source | Read directly from the media volume by absolute path (provided by Story 8.15's probe row). The volume is **read-only** for this binary; no writes ever flow through this handler. |
| `sendfile` path | Behind `os.File`-based `io.Copy`; on Linux/macOS the runtime uses `splice`/`sendfile` automatically when src and dst are both `*os.File` / `*net.TCPConn`. We never read into a Go buffer except for the verification tests. |
| ETag | First 32 hex chars of `BLAKE3(file.contents)` — taken from `media_info.content_hash` (Pipeline already computes it; we never hash here). |
| Out of scope | Remux fallback path (Story 8.4), multipart byteranges, content sniffing — `Content-Type` always comes from the probe row. |

## 1. Architecture diagram

```
            client
              │  GET  /stream/direct/{video_id}?sig=…
              │  HEAD /stream/direct/{video_id}?sig=…
              ▼
    ┌─────────────────────────────────────────────────────────┐
    │ middleware: requestID → log → metrics → SignedURL       │
    │             (aud=streaming-direct, sub=video_id)        │
    │             → LibraryGuard                              │
    └────────────────────────────┬────────────────────────────┘
                                 ▼
    ┌─────────────────────────────────────────────────────────┐
    │ direct.Handler.Serve(w, r)                              │
    │                                                         │
    │  1. probe.Lookup(video_id)        // 8.15 cached read   │
    │  2. caps.Decide(profile, media)   // 8.2 verdict        │
    │     if mode != direct → 409 with manifest_url hint      │
    │  3. os.Open(probe.Path)                                  │
    │  4. parse `Range:` (single byte-range only)             │
    │  5. evaluate `If-Range`, `If-None-Match`,               │
    │     `If-Modified-Since` (RFC 7232/7233)                 │
    │  6. write headers (200 / 206 / 304 / 412 / 416)         │
    │  7. io.Copy(w, io.NewSectionReader(f, off, len))        │
    └─────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/handlers/direct/handler.go` | `Handler.Serve(w,r)`, the `GET`/`HEAD` entry point. |
| `streaming/internal/handlers/direct/ranges.go` | `parseRange(headerValue, totalSize) ([]Range, error)`, single-range only. |
| `streaming/internal/handlers/direct/conditional.go` | RFC 7232 conditional evaluation (`If-None-Match`, `If-Modified-Since`, `If-Range`). |
| `streaming/internal/handlers/direct/handler_test.go` | Range tests (table) + integration. |
| `streaming/internal/handlers/direct/conditional_test.go` | Conditional GETs. |
| `streaming/internal/handlers/direct/profile_hint.go` | Map `Verdict.Mode != direct` → manifest URL hint payload. |
| `streaming/internal/handlers/direct/sendfile_linux.go` (build-tagged) | Optional `syscall.Sendfile` direct path for the sentinel test. The code goes through `io.Copy` on all platforms; the file exists only to assert the runtime picks the fast path. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/server/router.go` | Wire `GET /stream/direct/{video_id}` and `HEAD /stream/direct/{video_id}` to `direct.Handler`. |
| `streaming/internal/observability/metrics.go` | Register `direct_play_bytes_total{video_id_class}`, `direct_play_first_byte_seconds`, `direct_play_409_total{reason}`. |
| `specs/epics/08-streaming/README.md` | Tick 8.3 once landed. |

### 2.3 Type definitions

```go
// streaming/internal/handlers/direct/handler.go
package direct

import (
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
    "strconv"
    "time"

    "github.com/google/uuid"

    "maktaba/streaming/internal/auth"
    "maktaba/streaming/internal/caps"
    "maktaba/streaming/internal/httpx"
    "maktaba/streaming/internal/probe"
)

type Handler struct {
    Probe   probe.Lookup
    Caps    *caps.Registry
    Metrics *Metrics  // small, package-private wrapper around Prom counters
}

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
    cl := auth.ClaimsFrom(r.Context())          // never nil — middleware enforces
    videoID := uuid.MustParse(cl.Subject)        // sub is video_id by AC-1

    row, err := h.Probe.LookupVideo(r.Context(), videoID)
    if err != nil {
        httpx.Write(w, http.StatusNotFound, "video-not-found",
            "video not found", err.Error())
        return
    }

    profile := h.Caps.Get(profileFromQuery(r))
    verdict := caps.Decide(profile, row.MediaInfo, sessionOverrideFromQuery(r))

    if verdict.Mode != caps.ModeDirect {
        h.write409(w, r, row, verdict)
        return
    }

    f, err := os.Open(row.Path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            httpx.Write(w, http.StatusNotFound, "video-file-missing",
                "video file missing", row.Path)
            return
        }
        httpx.Write(w, http.StatusInternalServerError, "video-open-error",
            "could not open video file", err.Error())
        return
    }
    defer f.Close()

    stat, err := f.Stat()
    if err != nil {
        httpx.Write(w, http.StatusInternalServerError, "video-stat-error",
            "could not stat video file", err.Error())
        return
    }

    h.serveFile(w, r, f, stat, row)
}
```

```go
// streaming/internal/handlers/direct/ranges.go
package direct

import (
    "errors"
    "fmt"
    "strconv"
    "strings"
)

var (
    ErrNoRange      = errors.New("no Range header")
    ErrMultiRange   = errors.New("multipart/byteranges not supported")
    ErrUnsatisfiable = errors.New("range not satisfiable")
    ErrMalformed    = errors.New("malformed Range header")
)

type Range struct {
    Start, Length int64 // Length is byte count, never negative; Start+Length-1 is the inclusive end
}

// parseRange handles the three single-range RFC 7233 forms:
//   bytes=N-M     closed
//   bytes=N-      open-ended
//   bytes=-N      suffix (last N bytes)
//
// Multi-range (e.g. "bytes=0-100,200-300") returns ErrMultiRange so the
// handler can answer 416 per AC-3.
func parseRange(header string, total int64) (Range, error) {
    if header == "" {
        return Range{}, ErrNoRange
    }
    const prefix = "bytes="
    if !strings.HasPrefix(header, prefix) {
        return Range{}, ErrMalformed
    }
    spec := strings.TrimSpace(header[len(prefix):])
    if strings.Contains(spec, ",") {
        return Range{}, ErrMultiRange
    }

    dash := strings.IndexByte(spec, '-')
    if dash < 0 {
        return Range{}, ErrMalformed
    }
    startStr := strings.TrimSpace(spec[:dash])
    endStr := strings.TrimSpace(spec[dash+1:])

    switch {
    case startStr == "" && endStr == "":
        return Range{}, ErrMalformed
    case startStr == "":
        // suffix: last N bytes
        n, err := strconv.ParseInt(endStr, 10, 64)
        if err != nil || n <= 0 {
            return Range{}, ErrMalformed
        }
        if n >= total {
            return Range{Start: 0, Length: total}, nil
        }
        return Range{Start: total - n, Length: n}, nil

    case endStr == "":
        // open-ended: from N to EOF
        start, err := strconv.ParseInt(startStr, 10, 64)
        if err != nil || start < 0 {
            return Range{}, ErrMalformed
        }
        if start >= total {
            return Range{}, ErrUnsatisfiable
        }
        return Range{Start: start, Length: total - start}, nil

    default:
        start, err := strconv.ParseInt(startStr, 10, 64)
        if err != nil || start < 0 {
            return Range{}, ErrMalformed
        }
        end, err := strconv.ParseInt(endStr, 10, 64)
        if err != nil || end < start {
            return Range{}, ErrMalformed
        }
        if start >= total {
            return Range{}, ErrUnsatisfiable
        }
        if end >= total {
            end = total - 1 // RFC 7233 §4.1: clamp to total-1 (AC edge case)
        }
        return Range{Start: start, Length: end - start + 1}, nil
    }
}

func contentRange(rng Range, total int64) string {
    return fmt.Sprintf("bytes %d-%d/%d", rng.Start, rng.Start+rng.Length-1, total)
}
```

### 2.4 Function signatures

```go
// streaming/internal/handlers/direct/handler.go (continued)

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request,
    f *os.File, stat os.FileInfo, row *probe.Row) {

    total := stat.Size()
    etag := strongETag(row.ContentHash)
    lastMod := stat.ModTime().UTC()

    // 1. RFC 7232: If-None-Match short-circuits.
    if matchETag(r.Header.Get("If-None-Match"), etag) {
        w.Header().Set("ETag", etag)
        w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
        w.WriteHeader(http.StatusNotModified)
        return
    }

    // 2. Common headers (set before WriteHeader).
    w.Header().Set("Accept-Ranges", "bytes")
    w.Header().Set("ETag", etag)
    w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
    w.Header().Set("Content-Type", row.MIME) // from probe row, never sniffed
    w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")

    // 3. Parse Range, applying If-Range to decide whether to honor it.
    rng, rngErr := parseRange(r.Header.Get("Range"), total)
    ifRange := r.Header.Get("If-Range")
    rangeOK := rngErr == nil
    if rangeOK && ifRange != "" && !ifRangeMatches(ifRange, etag, lastMod) {
        // Stale precondition → fall back to 200 full body so the player resyncs.
        rangeOK = false
        rngErr = nil
    }

    // 4. Errors that produce 416.
    if errors.Is(rngErr, ErrMultiRange) {
        w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(total, 10))
        httpx.Write(w, http.StatusRequestedRangeNotSatisfiable,
            "range-multipart-unsupported",
            "multipart/byteranges not supported",
            "send a single byte-range")
        return
    }
    if errors.Is(rngErr, ErrUnsatisfiable) {
        w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(total, 10))
        httpx.Write(w, http.StatusRequestedRangeNotSatisfiable,
            "range-not-satisfiable",
            "range not satisfiable",
            fmt.Sprintf("file is %d bytes", total))
        return
    }
    if errors.Is(rngErr, ErrMalformed) {
        // RFC 7233 says we MAY ignore malformed ranges; we do, and return 200.
        rangeOK = false
        rngErr = nil
    }

    // 5. HEAD path (Safari probes with HEAD before GET).
    if r.Method == http.MethodHead {
        w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
        w.WriteHeader(http.StatusOK)
        return
    }

    // 6. Body.
    if !rangeOK {
        // Full 200 OK.
        w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
        w.WriteHeader(http.StatusOK)
        if _, err := io.Copy(w, f); err != nil {
            // Client disconnect or fs error mid-stream — log and bail (no panic).
            logCopyErr(r.Context(), err)
        }
        return
    }

    // 7. 206 Partial Content.
    w.Header().Set("Content-Range", contentRange(rng, total))
    w.Header().Set("Content-Length", strconv.FormatInt(rng.Length, 10))
    w.WriteHeader(http.StatusPartialContent)

    sec := io.NewSectionReader(f, rng.Start, rng.Length)
    if _, err := io.Copy(w, sec); err != nil {
        logCopyErr(r.Context(), err)
    }
}
```

```go
// streaming/internal/handlers/direct/conditional.go
package direct

func matchETag(headerValue, etag string) bool {
    // RFC 7232 §3.2: a comma-separated list; "*" matches anything.
    if headerValue == "" {
        return false
    }
    if strings.TrimSpace(headerValue) == "*" {
        return true
    }
    for _, tag := range strings.Split(headerValue, ",") {
        if strings.TrimSpace(tag) == etag {
            return true
        }
    }
    return false
}

func ifRangeMatches(headerValue, etag string, lastMod time.Time) bool {
    // RFC 7233 §3.2: If-Range may carry a strong ETag or an HTTP-date.
    if headerValue == "" {
        return true
    }
    if strings.HasPrefix(headerValue, `"`) {
        return headerValue == etag
    }
    t, err := http.ParseTime(headerValue)
    if err != nil {
        return false
    }
    return !lastMod.After(t)
}

func strongETag(contentHash string) string {
    return `"` + contentHash[:32] + `"` // strong ETag — content-hashed, no W/ prefix
}
```

## 3. The 409 fall-through

When a player hits `/stream/direct/{video_id}` for a video that the
matrix flags non-direct, we don't try to be clever — we tell the player
to call the API and follow the manifest. The body is structured so
clients can act on it.

```go
// streaming/internal/handlers/direct/profile_hint.go
package direct

func (h *Handler) write409(w http.ResponseWriter, r *http.Request,
    row *probe.Row, v caps.Verdict) {

    detail := struct {
        Mode        string `json:"mode"`
        Reason      string `json:"reason"`
        ManifestURL string `json:"manifest_url"` // hint, not signed; client must call API
    }{
        Mode:        string(v.Mode),
        Reason:      v.Reason,
        ManifestURL: fmt.Sprintf("/api/stream/sessions?video_id=%s", row.VideoID),
    }
    httpx.WriteJSONProblem(w, http.StatusConflict,
        "direct-play-unsupported",
        "direct play not supported for this client/source",
        detail)
    h.Metrics.PlayDeniedTotal.WithLabelValues(string(v.Mode)).Inc()
}
```

`httpx.WriteJSONProblem` is an extension of the §3 helper from Story 8.1
that takes a `detail any` arg encoded as a JSON object (RFC 7807 allows
extension members beyond the canonical four).

## 4. Test plan

### 4.1 Range parser unit tests (`handler_test.go::TestParseRange`)

Table-driven, against `total=1000`:

| Header                | Expected Start | Expected Length | Error |
|-----------------------|---------------:|----------------:|---|
| `bytes=0-99`          | 0              | 100             | nil |
| `bytes=900-`          | 900            | 100             | nil |
| `bytes=-100`          | 900            | 100             | nil |
| `bytes=0-`            | 0              | 1000            | nil |
| `bytes=0-9999999999`  | 0              | 1000 (clamped)  | nil |
| `bytes=2000-3000`     | —              | —               | ErrUnsatisfiable |
| `bytes=0-100,200-300` | —              | —               | ErrMultiRange |
| `bytes=foo-bar`       | —              | —               | ErrMalformed |
| `bytes=`              | —              | —               | ErrMalformed |
| `bytes=-0`            | —              | —               | ErrMalformed (suffix length zero) |
| `kilobytes=0-99`      | —              | —               | ErrMalformed |
| (empty)               | —              | —               | ErrNoRange |

### 4.2 Conditional tests (`conditional_test.go`)

| Test | What it pins |
|---|---|
| `TestIfNoneMatch_Hit304` | `If-None-Match: "<etag>"` → 304 with ETag and Last-Modified, empty body. |
| `TestIfNoneMatch_StarMatchesEverything` | `If-None-Match: *` → 304 (RFC 7232 §3.2 wildcard). |
| `TestIfNoneMatch_Miss200OrRange` | Different ETag → handler proceeds; subsequent Range works. |
| `TestIfRange_StaleETag_FallsBackToFullBody` | Stale `If-Range` → request becomes 200 full body, not 206. |
| `TestIfRange_FreshETag_PreservesRange` | Matching `If-Range` → 206 honored. |
| `TestIfRange_Date_Stale_FallsBack` | `If-Range: <date older than mtime>` → 200 full body. |
| `TestIfModifiedSince_NotImplementedAsAlt` | We document that we only implement `If-None-Match`; `If-Modified-Since` falls through to full 200. (Test asserts the documented behavior.) |

### 4.3 Range integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestServeFile_Closed206` | `Range: bytes=0-99` on a 1 GiB MP4 fixture → 206 with `Content-Range: bytes 0-99/{total}`, body is the first 100 bytes. |
| `TestServeFile_OpenEnded206` | `Range: bytes=900-` → 206; body length = total-900. |
| `TestServeFile_Suffix206` | `Range: bytes=-100` → 206; body is final 100 bytes. |
| `TestServeFile_HeadOK` | HEAD → 200 with full Content-Length and no body. AC-2. |
| `TestServeFile_Multipart416` | `Range: bytes=0-100,200-300` → 416 with `Content-Range: bytes */{total}`. AC-3. |
| `TestServeFile_PastEOF416` | `Range: bytes=2T-2T` (assuming smaller fixture) → 416 with `Content-Range: bytes */{total}`. |
| `TestServeFile_OverlongClampedToFile` | `Range: bytes=0-9999999999` on a 1 GiB file → 206 with full file (clamped). Edge case in story. |
| `TestServeFile_MalformedRangeServes200` | Malformed range header → 200 full body. |
| `TestServeFile_FullBodyOnNoRange` | No Range header → 200 with full Content-Length. |
| `TestServeFile_FallThrough409` | MKV file on `browser-chrome` → 409 with `manifest_url` in detail. AC-4. |

### 4.4 Capability fall-through

`TestServeFile_FallThrough409` mocks `caps.Registry.Get` to return a
profile that doesn't accept MKV; asserts the 409 envelope is JSON, has
the `manifest_url` field, and the response status is exactly `409`. The
metric `direct_play_409_total{reason="container mismatch"}` ticks once.

### 4.5 Edge integration

| Test | What it pins |
|---|---|
| `TestServeFile_FileDeletedDuringStream` | Open the file, start streaming, `os.Remove` mid-copy on Linux/macOS — the open FD continues; Go runtime delivers EOF cleanly; no panic, log-only. (Filesystems where the unlink causes EBUSY are excluded by an OS gate.) |
| `TestServeFile_FileNotFound404Problem` | The probe row says path `/missing` → 404 problem+json. |
| `TestServeFile_Safari_HeadThenRange0to1` | Sequence: HEAD → GET `Range: bytes=0-1` → both 200/206; test asserts both succeed. |
| `TestServeFile_StaleETagAfterReprobe` | Probe row's content_hash changes (Pipeline reprobed); `If-None-Match: <oldetag>` no longer matches; handler returns 200 full body. |

### 4.6 Performance gate

| Test | What it pins |
|---|---|
| `TestServeFile_ParallelTwoStreamsCPULow` | Two parallel 4 GiB MP4 reads from `tmpfs` for 30 s; CPU usage of the streaming process must stay below 5% of one core (asserts `sendfile`/`splice` is engaged on Linux; on macOS the `*os.File→*net.TCPConn` path uses `copy_file_range`/`sendfile`). Skipped on platforms where the runtime can't take the fast path; emit a warning. |
| `TestServeFile_FirstByteUnder50ms` | TTFB measured on local SSD; the test produces a 1 GiB fixture with `truncate`, then issues a `Range: bytes=0-1023` request. Must complete in ≤ 50 ms p99 over 100 trials. |

## 5. Test code scaffolding

```go
// streaming/internal/handlers/direct/handler_test.go
package direct_test

import (
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strconv"
    "testing"

    "github.com/stretchr/testify/require"
)

func makeFixture(t *testing.T, size int64) (string, string) {
    t.Helper()
    dir := t.TempDir()
    path := filepath.Join(dir, "test.mp4")
    f, err := os.Create(path)
    require.NoError(t, err)
    require.NoError(t, f.Truncate(size))
    require.NoError(t, f.Close())
    // Hash the empty contents — sufficient for ETag tests; production hashes
    // are computed by Pipeline.
    return path, "00000000000000000000000000000000"
}

func TestServeFile_Closed206(t *testing.T) {
    path, hash := makeFixture(t, 1<<20) // 1 MiB
    h := newHandlerWithFixture(t, path, hash, "video/mp4", "browser-chrome", "mp4")

    req := httptest.NewRequest("GET", "/stream/direct/00000000-0000-0000-0000-000000000000", nil)
    req.Header.Set("Range", "bytes=0-99")
    rec := httptest.NewRecorder()
    h.Serve(rec, req)

    require.Equal(t, http.StatusPartialContent, rec.Code)
    require.Equal(t, "bytes 0-99/1048576", rec.Header().Get("Content-Range"))
    require.Equal(t, "100", rec.Header().Get("Content-Length"))
    require.Equal(t, int64(100), int64(len(rec.Body.Bytes())))
    require.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
}

func TestServeFile_HeadOK(t *testing.T) {
    path, hash := makeFixture(t, 1<<20)
    h := newHandlerWithFixture(t, path, hash, "video/mp4", "browser-chrome", "mp4")

    req := httptest.NewRequest("HEAD", "/stream/direct/00000000-0000-0000-0000-000000000000", nil)
    rec := httptest.NewRecorder()
    h.Serve(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, strconv.Itoa(1<<20), rec.Header().Get("Content-Length"))
    require.Empty(t, rec.Body.Bytes())
}

func TestServeFile_Multipart416(t *testing.T) {
    path, hash := makeFixture(t, 1<<20)
    h := newHandlerWithFixture(t, path, hash, "video/mp4", "browser-chrome", "mp4")

    req := httptest.NewRequest("GET", "/stream/direct/00000000-0000-0000-0000-000000000000", nil)
    req.Header.Set("Range", "bytes=0-100,200-300")
    rec := httptest.NewRecorder()
    h.Serve(rec, req)

    require.Equal(t, http.StatusRequestedRangeNotSatisfiable, rec.Code)
    require.Equal(t, "bytes */1048576", rec.Header().Get("Content-Range"))
    require.Contains(t, rec.Body.String(), "range-multipart-unsupported")
}
```

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| File modified during read (mtime changed, content_hash differs) | ETag from probe row reflects new hash; stale `If-Range` falls back to 200 full body. | `TestServeFile_StaleETagAfterReprobe` |
| File on NFS disappears mid-stream | `io.Copy` returns an `*PathError` or `EBADF`; we log and close the connection — no panic, no half-baked body. | `TestServeFile_FileDeletedDuringStream` |
| Range past EOF | 416 with `Content-Range: bytes */total` (so the player can recover). | `TestServeFile_PastEOF416` |
| Range end exceeds file size | Clamped to `total-1`; status is 206. | `TestServeFile_OverlongClampedToFile` |
| MKV on a Chrome client | 409 with `manifest_url` in detail; clients should always have called the API first per Epic 7 §7.10 AC-5. | `TestServeFile_FallThrough409` |
| Safari HEAD before GET | HEAD returns full Content-Length, body empty; subsequent ranged GET works. | `TestServeFile_Safari_HeadThenRange0to1` |
| `Content-Type` from a malicious server-side path | Never inferred from filename — we read the MIME type from `probe.Row.MIME` (filled by Pipeline at probe time from ffprobe), which the player trusts. We add `X-Content-Type-Options: nosniff` so the browser doesn't override. | `TestServeFile_HeadOK` (asserts header) |
| Multiple `If-None-Match` values | Comma-split in `matchETag`; any match → 304. | `TestIfNoneMatch_Hit304` |
| Strong vs weak ETag | We always emit strong (no `W/` prefix); `matchETag` only compares strong tags — clients that send `W/"..."` get a clean miss and a fresh body. | `matchETag` test row. |
| Suffix range of 0 (`bytes=-0`) | RFC 7233 says invalid; we reject as `ErrMalformed`, which becomes 200 full body (lenient parse). Test asserts behavior. | `TestParseRange[bytes=-0]` |
| `Range: bytes=0-` | Open-ended; equivalent to "send everything"; we return 206 with full file. Some players (mpv) start with this. | `TestParseRange[bytes=0-]` + integration variant. |
| `Range` with a `Connection: close` | We honor the connection close after writing the response. No keep-alive bookkeeping in this handler. | Standard `net/http` behavior. |
| Player on slow link, server `write_ms=0` | `Server.WriteTimeout` is 0 (per Story 8.1 config); long writes don't trip a server-side timeout. The OS keeps the connection alive; client RST would error `io.Copy` and we log. | Implicit; not tested. |

## 7. Dependencies

No new top-level deps. We use:
- `net/http` (stdlib) for ranged response semantics.
- `io.SectionReader` (stdlib) for the body slice.
- `os.File` (stdlib) — Go runtime selects `sendfile`/`splice` automatically.

The `BLAKE3` hash itself is **not** computed here — we read
`probe.Row.ContentHash` (Pipeline owns hashing in Epic 1).

## 8. Acceptance checklist

**Range serving (story ACs)**
- [ ] AC-1: Closed/open-ended/suffix ranges all produce correct `Content-Range` and bytes.
- [ ] AC-2: HEAD returns 200 + full Content-Length + empty body; Safari smoke-test passes.
- [ ] AC-3: Multipart range → 416 with `Content-Range: bytes */total`.
- [ ] AC-4: Non-direct-playable source on this profile → 409 with `manifest_url` hint.

**Headers**
- [ ] `Accept-Ranges: bytes` set on every 200/206 response.
- [ ] `Content-Type` always sourced from `probe.Row.MIME`, never sniffed.
- [ ] `ETag` and `Last-Modified` always present and consistent.
- [ ] `X-Content-Type-Options: nosniff` set.

**Conditional**
- [ ] `If-None-Match` matching ETag → 304 with no body.
- [ ] `If-Range` mismatch → 200 full body fallback.

**Performance**
- [ ] p99 first-byte latency < 50 ms on local SSD over 100 trials.
- [ ] Two parallel streams use ≤ 5% of one core (sendfile path).

**Observability**
- [ ] `direct_play_bytes_total` counter exported.
- [ ] `direct_play_first_byte_seconds` histogram exported.
- [ ] `direct_play_409_total{reason}` counter exported.

**Docs**
- [ ] `specs/epics/08-streaming/README.md` ticks 8.3.
