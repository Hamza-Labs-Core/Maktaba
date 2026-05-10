// Package collections implements Story 7.14 collection routes:
//
//	GET    /api/collections
//	POST   /api/collections
//	GET    /api/collections/{id}
//	PATCH  /api/collections/{id}
//	DELETE /api/collections/{id}
//	GET    /api/collections/{id}/items
//	POST   /api/collections/{id}/items
//	DELETE /api/collections/{id}/items/{video_id}
//
// Smart collections (``is_smart=true``) compute items live from the
// ``smart_query`` JSON; this handler currently returns an empty
// items list for the smart case with a ``warning`` field — Epic 9
// Story 9.14 fills in the real evaluation.
package collections

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Collection is the over-the-wire shape.
type Collection struct {
	ID          string          `json:"id"`
	LibraryID   string          `json:"library_id"`
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	IsSmart     bool            `json:"is_smart"`
	SmartQuery  json.RawMessage `json:"smart_query,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateRequest is the POST body.
type CreateRequest struct {
	LibraryID   string          `json:"library_id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	IsSmart     bool            `json:"is_smart,omitempty"`
	SmartQuery  json.RawMessage `json:"smart_query,omitempty"`
}

// PatchRequest is the PATCH body — all optional.
type PatchRequest struct {
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	SmartQuery  json.RawMessage `json:"smart_query,omitempty"`
	Items       []ItemEntry     `json:"items,omitempty"`
}

// ItemEntry is the replace-all PATCH semantic for manual collections.
type ItemEntry struct {
	VideoID  string `json:"video_id"`
	Position int    `json:"position"`
}

// Handler bundles deps.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/collections", h.List)
	r.Post("/api/collections", h.Create)
	r.Get("/api/collections/{id}", h.Get)
	r.Patch("/api/collections/{id}", h.Patch)
	r.Delete("/api/collections/{id}", h.Delete)
	r.Get("/api/collections/{id}/items", h.GetItems)
	r.Post("/api/collections/{id}/items", h.AddItem)
	r.Delete("/api/collections/{id}/items/{video_id}", h.RemoveItem)
}

// List returns all collections accessible to the principal.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, library_id, name, description, is_smart, smart_query, created_at, updated_at
		FROM collections ORDER BY created_at DESC
	`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list collections"))
		return
	}
	defer rows.Close()
	items := []Collection{}
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			continue
		}
		if !p.AccessAllLibraries && !p.HasLibrary(c.LibraryID) {
			continue
		}
		items = append(items, c)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Create inserts a new collection.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req CreateRequest
	if e := common.ReadJSON(r, &req, 32<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
		return
	}
	if _, err := uuid.Parse(req.LibraryID); err != nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "library_id", Message: "required"}}))
		return
	}
	id := uuid.NewString()
	now := h.now()
	smartQ := req.SmartQuery
	if !req.IsSmart {
		smartQ = nil
	}
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO collections (id, library_id, name, description, is_smart, smart_query, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
	`, id, req.LibraryID, req.Name, sqlNullString(req.Description), req.IsSmart, optionalJSON(smartQ), p.UserID, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.dev/problems/collection-name-exists",
				Title:  "name already exists",
				Status: http.StatusConflict,
			})
			return
		}
		httperror.Write(w, r, httperror.Internal("create: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, Collection{
		ID: id, LibraryID: req.LibraryID, Name: req.Name,
		IsSmart: req.IsSmart, SmartQuery: smartQ, CreatedAt: now, UpdatedAt: now,
	})
}

// Get returns one collection.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := h.loadCollection(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("collection "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, c)
}

// Patch updates fields + optionally replaces the items list.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	var req PatchRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx"))
		return
	}
	defer tx.Rollback()

	sets := []string{}
	args := []any{}
	idx := 1
	if req.Name != nil {
		sets = append(sets, "name = $"+itoa(idx))
		args = append(args, *req.Name)
		idx++
	}
	if req.Description != nil {
		sets = append(sets, "description = $"+itoa(idx))
		args = append(args, *req.Description)
		idx++
	}
	if len(req.SmartQuery) > 0 {
		sets = append(sets, "smart_query = $"+itoa(idx))
		args = append(args, string(req.SmartQuery))
		idx++
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = $"+itoa(idx))
		args = append(args, h.now())
		idx++
		args = append(args, id)
		q := "UPDATE collections SET " + strings.Join(sets, ", ") + " WHERE id = $" + itoa(idx)
		if _, err := tx.ExecContext(r.Context(), q, args...); err != nil {
			httperror.Write(w, r, httperror.Internal("update: "+err.Error()))
			return
		}
	}
	if req.Items != nil {
		// Replace-all items semantic.
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM collection_items WHERE collection_id = $1`, id); err != nil {
			httperror.Write(w, r, httperror.Internal("clear items: "+err.Error()))
			return
		}
		for _, it := range req.Items {
			_, err := tx.ExecContext(r.Context(), `
				INSERT INTO collection_items (collection_id, video_id, position)
				VALUES ($1, $2, $3)
				ON CONFLICT (collection_id, video_id) DO UPDATE SET position = EXCLUDED.position
			`, id, it.VideoID, it.Position)
			if err != nil {
				httperror.Write(w, r, httperror.Internal("insert item: "+err.Error()))
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit"))
		return
	}
	c, _ := h.loadCollection(r, id)
	common.WriteJSON(w, r, http.StatusOK, c)
}

// Delete drops the row + cascade.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM collections WHERE id=$1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete"))
		return
	}
	common.WriteNoContent(w)
}

// CollectionItem is the items[] entry shape.
type CollectionItem struct {
	VideoID  string `json:"video_id"`
	Position int    `json:"position"`
	AddedAt  time.Time `json:"added_at"`
}

// GetItems returns the (sorted) items for a manual collection. Smart
// collections currently return an empty items list with a warning
// (Epic 9 Story 9.14 owns the live evaluation).
func (h *Handler) GetItems(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := h.loadCollection(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("collection "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load: "+err.Error()))
		return
	}
	if c.IsSmart {
		// Stub for now per EC ("invalid smart_query → 200 with items: [], warning").
		common.WriteJSON(w, r, http.StatusOK, map[string]any{
			"items":   []CollectionItem{},
			"warning": "smart-collection-evaluation-not-yet-implemented",
		})
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT video_id, position, added_at FROM collection_items
		WHERE collection_id=$1 ORDER BY position ASC, video_id ASC
	`, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("items: "+err.Error()))
		return
	}
	defer rows.Close()
	items := []CollectionItem{}
	for rows.Next() {
		var it CollectionItem
		if err := rows.Scan(&it.VideoID, &it.Position, &it.AddedAt); err != nil {
			continue
		}
		items = append(items, it)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// AddItem implements single-item POST.
func (h *Handler) AddItem(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	var req ItemEntry
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO collection_items (collection_id, video_id, position)
		VALUES ($1, $2, $3)
		ON CONFLICT (collection_id, video_id) DO UPDATE SET position = EXCLUDED.position
	`, id, req.VideoID, req.Position)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("add item: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, req)
}

func (h *Handler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	vid := chi.URLParam(r, "video_id")
	_, err := h.DB.ExecContext(r.Context(), `
		DELETE FROM collection_items WHERE collection_id=$1 AND video_id=$2
	`, id, vid)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("remove item"))
		return
	}
	common.WriteNoContent(w)
}

func (h *Handler) loadCollection(r *http.Request, id string) (Collection, error) {
	row := h.DB.QueryRowContext(r.Context(), `
		SELECT id, library_id, name, description, is_smart, smart_query, created_at, updated_at
		FROM collections WHERE id = $1
	`, id)
	return scanCollection(row)
}

type rowScanner interface {
	Scan(...any) error
}

func scanCollection(rs rowScanner) (Collection, error) {
	var c Collection
	var desc sql.NullString
	var sq []byte
	if err := rs.Scan(&c.ID, &c.LibraryID, &c.Name, &desc, &c.IsSmart, &sq, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Collection{}, err
	}
	if desc.Valid {
		c.Description = &desc.String
	}
	if len(sq) > 0 {
		c.SmartQuery = sq
	}
	return c, nil
}

func sqlNullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func optionalJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
