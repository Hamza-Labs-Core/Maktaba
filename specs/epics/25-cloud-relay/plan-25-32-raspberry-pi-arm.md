# Implementation Plan — Story 25.32 Raspberry Pi & ARM builds

> Companion to [story-25-32-raspberry-pi-arm.md](story-25-32-raspberry-pi-arm.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Distribution | Same arm64 .deb / AppImage from 25.29 + a setup script. Jetson gets a dedicated `:jetson-orin` Docker tag with CUDA. |
| Profile system | `packaging/profiles/pi-default.toml`, `pi5-default.toml`, `jetson-orin.toml`, etc. Wizard (25.35) selects by hardware probe. |
| Constraints | Memory-conservative defaults on 4 GB; CPU Whisper; SD-card warnings. |
| Out of scope | 32-bit (armhf). Custom Maktaba OS image. Pi camera. |

## 1. Setup script

`packaging/pi/get-maktaba.sh` (served at https://get.maktaba.app/pi.sh):

```bash
#!/bin/sh
set -e
if [ "$(uname -m)" != "aarch64" ]; then
  echo "Maktaba requires 64-bit Raspberry Pi OS (Bookworm or newer)." >&2
  exit 1
fi
# Detect SD vs SSD rootfs
ROOTDEV=$(findmnt -n -o SOURCE / | sed 's/[0-9]*$//')
if echo "$ROOTDEV" | grep -q 'mmcblk'; then SD=1; else SD=0; fi
echo "SD root detected: $SD"

curl -fsSL https://apt.maktaba.app/maktaba.gpg | sudo tee /usr/share/keyrings/maktaba.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/maktaba.gpg] https://apt.maktaba.app stable main" | \
    sudo tee /etc/apt/sources.list.d/maktaba.list
sudo apt-get update
sudo apt-get install -y maktaba avahi-daemon

# If external SSD detected, suggest moving data dir
if [ -d /mnt/usbssd ]; then
  sudo sed -i 's#data_dir = .*#data_dir = "/mnt/usbssd/maktaba"#' /etc/maktaba/config.toml
  sudo install -d -o maktaba -g maktaba /mnt/usbssd/maktaba
fi

# Apply Pi profile
PROFILE=pi-default
if grep -q 'Raspberry Pi 5' /proc/cpuinfo; then PROFILE=pi5-default; fi
sudo /usr/bin/maktaba apply-profile "$PROFILE"
sudo systemctl restart maktaba

echo "Maktaba is running. Open http://$(hostname).local:8080"
```

## 2. Profile files

`packaging/profiles/pi-default.toml`:

```toml
[transcribe]
backend = "faster-whisper"
device = "cpu"
model = "tiny"
worker_count = 1
quantization = "int8"

[pipeline]
extract_workers = 1
ffmpeg_threads = 2

[storage]
cache_max_gb = 5
```

`pi5-default.toml`: like above but `model = "base"`, `worker_count = 2`.

`jetson-orin.toml`: `device = "cuda"`, `model = "small.en"`, `worker_count = 4`.

`apply-profile` is a small subcommand of `maktaba` that copy-merges TOML over `config.toml`.

## 3. Profile selection in wizard (25.35)

Wizard's hardware probe (`internal/setup/probe.go`):

```go
type Probe struct {
    OS, Arch  string
    Model     string             // from /proc/cpuinfo "Model"
    CPUCores  int
    RAMGB     int
    GPU       string             // "apple-ne" | "nvidia" | "intel-igpu" | "none"
    RootIsSD  bool
}

func Detect() Probe {
    p := Probe{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUCores: runtime.NumCPU()}
    raw, _ := os.ReadFile("/proc/cpuinfo")
    for _, l := range bytes.Split(raw, []byte("\n")) {
        if bytes.HasPrefix(l, []byte("Model")) || bytes.HasPrefix(l, []byte("Hardware")) {
            p.Model = strings.TrimSpace(string(l[bytes.Index(l, []byte(":"))+1:]))
        }
    }
    mem, _ := mem.VirtualMemory()
    p.RAMGB = int(mem.Total / 1<<30)
    p.GPU = detectGPU()
    p.RootIsSD = detectSDRoot()
    return p
}
```

`Wizard.ProfileFor(p Probe) string` picks `pi-default`, `pi5-default`, `jetson-orin`, `mac-mini`, `pc-desktop`, `nas-bay`, `vps-small`, `vps-large`.

## 4. SD-card warning surface

If `RootIsSD` is true, write a notification to the wizard panel: "SD storage detected — performance will be limited; consider USB-SSD for `/var/lib/maktaba`".

## 5. Thermal throttling

Pipeline runs `vcgencmd get_throttled` every minute on Pi; exposes a Prometheus gauge `pi_thermal_throttle`. UI surfaces in diagnostics.

## 6. Jetson tag

`Dockerfile.jetson` overlays CUDA wheels:

```dockerfile
FROM nvcr.io/nvidia/l4t-base:r35.4.1
COPY --from=runtime / /         # main image
COPY pipeline/jetson-requirements.txt /tmp/
RUN python3 -m pip install -r /tmp/jetson-requirements.txt
ENV MAKTABA_TRANSCRIBE_DEVICE=cuda MAKTABA_TRANSCRIBE_MODEL=small.en
```

Published as `ghcr.io/hamza-labs-core/maktaba:jetson-orin`.

## 7. Test plan

### 7.1 Smoke (manual or self-hosted runners)

| Test | Pins |
|---|---|
| Pi 4 8 GB Bookworm | setup script → service up → healthz 200. |
| Pi 4 4 GB | `pi-default` applied; OOM-free under 1k-video scan. |
| Pi 5 | `pi5-default`. |
| Jetson Orin Nano | `:jetson-orin` → CUDA detected. |
| RK3588 board (Orange Pi 5) | arm64 deb works. |
| Reboot mid-transcribe | Resume per Epic 03. |

### 7.2 Unit

| Test | Pins |
|---|---|
| `TestDetectPiFromCpuinfo` | Bookworm fixture → "Raspberry Pi 5". |
| `TestProfileSelector` | Pi 4 4GB → pi-default; Pi 5 → pi5; Mac M2 → mac-mini. |
| `TestApplyProfileMerge` | `apply-profile pi-default` updates whisper.model = "tiny". |
| `TestSDRootDetection` | mmcblk path → true. |
| `Test32BitInstallRefused` | armv7l → error. |

## 8. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| 32-bit Pi OS | Setup script aborts. | `Test32BitInstallRefused`. |
| Pi swap thrashing | Profile caps workers; large model warned. | Spec. |
| Underpowered USB-C | Surface throttle metric. | UI. |
| Read-only rootfs | Doc; require writable data dir. | Doc. |
| HW video decode | h264_v4l2m2m off by default; opt-in. | Spec. |
| OS lifecycle | Bookworm min. | Doc. |
| `/mnt/usbssd` | Setup script picks up; data dir moved. | Implementation. |
| Ubuntu Server for ARM | Same arm64 deb works. | Doc. |
| Jetson power mode | Documented MAX-N for throughput. | Doc. |

## 9. Dependencies

- 25.29 (arm64 deb / AppImage).
- 25.30 (Docker image; CUDA overlay used for Jetson).
- 25.35 (wizard reads profile from probe).

## 10. Acceptance checklist

- [ ] `get-maktaba.sh` installer publishes at https://get.maktaba.app/pi.sh.
- [ ] Profile files for Pi 4 / Pi 5 / Jetson.
- [ ] Probe detects Pi model from `/proc/cpuinfo`.
- [ ] Profile auto-applied on first run.
- [ ] SD-card detection surfaces warning.
- [ ] Jetson Docker tag published.
- [ ] Tests in §7 pass.
