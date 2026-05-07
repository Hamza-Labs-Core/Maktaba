# Implementation Plan — Story 8.6 DASH Manifest (opt-in per session)

> Companion to [story-08-06-dash-manifest.md](story-08-06-dash-manifest.md).
> The story states *what* and *why*; this plan states *how*. Builds on
> [Story 8.5](plan-08-05-hls-transcode.md) (sister format) and depends on
> the session lifecycle in [Stories 8.8](plan-08-08-grpc-server.md) /
> [8.9](plan-08-09-session-store.md). Architecture reference:
> [§4.3](../../architecture.md#43-hls-manifest).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/dash/` — sibling of `hls/`; minimal footprint. |
| Format selection | Per-session, baked into `caps.SessionOverride.Format` (`hls` default, `dash` opt-in). The Format value is canonical on the `streaming_sessions.format` column (Story 8.9). |
| FFmpeg muxer | `-f dash`, separate scratch dir from HLS so a session never holds both formats simultaneously (architecture §4.3 explains why). |
| Live → static transition | Detected by watching `proc.Wait()`; on clean exit, the MPD is rewritten in place with `type="static"` and a fixed `mediaPresentationDuration`. |
| Out of scope | DASH player on the client (SDK choice — shaka-player is documented in the story but lives in the web client epic). DASH-encrypted flows (out of scope project-wide). |

## 1. Architecture diagram

```
            OpenSession(format="dash", ...)
                          │
                          ▼
        ┌──────────────────────────────────────────────────────┐
        │ dash.Session                                         │
        │   - id, videoID, dir = cache/dash/{session_id}       │
        │   - verdict, ladder (same source as HLS)             │
        │   - mpdReady chan (closes when manifest.mpd present) │
        │   - eofObserved bool                                 │
        └─────────────────────────┬────────────────────────────┘
                                  │
                                  ▼
        ┌──────────────────────────────────────────────────────┐
        │ ffmpeg -i src                                        │
        │   ... per-rendition encoder maps (same as HLS)       │
        │   -f dash                                            │
        │   -seg_duration 4                                    │
        │   -window_size 0                                     │
        │   -extra_window_size 0                               │
        │   -use_template 1 -use_timeline 1                    │
        │   -init_seg_name 'init-$RepresentationID$.m4s'       │
        │   -media_seg_name 'chunk-$RepresentationID$-$Number$.m4s'│
        │   -ldash 1   (low-latency DASH; OFF by default,      │
        │               opt-in via [dash] low_latency = true)  │
        │   {dir}/manifest.mpd                                 │
        └─────────────────────────┬────────────────────────────┘
                                  │
                                  ▼
        ┌──────────────────────────────────────────────────────┐
        │  GET /stream/{sid}/manifest.mpd                      │
        │   - reads ffmpeg-written manifest.mpd                │
        │   - if EOF observed and clean exit → swap            │
        │     type="dynamic" → "static" + fixed duration       │
        │   - sets Cache-Control: no-store                     │
        └──────────────────────────────────────────────────────┘
        ┌──────────────────────────────────────────────────────┐
        │  GET /stream/{sid}/init-<rep>.m4s                    │
        │  GET /stream/{sid}/chunk-<rep>-<n>.m4s               │
        │   - direct file reads (range-aware via direct.Handler│
        │   - immutable cache headers                          │
        └──────────────────────────────────────────────────────┘
```

DASH and HLS run in mutually exclusive lanes per session: AC-1's 409
`format-mismatch` is enforced by checking `streaming_sessions.format` on
every manifest fetch.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/dash/session.go` | `Session` lifecycle (Start/Stop/RestartFrom). Mirrors `hls.Session`. |
| `streaming/internal/dash/manifest.go` | `RewriteMPD(in, out, sess)` — flips `type` and patches `mediaPresentationDuration` on EOF. |
| `streaming/internal/dash/handler.go` | HTTP handlers for manifest + segments. |
| `streaming/internal/dash/manifest_test.go` | XML rewrite tests. |
| `streaming/internal/dash/handler_test.go` | End-to-end HTTP tests. |
| `streaming/internal/ffmpeg/dash.go` | `BuildDASHArgs(plan)` — same plan struct as HLS, output flags differ. |
| `streaming/internal/ffmpeg/dash_test.go` | Args-builder unit tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/server/router.go` | Wire `/stream/{sid}/manifest.mpd`, `/stream/{sid}/init-{rep}.m4s`, `/stream/{sid}/chunk-{rep}-{n}.m4s`. |
| `streaming/internal/handlers/manifest_dispatch.go` | Single dispatcher: `GET /stream/{sid}/manifest.{ext}` → look up `streaming_sessions.format`; route to HLS or DASH; mismatched ext → 409 `format-mismatch`. |
| `streaming/internal/observability/metrics.go` | `dash_sessions_active`, `dash_mpd_swaps_to_static_total`, `dash_segments_served_total{rendition}`. |
| `streaming/configs/streaming.toml.example` | `[dash] low_latency = false`, `static_ttl_sec = 1800`. |
| `specs/epics/08-streaming/README.md` | Tick 8.6. |

### 2.3 Type definitions

```go
// streaming/internal/ffmpeg/dash.go
package ffmpeg

func BuildDASHArgs(plan TranscodePlan) []string {
    a := []string{"-hide_banner", "-loglevel", "error", "-y"}

    // Hwaccel and start-time identical to HLS.
    switch plan.HwAccel {
    case HwAccelVideoToolbox:
        a = append(a, "-hwaccel", "videotoolbox")
    case HwAccelNVENC:
        a = append(a, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
    case HwAccelQSV:
        a = append(a, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
    }
    if plan.StartSec > 0 {
        a = append(a, "-ss", fmt.Sprintf("%.3f", plan.StartSec))
    }
    a = append(a, "-i", plan.InputPath)

    // Per-rendition encoder maps (reuses the HLS path's helpers).
    for i, r := range plan.Ladder {
        a = append(a, "-map", "0:v:0",
            "-filter:v:"+strconv.Itoa(i), fmt.Sprintf("scale=-2:%d", r.Height))
        encoder, encArgs := encoderArgs(plan.HwAccel, r)
        a = append(a, "-c:v:"+strconv.Itoa(i), encoder)
        a = append(a, encArgs...)
        a = append(a,
            "-b:v:"+strconv.Itoa(i), strconv.Itoa(r.BitrateKbps)+"k",
            "-g", strconv.Itoa(plan.GOPFrames),
            "-keyint_min", strconv.Itoa(plan.KeyIntMinFrames),
            "-sc_threshold", strconv.Itoa(plan.SCThreshold),
        )
    }

    // Single audio.
    a = append(a, "-map", fmt.Sprintf("0:a:%d", plan.AudioIdx),
        "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "48000")

    // DASH muxer.
    a = append(a,
        "-f", "dash",
        "-seg_duration", strconv.Itoa(plan.HLSTimeSec),
        "-use_template", "1",
        "-use_timeline", "1",
        "-init_seg_name", "init-$RepresentationID$.m4s",
        "-media_seg_name", "chunk-$RepresentationID$-$Number$.m4s",
        "-window_size", strconv.Itoa(plan.HLSListSize),
        "-extra_window_size", "0",
        "-streaming", "1",
        "-remove_at_exit", "0",
        "-utc_timing_url", "https://time.akamai.com/?iso",
    )
    if plan.LowLatencyDASH {
        a = append(a, "-ldash", "1")
    }
    a = append(a, filepath.Join(plan.OutputDir, "manifest.mpd"))
    return a
}
```

```go
// streaming/internal/dash/session.go
package dash

import (
    "context"
    "errors"
    "io/fs"
    "os"
    "path/filepath"
    "sync"
    "time"

    "github.com/google/uuid"
    "maktaba/streaming/internal/caps"
    "maktaba/streaming/internal/ffmpeg"
    "maktaba/streaming/internal/probe"
)

type Session struct {
    ID        uuid.UUID
    VideoID   uuid.UUID
    Dir       string
    Probe     *probe.Row
    Verdict   caps.Verdict
    HwAccel   ffmpeg.HwAccel
    FFmpegBin string

    mu       sync.Mutex
    proc     *exec.Cmd
    procDone chan error
    mpdReady chan struct{}
    eof      bool        // true after FFmpeg exits cleanly
    closed   bool
    startSec float64
}

func (s *Session) Start(ctx context.Context) error
func (s *Session) Stop(ctx context.Context) error
func (s *Session) RestartFrom(ctx context.Context, startSec float64) error
func (s *Session) WaitForMPD(ctx context.Context, timeout time.Duration) error
func (s *Session) IsEOF() bool
```

## 3. The MPD live → static rewrite

`RewriteMPD` is invoked on every fetch: if FFmpeg has exited cleanly we
flip `type="dynamic"` → `type="static"` and bake the duration. We don't
edit the file in place; the rewrite is streamed on the response, which
keeps FFmpeg's own writes safe.

```go
// streaming/internal/dash/manifest.go
package dash

import (
    "encoding/xml"
    "errors"
    "fmt"
    "io"
    "os"
    "strconv"
    "time"
)

type mpdEnvelope struct {
    XMLName                  xml.Name `xml:"MPD"`
    Type                     string   `xml:"type,attr"`
    MediaPresentationDuration string  `xml:"mediaPresentationDuration,attr,omitempty"`
    AvailabilityStartTime    string   `xml:"availabilityStartTime,attr,omitempty"`
    MinimumUpdatePeriod      string   `xml:"minimumUpdatePeriod,attr,omitempty"`
    InnerXML                 string   `xml:",innerxml"`
}

// RewriteMPD reads the manifest.mpd from disk and writes the (possibly
// adjusted) version to w. If sess.IsEOF() is true and the on-disk
// document already says type=static, we forward as-is (FFmpeg may have
// done it). Otherwise we override type and duration.
func RewriteMPD(w io.Writer, path string, sess *Session, sourceDuration time.Duration) error {
    raw, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return ErrMPDNotReady
        }
        return err
    }

    if !sess.IsEOF() {
        _, err := w.Write(raw)
        return err
    }

    // EOF observed → swap to static.
    var env mpdEnvelope
    if err := xml.Unmarshal(raw, &env); err != nil {
        // Hand-rolled string surgery as a fallback — FFmpeg's MPD escapes
        // poorly under mismatched namespaces. We only need to flip two attrs.
        out, err := patchMPDAttrs(raw, sourceDuration)
        if err != nil {
            return err
        }
        _, err = w.Write(out)
        return err
    }

    env.Type = "static"
    env.MediaPresentationDuration = isoDuration(sourceDuration)
    env.MinimumUpdatePeriod = "" // remove dynamic-only attr

    out, err := xml.Marshal(env)
    if err != nil {
        return err
    }
    _, err = w.Write(out)
    return err
}

func isoDuration(d time.Duration) string {
    secs := d.Seconds()
    h := int(secs / 3600)
    m := int(secs/60) % 60
    s := secs - float64(60*60*h+60*m)
    return fmt.Sprintf("PT%dH%dM%.3fS", h, m, s)
}
```

`patchMPDAttrs` is a regex-based fallback (we only ever change attribute
values on the `MPD` root element):

```go
// streaming/internal/dash/manifest.go (continued)

var (
    typeAttr     = regexp.MustCompile(`type\s*=\s*"[^"]*"`)
    durAttr      = regexp.MustCompile(`mediaPresentationDuration\s*=\s*"[^"]*"`)
    minUpdate    = regexp.MustCompile(`\s*minimumUpdatePeriod\s*=\s*"[^"]*"`)
)

func patchMPDAttrs(raw []byte, dur time.Duration) ([]byte, error) {
    body := string(raw)
    body = typeAttr.ReplaceAllString(body, `type="static"`)
    body = minUpdate.ReplaceAllString(body, "")
    if durAttr.MatchString(body) {
        body = durAttr.ReplaceAllString(body, fmt.Sprintf(`mediaPresentationDuration=%q`, isoDuration(dur)))
    } else {
        body = strings.Replace(body, `<MPD `, fmt.Sprintf(`<MPD mediaPresentationDuration=%q `, isoDuration(dur)), 1)
    }
    return []byte(body), nil
}
```

## 4. Format dispatcher

`manifest_dispatch.go` is the single chi route handler that routes to
HLS or DASH based on the session's persisted format. The 409 path is
explicit:

```go
// streaming/internal/handlers/manifest_dispatch.go
package handlers

func ManifestDispatch(hls *hls.Handler, dash *dash.Handler, store SessionStore) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sid := chi.URLParam(r, "session_id")
        ext := chi.URLParam(r, "ext") // "m3u8" or "mpd"

        sess, err := store.Get(r.Context(), sid)
        if err != nil {
            httpx.Write(w, http.StatusNotFound, "session-not-found",
                "session not found", "")
            return
        }
        switch sess.Format {
        case "hls":
            if ext != "m3u8" {
                httpx.Write(w, http.StatusConflict, "format-mismatch",
                    "session is HLS; manifest is .m3u8", "")
                return
            }
            hls.Master(w, r, sess)
        case "dash":
            if ext != "mpd" {
                httpx.Write(w, http.StatusConflict, "format-mismatch",
                    "session is DASH; manifest is .mpd", "")
                return
            }
            dash.Manifest(w, r, sess)
        default:
            httpx.Write(w, http.StatusInternalServerError, "session-format-unknown",
                "session has unknown format", sess.Format)
        }
    }
}
```

## 5. Test plan

### 5.1 Args-builder tests (`dash_test.go`)

| Test | What it pins |
|---|---|
| `TestBuildDASHArgs_BasicLadder` | 3-rung ladder → `-f dash`, `-seg_duration 4`, `-init_seg_name init-$RepresentationID$.m4s`, `-media_seg_name chunk-$RepresentationID$-$Number$.m4s`. |
| `TestBuildDASHArgs_LowLatencyOptIn` | `LowLatencyDASH=true` → `-ldash 1` present. |
| `TestBuildDASHArgs_GOP48` | `-g 48 -keyint_min 48 -sc_threshold 0` always present (same as HLS). |
| `TestBuildDASHArgs_HwAccelMatchesHLSPath` | Identical encoder swap as HLS for videotoolbox/nvenc/qsv. |

### 5.2 MPD rewrite tests (`manifest_test.go`)

| Test | What it pins |
|---|---|
| `TestRewriteMPD_Dynamic_PassesThroughWhileLive` | Session not EOF → output bytes are byte-identical to the on-disk file. |
| `TestRewriteMPD_StaticAfterEOF` | Session EOF → `type="static"`, `mediaPresentationDuration="PT1H23M45.678S"`, no `minimumUpdatePeriod`. |
| `TestRewriteMPD_FallbackRegexPath` | Hand-crafted MPD with namespaces that break Go's xml.Unmarshal still gets `type="static"` via the regex fallback. |
| `TestRewriteMPD_NotReady_ReturnsErrMPDNotReady` | Manifest file does not exist yet → returns `ErrMPDNotReady` (handler maps to 503 with Retry-After). |
| `TestRewriteMPD_DurationFromProbe` | Source `duration_sec = 5025.123` → MPD shows `PT1H23M45.123S`. |

### 5.3 Handler integration (`handler_test.go`)

Uses a mock-ffmpeg that writes a fixture manifest + init/chunk files
into the session dir.

| Test | What it pins |
|---|---|
| `TestDASHSession_DASHOnly_409OnHLSManifest` | DASH session → GET `manifest.m3u8` returns 409 `format-mismatch`. AC-1. |
| `TestDASHSession_GetMPD_DynamicWhileRunning` | While ffmpeg is running → MPD has `type="dynamic"`, `Cache-Control: no-store`. |
| `TestDASHSession_GetMPD_StaticAfterEOF` | After ffmpeg exits cleanly → next MPD fetch has `type="static"` with `mediaPresentationDuration` matching probe duration. AC-3. |
| `TestDASHSession_StaticPersistsForTTL` | After EOF, `dash_static_ttl_sec=1800` keeps the session row alive; MPD stays static. |
| `TestDASHSession_GetInitSegment` | GET `/stream/{sid}/init-0.m4s` returns the init segment with `Content-Type: video/mp4` and `immutable` cache header. |
| `TestDASHSession_GetChunk` | GET `/stream/{sid}/chunk-0-1.m4s` returns the chunk; missing → 404 problem+json after wait. |
| `TestDASHSession_LiveStaticTransitionAssertedViaTwoFetches` | Sequence: fetch MPD mid-stream → `dynamic`. Stop session. Fetch MPD again → `static`. Same session id. AC-3 acceptance. |
| `TestDASHSession_ShakaPlayerCompat` (manual/CI gated) | A small node script using `shaka-player`'s tooling validates the MPD; gated on having the harness installed. |

### 5.4 DASH-IF MPD validator (`integration_test.go`)

A CI gate using the public DASH-IF MPD validator JAR (or `mpdvalidator`
container image): the produced MPD passes baseline conformance.

| Test | What it pins |
|---|---|
| `TestDASHValidator_BaselineProfile` | The fixture session's `manifest.mpd` is accepted by `mpdvalidator`; failures dump the validator's full report. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Subtitle handling | Identical to HLS (VTT). DASH references VTT via an `AdaptationSet` with `mimeType="text/vtt"` (Story 8.11 owns the wrap; here we just splice it in if present). | Cross-link to 8.11. |
| Format requested but session is HLS | 409 `format-mismatch`. Manifest dispatcher only — never fall through to FFmpeg. | `TestDASHSession_DASHOnly_409OnHLSManifest`. |
| Player keeps polling MPD after EOF | Static MPD remains valid for `dash_static_ttl_sec` (1800 s default); after that the session row is reaped (Story 8.9) and 404 is returned. | `TestDASHSession_StaticPersistsForTTL`. |
| FFmpeg dies before writing manifest.mpd | Session marked failed; manifest fetch returns 503 `transcode-warming-up` with `Retry-After: 2`; the API surfaces 502 to the client per Story 8.8. | `TestDASHSession_MPDNotReady`. |
| Two `format=dash` and `format=hls` open requests for the same video | Independent sessions with independent dirs; no collision (the cache root differs: `cache/dash/` vs `cache/hls/`). | Implicit; covered by no test (single-session path). |
| Live latency tuning | `[dash] low_latency = true` adds `-ldash 1`; we don't change `-seg_duration` (4 s) — true sub-second LLDASH is out of scope. | `TestBuildDASHArgs_LowLatencyOptIn`. |
| MPD has unexpected namespaces | Regex fallback ensures the type/duration patch still works. | `TestRewriteMPD_FallbackRegexPath`. |
| Player sends a chunk request beyond the live window | 404 problem+json (no concept of 410 "rolled off" here — DASH players retry by reloading the MPD; window_size keeps the indices live). | Implicit. |
| Session `RestartFrom` mid-DASH | Same kill-then-respawn as HLS; the new MPD has a higher `minimumUpdatePeriod` boundary; players reload the manifest. | `TestDASHSession_RestartReissuesMPD`. |

## 7. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| FFmpeg binary | ≥ 6.0 | Modern DASH muxer with `-use_timeline 1` and `-ldash` flag. |
| DASH-IF MPD validator (CI gate) | optional | Conformance check on the produced MPD. |

(No new Go module deps.)

## 8. Acceptance checklist

**Manifest semantics (story ACs)**
- [ ] AC-1: `format=dash` session → `manifest.mpd` returns the DASH document; `manifest.m3u8` on the same session → 409 `format-mismatch`.
- [ ] AC-2: A produced MPD passes the DASH-IF baseline validator (CI gate).
- [ ] AC-3: After clean FFmpeg exit, MPD `type` flips from `dynamic` to `static` and `mediaPresentationDuration` is fixed; subsequent fetches see static for `dash_static_ttl_sec`.

**FFmpeg correctness**
- [ ] DASH args builder produces `-init_seg_name init-$RepresentationID$.m4s` and `-media_seg_name chunk-$RepresentationID$-$Number$.m4s` (verified by parsing argv).
- [ ] Forced GOP/keyframe interval matches HLS (`-g 48 -keyint_min 48 -sc_threshold 0`).
- [ ] `-ldash 1` is emitted iff `[dash] low_latency = true`.

**Live → static**
- [ ] Two consecutive MPD fetches (one mid-stream, one post-EOF) return the documented `type` values.
- [ ] Regex fallback handles unmarshal-incompatible MPDs.

**Observability**
- [ ] `dash_sessions_active` gauge.
- [ ] `dash_mpd_swaps_to_static_total` counter.
- [ ] `dash_segments_served_total{rendition}` counter.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[dash]` block.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.6.
