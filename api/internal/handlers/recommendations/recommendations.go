// Package recommendations implements Story 7.21:
//
//	GET /api/recommendations?surface=tv-home&limit=20
//
// Rails: continue, next-up, for-you, library. Each rail is filtered
// to videos the user can read; for-you is sourced from the
// ``user_recs`` table populated by a nightly Pipeline job (the
// Pipeline-side aggregation is owned by Epic 9).
//
// AC-3 caching is a small per-user in-memory LRU with a 60-s TTL.
package recommendations

import (
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Rail is one section in the response.
type Rail struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Items []Item `json:"items"`
}

// Item is one rail entry.
type Item struct {
	VideoID       string     `json:"video_id"`
	PositionSec   *float64   `json:"position_sec,omitempty"`
	DurationSec   *float64   `json:"duration_sec,omitempty"`
	LastWatchedAt *time.Time `json:"last_watched_at,omitempty"`
	PosterURL     string     `json:"poster_url,omitempty"`
	Score         *float64   `json:"score,omitempty"`
}

// Response is the AC-1 envelope.
type Response struct {
	Rails       []Rail    `json:"rails"`
	GeneratedAt time.Time `json:"generated_at"`
	CacheHit    bool      `json:"cache_hit"`
}

// Handler bundles deps.
type Handler struct {
	DB       *sql.DB
	NowFunc  func() time.Time
	CacheTTL time.Duration
	cacheMu  sync.Mutex
	cache    map[string]cachedResponse
}

type cachedResponse struct {
	resp Response
	exp  time.Time
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/recommendations", h.Get)
}

// Get implements AC-1, AC-3, AC-4.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	surface := r.URL.Query().Get("surface")
	if surface == "" {
		surface = "web-home"
	}
	limit, e := common.QueryInt(r, "limit", 20)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	key := p.UserID + ":" + surface
	if cached, ok := h.cacheLookup(key); ok {
		cached.CacheHit = true
		common.WriteJSON(w, r, http.StatusOK, cached)
		return
	}

	resp := Response{
		Rails:       []Rail{},
		GeneratedAt: h.now(),
	}

	resp.Rails = append(resp.Rails, h.continueRail(r, p.UserID, limit))
	resp.Rails = append(resp.Rails, h.forYouRail(r, p.UserID, limit))
	if surface != "mobile-home" {
		resp.Rails = append(resp.Rails, h.libraryRail(r, p.UserID, limit))
	}

	h.cacheStore(key, resp)
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// continueRail sources from playback_state where 0.05 ≤ pos/dur ≤ 0.95.
func (h *Handler) continueRail(r *http.Request, userID string, limit int) Rail {
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT ps.video_id, ps.position_sec, COALESCE(v.duration_sec, 0), ps.updated_at
		FROM playback_state ps
		JOIN videos v ON v.id = ps.video_id
		WHERE ps.user_id = $1
		  AND v.duration_sec IS NOT NULL
		  AND ps.position_sec / v.duration_sec BETWEEN 0.05 AND 0.95
		  AND ps.completed = FALSE
		ORDER BY ps.updated_at DESC LIMIT $2
	`, userID, limit)
	rail := Rail{ID: "continue", Title: "Continue Watching", Items: []Item{}}
	if err != nil {
		return rail
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		var dur float64
		var updated time.Time
		var pos float64
		if err := rows.Scan(&it.VideoID, &pos, &dur, &updated); err != nil {
			continue
		}
		it.PositionSec = &pos
		it.DurationSec = &dur
		it.LastWatchedAt = &updated
		rail.Items = append(rail.Items, it)
	}
	return rail
}

// forYouRail reads from user_recs (Pipeline-populated nightly).
func (h *Handler) forYouRail(r *http.Request, userID string, limit int) Rail {
	rail := Rail{ID: "for-you", Title: "For You", Items: []Item{}}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT video_id, score FROM user_recs
		WHERE user_id = $1 ORDER BY score DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return rail
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		var score float64
		if err := rows.Scan(&it.VideoID, &score); err != nil {
			continue
		}
		it.Score = &score
		rail.Items = append(rail.Items, it)
	}
	return rail
}

// libraryRail is a minimal stand-in until Epic 9 Story 9.9 ships
// topics: return the most-recent videos in the user's accessible
// libraries that they haven't watched.
func (h *Handler) libraryRail(r *http.Request, userID string, limit int) Rail {
	rail := Rail{ID: "library", Title: "From your library", Items: []Item{}}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT v.id FROM videos v
		WHERE v.state = 'ready' AND NOT EXISTS (
		  SELECT 1 FROM playback_state ps WHERE ps.user_id = $1 AND ps.video_id = v.id
		)
		ORDER BY v.updated_at DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return rail
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.VideoID); err != nil {
			continue
		}
		rail.Items = append(rail.Items, it)
	}
	return rail
}

// cacheLookup returns the cached response if fresh.
func (h *Handler) cacheLookup(key string) (Response, bool) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.cache == nil {
		return Response{}, false
	}
	v, ok := h.cache[key]
	if !ok {
		return Response{}, false
	}
	if h.now().After(v.exp) {
		delete(h.cache, key)
		return Response{}, false
	}
	return v.resp, true
}

// cacheStore inserts a fresh response with the configured TTL (default 60s).
func (h *Handler) cacheStore(key string, resp Response) {
	ttl := h.CacheTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.cache == nil {
		h.cache = map[string]cachedResponse{}
	}
	if len(h.cache) > 1024 {
		// Cheap bound: drop the cache when it grows too large.
		h.cache = map[string]cachedResponse{}
	}
	h.cache[key] = cachedResponse{resp: resp, exp: h.now().Add(ttl)}
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}
