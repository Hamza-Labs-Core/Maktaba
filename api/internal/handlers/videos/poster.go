package videos

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// thumbnailRoot is the pipeline's thumbnail cache: MAKTABA_THUMBNAIL_ROOT,
// else ~/.maktaba/cache/thumbnails (the same default the pipeline uses).
func thumbnailRoot() string {
	if v := os.Getenv("MAKTABA_THUMBNAIL_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".maktaba", "cache", "thumbnails")
}

// posterCandidates lists, best first, the images that can stand for a
// video: its own poster, the pipeline's poster and small copy in the
// thumbnail cache (a folder per content hash), and the copy the
// mediaintel import staged by video id while the real hash is pending.
func posterCandidates(root, videoID, contentHash, posterPath string) []string {
	var out []string
	if posterPath != "" {
		out = append(out, posterPath)
	}
	if root != "" {
		if contentHash != "" {
			dir := filepath.Join(root, contentHash)
			out = append(out,
				filepath.Join(dir, "poster.jpg"),
				filepath.Join(dir, "poster.webp"),
				filepath.Join(dir, "small.jpg"))
		}
		out = append(out, filepath.Join(root, "imported", videoID+".jpg"))
	}
	return out
}

// Poster implements GET /api/videos/{id}/poster: the library grid's card
// image. 404 when no image exists yet (the card shows its fallback).
func (h *Handler) Poster(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var libraryID, contentHash string
	var posterPath sql.NullString
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT library_id::text, content_hash, poster_path FROM videos WHERE id = $1`, id,
	).Scan(&libraryID, &contentHash, &posterPath)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}
	if herr := h.canRead(r.Context(), libraryID); herr != nil {
		httperror.Write(w, r, herr)
		return
	}
	// The video's own poster comes from the library, not from the request.
	if servePoster(w, r, posterPath.String) {
		return
	}
	// Cache candidates are built from the id and the hash: cleaned, and kept
	// inside the cache folder before they reach the file system.
	root := thumbnailRoot()
	if root == "" {
		httperror.Write(w, r, httperror.NotFound("no poster for video "+id))
		return
	}
	base := filepath.Clean(root) + string(filepath.Separator)
	for _, c := range posterCandidates(root, id, contentHash, "") {
		p := filepath.Clean(c)
		if !strings.HasPrefix(p, base) {
			continue
		}
		if servePoster(w, r, p) {
			return
		}
	}
	httperror.Write(w, r, httperror.NotFound("no poster for video "+id))
}

// servePoster serves p when it is a non-empty regular file.
func servePoster(w http.ResponseWriter, r *http.Request, p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return false
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, p)
	return true
}
