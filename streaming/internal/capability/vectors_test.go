package capability

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// vectorSource is one source of shared/playback/caps-vectors.json.
type vectorSource struct {
	Container     string  `json:"container"`
	VideoCodec    string  `json:"video_codec"`
	AudioCodec    string  `json:"audio_codec"`
	HDR           string  `json:"hdr"`
	DVProfile     int     `json:"dv_profile"`
	DVCompat      int     `json:"dv_compat"`
	Height        int     `json:"height"`
	BitrateKbps   int     `json:"bitrate_kbps"`
	AudioChannels int     `json:"audio_channels"`
	BitDepth      int     `json:"bit_depth"`
	FPS           float64 `json:"fps"`
}

// vectorVerdict is what the home decides for a source with a vector's report.
type vectorVerdict struct {
	Mode     Mode   `json:"mode"`
	Segments string `json:"segments,omitempty"`
	Format   string `json:"format,omitempty"`
	Audio    string `json:"audio,omitempty"`
	StripDV  bool   `json:"strip_dv,omitempty"`
	Stereo   bool   `json:"stereo_aac,omitempty"`
}

type capsVectors struct {
	About   string                  `json:"about"`
	Schema  int                     `json:"schema"`
	Sources map[string]vectorSource `json:"sources"`
	Vectors []struct {
		ID         string                   `json:"id"`
		Profile    string                   `json:"profile"`
		Model      string                   `json:"model"`
		Answers    json.RawMessage          `json:"answers"`
		DeviceCaps json.RawMessage          `json:"device_caps"`
		Verdicts   map[string]vectorVerdict `json:"verdicts"`
	} `json:"vectors"`
}

func vectorsPath(t *testing.T) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "playback", "caps-vectors.json")
}

func loadVectors(t *testing.T) capsVectors {
	t.Helper()
	raw, err := os.ReadFile(vectorsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var v capsVectors
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("caps-vectors.json: %v", err)
	}
	return v
}

func (s vectorSource) source() Source {
	return Source{Container: s.Container, VideoCodec: s.VideoCodec, AudioCodec: s.AudioCodec, HDR: s.HDR,
		DVProfile: s.DVProfile, DVCompat: s.DVCompat, Height: s.Height, BitrateKbps: s.BitrateKbps,
		AudioChannels: s.AudioChannels, BitDepth: s.BitDepth, FPS: s.FPS}
}

// T07, the home's half: each vector's report is a DeviceCaps with no key this home
// does not read, its profile a TV one, and the home decides for each source what
// the vector says. Each app's reader is tested against the same file.
func TestCapsVectors(t *testing.T) {
	v := loadVectors(t)
	if v.Schema != 1 || len(v.Vectors) == 0 {
		t.Fatalf("schema %d, %d vectors", v.Schema, len(v.Vectors))
	}
	r := NewRegistry()
	seen := map[string]bool{}
	for _, vec := range v.Vectors {
		if seen[vec.ID] {
			t.Errorf("vector %s twice", vec.ID)
		}
		seen[vec.ID] = true
		p, known := r.Get(vec.Profile)
		if !known || !p.Detailed {
			t.Errorf("%s: profile %q is no TV profile", vec.ID, vec.Profile)
			continue
		}
		var caps DeviceCaps
		dec := json.NewDecoder(bytes.NewReader(vec.DeviceCaps))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&caps); err != nil {
			t.Errorf("%s: device_caps: %v", vec.ID, err)
			continue
		}
		if len(vec.Answers) < 3 {
			t.Errorf("%s: no recorded answers", vec.ID)
		}
		if len(vec.Verdicts) != len(v.Sources) {
			t.Errorf("%s: %d verdicts for %d sources", vec.ID, len(vec.Verdicts), len(v.Sources))
		}
		for name, src := range v.Sources {
			want, ok := vec.Verdicts[name]
			got := r.DecideFor(p, &caps, src.source(), Override{})
			gv := vectorVerdict{Mode: got.Mode, Segments: got.Segments, Format: got.Format, Audio: got.AudioCodec, StripDV: got.StripDV, Stereo: got.StereoAAC}
			if os.Getenv("CAPS_VECTORS_PRINT") != "" {
				b, _ := json.Marshal(gv)
				t.Logf("%s × %s: %s (%s)", vec.ID, name, b, got.Reason)
			}
			if !ok || got.Mode != want.Mode || gv != want {
				t.Errorf("%s × %s: got %+v, want %+v (%s)", vec.ID, name, gv, want, got.Reason)
			}
		}
	}
}

// An empty list is the device's answer ("none"), not a field left out: the
// fingerprint and the merge tell them apart.
func TestDeviceCaps_EmptyListIsAnAnswer(t *testing.T) {
	none, err := ParseDeviceCaps([]byte(`{"passthrough":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	left, _ := ParseDeviceCaps([]byte(`{}`))
	if none.Key() == left.Key() {
		t.Fatal("[] and a field left out share a fingerprint")
	}
	p, _ := NewRegistry().Get("firetv")
	eff, _ := Apply(p, none)
	if eff.Passthrough == nil || len(eff.Passthrough) != 0 {
		t.Fatalf("passthrough %v, want none", eff.Passthrough)
	}
}
