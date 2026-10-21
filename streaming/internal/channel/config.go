package channel

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
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
//	MAKTABA_CHANNEL_ENCODER     auto | nvenc | videotoolbox | software
//	MAKTABA_CHANNEL_MAX_ACTIVE  channels on air at once (unset: 16 with
//	                            VideoToolbox, else 8)
//	MAKTABA_CHANNEL_NVENC_MAX   6 (NVENC sessions at once)
//	MAKTABA_CHANNEL_VT_MAX      0 (VideoToolbox sessions at once; 0 = no cap)
//	MAKTABA_CHANNEL_WARM_GRACE_SEC 60
//	MAKTABA_CHANNEL_HORIZON_DAYS   7 (guide days ahead)
//
// Each is only the fallback: the Maktaba TV output settings (rules
// tv_output, settings.go) override them when changed in the settings UI.
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

// EncoderChoice is a resolved encoder plus what the host's ffmpeg
// supports for it.
type EncoderChoice struct {
	Name string // nvenc | videotoolbox | software
	// VTSoftware: VideoToolbox without a hardware encoder (-allow_sw 1);
	// false when the hardware encoder answered (-allow_sw 0).
	VTSoftware bool
	// VTPrioSpeed: this ffmpeg's h264_videotoolbox has -prio_speed.
	VTPrioSpeed bool
}

// Probe runs ffmpeg checks for encoder detection (a fake in tests).
type Probe interface {
	// Encodes reports whether half a second of black encodes with codec.
	Encodes(ctx context.Context, codec string, extra ...string) bool
	// HasOption reports whether the encoder lists the private option.
	HasOption(ctx context.Context, codec, option string) bool
}

// FFmpegProbe is the real Probe.
type FFmpegProbe struct{ Bin string }

func (p FFmpegProbe) bin() string {
	if p.Bin == "" {
		return "ffmpeg"
	}
	return p.Bin
}

func (p FFmpegProbe) Encodes(ctx context.Context, codec string, extra ...string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=640x360:r=30:d=0.5", "-c:v", codec}
	args = append(args, extra...)
	args = append(args, "-f", "null", "-")
	return exec.CommandContext(ctx, p.bin(), args...).Run() == nil
}

func (p FFmpegProbe) HasOption(ctx context.Context, codec, option string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.bin(), "-hide_banner", "-h", "encoder="+codec).Output()
	return err == nil && strings.Contains(string(out), "-"+option+" ")
}

// DetectEncoder resolves the encoder preference to its name on this OS
// (see DetectEncoderFor).
func DetectEncoder(ctx context.Context, ffmpegBin, pref string) string {
	return DetectEncoderFor(ctx, runtime.GOOS, pref, FFmpegProbe{Bin: ffmpegBin}).Name
}

// DetectEncoderFor resolves the encoder preference on OS goos. "auto"
// test-encodes a few frames: on macOS ("darwin") with the VideoToolbox
// hardware encoder (no software fallback, so a Mac without one gets
// libx264), elsewhere with h264_nvenc (proves the driver + a free
// session); a box with neither gets libx264. An explicit nvenc or
// software is taken as is; an explicit videotoolbox still checks whether
// the hardware encoder answers (else -allow_sw 1).
func DetectEncoderFor(ctx context.Context, goos, pref string, probe Probe) EncoderChoice {
	pref = strings.ToLower(strings.TrimSpace(pref))
	vt := func(explicit bool) (EncoderChoice, bool) {
		if probe.Encodes(ctx, "h264_videotoolbox", "-allow_sw", "0", "-realtime", "1") {
			return EncoderChoice{Name: EncoderVideoToolbox,
				VTPrioSpeed: probe.HasOption(ctx, "h264_videotoolbox", "prio_speed")}, true
		}
		if explicit {
			return EncoderChoice{Name: EncoderVideoToolbox, VTSoftware: true,
				VTPrioSpeed: probe.HasOption(ctx, "h264_videotoolbox", "prio_speed")}, true
		}
		return EncoderChoice{}, false
	}
	switch pref {
	case EncoderNVENC:
		return EncoderChoice{Name: EncoderNVENC}
	case EncoderVideoToolbox, "vt", "h264_videotoolbox":
		c, _ := vt(true)
		return c
	case EncoderSoftware, "libx264", "cpu":
		return EncoderChoice{Name: EncoderSoftware}
	}
	if goos == "darwin" {
		if c, ok := vt(false); ok {
			return c
		}
		return EncoderChoice{Name: EncoderSoftware}
	}
	if probe.Encodes(ctx, "h264_nvenc") {
		return EncoderChoice{Name: EncoderNVENC}
	}
	return EncoderChoice{Name: EncoderSoftware}
}

// EncoderResolver caches the detection per preference: a settings change
// to "auto" test-encodes once, not at every channel start.
type EncoderResolver struct {
	FFmpeg string
	// GOOS is the OS detected for ("": runtime.GOOS).
	GOOS string
	// Probe runs the checks (nil: FFmpegProbe over FFmpeg).
	Probe Probe

	mu    sync.Mutex
	cache map[string]EncoderChoice
}

// Resolve returns the encoder for pref ("auto", "nvenc", ...).
func (r *EncoderResolver) Resolve(ctx context.Context, pref string) EncoderChoice {
	pref = strings.ToLower(strings.TrimSpace(pref))
	if pref == "" {
		pref = "auto"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[pref]; ok {
		return c
	}
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	probe := r.Probe
	if probe == nil {
		probe = FFmpegProbe{Bin: r.FFmpeg}
	}
	c := DetectEncoderFor(ctx, goos, pref, probe)
	if r.cache == nil {
		r.cache = map[string]EncoderChoice{}
	}
	r.cache[pref] = c
	return c
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
	return func(ctx context.Context, channelID uuid.UUID, dir string) (Pipeline, error) {
		c := cfg
		if o, ok := StartOptionsFrom(ctx); ok {
			c.Discontinuity = o.Discontinuity
		}
		return StartStation(c, channelID, dir)
	}
}

// SettingsStarter adapts StartStation to the engine's StartFunc, reading
// the output settings at every station start: a settings change applies
// to each channel the next time it starts. The NVENC gate's cap follows
// the settings too.
func SettingsStarter(base StationConfig, settings Settings, enc *EncoderResolver) StartFunc {
	return func(ctx context.Context, channelID uuid.UUID, dir string) (Pipeline, error) {
		cfg := base
		out := settings.Output(ctx)
		cfg.Profile = out.Profile(enc.Resolve(ctx, out.Encoder))
		cfg.LiveLookupReuse = settings.TV(ctx).LiveLookupReuse
		if cfg.Gate == nil {
			cfg.Gate = defaultGate
		}
		cfg.Gate.SetMax(out.NVENCMax)
		if cfg.VTGate == nil {
			cfg.VTGate = defaultVTGate
		}
		cfg.VTGate.SetMax(out.VTMax)
		if cfg.PathMapSource != nil {
			cfg.PathMap = cfg.PathMapSource()
		}
		if o, ok := StartOptionsFrom(ctx); ok {
			cfg.Discontinuity = o.Discontinuity
		}
		if ss, ok := settings.(StripSource); ok && cfg.StripDefaults == nil {
			cfg.StripDefaults = ss.Strip
		}
		st, err := StartStation(cfg, channelID, dir)
		if err != nil {
			return nil, err
		}
		return st, nil
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
