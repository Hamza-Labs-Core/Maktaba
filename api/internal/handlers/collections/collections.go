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
// Smart collections (“is_smart=true“) compute items live from the
// “smart_query“ JSON; this handler currently returns an empty
// items list for the smart case with a “warning“ field — Epic 9
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
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/libraries"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
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
		if !videos.CanRead(p, c.LibraryID) {
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
	var libExists int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM libraries WHERE id = $1`, req.LibraryID).Scan(&libExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "library_id", Message: "unknown library"}}))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
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
	h.audit(r, p, "collection-created", req.LibraryID, map[string]any{"collection_id": id, "name": req.Name, "is_smart": req.IsSmart})
	common.WriteJSON(w, r, http.StatusCreated, Collection{
		ID: id, LibraryID: req.LibraryID, Name: req.Name,
		IsSmart: req.IsSmart, SmartQuery: smartQ, CreatedAt: now, UpdatedAt: now,
	})
}

// Get returns one collection. A collection in a library the caller
// cannot read is 404, the same as an unknown id.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	c, ok := h.visibleCollection(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusOK, c)
}

// visibleCollection loads the collection and checks the caller may read
// its library (videos.CanRead, the rule videos use). It writes the
// 400/404/500 and returns false when not.
func (h *Handler) visibleCollection(w http.ResponseWriter, r *http.Request, id string) (Collection, bool) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return Collection{}, false
	}
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return Collection{}, false
	}
	c, err := h.loadCollection(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("collection "+id))
			return Collection{}, false
		}
		httperror.Write(w, r, httperror.Internal("load: "+err.Error()))
		return Collection{}, false
	}
	if !videos.CanRead(p, c.LibraryID) {
		httperror.Write(w, r, httperror.NotFound("collection "+id))
		return Collection{}, false
	}
	return c, true
}

// writableCollection is the admin role check (collection writes are
// admin-only, Story 10.13 v1) plus visibleCollection.
func (h *Handler) writableCollection(w http.ResponseWriter, r *http.Request, id string) (Collection, *principal.Principal, bool) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return Collection{}, nil, false
	}
	c, ok := h.visibleCollection(w, r, id)
	return c, p, ok
}

// checkItemsVisible refuses video ids that are malformed, unknown or in a
// library the caller cannot read: one 422 field error each, worded the
// same for unknown and hidden so a hidden video does not leak.
func (h *Handler) checkItemsVisible(r *http.Request, p *principal.Principal, ids []string) *httperror.Error {
	var bad []httperror.FieldError
	for i, vid := range ids {
		field := "items[" + itoa(i) + "].video_id"
		if _, err := uuid.Parse(vid); err != nil {
			bad = append(bad, httperror.FieldError{Field: field, Message: "malformed id"})
			continue
		}
		var lib string
		err := h.DB.QueryRowContext(r.Context(), `SELECT library_id FROM videos WHERE id = $1`, vid).Scan(&lib)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return httperror.Internal("load video")
		}
		if err != nil || !videos.CanRead(p, lib) {
			bad = append(bad, httperror.FieldError{Field: field, Message: "unknown video"})
		}
	}
	if len(bad) > 0 {
		return httperror.Unprocessable(bad)
	}
	return nil
}

// audit writes a best-effort category='library' audit row for a
// collection mutation, targeted at the collection's library.
func (h *Handler) audit(r *http.Request, p *principal.Principal, event, libraryID string, payload map[string]any) {
	libraries.WriteAudit(r.Context(), h.DB, event, libraries.ActorOf(p), libraryID, payload)
}

// Patch updates fields + optionally replaces the items list.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c0, p, ok := h.writableCollection(w, r, id)
	if !ok {
		return
	}
	var req PatchRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Items != nil {
		vids := make([]string, len(req.Items))
		for i, it := range req.Items {
			vids[i] = it.VideoID
		}
		if e := h.checkItemsVisible(r, p, vids); e != nil {
			httperror.Write(w, r, e)
			return
		}
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx"))
		return
	}
	defer func() { _ = tx.Rollback() }()

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
	h.audit(r, p, "collection-patched", c0.LibraryID, map[string]any{
		"collection_id": id, "name": req.Name != nil, "description": req.Description != nil,
		"smart_query": len(req.SmartQuery) > 0, "items": len(req.Items),
	})
	c, _ := h.loadCollection(r, id)
	common.WriteJSON(w, r, http.StatusOK, c)
}

// Delete drops the row + cascade.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, p, ok := h.writableCollection(w, r, id)
	if !ok {
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM collections WHERE id=$1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete"))
		return
	}
	h.audit(r, p, "collection-deleted", c.LibraryID, map[string]any{"collection_id": id, "name": c.Name})
	common.WriteNoContent(w)
}

// CollectionItem is the items[] entry shape.
type CollectionItem struct {
	VideoID  string    `json:"video_id"`
	Position int       `json:"position"`
	AddedAt  time.Time `json:"added_at"`
}

// GetItems returns the (sorted) items for a manual collection. Smart
// collections currently return an empty items list with a warning
// (Epic 9 Story 9.14 owns the live evaluation).
func (h *Handler) GetItems(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, ok := h.visibleCollection(w, r, id)
	if !ok {
		return
	}
	p := principal.FromContext(r.Context())
	if c.IsSmart {
		// Story 9.14 AC-2: live evaluation via the shared resolver.
		// `smart_query` is the same filter shape as a saved search, so
		// the items returned here mirror /search with that query. A
		// malformed `smart_query` is not a 5xx — per the EC it degrades
		// to an empty list with a warning so the UI stays usable.
		cursor, e := common.QueryInt(r, "cursor", 0)
		if e != nil {
			httperror.Write(w, r, e)
			return
		}
		limit, e := common.QueryInt(r, "limit", 100)
		if e != nil {
			httperror.Write(w, r, e)
			return
		}
		items, next, err := h.LiveItemsFor(r.Context(), p, c, cursor, limit)
		if err != nil {
			common.WriteJSON(w, r, http.StatusOK, map[string]any{
				"items":   []CollectionItem{},
				"warning": "smart-collection-query-invalid",
			})
			return
		}
		out := map[string]any{"items": items}
		if next > 0 {
			out["next_cursor"] = next
		}
		common.WriteJSON(w, r, http.StatusOK, out)
		return
	}
	// Only the items whose video sits in a library the caller can read: a
	// collection may hold videos of other libraries.
	q := `SELECT ci.video_id, ci.position, ci.added_at FROM collection_items ci
		JOIN videos v ON v.id = ci.video_id
		WHERE ci.collection_id = $1`
	args := []any{id}
	frag, fargs, none := libraryScope(p, "v.library_id", len(args)+1)
	if none {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": []CollectionItem{}})
		return
	}
	if frag != "" {
		q += " AND " + frag
		args = append(args, fargs...)
	}
	q += " ORDER BY ci.position ASC, ci.video_id ASC"
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
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
	id := chi.URLParam(r, "id")
	c, p, ok := h.writableCollection(w, r, id)
	if !ok {
		return
	}
	var req ItemEntry
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := h.checkItemsVisible(r, p, []string{req.VideoID}); e != nil {
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
	h.audit(r, p, "collection-item-added", c.LibraryID, map[string]any{"collection_id": id, "video_id": req.VideoID})
	common.WriteJSON(w, r, http.StatusOK, req)
}

func (h *Handler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, p, ok := h.writableCollection(w, r, id)
	if !ok {
		return
	}
	vid := chi.URLParam(r, "video_id")
	_, err := h.DB.ExecContext(r.Context(), `
		DELETE FROM collection_items WHERE collection_id=$1 AND video_id=$2
	`, id, vid)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("remove item"))
		return
	}
	h.audit(r, p, "collection-item-removed", c.LibraryID, map[string]any{"collection_id": id, "video_id": vid})
	common.WriteNoContent(w)
}

func (h *Handler) loadCollection(r *http.Request, id string) (Collection, error) {
	row := h.DB.QueryRowContext(r.Context(), `
		SELECT id, library_id, name, description, is_smart, smart_query, created_at, updated_at
		FROM collections WHERE id = $1
	`, id)
	return scanCollection(row)
}

// libraryScope is the SQL fragment that keeps rows to the libraries p can
// read: "" for a caller who reads every library, none=true for a caller
// who reads none. Placeholders start at $next.
func libraryScope(p *principal.Principal, col string, next int) (frag string, args []any, none bool) {
	if p != nil && p.AccessAllLibraries {
		return "", nil, false
	}
	if p == nil || len(p.Libraries) == 0 {
		return "", nil, true
	}
	ph := make([]string, len(p.Libraries))
	for i, l := range p.Libraries {
		ph[i] = "$" + itoa(next+i)
		args = append(args, l)
	}
	return col + " IN (" + strings.Join(ph, ",") + ")", args, false
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
