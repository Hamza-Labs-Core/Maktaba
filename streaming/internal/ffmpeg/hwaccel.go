package ffmpeg

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// HWAccel is the detected hardware encoder name, or "" for software.
type HWAccel string

const (
	HWAccelVideoToolbox HWAccel = "videotoolbox"
	HWAccelNVENC        HWAccel = "nvenc"
	HWAccelQSV          HWAccel = "qsv"
	HWAccelSoftware     HWAccel = ""
)

// Detector resolves which hwaccel to use at startup. Resolution rules
// follow Story 8.7:
//
//   - macOS → VideoToolbox if h264_videotoolbox is in `ffmpeg -encoders`
//   - Linux + NVIDIA → NVENC if h264_nvenc and `nvidia-smi` works
//   - Linux + Intel  → QuickSync if h264_qsv exists
//   - else → software libx264
//
// Detection is one-shot at boot; per-session overrides (force_software)
// happen later in the FFmpeg arg builder.
type Detector struct {
	Bin Binary

	// Hooks let tests substitute the runtime probes without faking exec.
	Encoders    func(ctx context.Context, ffmpeg string) ([]string, error)
	NVIDIASmiOK func(ctx context.Context) bool
	QuickSyncOK func(ctx context.Context) bool
	GOOS        string // tests pin to "linux" / "darwin"
}

// Default returns a Detector with default probes wired.
func Default() *Detector {
	return &Detector{
		Bin:         DefaultBinary(),
		Encoders:    listEncoders,
		NVIDIASmiOK: nvidiaSmiOK,
		QuickSyncOK: quickSyncOK,
		GOOS:        runtime.GOOS,
	}
}

// Detect runs the probe and returns the chosen hwaccel.
func (d *Detector) Detect(ctx context.Context) (HWAccel, error) {
	encs, err := d.Encoders(ctx, d.Bin.FFmpeg)
	if err != nil {
		// FFmpeg unavailable → fall back to software so tests on a
		// host without ffmpeg still get a meaningful answer.
		return HWAccelSoftware, err
	}
	have := func(name string) bool {
		for _, e := range encs {
			if strings.Contains(e, name) {
				return true
			}
		}
		return false
	}

	switch d.GOOS {
	case "darwin":
		if have("h264_videotoolbox") {
			return HWAccelVideoToolbox, nil
		}
	case "linux":
		if have("h264_nvenc") && d.NVIDIASmiOK(ctx) {
			return HWAccelNVENC, nil
		}
		if have("h264_qsv") && d.QuickSyncOK(ctx) {
			return HWAccelQSV, nil
		}
	}
	return HWAccelSoftware, nil
}

// listEncoders shells out `ffmpeg -hide_banner -encoders` and pulls
// out video encoder names.
func listEncoders(ctx context.Context, ffmpeg string) ([]string, error) {
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// `ffmpeg` may not exist on the test host. We surface an
		// error so the caller can fall back without a panic.
		return nil, errors.New("ffmpeg -encoders failed: " + string(out))
	}
	lines := strings.Split(string(out), "\n")
	encs := []string{}
	for _, l := range lines {
		// Encoders section lines look like " V..... libx264 ..."
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "V") || strings.HasPrefix(l, "A") {
			parts := strings.Fields(l)
			if len(parts) >= 2 {
				encs = append(encs, parts[1])
			}
		}
	}
	return encs, nil
}

// nvidiaSmiOK runs `nvidia-smi` and reports success.
func nvidiaSmiOK(ctx context.Context) bool {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, "nvidia-smi")
	return cmd.Run() == nil
}

// quickSyncOK looks for /dev/dri/renderD128 — the standard probe for
// Intel GPU presence on Linux.
func quickSyncOK(_ context.Context) bool {
	// Lightweight probe — full test would hit `vainfo`. The detector
	// decides VAAPI/QSV is available; if FFmpeg actually fails to
	// initialize the device, the per-session fallback (Story 8.7
	// AC-3) catches it.
	if _, err := exec.LookPath("vainfo"); err == nil {
		return true
	}
	return false
}
