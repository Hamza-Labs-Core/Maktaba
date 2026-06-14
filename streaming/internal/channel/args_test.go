package channel

import (
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
)

func ladder() []ffmpeg.Rendition {
	return []ffmpeg.Rendition{
		{Name: "v0", Height: 720, Width: 1280, BitrateKbps: 3000},
		{Name: "v1", Height: 480, Width: 854, BitrateKbps: 1200},
	}
}

func joinArgs(a ffmpeg.Args) string { return strings.Join(a, " ") }

func TestLiveHLSArgs_ConcatInputAndLiveFlags(t *testing.T) {
	a := LiveHLSArgs("/tmp/c.ffconcat", "/tmp/out", "master.m3u8", ladder(), "")
	s := joinArgs(a)
	if !strings.Contains(s, "-f concat -safe 0") {
		t.Error("missing concat demuxer flags")
	}
	if !strings.Contains(s, "-i /tmp/c.ffconcat") {
		t.Error("missing concat input")
	}
	// Live window: must rotate + omit endlist (continuous stream).
	if !strings.Contains(s, "delete_segments") || !strings.Contains(s, "omit_endlist") {
		t.Errorf("missing live HLS flags:\n%s", s)
	}
	if !strings.Contains(s, "-master_pl_name master.m3u8") {
		t.Error("missing master playlist name")
	}
}

func TestLiveHLSArgs_HWAccelInjected(t *testing.T) {
	a := LiveHLSArgs("/tmp/c", "/tmp/out", "m.m3u8", ladder(), "videotoolbox")
	s := joinArgs(a)
	if !strings.Contains(s, "-hwaccel videotoolbox") {
		t.Error("hwaccel not injected")
	}
	if !strings.Contains(s, "h264_videotoolbox") {
		t.Error("hw encoder not selected")
	}
}

func TestMPEGTSArgs_PipesToStdout(t *testing.T) {
	a := MPEGTSArgs("/tmp/c.ffconcat", ladder(), "")
	s := joinArgs(a)
	if !strings.Contains(s, "-f mpegts") {
		t.Error("missing mpegts mux")
	}
	if !strings.HasSuffix(s, "pipe:1") {
		t.Errorf("mpegts must write to stdout pipe:1, got tail: %q", s)
	}
	if !strings.Contains(s, "-f concat -safe 0") {
		t.Error("mpegts should reuse the concat input")
	}
}

func TestEncoderFor(t *testing.T) {
	cases := map[string]string{
		"":             "libx264",
		"videotoolbox": "h264_videotoolbox",
		"nvenc":        "h264_nvenc",
		"vaapi":        "h264_vaapi",
		"unknown":      "libx264",
	}
	for in, want := range cases {
		if got := encoderFor(in); got != want {
			t.Errorf("encoderFor(%q) = %q want %q", in, got, want)
		}
	}
}
