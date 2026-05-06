# Story 25.32 — Raspberry Pi & ARM builds

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

A meaningful slice of self-hosters run on a Pi 4 / Pi 5, NVIDIA Jetson,
or ARM-based mini-PC. This story is the "make the Pi experience not
suck" mandate. It overlaps 25.29 (Linux packages) and 25.30 (Docker)
but adds ARM-specific tunings.

Targets:

- **Raspberry Pi 4** (4 GB and 8 GB) — armv8 64-bit OS only.
- **Raspberry Pi 5** — same family, faster.
- **NVIDIA Jetson** (Nano EOL; Orin supported) — for users who
  want CUDA Whisper on a low-power box.
- **Generic ARM64 mini-PCs** (Rockchip RK3588 single-board
  computers, Apple Silicon Linux VMs).

Constraints we accommodate:

- **Memory.** 4 GB Pi forces small-config: SQLite by default,
  Whisper `tiny` or `base` model, transcoding limited to
  remux + audio (no full re-encode unless explicit).
- **Disk I/O.** SD-card-only Pi has terrible random IOPS;
  recommend USB-SSD; the installer warns when it detects
  rootfs on SD.
- **Whisper.** CPU-only on Pi (no GPU); we ship
  `faster-whisper` with CTranslate2 INT8 quantization. Pi 4
  manages `tiny` at ~2× realtime; `base` at ~1×; `small` is
  a stretch.
- **No HW-accelerated FFmpeg on Pi.** We use software
  remuxing only on Pi defaults (transcoding kicks in
  on-demand and may be slow).
- **64-bit only.** No armhf builds.

Distribution channels:

- The Linux packages (25.29) cover Pi via the same `.deb` /
  AppImage, just for `arm64`.
- A **first-run profile** detects "I'm on a Pi" via
  `/proc/cpuinfo` Hardware string and applies a memory-conservative
  defaults bundle. Profile name `pi-default`.

Specifically for Jetson:

- A separate Docker tag `:jetson-orin` ships PyTorch with
  CUDA on Jetson, plus FFmpeg with NVENC. Users opt in by
  pulling the tag explicitly.

Imaging:

- A Pi-imager-friendly `Maktaba OS` image is **out of v1**;
  we publish a setup script that runs on a fresh Raspberry Pi
  OS install:
  ```
  curl -sSL https://get.maktaba.app/pi.sh | sudo bash
  ```
  installs deps, mounts an external SSD if present, and runs
  the .deb installer. Documented; not a custom OS image yet.

## Acceptance criteria

- **Given** a fresh Raspberry Pi OS 64-bit on a Pi 4 (8 GB),
  **when** the user runs the setup script,
  **then** Maktaba is installed, configured with `pi-default`
  profile, and serving on port 8080 within 5 minutes.
- **Given** a Pi 4 with 4 GB RAM,
  **when** the user installs without changing defaults,
  **then** the Whisper model is `tiny`, the transcoding
  worker pool is 1, and OOM does not occur at idle.
- **Given** the rootfs is an SD card,
  **when** the installer detects it,
  **then** a warning is logged and shown in the web UI:
  "SD storage detected. For better performance, move the
  Maktaba data dir to a USB-SSD."
- **Given** a Jetson Orin Nano,
  **when** the user opts into the `jetson-orin` Docker
  tag,
  **then** Whisper runs on CUDA and transcribes ~5×
  realtime for `small.en`.
- **Given** the Pi reboots,
  **when** systemd starts services,
  **then** Maktaba is up within 90s of boot (ARM-class
  systems are slower than x86).
- **Given** a Pi user wants to scan a 2 TB external HDD,
  **when** the scan starts,
  **then** memory and CPU stay within configured caps and
  the scan completes (slow but correct).
- **Given** a Pi 5,
  **when** the user installs,
  **then** the default profile bumps Whisper model to
  `base` because Pi 5's neon and clock can handle it.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | smoke       | Pi 4 8 GB + Raspberry Pi OS Bookworm | install | success |
| T02 | smoke       | Pi 4 4 GB | install | success, `pi-default` profile applied |
| T03 | smoke       | Pi 5 | install | profile auto-detects |
| T04 | smoke       | Jetson Orin Nano | docker pull + run | CUDA detected, GPU used |
| T05 | unit        | profile detector reads `/proc/cpuinfo` | parse | matches Pi 4/5/Jetson |
| T06 | regression  | OOM under indexing on 4 GB Pi | 1k-video scan | no OOM, throttles instead |
| T07 | integration | external SSD mount detection | run | recommends data dir move |
| T08 | smoke       | RK3588 board (e.g., Orange Pi 5) | install | works (we test the most common) |
| T09 | regression  | reboot resilience | reboot mid-transcribe | resumes from last segment per Epic 03 |
| T10 | manual      | Pi 4 over Wi-Fi | scan | warns "Wi-Fi NAS access may be slow" |

## Edge cases

- **32-bit Pi OS.** Some users still run armhf; we
  refuse to install with a clear message: "Maktaba
  requires 64-bit Raspberry Pi OS (Bookworm or newer)".
- **Pi swap thrashing.** With 4 GB and swap-on-SD,
  Whisper can OOM-kill the system. Profile defaults
  cap workers to 1 and explicitly disable swap-heavy
  models.
- **Power.** Underpowered USB-C → CPU throttle → slow
  transcribe. We surface throttle events from
  `vcgencmd` in the diagnostics panel.
- **Read-only rootfs.** Some Pi users run with overlayfs;
  Maktaba's data dir must be writable. We document.
- **HW video decode.** Pi 5 has H.264 decode; FFmpeg
  build supports it via `h264_v4l2m2m`. Out for v1
  defaults; users can opt-in.
- **Temperature.** Sustained transcoding warms a Pi; we
  expose a "thermal throttling" status in the UI.
- **OS lifecycle.** RPi OS Bullseye is the oldest target;
  Bookworm + later supported.
- **Storage path conventions.** `/mnt/usbssd` is our
  recommended SSD path; `/var/lib/maktaba` defaults to
  there if detected.
- **Ubuntu Server for ARM.** Same arm64 .deb works.
- **Jetson power modes.** Documented; user must set
  `nvpmodel` to MAX-N for transcribe throughput.

## Files / packages

- `packaging/pi/get-maktaba.sh` (setup script published
  at https://get.maktaba.app/pi.sh).
- `packaging/profiles/pi-default.toml`,
  `packaging/profiles/pi5-default.toml`,
  `packaging/profiles/jetson-orin.toml`.
- `release/.github/workflows/release-arm64.yml`.

## Open questions

- **Custom Maktaba OS image.** Defer; users mostly want
  to keep their existing OS.
- **Pi camera integration.** Not Maktaba's domain.
