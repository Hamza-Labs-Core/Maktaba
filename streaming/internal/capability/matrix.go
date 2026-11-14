// Package capability implements the client profile registry and the
// can-direct-play / can-remux decision logic (Story 8.2). The matrix
// drives which serving mode a session opens in: direct, remux, or
// transcode.
package capability

import (
	"strings"
	"sync"
)

// Profile is the named client capability set. A request opens a
// session with a profile string ("ios-native", "browser-chrome",
// "tvos", etc.); we look it up here. Unknown profiles fall back to
// "generic" (HLS H.264 + AAC, max 720p) per AC-3.
type Profile struct {
	Name             string
	Containers       []string // e.g. "mp4", "mkv", "webm", "mov", "ts"
	VideoCodecs      []string // e.g. "h264", "h265", "vp9", "av1"
	AudioCodecs      []string // e.g. "aac", "ac3", "eac3", "mp3", "opus", "flac"
	HDR              []string // e.g. "sdr", "hdr10", "dolby-vision", "hlg"
	MaxHeight        int      // 1080, 720, 2160…
	MaxBitrateKbps   int      // 0 = unlimited
	MaxAudioChannels int      // 2, 6, 8…
	SupportsHLS      bool
	SupportsDASH     bool
	HardwareDecoded  bool

	// Epic 58 story 58.02: the TV profiles (firetv, tizen, webos, roku, cast,
	// vega) are Detailed: their verdicts weigh HDR and Dolby Vision, play audio
	// a device lacks as EAC3 or AAC beside the video copied, and pick the HLS
	// segment format (decideDetailed). The older profiles decide on codecs,
	// container, height and bitrate alone, as they always have.
	Detailed bool
	// Ceiling is the most a device's report (DeviceCaps) may widen the profile
	// to; the profile itself is the baseline, which plays on every model of the
	// platform. Nil: the profile is its own ceiling (a report only narrows it).
	Ceiling *Profile
	// Passthrough lists the audio the device sends on undecoded to a receiver.
	Passthrough []string
	// HLSSegments lists the HLS segment formats played ("ts", "fmp4"); empty: ts.
	HLSSegments []string
	// DASHOnlyVideo lists the video codecs the platform plays only inside DASH
	// (Roku's AV1).
	DASHOnlyVideo []string
	// StereoBeside: an HLS stream with a Dolby audio track carries a stereo AAC
	// track beside it (Roku's certification asks for one).
	StereoBeside bool
	// VideoHeights, VideoBitDepths and VideoFPS are a report's per-codec limits
	// (Apply; absent: MaxHeight, any).
	VideoHeights   map[string]int
	VideoBitDepths map[string]int
	VideoFPS       map[string]int
}

// Source is what a probe row looks like to the matrix. We keep it
// shallow so the probe package owns the wire shape.
type Source struct {
	Container             string
	VideoCodec            string
	AudioCodec            string
	HDR                   string
	Height                int
	BitrateKbps           int
	AudioChannels         int
	IsContainerOnlyChange bool // true if the codecs are direct-playable but the container needs a remux
	// DVProfile is the Dolby Vision profile (5, 7, 8; 0: none) and DVCompat its
	// base layer's signal compatibility id (8.1: 1 is HDR10, 8.4: 4 is HLG,
	// 8.2: 2 is SDR), from the probe's DOVI configuration record.
	DVProfile int
	DVCompat  int
	// BitDepth (8, 10; 0 unknown) and FPS (0 unknown) of the video.
	BitDepth int
	FPS      float64
}

// Mode is the verdict.
type Mode string

const (
	ModeDirect    Mode = "direct"
	ModeRemux     Mode = "remux"
	ModeTranscode Mode = "transcode"
)

// Verdict bundles the Mode with any per-session overrides applied.
type Verdict struct {
	Mode           Mode
	Reason         string
	BitrateCapKbps int
	HeightCap      int
	UsedFallback   bool

	// The rest is set by a Detailed profile's verdict (decideDetailed).
	//
	// Segments is the HLS segment format of a remux or transcode ("ts",
	// "fmp4"; "" for direct play and the older profiles: ts).
	Segments string
	// Format is "dash" when the device plays the video only by DASH (the
	// session goes by DASH whatever was asked); "" otherwise.
	Format string
	// AudioCodec is what a remux encodes the audio to ("eac3", "aac"); "": the
	// source's audio is copied.
	AudioCodec string
	// StripDV: a remux drops the Dolby Vision RPU (and a profile 7 enhancement
	// layer), sending the base layer (HDR10, HLG or SDR) alone.
	StripDV bool
	// StereoAAC: a stereo AAC track goes beside the Dolby one (Roku).
	StereoAAC bool
	// Clamped lists what the device's report asked beyond its platform's
	// ceiling (Apply); Reason names it too.
	Clamped []string
}

// Override is a per-session forcing flag set on OpenSession.
type Override struct {
	ForceTranscode bool
	ForceSoftware  bool
	MaxBitrateKbps int
	MaxHeight      int
	// Format is the session's asked-for format ("hls", "dash"; "" hls).
	Format string
}

// Registry holds the profile table.
type Registry struct {
	mu       sync.RWMutex
	profiles map[string]*Profile
}

// NewRegistry returns the default profile registry. Profile shapes
// follow the §4.2 client matrix in architecture.md.
func NewRegistry() *Registry {
	r := &Registry{profiles: map[string]*Profile{}}
	for _, p := range defaultProfiles() {
		r.profiles[p.Name] = p
	}
	return r
}

// Register adds or replaces a profile. Tests use this to inject
// custom matrices without rebuilding the binary.
func (r *Registry) Register(p *Profile) {
	r.mu.Lock()
	r.profiles[p.Name] = p
	r.mu.Unlock()
}

// Get returns the named profile, or the generic fallback when unknown.
// Second return is false when fallback was used (callers log).
func (r *Registry) Get(name string) (*Profile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.profiles[name]; ok {
		return p, true
	}
	return r.profiles["generic"], false
}

// Decide returns the serving mode for source under profile, after
// applying overrides. Order of precedence:
//
//  1. force_transcode → always transcode
//  2. exceeds matrix bitrate/height → transcode (with cap)
//  3. container-only mismatch → remux
//  4. all containers/codecs in profile allow-list → direct
//  5. otherwise → transcode
func (r *Registry) Decide(profile *Profile, src Source, ov Override) Verdict {
	if profile.Detailed {
		return decideDetailed(profile, src, ov)
	}
	if ov.ForceTranscode {
		v := Verdict{Mode: ModeTranscode, Reason: "force_transcode override"}
		v.BitrateCapKbps = effectiveBitrate(profile, ov)
		v.HeightCap = effectiveHeight(profile, ov)
		return v
	}

	if profile.MaxHeight > 0 && src.Height > profile.MaxHeight {
		return Verdict{
			Mode:           ModeTranscode,
			Reason:         "source height exceeds profile",
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov),
		}
	}
	if profile.MaxBitrateKbps > 0 && src.BitrateKbps > profile.MaxBitrateKbps {
		return Verdict{
			Mode:           ModeTranscode,
			Reason:         "source bitrate exceeds profile",
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov),
		}
	}
	if ov.MaxBitrateKbps > 0 && src.BitrateKbps > ov.MaxBitrateKbps {
		return Verdict{
			Mode:           ModeTranscode,
			Reason:         "source bitrate exceeds override",
			BitrateCapKbps: ov.MaxBitrateKbps,
			HeightCap:      effectiveHeight(profile, ov),
		}
	}

	codecsOK := contains(profile.VideoCodecs, src.VideoCodec) && contains(profile.AudioCodecs, src.AudioCodec)
	if !codecsOK {
		return Verdict{Mode: ModeTranscode, Reason: "codec not in profile",
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov)}
	}
	containerOK := containsContainer(profile.Containers, src.Container)
	if !containerOK {
		return Verdict{Mode: ModeRemux, Reason: "container not in profile, codecs OK"}
	}
	return Verdict{Mode: ModeDirect, Reason: "all in profile"}
}

func effectiveBitrate(p *Profile, ov Override) int {
	if ov.MaxBitrateKbps > 0 && (p.MaxBitrateKbps == 0 || ov.MaxBitrateKbps < p.MaxBitrateKbps) {
		return ov.MaxBitrateKbps
	}
	return p.MaxBitrateKbps
}

func effectiveHeight(p *Profile, ov Override) int {
	if ov.MaxHeight > 0 && (p.MaxHeight == 0 || ov.MaxHeight < p.MaxHeight) {
		return ov.MaxHeight
	}
	return p.MaxHeight
}

// containerAliases maps ffprobe's demuxer names onto the profiles' container names.
var containerAliases = map[string]string{"matroska": "mkv"}

// containsContainer reports whether the profile plays the source's container. The
// probe stores ffprobe's format_name, a list of the names its demuxer answers to
// ("mov,mp4,m4a,3gp,3g2,mj2", "matroska,webm"): any one of them in the profile is enough.
func containsContainer(profile []string, container string) bool {
	for _, name := range strings.Split(container, ",") {
		name = strings.TrimSpace(name)
		if alias, ok := containerAliases[strings.ToLower(name)]; ok {
			name = alias
		}
		if contains(profile, name) {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	n := strings.ToLower(needle)
	for _, h := range haystack {
		if strings.EqualFold(h, n) {
			return true
		}
	}
	return false
}

// defaultProfiles seeds the registry. Numbers picked from §4.2 of
// the architecture doc plus reasonable per-profile ceilings.
func defaultProfiles() []*Profile {
	return append([]*Profile{
		{
			Name:             "generic",
			Containers:       []string{"mp4", "ts", "m3u8"},
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac"},
			HDR:              []string{"sdr"},
			MaxHeight:        720,
			MaxBitrateKbps:   4000,
			MaxAudioChannels: 2,
			SupportsHLS:      true,
		},
		// The desktop player on Windows and Linux (Epic 42.4): JavaFX's media stack plays
		// H.264 with AAC, in MP4 or as HLS, nothing else (no mkv, no HEVC), so anything else
		// is remuxed or transcoded; a desktop screen takes 1080p.
		{
			Name:             "desktop-player",
			Containers:       []string{"mp4", "ts", "m3u8"},
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac"},
			HDR:              []string{"sdr"},
			MaxHeight:        1080,
			MaxBitrateKbps:   20000,
			MaxAudioChannels: 2,
			SupportsHLS:      true,
		},
		{
			Name:             "browser-chrome",
			Containers:       []string{"mp4", "webm", "mkv"},
			VideoCodecs:      []string{"h264", "h265", "vp9", "av1"},
			AudioCodecs:      []string{"aac", "opus", "mp3", "flac"},
			HDR:              []string{"sdr", "hlg"},
			MaxHeight:        2160,
			MaxBitrateKbps:   25000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
			SupportsDASH:     true,
		},
		{
			Name:             "browser-safari",
			Containers:       []string{"mp4", "mov"},
			VideoCodecs:      []string{"h264", "h265"},
			AudioCodecs:      []string{"aac", "ac3"},
			HDR:              []string{"sdr", "hdr10", "dolby-vision"},
			MaxHeight:        2160,
			MaxBitrateKbps:   25000,
			MaxAudioChannels: 6,
			SupportsHLS:      true,
		},
		{
			Name:             "ios-native",
			Containers:       []string{"mp4", "mov"},
			VideoCodecs:      []string{"h264", "h265"},
			AudioCodecs:      []string{"aac", "ac3", "eac3"},
			HDR:              []string{"sdr", "hdr10", "dolby-vision"},
			MaxHeight:        2160,
			MaxBitrateKbps:   40000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
		},
		{
			Name:             "tvos",
			Containers:       []string{"mp4", "mov", "mkv"},
			VideoCodecs:      []string{"h264", "h265"},
			AudioCodecs:      []string{"aac", "ac3", "eac3"},
			HDR:              []string{"sdr", "hdr10", "dolby-vision"},
			MaxHeight:        2160,
			MaxBitrateKbps:   60000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
		},
		{
			Name:             "android-native",
			Containers:       []string{"mp4", "mkv", "webm"},
			VideoCodecs:      []string{"h264", "h265", "vp9", "av1"},
			AudioCodecs:      []string{"aac", "opus", "mp3"},
			HDR:              []string{"sdr", "hdr10"},
			MaxHeight:        2160,
			MaxBitrateKbps:   25000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
			SupportsDASH:     true,
		},
		{
			Name:             "androidtv",
			Containers:       []string{"mp4", "mkv"},
			VideoCodecs:      []string{"h264", "h265", "vp9", "av1"},
			AudioCodecs:      []string{"aac", "ac3", "eac3", "opus"},
			HDR:              []string{"sdr", "hdr10", "dolby-vision"},
			MaxHeight:        2160,
			MaxBitrateKbps:   60000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
			SupportsDASH:     true,
		},
	}, tvProfiles()...)
}
