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
// “smart_query“ JSON (smart.go, Story 9.14).
//
// Epic 45 Phase 3 (story 45.17, the web's Collections): GET
// /api/collections?library= lists one library's collections (404 for one the
// caller may not see), each with its visible videos and up to four of them for
// the poster; a manual collection's items carry their video's card; POST
// …/items also takes {video_ids} (appended at the end); a smart collection may
// be a saved grid filter (smart_query {"filters": {...}}, smart.go).
package collections

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	// On the list: a manual collection's visible videos and the first four
	// of them (its poster mosaic). A smart one's are its query's.
	Videos         *int64   `json:"videos,omitempty"`
	PosterVideoIDs []string `json:"poster_video_ids,omitempty"`
}

// visibleVideo is the predicate on videos v that keeps a video listed (the
// browsing rows' rule): not soft-deleted, not missing, not superseded.
const visibleVideo = `v.deleted_at IS NULL AND v.state NOT IN ('missing', 'superseded')`

// maxItems caps a manual collection's items read at once.
const maxItems = 5000

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
	// ListVideos is GET /api/videos (videos.Handler.List): a saved-filter
	// smart collection's items are that list with the collection's filters.
	ListVideos http.HandlerFunc
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

// List returns the collections the caller may see: every visible library's,
// newest first, or with ?library= that library's, by name (404 for a library
// the caller may not see, the same as one that does not exist; an auto
// collection someone dismissed is left out there).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	lib := r.URL.Query().Get("library")
	q := `SELECT id, library_id, name, description, is_smart, smart_query, created_at, updated_at
		FROM collections ORDER BY created_at DESC`
	var args []any
	if lib != "" {
		if _, err := uuid.Parse(lib); err != nil || !videos.CanRead(p, lib) {
			httperror.Write(w, r, httperror.NotFound("library "+lib))
			return
		}
		q = `SELECT id, library_id, name, description, is_smart, smart_query, created_at, updated_at
			FROM collections WHERE library_id = $1 AND dismissed_at IS NULL ORDER BY name, id`
		args = append(args, lib)
	}
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list collections").Wrap(err))
		return
	}
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
	rows.Close()
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("read collections").Wrap(err))
		return
	}
	if err := h.addCounts(r, p, items); err != nil {
		httperror.Write(w, r, httperror.Internal("count collection items").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// addCounts sets each manual collection's visible videos and poster ids:
// one grouped count and one read of the first items, never a query per
// collection.
func (h *Handler) addCounts(r *http.Request, p *principal.Principal, items []Collection) error {
	var manual []string
	at := map[string]int{}
	for i, c := range items {
		if !c.IsSmart {
			at[c.ID] = i
			manual = append(manual, c.ID)
			var zero int64
			items[i].Videos = &zero
			items[i].PosterVideoIDs = []string{}
		}
	}
	if len(manual) == 0 {
		return nil
	}
	var args []any
	ph := make([]string, len(manual))
	for i, id := range manual {
		args = append(args, id)
		ph[i] = "$" + itoa(i+1)
	}
	cond := "ci.collection_id IN (" + strings.Join(ph, ",") + ") AND " + visibleVideo
	frag, fargs, none := libraryScope(p, "v.library_id", len(args)+1)
	if none {
		return nil
	}
	if frag != "" {
		cond += " AND " + frag
		args = append(args, fargs...)
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT ci.collection_id, ci.video_id FROM collection_items ci
		JOIN videos v ON v.id = ci.video_id WHERE `+cond+`
		ORDER BY ci.collection_id, ci.position, ci.video_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, vid string
		if err := rows.Scan(&cid, &vid); err != nil {
			return err
		}
		c := &items[at[cid]]
		*c.Videos++
		if len(c.PosterVideoIDs) < 4 {
			c.PosterVideoIDs = append(c.PosterVideoIDs, vid)
		}
	}
	return rows.Err()
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
		httperror.Write(w, r, httperror.Internal("load library").Wrap(err))
		return
	}
	if req.IsSmart {
		if e := validateSmartQuery(req.SmartQuery); e != nil {
			httperror.Write(w, r, e)
			return
		}
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
			httperror.Write(w, r, nameExists())
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
	if e := common.ReadJSON(r, &req, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
			return
		}
		req.Name = &trimmed
	}
	if len(req.SmartQuery) > 0 {
		if !c0.IsSmart {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "smart_query", Message: "not a smart collection"}}))
			return
		}
		if e := validateSmartQuery(req.SmartQuery); e != nil {
			httperror.Write(w, r, e)
			return
		}
	}
	if req.Items != nil && c0.IsSmart {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "items", Message: "a smart collection's items are its query's"}}))
		return
	}
	if len(req.Items) > maxItems {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "items", Message: "at most " + itoa(maxItems) + " items"}}))
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
		httperror.Write(w, r, httperror.Internal("tx").Wrap(err))
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
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				httperror.Write(w, r, nameExists())
				return
			}
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
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
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
		httperror.Write(w, r, httperror.Internal("delete").Wrap(err))
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
	// A manual collection's item carries its video's card (story 45.17).
	Video *ItemVideo `json:"video,omitempty"`
}

// ItemVideo is the card of a collection item's video; its picture is
// /api/videos/{id}/poster.
type ItemVideo struct {
	ID          string   `json:"id"`
	Title       *string  `json:"title"`
	Filename    string   `json:"filename"`
	LibraryID   string   `json:"library_id"`
	DurationSec *float64 `json:"duration_sec"`
	State       string   `json:"state"`
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
	if c.IsSmart && h.ListVideos != nil {
		if f, err := parseSmartQuery(c.SmartQuery, c.LibraryID); err == nil && f.Filters != nil {
			h.filterItems(w, r, c, f)
			return
		}
	}
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
	// Only the items whose video sits in a library the caller can read (a
	// collection may hold videos of other libraries) and is not hidden.
	q := `SELECT ci.video_id, ci.position, ci.added_at, v.title, v.filename, v.library_id, v.duration_sec, v.state
		FROM collection_items ci
		JOIN videos v ON v.id = ci.video_id
		WHERE ci.collection_id = $1 AND ` + visibleVideo
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
	q += " ORDER BY ci.position ASC, ci.video_id ASC LIMIT " + itoa(maxItems)
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("items: "+err.Error()))
		return
	}
	defer rows.Close()
	items := []CollectionItem{}
	for rows.Next() {
		var it CollectionItem
		var added nullTime
		var title, filename sql.NullString
		var dur sql.NullFloat64
		vc := ItemVideo{}
		if err := rows.Scan(&it.VideoID, &it.Position, &added, &title, &filename, &vc.LibraryID, &dur, &vc.State); err != nil {
			httperror.Write(w, r, httperror.Internal("scan item").Wrap(err))
			return
		}
		it.AddedAt = added.T
		vc.ID = it.VideoID
		vc.Filename = filename.String
		if title.Valid {
			vc.Title = &title.String
		}
		if dur.Valid {
			vc.DurationSec = &dur.Float64
		}
		it.Video = &vc
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("read items").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// AddItemsRequest is the POST …/items body: one item at a position
// ({video_id, position}), or several appended at the end in their order
// ({video_ids}; the ones already in the collection keep their place).
type AddItemsRequest struct {
	VideoID  string   `json:"video_id,omitempty"`
	Position int      `json:"position,omitempty"`
	VideoIDs []string `json:"video_ids,omitempty"`
}

// AddItem implements POST /api/collections/{id}/items.
func (h *Handler) AddItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, p, ok := h.writableCollection(w, r, id)
	if !ok {
		return
	}
	var req AddItemsRequest
	if e := common.ReadJSON(r, &req, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if c.IsSmart {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "video_ids", Message: "a smart collection's items are its query's"}}))
		return
	}
	if req.VideoIDs != nil {
		h.appendItems(w, r, c, p, req.VideoIDs)
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
	common.WriteJSON(w, r, http.StatusOK, ItemEntry{VideoID: req.VideoID, Position: req.Position})
}

// appendItems adds ids after the collection's last item, in their order,
// skipping repeats and the ones already in it. One unknown or hidden video
// refuses the whole request. Answers {added}.
func (h *Handler) appendItems(w http.ResponseWriter, r *http.Request, c Collection, p *principal.Principal, ids []string) {
	if len(ids) == 0 || len(ids) > 500 {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "video_ids", Message: "1 to 500 videos"}}))
		return
	}
	var uniq []string
	seen := map[string]bool{}
	for _, v := range ids {
		if !seen[v] {
			seen[v] = true
			uniq = append(uniq, v)
		}
	}
	if e := h.checkItemsVisible(r, p, uniq); e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(position) FROM collection_items WHERE collection_id = $1`, c.ID).Scan(&last); err != nil {
		httperror.Write(w, r, httperror.Internal("last position").Wrap(err))
		return
	}
	pos := int(last.Int64)
	added := 0
	for _, vid := range uniq {
		res, err := tx.ExecContext(ctx, `INSERT INTO collection_items (collection_id, video_id, position)
			VALUES ($1, $2, $3) ON CONFLICT (collection_id, video_id) DO NOTHING`, c.ID, vid, pos+10)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("add item").Wrap(err))
			return
		}
		if n, _ := res.RowsAffected(); n > 0 {
			pos += 10
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	h.audit(r, p, "collection-items-added", c.LibraryID, map[string]any{"collection_id": c.ID, "added": added})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"added": added})
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
		httperror.Write(w, r, httperror.Internal("remove item").Wrap(err))
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
	var created, updated nullTime
	if err := rs.Scan(&c.ID, &c.LibraryID, &c.Name, &desc, &c.IsSmart, &sq, &created, &updated); err != nil {
		return Collection{}, err
	}
	c.CreatedAt, c.UpdatedAt = created.T, updated.T
	if desc.Valid {
		c.Description = &desc.String
	}
	if len(sq) > 0 {
		c.SmartQuery = sq
	}
	return c, nil
}

func nameExists() *httperror.Error {
	return &httperror.Error{
		Type:   "https://maktaba.dev/problems/collection-name-exists",
		Title:  "name already exists",
		Status: http.StatusConflict,
	}
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

// nullTime scans a timestamp stored natively (Postgres) or as text (SQLite).
type nullTime struct {
	T     time.Time
	Valid bool
}

func (n *nullTime) Scan(v any) error {
	n.Valid = false
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		n.T, n.Valid = x.UTC(), true
		return nil
	case string:
		return n.parse(x)
	case []byte:
		return n.parse(string(x))
	}
	return fmt.Errorf("nullTime: cannot scan %T", v)
}

func (n *nullTime) parse(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			n.T, n.Valid = t.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("nullTime: bad timestamp %q", s)
}
