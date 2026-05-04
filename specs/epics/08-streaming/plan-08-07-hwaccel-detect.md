# Implementation Plan — Story 8.7 Hardware Acceleration Auto-Detect

> Companion to [story-08-07-hwaccel-detect.md](story-08-07-hwaccel-detect.md).
> The story states *what* and *why*; this plan states *how*. Builds on
> [Story 8.5](plan-08-05-hls-transcode.md)'s args builder and feeds
> [Story 8.8](plan-08-08-grpc-server.md)'s `Capabilities` RPC.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/hwaccel/` — pure Go, all detection logic. |
| Detection time | Boot. Result is cached in-process and exposed via `GetCapabilities`. Re-detection is opt-in via SIGHUP / `LISTEN streaming_settings_changed`. |
| Probe approach | Parse `ffmpeg -hide_banner -encoders` and `-hwaccels` output; on Linux, additionally probe `nvidia-smi` (NVENC), `/dev/dri/renderD128` + `vainfo` (QSV/VA-API). |
| Per-session override | `caps.SessionOverride.ForceSoftware` already exists (Story 8.2); the plan-builder consults the detector for the default and the override for the per-session swap. |
| Failure fallback | `Try (hwaccel) → fail before segment 1 → Restart with software → fail again → close session 502.` Driven by `hls.Session.observe` in Story 8.5. |
| NVENC concurrent-session cap | Tracked by a counter (per-host) hooked into Story 8.10's slot accounting; this plan only exposes the metric. |
| Out of scope | The actual encoder-arg swapping (Stories 8.5/8.6 own those). The session restart-on-failure orchestration (8.5 + 8.10). |

## 1. Architecture diagram

```
            boot
              │
              ▼
    ┌──────────────────────────────────────────────────────┐
    │ hwaccel.Detect(ffmpegBin) → Capabilities             │
    │                                                      │
    │ Steps (run sequentially, fall through on failure):   │
    │   1. Probe `ffmpeg -encoders` for known names.       │
    │   2. Platform probes:                                │
    │      - macOS arm64/x86 → check h264_videotoolbox is  │
    │        actually usable (run a 1-second test encode   │
    │        on a black frame — negligible cost, catches   │
    │        builds that lack VideoToolbox).               │
    │      - Linux + nvidia-smi exit 0 + h264_nvenc        │
    │        encoder present → NVENC.                       │
    │      - Linux + /dev/dri/renderD128 readable +        │
    │        h264_qsv → QuickSync.                         │
    │   3. Else → libx264 software.                        │
    │                                                      │
    │ Return: Capabilities{ Encoder, Decoder, Reason,      │
    │                       SessionConcurrencyCap }         │
    └─────────────────────────────────────────────────────┘
              │
              ▼
    ┌──────────────────────────────────────────────────────┐
    │ Stored in hwaccel.Detected (atomic.Pointer)          │
    │ Consumed by:                                         │
    │   - ffmpeg.BuildTranscodeArgs / BuildDASHArgs        │
    │   - grpc.GetCapabilities (Story 8.8)                 │
    │   - GET /api/stream/capabilities surface             │
    │   - hls.Session restart-on-failure path (Story 8.5)  │
    └──────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/hwaccel/hwaccel.go` | `Detect()`, `Capabilities`, `HwAccel` enum re-export. |
| `streaming/internal/hwaccel/probe_macos.go` (build-tagged `//go:build darwin`) | macOS-specific videotoolbox probe. |
| `streaming/internal/hwaccel/probe_linux.go` (build-tagged `//go:build linux`) | NVENC + QSV probes. |
| `streaming/internal/hwaccel/probe_other.go` (build-tagged `//go:build !linux && !darwin`) | Stub: returns software. |
| `streaming/internal/hwaccel/parse.go` | Parsers for `ffmpeg -encoders`, `ffmpeg -hwaccels`, `nvidia-smi`, `vainfo`. |
| `streaming/internal/hwaccel/hwaccel_test.go` | Selection-table tests. |
| `streaming/internal/hwaccel/parse_test.go` | Parser tests against fixture outputs. |
| `streaming/internal/hwaccel/integration_test.go` | Hits real ffmpeg (CI gate). |
| `streaming/internal/hwaccel/fallback.go` | `MarkSessionHwFailed(videoID)` registry — Story 8.5 calls this when the FFmpeg path fails before segment 1. |
| `streaming/internal/hwaccel/testdata/` | Fixture stdout from `ffmpeg -encoders` for each platform (macos-arm64, macos-x86, linux-nvidia, linux-intel, linux-none, windows-intel). |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/cmd/maktaba-streaming/main.go` | Call `hwaccel.Detect` at boot; log selection; expose via dependency container. |
| `streaming/internal/ffmpeg/transcode.go` | The `encoderArgs` switch consumes `hwaccel.HwAccel` (already imported in 8.5; here we make the source-of-truth `hwaccel.Detected.Load()`). |
| `streaming/internal/observability/metrics.go` | `hwaccel_detection_failed_total{stage}`, `hwaccel_session_capacity_exceeded_total{accel}`, `hwaccel_session_failed_fallback_total{accel}`. |
| `streaming/configs/streaming.toml.example` | `[hwaccel] prefer = "auto"` already set in 8.1; we document the values here. |
| `specs/epics/08-streaming/README.md` | Tick 8.7. |

### 2.3 Type definitions

```go
// streaming/internal/hwaccel/hwaccel.go
package hwaccel

import (
    "context"
    "sync/atomic"
)

// HwAccel mirrors ffmpeg.HwAccel (re-export to avoid import cycles).
// Code paths use `hwaccel.HwAccel`; ffmpeg package consumes via interface.
type HwAccel string

const (
    None        HwAccel = "none"
    VideoToolbox HwAccel = "videotoolbox"
    NVENC       HwAccel = "nvenc"
    QSV         HwAccel = "qsv"
)

// Capabilities is the immutable result of one detection pass.
type Capabilities struct {
    Encoder              HwAccel       // h264 encoder lane (the one used by Stories 8.5/8.6)
    HEVCEncoder          HwAccel       // hevc encoder lane (for future stories)
    Decoder              HwAccel       // matching decoder hint, or None
    EncoderName          string        // 'h264_videotoolbox', 'h264_nvenc', 'h264_qsv', 'libx264'
    Reason               string        // human-readable; goes to logs and GetCapabilities
    SessionConcurrencyCap int          // 0 = unlimited; 3 for consumer NVENC SKUs
    DetectedAt           time.Time
    FFmpegVersion        string
}

// Detect probes the host. Pure of side effects beyond invoking ffmpeg /
// nvidia-smi / vainfo as child processes; returns the first failing-soft
// step's reason in Capabilities.Reason.
func Detect(ctx context.Context, cfg DetectConfig) (*Capabilities, error)

// DetectConfig allows the operator to bias toward software.
type DetectConfig struct {
    FFmpegBin   string
    FFprobeBin  string
    Prefer      string // 'auto', 'videotoolbox', 'nvenc', 'qsv', 'software'
    SkipTestEncode bool // skip the 1-second test encode in CI
}
```

### 2.4 Detection algorithm

```go
// streaming/internal/hwaccel/hwaccel.go
package hwaccel

func Detect(ctx context.Context, cfg DetectConfig) (*Capabilities, error) {
    encs, err := parseEncoders(ctx, cfg.FFmpegBin)
    if err != nil {
        return softwareFallback("ffmpeg -encoders failed: " + err.Error()), nil
    }
    ver := parseFFmpegVersion(ctx, cfg.FFmpegBin)

    // Operator pin overrides everything.
    switch strings.ToLower(cfg.Prefer) {
    case "software":
        return softwareWithVersion(ver, "operator pinned to software"), nil
    case "videotoolbox":
        if !encs.Has("h264_videotoolbox") {
            return softwareWithVersion(ver, "operator preferred videotoolbox but encoder absent"), nil
        }
        return &Capabilities{Encoder: VideoToolbox, EncoderName: "h264_videotoolbox",
            Reason: "operator pinned to videotoolbox", FFmpegVersion: ver, DetectedAt: time.Now()}, nil
    case "nvenc":
        if !encs.Has("h264_nvenc") {
            return softwareWithVersion(ver, "operator preferred nvenc but encoder absent"), nil
        }
        return &Capabilities{Encoder: NVENC, EncoderName: "h264_nvenc",
            SessionConcurrencyCap: nvencCapFromSMI(ctx),
            Reason: "operator pinned to nvenc", FFmpegVersion: ver, DetectedAt: time.Now()}, nil
    case "qsv":
        if !encs.Has("h264_qsv") {
            return softwareWithVersion(ver, "operator preferred qsv but encoder absent"), nil
        }
        return &Capabilities{Encoder: QSV, EncoderName: "h264_qsv",
            Reason: "operator pinned to qsv", FFmpegVersion: ver, DetectedAt: time.Now()}, nil
    }

    // Auto: platform-specific probe order.
    return autoDetect(ctx, cfg, encs, ver)
}
```

```go
// streaming/internal/hwaccel/probe_macos.go
//go:build darwin

package hwaccel

func autoDetect(ctx context.Context, cfg DetectConfig, encs Encoders, ver string) (*Capabilities, error) {
    if !encs.Has("h264_videotoolbox") {
        return softwareWithVersion(ver, "h264_videotoolbox not in `ffmpeg -encoders` (probably static build without VideoToolbox)"), nil
    }
    if cfg.SkipTestEncode {
        return &Capabilities{Encoder: VideoToolbox, EncoderName: "h264_videotoolbox",
            Reason: "videotoolbox detected (test-encode skipped)", FFmpegVersion: ver,
            DetectedAt: time.Now()}, nil
    }
    if err := testEncodeVideoToolbox(ctx, cfg.FFmpegBin); err != nil {
        return softwareWithVersion(ver, "videotoolbox test encode failed: "+err.Error()), nil
    }
    return &Capabilities{Encoder: VideoToolbox, EncoderName: "h264_videotoolbox",
        Reason: "videotoolbox auto-detected", FFmpegVersion: ver, DetectedAt: time.Now()}, nil
}

// testEncodeVideoToolbox runs `ffmpeg -f lavfi -i color=c=black:s=320x240:d=1
//   -c:v h264_videotoolbox -frames:v 24 -f null -` and asserts exit 0.
// One-second runtime; cheap.
func testEncodeVideoToolbox(ctx context.Context, ffmpegBin string) error {
    cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    cmd := exec.CommandContext(cctx, ffmpegBin,
        "-hide_banner", "-loglevel", "error",
        "-f", "lavfi", "-i", "color=c=black:s=320x240:d=1",
        "-c:v", "h264_videotoolbox", "-frames:v", "24", "-f", "null", "-")
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("%w: stderr=%s", err, strings.TrimSpace(stderr.String()))
    }
    return nil
}
```

```go
// streaming/internal/hwaccel/probe_linux.go
//go:build linux

package hwaccel

func autoDetect(ctx context.Context, cfg DetectConfig, encs Encoders, ver string) (*Capabilities, error) {
    // 1. NVENC.
    if encs.Has("h264_nvenc") && nvidiaSMIWorks(ctx) {
        return &Capabilities{
            Encoder: NVENC, EncoderName: "h264_nvenc",
            SessionConcurrencyCap: nvencCapFromSMI(ctx),
            Reason: "nvenc auto-detected (nvidia-smi reachable)",
            FFmpegVersion: ver, DetectedAt: time.Now(),
        }, nil
    }
    // 2. QSV.
    if encs.Has("h264_qsv") && qsvDeviceUsable(ctx) {
        return &Capabilities{
            Encoder: QSV, EncoderName: "h264_qsv",
            Reason: "qsv auto-detected (/dev/dri/renderD128 + vainfo)",
            FFmpegVersion: ver, DetectedAt: time.Now(),
        }, nil
    }
    // 3. Software.
    return softwareWithVersion(ver, "no hwaccel detected on linux"), nil
}

func nvidiaSMIWorks(ctx context.Context) bool {
    cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()
    return exec.CommandContext(cctx, "nvidia-smi", "-L").Run() == nil
}

// nvencCapFromSMI reads `nvidia-smi -q -d ENCODER` and returns the
// max concurrent sessions per the consumer-GPU restriction. Returns 0
// (unlimited) on data-center GPUs.
func nvencCapFromSMI(ctx context.Context) int {
    out, err := exec.CommandContext(ctx, "nvidia-smi",
        "--query-gpu=name", "--format=csv,noheader").Output()
    if err != nil {
        return 3 // safe default for consumer GPUs
    }
    name := strings.TrimSpace(string(out))
    if isDataCenterGPU(name) { // A100, H100, T4, L4, etc.
        return 0
    }
    return 3
}

func qsvDeviceUsable(ctx context.Context) bool {
    if _, err := os.Stat("/dev/dri/renderD128"); err != nil {
        return false
    }
    cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()
    return exec.CommandContext(cctx, "vainfo").Run() == nil
}
```

### 2.5 Encoder-list parser

```go
// streaming/internal/hwaccel/parse.go
package hwaccel

type Encoders struct {
    set map[string]bool
}

func (e Encoders) Has(name string) bool { return e.set[name] }

// parseEncoders runs `ffmpeg -hide_banner -encoders` and returns the set
// of encoder short names. The ffmpeg output looks like:
//
//   V..... libx264              libx264 H.264 / AVC
//   V..... h264_videotoolbox    H.264 (VideoToolbox)
//   V.S... hevc_qsv             HEVC (Intel Quick Sync Video)
//
// We extract the second column.
func parseEncoders(ctx context.Context, bin string) (Encoders, error) {
    out, err := exec.CommandContext(ctx, bin, "-hide_banner", "-encoders").Output()
    if err != nil {
        return Encoders{}, err
    }
    set := make(map[string]bool, 256)
    sc := bufio.NewScanner(bytes.NewReader(out))
    for sc.Scan() {
        line := sc.Text()
        if len(line) < 8 || line[0] != ' ' {
            continue
        }
        // The flag block is 6 chars, then space, then name.
        fields := strings.Fields(line[7:])
        if len(fields) >= 1 {
            set[fields[0]] = true
        }
    }
    if len(set) == 0 {
        return Encoders{}, errors.New("ffmpeg -encoders produced no parseable rows")
    }
    return Encoders{set: set}, nil
}
```

## 3. Test plan

### 3.1 Selection-table tests (`hwaccel_test.go`)

Stubs the platform-probe functions so we can exercise the decision tree:

| Platform fixture | Encoders present | Expected Encoder | Expected Reason fragment |
|---|---|---|---|
| macos-arm64 | h264_videotoolbox | VideoToolbox | "videotoolbox auto-detected" |
| macos-x86 | h264_videotoolbox (Intel Macs) | VideoToolbox | "videotoolbox auto-detected" |
| linux-nvidia | h264_nvenc, libx264 + nvidia-smi works | NVENC | "nvenc auto-detected" |
| linux-intel | h264_qsv, libx264 + /dev/dri/renderD128 + vainfo OK | QSV | "qsv auto-detected" |
| linux-none | libx264 only | None (libx264) | "no hwaccel detected on linux" |
| windows-intel | h264_qsv, libx264 (we treat the platform as a flag-tagged stub returning software) | None | "windows: software fallback" |
| operator pin software | macos-arm64 with h264_videotoolbox | None | "operator pinned to software" |
| operator pin nvenc on macOS (no encoder) | h264_videotoolbox only | None | "operator preferred nvenc but encoder absent" |

Implementation:

```go
// streaming/internal/hwaccel/hwaccel_test.go
package hwaccel_test

func TestDetect_TableDriven(t *testing.T) {
    rows := []struct {
        name      string
        platform  string // matches build-tag fixture
        encoders  []string
        nvSMI     bool
        qsvDev    bool
        prefer    string
        wantAccel hwaccel.HwAccel
        wantSubstr string
    }{
        {"macos arm64 auto", "darwin", []string{"libx264", "h264_videotoolbox"},
            false, false, "auto", hwaccel.VideoToolbox, "videotoolbox auto-detected"},
        {"linux nvidia auto", "linux", []string{"libx264", "h264_nvenc"},
            true, false, "auto", hwaccel.NVENC, "nvenc auto-detected"},
        {"linux qsv auto", "linux", []string{"libx264", "h264_qsv"},
            false, true, "auto", hwaccel.QSV, "qsv auto-detected"},
        {"linux fallback", "linux", []string{"libx264"},
            false, false, "auto", hwaccel.None, "no hwaccel"},
        {"force software", "darwin", []string{"libx264", "h264_videotoolbox"},
            false, false, "software", hwaccel.None, "operator pinned to software"},
        {"operator nvenc not present", "darwin", []string{"libx264", "h264_videotoolbox"},
            false, false, "nvenc", hwaccel.None, "encoder absent"},
    }
    for _, r := range rows {
        t.Run(r.name, func(t *testing.T) {
            // Override the package-private hooks for this test.
            hwaccel.SetParseEncodersForTest(func(_ context.Context, _ string) (hwaccel.Encoders, error) {
                return hwaccel.NewEncodersForTest(r.encoders), nil
            })
            hwaccel.SetNvidiaSMIWorksForTest(func(_ context.Context) bool { return r.nvSMI })
            hwaccel.SetQSVUsableForTest(func(_ context.Context) bool { return r.qsvDev })
            hwaccel.SetTestEncodeForTest(func(_ context.Context, _ string) error { return nil })

            cap, err := hwaccel.Detect(context.Background(), hwaccel.DetectConfig{
                FFmpegBin: "ffmpeg", Prefer: r.prefer,
            })
            require.NoError(t, err)
            require.Equal(t, r.wantAccel, cap.Encoder)
            require.Contains(t, cap.Reason, r.wantSubstr)
        })
    }
}
```

### 3.2 Parser tests (`parse_test.go`)

Fixture file `testdata/ffmpeg_encoders_macos.txt` etc., asserted against
`parseEncoders`:

| Test | What it pins |
|---|---|
| `TestParseEncoders_MacOSARM64` | Reads fixture; asserts `h264_videotoolbox`, `libx264`, `aac` are all in the set. |
| `TestParseEncoders_LinuxNoHwAccel` | Fixture from a CPU-only build; `h264_nvenc` and `h264_qsv` absent. |
| `TestParseEncoders_EmptyOutputErrors` | `ffmpeg -encoders` returns empty stdout → `parseEncoders` returns an error (we don't silently fall through to software because something is wrong with the install). |
| `TestParseEncoders_GarbageLinesIgnored` | Line `==========` etc. don't break the parser. |
| `TestParseFFmpegVersion` | Fixture with `ffmpeg version 6.1` → parses to `"6.1"`. |
| `TestNvencCapFromSMI_ConsumerGPU` | Mock `nvidia-smi` returning `GeForce RTX 4090` → cap = 3. |
| `TestNvencCapFromSMI_DataCenterGPU` | Mock returning `NVIDIA A100-PCIE-40GB` → cap = 0. |

### 3.3 Integration tests (gated)

| Test | What it pins |
|---|---|
| `TestIntegration_RealFFmpegSelfDetect` (skipped unless `MAKTABA_TEST_HWACCEL=1`) | Runs against real ffmpeg on the host; asserts `Detect` returns a non-nil `Capabilities` and the encoder is the platform-expected one. |
| `TestIntegration_TestEncodeVideoToolbox` (skipped unless darwin) | Runs the 1-second test encode; asserts exit 0. |
| `TestIntegration_ForceSoftwareSessionHasNoVideoToolboxArgs` | Spawn a fixture session with `force_software=true` and inspect `ps -o command=` on the spawned ffmpeg → no `videotoolbox` substring in argv. (Cross-link: this tests the **integration** with Story 8.5's args builder.) |

### 3.4 Failure-fallback tests (`hls_session_test.go` extension)

These are technically Story 8.5 territory but referenced here for traceability:

| Test | What it pins |
|---|---|
| `TestSession_HwAccelFailsBeforeSegment1_RestartSoftware` | Mock-ffmpeg exits 1 within 100 ms (no segment written); session restarts once with `HwAccelNone`; second invocation succeeds. The mock asserts arg ordering changed (no `videotoolbox`/`nvenc`/`qsv`). |
| `TestSession_HwAccelFailsTwice_502Close` | Both attempts fail → session closed with 502 `transcode-failed`; `streaming_sessions.closed_reason='hw_failed_software_failed'`; `hwaccel_session_failed_fallback_total{accel="videotoolbox"}` counter increments. |
| `TestSession_NVENCSessionCapacityExceeded` | `nvencCapFromSMI` returns 3; open 3 sessions then a 4th — the 4th uses libx264 software despite NVENC being the default. `hwaccel_session_capacity_exceeded_total{accel="nvenc"}` ticks once. |

## 4. Test code scaffolding

```go
// streaming/internal/hwaccel/parse_test.go
package hwaccel_test

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/hwaccel"
)

func TestParseEncoders_MacOSARM64(t *testing.T) {
    data, err := os.ReadFile(filepath.Join("testdata", "ffmpeg_encoders_macos.txt"))
    require.NoError(t, err)

    encs := hwaccel.ParseEncodersFromBytesForTest(data)
    require.True(t, encs.Has("h264_videotoolbox"))
    require.True(t, encs.Has("libx264"))
    require.True(t, encs.Has("aac"))
    require.False(t, encs.Has("h264_nvenc"))
    require.False(t, encs.Has("h264_qsv"))
}

func TestParseEncoders_GarbageLinesIgnored(t *testing.T) {
    data := []byte(`Encoders:
 V..... = Video
 ------
 V..... libx264              libx264 H.264 / AVC
 V..... h264_videotoolbox    H.264 (VideoToolbox)
random garbage line
 V.S... hevc_qsv             HEVC (Intel Quick Sync Video)
`)
    encs := hwaccel.ParseEncodersFromBytesForTest(data)
    require.True(t, encs.Has("libx264"))
    require.True(t, encs.Has("h264_videotoolbox"))
    require.True(t, encs.Has("hevc_qsv"))
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Hardware decoder limit on consumer NVENC | `SessionConcurrencyCap` set to 3; Story 8.10's slot accounting checks against this and falls back to software for sessions over the cap. Metric `hwaccel_session_capacity_exceeded_total` increments. | `TestSession_NVENCSessionCapacityExceeded` |
| Source uses an HW-unsupported feature (HEVC 10-bit on a NVENC SKU that lacks it) | Detected by AC-3 fallback in Story 8.5: ffmpeg fails before segment 1, session restarts software. `hwaccel_session_failed_fallback_total{accel="nvenc",reason="bitdepth"}` (reason from stderr classification). | `TestSession_HwAccelFailsBeforeSegment1_RestartSoftware` |
| FFmpeg static build without VideoToolbox on macOS | Encoder list lacks `h264_videotoolbox`; we fall back to software with explicit reason in `Capabilities.Reason`. | `TestDetect_TableDriven[macos no videotoolbox]`. |
| `nvidia-smi` exists but errors (driver missing) | `nvidiaSMIWorks` returns false; QSV next, else software. | `TestDetect_TableDriven[linux nvidia no driver]`. |
| `vainfo` not installed | `qsvDeviceUsable` returns false; software fallback. | Stub in test table. |
| Operator pin to nvenc on a host without nvenc | Logged warning + software fallback (`"encoder absent"` in `Reason`). We never start a session that we'll hit AC-3 fallback on for *every* request. | `TestDetect_TableDriven[operator nvenc not present]`. |
| Re-detection after host config change | `SIGHUP` triggers `Detect` again; `Detected` is updated atomically. Existing sessions keep their HwAccel until they close (we do not restart in-flight). | `TestDetect_AtomicSwap` (small unit test on the Pointer). |
| `Detect` itself errors (ffmpeg binary missing entirely) | Returns nil + error; main.go logs FATAL and exits — the binary cannot serve transcode requests without an ffmpeg. The init-time failure is intentional: better to crash loudly than silently serve direct/remux only. | `TestDetect_FFmpegMissingErrors`. |
| 1-second test encode hangs | Wrapped in 5-second context; if it exceeds, treat as failed and fall back. | `testEncodeVideoToolbox` ctx timeout. |
| Operator sets `prefer="auto"` on Linux with both nvenc and qsv | NVENC wins (our priority order in `autoDetect`). Documented. | Decision-table test `linux both nvidia and qsv`. |

## 6. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `nvidia-smi` (binary, optional) | any | Probe NVENC presence on Linux. |
| `vainfo` (binary, optional) | any | Probe QSV/VA-API. |

(All Go-side deps already exist from earlier stories.)

## 7. Acceptance checklist

**Detection (story ACs)**
- [ ] AC-1: macOS → `h264_videotoolbox`; Linux + NVIDIA → `h264_nvenc`; Linux + Intel iGPU → `h264_qsv`; everything else → `libx264 -preset veryfast`. All paths log the chosen encoder + reason.
- [ ] AC-2: `force_software=true` per session bypasses detection — no `videotoolbox` / `nvenc` / `qsv` strings in the spawned FFmpeg argv. (Cross-checked in Story 8.5 integration test.)
- [ ] AC-3: Session whose hwaccel encoder errors before segment 1 is restarted once with software; if that fails too, session is closed 502 and matrix verdict for the source is recorded as transcode-failed.

**Selection table coverage**
- [ ] All 6 platform fixtures × 2 operator-pin variants pass.
- [ ] NVENC consumer GPU returns `SessionConcurrencyCap=3`; data-center GPU returns 0.

**Robustness**
- [ ] `parseEncoders` returns an error on empty stdout (we don't silently degrade).
- [ ] 1-second test-encode is wrapped in a 5-second context and returns its stderr on failure.
- [ ] SIGHUP / `streaming_settings_changed` triggers a re-detection without dropping in-flight sessions.

**Observability**
- [ ] `hwaccel_detection_failed_total{stage}` counter (stages: `enc_list`, `nvidia_smi`, `vainfo`, `test_encode`).
- [ ] `hwaccel_session_capacity_exceeded_total{accel}` counter.
- [ ] `hwaccel_session_failed_fallback_total{accel}` counter.
- [ ] Boot log line: `hwaccel_detected encoder=h264_nvenc reason="..." ffmpeg_version=6.1`.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[hwaccel] prefer = "auto"|"videotoolbox"|"nvenc"|"qsv"|"software"`.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.7.
