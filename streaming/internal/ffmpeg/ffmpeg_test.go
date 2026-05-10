package ffmpeg

import (
	"context"
	"strings"
	"testing"
)

func TestRemuxArgs_CopyOnly(t *testing.T) {
	args := RemuxArgs("/in.mkv", "/out.mp4")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c copy") {
		t.Fatalf("missing copy: %s", joined)
	}
	if !strings.Contains(joined, "-movflags +faststart+frag_keyframe+empty_moov") {
		t.Fatalf("missing movflags: %s", joined)
	}
	if !strings.Contains(joined, "/in.mkv") || !strings.Contains(joined, "/out.mp4") {
		t.Fatalf("missing paths: %s", joined)
	}
}

func TestHLSArgs_HasLadderRungs(t *testing.T) {
	ladder := DefaultLadder(0)
	args := HLSArgs("/in.mp4", "/out", "master.m3u8", ladder, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f hls") {
		t.Fatalf("missing -f hls")
	}
	for i := range ladder {
		if !strings.Contains(joined, "-c:v:"+itoa(i)) {
			t.Fatalf("missing video output for rung %d", i)
		}
	}
}

func TestHLSArgs_HWAccelEncoder(t *testing.T) {
	args := HLSArgs("/in.mp4", "/out", "master.m3u8", DefaultLadder(0), "videotoolbox")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "h264_videotoolbox") {
		t.Fatalf("expected h264_videotoolbox encoder: %s", joined)
	}
}

func TestDASHArgs_OutputsMPD(t *testing.T) {
	args := DASHArgs("/in.mp4", "/out", DefaultLadder(0), "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f dash") {
		t.Fatalf("missing -f dash")
	}
	if !strings.Contains(joined, "manifest.mpd") {
		t.Fatalf("missing mpd output: %s", joined)
	}
}

func TestDefaultLadder_MaxBitrateTrim(t *testing.T) {
	l := DefaultLadder(2000)
	if len(l) != 1 {
		t.Fatalf("got %d rungs", len(l))
	}
	if l[0].BitrateKbps != 1200 {
		t.Fatalf("kept %dkbps, expected 1200", l[0].BitrateKbps)
	}
}

func TestDefaultLadder_TooLowKeepsSmallest(t *testing.T) {
	l := DefaultLadder(200)
	if len(l) != 1 {
		t.Fatalf("got %d rungs, want 1 (forced lowest)", len(l))
	}
	if l[0].Name != "v2" {
		t.Fatalf("rung=%s", l[0].Name)
	}
}

func TestDetector_DarwinPrefersVideoToolbox(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "darwin",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return []string{"libx264", "h264_videotoolbox"}, nil
		},
		NVIDIASmiOK: func(_ context.Context) bool { return false },
		QuickSyncOK: func(_ context.Context) bool { return false },
	}
	got, err := d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != HWAccelVideoToolbox {
		t.Fatalf("got %s want videotoolbox", got)
	}
}

func TestDetector_LinuxNVIDIAPrefersNVENC(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "linux",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return []string{"libx264", "h264_nvenc", "h264_qsv"}, nil
		},
		NVIDIASmiOK: func(_ context.Context) bool { return true },
		QuickSyncOK: func(_ context.Context) bool { return true },
	}
	got, _ := d.Detect(context.Background())
	if got != HWAccelNVENC {
		t.Fatalf("got %s want nvenc", got)
	}
}

func TestDetector_LinuxNoGPUFallsBackToSoftware(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "linux",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return []string{"libx264"}, nil
		},
		NVIDIASmiOK: func(_ context.Context) bool { return false },
		QuickSyncOK: func(_ context.Context) bool { return false },
	}
	got, _ := d.Detect(context.Background())
	if got != HWAccelSoftware {
		t.Fatalf("got %s want software", got)
	}
}

func TestDetector_NoFFmpegFallsBackToSoftware(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "darwin",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return nil, errReadable
		},
	}
	got, _ := d.Detect(context.Background())
	if got != HWAccelSoftware {
		t.Fatalf("got %s want software fallback", got)
	}
}

var errReadable = readableError("ffmpeg not on PATH")

type readableError string

func (e readableError) Error() string { return string(e) }
