package ffmpeg

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
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
// follow Story 8.7, each candidate kept only when ffmpeg lists its
// encoder and a short test encode with it works (the device is there, and
// its driver and this ffmpeg build agree; a listed encoder proves neither):
//
//   - macOS → VideoToolbox (h264_videotoolbox)
//   - Linux and Windows → NVENC (h264_nvenc, an NVIDIA GPU), else QuickSync
//     (h264_qsv, an Intel GPU)
//   - else → software libx264
//
// Prefer (MAKTABA_HWACCEL_PREFER) narrows it: "auto" (default) tries them
// all, "software" (or "off", "none") none, "nvenc", "qsv" or "videotoolbox"
// only that one. Detection is one-shot at boot; per-session overrides
// (force_software) happen later in the FFmpeg arg builder.
type Detector struct {
	Bin    Binary
	Prefer string

	// Hooks let tests substitute the runtime probes without faking exec.
	Encoders func(ctx context.Context, ffmpeg string) ([]string, error)
	Works    func(ctx context.Context, ffmpeg, encoder string) bool
	GOOS     string // tests pin to "linux" / "darwin" / "windows"
}

// Default returns a Detector with default probes wired, running bin.
func Default(bin Binary) *Detector {
	if bin.FFmpeg == "" {
		bin = DefaultBinary()
	}
	return &Detector{
		Bin:      bin,
		Encoders: listEncoders,
		Works:    encodes,
		GOOS:     runtime.GOOS,
	}
}

// Detect runs the probe and returns the chosen hwaccel.
func (d *Detector) Detect(ctx context.Context) (HWAccel, error) {
	prefer := strings.ToLower(strings.TrimSpace(d.Prefer))
	switch prefer {
	case "software", "off", "none", "libx264":
		return HWAccelSoftware, nil
	case "", "auto":
		prefer = ""
	}
	encs, err := d.Encoders(ctx, d.Bin.FFmpeg)
	if err != nil {
		// FFmpeg unavailable → fall back to software so tests on a
		// host without ffmpeg still get a meaningful answer.
		return HWAccelSoftware, err
	}
	have := func(name string) bool {
		for _, e := range encs {
			if e == name {
				return true
			}
		}
		return false
	}

	var candidates []HWAccel
	switch d.GOOS {
	case "darwin":
		candidates = []HWAccel{HWAccelVideoToolbox}
	case "linux", "windows":
		candidates = []HWAccel{HWAccelNVENC, HWAccelQSV}
	}
	for _, hw := range candidates {
		if prefer != "" && prefer != string(hw) {
			continue
		}
		enc := encoderForHWAccel(string(hw))
		if have(enc) && d.Works(ctx, d.Bin.FFmpeg, enc) {
			return hw, nil
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

// encodes reports whether ffmpeg encodes a fraction of a second of black
// with encoder, with the preset the transcodes give it: a GPU encoder works
// only where its device and driver are (Windows' builds list h264_nvenc on
// any PC).
func encodes(ctx context.Context, ffmpeg, encoder string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=256x144:r=25:d=0.2", "-c:v", encoder}
	if p := presetForEncoder(encoder); p != "" {
		args = append(args, "-preset", p)
	}
	return exec.CommandContext(ctx, ffmpeg, append(args, "-f", "null", "-")...).Run() == nil
}
