// Package tags implements the Story 7.14 tag routes:
//
//	GET    /api/tags
//	POST   /api/tags
//	PATCH  /api/videos/{id}/tags  (add/remove delta)
//	DELETE /api/tags/{id}
//
// Tag uniqueness is name_norm = NFC-normalised + lowercased, so
// “Tafsir“ and “tafsir“ collapse to one row. Display preserves
// the original casing.
package tags

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Tag is the API shape.
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PatchVideoTagsRequest is the AC-3 delta shape.
type PatchVideoTagsRequest struct {
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}

// Handler bundles deps.
type Handler struct {
	DB *sql.DB
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/tags", h.List)
	r.Post("/api/tags", h.Create)
	r.Delete("/api/tags/{id}", h.Delete)
	r.Patch("/api/videos/{id}/tags", h.PatchVideoTags)
}

// List returns every tag.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, name FROM tags ORDER BY name`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list tags"))
		return
	}
	defer rows.Close()
	items := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err == nil {
			items = append(items, t)
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Create is a no-op idempotent upsert. Returns the existing or new row.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req Tag
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
		return
	}
	id, name, err := h.upsertTag(r, req.Name)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("upsert tag: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, Tag{ID: id, Name: name})
}

// Delete drops the row + cascades video_tags.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM tags WHERE id=$1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete tag"))
		return
	}
	common.WriteNoContent(w)
}

// PatchVideoTags implements the AC-3 delta semantic: “add“ ∪ existing
// minus “remove“. Order-independent.
func (h *Handler) PatchVideoTags(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	videoID := chi.URLParam(r, "id")
	var req PatchVideoTagsRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx"))
		return
	}
	defer tx.Rollback()

	for _, name := range req.Add {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		tagID, _, err := upsertTagTx(tx, r, name)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("upsert tag: "+err.Error()))
			return
		}
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO video_tags (video_id, tag_id) VALUES ($1, $2)
			ON CONFLICT (video_id, tag_id) DO NOTHING
		`, videoID, tagID); err != nil {
			httperror.Write(w, r, httperror.Internal("attach tag: "+err.Error()))
			return
		}
	}
	for _, name := range req.Remove {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		norm := NormaliseTagName(name)
		if _, err := tx.ExecContext(r.Context(), `
			DELETE FROM video_tags WHERE video_id = $1
			  AND tag_id IN (SELECT id FROM tags WHERE name_norm = $2)
		`, videoID, norm); err != nil {
			httperror.Write(w, r, httperror.Internal("detach tag: "+err.Error()))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit"))
		return
	}

	// Read back current tag set.
	rows, _ := h.DB.QueryContext(r.Context(), `
		SELECT t.id, t.name FROM tags t
		JOIN video_tags vt ON vt.tag_id = t.id
		WHERE vt.video_id = $1 ORDER BY t.name
	`, videoID)
	items := []Tag{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t Tag
			if err := rows.Scan(&t.ID, &t.Name); err == nil {
				items = append(items, t)
			}
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// upsertTag inserts the tag if missing and returns (id, canonical_name).
func (h *Handler) upsertTag(r *http.Request, name string) (string, string, error) {
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	id, n, err := upsertTagTx(tx, r, name)
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return id, n, nil
}

func upsertTagTx(tx *sql.Tx, r *http.Request, name string) (string, string, error) {
	id := uuid.NewString()
	nameNorm := NormaliseTagName(name)
	_, err := tx.ExecContext(r.Context(), `
		INSERT INTO tags (id, name, name_norm) VALUES ($1, $2, $3)
		ON CONFLICT (name_norm) DO NOTHING
	`, id, name, nameNorm)
	if err != nil {
		return "", "", err
	}
	var realID, realName string
	if err := tx.QueryRowContext(r.Context(), `SELECT id, name FROM tags WHERE name_norm = $1`, nameNorm).Scan(&realID, &realName); err != nil {
		return "", "", err
	}
	return realID, realName, nil
}

// NormaliseTagName is the uniqueness key for tag dedup: NFC + casefold +
// strip leading/trailing whitespace.
func NormaliseTagName(s string) string {
	s = strings.TrimSpace(s)
	s = norm.NFC.String(s)
	return strings.ToLower(s)
}
