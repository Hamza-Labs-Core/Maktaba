package handlers

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
)

// The session's signed URL (?sig=, Epic 8 Story 8.1) covers every path under
// /stream/{session_id}/, but a player resolves the playlist's child URIs
// (rendition playlists, segments, init sections) without the query string: a
// browser's native HLS never adds it. So the playlists the session serves carry
// the same token on each child URI, and the whole session plays from the one
// URL the API minted.

var hlsURIAttr = regexp.MustCompile(`URI="([^"]*)"`)

var mpdURLAttr = regexp.MustCompile(`\b(media|initialization)="([^"]*)"`)

// withSig appends sig=<token> to a relative or same-origin URI; URIs that
// already carry a sig, and other origins' or data: URIs, are left alone.
func withSig(uri, sig string) string {
	u := strings.TrimSpace(uri)
	if u == "" || strings.Contains(u, "://") || strings.HasPrefix(u, "data:") || strings.HasPrefix(u, "//") {
		return uri
	}
	base, frag, _ := strings.Cut(u, "#")
	if strings.Contains(base, "?sig=") || strings.Contains(base, "&sig=") {
		return uri
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	out := base + sep + "sig=" + url.QueryEscape(sig)
	if frag != "" {
		out += "#" + frag
	}
	return out
}

// SignPlaylist puts sig on every child URI of an HLS playlist: the URI lines
// and the URI="..." attributes (EXT-X-MEDIA, EXT-X-MAP, EXT-X-I-FRAME-STREAM-INF).
func SignPlaylist(data []byte, sig string) []byte {
	lines := bytes.Split(data, []byte("\n"))
	for i, raw := range lines {
		line := strings.TrimRight(string(raw), "\r")
		cr := len(line) != len(raw)
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "#"):
			if !strings.Contains(trimmed, `URI="`) || strings.HasPrefix(trimmed, "#EXT-X-KEY") {
				continue // a key URI points at a key server, not this session
			}
			line = hlsURIAttr.ReplaceAllStringFunc(line, func(m string) string {
				sub := hlsURIAttr.FindStringSubmatch(m)
				return `URI="` + withSig(sub[1], sig) + `"`
			})
		default:
			line = withSig(line, sig)
		}
		if cr {
			line += "\r"
		}
		lines[i] = []byte(line)
	}
	return bytes.Join(lines, []byte("\n"))
}

// SignMPD puts sig on a DASH manifest's SegmentTemplate media/initialization
// URLs ('&' escaped for XML).
func SignMPD(data []byte, sig string) []byte {
	return mpdURLAttr.ReplaceAllFunc(data, func(m []byte) []byte {
		sub := mpdURLAttr.FindSubmatch(m)
		v := strings.ReplaceAll(withSig(strings.ReplaceAll(string(sub[2]), "&amp;", "&"), sig), "&", "&amp;")
		return []byte(string(sub[1]) + `="` + v + `"`)
	})
}
