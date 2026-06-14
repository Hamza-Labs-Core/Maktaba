package channel

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// HTTPHandler serves a channel's live HLS window (master + variant
// playlists + segments) from the engine's on-disk dir, touching the
// engine on every segment fetch so the warm/idle reaper sees activity.
type HTTPHandler struct {
	Engine *Engine
}

// Mount wires the channel HLS routes. The streaming server applies the
// signed-URL auth middleware around these (same model as /stream),
// Story 27.3 D8.
func (h *HTTPHandler) Mount(r chi.Router) {
	r.Route("/stream/channel/{id}", func(sub chi.Router) {
		sub.Get("/master.m3u8", h.serveFile)
		sub.Get("/manifest.m3u8", h.serveFile)
		sub.Get("/{rendition}/index.m3u8", h.serveFile)
		sub.Get("/{rendition}/{segment}", h.serveSegment)
	})
}

// serveFile serves a playlist (master or variant index) from the
// channel's HLS dir.
func (h *HTTPHandler) serveFile(w http.ResponseWriter, r *http.Request) {
	dir, ok := h.dirFor(w, r)
	if !ok {
		return
	}
	name := masterOrVariant(r)
	if name == "" {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	serveStatic(w, r, dir, name)
}

// serveSegment serves a .ts segment and touches the engine to keep the
// channel warm (viewer-activity signal, Story 27.3 §4).
func (h *HTTPHandler) serveSegment(w http.ResponseWriter, r *http.Request) {
	dir, ok := h.dirFor(w, r)
	if !ok {
		return
	}
	rendition := chi.URLParam(r, "rendition")
	segment := chi.URLParam(r, "segment")
	if !safeComponent(rendition) || !safeComponent(segment) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		h.Engine.Touch(id)
	}
	serveStatic(w, r, dir, filepath.Join(rendition, segment))
}

// dirFor resolves the on-disk HLS dir for the {id} URL param.
func (h *HTTPHandler) dirFor(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return "", false
	}
	return h.Engine.Layout.HLSDir(channelSessionID(id)), true
}

// masterOrVariant returns the file name for a playlist request, with the
// rendition prefix when present, after validating the components.
func masterOrVariant(r *http.Request) string {
	rendition := chi.URLParam(r, "rendition")
	if rendition != "" {
		if !safeComponent(rendition) {
			return ""
		}
		return filepath.Join(rendition, "index.m3u8")
	}
	if strings.HasSuffix(r.URL.Path, "manifest.m3u8") {
		return "master.m3u8" // alias
	}
	return "master.m3u8"
}

// serveStatic serves a file from base/name with traversal protection.
func serveStatic(w http.ResponseWriter, r *http.Request, base, name string) {
	full := filepath.Join(base, name)
	// Defense in depth: the resolved path must stay under base.
	if rel, err := filepath.Rel(base, full); err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if strings.HasSuffix(name, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasSuffix(name, ".ts") {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.ServeFile(w, r, full)
}

// safeComponent rejects path components that could escape the HLS dir.
func safeComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\") && !strings.Contains(s, "..")
}
