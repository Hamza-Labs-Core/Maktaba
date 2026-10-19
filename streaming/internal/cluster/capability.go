package cluster

import (
	"bufio"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// Capabilities is what a streaming host can do, detected once at start (R1.3).
type Capabilities struct {
	// Encoders this host can use: videotoolbox, nvenc (each only when a test encode
	// answers), software always.
	Encoders []string
	// CanPush: ffmpeg can push over rtmps (TLS) and srt (libsrt).
	CanPush map[string]bool
	// CanLive: yt-dlp is present (live blocks).
	CanLive bool
}

// DetectCapabilities test-encodes with the hardware encoders this OS may have, reads
// `ffmpeg -protocols` and looks for yt-dlp.
func DetectCapabilities(ctx context.Context, goos, ffmpeg, ytdlp string, probe channel.Probe) Capabilities {
	if probe == nil {
		probe = channel.FFmpegProbe{Bin: ffmpeg}
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	var encs []string
	if goos == "darwin" {
		if probe.Encodes(ctx, "h264_videotoolbox", "-allow_sw", "0", "-realtime", "1") {
			encs = append(encs, channel.EncoderVideoToolbox)
		}
	} else if probe.Encodes(ctx, "h264_nvenc") {
		encs = append(encs, channel.EncoderNVENC)
	}
	encs = append(encs, channel.EncoderSoftware)
	return Capabilities{Encoders: encs, CanPush: PushProtocols(ctx, ffmpeg), CanLive: HasYtDlp(ytdlp)}
}

// PushProtocols runs `ffmpeg -protocols` and parses it.
func PushProtocols(ctx context.Context, ffmpeg string) map[string]bool {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-protocols").Output()
	if err != nil {
		return map[string]bool{"rtmps": false, "srt": false}
	}
	return ParseProtocols(string(out))
}

// ParseProtocols reads the output protocols of `ffmpeg -protocols`: rtmps (needs TLS)
// and srt (needs libsrt).
func ParseProtocols(out string) map[string]bool {
	res := map[string]bool{"rtmps": false, "srt": false}
	output := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch strings.ToLower(strings.TrimSuffix(line, ":")) {
		case "output":
			output = true
			continue
		case "input":
			output = false
			continue
		}
		if !output {
			continue
		}
		if _, ok := res[line]; ok {
			res[line] = true
		}
	}
	return res
}

// HasYtDlp reports whether the yt-dlp binary resolves (MAKTABA_YTDLP or PATH).
func HasYtDlp(bin string) bool {
	if bin == "" {
		bin = "yt-dlp"
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// Capacity is how many channels a host may carry at once (design §2.3): the admin's
// override, else the output settings' EffectiveMaxActive for the encoder, capped on
// NVENC hosts at half the NVENC session cap (a channel briefly holds two sessions at
// a block boundary).
func Capacity(out channel.OutputSettings, encoder string, override *int) int {
	if override != nil {
		if *override < 0 {
			return 0
		}
		return *override
	}
	c := out.EffectiveMaxActive(encoder)
	if encoder == channel.EncoderNVENC && out.NVENCMax > 0 {
		if half := out.NVENCMax / 2; half < c {
			c = half
		}
		if c < 1 {
			c = 1
		}
	}
	return c
}
