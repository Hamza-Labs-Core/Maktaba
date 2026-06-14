// Package filler implements the Story 27.10 filler & bumper routes:
//
//	GET    /api/filler/pools            ?channel_id=
//	POST   /api/filler/pools            { name, channel_id? }
//	PATCH  /api/filler/pools/{id}       { name?, channel_id? }
//	DELETE /api/filler/pools/{id}
//	POST   /api/filler/pools/{id}/items { items: [{ video_id, type }] }
//	DELETE /api/filler/items/{id}
//
// A filler item is an ordinary library `videos` row designated as a
// bumper/filler/station_id; its duration is taken from the probed media
// metadata so the scheduler's fit logic (27.2) can pad slots to the
// wall-clock boundary. Pools are tied to a channel; the scheduler reads
// them when generating the schedule.
//
// Mutations are admin-only, mirroring the other CRUD surfaces (tags,
// users); the real ACL authority is the server-side principal check here.
package filler

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Valid item kinds (mirrors the 0085 CHECK constraint).
var validKinds = map[string]bool{"bumper": true, "filler": true, "station_id": true}

// Item is the API shape of a filler item.
type Item struct {
	ID         string `json:"id"`
	PoolID     string `json:"pool_id"`
	VideoID    string `json:"video_id"`
	Type       string `json:"type"`
	Title      string `json:"title,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
}

// Pool is the API shape of a filler pool, with its items nested.
type Pool struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id,omitempty"`
	Name      string `json:"name"`
	Items     []Item `json:"items"`
}

// Handler bundles deps.
type Handler struct {
	DB *sql.DB
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/filler/pools", h.ListPools)
	r.Post("/api/filler/pools", h.CreatePool)
	r.Patch("/api/filler/pools/{id}", h.UpdatePool)
	r.Delete("/api/filler/pools/{id}", h.DeletePool)
	r.Post("/api/filler/pools/{id}/items", h.AddItems)
	r.Delete("/api/filler/items/{id}", h.DeleteItem)
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

// CreatePoolRequest / UpdatePoolRequest are the pool mutation bodies.
type CreatePoolRequest struct {
	Name      string `json:"name"`
	ChannelID string `json:"channel_id"`
}

type UpdatePoolRequest struct {
	Name      *string `json:"name"`
	ChannelID *string `json:"channel_id"`
}

// AddItemsRequest designates one or more videos as filler/bumper.
type AddItemsRequest struct {
	Items []struct {
		VideoID string `json:"video_id"`
		Type    string `json:"type"`
	} `json:"items"`
}

// ListPools returns every pool (optionally scoped to a channel) with its
// items. A nil channel_id query returns all pools.
func (h *Handler) ListPools(w http.ResponseWriter, r *http.Request) {
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))

	var rows *sql.Rows
	var err error
	if channelID != "" {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT id, COALESCE(channel_id::text, ''), name FROM filler_pools WHERE channel_id = $1 ORDER BY name`,
			channelID)
	} else {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT id, COALESCE(channel_id::text, ''), name FROM filler_pools ORDER BY name`)
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list filler pools"))
		return
	}
	defer rows.Close()

	pools := []Pool{}
	byID := map[string]int{}
	for rows.Next() {
		var p Pool
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Name); err == nil {
			p.Items = []Item{}
			byID[p.ID] = len(pools)
			pools = append(pools, p)
		}
	}
	if len(pools) > 0 {
		h.attachItems(r, channelID, pools, byID)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": pools})
}

// attachItems loads the items for the listed pools in one query and folds
// them onto their pool by pool_id, joining `videos` for the title. The
// same channel filter as ListPools is re-applied so the item query matches
// the pool set without threading an array parameter through the driver.
func (h *Handler) attachItems(r *http.Request, channelID string, pools []Pool, byID map[string]int) {
	const base = `
		SELECT fi.id, fi.pool_id, fi.video_id, fi.type, fi.duration_ms,
		       COALESCE(v.title, '')
		FROM filler_items fi
		JOIN filler_pools fp ON fp.id = fi.pool_id
		LEFT JOIN videos v ON v.id = fi.video_id`
	var rows *sql.Rows
	var err error
	if channelID != "" {
		rows, err = h.DB.QueryContext(r.Context(), base+" WHERE fp.channel_id = $1 ORDER BY fi.created_at", channelID)
	} else {
		rows, err = h.DB.QueryContext(r.Context(), base+" ORDER BY fi.created_at")
	}
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.PoolID, &it.VideoID, &it.Type, &it.DurationMS, &it.Title); err != nil {
			continue
		}
		if idx, ok := byID[it.PoolID]; ok {
			pools[idx].Items = append(pools[idx].Items, it)
		}
	}
}

// CreatePool inserts a pool.
func (h *Handler) CreatePool(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var req CreatePoolRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
		return
	}
	id := uuid.NewString()
	var chID any
	if strings.TrimSpace(req.ChannelID) != "" {
		chID = req.ChannelID
	}
	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO filler_pools (id, channel_id, name) VALUES ($1, $2, $3)`,
		id, chID, req.Name); err != nil {
		httperror.Write(w, r, httperror.Internal("create filler pool: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, Pool{ID: id, ChannelID: req.ChannelID, Name: req.Name, Items: []Item{}})
}

// UpdatePool renames a pool or re-homes it to another channel.
func (h *Handler) UpdatePool(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var req UpdatePoolRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
			return
		}
		if _, err := h.DB.ExecContext(r.Context(),
			`UPDATE filler_pools SET name=$1, updated_at=now() WHERE id=$2`, name, id); err != nil {
			httperror.Write(w, r, httperror.Internal("update filler pool"))
			return
		}
	}
	if req.ChannelID != nil {
		var chID any
		if strings.TrimSpace(*req.ChannelID) != "" {
			chID = *req.ChannelID
		}
		if _, err := h.DB.ExecContext(r.Context(),
			`UPDATE filler_pools SET channel_id=$1, updated_at=now() WHERE id=$2`, chID, id); err != nil {
			httperror.Write(w, r, httperror.Internal("update filler pool channel"))
			return
		}
	}
	common.WriteNoContent(w)
}

// DeletePool drops the pool + cascades its items.
func (h *Handler) DeletePool(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM filler_pools WHERE id=$1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete filler pool"))
		return
	}
	common.WriteNoContent(w)
}

// AddItems designates videos as filler/bumper, taking each duration from
// the probed media metadata. Unknown kinds are rejected; an already-present
// (pool, video) pair is a no-op (idempotent).
func (h *Handler) AddItems(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	poolID := chi.URLParam(r, "id")
	var req AddItemsRequest
	if e := common.ReadJSON(r, &req, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if len(req.Items) == 0 {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "items", Message: "required"}}))
		return
	}
	created := []Item{}
	for _, in := range req.Items {
		vid := strings.TrimSpace(in.VideoID)
		kind := strings.TrimSpace(in.Type)
		if kind == "" {
			kind = "filler"
		}
		if vid == "" || !validKinds[kind] {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "items", Message: "video_id required and type must be one of bumper|filler|station_id"}}))
			return
		}
		id := uuid.NewString()
		// duration_ms is sourced from the probed videos.duration_sec when
		// present; the scheduler's fit logic degrades gracefully on null.
		if _, err := h.DB.ExecContext(r.Context(), `
			INSERT INTO filler_items (id, pool_id, video_id, type, duration_ms)
			VALUES ($1, $2, $3, $4,
			        (SELECT (v.duration_sec * 1000)::bigint FROM videos v WHERE v.id = $3 LIMIT 1))
			ON CONFLICT (pool_id, video_id) DO NOTHING`,
			id, poolID, vid, kind); err != nil {
			httperror.Write(w, r, httperror.Internal("add filler item: "+err.Error()))
			return
		}
		created = append(created, Item{ID: id, PoolID: poolID, VideoID: vid, Type: kind})
	}
	common.WriteJSON(w, r, http.StatusCreated, map[string]any{"items": created})
}

// DeleteItem removes a single filler item.
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM filler_items WHERE id=$1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete filler item"))
		return
	}
	common.WriteNoContent(w)
}
