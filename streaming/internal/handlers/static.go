package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

// StaticAssetResolver maps a video id and asset kind to a path on disk.
// Backed by the cache layout in production.
type StaticAssetResolver interface {
	PosterPath(videoID string) string
	SpritePath(videoID, ext string) string
	ThumbPath(videoID, name string) string
}

// StaticHandler serves Story 8.13 — posters, sprite sheets, chapter
// thumbnails. Each path requires a streaming-static JWT (sub =
// sha256(artifact path)); the LibraryGuard middleware checks lib[]
// against the video's library before reaching this handler.
type StaticHandler struct {
	Resolver StaticAssetResolver
	// Files is the file opener so tests inject in-memory readers.
	Files FileOpener
}

// ServePoster handles GET /stream/posters/{video_id}.jpg.
func (h *StaticHandler) ServePoster(w http.ResponseWriter, r *http.Request) {
	videoID := chiURLParam(r, "video_id")
	if videoID == "" {
		// fall back: when called via a session route the subject is the session
		videoID = auth.SubjectFromContext(r.Context())
	}
	path := h.Resolver.PosterPath(strings.TrimSuffix(videoID, ".jpg"))
	h.serveFile(w, r, path, "image/jpeg")
}

// ServeSprite handles GET /stream/sprites/{video_id}.{webp,vtt}.
func (h *StaticHandler) ServeSprite(w http.ResponseWriter, r *http.Request) {
	videoID := chiURLParam(r, "video_id")
	if videoID == "" {
		videoID = auth.SubjectFromContext(r.Context())
	}
	ext := filepath.Ext(videoID)
	if ext == "" {
		// the chi route may inject the bare id; fall back to the URL suffix.
		ext = filepath.Ext(r.URL.Path)
	}
	id := strings.TrimSuffix(videoID, ext)
	contentType := "application/octet-stream"
	switch ext {
	case ".webp":
		contentType = "image/webp"
	case ".vtt":
		contentType = "text/vtt; charset=utf-8"
	}
	path := h.Resolver.SpritePath(id, ext)
	h.serveFile(w, r, path, contentType)
}

// ServeThumb handles GET /stream/thumbs/{video_id}/{name}.
func (h *StaticHandler) ServeThumb(w http.ResponseWriter, r *http.Request) {
	videoID := chiURLParam(r, "video_id")
	name := chiURLParam(r, "name")
	if videoID == "" || name == "" {
		httpx.Write(w, http.StatusBadRequest, "missing-thumb-id", "video_id or name missing", "")
		return
	}
	// Story 23.5 AC-2: video_id and name are untrusted URL segments.
	// The resolver builds {thumbsBase}/{name}; route the resolved
	// path through the single canonicalizer, asserting it stays
	// inside this video's thumbs directory — defeats
	// `..`/NUL/symlink-escape regardless of resolver internals.
	rawPath := h.Resolver.ThumbPath(videoID, name)
	base := h.Resolver.ThumbPath(videoID, "")
	rel := strings.TrimPrefix(strings.TrimPrefix(rawPath, base), string(filepath.Separator))
	path, err := httpx.CanonicalUnder(base, rel)
	if err != nil {
		// Indistinguishable-from-missing so a probe can't tell
		// "traversal rejected" from "no such asset".
		httpx.Write(w, http.StatusNotFound, "asset-not-found", "asset not found", "")
		return
	}
	h.serveFile(w, r, path, contentTypeForThumb(name))
}

func contentTypeForThumb(name string) string {
	switch filepath.Ext(name) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".png":
		return "image/png"
	}
	return "application/octet-stream"
}

func (h *StaticHandler) serveFile(w http.ResponseWriter, _ *http.Request, path, contentType string) {
	f, err := h.Files.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			httpx.Write(w, http.StatusNotFound, "asset-not-found", "asset not found", path)
			return
		}
		httpx.Write(w, http.StatusNotFound, "asset-not-found", "asset not found", err.Error())
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		httpx.Write(w, http.StatusInternalServerError, "stat-failed", "stat failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Cache-Control", "private, max-age=2592000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = copyReader(w, f, 0, stat.Size())
}
