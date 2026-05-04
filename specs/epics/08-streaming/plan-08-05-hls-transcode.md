# Implementation Plan — Story 8.5 HLS Adaptive Transcode Pipeline

> Companion to [story-08-05-hls-transcode.md](story-08-05-hls-transcode.md).
> The story states *what* and *why*; this plan states *how*. Anchored on
> [architecture.md §4.3–4.4](../../architecture.md#43-hls-manifest).
> Builds on the verdict from [Story 8.2](plan-08-02-capability-matrix.md),
> the hwaccel selection in [Story 8.7](plan-08-07-hwaccel-detect.md), and
> the session lifecycle from [Story 8.8](plan-08-08-grpc-server.md) /
> [8.9](plan-08-09-session-store.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/hls/` (manifests, segment serving) and `streaming/internal/ffmpeg/transcode.go` (FFmpeg orchestration). |
| Per-session scratch dir | `cache/hls/{session_id}/` (matches §4.8). Per-rendition subdir `v0/`, `v1/`, `v2/` matching `var_stream_map`. |
| Manifest assembly | We do **not** rely on FFmpeg's master playlist — we generate it from the Verdict.Ladder so we can attach subtitle/audio groups, DATERANGE chapters, and adapt to per-session knobs (Stories 8.11/8.12). FFmpeg writes only the variant playlists and segments. |
| Segment format | MPEG-TS by default (`hls_segment_type=mpegts`). CMAF/fMP4 is an opt-in flag (`format=hls-cmaf` on session open) for stories shipping later. |
| Keyframe interval | Forced 2 s (`-g 48 -keyint_min 48` at 24 fps source-derived). Closed GOPs (`-sc_threshold 0`). |
| Ladder source | `caps.Verdict.Ladder` from Story 8.2's `BuildLadder`. We never re-derive ladder rules here. |
| Out of scope | DASH (8.6), hwaccel selection details (8.7), sub muxing (8.11), chapter DATERANGE (8.12), session table (8.9), slot accounting (8.10). |

## 1. Architecture diagram

```
                       OpenSession (Story 8.8)
                              │
                              ▼
        ┌────────────────────────────────────────────────────┐
        │  hls.Session                                       │
        │   - id, videoID, profile, override                 │
        │   - verdict (Mode=transcode, Ladder=[1080,720,480])│
        │   - dir = cache/hls/{session_id}                   │
        │   - ffmpegProc, ffmpegStarted, ffmpegEOF           │
        │   - segCounters per variant                        │
        └─────────────────────┬──────────────────────────────┘
                              │ Spawn (sync until master playlist exists or 5 s)
                              ▼
        ┌────────────────────────────────────────────────────┐
        │ ffmpeg.Transcode(ctx, plan)                        │
        │   - per-rendition encoder configs                  │
        │   - hwaccel from Story 8.7                         │
        │   - -hls_time 4 -hls_list_size 6                   │
        │   - -hls_flags independent_segments+delete_segments│
        │   - writes:                                        │
        │       v0/index.m3u8, v0/seg-N.ts                   │
        │       v1/index.m3u8, v1/seg-N.ts                   │
        │       v2/index.m3u8, v2/seg-N.ts                   │
        │       (audio is muxed into each variant via         │
        │        var_stream_map; per architecture §9.4 there  │
        │        is no separate /audio/{lang}/seg-N.aac route)│
        └─────────────────────┬──────────────────────────────┘
                              │
                              ▼ (manifest fetched by player)
        ┌────────────────────────────────────────────────────┐
        │  GET /stream/{sid}/manifest.m3u8                    │
        │   master playlist generated in-process from        │
        │   ladder + audio + subs + chapters                  │
        │   Cache-Control: no-store                           │
        ├────────────────────────────────────────────────────┤
        │  GET /stream/{sid}/{rendition}/index.m3u8           │
        │   pass-through reads of ffmpeg-written file         │
        │   on cold-miss waits up to 5 s                      │
        ├────────────────────────────────────────────────────┤
        │  GET /stream/{sid}/{rendition}/seg-{n}.ts           │
        │   serves from disk; waits up to 5 s for the file    │
        │   immutable + max-age=1y                            │
        └────────────────────────────────────────────────────┘
```

A **seek beyond the rolling window** kills the ffmpeg subprocess and
respawns with `-ss <start>`; the session's `media_sequence` is bumped so
the variant playlist's `#EXT-X-MEDIA-SEQUENCE` advances and the player
sees a discontinuity tag.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/hls/session.go` | `Session` struct, lifecycle (Start, Stop, Restart, Seek). |
| `streaming/internal/hls/manifest.go` | `WriteMaster(w, sess)`, `RewriteVariant(w, path, mediaSequence)`. |
| `streaming/internal/hls/segments.go` | `ServeSegment(w, r, sess, variant, seq)` — disk read with wait-loop. |
| `streaming/internal/hls/handler.go` | HTTP handlers wired to chi routes. |
| `streaming/internal/hls/segment_wait.go` | `waitForFile(ctx, path, deadline) error` — fs polling helper. |
| `streaming/internal/hls/discontinuity.go` | Tracks the last `#EXT-X-DISCONTINUITY-SEQUENCE` per session for seek-restart bookkeeping. |
| `streaming/internal/ffmpeg/transcode.go` | `BuildTranscodeArgs(plan) []string`, `Transcode(ctx, plan)`. |
| `streaming/internal/ffmpeg/transcode_test.go` | Args-builder tests (no FFmpeg required). |
| `streaming/internal/hls/session_test.go` | Lifecycle tests with a mock-ffmpeg. |
| `streaming/internal/hls/manifest_test.go` | Master playlist string tests + Apple-tools validation. |
| `streaming/internal/hls/handler_test.go` | End-to-end HTTP tests with fixtures. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/server/router.go` | Wire `/stream/{sid}/manifest.m3u8`, `/stream/{sid}/{rendition}/index.m3u8`, `/stream/{sid}/{rendition}/seg-{n}.ts`. |
| `streaming/internal/observability/metrics.go` | Add `hls_segments_served_total{rendition}`, `hls_segment_starvation_total`, `hls_session_active`, `hls_seek_restart_total`, `hls_session_start_seconds` histogram. |
| `streaming/configs/streaming.toml.example` | `[hls] segment_wait_ms = 5000`, `hls_time = 4`, `hls_list_size = 6`. |
| `specs/epics/08-streaming/README.md` | Tick 8.5. |

### 2.3 Type definitions

```go
// streaming/internal/hls/session.go
package hls

import (
    "context"
    "errors"
    "io"
    "os/exec"
    "path/filepath"
    "sync"
    "time"

    "github.com/google/uuid"

    "maktaba/streaming/internal/caps"
    "maktaba/streaming/internal/ffmpeg"
    "maktaba/streaming/internal/probe"
)

// Session is the in-memory state for one open transcode session. The
// persistent row in `streaming_sessions` is owned by Story 8.9.
type Session struct {
    ID         uuid.UUID
    VideoID    uuid.UUID
    UserID     uuid.UUID
    Dir        string         // cache/hls/{id}
    Probe      *probe.Row
    Verdict    caps.Verdict
    Override   caps.SessionOverride
    HwAccel    ffmpeg.HwAccel
    FFmpegBin  string

    mu               sync.Mutex
    proc             *exec.Cmd
    procDone         chan error
    masterReady      chan struct{}
    startSec         float64
    discSequence     int            // bumped on every restart (seek)
    mediaSeqByVar    map[string]int // last media-sequence emitted per variant
    burnSubs         bool
    closed           bool
}

func New(opts SessionOptions) *Session

func (s *Session) Start(ctx context.Context) error
func (s *Session) Stop(ctx context.Context) error
func (s *Session) RestartFrom(ctx context.Context, startSec float64) error
func (s *Session) IsRunning() bool
func (s *Session) WaitForMaster(ctx context.Context, timeout time.Duration) error
```

```go
// streaming/internal/ffmpeg/transcode.go
package ffmpeg

// HwAccel is what Story 8.7's detector decides at boot (overridable
// per-session). The transcode args builder consults this to swap encoders.
type HwAccel string

const (
    HwAccelNone        HwAccel = "none"
    HwAccelVideoToolbox HwAccel = "videotoolbox"
    HwAccelNVENC       HwAccel = "nvenc"
    HwAccelQSV         HwAccel = "qsv"
)

// TranscodePlan is the structured input. Mirrors caps.Verdict + session-
// specific knobs.
type TranscodePlan struct {
    InputPath    string
    OutputDir    string
    StartSec     float64

    Ladder       []caps.Rendition
    AudioIdx     int
    BurnSubsPath string  // empty = no burn-in (architecture §4.4 burn flow)
    HwAccel      HwAccel

    // HLS knobs.
    HLSTimeSec    int    // default 4
    HLSListSize   int    // default 6
    SegmentType   string // 'mpegts' | 'fmp4'
    GOPFrames     int    // default 48 (2s at 24 fps)
    KeyIntMinFrames int  // default 48
    SCThreshold   int    // 0 (closed GOPs)
}

func BuildTranscodeArgs(plan TranscodePlan) []string
func Transcode(ctx context.Context, plan TranscodePlan, ffmpegBin string) (cmd *exec.Cmd, done <-chan error, err error)
```

### 2.4 The FFmpeg command builder

`BuildTranscodeArgs` is the single source of truth. Pure function; no
side effects. Follows §4.4's reference command:

```go
func BuildTranscodeArgs(p TranscodePlan) []string {
    a := []string{
        "-hide_banner", "-loglevel", "error",
        "-y",
    }

    // Hwaccel decode hint (where supported).
    switch p.HwAccel {
    case HwAccelVideoToolbox:
        a = append(a, "-hwaccel", "videotoolbox")
    case HwAccelNVENC:
        a = append(a, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
    case HwAccelQSV:
        a = append(a, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
    }

    if p.StartSec > 0 {
        // Output-side -ss for accurate seek; input-side fast seek for big jumps.
        a = append(a, "-ss", fmt.Sprintf("%.3f", p.StartSec))
    }
    a = append(a, "-i", p.InputPath)

    // Optional burn-in subtitles (Story 8.5 AC-6).
    var vfPrefix string
    if p.BurnSubsPath != "" {
        vfPrefix = fmt.Sprintf("subtitles='%s':force_style='FontName=Inter,FontSize=22,Outline=1',",
            shellEscape(p.BurnSubsPath))
    }

    // Per-rendition video maps + filter graphs.
    for i, r := range p.Ladder {
        a = append(a, "-map", "0:v:0",
            "-filter:v:"+strconv.Itoa(i),
            fmt.Sprintf("%sscale=-2:%d", vfPrefix, r.Height))

        encoder, encArgs := encoderArgs(p.HwAccel, r)
        a = append(a, "-c:v:"+strconv.Itoa(i), encoder)
        a = append(a, encArgs...)

        a = append(a,
            "-b:v:"+strconv.Itoa(i), strconv.Itoa(r.BitrateKbps)+"k",
            "-maxrate:v:"+strconv.Itoa(i), strconv.Itoa(r.BitrateKbps*110/100)+"k",
            "-bufsize:v:"+strconv.Itoa(i), strconv.Itoa(r.BitrateKbps*200/100)+"k",
            "-g", strconv.Itoa(p.GOPFrames),
            "-keyint_min", strconv.Itoa(p.KeyIntMinFrames),
            "-sc_threshold", strconv.Itoa(p.SCThreshold),
        )
    }

    // Single audio output (per architecture §4.4); multi-language audio
    // is a future story.
    audioIdx := 0
    if p.AudioIdx >= 0 {
        audioIdx = p.AudioIdx
    }
    a = append(a, "-map", fmt.Sprintf("0:a:%d", audioIdx),
        "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "48000")

    // HLS muxer.
    a = append(a,
        "-f", "hls",
        "-hls_time", strconv.Itoa(p.HLSTimeSec),
        "-hls_list_size", strconv.Itoa(p.HLSListSize),
        "-hls_flags", "independent_segments+delete_segments+omit_endlist",
        "-hls_segment_type", p.SegmentType,
        "-hls_segment_filename", filepath.Join(p.OutputDir, "v%v", "seg-%d.ts"),
        "-master_pl_name", "ffmpeg-master.m3u8", // unused; we generate our own master
        "-var_stream_map", varStreamMap(p.Ladder),
        filepath.Join(p.OutputDir, "v%v", "index.m3u8"),
    )
    return a
}

func varStreamMap(l []caps.Rendition) string {
    parts := make([]string, len(l))
    for i, r := range l {
        parts[i] = fmt.Sprintf("v:%d,a:0,name:%s", i, r.Name)
    }
    return strings.Join(parts, " ")
}

func encoderArgs(hw HwAccel, r caps.Rendition) (string, []string) {
    switch hw {
    case HwAccelVideoToolbox:
        return "h264_videotoolbox", []string{"-realtime", "0", "-allow_sw", "1"}
    case HwAccelNVENC:
        return "h264_nvenc", []string{"-preset", "p4", "-rc", "vbr", "-tune", "hq"}
    case HwAccelQSV:
        return "h264_qsv", []string{"-preset", "veryfast"}
    default:
        return "libx264", []string{"-preset", "veryfast", "-crf", crfFor(r.Height)}
    }
}
```

`shellEscape` quotes single-quotes in subtitle paths (FFmpeg's filter
parser requires it). Per Story 8.11 the filter path may contain `[ ]: ,'`.

## 3. Master playlist generation

`hls.WriteMaster` is the in-process generator (we ignore FFmpeg's
master and write our own so we control audio/subs/DATERANGE):

```go
// streaming/internal/hls/manifest.go
package hls

import (
    "fmt"
    "io"
    "strings"

    "maktaba/streaming/internal/caps"
)

// WriteMaster writes the HLS master playlist to w. The structure is:
//   #EXTM3U
//   #EXT-X-VERSION:7
//   #EXT-X-INDEPENDENT-SEGMENTS
//   <#EXT-X-MEDIA AUDIO> per language
//   <#EXT-X-MEDIA SUBTITLES> per track (skipped when burn_subs=true)
//   <#EXT-X-STREAM-INF> per rendition + relative URI
//
// The audio and subtitle groups are populated by Story 8.11 (subs) and a
// future Story (audio). For 8.5 we emit a single audio group `aud-default`
// pointing to the muxed audio output and the subtitle group from 8.11.
func WriteMaster(w io.Writer, s *Session, audio []AudioTrack, subs []SubTrack, chapters []Chapter) error {
    var b strings.Builder
    b.WriteString("#EXTM3U\n")
    b.WriteString("#EXT-X-VERSION:7\n")
    b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n\n")

    // Use chapters.quoteEscape (defined in plan-08-12-chapter-delivery.md
    // §2.5) for NAME and LANGUAGE values. We MUST NOT use Go's `%q` here
    // because it escapes non-ASCII to `\uXXXX`, which mangles Arabic
    // (and other) display names. quoteEscape only escapes `"` and `\`,
    // leaving UTF-8 bytes intact — which is exactly what RFC 8216 §4.2
    // quoted-string requires.
    for _, a := range audio {
        fmt.Fprintf(&b,
            "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud-default\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=YES,URI=\"%s\"\n",
            chapters.QuoteEscape(a.Name), chapters.QuoteEscape(a.Lang), yesNo(a.Default), chapters.QuoteEscape(a.URI))
    }
    if !s.burnSubs {
        for _, sub := range subs {
            fmt.Fprintf(&b,
                "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,FORCED=NO,URI=\"%s\"\n",
                chapters.QuoteEscape(sub.Name), chapters.QuoteEscape(sub.Lang),
                yesNo(sub.Default), yesNo(sub.AutoSelect),
                chapters.QuoteEscape(sub.URI))
        }
    }

    for _, r := range s.Verdict.Ladder {
        codecs := codecsAttr(r) // "avc1.640028,mp4a.40.2" etc.
        attrs := []string{
            fmt.Sprintf("BANDWIDTH=%d", r.BitrateKbps*1000),
            fmt.Sprintf("RESOLUTION=%dx%d", r.Width, r.Height),
            fmt.Sprintf("CODECS=%q", codecs),
            "AUDIO=\"aud-default\"",
        }
        if !s.burnSubs && len(subs) > 0 {
            attrs = append(attrs, "SUBTITLES=\"subs\"")
        }
        fmt.Fprintf(&b, "#EXT-X-STREAM-INF:%s\n%s/index.m3u8\n",
            strings.Join(attrs, ","), r.Name)
    }

    // DATERANGE chapters — Story 8.12 owns the data; we stitch lines here.
    for _, c := range chapters {
        fmt.Fprintln(&b, c.MarshalDATERANGE(s.startedAt))
    }

    _, err := io.WriteString(w, b.String())
    return err
}
```

## 4. Variant playlist serving

The variant `index.m3u8` is the file FFmpeg writes — but we **rewrite
the path on each fetch** to splice in `EXT-X-DISCONTINUITY-SEQUENCE`
when a seek-restart has happened, and to inject `EXT-X-ENDLIST` once
the FFmpeg has exited cleanly:

```go
// streaming/internal/hls/manifest.go (continued)

// RewriteVariant streams the on-disk variant playlist to w with two
// adjustments:
//   1. Insert `#EXT-X-DISCONTINUITY-SEQUENCE:N` after the version line if
//      the session has restarted at least once.
//   2. Append `#EXT-X-ENDLIST` if FFmpeg exited cleanly (sess.IsRunning() == false).
func RewriteVariant(w io.Writer, path string, sess *Session) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer f.Close()

    sc := bufio.NewScanner(f)
    sawVersion := false
    insertedDisc := false
    for sc.Scan() {
        line := sc.Text()
        if _, err := fmt.Fprintln(w, line); err != nil {
            return err
        }
        if !insertedDisc && sess.discSequence > 0 && strings.HasPrefix(line, "#EXT-X-VERSION:") {
            fmt.Fprintf(w, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", sess.discSequence)
            insertedDisc = true
        }
        _ = sawVersion
    }
    if !sess.IsRunning() {
        fmt.Fprintln(w, "#EXT-X-ENDLIST")
    }
    return sc.Err()
}
```

Caching headers per AC:
- master: `Cache-Control: no-store` (dynamic)
- variant: `Cache-Control: no-store` (live window)
- segment: `Cache-Control: public, max-age=31536000, immutable` (AC-3)

## 5. Segment serving with wait-loop

```go
// streaming/internal/hls/segment_wait.go
package hls

func waitForFile(ctx context.Context, path string, max time.Duration) (os.FileInfo, error) {
    deadline := time.Now().Add(max)
    for {
        st, err := os.Stat(path)
        if err == nil && st.Size() > 0 {
            return st, nil
        }
        if time.Now().After(deadline) {
            return nil, os.ErrNotExist
        }
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(100 * time.Millisecond):
        }
    }
}
```

`hls.ServeSegment` wraps the wait + range read:

```go
func (h *Handler) ServeSegment(w http.ResponseWriter, r *http.Request, s *Session, variant string, seq int) {
    if !validVariant(s, variant) || seq < 0 {
        httpx.Write(w, http.StatusNotFound, "segment-not-found",
            "segment does not exist", "")
        return
    }
    if s.IsClosed() {
        httpx.Write(w, http.StatusNotFound, "session-closed",
            "session has ended", "")
        return
    }

    path := filepath.Join(s.Dir, variant, fmt.Sprintf("seg-%d.ts", seq))
    waitMax := time.Duration(h.cfg.SegmentWaitMs) * time.Millisecond

    if seq < s.MinHeldSegment(variant) {
        // Beyond the rolling window — already deleted.
        httpx.Write(w, http.StatusGone, "segment-rolled-off",
            "segment outside rolling window", "")
        return
    }

    if _, err := waitForFile(r.Context(), path, waitMax); err != nil {
        if errors.Is(err, os.ErrNotExist) {
            h.metrics.SegmentStarvation.WithLabelValues(variant).Inc()
            httpx.Write(w, http.StatusNotFound, "segment-not-yet-written",
                "segment not yet available", "")
            return
        }
        return // ctx cancel — client gave up
    }

    w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
    w.Header().Set("Content-Type", "video/MP2T")
    // HLS .ts segment MIME is a fixed handler constant (no DB column).
    h.direct.ServeFileWithContentType(w, r, path, "video/MP2T", &probe.Row{
        Path: path,
    })
    h.metrics.SegmentsServed.WithLabelValues(variant).Inc()
}
```

## 6. Seek triggers cold restart

```go
// streaming/internal/hls/session.go (continued)

// RestartFrom kills the current ffmpeg and respawns from startSec.
// AC-5: seeks beyond the rolling window come through here.
func (s *Session) RestartFrom(ctx context.Context, startSec float64) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.closed {
        return errors.New("session closed")
    }

    // Stop current ffmpeg.
    if err := s.killFFmpegLocked(); err != nil {
        return err
    }

    // Bump discontinuity counter so the variant rewriter inserts the tag.
    s.discSequence++
    s.startSec = startSec

    // Wipe old segments under each variant dir; ffmpeg will start at seq 0
    // again, but the master/variant playlists carry EXT-X-DISCONTINUITY
    // so the player resyncs.
    for _, r := range s.Verdict.Ladder {
        _ = os.RemoveAll(filepath.Join(s.Dir, r.Name))
    }

    return s.startFFmpegLocked(ctx)
}

func (s *Session) startFFmpegLocked(ctx context.Context) error {
    plan := ffmpeg.TranscodePlan{
        InputPath:       s.Probe.Path,
        OutputDir:       s.Dir,
        StartSec:        s.startSec,
        Ladder:          s.Verdict.Ladder,
        AudioIdx:        s.audioIdx,
        BurnSubsPath:    s.burnSubsPath,
        HwAccel:         s.HwAccel,
        HLSTimeSec:      4,
        HLSListSize:     6,
        SegmentType:     "mpegts",
        GOPFrames:       48,
        KeyIntMinFrames: 48,
        SCThreshold:     0,
    }
    cmd, done, err := ffmpeg.Transcode(ctx, plan, s.FFmpegBin)
    if err != nil {
        return err
    }
    s.proc = cmd
    s.procDone = doneToChan(done)
    s.masterReady = make(chan struct{})
    go s.watchMasterReady() // closes masterReady when v0/index.m3u8 appears
    go s.watchProcessExit()
    return nil
}
```

## 7. Test plan

### 7.1 Args-builder unit tests (no FFmpeg required)

`streaming/internal/ffmpeg/transcode_test.go`:

| Test | What it pins |
|---|---|
| `TestBuildTranscodeArgs_BasicLadder` | 3-rung ladder → 3 `-map 0:v:0`, 3 `-c:v:N`, single `-map 0:a:%d`, `-var_stream_map "v:0,a:0,name:1080p v:1,a:0,name:720p v:2,a:0,name:480p"`. |
| `TestBuildTranscodeArgs_HwAccelSwapsEncoder` | `HwAccel=videotoolbox` → `h264_videotoolbox` substituted. |
| `TestBuildTranscodeArgs_BurnSubs_PrefixesFilter` | `BurnSubsPath="/x/y.srt"` → `-filter:v:0` value starts with `subtitles=...`. |
| `TestBuildTranscodeArgs_BurnSubs_EscapesPath` | Path with `'` is escaped (`\'\''` form). |
| `TestBuildTranscodeArgs_KeyframeInterval` | `-g 48 -keyint_min 48 -sc_threshold 0` always present. |
| `TestBuildTranscodeArgs_StartSecAfterMap` | `-ss <N>` appears before `-i` and after the hwaccel hints. |
| `TestBuildTranscodeArgs_SegmentTypeFMP4` | `SegmentType="fmp4"` → `-hls_segment_type fmp4` and segment name uses `.m4s`. |

### 7.2 Manifest tests (`manifest_test.go`)

| Test | What it pins |
|---|---|
| `TestWriteMaster_StructureMatchesArchSpec` | Output matches §4.3 example for a 3-rendition ladder + ar/en audio + ar subs + en subs. Uses a golden file. |
| `TestWriteMaster_BurnSubsOmitsSubsGroup` | `burnSubs=true` → no `#EXT-X-MEDIA:TYPE=SUBTITLES` lines, no `SUBTITLES="subs"` attr. AC-6. |
| `TestWriteMaster_DATERANGEPerChapter` | 3 chapters → 3 `#EXT-X-DATERANGE:CLASS="chapter"` lines. (Story 8.12 owns format; here we test composition.) |
| `TestWriteMaster_BitrateCapTrimsLadder` | Ladder built with `MaxBitrateKbps=1500` → only 480p in master output. AC-4. |
| `TestRewriteVariant_AddsDiscontinuitySequence` | Session with `discSequence=2` → output has `#EXT-X-DISCONTINUITY-SEQUENCE:2` after the version line. |
| `TestRewriteVariant_AddsEndListWhenIdle` | FFmpeg exited cleanly → `#EXT-X-ENDLIST` appended. AC-2. |
| `TestRewriteVariant_NoEndListWhileRunning` | FFmpeg still running → no `#EXT-X-ENDLIST`. |
| `TestMaster_AppleValidator` (integration, gated on `which mediastreamvalidator`) | Apple's HLS validator returns 0 on the produced playlists for a fixture session. CI gate. |

### 7.3 Session lifecycle (`session_test.go`)

Uses a `mockTranscoder` swapping `ffmpeg.Transcode` for a function that
writes a fixture master + variant + a few segments to disk.

| Test | What it pins |
|---|---|
| `TestSession_StartCreatesMasterWithin5s` | `Start` → `WaitForMaster` returns within 5 s; AC-1's window. |
| `TestSession_StopKillsFFmpegWithin2s` | `Stop` → underlying process dead within 2 s grace. |
| `TestSession_RestartBumpsDiscontinuity` | `RestartFrom(600.0)` → `discSequence` increments by 1; old variant dirs wiped; new master playlist regenerable. |
| `TestSession_FFmpegCrashTriggersClose` | mock exits non-zero → session.IsRunning() goes false; `Stop` is idempotent; `streaming_sessions` row marked failed (delegated to 8.9 hook). AC edge case. |
| `TestSession_StopOnAlreadyClosedSession` | Stop then Stop → both no-op (idempotent). |

### 7.4 Handler integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestHandler_GetMasterReturnsAssembledPlaylist` | Authenticated GET → master matches the WriteMaster output, status 200, `Cache-Control: no-store`. AC-1. |
| `TestHandler_GetVariantReturnsRewrittenIndex` | GET `v0/index.m3u8` → contains the live segments and DISCONTINUITY-SEQUENCE if applicable. AC-2. |
| `TestHandler_GetSegmentImmutable` | GET `v0/seg-3.ts` → 200, `Cache-Control: public, max-age=31536000, immutable`, `Content-Type: video/MP2T`. AC-3. |
| `TestHandler_GetSegmentWaitsThenServes` | Request seg-9 before mock-ffmpeg writes it; mock writes after 600 ms; handler returns 200. |
| `TestHandler_GetSegmentTimesOut404` | Request seg-99 (never written); after `segment_wait_ms` timeout → 404 problem+json; `hls_segment_starvation_total` increments. |
| `TestHandler_GetSegmentBeforeRollingWindow_410` | Request seg-0 after FFmpeg has rolled past it → 410 `segment-rolled-off`. AC edge case. |
| `TestHandler_GetSegmentClosedSession_404Fast` | Closed session → 404 immediately, does not wait `segment_wait_ms`. AC edge case. |
| `TestHandler_BitrateCap1500_Only480p` | Open session with `max_bitrate_kbps=1500` → master playlist contains exactly one variant (480p). AC-4. |
| `TestHandler_SeekBeyondWindow_TriggersRestart` | Player asks for a segment with seq beyond the rolling window; handler returns 410, the client makes a new session via API or re-opens manifest after `RestartFrom` (driven by 8.8). The test asserts: invoking `Session.RestartFrom(600)` produces a v0/seg-0.ts within 2 s p95. AC-5. |
| `TestHandler_BurnSubsManifestHasNoSubsGroup` | Open with `burn_subs=true` → master has no `#EXT-X-MEDIA:TYPE=SUBTITLES`; variant videos contain visible cue pixels (asserted by `ffprobe -show_frames` finding non-uniform pixel histograms — sanity gate). AC-6. |

### 7.5 Performance and correctness gates

| Test | What it pins |
|---|---|
| `TestEndToEnd_HLSValidator` (gated on `mediastreamvalidator`) | A 30 s sample MKV transcoded → master + 3 variant playlists + ≥ 8 segments; Apple's validator returns 0. |
| `TestEndToEnd_RollingWindow` | After 7 segments are written, the variant playlist lists segments 1–7 (window = 6) AND seg-0.ts is unlinked from disk within 1 s. |
| `TestEndToEnd_SeekResumesUnder2sP95` | 100 trials of `RestartFrom(600)` → p95 of (start → first segment served) ≤ 2 s on local SSD. AC-5 acceptance. |

## 8. Test code scaffolding

```go
// streaming/internal/ffmpeg/transcode_test.go
package ffmpeg_test

import (
    "strings"
    "testing"

    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/caps"
    "maktaba/streaming/internal/ffmpeg"
)

func TestBuildTranscodeArgs_BasicLadder(t *testing.T) {
    plan := ffmpeg.TranscodePlan{
        InputPath:       "/m/in.mkv",
        OutputDir:       "/c/sess",
        Ladder:          []caps.Rendition{
            {Name: "1080p", Width: 1920, Height: 1080, BitrateKbps: 4500, Codec: "h264", Profile: "high"},
            {Name: "720p",  Width: 1280, Height:  720, BitrateKbps: 2500, Codec: "h264", Profile: "main"},
            {Name: "480p",  Width:  854, Height:  480, BitrateKbps:  900, Codec: "h264", Profile: "main"},
        },
        AudioIdx:        0,
        HLSTimeSec:      4,
        HLSListSize:     6,
        SegmentType:     "mpegts",
        GOPFrames:       48,
        KeyIntMinFrames: 48,
        SCThreshold:     0,
    }
    args := ffmpeg.BuildTranscodeArgs(plan)
    line := strings.Join(args, " ")

    // BuildTranscodeArgs returns []string; joining with " " gives no
    // shell quoting (the slice already separates flag and value). So
    // the assertion must match what's actually produced: the next
    // element after `-var_stream_map` is the literal map argument with
    // no surrounding quotes.
    varStreamMap := "v:0,a:0,name:1080p v:1,a:0,name:720p v:2,a:0,name:480p"

    require.Contains(t, line, "-i /m/in.mkv")
    require.Equal(t, 3, strings.Count(line, "-map 0:v:0"))
    require.Contains(t, line, "-var_stream_map "+varStreamMap)
    // Belt-and-braces: also verify the map appears as its own element
    // in the slice (i.e., FFmpeg will receive a single argv entry).
    require.Contains(t, args, varStreamMap)
    require.Contains(t, line, "-g 48")
    require.Contains(t, line, "-keyint_min 48")
    require.Contains(t, line, "-sc_threshold 0")
    require.Contains(t, line, "-hls_time 4")
    require.Contains(t, line, "-hls_list_size 6")
    require.Contains(t, line, "-hls_flags independent_segments+delete_segments+omit_endlist")
}

func TestBuildTranscodeArgs_BurnSubs_EscapesPath(t *testing.T) {
    plan := ffmpeg.TranscodePlan{
        InputPath:    "/m/in.mkv",
        OutputDir:    "/c/sess",
        BurnSubsPath: "/m/it's mine.srt",
        Ladder: []caps.Rendition{
            {Name: "720p", Width: 1280, Height: 720, BitrateKbps: 2500},
        },
    }
    args := ffmpeg.BuildTranscodeArgs(plan)
    joined := strings.Join(args, " ")
    require.Contains(t, joined, `subtitles='/m/it\'\''s mine.srt'`)
}
```

## 9. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Player requests seg-0 after FFmpeg rolled past it | 410 `segment-rolled-off`. Avoids confusing 404 → "segment not yet written" misread. | `TestHandler_GetSegmentBeforeRollingWindow_410` |
| FFmpeg crashes mid-stream | `procDone` channel emits non-nil error; session marked closed; reaper (8.9) finds it; subsequent segment requests get 404 fast. | `TestSession_FFmpegCrashTriggersClose` + `TestHandler_GetSegmentClosedSession_404Fast` |
| Player asks for a segment of a closed session | 404 immediately, no wait. | `TestHandler_GetSegmentClosedSession_404Fast` |
| Network filesystem latency causes encoder to fall behind player | `hls_segment_starvation_total` increments; player downshifts via ABR (browser behavior). We surface the metric, no auto-downshift on server side. | `TestHandler_GetSegmentTimesOut404` |
| Independent segment alignment for ABR | Forced `-g 48 -keyint_min 48 -sc_threshold 0` aligns keyframes at 2 s; switching renditions at any segment boundary is safe. | `TestBuildTranscodeArgs_KeyframeInterval` |
| `burn_subs=true` + bitrate cap | Both apply: ladder is trimmed AND filter graph is prefixed; manifest has no subs group. | Two-pronged tests in §7.2 + §7.4. |
| Master playlist requested before FFmpeg first segment | We block in `WaitForMaster` for up to 5 s; timeout → 503 `transcode-warming-up`. (FFmpeg writes the first segment then the variant playlist; we treat existence of v0/index.m3u8 as readiness.) | `TestSession_StartCreatesMasterWithin5s` |
| FFmpeg writes a partial seg-N.ts then crashes | `waitForFile` requires `Size() > 0`; partial writes (still appending) are tolerated by the player's HTTP range layer. We do NOT delete partial files — FFmpeg's atomic-rename per segment means partials shouldn't appear in steady state. | `validateRemuxOutput`-equivalent for partial seg detection in stress test. |
| Two seek-restarts within 1 s | The discontinuity counter increments twice; `RestartFrom` is mutex-locked; the second invocation kills the just-spawned ffmpeg before it writes any segment. | `TestSession_RestartIdempotent`. |
| `force_software=true` after detection picked NVENC | Story 8.7 returns `HwAccelNone`; args builder selects libx264 — verified by inspecting argv. | Cross-tested in `TestBuildTranscodeArgs_HwAccelSwapsEncoder`. |
| Bitrate cap zero-rendition edge | Ladder builder (Story 8.2) keeps at least one fallback rung; the master playlist never has zero variants. | `TestLadder_AlwaysOneRung` (cross-link). |

## 10. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| FFmpeg binary | ≥ 6.0 | `delete_segments`, `omit_endlist`, fmp4 segment type, modern HLS muxer; pinned in `streaming/Dockerfile`. |
| `mediastreamvalidator` (Apple HLS Tools) | optional CI gate | Conformance check; tests skip when not installed. |

(No new Go module deps.)

## 11. Acceptance checklist

**Manifest (story ACs)**
- [ ] AC-1: Master playlist matches §4.3 with `Cache-Control: no-store`.
- [ ] AC-2: Variant playlist updates with rolling window; `EXT-X-MEDIA-SEQUENCE` advances; `EXT-X-ENDLIST` on clean exit.
- [ ] AC-3: Segments served with `video/MP2T`, immutable cache, JWT enforced; missing → wait up to 5 s then 404.
- [ ] AC-4: `max_bitrate_kbps=1500` → only 480p in the master.
- [ ] AC-5: Seek beyond window → cold restart, discontinuity tag, p95 ≤ 2 s to first segment.
- [ ] AC-6: `burn_subs=true` → manifest has no subs group; visible cues in rendered video.

**FFmpeg**
- [ ] Args builder enforces `-g 48 -keyint_min 48 -sc_threshold 0`.
- [ ] Burn-subs path is shell-escaped before splicing into `-vf`.
- [ ] Hwaccel encoders swap correctly (videotoolbox/nvenc/qsv/libx264).
- [ ] `-hls_segment_type` is configurable (mpegts default; fmp4 supported).
- [ ] Process group set so context cancel kills children cleanly.

**Lifecycle**
- [ ] `Start` blocks until v0/index.m3u8 exists or 5 s elapses.
- [ ] `Stop` SIGTERM then SIGKILL after 2 s grace.
- [ ] `RestartFrom` wipes variant dirs and bumps `discSequence`.

**Conformance**
- [ ] `mediastreamvalidator` returns 0 against a fixture session (CI gate).
- [ ] Rolling window deletes seg-0 from disk within 1 s of being dropped from the playlist.

**Observability**
- [ ] `hls_segments_served_total{rendition}` counter.
- [ ] `hls_segment_starvation_total{rendition}` counter.
- [ ] `hls_seek_restart_total` counter.
- [ ] `hls_session_start_seconds` histogram.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[hls]` block.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.5.
