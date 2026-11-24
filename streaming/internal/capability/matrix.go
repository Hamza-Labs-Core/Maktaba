// Package capability implements the client profile registry and the
// can-direct-play / can-remux decision logic (Story 8.2). The matrix
// drives which serving mode a session opens in: direct, remux, or
// transcode.
package capability

import (
	"sort"
	"strconv"
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
	VideoCodecs      []string // e.g. "h264", "hevc", "vp9", "av1" (read through canonVideo)
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
	// VideoHardware is a report's word on whether each codec is decoded in
	// hardware (story 13.19; absent: not said). No decision reads it; a
	// verdict's Reason names it.
	VideoHardware map[string]bool
	// AudioByReport: a transcode keeps up to the report's audio channels
	// (story 13.19, desktop-mpv; TranscodeAudio). Without it a transcode's
	// audio is the encoder's AAC, as it always was.
	AudioByReport bool
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
	// source's audio is copied. For an AudioByReport profile's transcode
	// (story 13.19) it is the transcode's audio (TranscodeAudio; "": the
	// encoder's default AAC, as for every other profile).
	AudioCodec string
	// ReportHeightCap is the tallest H.264 picture the device's report says it
	// decodes (an AudioByReport profile's transcode, story 13.19; 0: none): the
	// transcode's ladder stops there.
	ReportHeightCap int
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

// Names lists the registry's profiles, sorted: what a client may ask for by name (a player
// asks the home before it asks for a profile an older home would read as "generic").
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.profiles))
	for n := range r.profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
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
//  3. a codec not in the profile, or HDR it does not show → transcode
//  4. container-only mismatch → remux
//  5. all containers/codecs/HDR in profile allow-list → direct
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

	// a device's report (Apply) sets each codec's own height, bit depth and
	// frame rate: a phone whose HEVC decoder is 8-bit or stops at 1080p gets a
	// transcode of what it cannot decode. Without a report these are unset.
	vcodec := canonVideo(src.VideoCodec)
	if h := profile.heightFor(vcodec); h > 0 && src.Height > h {
		reason := "source height exceeds profile"
		if how := decodedBy(profile, vcodec); how != "" {
			// story 13.19: the session's detail and the Player's problem report say why
			reason = "source height exceeds the device's: " + codecName(vcodec) + " decoded in " + how +
				" up to " + strconv.Itoa(h) + "p"
		}
		return Verdict{
			Mode:           ModeTranscode,
			Reason:         reason,
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov),
		}
	}
	if d := profile.VideoBitDepths[vcodec]; d > 0 && src.BitDepth > d {
		reason := "source bit depth exceeds the device's " + strconv.Itoa(d) + "-bit " + vcodec
		if how := decodedBy(profile, vcodec); how != "" {
			reason += " (decoded in " + how + ")"
		}
		return Verdict{
			Mode:           ModeTranscode,
			Reason:         reason,
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov),
		}
	}
	if f := profile.VideoFPS[vcodec]; f > 0 && src.FPS > float64(f)+0.5 {
		return Verdict{
			Mode:           ModeTranscode,
			Reason:         "source frame rate exceeds the device's",
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov),
		}
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

	// a profile's codec names and the probe's (ffprobe's "hevc", "eac3") are
	// read as one name each: "h265" in a profile is the probe's "hevc"
	codecsOK := has(profile.VideoCodecs, src.VideoCodec, canonVideo) && has(profile.AudioCodecs, src.AudioCodec, canonAudio)
	if !codecsOK {
		return Verdict{Mode: ModeTranscode, Reason: "codec not in profile",
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov)}
	}
	if !showsPicture(profile, src) {
		// the transcode tone-maps it to SDR
		return Verdict{Mode: ModeTranscode, Reason: "HDR not in profile",
			BitrateCapKbps: effectiveBitrate(profile, ov),
			HeightCap:      effectiveHeight(profile, ov)}
	}
	containerOK := containsContainer(profile.Containers, src.Container)
	if !containerOK {
		return Verdict{Mode: ModeRemux, Reason: "container not in profile, codecs OK"}
	}
	return Verdict{Mode: ModeDirect, Reason: "all in profile"}
}

// decodedBy is how a report says the device decodes codec ("hardware",
// "software"; "" when it does not say).
func decodedBy(p *Profile, codec string) string {
	hw, ok := p.VideoHardware[codec]
	switch {
	case !ok:
		return ""
	case hw:
		return "hardware"
	}
	return "software"
}

// codecName is a video codec as a person reads it ("AV1", "HEVC", "MPEG-2").
func codecName(codec string) string {
	switch codec {
	case "h264":
		return "H.264"
	case "mpeg2video":
		return "MPEG-2"
	}
	return strings.ToUpper(codec)
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
		// The desktop player on libmpv (Epic 13 story 13.18): FFmpeg's decoders in software
		// where the machine has no hardware for a codec, so it plays the home's files as they
		// are. mpv tone-maps HDR10 and HLG to an SDR screen and folds 5.1 and 7.1 down to the
		// speakers it has. A Player on a home without this profile asks for desktop-player.
		// ("mpegts" is ffprobe's name for a TS file: mpv plays one as it is, as JavaFX could not.)
		{
			Name:             "desktop-mpv",
			Containers:       []string{"mkv", "webm", "mp4", "mov", "ts", "mpegts", "m3u8"},
			VideoCodecs:      []string{"h264", "hevc", "vp9", "av1", "mpeg2video"},
			AudioCodecs:      []string{"aac", "ac3", "eac3", "dts", "flac", "opus", "vorbis", "mp3"},
			HDR:              []string{"sdr", "hdr10", "hlg"},
			MaxHeight:        2160,
			MaxBitrateKbps:   80000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
			// story 13.19: the Player's report (device_caps: what decodes in hardware,
			// the height software decodes smoothly, the audio device's channels)
			// narrows it per codec, and a transcode keeps the device's channels
			AudioByReport: true,
		},
		{
			Name:       "browser-chrome",
			Containers: []string{"mp4", "webm", "mkv"},
			// no HEVC: Chrome decodes it only where the hardware does, and
			// Firefox and Edge on Linux not at all
			VideoCodecs:      []string{"h264", "vp9", "av1"},
			AudioCodecs:      []string{"aac", "opus", "mp3", "flac"},
			HDR:              []string{"sdr", "hlg"},
			MaxHeight:        2160,
			MaxBitrateKbps:   25000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
			SupportsDASH:     true,
		},
		{
			Name:       "browser-safari",
			Containers: []string{"mp4", "mov"},
			// no HEVC: Safari and AVPlayer refuse an MP4 whose HEVC is
			// tagged hev1 (ffmpeg's default), and the probe does not record
			// the tag; these profiles' remux is a transcode
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac", "ac3"},
			HDR:              []string{"sdr", "hdr10", "dolby-vision"},
			MaxHeight:        2160,
			MaxBitrateKbps:   25000,
			MaxAudioChannels: 6,
			SupportsHLS:      true,
		},
		{
			Name:       "ios-native",
			Containers: []string{"mp4", "mov"},
			// no HEVC: Safari and AVPlayer refuse an MP4 whose HEVC is
			// tagged hev1 (ffmpeg's default), and the probe does not record
			// the tag; these profiles' remux is a transcode
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac", "ac3", "eac3"},
			HDR:              []string{"sdr", "hdr10", "dolby-vision"},
			MaxHeight:        2160,
			MaxBitrateKbps:   40000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
		},
		{
			Name:       "tvos",
			Containers: []string{"mp4", "mov", "mkv"},
			// no HEVC: Safari and AVPlayer refuse an MP4 whose HEVC is
			// tagged hev1 (ffmpeg's default), and the probe does not record
			// the tag; these profiles' remux is a transcode
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac", "ac3", "eac3"},
			HDR:              []string{"sdr", "hdr10", "dolby-vision"},
			MaxHeight:        2160,
			MaxBitrateKbps:   60000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
		},
		// The Android Player on a phone, tablet or foldable (Media3). Its report
		// (device_caps, read from the device's own decoders) narrows this, and
		// widens it up to its Ceiling: the Dolby, FLAC, Vorbis and DTS audio a
		// device decodes itself, and HLG on a screen that shows it.
		// Without a report, this is what every Android phone plays.
		{
			Name:             "android-native",
			Containers:       []string{"mp4", "mkv", "webm"},
			VideoCodecs:      []string{"h264", "hevc", "vp9", "av1"},
			AudioCodecs:      []string{"aac", "opus", "mp3"},
			HDR:              []string{"sdr", "hdr10"},
			MaxHeight:        2160,
			MaxBitrateKbps:   25000,
			MaxAudioChannels: 8,
			SupportsHLS:      true,
			SupportsDASH:     true,
			Ceiling: &Profile{
				Containers:       []string{"mp4", "mkv", "webm"},
				VideoCodecs:      []string{"h264", "hevc", "vp9", "av1"},
				AudioCodecs:      []string{"aac", "opus", "mp3", "ac3", "eac3", "flac", "vorbis", "dts"},
				HDR:              []string{"sdr", "hdr10", "hlg"},
				MaxHeight:        2160,
				MaxBitrateKbps:   25000,
				MaxAudioChannels: 8,
				SupportsHLS:      true,
				SupportsDASH:     true,
				HLSSegments:      []string{"ts", "fmp4"},
			},
		},
		{
			Name:             "androidtv",
			Containers:       []string{"mp4", "mkv"},
			VideoCodecs:      []string{"h264", "hevc", "vp9", "av1"},
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
