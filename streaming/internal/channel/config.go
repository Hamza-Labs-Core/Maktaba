package channel

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Environment knobs for the channel output (all optional):
//
//	MAKTABA_CHANNEL_RESOLUTION  "1920x1080" (WxH)
//	MAKTABA_CHANNEL_FPS         30
//	MAKTABA_CHANNEL_VIDEO_KBPS  6000
//	MAKTABA_CHANNEL_AUDIO_CODEC aac | ac3
//	MAKTABA_CHANNEL_AUDIO_KBPS  192
//	MAKTABA_CHANNEL_ENCODER     auto | nvenc | software
//	MAKTABA_CHANNEL_SEGMENT_SEC 4
//	MAKTABA_CHANNEL_FONT        font file for slate captions
//	MAKTABA_YTDLP               yt-dlp binary for live blocks
//	MAKTABA_STREAM_PATH_MAP     "from=>to;from2=>to2" library path rewrites

// ProfileFromEnv reads the output profile (encoder left as configured;
// resolve "auto" with DetectEncoder).
func ProfileFromEnv() (OutputProfile, string) {
	p := DefaultProfile()
	if v := os.Getenv("MAKTABA_CHANNEL_RESOLUTION"); v != "" {
		if w, h, ok := strings.Cut(strings.ToLower(v), "x"); ok {
			if wi, err := strconv.Atoi(strings.TrimSpace(w)); err == nil && wi >= 160 {
				if hi, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && hi >= 90 {
					p.Width, p.Height = wi, hi
				}
			}
		}
	}
	p.FPS = envInt("MAKTABA_CHANNEL_FPS", p.FPS)
	p.VideoKbps = envInt("MAKTABA_CHANNEL_VIDEO_KBPS", p.VideoKbps)
	p.AudioKbps = envInt("MAKTABA_CHANNEL_AUDIO_KBPS", p.AudioKbps)
	p.SegmentSeconds = envInt("MAKTABA_CHANNEL_SEGMENT_SEC", p.SegmentSeconds)
	if strings.EqualFold(os.Getenv("MAKTABA_CHANNEL_AUDIO_CODEC"), "ac3") {
		p.AudioCodec = "ac3"
	}
	enc := strings.ToLower(strings.TrimSpace(os.Getenv("MAKTABA_CHANNEL_ENCODER")))
	if enc == "" {
		enc = "auto"
	}
	return p.normalised(), enc
}

// DetectEncoder resolves the encoder preference. "auto" test-encodes a
// few frames with h264_nvenc (proves the driver + a free session), so a
// box without a working NVIDIA GPU gets libx264.
func DetectEncoder(ctx context.Context, ffmpegBin, pref string) string {
	switch pref {
	case EncoderNVENC:
		return EncoderNVENC
	case EncoderSoftware, "libx264", "cpu":
		return EncoderSoftware
	}
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegBin, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=640x360:r=30:d=0.5",
		"-c:v", "h264_nvenc", "-f", "null", "-")
	if cmd.Run() == nil {
		return EncoderNVENC
	}
	return EncoderSoftware
}

// FindFont returns a font file for slate captions: MAKTABA_CHANNEL_FONT
// or a common system font, "" if none.
func FindFont() string {
	cands := []string{os.Getenv("MAKTABA_CHANNEL_FONT"),
		`C:/Windows/Fonts/arial.ttf`, `C:/Windows/Fonts/segoeui.ttf`,
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		"/Library/Fonts/Arial.ttf"}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// StationStarter adapts StartStation to the engine's StartFunc.
func StationStarter(cfg StationConfig) StartFunc {
	return func(_ context.Context, channelID uuid.UUID, dir string) (Pipeline, error) {
		return StartStation(cfg, channelID, dir)
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return def
}
