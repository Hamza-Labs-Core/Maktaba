package supervisor

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 59 story 59.05: the tier's transcode limits reach the streaming role (Light one in
// software at 720p, two on a GPU; Standard two at 1080p, four; Strong none), the admin's
// render node, VA-API first on a NAS or in a container, and the verdict's file.
func TestVideoEnv(t *testing.T) {
	defer func(p func() string) { platform = p }(platform)
	t.Setenv(EnvTranscodeSoftwareMax, "")
	cfg := config.Defaults()
	status := EnvVideoStatusFile + "=" + filepath.Join(cfg.StateDir(), "streaming", "video.json")
	platform = func() string { return "linux" }
	for tier, want := range map[string][]string{
		"light":    {EnvTranscodeSoftwareMax + "=1", EnvTranscodeSoftwareMaxHeight + "=720", EnvTranscodeHardwareMax + "=2", status},
		"standard": {EnvTranscodeSoftwareMax + "=2", EnvTranscodeSoftwareMaxHeight + "=1080", EnvTranscodeHardwareMax + "=4", status},
		"strong":   {status},
		"":         {status},
	} {
		c := cfg
		c.Server.Tier = tier
		if got := VideoEnv(c); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", tier, got, want)
		}
	}
	for p, first := range map[string]bool{"dsm": true, "truenas": true, "docker": true, "linux": false, "darwin": false} {
		platform = func() string { return p }
		if got := slices.Contains(VideoEnv(cfg), EnvVAAPIFirst+"=1"); got != first {
			t.Errorf("%s: VA-API first %v", p, got)
		}
	}
	c := cfg
	c.Streaming.RenderDevice = "/dev/dri/renderD129"
	if !slices.Contains(VideoEnv(c), EnvRenderDevice+"=/dev/dri/renderD129") {
		t.Errorf("render_device: %v", VideoEnv(c))
	}
	// an operator's own is kept
	t.Setenv(EnvTranscodeSoftwareMax, "3")
	c.Server.Tier = "light"
	if slices.Contains(VideoEnv(c), EnvTranscodeSoftwareMax+"=1") {
		t.Errorf("operator's own replaced: %v", VideoEnv(c))
	}
}
