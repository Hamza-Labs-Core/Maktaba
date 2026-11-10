package supervisor

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// The streaming role's video environment (Epic 59 story 59.05).
const (
	EnvTranscodeSoftwareMax       = "MAKTABA_TRANSCODE_SOFTWARE_MAX"
	EnvTranscodeSoftwareMaxHeight = "MAKTABA_TRANSCODE_SOFTWARE_MAX_HEIGHT"
	EnvTranscodeHardwareMax       = "MAKTABA_TRANSCODE_HARDWARE_MAX"
	EnvVAAPIFirst                 = "MAKTABA_HWACCEL_VAAPI_FIRST"
	EnvRenderDevice               = "MAKTABA_RENDER_DEVICE"
	EnvVideoStatusFile            = "MAKTABA_VIDEO_STATUS_FILE"
)

// VideoStatusPath is where the streaming role writes its video verdict (the encoder, the
// render node, why it is in software) for Settings → This machine.
func VideoStatusPath(cfg config.Config) string {
	return filepath.Join(cfg.StateDir(), "streaming", "video.json")
}

// platform is this machine's platform (hostinfo's; tests replace it).
var platform = func() string { return hostinfo.Detect().Platform }

// VideoEnv is the streaming role's video settings: the hardware tier's transcode limits (one
// at 720p at most in software on Light, two at 1080p on Standard; more on a working GPU
// encoder; none on Strong or without a tier), the admin's render node ([streaming]
// render_device), VA-API before QuickSync on a NAS or in a container, and where it writes its
// verdict. An operator's own variables are kept.
func VideoEnv(cfg config.Config) []string {
	var out []string
	add := func(k, v string) {
		if v != "" && os.Getenv(k) == "" {
			out = append(out, k+"="+v)
		}
	}
	num := func(n int) string {
		if n <= 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	if tier, ok := hostinfo.ParseTier(cfg.Server.Tier); ok {
		d := hostinfo.DefaultsFor(tier, hostinfo.Machine{})
		add(EnvTranscodeSoftwareMax, num(d.SoftwareTranscodes))
		add(EnvTranscodeSoftwareMaxHeight, num(d.SoftwareMaxHeight))
		add(EnvTranscodeHardwareMax, num(d.HardwareTranscodes))
	}
	add(EnvRenderDevice, cfg.Streaming.RenderDevice)
	if p := platform(); hostinfo.IsNAS(p) || p == hostinfo.PlatformDocker {
		add(EnvVAAPIFirst, "1")
	}
	add(EnvVideoStatusFile, VideoStatusPath(cfg))
	return out
}
