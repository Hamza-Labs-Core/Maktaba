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
	HWAccelVAAPI        HWAccel = "vaapi"
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
//   - Linux → VA-API (h264_vaapi on a /dev/dri render node: a NAS's Intel or
//     AMD chip; story 59.05), after QuickSync, or before it on a NAS or in a
//     container (PreferVAAPI: there QuickSync needs libmfx, which covers no
//     newer Intel GPU, and VA-API uses less memory)
//   - else → software libx264
//
// Prefer (MAKTABA_HWACCEL_PREFER) narrows it: "auto" (default) tries them
// all, "software" (or "off", "none") none, "nvenc", "qsv", "vaapi" or
// "videotoolbox" only that one; any other value is software, with an error
// saying so.
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

	// VAAPI finds the render node VA-API works on (nil: VA-API is not tried);
	// PreferVAAPI tries it before QuickSync (a NAS, a container).
	VAAPI       *VAAPIProbe
	PreferVAAPI bool
	// Now is the clock (tests).
	Now func() time.Time
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
		Now:      time.Now,
	}
}

// Detect runs the probe and returns the chosen hwaccel.
func (d *Detector) Detect(ctx context.Context) (HWAccel, error) {
	st, _, err := d.detect(ctx)
	return HWAccel(strings.TrimPrefix(st.Encoder, "software")), err
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

// detect is Detect with the verdict's details (VideoStatus), and whether a
// later try may find more (software although this ffmpeg lists a GPU encoder
// the preference allows).
func (d *Detector) detect(ctx context.Context) (st VideoStatus, again retry, err error) {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	defer func() {
		st.CheckedAt = now().UTC()
		if st.Encoder == "" {
			st.Encoder = "software"
		}
		st.SoftwareOnly = st.Encoder == "software"
	}()
	prefer := strings.ToLower(strings.TrimSpace(d.Prefer))
	switch prefer {
	case "software", "off", "none", "libx264":
		return st, retryNo, nil
	case "", "auto":
		prefer = ""
	case string(HWAccelNVENC), string(HWAccelQSV), string(HWAccelVAAPI), string(HWAccelVideoToolbox):
	default:
		return st, retryNo, fmt.Errorf("MAKTABA_HWACCEL_PREFER=%q is none of auto, software, nvenc, qsv, vaapi, videotoolbox: software", d.Prefer)
	}
	encs, err := d.Encoders(ctx, d.Bin.FFmpeg)
	if err != nil {
		// FFmpeg unavailable → fall back to software so tests on a
		// host without ffmpeg still get a meaningful answer.
		return st, retryNo, err
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
	case "linux":
		candidates = []HWAccel{HWAccelNVENC, HWAccelQSV, HWAccelVAAPI}
		if d.PreferVAAPI {
			candidates = []HWAccel{HWAccelNVENC, HWAccelVAAPI, HWAccelQSV}
		}
	case "windows":
		candidates = []HWAccel{HWAccelNVENC, HWAccelQSV}
	}
	for _, hw := range candidates {
		if prefer != "" && prefer != string(hw) {
			continue
		}
		enc := encoderForHWAccel(string(hw))
		if hw == HWAccelVAAPI {
			if d.VAAPI == nil {
				continue
			}
			if !have(enc) {
				// this ffmpeg has no VA-API: said only when it was asked for
				if prefer != "" {
					st.Reason, st.Error = ReasonDeviceFailed, "this ffmpeg has no h264_vaapi encoder"
				}
				continue
			}
			// a render node is there or not: nothing a later try changes (the
			// container's devices are set when it starts)
			if _, vs := d.VAAPI.Find(ctx, d.Bin.FFmpeg); vs.Encoder != "" {
				return vs, retryNo, nil
			} else if st.Reason == "" {
				st = vs
			}
			continue
		}
		if !have(enc) {
			continue
		}
		ok, busy := d.Works(ctx, d.Bin.FFmpeg, enc)
		if ok {
			return VideoStatus{Encoder: string(hw), Codecs: []string{"H.264"}}, retryNo, nil
		}
		if busy {
			again = retryBusy
		} else if again == retryNo {
			again = retryMaybe
		}
	}
	return st, again, nil
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
	return HWAccel(strings.TrimPrefix(c.Status().Encoder, "software"))
}

// Status is the verdict now with its details (Encoder "software" before the first).
func (c *Choice) Status() VideoStatus {
	if c == nil {
		return VideoStatus{Encoder: "software", SoftwareOnly: true}
	}
	st, ok := c.v.Load().(VideoStatus)
	if !ok {
		return VideoStatus{Encoder: "software", SoftwareOnly: true}
	}
	return st
}

// VAAPIDevice is the render node the VA-API verdict runs on (nil: not VA-API).
func (c *Choice) VAAPIDevice() *VAAPIDevice {
	return c.Status().VAAPI
}

// NewChoiceForTest is a Choice holding st.
func NewChoiceForTest(st VideoStatus) *Choice {
	c := &Choice{}
	c.v.Store(st)
	return c
}

// WatchChoice detects at once (within timeout), then again while the verdict
// is software although this ffmpeg lists a GPU encoder: after interval, each
// wait twice the last. A busy device (another program held its sessions) is
// tried until it works, at least once an hour; one that failed without a sign
// of its device (Windows' builds list h264_nvenc on any PC, a driver may load
// after the server) gets maybeTries tries more. report hears every verdict
// that changes, and the first.
func WatchChoice(ctx context.Context, d *Detector, timeout, interval time.Duration, report func(VideoStatus, error)) *Choice {
	c := &Choice{}
	try := func() retry {
		tctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		st, again, err := d.detect(tctx)
		if old, seen := c.v.Load().(VideoStatus); !seen || old.Encoder != st.Encoder || old.Reason != st.Reason || err != nil {
			report(st, err)
		}
		c.v.Store(st)
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
