package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 13.19 T09: a transcode's audio follows EncodePlan.AudioCodec: E-AC-3 5.1 at 640k for a
// desktop Player with 5.1 speakers, stereo AAC at 192k for one with fewer (or no report), and
// the encoder's AAC at 128k for every other profile, as before.
func TestTranscodeAudio_ByTheDevicesChannels(t *testing.T) {
	cases := []struct {
		codec string
		want  string
	}{
		{"eac3", "-c:a eac3 -b:a 640k -ac 6"},
		{"aac", "-c:a aac -b:a 192k -ac 2"},
		{"", "-c:a aac -b:a 128k -f"},
	}
	for _, c := range cases {
		p := SoftwarePlan(3, true)
		p.AudioCodec = c.codec
		hls := joined(HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), p))
		if !strings.Contains(hls, c.want) {
			t.Errorf("HLS with %q: %s", c.codec, hls)
		}
		dash := joined(DASHArgs("/in.mkv", "/out", DefaultLadder(0), p))
		if !strings.Contains(dash, strings.TrimSuffix(c.want, " -f")) {
			t.Errorf("DASH with %q: %s", c.codec, dash)
		}
		if c.codec == "" && (strings.Contains(hls, "-ac ") || strings.Contains(dash, "-ac ")) {
			t.Errorf("the default transcode names no channel count: %s", hls)
		}
	}
	// a source without audio maps none, whatever the plan says
	p := SoftwarePlan(1, false)
	p.AudioCodec = "eac3"
	if a := joined(HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0)[:1], p)); strings.Contains(a, "-c:a") {
		t.Errorf("no audio: %s", a)
	}
}

// The master playlist written before the first segment names E-AC-3's codec for a 5.1
// transcode, AAC's otherwise.
func TestTranscodeAudio_MasterPlaylistCodecs(t *testing.T) {
	if !strings.Contains(string(buildMasterPlaylist(DefaultLadder(0), audioTag("eac3"))), `CODECS="avc1.64002a,ec-3"`) {
		t.Error("an E-AC-3 transcode's master playlist names ec-3")
	}
	for _, codec := range []string{"aac", ""} {
		if !strings.Contains(string(buildMasterPlaylist(DefaultLadder(0), audioTag(codec))), `CODECS="avc1.64002a,mp4a.40.2"`) {
			t.Errorf("%q: the master playlist names AAC", codec)
		}
	}
	dir := t.TempDir()
	o := &Orchestrator{Runner: &recordRunner{}}
	if _, err := o.Start(t.Context(), Job{SessionID: "s1", InputPath: "/in.mkv", OutputDir: dir, Ladder: DefaultLadder(0),
		Format: "hls", AudioCodec: "eac3"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "master.m3u8"))
	if err != nil || !strings.Contains(string(b), "ec-3") {
		t.Fatalf("the orchestrator's master playlist: %s (%v)", b, err)
	}
}

type recordRunner struct{ job Job }

func (r *recordRunner) Start(_ context.Context, job Job) (*Handle, error) {
	r.job = job
	return NewHandleForTest(func(context.Context) error { return nil }, func() int { return 1 }, func() bool { return true }), nil
}
