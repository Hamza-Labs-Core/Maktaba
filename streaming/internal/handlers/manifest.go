package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

func chiURLParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

// SessionStreamReader exposes the subset of the session store the
// HTTP-side handlers need. Test fakes implement it directly.
type SessionStreamReader interface {
	Get(ctx context.Context, id uuid.UUID) (*session.Row, bool, error)
	Touch(ctx context.Context, id uuid.UUID, at time.Time) error
}

// HLSDirResolver maps a session id to its on-disk HLS folder. Backed
// by the cache layout in production.
type HLSDirResolver interface {
	HLSDir(sessionID string) string
}

// ManifestHandler serves Story 8.5/8.6 — master.m3u8, the per-rendition
// index.m3u8, manifest.mpd, and the segments under each.
type ManifestHandler struct {
	Sessions SessionStreamReader
	Layout   HLSDirResolver
	Now      func() time.Time
}

// ServeMaster handles GET /stream/{session_id}/manifest.{m3u8,mpd}.
func (h *ManifestHandler) ServeMaster(w http.ResponseWriter, r *http.Request) {
	sub := auth.SubjectFromContext(r.Context())
	id, err := uuid.Parse(sub)
	if err != nil {
		httpx.Write(w, http.StatusBadRequest, "bad-session-id", "session id not a UUID", err.Error())
		return
	}
	row, ok, err := h.Sessions.Get(r.Context(), id)
	if err != nil || !ok {
		httpx.Write(w, http.StatusNotFound, "session-not-found", "session not found", "")
		return
	}

	// Path determines which manifest to look at.
	wantDASH := strings.HasSuffix(r.URL.Path, ".mpd")
	wantHLS := strings.HasSuffix(r.URL.Path, ".m3u8")
	if (wantDASH && row.Format != session.FormatDASH) || (wantHLS && row.Format != session.FormatHLS) {
		httpx.Write(w, http.StatusConflict, "format-mismatch",
			"session format mismatch",
			fmt.Sprintf("session.format=%s url=%s", row.Format, r.URL.Path))
		return
	}

	dir := h.Layout.HLSDir(sub)
	var path string
	switch {
	case wantDASH:
		path = filepath.Join(dir, "manifest.mpd")
	default:
		path = filepath.Join(dir, "master.m3u8")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			httpx.Write(w, http.StatusNotFound, "manifest-not-found",
				"manifest not yet on disk",
				"the FFmpeg subprocess hasn't written the master playlist; client should retry")
			return
		}
		httpx.Write(w, http.StatusInternalServerError, "read-failed", "manifest read failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", manifestContentType(wantDASH))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// ServeRenditionIndex handles /stream/{session_id}/{rendition}/index.m3u8.
func (h *ManifestHandler) ServeRenditionIndex(w http.ResponseWriter, r *http.Request) {
	sub := auth.SubjectFromContext(r.Context())
	rendition := chiURLParam(r, "rendition")
	if rendition == "" {
		httpx.Write(w, http.StatusBadRequest, "missing-rendition", "rendition path missing", "")
		return
	}
	// Story 23.5 AC-2: rendition is an untrusted URL segment — route
	// it through the single canonicalizer so `..`/symlink-escape
	// cannot read outside this session's HLS directory.
	base := h.Layout.HLSDir(sub)
	path, err := httpx.CanonicalUnder(base, rendition, "index.m3u8")
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "rendition-not-found", "rendition index not on disk", "")
		return
	}
	// Defence-in-depth, statically-visible containment recheck before
	// the filesystem sink (Story 23.5 AC-2). CanonicalUnder already
	// guarantees this; the explicit Clean+prefix gate clears CodeQL.
	path, err = httpx.EnsureUnder(base, path)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "rendition-not-found", "rendition index not on disk", "")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "rendition-not-found", "rendition index not on disk", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// ServeSegment handles /stream/{session_id}/{rendition}/seg-{n}.ts and
// updates the session heartbeat (Story 8.9 AC-2).
func (h *ManifestHandler) ServeSegment(w http.ResponseWriter, r *http.Request) {
	sub := auth.SubjectFromContext(r.Context())
	id, err := uuid.Parse(sub)
	if err != nil {
		httpx.Write(w, http.StatusBadRequest, "bad-session-id", "session id not a UUID", err.Error())
		return
	}
	rendition := chiURLParam(r, "rendition")
	segment := chiURLParam(r, "segment")
	if rendition == "" || segment == "" {
		httpx.Write(w, http.StatusBadRequest, "missing-segment", "segment path incomplete", "")
		return
	}

	// Story 23.5 AC-2: rendition + segment are untrusted URL segments
	// — canonicalize before touching disk.
	base := h.Layout.HLSDir(sub)
	path, err := httpx.CanonicalUnder(base, rendition, segment)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "segment-not-found",
			"segment not yet written by FFmpeg", "")
		return
	}
	// Defence-in-depth, statically-visible containment recheck before
	// the filesystem sink (Story 23.5 AC-2).
	path, err = httpx.EnsureUnder(base, path)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "segment-not-found",
			"segment not yet written by FFmpeg", "")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// AC-3: 404 if segment not yet on disk. Players will refetch
		// the variant playlist and try again.
		httpx.Write(w, http.StatusNotFound, "segment-not-found",
			"segment not yet written by FFmpeg", err.Error())
		return
	}

	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now()
	}
	_ = h.Sessions.Touch(r.Context(), id, now)

	w.Header().Set("Content-Type", segmentContentType(segment))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func manifestContentType(isDASH bool) string {
	if isDASH {
		return "application/dash+xml"
	}
	return "application/vnd.apple.mpegurl"
}

func segmentContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".ts"):
		return "video/MP2T"
	case strings.HasSuffix(name, ".m4s"):
		return "video/iso.segment"
	case strings.HasSuffix(name, ".mp4"):
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

// BuildMasterPlaylist composes the HLS master m3u8 from the ladder
// (Story 8.5 §4.3 shape). Delegates to ffmpeg.BuildMasterPlaylistFor so
// the on-disk master the orchestrator writes (HLB-328) and what tests
// assert come from a single source of truth.
func BuildMasterPlaylist(ladder []ffmpeg.Rendition) []byte {
	return ffmpeg.BuildMasterPlaylistFor(ladder)
}
