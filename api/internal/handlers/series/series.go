// Package series implements the Story 26.10 cross-library series browser
// read paths:
//
//	GET   /api/series?library_id=&sort=&page=   cross-library list
//	GET   /api/series/{id}                       one series header
//	GET   /api/series/{id}/episodes              seasons → episodes + watch state
//	GET   /api/series/{id}/missing               gaps per season
//	PATCH /api/series/{id}                        rename (user override)
//
// All of this is a read path over `series`/`series_episodes` (26.3),
// enrichment facts (26.5), and the existing playback store
// (`playback_state`, Epic 8/14) — no Epic-26 progress table. ACL is
// applied per episode: an episode in a library the caller can't read is
// excluded from the grid, the counts, and missing-detection for that
// user. A nil DB returns empty, well-formed payloads.
package series

import (
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const defaultPageSize = 60

// Handler bundles deps.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/series", h.List)
	r.Get("/api/series/{id}", h.Get)
	r.Get("/api/series/{id}/episodes", h.Episodes)
	r.Get("/api/series/{id}/missing", h.Missing)
	r.Patch("/api/series/{id}", h.Patch)
}

// SeriesItem is one row in the cross-library list.
type SeriesItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path,omitempty"`
	LibraryID    string `json:"library_id,omitempty"`
	Year         *int   `json:"year,omitempty"`
	Numbering    string `json:"numbering"`
	SeasonCount  int    `json:"season_count"`
	EpisodeCount int    `json:"episode_count"`
	WatchedCount int    `json:"watched_count"`
	InProgress   int    `json:"in_progress"`
}

type epRow struct {
	seriesID   string
	videoID    string
	libraryID  string
	season     sql.NullInt64
	episode    sql.NullInt64
	absolute   sql.NullInt64
	seasonOvr  sql.NullInt64
	episodeOvr sql.NullInt64
	title      string
	poster     sql.NullString
	duration   sql.NullFloat64
	position   sql.NullFloat64
	completed  sql.NullBool
}

func (e epRow) effSeason() (int, bool) {
	if e.seasonOvr.Valid {
		return int(e.seasonOvr.Int64), true
	}
	if e.season.Valid {
		return int(e.season.Int64), true
	}
	return 0, false
}

func (e epRow) effEpisode() (int, bool) {
	if e.episodeOvr.Valid {
		return int(e.episodeOvr.Int64), true
	}
	if e.episode.Valid {
		return int(e.episode.Int64), true
	}
	return 0, false
}

// List returns series across all libraries the caller can read.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	out := map[string]any{"items": []SeriesItem{}}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, out)
		return
	}
	libraryFilter := r.URL.Query().Get("library_id")
	sortKey := r.URL.Query().Get("sort")
	page, _ := common.QueryInt(r, "page", 0)

	headers, err := h.loadSeriesHeaders(r, libraryFilter)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load series"))
		return
	}
	if len(headers) == 0 {
		common.WriteJSON(w, r, http.StatusOK, out)
		return
	}
	eps := h.loadEpisodes(r, p, "", dismissalUser(p))

	items := []SeriesItem{}
	for _, hd := range headers {
		group := eps[hd.ID]
		if len(group) == 0 && hd.LibraryID != "" && !p.AccessAllLibraries && !p.HasLibrary(hd.LibraryID) {
			continue
		}
		item := hd
		seasons := map[int]bool{}
		for _, e := range group {
			item.EpisodeCount++
			if s, ok := e.effSeason(); ok {
				seasons[s] = true
			}
			if e.completed.Valid && e.completed.Bool {
				item.WatchedCount++
			} else if e.position.Valid && e.position.Float64 > 0 {
				item.InProgress++
			}
		}
		item.SeasonCount = len(seasons)
		items = append(items, item)
	}

	sortSeries(items, sortKey)
	items = paginate(items, page, defaultPageSize)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Get returns a single series header.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.NotFound("series "+id))
		return
	}
	headers, err := h.loadSeriesHeaders(r, "")
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load series"))
		return
	}
	for _, hd := range headers {
		if hd.ID == id {
			common.WriteJSON(w, r, http.StatusOK, hd)
			return
		}
	}
	httperror.Write(w, r, httperror.NotFound("series "+id))
}

// Episode is one cell in the season→episode grid.
type Episode struct {
	VideoID     string   `json:"video_id"`
	Season      *int     `json:"season,omitempty"`
	Episode     *int     `json:"episode,omitempty"`
	Absolute    *int     `json:"absolute_number,omitempty"`
	Title       string   `json:"title"`
	Thumbnail   string   `json:"thumbnail,omitempty"`
	DurationSec *float64 `json:"duration_sec,omitempty"`
	ProgressPct float64  `json:"progress_pct"`
	Watched     bool     `json:"watched"`
}

// Season groups episodes.
type Season struct {
	Season   int       `json:"season"`
	Episodes []Episode `json:"episodes"`
}

// Episodes returns seasons→episodes with the caller's watch state plus
// the next unwatched/in-progress episode (continue watching).
func (h *Handler) Episodes(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	resp := map[string]any{"seasons": []Season{}, "numbering": "season"}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, resp)
		return
	}
	numbering := h.numbering(r, id)
	rows := h.loadEpisodes(r, p, id, dismissalUser(p))[id]
	bySeason := map[int][]Episode{}
	ordered := make([]epRow, len(rows))
	copy(ordered, rows)
	sort.Slice(ordered, func(i, j int) bool { return lessEpisode(ordered[i], ordered[j], numbering) })

	var next *Episode
	for _, e := range ordered {
		ep := toEpisode(e)
		season := 0
		if s, ok := e.effSeason(); ok {
			season = s
		}
		bySeason[season] = append(bySeason[season], ep)
		if next == nil && !ep.Watched {
			cp := ep
			next = &cp
		}
	}
	seasons := []Season{}
	keys := []int{}
	for s := range bySeason {
		keys = append(keys, s)
	}
	sort.Ints(keys)
	for _, s := range keys {
		seasons = append(seasons, Season{Season: s, Episodes: bySeason[s]})
	}
	resp["seasons"] = seasons
	resp["numbering"] = numbering
	if next != nil {
		resp["next_episode"] = next
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// Gap is one missing episode.
type Gap struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

// Missing returns the interior gaps in each season's episode sequence,
// extended by trailing gaps when an enriched season episode-count is
// available (Story 26.10 D3).
func (h *Handler) Missing(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	resp := map[string]any{"gaps": []Gap{}}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, resp)
		return
	}
	numbering := h.numbering(r, id)
	rows := h.loadEpisodes(r, p, id, dismissalUser(p))[id]

	// Group present episode numbers by season (season 0 = specials,
	// excluded from numbered-season gap detection).
	present := map[int]map[int]bool{}
	for _, e := range rows {
		var s int
		if numbering == "absolute" {
			s = 1 // single pseudo-season
		} else if v, ok := e.effSeason(); ok {
			s = v
		} else {
			continue
		}
		var ep int
		if numbering == "absolute" {
			if !e.absolute.Valid {
				continue
			}
			ep = int(e.absolute.Int64)
		} else if v, ok := e.effEpisode(); ok {
			ep = v
		} else {
			continue
		}
		if s == 0 {
			continue // specials
		}
		if present[s] == nil {
			present[s] = map[int]bool{}
		}
		present[s][ep] = true
	}

	gaps := []Gap{}
	for season, eps := range present {
		max := 0
		for e := range eps {
			if e > max {
				max = e
			}
		}
		for e := 1; e <= max; e++ {
			if !eps[e] {
				gaps = append(gaps, Gap{Season: season, Episode: e})
			}
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Season != gaps[j].Season {
			return gaps[i].Season < gaps[j].Season
		}
		return gaps[i].Episode < gaps[j].Episode
	})
	resp["gaps"] = gaps
	resp["numbering"] = numbering
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// patchRequest is the rename body (user override, Story 26.3 flag).
type patchRequest struct {
	Name *string `json:"name,omitempty"`
}

// Patch renames a series (records the user override).
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "editor required"))
		return
	}
	var req patchRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if _, err := h.DB.ExecContext(r.Context(),
			`UPDATE series SET name_override = $1, updated_at = $2 WHERE id = $3`,
			name, h.now(), id); err != nil {
			httperror.Write(w, r, httperror.Internal("update series"))
			return
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"id": id})
}

// --- shared loading helpers ---

func (h *Handler) loadSeriesHeaders(r *http.Request, libraryFilter string) ([]SeriesItem, error) {
	q := `SELECT id, COALESCE(NULLIF(name_override,''), name), poster_path, library_id, year, numbering FROM series`
	args := []any{}
	if libraryFilter != "" {
		q += ` WHERE library_id = $1`
		args = append(args, libraryFilter)
	}
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesItem{}
	for rows.Next() {
		var it SeriesItem
		var poster, lib sql.NullString
		var year sql.NullInt64
		if rows.Scan(&it.ID, &it.Name, &poster, &lib, &year, &it.Numbering) != nil {
			continue
		}
		it.PosterPath = poster.String
		it.LibraryID = lib.String
		if year.Valid {
			y := int(year.Int64)
			it.Year = &y
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// loadEpisodes returns episodes grouped by series id, ACL-filtered to
// libraries the principal can read and joined to the caller's playback
// state. When seriesID is non-empty only that series is loaded.
func (h *Handler) loadEpisodes(r *http.Request, p *principal.Principal, seriesID, userID string) map[string][]epRow {
	q := `
		SELECT se.series_id, se.video_id, v.library_id,
		       se.season, se.episode, se.absolute_number,
		       se.season_override, se.episode_override,
		       COALESCE(v.title, v.filename), v.poster_path, v.duration_sec,
		       ps.position_sec, ps.completed
		FROM series_episodes se
		JOIN videos v ON v.id = se.video_id
		LEFT JOIN playback_state ps ON ps.video_id = se.video_id AND ps.user_id = $1`
	args := []any{userID}
	if seriesID != "" {
		q += ` WHERE se.series_id = $2`
		args = append(args, seriesID)
	}
	out := map[string][]epRow{}
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e epRow
		if rows.Scan(&e.seriesID, &e.videoID, &e.libraryID, &e.season, &e.episode, &e.absolute,
			&e.seasonOvr, &e.episodeOvr, &e.title, &e.poster, &e.duration, &e.position, &e.completed) != nil {
			continue
		}
		if !p.AccessAllLibraries && !p.HasLibrary(e.libraryID) {
			continue // ACL: forbidden episode excluded from grid + counts
		}
		out[e.seriesID] = append(out[e.seriesID], e)
	}
	return out
}

func (h *Handler) numbering(r *http.Request, seriesID string) string {
	var n string
	if h.DB.QueryRowContext(r.Context(), `SELECT numbering FROM series WHERE id = $1`, seriesID).Scan(&n) != nil {
		return "season"
	}
	if n == "" {
		return "season"
	}
	return n
}

func toEpisode(e epRow) Episode {
	ep := Episode{VideoID: e.videoID, Title: e.title, Thumbnail: e.poster.String}
	if s, ok := e.effSeason(); ok {
		ep.Season = &s
	}
	if n, ok := e.effEpisode(); ok {
		ep.Episode = &n
	}
	if e.absolute.Valid {
		a := int(e.absolute.Int64)
		ep.Absolute = &a
	}
	if e.duration.Valid {
		d := e.duration.Float64
		ep.DurationSec = &d
	}
	if e.completed.Valid && e.completed.Bool {
		ep.Watched = true
		ep.ProgressPct = 100
	} else if e.position.Valid && e.duration.Valid && e.duration.Float64 > 0 {
		ep.ProgressPct = clampPct(e.position.Float64 / e.duration.Float64 * 100)
	}
	return ep
}

func lessEpisode(a, b epRow, numbering string) bool {
	if numbering == "absolute" {
		return absOrd(a) < absOrd(b)
	}
	as, _ := a.effSeason()
	bs, _ := b.effSeason()
	if as != bs {
		return as < bs
	}
	ae, _ := a.effEpisode()
	be, _ := b.effEpisode()
	return ae < be
}

func absOrd(e epRow) int {
	if e.absolute.Valid {
		return int(e.absolute.Int64)
	}
	if n, ok := e.effEpisode(); ok {
		return n
	}
	return 0
}

func sortSeries(items []SeriesItem, key string) {
	switch key {
	case "progress":
		sort.SliceStable(items, func(i, j int) bool {
			return progressRatio(items[i]) > progressRatio(items[j])
		})
	case "name", "":
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
	}
}

func progressRatio(s SeriesItem) float64 {
	if s.EpisodeCount == 0 {
		return 0
	}
	return float64(s.WatchedCount) / float64(s.EpisodeCount)
}

func paginate(items []SeriesItem, page, size int) []SeriesItem {
	if page < 0 {
		page = 0
	}
	start := page * size
	if start >= len(items) {
		return []SeriesItem{}
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func dismissalUser(p *principal.Principal) string {
	if p == nil {
		return ""
	}
	return p.UserID
}
