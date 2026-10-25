package main

import (
	"mime"
	"strings"
)

// proxiedType is the Content-Type a response read from another machine over the cluster
// link is served with on this machine's origin (the home's front door pulling a channel,
// a node fetching the home's files, a VOD fallback): the media and data types the link's
// streams carry (HLS playlists and segments, DASH manifests, subtitles, video, audio,
// images, JSON and problem details, plain text) as they came, and anything else as
// application/octet-stream, so another machine can never serve a page (HTML, SVG, XML)
// here. The response also says nosniff.
// proxiedCSP is the Content-Security-Policy of what another machine's stream sent: a
// player reading it is not concerned, and a browser opening it as a document (an +xml type
// renders as one) runs nothing of it.
const proxiedCSP = "default-src 'none'; sandbox"

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
	case "application/vnd.apple.mpegurl", "application/x-mpegurl", "application/dash+xml", "text/vtt",
		"application/json", "application/problem+json", "application/octet-stream", "text/plain":
		return ct
	}
	return "application/octet-stream"
}
