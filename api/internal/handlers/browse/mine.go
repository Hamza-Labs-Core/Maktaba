package browse

// Epic 45 Phase 2: a user's own marks on videos (stories 45.8, 45.9, 45.14).
//
//	POST /api/me/watched     {video_ids, watched}   mark watched / unwatched
//	POST /api/me/favorites   {video_ids, favorite}  add / remove favorites
//	GET  /api/me/videos/{id}                        the caller's state of one video
//
// A user changes only their own rows. A video the caller may not see (another
// library, or none at all) is 404, as one that does not exist; a request that
// names several is refused whole, nothing written.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// MaxMarkIDs caps the videos one mark request names.
const MaxMarkIDs = 500

// writer is the signed-in caller with a user row, or an error written: the
// admin token (no user) reads empty lists but owns nothing to change.
func writer(w http.ResponseWriter, r *http.Request) (*principal.Principal, string) {
	p := caller(w, r)
	if p == nil {
		return nil, ""
	}
	uid := userID(p)
	if uid == "" {
		httperror.Write(w, r, httperror.Forbidden("", "marks, favorites and playlists belong to a user account"))
		return nil, ""
	}
	return p, uid
}

// cleanIDs drops repeats, keeping the first order; a malformed id is 404 (it
// names no video), as is an empty list's absence handled by the caller.
func cleanIDs(ids []string) ([]string, *httperror.Error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return nil, httperror.NotFound("video " + id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// checkVisible answers 404 unless every id is a (not deleted) video in a
// library the caller may see.
func (h *Handler) checkVisible(ctx context.Context, s scope, ids []string) (*httperror.Error, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var args []any
	in := inList(ids, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT id, library_id FROM videos WHERE id IN (`+in+`) AND deleted_at IS NULL`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var id, lib string
		if err := rows.Scan(&id, &lib); err != nil {
			return nil, err
		}
		if s.has(lib) {
			found[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !found[id] {
			return httperror.NotFound("video " + id), nil
		}
	}
	return nil, nil
}

// MarkRequest is the body of POST /api/me/watched and /api/me/favorites.
type MarkRequest struct {
	VideoIDs []string `json:"video_ids"`
	Watched  *bool    `json:"watched,omitempty"`
	Favorite *bool    `json:"favorite,omitempty"`
}

// readMark reads and checks a mark request: 1..500 videos the caller may see
// and the flag named by field.
func (h *Handler) readMark(w http.ResponseWriter, r *http.Request, field string) (*principal.Principal, string, []string, bool, bool) {
	p, uid := writer(w, r)
	if p == nil {
		return nil, "", nil, false, false
	}
	var req MarkRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return nil, "", nil, false, false
	}
	flag := req.Watched
	if field == "favorite" {
		flag = req.Favorite
	}
	var fe []httperror.FieldError
	if flag == nil {
		fe = append(fe, httperror.FieldError{Field: field, Message: "required: true or false"})
	}
	if len(req.VideoIDs) == 0 || len(req.VideoIDs) > MaxMarkIDs {
		fe = append(fe, httperror.FieldError{Field: "video_ids", Message: "1 to " + strconv.Itoa(MaxMarkIDs) + " videos"})
	}
	if len(fe) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fe))
		return nil, "", nil, false, false
	}
	ids, e := cleanIDs(req.VideoIDs)
	if e != nil {
		httperror.Write(w, r, e)
		return nil, "", nil, false, false
	}
	if e, err := h.checkVisible(r.Context(), scopeOf(p), ids); err != nil {
		internal(w, r, "check videos", err)
		return nil, "", nil, false, false
	} else if e != nil {
		httperror.Write(w, r, e)
		return nil, "", nil, false, false
	}
	return p, uid, ids, *flag, true
}

// Watched implements POST /api/me/watched. Watched: finished, from the start
// next time, and now (the video joins Recently Watched). Unwatched: not
// finished, from the start; the time is kept, so it does not jump into
// Recently Watched (a video never played stays without a row).
func (h *Handler) Watched(w http.ResponseWriter, r *http.Request) {
	_, uid, ids, watched, ok := h.readMark(w, r, "watched")
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		internal(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	now := h.ts(h.now())
	for _, id := range ids {
		if watched {
			_, err = tx.ExecContext(ctx, `INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at)
				VALUES ($1, $2, 0, $3, $4)
				ON CONFLICT (user_id, video_id) DO UPDATE SET position_sec = 0, completed = $3, updated_at = $4`,
				uid, id, true, now)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE playback_state SET position_sec = 0, completed = $3
				WHERE user_id = $1 AND video_id = $2`, uid, id, false)
		}
		if err != nil {
			internal(w, r, "mark", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		internal(w, r, "commit marks", err)
		return
	}
	writeJSON(w, r, map[string]any{"updated": len(ids)})
}

// Favorites implements POST /api/me/favorites.
func (h *Handler) Favorites(w http.ResponseWriter, r *http.Request) {
	_, uid, ids, favorite, ok := h.readMark(w, r, "favorite")
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		internal(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	now := h.ts(h.now())
	for _, id := range ids {
		if favorite {
			_, err = tx.ExecContext(ctx, `INSERT INTO user_favorites (user_id, video_id, created_at) VALUES ($1, $2, $3)
				ON CONFLICT (user_id, video_id) DO NOTHING`, uid, id, now)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM user_favorites WHERE user_id = $1 AND video_id = $2`, uid, id)
		}
		if err != nil {
			internal(w, r, "favorite", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		internal(w, r, "commit favorites", err)
		return
	}
	writeJSON(w, r, map[string]any{"updated": len(ids)})
}

// PlaylistRef names a playlist.
type PlaylistRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SeriesName names a series.
type SeriesName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MyVideo is GET /api/me/videos/{id}.
type MyVideo struct {
	VideoID     string        `json:"video_id"`
	PositionSec float64       `json:"position_sec"`
	Completed   bool          `json:"completed"`
	Favorite    bool          `json:"favorite"`
	Playlists   []PlaylistRef `json:"playlists"`
	Series      *SeriesName   `json:"series"`
}

// MyVideo implements GET /api/me/videos/{id}: the caller's own state of a
// video they may see (the video page's buttons and its series' position).
func (h *Handler) MyVideo(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	ctx := r.Context()
	if e, err := h.checkVisible(ctx, scopeOf(p), []string{id}); err != nil {
		internal(w, r, "check video", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	out := MyVideo{VideoID: id, Playlists: []PlaylistRef{}}
	var sid, sname sql.NullString
	err := h.DB.QueryRowContext(ctx, `SELECT s.id, COALESCE(s.name_override, s.name) FROM series_episodes se
		JOIN series s ON s.id = se.series_id WHERE se.video_id = $1 ORDER BY s.id LIMIT 1`, id).Scan(&sid, &sname)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		internal(w, r, "series", err)
		return
	}
	if sid.Valid {
		out.Series = &SeriesName{ID: sid.String, Name: sname.String}
	}
	if uid := userID(p); uid != "" {
		err := h.DB.QueryRowContext(ctx, `SELECT position_sec, completed FROM playback_state WHERE user_id = $1 AND video_id = $2`,
			uid, id).Scan(&out.PositionSec, &out.Completed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			internal(w, r, "playback", err)
			return
		}
		var one int
		err = h.DB.QueryRowContext(ctx, `SELECT 1 FROM user_favorites WHERE user_id = $1 AND video_id = $2`, uid, id).Scan(&one)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			internal(w, r, "favorite", err)
			return
		}
		out.Favorite = err == nil
		rows, err := h.DB.QueryContext(ctx, `SELECT p.id, p.name FROM user_playlists p
			JOIN user_playlist_items i ON i.playlist_id = p.id
			WHERE p.user_id = $1 AND i.video_id = $2 ORDER BY p.name, p.id`, uid, id)
		if err != nil {
			internal(w, r, "playlists", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var pr PlaylistRef
			if err := rows.Scan(&pr.ID, &pr.Name); err != nil {
				internal(w, r, "scan playlist", err)
				return
			}
			out.Playlists = append(out.Playlists, pr)
		}
		if err := rows.Err(); err != nil {
			internal(w, r, "read playlists", err)
			return
		}
	}
	writeJSON(w, r, out)
}

// favoriteCards is the caller's favorites they may still see, newest first.
func (h *Handler) favoriteCards(ctx context.Context, uid string, s scope, n int) ([]Card, error) {
	if uid == "" {
		return []Card{}, nil
	}
	args := []any{uid}
	libCond := libFilter(s, "", &args)
	args = append(args, n)
	return h.playbackCards(ctx, `SELECT `+cardColumns+`, ps.position_sec, ps.completed
		FROM user_favorites f JOIN videos v ON v.id = f.video_id
		LEFT JOIN playback_state ps ON ps.user_id = $1 AND ps.video_id = v.id
		WHERE f.user_id = $1 AND `+and(visible, libCond)+`
		ORDER BY f.created_at DESC, v.id DESC LIMIT `+pidx(len(args)), args)
}

// playbackCards reads cards followed by the caller's position and completed
// (NULL when they have no playback row).
func (h *Handler) playbackCards(ctx context.Context, q string, args []any) ([]Card, error) {
	rows, err := h.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		var pos sql.NullFloat64
		var done sql.NullBool
		c, err := scanCard(rows, &pos, &done)
		if err != nil {
			return nil, err
		}
		if pos.Valid {
			c.PositionSec = &pos.Float64
		}
		if done.Valid {
			c.Completed = &done.Bool
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
