// Package speakers implements the Story 7.14 speaker routes:
//
//	GET   /api/speakers?video_id=...
//	PATCH /api/speakers/{id}        (rename)
//	POST  /api/speakers/merge       (collapse two clusters)
//
// Merge is a single transaction (AC-4): rewrite “segment_speakers“
// rows then drop the duplicate row. Rename validates name uniqueness
// per video.
package speakers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Speaker is the API shape.
type Speaker struct {
	ID           string  `json:"id"`
	VideoID      *string `json:"video_id,omitempty"`
	ClusterLabel *string `json:"cluster_label,omitempty"`
	Name         string  `json:"name"`
}

// MergeRequest is the AC-4 body.
type MergeRequest struct {
	Keep string `json:"keep"`
	Drop string `json:"drop"`
}

// Handler bundles deps.
type Handler struct {
	DB *sql.DB
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/speakers", h.List)
	r.Patch("/api/speakers/{id}", h.Rename)
	r.Post("/api/speakers/merge", h.Merge)
}

// List returns speakers, optionally filtered by ?video_id.
//
// Library visibility (the videos rule): ?video_id of a video the caller
// cannot read is 404; without it, a caller who does not read every
// library sees only the speakers of videos in its libraries.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	full := p.IsAdmin || p.AccessAllLibraries
	q := r.URL.Query()
	args := []any{}
	conds := []string{}
	if v := q.Get("video_id"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			httperror.Write(w, r, httperror.BadRequest("video_id must be a uuid"))
			return
		}
		if !full {
			var lib string
			err := h.DB.QueryRowContext(r.Context(), `SELECT library_id FROM videos WHERE id = $1`, v).Scan(&lib)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
				return
			}
			if err != nil || !p.HasLibrary(lib) {
				httperror.Write(w, r, httperror.NotFound("video "+v))
				return
			}
		}
		args = append(args, v)
		conds = append(conds, "video_id = $"+strconv.Itoa(len(args)))
	}
	if !full {
		if len(p.Libraries) == 0 {
			common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": []Speaker{}})
			return
		}
		ph := make([]string, len(p.Libraries))
		for i, l := range p.Libraries {
			args = append(args, l)
			ph[i] = "$" + strconv.Itoa(len(args))
		}
		conds = append(conds, "video_id IN (SELECT id FROM videos WHERE library_id IN ("+strings.Join(ph, ",")+"))")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := h.DB.QueryContext(r.Context(),
		"SELECT id, video_id, cluster_label, name FROM speakers"+where+" ORDER BY name", args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list speakers").Wrap(err))
		return
	}
	defer rows.Close()
	items := []Speaker{}
	for rows.Next() {
		var s Speaker
		var videoID, cluster sql.NullString
		if err := rows.Scan(&s.ID, &videoID, &cluster, &s.Name); err != nil {
			continue
		}
		if videoID.Valid {
			s.VideoID = &videoID.String
		}
		if cluster.Valid {
			s.ClusterLabel = &cluster.String
		}
		items = append(items, s)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Rename implements PATCH /api/speakers/{id} with name uniqueness check
// (EC: "rename to existing name → 409 speaker-name-exists").
func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		Name string `json:"name"`
	}
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
		return
	}
	_, err := h.DB.ExecContext(r.Context(), `
		UPDATE speakers SET name = $1, updated_at = now() WHERE id = $2
	`, req.Name, id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.dev/problems/speaker-name-exists",
				Title:  "speaker name already exists",
				Status: http.StatusConflict,
				Detail: "merge the speakers instead",
			})
			return
		}
		httperror.Write(w, r, httperror.Internal("rename: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"id": id, "name": req.Name})
}

// Merge implements AC-4: one transaction, rewrite segment_speakers,
// delete the duplicate row, return affected segment count.
func (h *Handler) Merge(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req MergeRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Keep == "" || req.Drop == "" || req.Keep == req.Drop {
		httperror.Write(w, r, httperror.BadRequest("keep + drop required and distinct"))
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(r.Context(), `
		UPDATE segment_speakers SET speaker_id = $1
		WHERE speaker_id = $2
		  AND segment_id NOT IN (
		    SELECT segment_id FROM segment_speakers WHERE speaker_id = $1
		  )
	`, req.Keep, req.Drop)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("merge update: "+err.Error()))
		return
	}
	// Drop any leftover dupes that collided with existing keep links.
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM segment_speakers WHERE speaker_id = $1`, req.Drop); err != nil {
		httperror.Write(w, r, httperror.Internal("merge clean: "+err.Error()))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM speakers WHERE id = $1`, req.Drop); err != nil {
		httperror.Write(w, r, httperror.Internal("merge drop: "+err.Error()))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	n, _ := res.RowsAffected()
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"affected_segments": n})
}
