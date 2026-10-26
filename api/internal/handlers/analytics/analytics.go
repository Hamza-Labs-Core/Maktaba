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

// Handler serves the admin analytics page (Story 29.3, rebuilt by Epic 44
// story 44.6), per-video stats (29.5) and the export (29.6). Every admin
// route is admin-gated in-handler via principal.FromContext (the
// channels/p28 convention), before anything is read.
type Handler struct {
	DB     *sql.DB
	Driver string // "postgres" (default) | "sqlite"

	// StaleTimeout defines the live-play window (a play is "now" only if
	// its heartbeat is within this of now). Zero ⇒ watch default.
	StaleTimeout time.Duration

	// SummaryTTL is the summary cache window. Zero ⇒ 30s.
	SummaryTTL time.Duration

	NowFunc func() time.Time

	cache *summaryCache
}

// A viewing in an app or through the tuner is "now" when it was seen within
// tvStale (the front door's tracker writes every 15 s and ends a viewing
// after 60 s without a segment); a remote TV session within remoteStale
// (its controller ends one after 150 s of silence).
const (
	tvStale     = 2 * time.Minute
	remoteStale = 150 * time.Second
)

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
	// Epic 44 story 44.6 — the admin analytics page. Each takes the range
	// (range=today|7d|30d|90d|1y|all, or from=&to=) and the filters
	// (library, user, client_app, surface).
	r.Get("/api/admin/analytics/summary", h.SummaryEndpoint)
	r.Get("/api/admin/analytics/now", h.Now)
	r.Get("/api/admin/analytics/activity", h.Activity)
	r.Get("/api/admin/analytics/top-videos", h.TopVideos)
	r.Get("/api/admin/analytics/top-channels", h.TopChannels)
	r.Get("/api/admin/analytics/libraries", h.Libraries)
	r.Get("/api/admin/analytics/genres", h.Genres)
	r.Get("/api/admin/analytics/people", h.People)
	r.Get("/api/admin/analytics/apps", h.Apps)
	r.Get("/api/admin/analytics/remote-tv", h.RemoteTV)

	// Story 29.6 — export (plays and viewings).
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

// begin is every admin route's start: the admin check, then the filters.
func (h *Handler) begin(w http.ResponseWriter, r *http.Request) (Filters, bool) {
	if requireAdmin(w, r) == nil {
		return Filters{}, false
	}
	f, e := ParseFilters(r, h.now())
	if e != nil {
		httperror.Write(w, r, e)
		return Filters{}, false
	}
	return f, true
}

// limit reads ?limit= (default def, at most hi).
func limit(w http.ResponseWriter, r *http.Request, def, hi int) (int, bool) {
	n, e := common.QueryInt(r, "limit", def)
	if e != nil {
		httperror.Write(w, r, e)
		return 0, false
	}
	return clamp(n, def, hi), true
}

func window(f Filters) Window {
	w := Window{Range: f.Range.Label}
	if f.Range.HasLowerBound() {
		w.From = f.Range.Start.UTC().Format(time.RFC3339)
	}
	if f.Range.HasUpperBound() {
		w.To = f.Range.End.UTC().Format(time.RFC3339)
	}
	return w
}

// SummaryEndpoint implements GET /api/admin/analytics/summary: watch time,
// plays, viewings, people and Live TV hours, by surface, app, platform and
// device. Cached per filters for SummaryTTL unless refresh=true.
func (h *Handler) SummaryEndpoint(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	now := h.now()
	key := f.key()
	if r.URL.Query().Get("refresh") != "true" {
		if cached, ok := h.summaries().get(key, now); ok {
			common.WriteJSON(w, r, http.StatusOK, cached)
			return
		}
	}

	rp := h.repo()
	s := Summary{Window: window(f)}
	if err := rp.summaryTotals(r.Context(), f, &s); err != nil {
		httperror.Write(w, r, httperror.Internal("summary totals").Wrap(err))
		return
	}
	var err error
	if s.Surfaces, err = rp.surfaces(r.Context(), f, allSurfaces); err != nil {
		httperror.Write(w, r, httperror.Internal("surface breakdown").Wrap(err))
		return
	}
	if s.Apps, err = rp.breakdown(r.Context(), f, "app"); err != nil {
		httperror.Write(w, r, httperror.Internal("app breakdown").Wrap(err))
		return
	}
	if s.Platforms, err = rp.breakdown(r.Context(), f, "platform"); err != nil {
		httperror.Write(w, r, httperror.Internal("platform breakdown").Wrap(err))
		return
	}
	if s.Devices, err = rp.breakdown(r.Context(), f, "device"); err != nil {
		httperror.Write(w, r, httperror.Internal("device breakdown").Wrap(err))
		return
	}
	h.summaries().put(key, s, now)
	common.WriteJSON(w, r, http.StatusOK, s)
}

// Now implements GET /api/admin/analytics/now: the plays with a fresh
// heartbeat and the open viewings seen lately, with their app and device.
// The range does not apply (it is now); the other filters do.
func (h *Handler) Now(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	now := h.now()
	rp := h.repo()
	plays, err := rp.nowPlays(r.Context(), f, now.Add(-h.staleTimeout()), now)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load plays now").Wrap(err))
		return
	}
	views, err := rp.nowViewings(r.Context(), f, now.Add(-tvStale), now.Add(-remoteStale), now)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load viewings now").Wrap(err))
		return
	}
	counts := map[string]int64{}
	for _, s := range allSurfaces {
		counts[s] = 0
	}
	counts[SurfaceVideo] = int64(len(plays))
	for _, v := range views {
		counts[v.Surface]++
	}
	common.WriteJSON(w, r, http.StatusOK, NowResponse{
		Plays: plays, Viewings: views, Counts: counts, At: now.UTC().Format(time.RFC3339),
	})
}

// TopVideos implements GET /api/admin/analytics/top-videos?limit=.
func (h *Handler) TopVideos(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	n, ok := limit(w, r, 10, 100)
	if !ok {
		return
	}
	items, err := h.repo().topVideos(r.Context(), f, n)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("top videos").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"videos": items, "range": f.Range.Label})
}

// TopChannels implements GET /api/admin/analytics/top-channels?limit=: the
// most watched channels with their surfaces and peak viewers.
func (h *Handler) TopChannels(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	n, ok := limit(w, r, 10, 100)
	if !ok {
		return
	}
	rp := h.repo()
	out, err := rp.topChannels(r.Context(), f, n)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("top channels").Wrap(err))
		return
	}
	if out.Surfaces, err = rp.surfaces(r.Context(), f, allSurfaces[1:]); err != nil {
		httperror.Write(w, r, httperror.Internal("channel surfaces").Wrap(err))
		return
	}
	out.Window = window(f)
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Libraries implements GET /api/admin/analytics/libraries.
func (h *Handler) Libraries(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	items, err := h.repo().libraries(r.Context(), f)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("library breakdown").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"libraries": items, "range": f.Range.Label})
}

// Genres implements GET /api/admin/analytics/genres?limit=.
func (h *Handler) Genres(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	n, ok := limit(w, r, 12, 100)
	if !ok {
		return
	}
	items, err := h.repo().genres(r.Context(), f, n)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("genre breakdown").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"genres": items, "range": f.Range.Label})
}

// Activity implements GET /api/admin/analytics/activity?bucket=hour|day|week|month:
// the trend of videos and TV and the day x hour heatmap.
func (h *Handler) Activity(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	bucket := validBucket(r.URL.Query().Get("bucket"), f.Range, h.now())
	rp := h.repo()
	series, err := rp.series(r.Context(), f, bucket)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("activity series").Wrap(err))
		return
	}
	cells, err := rp.heatCells(r.Context(), f)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("activity heatmap").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, ActivityResponse{
		Window: window(f), Bucket: bucket, Series: series, Heatmap: BuildHeatmap(cells),
	})
}

// People implements GET /api/admin/analytics/people?limit=: per person,
// watch time, plays, channels, apps used and when last seen.
func (h *Handler) People(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	n, ok := limit(w, r, 50, 500)
	if !ok {
		return
	}
	people, err := h.repo().people(r.Context(), f, n)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("people").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"people": people, "range": f.Range.Label})
}

// Apps implements GET /api/admin/analytics/apps: per app and platform, the
// API's requests, errors, speed, people and versions; the daily trend; the
// platforms and devices watched on.
func (h *Handler) Apps(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	rp := h.repo()
	out := AppsResponse{Window: window(f)}
	var err error
	if out.Apps, out.Trend, err = rp.apps(r.Context(), f); err != nil {
		httperror.Write(w, r, httperror.Internal("app usage").Wrap(err))
		return
	}
	if out.Platforms, err = rp.breakdown(r.Context(), f, "platform"); err != nil {
		httperror.Write(w, r, httperror.Internal("platform breakdown").Wrap(err))
		return
	}
	if out.Devices, err = rp.breakdown(r.Context(), f, "device"); err != nil {
		httperror.Write(w, r, httperror.Internal("device breakdown").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// RemoteTV implements GET /api/admin/analytics/remote-tv: the remote TV's
// minutes delivered and cost per day (tv_remote_usage_daily: the home's
// whole remote TV, no person or app in it, so only the range applies), and
// its viewings in the range (every filter applies).
func (h *Handler) RemoteTV(w http.ResponseWriter, r *http.Request) {
	f, ok := h.begin(w, r)
	if !ok {
		return
	}
	rp := h.repo()
	out := RemoteTVResponse{Window: window(f)}
	var err error
	if out.Days, err = rp.remoteDays(r.Context(), f); err != nil {
		httperror.Write(w, r, httperror.Internal("remote TV usage").Wrap(err))
		return
	}
	for _, d := range out.Days {
		out.MinutesDelivered += d.MinutesDelivered
		out.SimulcastMinutes += d.SimulcastMinutes
		out.CostUSD += d.CostUSD
	}
	// The remote TV's own viewings, unless the filters leave it out.
	if f.Surface == "" || f.Surface == SurfaceRemote {
		rf := f
		rf.Surface = SurfaceRemote
		var s Summary
		if err := rp.summaryTotals(r.Context(), rf, &s); err != nil {
			httperror.Write(w, r, httperror.Internal("remote TV viewings").Wrap(err))
			return
		}
		out.Viewings, out.WatchSec, out.People = s.Viewings, s.TVSec, s.People
	}
	common.WriteJSON(w, r, http.StatusOK, out)
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
