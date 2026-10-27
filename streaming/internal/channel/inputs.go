package channel

import (
	"errors"
	"net/url"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// Inputs handed to ffmpeg/ffprobe come from the database (video paths,
// live source URLs, look asset paths). ffmpeg understands many protocols
// (file:, concat:, subfile:, udp:, pipe:, data: …), so a stored value
// must never be able to pick one: local paths are forced through the
// file: protocol, and network inputs are limited to an allow-list.

// LocalInput returns the ffmpeg input for a stored local path, forcing
// the file: protocol, as this machine's disk spells it (pathmap.OnDisk).
// Values that look like URLs or protocol specs are refused (a Windows
// drive letter "C:" is a path, not a protocol).
func LocalInput(p string) (string, error) {
	if p == "" {
		return "", errors.New("channel: empty media path")
	}
	if strings.Contains(p, "://") || hasProtocolPrefix(p) || strings.ContainsAny(p, "\x00\n\r") {
		return "", errors.New("channel: media path is not a local file path")
	}
	return "file:" + pathmap.OnDisk(p), nil
}

// hasProtocolPrefix reports "name:" prefixes longer than a drive letter.
func hasProtocolPrefix(p string) bool {
	i := strings.IndexByte(p, ':')
	if i <= 1 { // no colon, or "C:" drive letter
		return false
	}
	for _, r := range p[:i] {
		if !schemeChar(r) {
			return false // a colon later in a normal path
		}
	}
	return true
}

func schemeChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return r == '+' || r == '-' || r == '.'
}

// IsStreamURL reports whether u is a network stream ffmpeg may open for
// a live block: http(s), rtmp(s), rtsp or srt with a host.
func IsStreamURL(u string) bool {
	pu, err := url.Parse(strings.TrimSpace(u))
	if err != nil || pu.Host == "" {
		return false
	}
	switch strings.ToLower(pu.Scheme) {
	case "http", "https", "rtmp", "rtmps", "rtsp", "srt":
		return true
	}
	return false
}

// redactURL drops the query (signed tokens) from a URL for logs.
func redactURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		return u[:i]
	}
	return u
}
