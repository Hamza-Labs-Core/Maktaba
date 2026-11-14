package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// DeviceCaps is what a device reports it plays (Epic 58 story 58.02), sent on
// POST /api/stream/sessions as device_caps. Each app fills it from its
// platform's own query (MediaCodecList, webapis.avplay, roDeviceInfo,
// canDisplayType, …). A nil slice is a field the report leaves out: the
// profile's value stands. A present one, even empty, is the device's answer
// (the lists carry no omitempty, so Key tells the two apart).
type DeviceCaps struct {
	Containers       []string   `json:"containers"`
	Video            []VideoCap `json:"video"`
	HDR              []string   `json:"hdr"`
	Audio            []string   `json:"audio"`
	Passthrough      []string   `json:"passthrough"`
	MaxAudioChannels int        `json:"max_audio_channels,omitempty"`
	HLSSegments      []string   `json:"hls_segments"`
	DASH             *bool      `json:"dash,omitempty"`
	MaxBitrateKbps   int        `json:"max_bitrate_kbps,omitempty"`
}

// VideoCap is one video codec the device decodes, with its limits (0: the
// profile's).
type VideoCap struct {
	Codec     string `json:"codec"`
	MaxHeight int    `json:"max_height,omitempty"`
	MaxFPS    int    `json:"max_fps,omitempty"`
	BitDepth  int    `json:"bit_depth,omitempty"`
}

// ParseDeviceCaps reads a device_caps object. Keys it does not know are
// ignored (a newer app may send more than this home reads); a value of the
// wrong type is an error.
func ParseDeviceCaps(raw []byte) (*DeviceCaps, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var c DeviceCaps
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("device_caps: %w", err)
	}
	return &c, nil
}

// Key is a stable fingerprint of the report ("" for none): two opens with the
// same profile and key fit the same session.
func (c *DeviceCaps) Key() string {
	if c == nil {
		return ""
	}
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// canonVideo is a video codec's one name: ffprobe's ("hevc", "h264", "av1",
// "vp9"), whatever a profile, a report or a probe calls it.
func canonVideo(c string) string {
	switch c = strings.ToLower(strings.TrimSpace(c)); c {
	case "h265", "hev1", "hvc1", "hevc", "x265":
		return "hevc"
	case "avc", "avc1", "h264", "x264":
		return "h264"
	case "av01", "av1":
		return "av1"
	case "vp09", "vp9":
		return "vp9"
	}
	return c
}

// canonAudio is an audio codec's one name (ffprobe's, DTS in all its forms as "dts").
func canonAudio(c string) string {
	switch c = strings.ToLower(strings.TrimSpace(c)); c {
	case "e-ac-3", "ec-3", "eac3", "dd+", "ddp":
		return "eac3"
	case "ac-3", "ac3", "dd":
		return "ac3"
	case "dts", "dca", "dts-hd", "dtshd", "dts-x", "dtsx", "dts-hd-ma":
		return "dts"
	case "truehd", "mlp", "atmos-truehd":
		return "truehd"
	case "ac-4", "ac4":
		return "ac4"
	case "mp4a", "aac", "aac-lc":
		return "aac"
	}
	return c
}

// canonContainer is a container's one name ("matroska" is "mkv").
func canonContainer(c string) string {
	switch c = strings.ToLower(strings.TrimSpace(c)); c {
	case "matroska":
		return "mkv"
	case "mpegts", "m2ts":
		return "ts"
	}
	return c
}

// canonHDR is an HDR format's one name: "sdr", "hdr10", "hlg", "dv-p5",
// "dv-p7", "dv-p8". "dolby-vision" (the older profiles' word) is every
// Dolby Vision profile (expandHDR).
func canonHDR(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	switch c {
	case "hdr10+", "hdr10plus":
		return "hdr10" // its HDR10 base plays where HDR10 does
	case "dovi", "dv":
		return "dolby-vision"
	}
	return c
}

var dvProfiles = []string{"dv-p5", "dv-p7", "dv-p8"}

func expandHDR(list []string) []string {
	out := make([]string, 0, len(list))
	for _, h := range list {
		if h = canonHDR(h); h == "dolby-vision" {
			out = append(out, dvProfiles...)
			continue
		}
		out = append(out, h)
	}
	return out
}

func canonAll(list []string, canon func(string) string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if c := canon(v); c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// intersect keeps the reported values the ceiling has (canonical names), and
// lists the rest as clamped (prefixed with what they are).
func intersect(report, ceiling []string, canon func(string) string, what string, clamped *[]string) []string {
	ceil := canonAll(ceiling, canon)
	out := []string{}
	for _, v := range canonAll(report, canon) {
		if slices.Contains(ceil, v) {
			out = append(out, v)
		} else {
			*clamped = append(*clamped, what+":"+v)
		}
	}
	return out
}

// minPositive is the smaller of two limits where 0 means none.
func minPositive(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	}
	return min(a, b)
}

// Apply merges a device's report into the named profile: the report narrows the
// profile freely and widens it only up to the profile's Ceiling (a profile with
// none is its own ceiling: a report only narrows it). The result is a new
// profile (the registry's is never changed) and what the report asked beyond
// the ceiling, which the verdict's Reason names. A nil report is the profile
// itself: its baseline, which plays on every model of the platform.
func Apply(p *Profile, c *DeviceCaps) (*Profile, []string) {
	if c == nil {
		return p, nil
	}
	ceil := p.Ceiling
	if ceil == nil {
		ceil = p
	}
	out := *p
	out.Ceiling = nil
	clamped := []string{}

	if c.Containers != nil {
		out.Containers = intersect(c.Containers, ceil.Containers, canonContainer, "container", &clamped)
	}
	if c.Video != nil {
		ceilCodecs := canonAll(ceil.VideoCodecs, canonVideo)
		out.VideoCodecs = []string{}
		out.VideoHeights = map[string]int{}
		out.VideoBitDepths = map[string]int{}
		out.VideoFPS = map[string]int{}
		top := 0
		for _, v := range c.Video {
			codec := canonVideo(v.Codec)
			if codec == "" || slices.Contains(out.VideoCodecs, codec) {
				continue
			}
			if !slices.Contains(ceilCodecs, codec) {
				clamped = append(clamped, "video:"+codec)
				continue
			}
			limit := ceil.heightFor(codec)
			h := minPositive(v.MaxHeight, limit)
			if limit > 0 && v.MaxHeight > limit {
				clamped = append(clamped, "height:"+codec+" "+strconv.Itoa(v.MaxHeight)+">"+strconv.Itoa(limit))
			}
			out.VideoCodecs = append(out.VideoCodecs, codec)
			out.VideoHeights[codec] = h
			if v.BitDepth > 0 {
				out.VideoBitDepths[codec] = v.BitDepth
			}
			if v.MaxFPS > 0 {
				out.VideoFPS[codec] = v.MaxFPS
			}
			top = max(top, h)
		}
		out.MaxHeight = top
	}
	if c.HDR != nil {
		out.HDR = intersect(expandHDR(c.HDR), expandHDR(ceil.HDR), canonHDR, "hdr", &clamped)
	}
	if c.Audio != nil {
		out.AudioCodecs = intersect(c.Audio, ceil.AudioCodecs, canonAudio, "audio", &clamped)
	}
	if c.Passthrough != nil {
		out.Passthrough = intersect(c.Passthrough, ceil.Passthrough, canonAudio, "passthrough", &clamped)
	}
	if c.MaxAudioChannels > 0 {
		if ceil.MaxAudioChannels > 0 && c.MaxAudioChannels > ceil.MaxAudioChannels {
			clamped = append(clamped, "channels:"+strconv.Itoa(c.MaxAudioChannels)+">"+strconv.Itoa(ceil.MaxAudioChannels))
		}
		out.MaxAudioChannels = minPositive(c.MaxAudioChannels, ceil.MaxAudioChannels)
	}
	if c.HLSSegments != nil {
		ceilSegs := ceil.HLSSegments
		if len(ceilSegs) == 0 {
			ceilSegs = []string{"ts"}
		}
		out.HLSSegments = intersect(c.HLSSegments, ceilSegs, strings.ToLower, "hls_segments", &clamped)
	}
	if c.DASH != nil {
		if *c.DASH && !ceil.SupportsDASH {
			clamped = append(clamped, "dash")
		}
		out.SupportsDASH = *c.DASH && ceil.SupportsDASH
	}
	if c.MaxBitrateKbps > 0 {
		if ceil.MaxBitrateKbps > 0 && c.MaxBitrateKbps > ceil.MaxBitrateKbps {
			clamped = append(clamped, "bitrate:"+strconv.Itoa(c.MaxBitrateKbps)+">"+strconv.Itoa(ceil.MaxBitrateKbps))
		}
		out.MaxBitrateKbps = minPositive(c.MaxBitrateKbps, ceil.MaxBitrateKbps)
	} else if c.Video != nil {
		// the baseline's bitrate is an H.264 1080p one: a device naming its video
		// limits and no bitrate is held to its platform's
		out.MaxBitrateKbps = ceil.MaxBitrateKbps
	}
	if len(clamped) == 0 {
		clamped = nil
	}
	return &out, clamped
}

// heightFor is the tallest picture the profile plays in codec (its per-codec
// limit from a report, else MaxHeight).
func (p *Profile) heightFor(codec string) int {
	if h, ok := p.VideoHeights[canonVideo(codec)]; ok {
		return h
	}
	return p.MaxHeight
}
