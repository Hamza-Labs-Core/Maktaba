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
	root := thumbnailRoot()
	for _, p := range posterCandidates(root, id, contentHash, posterPath.String) {
		// The video's own poster_path is the library's; every other candidate
		// is built from the id and hash and must stay inside the cache folder.
		if p != posterPath.String && !withinBase(root, p) {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
			w.Header().Set("Cache-Control", "private, max-age=3600")
			http.ServeFile(w, r, p)
			return
		}
	}
	httperror.Write(w, r, httperror.NotFound("no poster for video "+id))
}

// withinBase reports whether p, cleaned, lies inside base. The id is a parsed
// UUID and content_hash is 64 hex per the schema, so this never rejects a real
// candidate; it is the explicit barrier at the file-system sink.
func withinBase(base, p string) bool {
	if base == "" {
		return false
	}
	b := filepath.Clean(base) + string(filepath.Separator)
	return strings.HasPrefix(filepath.Clean(p), b)
}
