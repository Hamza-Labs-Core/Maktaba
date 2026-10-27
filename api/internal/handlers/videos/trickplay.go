package videos

// Epic 45 Phase 3 (story 45.19): scrub thumbnails (trickplay).
//
//	GET /api/videos/{id}/trickplay         -> Trickplay (404: no sheet)
//	GET /api/videos/{id}/trickplay/sprite  -> the sheet (image/jpeg)
//
// The pipeline's thumbnail stage makes one sprite sheet per video (a grid of
// frames sampled evenly over the whole video) and, since this story, its
// index sprite.json beside it: the grid and the seconds between two tiles. A
// sheet made before has no index: it is the old 5x5 grid over the duration.
// The tile's size is the sheet's size over the grid. The sheet is found as
// the poster is: the video's sprite_path, else the thumbnail cache's folder of
// its content hash. Who may read is the poster's rule.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg" // the sheets are JPEG
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// legacyGrid is the grid of the sheets made before the index existed.
const legacyGrid = 5

// Trickplay is a video's scrub-preview sheet: tile i (row-major) shows the
// video at i*IntervalSec; the sheet is SpriteURL.
type Trickplay struct {
	SpriteURL   string  `json:"sprite_url"`
	Columns     int     `json:"columns"`
	Rows        int     `json:"rows"`
	Count       int     `json:"count"`
	IntervalSec float64 `json:"interval_sec"`
	TileWidth   int     `json:"tile_width"`
	TileHeight  int     `json:"tile_height"`
	SheetWidth  int     `json:"sheet_width"`
	SheetHeight int     `json:"sheet_height"`
}

type spriteIndex struct {
	Version     int     `json:"version"`
	Columns     int     `json:"columns"`
	Rows        int     `json:"rows"`
	Count       int     `json:"count"`
	IntervalSec float64 `json:"interval_sec"`
}

// spriteOf finds the video's sheet: its path and its duration (for a sheet
// with no index). It writes the 400/403/404/500 and returns "" when there is
// none to serve.
func (h *Handler) spriteOf(w http.ResponseWriter, r *http.Request) (string, float64) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return "", 0
	}
	var libraryID string
	var contentHash, spritePath sql.NullString
	var duration sql.NullFloat64
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT library_id, content_hash, sprite_path, duration_sec FROM videos WHERE id = $1`, id,
	).Scan(&libraryID, &contentHash, &spritePath, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return "", 0
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return "", 0
	}
	if herr := h.canRead(r.Context(), libraryID); herr != nil {
		httperror.Write(w, r, herr)
		return "", 0
	}
	if isFile(spritePath.String) {
		return spritePath.String, duration.Float64
	}
	// the cache's candidate is built from the hash: cleaned and kept inside
	// the cache folder before it reaches the file system
	if root := thumbnailRoot(); root != "" && contentHash.String != "" {
		base := filepath.Clean(root) + string(filepath.Separator)
		p := filepath.Clean(filepath.Join(root, contentHash.String, "sprite.jpg"))
		if strings.HasPrefix(p, base) && isFile(p) {
			return p, duration.Float64
		}
	}
	httperror.Write(w, r, httperror.NotFound("no scrub thumbnails for video "+id))
	return "", 0
}

func isFile(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// TrickplayIndex implements GET /api/videos/{id}/trickplay.
func (h *Handler) TrickplayIndex(w http.ResponseWriter, r *http.Request) {
	sprite, duration := h.spriteOf(w, r)
	if sprite == "" {
		return
	}
	idx := spriteIndex{Columns: legacyGrid, Rows: legacyGrid, Count: legacyGrid * legacyGrid}
	if raw, err := os.ReadFile(strings.TrimSuffix(sprite, filepath.Ext(sprite)) + ".json"); err == nil {
		var got spriteIndex
		if json.Unmarshal(raw, &got) == nil && got.Version == 1 && got.Columns > 0 && got.Rows > 0 && got.IntervalSec > 0 {
			idx = got
			if idx.Count <= 0 || idx.Count > idx.Columns*idx.Rows {
				idx.Count = idx.Columns * idx.Rows
			}
		}
	}
	if idx.IntervalSec <= 0 {
		if duration <= 0 {
			// an old sheet of a video of unknown length: no time for any tile
			httperror.Write(w, r, httperror.NotFound("no scrub thumbnails for video "+chi.URLParam(r, "id")))
			return
		}
		idx.IntervalSec = duration / float64(idx.Count)
	}
	f, err := os.Open(sprite)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("no scrub thumbnails"))
		return
	}
	cfg, _, err := image.DecodeConfig(f)
	_ = f.Close()
	if err != nil || cfg.Width < idx.Columns || cfg.Height < idx.Rows {
		httperror.Write(w, r, httperror.NotFound("no scrub thumbnails"))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	common.WriteJSON(w, r, http.StatusOK, Trickplay{
		SpriteURL:   "/api/videos/" + chi.URLParam(r, "id") + "/trickplay/sprite",
		Columns:     idx.Columns,
		Rows:        idx.Rows,
		Count:       idx.Count,
		IntervalSec: idx.IntervalSec,
		TileWidth:   cfg.Width / idx.Columns,
		TileHeight:  cfg.Height / idx.Rows,
		SheetWidth:  cfg.Width,
		SheetHeight: cfg.Height,
	})
}

// TrickplaySprite implements GET /api/videos/{id}/trickplay/sprite.
func (h *Handler) TrickplaySprite(w http.ResponseWriter, r *http.Request) {
	sprite, _ := h.spriteOf(w, r)
	if sprite == "" {
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, sprite)
}
