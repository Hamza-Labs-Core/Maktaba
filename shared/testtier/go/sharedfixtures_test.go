package testtier

// Story 20.2 (HLB-389): the shared/fixtures/probe-goldens/*.json edge
// cases are a CONTRACT, not inert files. This test gives them teeth —
// it fails if a golden is missing, malformed, or no longer encodes the
// invariant its filename promises. Without this, a fixture could rot
// (or be silently deleted) and no gate would notice.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// probeGolden mirrors the documented fields of the probe-goldens JSON.
// Unknown keys (e.g. "_fixture") are ignored by encoding/json.
type probeGolden struct {
	ContentHash   string  `json:"content_hash"`
	Path          string  `json:"path"`
	Container     string  `json:"container"`
	VideoCodec    string  `json:"video_codec"`
	AudioCodec    string  `json:"audio_codec"`
	Height        int     `json:"height"`
	Width         int     `json:"width"`
	DurationSec   float64 `json:"duration_sec"`
	AudioChannels int     `json:"audio_channels"`
	Probed        bool    `json:"probed"`
}

// sharedFixturesDir walks up from this test file to the repo root and
// returns <root>/shared/fixtures. Fails the test if not found so a
// missing fixtures tree can never be a silent pass.
func sharedFixturesDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 16; i++ {
		cand := filepath.Join(dir, "shared", "fixtures")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	t.Fatal("shared/fixtures not found by walking up from test file")
	return ""
}

func loadGolden(t testing.TB, dir, name string) probeGolden {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "probe-goldens", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	var g probeGolden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("golden %s is not valid JSON: %v", name, err)
	}
	return g
}

func TestProbeGoldensExistAndAreValidJSON(t *testing.T) {
	dir := sharedFixturesDir(t)
	for _, name := range []string{
		"no-audio.probe.json",
		"corrupt-moov.probe.json",
		"rtl-filename.probe.json",
	} {
		g := loadGolden(t, dir, name)
		if g.ContentHash == "" {
			t.Fatalf("%s: content_hash must be set", name)
		}
	}
}

func TestNoAudioGoldenEncodesInvariant(t *testing.T) {
	g := loadGolden(t, sharedFixturesDir(t), "no-audio.probe.json")
	if g.AudioCodec != "" || g.AudioChannels != 0 {
		t.Fatalf("EC1 no-audio: want empty audio_codec and 0 channels, got %q / %d",
			g.AudioCodec, g.AudioChannels)
	}
	if !g.Probed {
		t.Fatal("EC1 no-audio: a probable video with no audio is still probed=true")
	}
}

func TestCorruptMoovGoldenEncodesInvariant(t *testing.T) {
	g := loadGolden(t, sharedFixturesDir(t), "corrupt-moov.probe.json")
	if g.Probed {
		t.Fatal("EC2 corrupt-moov: an unreadable moov atom must yield probed=false")
	}
	if g.DurationSec != 0 {
		t.Fatalf("EC2 corrupt-moov: duration must be 0, got %v", g.DurationSec)
	}
}

func TestRTLFilenameGoldenPreservesNonASCIIPath(t *testing.T) {
	g := loadGolden(t, sharedFixturesDir(t), "rtl-filename.probe.json")
	if g.Path == "" {
		t.Fatal("EC3 rtl-filename: path must be set")
	}
	ascii := true
	for _, r := range g.Path {
		if r > 0x7f {
			ascii = false
			break
		}
	}
	if ascii {
		t.Fatalf("EC3 rtl-filename: path %q has no non-ASCII bytes — RTL invariant lost", g.Path)
	}
	if !strings.HasSuffix(g.Path, ".mp4") {
		t.Fatalf("EC3 rtl-filename: path %q should keep its extension", g.Path)
	}
}
