package browse

// Epic 45 Phase 2, story 45.10: each user's playlists.
//
//	GET    /api/me/playlists                         the caller's playlists
//	POST   /api/me/playlists                         {name, description?, video_ids?}
//	GET    /api/me/playlists/{id}                    {playlist, items}
//	PATCH  /api/me/playlists/{id}                    {name?, description?}
//	DELETE /api/me/playlists/{id}
//	POST   /api/me/playlists/{id}/items              {video_ids}: appended
//	DELETE /api/me/playlists/{id}/items/{video_id}
//	PUT    /api/me/playlists/{id}/order              {video_ids}: the visible items' order
//
// A playlist is its owner's alone: another user's is 404 (an admin's too). A
// video the owner may not see is 404 when added or removed; an item whose
// library they later lose (or that went missing) stays stored but is never
// shown or counted, and keeps its place after the visible ones on a reorder.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	// MaxPlaylists caps a user's playlists.
	MaxPlaylists = 200
	// MaxPlaylistItems caps a playlist's items.
	MaxPlaylistItems = 5000
	// MaxPlaylistName caps a playlist's name (characters).
	MaxPlaylistName = 200
	// MaxPlaylistDescription caps its description (characters).
	MaxPlaylistDescription = 2000
)

func (h *Handler) mountPlaylists(r chi.Router) {
	r.Get("/api/me/playlists", h.ListPlaylists)
	r.Post("/api/me/playlists", h.CreatePlaylist)
	r.Get("/api/me/playlists/{id}", h.GetPlaylist)
	r.Patch("/api/me/playlists/{id}", h.PatchPlaylist)
	r.Delete("/api/me/playlists/{id}", h.DeletePlaylist)
	r.Post("/api/me/playlists/{id}/items", h.AddPlaylistItems)
	r.Delete("/api/me/playlists/{id}/items/{video_id}", h.RemovePlaylistItem)
	r.Put("/api/me/playlists/{id}/order", h.OrderPlaylist)
}

// PlaylistSummary is a playlist with its visible items' count, length and
// first pictures.
type PlaylistSummary struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Videos         int64     `json:"videos"`
	DurationSec    float64   `json:"duration_sec"`
	PosterVideoIDs []string  `json:"poster_video_ids"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// summaries is the user's playlists (or the one named by only), the newest
// change first, counting only the items the caller may see.
func (h *Handler) summaries(ctx context.Context, uid string, s scope, only string) ([]PlaylistSummary, error) {
	args := []any{uid}
	one := ""
	if only != "" {
		args = append(args, only)
		one = " AND p.id = " + pidx(len(args))
	}
	vis := and(visible, s.cond("v.library_id", &args))
	rows, err := h.DB.QueryContext(ctx, `SELECT p.id, p.name, p.description, p.created_at, p.updated_at,
			COUNT(v.id), COALESCE(SUM(v.duration_sec), 0)
		FROM user_playlists p
		LEFT JOIN user_playlist_items i ON i.playlist_id = p.id
		LEFT JOIN videos v ON v.id = i.video_id AND `+vis+`
		WHERE p.user_id = $1`+one+`
		GROUP BY p.id, p.name, p.description, p.created_at, p.updated_at
		ORDER BY p.updated_at DESC, p.id`, args...)
	if err != nil {
		return nil, err
	}
	out := []PlaylistSummary{}
	idx := map[string]int{}
	for rows.Next() {
		var ps PlaylistSummary
		var created, updated nullTime
		if err := rows.Scan(&ps.ID, &ps.Name, &ps.Description, &created, &updated, &ps.Videos, &ps.DurationSec); err != nil {
			rows.Close()
			return nil, err
		}
		ps.CreatedAt, ps.UpdatedAt, ps.PosterVideoIDs = created.T, updated.T, []string{}
		idx[ps.ID] = len(out)
		out = append(out, ps)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	// the first visible items of each, by position
	args = []any{uid}
	if only != "" {
		args = append(args, only)
	}
	vis = and(visible, s.cond("v.library_id", &args))
	args = append(args, posterCount)
	prow, err := h.DB.QueryContext(ctx, `SELECT playlist_id, video_id FROM (
			SELECT i.playlist_id, i.video_id, ROW_NUMBER() OVER (PARTITION BY i.playlist_id ORDER BY i.position, i.video_id) AS rn
			FROM user_playlists p JOIN user_playlist_items i ON i.playlist_id = p.id
			JOIN videos v ON v.id = i.video_id
			WHERE p.user_id = $1`+one+` AND `+vis+`) x
		WHERE rn <= `+pidx(len(args))+` ORDER BY playlist_id, rn`, args...)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var pid, vid string
		if err := prow.Scan(&pid, &vid); err != nil {
			return nil, err
		}
		if i, ok := idx[pid]; ok {
			out[i].PosterVideoIDs = append(out[i].PosterVideoIDs, vid)
		}
	}
	return out, prow.Err()
}

// ListPlaylists implements GET /api/me/playlists.
func (h *Handler) ListPlaylists(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	uid := userID(p)
	if uid == "" {
		writeJSON(w, r, map[string]any{"items": []PlaylistSummary{}})
		return
	}
	items, err := h.summaries(r.Context(), uid, scopeOf(p), "")
	if err != nil {
		internal(w, r, "playlists", err)
		return
	}
	writeJSON(w, r, map[string]any{"items": items})
}

// PlaylistRequest is the body of POST and PATCH /api/me/playlists.
type PlaylistRequest struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	VideoIDs    []string `json:"video_ids,omitempty"`
}

// checkText trims and checks the name and description a request sets.
func (req *PlaylistRequest) checkText(create bool) []httperror.FieldError {
	var fe []httperror.FieldError
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		req.Name = &n
		if n == "" || utf8.RuneCountInString(n) > MaxPlaylistName {
			fe = append(fe, httperror.FieldError{Field: "name", Message: "1 to " + strconv.Itoa(MaxPlaylistName) + " characters"})
		}
	} else if create {
		fe = append(fe, httperror.FieldError{Field: "name", Message: "required"})
	}
	if req.Description != nil {
		d := strings.TrimSpace(*req.Description)
		req.Description = &d
		if utf8.RuneCountInString(d) > MaxPlaylistDescription {
			fe = append(fe, httperror.FieldError{Field: "description", Message: "at most " + strconv.Itoa(MaxPlaylistDescription) + " characters"})
		}
	}
	return fe
}

// CreatePlaylist implements POST /api/me/playlists: 201 and the playlist.
func (h *Handler) CreatePlaylist(w http.ResponseWriter, r *http.Request) {
	p, uid := writer(w, r)
	if p == nil {
		return
	}
	var req PlaylistRequest
	if e := common.ReadJSON(r, &req, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	fe := req.checkText(true)
	if len(req.VideoIDs) > MaxPlaylistItems {
		fe = append(fe, httperror.FieldError{Field: "video_ids", Message: "at most " + strconv.Itoa(MaxPlaylistItems) + " videos"})
	}
	if len(fe) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fe))
		return
	}
	ids, e := cleanIDs(req.VideoIDs)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	s := scopeOf(p)
	if e, err := h.checkVisible(ctx, s, ids); err != nil {
		internal(w, r, "check videos", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		internal(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := h.lockUser(ctx, tx, uid); err != nil {
		internal(w, r, "lock user", err)
		return
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_playlists WHERE user_id = $1`, uid).Scan(&n); err != nil {
		internal(w, r, "count playlists", err)
		return
	}
	if n >= MaxPlaylists {
		httperror.Write(w, r, httperror.Conflict("", "at most "+strconv.Itoa(MaxPlaylists)+" playlists"))
		return
	}
	id := uuid.NewString()
	desc := ""
	if req.Description != nil {
		desc = *req.Description
	}
	now := h.ts(h.now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_playlists (id, user_id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`, id, uid, *req.Name, desc, now); err != nil {
		internal(w, r, "create playlist", err)
		return
	}
	for i, vid := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_playlist_items (playlist_id, video_id, position, added_at)
			VALUES ($1, $2, $3, $4)`, id, vid, i, now); err != nil {
			internal(w, r, "add item", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		internal(w, r, "commit playlist", err)
		return
	}
	sums, err := h.summaries(ctx, uid, s, id)
	if err != nil || len(sums) != 1 {
		internal(w, r, "read playlist", err)
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, sums[0])
}

// lockUser makes one user's playlist writes run one after the other on
// Postgres (SQLite's write lock already does).
func (h *Handler) lockUser(ctx context.Context, tx *sql.Tx, uid string) error {
	if isSQLite(h.DB) {
		return nil
	}
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, uid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// ownPlaylist answers 404 unless id is a playlist of uid (the same answer
// for a malformed id, an unknown one and another user's).
func (h *Handler) ownPlaylist(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, uid, id string) (*httperror.Error, error) {
	if _, err := uuid.Parse(id); err != nil || uid == "" {
		return httperror.NotFound("playlist " + id), nil
	}
	lock := ""
	if _, ok := q.(*sql.Tx); ok && !isSQLite(h.DB) {
		lock = " FOR UPDATE"
	}
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM user_playlists WHERE id = $1 AND user_id = $2`+lock, id, uid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return httperror.NotFound("playlist " + id), nil
	}
	return nil, err
}

// playlistCards is a playlist's visible items in order, with the caller's
// playback.
func (h *Handler) playlistCards(ctx context.Context, uid string, s scope, id string) ([]Card, error) {
	args := []any{uid, id}
	vis := and(visible, s.cond("v.library_id", &args))
	return h.playbackCards(ctx, `SELECT `+cardColumns+`, ps.position_sec, ps.completed
		FROM user_playlist_items i JOIN videos v ON v.id = i.video_id
		LEFT JOIN playback_state ps ON ps.user_id = $1 AND ps.video_id = v.id
		WHERE i.playlist_id = $2 AND `+vis+`
		ORDER BY i.position, i.video_id`, args)
}

// playlistDoc writes {playlist, items}.
func (h *Handler) playlistDoc(w http.ResponseWriter, r *http.Request, uid string, s scope, id string) {
	ctx := r.Context()
	sums, err := h.summaries(ctx, uid, s, id)
	if err != nil {
		internal(w, r, "read playlist", err)
		return
	}
	if len(sums) != 1 {
		httperror.Write(w, r, httperror.NotFound("playlist "+id))
		return
	}
	items, err := h.playlistCards(ctx, uid, s, id)
	if err != nil {
		internal(w, r, "playlist items", err)
		return
	}
	writeJSON(w, r, map[string]any{"playlist": sums[0], "items": items})
}

// GetPlaylist implements GET /api/me/playlists/{id}.
func (h *Handler) GetPlaylist(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	id, uid := chi.URLParam(r, "id"), userID(p)
	if e, err := h.ownPlaylist(r.Context(), h.DB, uid, id); err != nil {
		internal(w, r, "load playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	h.playlistDoc(w, r, uid, scopeOf(p), id)
}

// PatchPlaylist implements PATCH /api/me/playlists/{id}: the name and the
// description; answers the playlist.
func (h *Handler) PatchPlaylist(w http.ResponseWriter, r *http.Request) {
	p, uid := writer(w, r)
	if p == nil {
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	if e, err := h.ownPlaylist(ctx, h.DB, uid, id); err != nil {
		internal(w, r, "load playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	var req PlaylistRequest
	if e := common.ReadJSON(r, &req, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if fe := req.checkText(false); len(fe) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fe))
		return
	}
	now := h.ts(h.now())
	if req.Name != nil {
		if _, err := h.DB.ExecContext(ctx, `UPDATE user_playlists SET name = $1, updated_at = $2 WHERE id = $3 AND user_id = $4`,
			*req.Name, now, id, uid); err != nil {
			internal(w, r, "rename playlist", err)
			return
		}
	}
	if req.Description != nil {
		if _, err := h.DB.ExecContext(ctx, `UPDATE user_playlists SET description = $1, updated_at = $2 WHERE id = $3 AND user_id = $4`,
			*req.Description, now, id, uid); err != nil {
			internal(w, r, "describe playlist", err)
			return
		}
	}
	sums, err := h.summaries(ctx, uid, scopeOf(p), id)
	if err != nil || len(sums) != 1 {
		internal(w, r, "read playlist", err)
		return
	}
	writeJSON(w, r, sums[0])
}

// DeletePlaylist implements DELETE /api/me/playlists/{id}: 204.
func (h *Handler) DeletePlaylist(w http.ResponseWriter, r *http.Request) {
	p, uid := writer(w, r)
	if p == nil {
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	if e, err := h.ownPlaylist(ctx, h.DB, uid, id); err != nil {
		internal(w, r, "load playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	// the items first: SQLite enforces the cascade only with foreign keys on
	for _, q := range []string{`DELETE FROM user_playlist_items WHERE playlist_id = $1`,
		`DELETE FROM user_playlists WHERE id = $1`} {
		if _, err := h.DB.ExecContext(ctx, q, id); err != nil {
			internal(w, r, "delete playlist", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ItemsRequest is the body of POST .../items and PUT .../order.
type ItemsRequest struct {
	VideoIDs []string `json:"video_ids"`
}

// AddPlaylistItems implements POST /api/me/playlists/{id}/items: the videos
// not already in it go at its end, in the request's order. {added}
func (h *Handler) AddPlaylistItems(w http.ResponseWriter, r *http.Request) {
	p, uid := writer(w, r)
	if p == nil {
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	if e, err := h.ownPlaylist(ctx, h.DB, uid, id); err != nil {
		internal(w, r, "load playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	var req ItemsRequest
	if e := common.ReadJSON(r, &req, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if len(req.VideoIDs) == 0 || len(req.VideoIDs) > MaxPlaylistItems {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "video_ids",
			Message: "1 to " + strconv.Itoa(MaxPlaylistItems) + " videos"}}))
		return
	}
	ids, e := cleanIDs(req.VideoIDs)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e, err := h.checkVisible(ctx, scopeOf(p), ids); err != nil {
		internal(w, r, "check videos", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		internal(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if e, err := h.ownPlaylist(ctx, tx, uid, id); err != nil {
		internal(w, r, "lock playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e) // removed meanwhile
		return
	}
	in := map[string]bool{}
	next := 0
	rows, err := tx.QueryContext(ctx, `SELECT video_id, position FROM user_playlist_items WHERE playlist_id = $1`, id)
	if err != nil {
		internal(w, r, "read items", err)
		return
	}
	for rows.Next() {
		var vid string
		var pos int
		if err := rows.Scan(&vid, &pos); err != nil {
			rows.Close()
			internal(w, r, "scan item", err)
			return
		}
		in[vid] = true
		if pos >= next {
			next = pos + 1
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internal(w, r, "read items", err)
		return
	}
	var add []string
	for _, vid := range ids {
		if !in[vid] {
			add = append(add, vid)
		}
	}
	if len(in)+len(add) > MaxPlaylistItems {
		httperror.Write(w, r, httperror.Conflict("", "a playlist holds at most "+strconv.Itoa(MaxPlaylistItems)+" videos"))
		return
	}
	now := h.ts(h.now())
	for i, vid := range add {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_playlist_items (playlist_id, video_id, position, added_at)
			VALUES ($1, $2, $3, $4)`, id, vid, next+i, now); err != nil {
			internal(w, r, "add item", err)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_playlists SET updated_at = $1 WHERE id = $2`, now, id); err != nil {
		internal(w, r, "touch playlist", err)
		return
	}
	if err := tx.Commit(); err != nil {
		internal(w, r, "commit items", err)
		return
	}
	writeJSON(w, r, map[string]any{"added": len(add)})
}

// RemovePlaylistItem implements DELETE /api/me/playlists/{id}/items/{video_id}:
// 204, or 404 when the video is not one of its visible items.
func (h *Handler) RemovePlaylistItem(w http.ResponseWriter, r *http.Request) {
	p, uid := writer(w, r)
	if p == nil {
		return
	}
	id, vid := chi.URLParam(r, "id"), chi.URLParam(r, "video_id")
	ctx := r.Context()
	if e, err := h.ownPlaylist(ctx, h.DB, uid, id); err != nil {
		internal(w, r, "load playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if _, err := uuid.Parse(vid); err != nil {
		httperror.Write(w, r, httperror.NotFound("video "+vid))
		return
	}
	if e, err := h.checkVisible(ctx, scopeOf(p), []string{vid}); err != nil {
		internal(w, r, "check video", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	res, err := h.DB.ExecContext(ctx, `DELETE FROM user_playlist_items WHERE playlist_id = $1 AND video_id = $2`, id, vid)
	if err != nil {
		internal(w, r, "remove item", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("video "+vid+" in the playlist"))
		return
	}
	if _, err := h.DB.ExecContext(ctx, `UPDATE user_playlists SET updated_at = $1 WHERE id = $2`, h.ts(h.now()), id); err != nil {
		internal(w, r, "touch playlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// OrderPlaylist implements PUT /api/me/playlists/{id}/order: video_ids is
// the new order of the visible items, each exactly once (else 422); the items
// the caller may not see keep their order after them. Answers {playlist, items}.
func (h *Handler) OrderPlaylist(w http.ResponseWriter, r *http.Request) {
	p, uid := writer(w, r)
	if p == nil {
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	if e, err := h.ownPlaylist(ctx, h.DB, uid, id); err != nil {
		internal(w, r, "load playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	var req ItemsRequest
	if e := common.ReadJSON(r, &req, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	s := scopeOf(p)
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		internal(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if e, err := h.ownPlaylist(ctx, tx, uid, id); err != nil {
		internal(w, r, "lock playlist", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e) // removed meanwhile
		return
	}
	args := []any{id}
	vis := and(visible, s.cond("v.library_id", &args))
	rows, err := tx.QueryContext(ctx, `SELECT i.video_id, CASE WHEN v.id IS NULL THEN 0 ELSE 1 END
		FROM user_playlist_items i LEFT JOIN videos v ON v.id = i.video_id AND `+vis+`
		WHERE i.playlist_id = $1 ORDER BY i.position, i.video_id`, args...)
	if err != nil {
		internal(w, r, "read items", err)
		return
	}
	shown := map[string]bool{}
	var hidden []string
	for rows.Next() {
		var vid string
		var vis int
		if err := rows.Scan(&vid, &vis); err != nil {
			rows.Close()
			internal(w, r, "scan item", err)
			return
		}
		if vis == 1 {
			shown[vid] = true
		} else {
			hidden = append(hidden, vid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internal(w, r, "read items", err)
		return
	}
	seen := map[string]bool{}
	ok := len(req.VideoIDs) == len(shown)
	for _, vid := range req.VideoIDs {
		if !shown[vid] || seen[vid] {
			ok = false
			break
		}
		seen[vid] = true
	}
	if !ok {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "video_ids",
			Message: "the playlist's videos, each once"}}))
		return
	}
	for i, vid := range append(append([]string(nil), req.VideoIDs...), hidden...) {
		if _, err := tx.ExecContext(ctx, `UPDATE user_playlist_items SET position = $1 WHERE playlist_id = $2 AND video_id = $3`,
			i, id, vid); err != nil {
			internal(w, r, "order item", err)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_playlists SET updated_at = $1 WHERE id = $2`, h.ts(h.now()), id); err != nil {
		internal(w, r, "touch playlist", err)
		return
	}
	if err := tx.Commit(); err != nil {
		internal(w, r, "commit order", err)
		return
	}
	h.playlistDoc(w, r, uid, s, id)
}
