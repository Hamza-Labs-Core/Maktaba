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

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
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

// smallCandidates lists the pipeline's small copies of a video's image (the
// thumb_small stage's 320 px JPEG), best first: the one in its content-hash
// folder, then the one the mediaintel import staged by video id while the
// hash is pending. The pipeline generates each once and keeps it in the
// thumbnail cache; the API only reads them.
func smallCandidates(root, videoID, contentHash string) []string {
	if root == "" {
		return nil
	}
	var out []string
	if contentHash != "" {
		out = append(out, filepath.Join(root, contentHash, "small.jpg"))
	}
	return append(out, filepath.Join(root, "imported", videoID+".jpg"))
}

// posterSize is the ?size= of GET /api/videos/{id}/poster.
type posterSize int

const (
	posterFull posterSize = iota
	posterSmall
)

// parsePosterSize reads ?size=: absent or "full" is the full image, "small"
// the pipeline's small copy (phone grids, Epic 48 G4); anything else is not ok.
func parsePosterSize(r *http.Request) (posterSize, bool) {
	switch r.URL.Query().Get("size") {
	case "", "full":
		return posterFull, true
	case "small":
		return posterSmall, true
	}
	return posterFull, false
}

// Poster implements GET /api/videos/{id}/poster: the library grid's card
// image. ?size=small serves the pipeline's small copy when there is one,
// else the full image. 404 when no image exists yet (the card shows its
// fallback), and 404 for a viewer without the video's library, so the route
// never tells whether another library's video exists.
func (h *Handler) Poster(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	size, ok := parsePosterSize(r)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("size must be small or full"))
		return
	}
	var libraryID, contentHash string
	var posterPath sql.NullString
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT library_id, content_hash, poster_path FROM videos WHERE id = $1`, id,
	).Scan(&libraryID, &contentHash, &posterPath)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return
	}
	// Signed in without the library: the same 404 as a missing video.
	if p := principal.FromContext(r.Context()); p != nil && !CanRead(p, libraryID) {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	if herr := h.canRead(r.Context(), libraryID); herr != nil {
		httperror.Write(w, r, herr)
		return
	}
	root := thumbnailRoot()
	// The small copy first when asked for; the full image is the fallback.
	if size == posterSmall && serveCached(w, r, root, smallCandidates(root, id, contentHash)) {
		return
	}
	// The video's own poster comes from the library, not from the request.
	if servePoster(w, r, posterPath.String) {
		return
	}
	if serveCached(w, r, root, posterCandidates(root, id, contentHash, "")) {
		return
	}
	httperror.Write(w, r, httperror.NotFound("no poster for video "+id))
}

// serveCached serves the first candidate that is a file inside the
// thumbnail cache. Candidates are built from the id and the hash: cleaned,
// and kept inside the cache folder before they reach the file system.
func serveCached(w http.ResponseWriter, r *http.Request, root string, candidates []string) bool {
	if root == "" {
		return false
	}
	base := filepath.Clean(root) + string(filepath.Separator)
	for _, c := range candidates {
		p := filepath.Clean(c)
		if !strings.HasPrefix(p, base) {
			continue
		}
		if servePoster(w, r, p) {
			return true
		}
	}
	return false
}

// servePoster serves p when it is a non-empty regular file. Both sizes are
// cacheable for an hour per user: a phone grid's repeat visit fetches nothing.
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
