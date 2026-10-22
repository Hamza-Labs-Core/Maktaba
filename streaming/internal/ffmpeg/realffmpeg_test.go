package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/mediatest"
)

// The transcodes' arguments against the real ffmpeg (the media e2e tier):
// each GPU verdict's -hwaccel is one this ffmpeg has and its preset one its
// encoder takes, whether or not this machine has the GPU; then a clip
// transcodes to the HLS ladder in software and with the encoder the detector
// picks here (NVENC on a PC with an NVIDIA GPU).
func TestTranscodeArgs_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	mediatest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-hwaccels").CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg -hwaccels: %v %s", err, out)
	}
	hwaccels := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) == 1 {
			hwaccels[f[0]] = true
		}
	}
	encs, err := listEncoders(ctx, "ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, e := range encs {
		listed[e] = true
	}
	for _, hw := range []HWAccel{HWAccelNVENC, HWAccelQSV, HWAccelVideoToolbox} {
		enc := encoderForHWAccel(string(hw))
		if !listed[enc] {
			t.Logf("%s: this ffmpeg has no %s", hw, enc)
			continue
		}
		if dec := decodeHWAccel(string(hw)); dec != "" && !hwaccels[dec] {
			t.Errorf("%s: -hwaccel %s, which this ffmpeg does not have (it has %v)", hw, dec, hwaccels)
		}
		if p := presetForEncoder(enc); p != "" {
			help, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-h", "encoder="+enc).CombinedOutput()
			if err != nil {
				t.Fatalf("ffmpeg -h encoder=%s: %v", enc, err)
			}
			// the preset is one of -preset's named values
			if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(p) + `\s`).Match(help) {
				t.Errorf("%s: preset %q is not one %s takes:\n%s", hw, p, enc, help)
			}
		}
	}

	clip := filepath.Join(t.TempDir(), "in.mp4")
	if out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=1280x720:r=25:d=3", "-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", clip).CombinedOutput(); err != nil {
		t.Fatalf("clip: %v %s", err, out)
	}
	picked, err := Default(DefaultBinary()).Detect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the detector picks %q here", picked)
	runs := []HWAccel{HWAccelSoftware}
	if picked != HWAccelSoftware {
		runs = append(runs, picked)
	}
	for _, hw := range runs {
		dir := t.TempDir()
		args := HLSArgs(clip, dir, "master.m3u8", DefaultLadder(0), string(hw))
		if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("%q: ffmpeg %s: %v\n%s", hw, strings.Join(args, " "), err, out)
		}
		for _, f := range []string{"master.m3u8", filepath.Join("v0", "index.m3u8"), filepath.Join("v0", "seg-0.ts")} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("%q: no %s: %v", hw, f, err)
			}
		}
	}
}
