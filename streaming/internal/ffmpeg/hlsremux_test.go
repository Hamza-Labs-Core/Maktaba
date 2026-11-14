package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func joined(a Args) string { return strings.Join(a, " ") }

// T03: an HEVC remux for Cast goes in fMP4: the video copied and tagged hvc1,
// init.mp4 and seg-N.m4s, never TS.
func TestHLSRemuxArgs_CastHEVCInFMP4(t *testing.T) {
	a := joined(HLSRemuxArgs("/m/movie.mkv", "/c/s1", RemuxPlan{
		Segments: SegmentsFMP4, VideoCodec: "hevc", Audio: true, SourceAudio: "eac3",
	}))
	for _, want := range []string{
		"-map 0:v:0 -c:v copy", "-tag:v hvc1", "-c:a:0 copy",
		"-hls_segment_type fmp4", "-hls_fmp4_init_filename init.mp4",
		"-hls_segment_filename /c/s1/v%v/seg-%d.m4s", "-var_stream_map v:0,a:0", "/c/s1/v%v/index.m3u8",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("args lack %q:\n%s", want, a)
		}
	}
	for _, never := range []string{".ts", "libx264", "-master_pl_name", "-bsf:v"} {
		if strings.Contains(a, never) {
			t.Errorf("args carry %q:\n%s", never, a)
		}
	}
}

// An H.264 remux stays in TS (no existing client sees fMP4), untagged.
func TestHLSRemuxArgs_H264InTS(t *testing.T) {
	a := joined(HLSRemuxArgs("/m/a.mkv", "/c/s", RemuxPlan{Segments: SegmentsTS, VideoCodec: "h264"}))
	if !strings.Contains(a, "seg-%d.ts") || strings.Contains(a, "fmp4") || strings.Contains(a, "hvc1") ||
		strings.Contains(a, "0:a:0") || !strings.Contains(a, "-var_stream_map v:0 ") {
		t.Fatalf("args:\n%s", a)
	}
}

// Dolby Vision dropped, TrueHD as EAC3 5.1, a stereo AAC track beside it (Roku):
// the audio as two renditions of one group.
func TestHLSRemuxArgs_DVStripAudioAndStereo(t *testing.T) {
	a := joined(HLSRemuxArgs("/m/a.mkv", "/c/s", RemuxPlan{
		Segments: SegmentsFMP4, VideoCodec: "hevc", Audio: true, AudioCodec: "eac3", SourceAudio: "truehd",
		StripDV: DVStripFilter(false), StereoAAC: true,
	}))
	for _, want := range []string{
		"-bsf:v filter_units=remove_types=62|63", "-c:a:0 eac3 -b:a:0 640k -ac:a:0 6",
		"-map 0:a:0 -c:a:1 aac -b:a:1 192k -ac:a:1 2",
		"-var_stream_map v:0,agroup:aud a:0,agroup:aud a:1,agroup:aud",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("args lack %q:\n%s", want, a)
		}
	}
	if DVStripFilter(true) != "filter_units=remove_types=62|63,dovi_rpu=strip=1" {
		t.Fatal("ffmpeg 7.1's dovi_rpu not used")
	}
}

func TestDASHRemuxArgs(t *testing.T) {
	a := joined(DASHRemuxArgs("/m/a.mkv", "/c/s", RemuxPlan{VideoCodec: "av1", Audio: true, AudioCodec: "aac", StereoAAC: true}))
	for _, want := range []string{"-c:v copy", "-c:a:0 aac", "-f dash", "-adaptation_sets id=0,streams=v id=1,streams=a", "/c/s/manifest.mpd"} {
		if !strings.Contains(a, want) {
			t.Errorf("args lack %q:\n%s", want, a)
		}
	}
	if strings.Contains(a, "-c:a:1") {
		t.Error("a DASH remux carries a second audio track")
	}
}

func TestBuildRemuxMaster(t *testing.T) {
	m := string(BuildRemuxMaster(RemuxPlan{
		Segments: SegmentsFMP4, VideoCodec: "hevc", TenBit: true, Audio: true, SourceAudio: "eac3",
		Width: 3840, Height: 2160, BitrateKbps: 40000, VideoRange: "PQ",
	}))
	for _, want := range []string{"#EXT-X-VERSION:7", "BANDWIDTH=40000000", "RESOLUTION=3840x2160",
		`CODECS="hvc1.2.4.L153.B0,ec-3"`, "VIDEO-RANGE=PQ", "\nv0/index.m3u8\n"} {
		if !strings.Contains(m, want) {
			t.Errorf("master lacks %q:\n%s", want, m)
		}
	}
	m = string(BuildRemuxMaster(RemuxPlan{Segments: SegmentsTS, VideoCodec: "h264", Audio: true, AudioCodec: "eac3",
		SourceAudio: "truehd", StereoAAC: true}))
	for _, want := range []string{"#EXT-X-VERSION:6", `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud"`, `URI="v1/index.m3u8"`,
		`URI="v2/index.m3u8"`, `CODECS="avc1.640033,ec-3,mp4a.40.2"`, `AUDIO="aud"`} {
		if !strings.Contains(m, want) {
			t.Errorf("master lacks %q:\n%s", want, m)
		}
	}
}

// A transcode for a device that plays fMP4 alone writes fMP4; every other one TS.
func TestHLSArgs_Segments(t *testing.T) {
	p := SoftwarePlan(1, true)
	if a := joined(HLSArgs("/in", "/out", "master.m3u8", DefaultLadder(0)[:1], p)); !strings.Contains(a, "seg-%d.ts") || strings.Contains(a, "fmp4") {
		t.Fatalf("default:\n%s", a)
	}
	p.Segments = SegmentsFMP4
	a := joined(HLSArgs("/in", "/out", "master.m3u8", DefaultLadder(0)[:1], p))
	if !strings.Contains(a, "-hls_segment_type fmp4") || !strings.Contains(a, "seg-%d.m4s") {
		t.Fatalf("fmp4:\n%s", a)
	}
}

// The orchestrator writes a remux's own master up front; the runner picks the
// Dolby Vision filter its ffmpeg has and runs no encoder.
func TestOrchestrator_RemuxJob(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("a shell script stands in for ffmpeg")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$*\" >> '"+log+"'\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dovi bool
		err  error
		want string
	}{{true, nil, "filter_units=remove_types=62|63,dovi_rpu=strip=1"}, {false, nil, "filter_units=remove_types=62|63"}, {false, errors.New("no"), "filter_units"}} {
		_ = os.Remove(log)
		r := &execRunner{Bin: Binary{FFmpeg: script}, bsfs: func(context.Context, string) (map[string]bool, error) {
			return map[string]bool{"dovi_rpu": tc.dovi}, tc.err
		}}
		o := &Orchestrator{Runner: r}
		out := filepath.Join(dir, uuid.NewString())
		h, err := o.Start(context.Background(), Job{SessionID: uuid.NewString(), InputPath: "/m/a.mkv", OutputDir: out,
			Format: "hls", Remux: &RemuxPlan{Segments: SegmentsFMP4, VideoCodec: "hevc", DropDV: true, VideoRange: "PQ"}})
		if err != nil {
			t.Fatal(err)
		}
		if !waitUntil(10*time.Second, func() bool { b, _ := os.ReadFile(log); return len(b) > 0 }) {
			t.Fatal("ffmpeg not run")
		}
		b, _ := os.ReadFile(log)
		_ = h.Stop(context.Background())
		if !strings.Contains(string(b), tc.want) || strings.Contains(string(b), "libx264") {
			t.Errorf("dovi %v err %v: %s", tc.dovi, tc.err, b)
		}
		m, _ := os.ReadFile(filepath.Join(out, "master.m3u8"))
		if !strings.Contains(string(m), "VIDEO-RANGE=PQ") {
			t.Errorf("master %s", m)
		}
	}
}
