// Package recommendations implements Story 7.21 + Epic 14 (Stories
// 14.6 / 14.7):
//
//	GET    /api/recommendations?surface=tv-home&limit=20
//	DELETE /api/recommendations/rows/{reason_kind}
//	DELETE /api/recommendations/items/{video_id}
//	POST   /api/recommendations/refresh            (admin)
//
// Rails: continue, for-you, library. Each rail is scoped to the
// caller's user_id; for-you is sourced from the `user_recs` table
// populated by a nightly Pipeline job (Epic 9). Every rail carries a
// stable reason_kind plus reason_args so a 10-foot client can render
// a localized "Because you watched X" reason string (Story 14.6).
//
// "Not interested" dismissals (Story 14.7) are persisted in
// recommendation_dismissals and filtered out of every response so a
// hide survives across devices and sessions. The two DELETE endpoints
// record a row-scope (reason_kind) or item-scope (video_id) hide; the
// admin POST /refresh busts this user's cache so the next GET
// recomputes immediately.
//
// AC caching is a small per-user in-memory map with a TTL (default
// 60 s; the surface key keeps tv-home and web-home independent).
package recommendations

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// Reason kinds. A client maps these to localized strings; the server
// never ships English copy (Story 14.6 AC: localized titles client-side).
const (
	ReasonContinueWatching = "continue_watching"
	ReasonForYou           = "for_you"
	ReasonFromLibrary      = "from_library"
	ReasonNewlyAdded       = "newly_added"
	ReasonEditorPicks      = "editor_picks"
)

// maxRows caps the response at 5 rails and maxItems caps each rail at
// 20 items (Story 14.6 AC: up to 5 rows / 20 items).
const (
	maxRows  = 5
	maxItems = 20
)

// Rail is one section in the response.
type Rail struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	ReasonKind string         `json:"reason_kind"`
	ReasonArgs map[string]any `json:"reason_args,omitempty"`
	Items      []Item         `json:"items"`
}

// Item is one rail entry.
type Item struct {
	VideoID       string     `json:"video_id"`
	PositionSec   *float64   `json:"position_sec,omitempty"`
	DurationSec   *float64   `json:"duration_sec,omitempty"`
	RemainingSec  *float64   `json:"remaining_sec,omitempty"`
	LastWatchedAt *time.Time `json:"last_watched_at,omitempty"`
	PosterURL     string     `json:"poster_url,omitempty"`
	Score         *float64   `json:"score,omitempty"`
}

// Response is the AC-1 envelope.
type Response struct {
	Rails       []Rail    `json:"rails"`
	GeneratedAt time.Time `json:"generated_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	CacheHit    bool      `json:"cache_hit"`
}

// Handler bundles deps.
type Handler struct {
	DB       *sql.DB
	NowFunc  func() time.Time
	CacheTTL time.Duration
	// Settings holds the runtime cache TTL (recs.cache_ttl_sec; nil or
	// unset: CacheTTL, else 60 s).
	Settings *runtimecfg.Reader
	cacheMu  sync.Mutex
	cache    map[string]cachedResponse
}

type cachedResponse struct {
	resp Response
	exp  time.Time
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/recommendations", h.Get)
	r.Delete("/api/recommendations/rows/{reason_kind}", h.DismissRow)
	r.Delete("/api/recommendations/items/{video_id}", h.DismissItem)
	r.Post("/api/recommendations/refresh", h.Refresh)
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
	limit, e := common.QueryInt(r, "limit", maxItems)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > maxItems {
		limit = maxItems
	}

	key := p.UserID + ":" + surface
	if cached, ok := h.cacheLookup(key); ok {
		cached.CacheHit = true
		common.WriteJSON(w, r, http.StatusOK, cached)
		return
	}

	if h.DB == nil {
		now := h.now()
		common.WriteJSON(w, r, http.StatusOK, Response{
			Rails:       []Rail{},
			GeneratedAt: now,
			ExpiresAt:   now.Add(h.ttlFor(r.Context())),
		})
		return
	}

	dismissedRows, dismissedItems := h.loadDismissals(r, p.UserID)

	rails := make([]Rail, 0, maxRows)
	for _, rail := range []Rail{
		h.continueRail(r, p, limit),
		h.forYouRail(r, p, limit),
		h.libraryRail(r, p, limit),
	} {
		if surface == "mobile-home" && rail.ID == "library" {
			continue
		}
		if dismissedRows[rail.ReasonKind] {
			continue
		}
		rail.Items = filterItems(rail.Items, dismissedItems)
		if len(rail.Items) == 0 {
			continue
		}
		if len(rail.Items) > maxItems {
			rail.Items = rail.Items[:maxItems]
		}
		rails = append(rails, rail)
		if len(rails) == maxRows {
			break
		}
	}

	now := h.now()
	resp := Response{
		Rails:       rails,
		GeneratedAt: now,
		ExpiresAt:   now.Add(h.ttlFor(r.Context())),
	}

	h.cacheStore(key, resp)
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// DismissRow records a row-scope "Not interested" (Story 14.7
// DELETE /rows/{reason_kind}). Idempotent; busts this user's cache.
func (h *Handler) DismissRow(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	reasonKind := chi.URLParam(r, "reason_kind")
	if reasonKind == "" {
		httperror.Write(w, r, httperror.BadRequest("reason_kind required"))
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Internal("recommendations store unavailable"))
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO recommendation_dismissals (user_id, reason_kind)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, p.UserID, reasonKind); err != nil {
		httperror.Write(w, r, httperror.Internal("could not record dismissal").Wrap(err))
		return
	}
	h.bustUser(p.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// DismissItem records an item-scope "Not interested" (Story 14.7
// DELETE /items/{video_id}). Idempotent; busts this user's cache.
func (h *Handler) DismissItem(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	videoID := chi.URLParam(r, "video_id")
	if videoID == "" {
		httperror.Write(w, r, httperror.BadRequest("video_id required"))
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Internal("recommendations store unavailable"))
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO recommendation_dismissals (user_id, reason_kind, video_id)
		VALUES ($1, '', $2)
		ON CONFLICT DO NOTHING
	`, p.UserID, videoID); err != nil {
		httperror.Write(w, r, httperror.Internal("could not record dismissal").Wrap(err))
		return
	}
	h.bustUser(p.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// Refresh is the admin recompute trigger (Story 14.7 POST /refresh).
// There is no nightly composer in this build, so "recompute" means
// drop the cache so the next GET rebuilds from live tables.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return
	}
	h.cacheMu.Lock()
	h.cache = map[string]cachedResponse{}
	h.cacheMu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// continueRail sources from playback_state where 0.05 ≤ pos/dur ≤ 0.95.
// Like every rail it keeps to the libraries p can read: a lost library
// grant hides its videos even with playback history left behind.
func (h *Handler) continueRail(r *http.Request, p *principal.Principal, limit int) Rail {
	rail := Rail{
		ID:         "continue",
		Title:      "Continue Watching",
		ReasonKind: ReasonContinueWatching,
		Items:      []Item{},
	}
	args := []any{p.UserID, limit}
	scope, ok := railScope(p, &args)
	if !ok {
		return rail
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT ps.video_id, ps.position_sec, COALESCE(v.duration_sec, 0), ps.updated_at
		FROM playback_state ps
		JOIN videos v ON v.id = ps.video_id
		WHERE ps.user_id = $1
		  AND v.duration_sec IS NOT NULL
		  AND v.duration_sec > 0
		  AND ps.position_sec / v.duration_sec BETWEEN 0.05 AND 0.95
		  AND ps.completed = FALSE`+scope+`
		ORDER BY ps.updated_at DESC, ps.video_id ASC LIMIT $2
	`, args...)
	if err != nil {
		return rail
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var it Item
		var dur, pos float64
		var updated time.Time
		if err := rows.Scan(&it.VideoID, &pos, &dur, &updated); err != nil {
			continue
		}
		if seen[it.VideoID] {
			continue
		}
		seen[it.VideoID] = true
		it.PositionSec = &pos
		it.DurationSec = &dur
		rem := dur - pos
		if rem < 0 {
			rem = 0
		}
		it.RemainingSec = &rem
		it.LastWatchedAt = &updated
		rail.Items = append(rail.Items, it)
	}
	return rail
}

// forYouRail reads from user_recs (Pipeline-populated nightly). Order
// is deterministic: (score DESC, video_id ASC) so two equal-score
// videos always sort the same way (Story 14.7 determinism AC).
func (h *Handler) forYouRail(r *http.Request, p *principal.Principal, limit int) Rail {
	rail := Rail{
		ID:         "for-you",
		Title:      "For You",
		ReasonKind: ReasonForYou,
		Items:      []Item{},
	}
	args := []any{p.UserID, limit}
	scope, ok := railScope(p, &args)
	if !ok {
		return rail
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT ur.video_id, ur.score FROM user_recs ur
		JOIN videos v ON v.id = ur.video_id
		WHERE ur.user_id = $1`+scope+`
		ORDER BY ur.score DESC, ur.video_id ASC LIMIT $2
	`, args...)
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

// railScope is the " AND v.library_id IN (...)" filter for p's readable
// libraries, appending its args after *args ("" for an all-library
// caller; ok=false when p reads no library).
func railScope(p *principal.Principal, args *[]any) (string, bool) {
	if p.AccessAllLibraries {
		return "", true
	}
	if len(p.Libraries) == 0 {
		return "", false
	}
	ph := make([]string, len(p.Libraries))
	for i, lib := range p.Libraries {
		*args = append(*args, lib)
		ph[i] = "$" + strconv.Itoa(len(*args))
	}
	return " AND v.library_id IN (" + strings.Join(ph, ",") + ")", true
}

// libraryRail is a minimal stand-in until Epic 9 Story 9.9 ships
// topics: return the most-recent unwatched videos in the user's
// accessible libraries.
func (h *Handler) libraryRail(r *http.Request, p *principal.Principal, limit int) Rail {
	rail := Rail{
		ID:         "library",
		Title:      "From your library",
		ReasonKind: ReasonFromLibrary,
		Items:      []Item{},
	}
	args := []any{p.UserID}
	scope := ""
	if !p.AccessAllLibraries {
		// Only the libraries the user may read (the video detail rule).
		if len(p.Libraries) == 0 {
			return rail
		}
		ph := make([]string, len(p.Libraries))
		for i, lib := range p.Libraries {
			args = append(args, lib)
			ph[i] = "$" + strconv.Itoa(len(args))
		}
		scope = " AND v.library_id IN (" + strings.Join(ph, ",") + ")"
	}
	args = append(args, limit)
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT v.id FROM videos v
		WHERE v.state = 'ready' AND NOT EXISTS (
		  SELECT 1 FROM playback_state ps WHERE ps.user_id = $1 AND ps.video_id = v.id
		)`+scope+`
		ORDER BY v.updated_at DESC, v.id ASC LIMIT $`+strconv.Itoa(len(args)), args...)
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

// loadDismissals returns the set of dismissed reason_kinds (row scope:
// video_id IS NULL) and dismissed video_ids (item scope) for a user.
func (h *Handler) loadDismissals(r *http.Request, userID string) (rows, items map[string]bool) {
	rows = map[string]bool{}
	items = map[string]bool{}
	res, err := h.DB.QueryContext(r.Context(), `
		SELECT reason_kind, video_id FROM recommendation_dismissals
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return rows, items
	}
	defer res.Close()
	for res.Next() {
		var reasonKind string
		var videoID sql.NullString
		if err := res.Scan(&reasonKind, &videoID); err != nil {
			continue
		}
		if videoID.Valid && videoID.String != "" {
			items[videoID.String] = true
			continue
		}
		rows[reasonKind] = true
	}
	return rows, items
}

func filterItems(in []Item, dismissed map[string]bool) []Item {
	if len(dismissed) == 0 {
		return in
	}
	// Preserve query order (continue=updated_at, for-you=score,
	// library=updated_at); just drop dismissed video_ids.
	out := make([]Item, 0, len(in))
	for _, it := range in {
		if dismissed[it.VideoID] {
			continue
		}
		out = append(out, it)
	}
	return out
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

// cacheStore inserts a fresh response with the configured TTL.
func (h *Handler) cacheStore(key string, resp Response) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.cache == nil {
		h.cache = map[string]cachedResponse{}
	}
	if len(h.cache) > 1024 {
		// Cheap bound: drop the cache when it grows too large.
		h.cache = map[string]cachedResponse{}
	}
	exp := resp.ExpiresAt
	if exp.IsZero() {
		exp = h.now().Add(h.ttl())
	}
	h.cache[key] = cachedResponse{resp: resp, exp: exp}
}

// bustUser drops every surface entry for one user (called after a
// dismissal so the next GET reflects the hide immediately).
func (h *Handler) bustUser(userID string) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	for k := range h.cache {
		if len(k) >= len(userID)+1 && k[:len(userID)+1] == userID+":" {
			delete(h.cache, k)
		}
	}
}

// ttlFor is the cache TTL with the runtime setting recs.cache_ttl_sec over it.
func (h *Handler) ttlFor(ctx context.Context) time.Duration {
	if d, ok := h.Settings.Seconds(ctx, runtimecfg.RecsCacheTTL); ok {
		return d
	}
	return h.ttl()
}

func (h *Handler) ttl() time.Duration {
	if h.CacheTTL > 0 {
		return h.CacheTTL
	}
	return 60 * time.Second
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}
