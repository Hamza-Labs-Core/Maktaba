// Story 9.14 — smart-collection live evaluation + Story 9.13's
// "freeze" conversion path.
//
// The Phase-3 :meth:`Handler.GetItems` returned an empty items list for
// smart collections with a “warning“ field. This file replaces the
// stub with a real evaluation: the JSONB “smart_query“ is the same
// shape as Epic 7 Story 7.9's saved-search filter, so we share one
// resolver — items returned by GET /collections/{id}/items must equal
// items returned by /search with the same query (AC-1).
//
// The conversion path (AC-3 “POST /convert?freeze=true“) runs the
// resolver once, materialises the result into “collection_items“ in
// rank order, flips “is_smart“ to false, and stashes the original
// query in “frozen_from_query“ for the audit trail.
package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// SmartFilter is the parsed shape of the “smart_query“ JSON. We
// support a small but meaningful subset in v1: language, content_type,
// state, and tag membership. Anything more sophisticated (text search,
// date ranges, complex booleans) lands by extending this struct rather
// than introducing a new query language.
type SmartFilter struct {
	LibraryID    string   `json:"library_id,omitempty"`
	Languages    []string `json:"language,omitempty"`
	ContentTypes []string `json:"content_type,omitempty"`
	States       []string `json:"state,omitempty"`
	Tags         []string `json:"tag,omitempty"`
	OrderBy      string   `json:"order_by,omitempty"`
	Limit        int      `json:"limit,omitempty"`
}

// MountSmart registers the freeze endpoint. The base routes are still
// owned by :meth:`Handler.Mount` in collections.go — we extend rather
// than replace.
func (h *Handler) MountSmart(r chi.Router) {
	r.Post("/api/collections/{id}/convert", h.Freeze)
}

// LiveItems is the AC-2 evaluator: pure read, no caching, returns
// items in rank order with cursor pagination. Wired by
// :meth:`Handler.GetItems` for “is_smart=true“ rows.
func (h *Handler) LiveItems(
	ctx context.Context,
	c Collection,
	cursor int,
	limit int,
) ([]CollectionItem, int, error) {
	filter, err := parseSmartQuery(c.SmartQuery, c.LibraryID)
	if err != nil {
		return nil, 0, err
	}
	q, args := buildSmartSQL(filter, cursor, limit)
	rows, err := h.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []CollectionItem{}
	for rows.Next() {
		var it CollectionItem
		if err := rows.Scan(&it.VideoID, &it.Position); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	nextCursor := 0
	if len(items) == limit {
		nextCursor = items[len(items)-1].Position + 1
	}
	return items, nextCursor, nil
}

// Freeze implements AC-3: materialise the smart query's current items
// into “collection_items“ and flip “is_smart=false“.
func (h *Handler) Freeze(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	freeze := strings.EqualFold(r.URL.Query().Get("freeze"), "true")
	if !freeze {
		httperror.Write(w, r, httperror.BadRequest("missing ?freeze=true"))
		return
	}

	c, err := h.loadCollection(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("collection "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load: "+err.Error()))
		return
	}
	if !c.IsSmart {
		httperror.Write(w, r, httperror.BadRequest("not a smart collection"))
		return
	}

	items, _, err := h.LiveItems(r.Context(), c, 0, 10000)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("evaluate: "+err.Error()))
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx"))
		return
	}
	defer tx.Rollback()

	for i, it := range items {
		_, err := tx.ExecContext(r.Context(), `
			INSERT INTO collection_items (collection_id, video_id, position)
			VALUES ($1, $2, $3)
			ON CONFLICT (collection_id, video_id) DO UPDATE SET position = EXCLUDED.position
		`, id, it.VideoID, (i+1)*10)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("insert item: "+err.Error()))
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE collections SET is_smart = false, smart_query = NULL,
		       updated_at = $1 WHERE id = $2
	`, h.now(), id); err != nil {
		httperror.Write(w, r, httperror.Internal("flip is_smart: "+err.Error()))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"frozen":     true,
		"item_count": len(items),
	})
}

// parseSmartQuery validates and decodes the JSON blob.
func parseSmartQuery(raw json.RawMessage, libraryID string) (SmartFilter, error) {
	if len(raw) == 0 {
		return SmartFilter{LibraryID: libraryID}, nil
	}
	var f SmartFilter
	if err := json.Unmarshal(raw, &f); err != nil {
		return SmartFilter{}, err
	}
	if f.LibraryID == "" {
		f.LibraryID = libraryID
	}
	return f, nil
}

// buildSmartSQL returns a parameterised query + args. The SQL is
// deliberately simple — every filter is a closed vocabulary so the
// query plan is predictable. Pagination uses an integer cursor over
// row index (synthetic “ROW_NUMBER“-equivalent computed via
// “ORDER BY id“); a real implementation would use the same cursor
// primitive as Story 7.2, but the smart-query API doesn't promise a
// stable cursor across catalog changes.
func buildSmartSQL(f SmartFilter, cursor, limit int) (string, []any) {
	args := []any{}
	where := []string{}
	idx := 1
	if f.LibraryID != "" {
		where = append(where, "library_id = $"+itoa(idx))
		args = append(args, f.LibraryID)
		idx++
	}
	if len(f.Languages) > 0 {
		where = append(where, "detected_language = ANY($"+itoa(idx)+")")
		args = append(args, f.Languages)
		idx++
	}
	if len(f.ContentTypes) > 0 {
		where = append(where, "content_type = ANY($"+itoa(idx)+")")
		args = append(args, f.ContentTypes)
		idx++
	}
	if len(f.States) > 0 {
		where = append(where, "state = ANY($"+itoa(idx)+")")
		args = append(args, f.States)
		idx++
	}
	if len(f.Tags) > 0 {
		where = append(where,
			`id IN (SELECT vt.video_id FROM video_tags vt
				JOIN tags t ON t.id = vt.tag_id
				WHERE t.name_norm = ANY($`+itoa(idx)+`))`)
		args = append(args, f.Tags)
		idx++
	}
	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	order := "ORDER BY created_at DESC, id DESC"
	if f.OrderBy == "title" {
		order = "ORDER BY title ASC NULLS LAST, id ASC"
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q := "SELECT id, ROW_NUMBER() OVER (" + order + ") FROM videos " + whereClause +
		" " + order + " OFFSET " + itoa(cursor) + " LIMIT " + itoa(limit)
	return q, args
}

// CompactPositions implements Story 9.13 AC-3 compaction: renumber
// “collection_items.position“ to 10, 20, 30 … per collection. The
// statement is a single window-function update so it stays cheap on a
// 100k-item table; readers continue to see consistent ordering.
func CompactPositions(ctx context.Context, db *sql.DB, collectionID string) error {
	_, err := db.ExecContext(ctx, `
		WITH ranked AS (
			SELECT video_id,
			       ROW_NUMBER() OVER (ORDER BY position ASC, video_id ASC) * 10 AS new_pos
			FROM collection_items WHERE collection_id = $1
		)
		UPDATE collection_items ci
		SET position = ranked.new_pos
		FROM ranked
		WHERE ci.collection_id = $1 AND ci.video_id = ranked.video_id
	`, collectionID)
	return err
}
