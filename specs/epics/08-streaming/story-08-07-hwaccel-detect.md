# Story 8.7 — Hardware acceleration auto-detect

Per §4.4: VideoToolbox on Apple Silicon, NVENC on NVIDIA, QuickSync on
Intel, libx264 fallback. Detected once at startup; per-session overridable.

**AC-1 — Boot-time detection.**
- **Given** the binary starts on macOS,
- **When** `ffmpeg -encoders` is parsed,
- **Then** `h264_videotoolbox` is selected; logged at info; exposed via
  `streaming.HealthCheck` and `GET /api/stream/capabilities`.
- **Given** the binary starts on Linux with an NVIDIA GPU and
  `nvidia-smi` succeeds,
- **Then** `h264_nvenc` is selected.
- **Given** none of the above,
- **Then** `libx264 -preset veryfast` is selected.

**AC-2 — Per-session override.**
- **Given** `force_software=true` on session open,
- **When** FFmpeg is spawned,
- **Then** software libx264 is used regardless of detected hwaccel
  (useful for problematic source files that crash the hardware path).

**AC-3 — Hwaccel failure fallback.**
- **Given** a session where the hwaccel encoder errors out within the
  first segment,
- **When** the failure is detected (FFmpeg exits non-zero before segment
  1),
- **Then** the session is restarted once with software encoding; if that
  also fails the session is closed with `502 Bad Gateway` and the matrix
  verdict for the source is recorded as transcode-failed.

**Test cases:**
- Unit: encoder selection table for {macOS-arm64, macOS-x86, Linux+nvidia,
  Linux+intel, Linux+none, Windows+intel}.
- Integration: spawn fixture session with `force_software=true` → no
  `videotoolbox` arguments in the FFmpeg command line.
- Integration: simulate hwaccel failure (mock FFmpeg) → fallback path
  succeeds.

**Edge cases:**
- Hardware decoder limit reached (e.g. NVENC concurrent session cap on
  consumer GPUs is 3) — new sessions over the cap fall back to software
  even though detection said NVENC. Tracked via metric
  `hwaccel_session_capacity_exceeded_total`.
- Source file uses a feature the hardware encoder doesn't support (e.g.
  HEVC 10-bit input on a NVENC SKU that lacks it) — caught by the AC-3
  fallback.
