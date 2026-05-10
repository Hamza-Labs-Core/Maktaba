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
	Name              string
	Containers        []string // e.g. "mp4", "mkv", "webm", "mov", "ts"
	VideoCodecs       []string // e.g. "h264", "h265", "vp9", "av1"
	AudioCodecs       []string // e.g. "aac", "ac3", "eac3", "mp3", "opus", "flac"
	HDR               []string // e.g. "sdr", "hdr10", "dolby-vision", "hlg"
	MaxHeight         int      // 1080, 720, 2160…
	MaxBitrateKbps    int      // 0 = unlimited
	MaxAudioChannels  int      // 2, 6, 8…
	SupportsHLS       bool
	SupportsDASH      bool
	HardwareDecoded   bool
}

// Source is what a probe row looks like to the matrix. We keep it
// shallow so the probe package owns the wire shape.
type Source struct {
	Container        string
	VideoCodec       string
	AudioCodec       string
	HDR              string
	Height           int
	BitrateKbps      int
	AudioChannels    int
	IsContainerOnlyChange bool // true if the codecs are direct-playable but the container needs a remux
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
	Mode             Mode
	Reason           string
	BitrateCapKbps   int
	HeightCap        int
	UsedFallback     bool
}

// Override is a per-session forcing flag set on OpenSession.
type Override struct {
	ForceTranscode bool
	ForceSoftware  bool
	MaxBitrateKbps int
	MaxHeight      int
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
	containerOK := contains(profile.Containers, src.Container)
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
	return []*Profile{
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
	}
}
