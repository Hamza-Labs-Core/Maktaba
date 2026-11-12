package httpx

import (
	"net/http"
	"strings"
)

// PassType is the Content-Type a wrapping handler passes another handler's body on
// under: the body's own type, unless it is one a browser runs as a page (HTML,
// XHTML, SVG, XML), which goes out as application/octet-stream. A stream, a
// playlist or a problem keeps its type; nothing a wrapper passes on can be opened
// as a page.
func PassType(contentType string) string {
	t, _, _ := strings.Cut(contentType, ";")
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "text/xml", "application/xml":
		return "application/octet-stream"
	}
	return contentType
}

// PassHeaders prepares h for a body a wrapping handler passes on with status:
// nosniff, so a browser keeps to the type it is told, and the type itself, which it
// returns. A body whose handler set no type is application/octet-stream (the
// server would otherwise guess one from the body's first bytes, a page's too). A
// status that carries no body gets no type ("": none is set then).
func PassHeaders(h http.Header, status int) string {
	h.Set("X-Content-Type-Options", "nosniff")
	ct := h.Get("Content-Type")
	if ct == "" {
		if status == http.StatusNoContent || status == http.StatusNotModified || status/100 == 1 {
			return ""
		}
		return "application/octet-stream"
	}
	return PassType(ct)
}
