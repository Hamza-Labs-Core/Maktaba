package main

import (
	"mime"
	"strings"
)

// proxiedType is the Content-Type a response read from another machine over the cluster
// link is served with on this machine's origin (the home's front door pulling a channel,
// a node fetching the home's files): the media and data types the link's streams carry
// (HLS playlists and segments, video, audio, images, JSON, plain text) as they came, and
// anything else as application/octet-stream, so another machine can never serve a page
// (HTML, SVG, XML) here. The response also says nosniff.
func proxiedType(ct string) string {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return "application/octet-stream"
	}
	switch {
	case strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"):
		return ct
	case strings.HasPrefix(mt, "image/") && mt != "image/svg+xml":
		return ct
	}
	switch mt {
	case "application/vnd.apple.mpegurl", "application/x-mpegurl", "application/json",
		"application/octet-stream", "text/plain":
		return ct
	}
	return "application/octet-stream"
}
