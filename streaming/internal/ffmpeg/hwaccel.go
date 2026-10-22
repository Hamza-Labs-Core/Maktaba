package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
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
// only that one; any other value is software, with an error saying so.
// Detection runs at boot and again later while it found software although
// this ffmpeg lists a GPU encoder (WatchChoice); a session asking for
// software (force_software) gets it in OpenSession.
type Detector struct {
	Bin    Binary
	Prefer string

	// Hooks let tests substitute the runtime probes without faking exec.
	Encoders func(ctx context.Context, ffmpeg string) ([]string, error)
	// Works test-encodes with encoder; busy: it failed as a device that is there
	// but has no session to give does (another program holds them).
	Works func(ctx context.Context, ffmpeg, encoder string) (ok, busy bool)
	GOOS  string // tests pin to "linux" / "darwin" / "windows"
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
	hw, _, err := d.detect(ctx)
	return hw, err
}

// retry says whether a later detection may find more.
type retry int

const (
	retryNo retry = iota
	// retryMaybe: a listed GPU encoder failed its test encode without a sign of
	// its device (Windows' builds list h264_nvenc on any PC; a driver that loads
	// after the server does fails so too): a few tries more, then none.
	retryMaybe
	// retryBusy: its device is there but had no session to give.
	retryBusy
)

// detect is Detect, and whether a later try may find more (software although
// this ffmpeg lists a GPU encoder the preference allows).
func (d *Detector) detect(ctx context.Context) (hw HWAccel, again retry, err error) {
	prefer := strings.ToLower(strings.TrimSpace(d.Prefer))
	switch prefer {
	case "software", "off", "none", "libx264":
		return HWAccelSoftware, retryNo, nil
	case "", "auto":
		prefer = ""
	case string(HWAccelNVENC), string(HWAccelQSV), string(HWAccelVideoToolbox):
	default:
		return HWAccelSoftware, retryNo, fmt.Errorf("MAKTABA_HWACCEL_PREFER=%q is none of auto, software, nvenc, qsv, videotoolbox: software", d.Prefer)
	}
	encs, err := d.Encoders(ctx, d.Bin.FFmpeg)
	if err != nil {
		// FFmpeg unavailable → fall back to software so tests on a
		// host without ffmpeg still get a meaningful answer.
		return HWAccelSoftware, retryNo, err
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
		if !have(enc) {
			continue
		}
		ok, busy := d.Works(ctx, d.Bin.FFmpeg, enc)
		if ok {
			return hw, retryNo, nil
		}
		if busy {
			again = retryBusy
		} else if again == retryNo {
			again = retryMaybe
		}
	}
	return HWAccelSoftware, again, nil
}

// maybeTries: how many more detections a GPU encoder that failed without a
// sign of its device gets (at interval, 2×, 4×: within the first hour);
// busyBackoffMax: the longest wait between the tries of a busy one.
const (
	maybeTries     = 3
	busyBackoffMax = time.Hour
)

// Choice is the transcodes' encoder verdict, kept current (WatchChoice).
type Choice struct{ v atomic.Value }

// Get is the verdict now ("" software).
func (c *Choice) Get() HWAccel {
	if c == nil {
		return HWAccelSoftware
	}
	hw, _ := c.v.Load().(HWAccel)
	return hw
}

// WatchChoice detects at once (within timeout), then again while the verdict
// is software although this ffmpeg lists a GPU encoder: after interval, each
// wait twice the last. A busy device (another program held its sessions) is
// tried until it works, at least once an hour; one that failed without a sign
// of its device (Windows' builds list h264_nvenc on any PC, a driver may load
// after the server) gets maybeTries tries more. report hears every verdict
// that changes, and the first.
func WatchChoice(ctx context.Context, d *Detector, timeout, interval time.Duration, report func(HWAccel, error)) *Choice {
	c := &Choice{}
	try := func() retry {
		tctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		hw, again, err := d.detect(tctx)
		if old, seen := c.v.Load().(HWAccel); !seen || old != hw || err != nil {
			report(hw, err)
		}
		c.v.Store(hw)
		return again
	}
	again := try()
	if again == retryNo {
		return c
	}
	go func() {
		wait, maybe := interval, 0
		for {
			if again == retryMaybe {
				if maybe++; maybe > maybeTries {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if again = try(); again == retryNo {
				return
			}
			if wait *= 2; wait > busyBackoffMax {
				wait = busyBackoffMax
			}
		}
	}()
	return c
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
func encodes(ctx context.Context, ffmpeg, encoder string) (ok, busy bool) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=256x144:r=25:d=0.2", "-c:v", encoder}
	if p := presetForEncoder(encoder); p != "" {
		args = append(args, "-preset", p)
	}
	out, err := exec.CommandContext(ctx, ffmpeg, append(args, "-f", "null", "-")...).CombinedOutput()
	if err == nil {
		return true, false
	}
	return false, refusedSession(string(out))
}
