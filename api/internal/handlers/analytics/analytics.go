package analytics

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/watch"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Handler serves the admin analytics dashboard (Story 29.3), per-video
// stats (29.5) and the export (29.6). Every route is admin-gated
// in-handler via principal.FromContext (the channels/p28 convention).
type Handler struct {
	DB     *sql.DB
	Driver string // "postgres" (default) | "sqlite"

	// StaleTimeout defines the live-session window (a session is "live"
	// only if its heartbeat is within this of now). Zero ⇒ watch default.
	StaleTimeout time.Duration

	// SummaryTTL is the summary cache window. Zero ⇒ 30s.
	SummaryTTL time.Duration

	NowFunc func() time.Time

	cache *summaryCache
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

func (h *Handler) repo() *repo { return newRepo(h.DB, h.Driver) }

func (h *Handler) staleTimeout() time.Duration {
	if h.StaleTimeout > 0 {
		return h.StaleTimeout
	}
	return watch.DefaultStaleTimeout
}

func (h *Handler) summaries() *summaryCache {
	if h.cache == nil {
		h.cache = newSummaryCache(h.SummaryTTL)
	}
	return h.cache
}

// Mount wires the analytics routes.
func (h *Handler) Mount(r chi.Router) {
	// Story 29.3 — admin dashboard.
	r.Get("/api/admin/analytics/live", h.Live)
	r.Get("/api/admin/analytics/summary", h.SummaryEndpoint)
	r.Get("/api/admin/analytics/top-videos", h.TopVideos)
	r.Get("/api/admin/analytics/activity", h.Activity)
	r.Get("/api/admin/analytics/users", h.Users)

	// Story 29.6 — export.
	r.Get("/api/admin/analytics/export", h.Export)

	// Story 29.5 — per-video stats (authenticated; admin gets the
	// per-user breakdown).
	r.Get("/api/videos/{id}/stats", h.VideoStats)
}

// requireAdmin returns the principal iff it is an admin, else writes 403.
func requireAdmin(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return nil
	}
	return p
}

// Live implements GET /api/admin/analytics/live.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	now := h.now()
	sessions, err := h.repo().live(r.Context(), now.Add(-h.staleTimeout()), now)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load live sessions"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"sessions": sessions})
}

// SummaryEndpoint implements GET /api/admin/analytics/summary?range=.
func (h *Handler) SummaryEndpoint(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	now := h.now()
	rng := ParseRange(r.URL.Query().Get("range"), now)
	refresh := r.URL.Query().Get("refresh") == "true"

	if !refresh {
		if cached, ok := h.summaries().get(rng.Label, now); ok {
			common.WriteJSON(w, r, http.StatusOK, cached)
			return
		}
	}

	rp := h.repo()
	s := Summary{Range: rng.Label}
	var err error
	if s.TotalWatchSec, s.TotalSessions, s.UniqueViewers, s.CompletionRate, err = rp.summaryTotals(r.Context(), rng); err != nil {
		httperror.Write(w, r, httperror.Internal("summary totals"))
		return
	}
	if s.Devices, err = rp.breakdown(r.Context(), rng, "device_type"); err != nil {
		httperror.Write(w, r, httperror.Internal("device breakdown"))
		return
	}
	if s.Platforms, err = rp.breakdown(r.Context(), rng, "platform"); err != nil {
		httperror.Write(w, r, httperror.Internal("platform breakdown"))
		return
	}
	if s.Libraries, err = rp.libraries(r.Context(), rng); err != nil {
		httperror.Write(w, r, httperror.Internal("library breakdown"))
		return
	}
	if s.Genres, err = rp.genres(r.Context(), rng, 12); err != nil {
		httperror.Write(w, r, httperror.Internal("genre breakdown"))
		return
	}
	h.summaries().put(rng.Label, s, now)
	common.WriteJSON(w, r, http.StatusOK, s)
}

// TopVideos implements GET /api/admin/analytics/top-videos?range=&limit=.
func (h *Handler) TopVideos(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	limit, e := common.QueryInt(r, "limit", 10)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	limit = clamp(limit, 10, 100)
	rng := ParseRange(r.URL.Query().Get("range"), h.now())
	items, err := h.repo().topVideos(r.Context(), rng, limit)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("top videos"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"videos": items, "range": rng.Label})
}

// Activity implements GET /api/admin/analytics/activity?range=&bucket=.
func (h *Handler) Activity(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	rng := ParseRange(r.URL.Query().Get("range"), h.now())
	bucket := validBucket(r.URL.Query().Get("bucket"))
	rp := h.repo()
	series, err := rp.series(r.Context(), rng, bucket)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("activity series"))
		return
	}
	cells, err := rp.heatCells(r.Context(), rng)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("activity heatmap"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, ActivityResponse{
		Bucket: bucket, Series: series, Heatmap: BuildHeatmap(cells),
	})
}

// Users implements GET /api/admin/analytics/users?range=&limit=.
func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	limit, e := common.QueryInt(r, "limit", 10)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	limit = clamp(limit, 10, 100)
	rng := ParseRange(r.URL.Query().Get("range"), h.now())
	users, err := h.repo().activeUsers(r.Context(), rng, limit)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("active users"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"users": users, "range": rng.Label})
}

func clamp(v, def, hi int) int {
	if v <= 0 {
		return def
	}
	if v > hi {
		return hi
	}
	return v
}
