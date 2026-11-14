package streaming

import (
	"encoding/json"
	"net/url"
	"strings"
)

// DeviceCaps is what a device reports it plays (Epic 58 story 58.02), the
// optional device_caps of POST /api/stream/sessions. Each TV app fills it from
// its platform's own query, at start and whenever its output changes. The
// streaming service merges it into the client profile: it narrows the profile
// freely and widens it only up to the platform's ceiling. A field left out is
// the profile's; keys this home does not know are ignored (a newer app may send
// more).
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

// A list left out is null (the profile's), an empty one the device's "none":
// the lists carry no omitempty, so an empty answer is passed on as one.

// VideoCap is one video codec the device decodes and its limits.
type VideoCap struct {
	Codec     string `json:"codec"`
	MaxHeight int    `json:"max_height,omitempty"`
	MaxFPS    int    `json:"max_fps,omitempty"`
	BitDepth  int    `json:"bit_depth,omitempty"`
}

// deviceCapsLimit bounds each list of a report: a platform's real answer is a
// handful of entries.
const deviceCapsLimit = 32

// readDeviceCaps reads a request's device_caps (nil when it sent none): the
// known keys kept, unknown ones dropped, a value of the wrong type an error.
// The request body itself is read strictly (an unknown top-level key is a 400);
// device_caps alone is lenient, as each platform's report grows on its own.
func readDeviceCaps(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var c DeviceCaps
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	for _, l := range [][]string{c.Containers, c.HDR, c.Audio, c.Passthrough, c.HLSSegments} {
		if len(l) > deviceCapsLimit {
			return nil, errTooMany
		}
	}
	if len(c.Video) > deviceCapsLimit {
		return nil, errTooMany
	}
	return json.Marshal(c)
}

type capsError string

func (e capsError) Error() string { return string(e) }

const errTooMany = capsError("a device_caps list has more than 32 entries")

// sigOf is a signed URL's token ("" without one).
func sigOf(signed string) string {
	u, err := url.Parse(signed)
	if err != nil {
		return ""
	}
	return u.Query().Get("sig")
}

// streamPath is the session's own folder on the streaming service, which a
// stream_sig opens: its subtitle renditions, sidecar subtitles and chapters.
func streamPath(sessionID string) string {
	return "/stream/" + strings.ToLower(sessionID) + "/chapters.json"
}
