// Package search implements Stories 7.8 and 7.9 and the per-video results:
//
//	POST /api/search                 — hybrid / FTS / semantic search, saved as a search_id
//	GET  /api/search/results/{sid}   — a page of the saved "In titles" / "In transcripts" list
//	GET  /api/search/passages        — every matching passage of one video of a saved search
//	GET  /api/search/recent          — the caller's recent searches (DELETE: one, or all)
//	GET  /api/search/suggest         — typeahead (Story 7.8 AC-4)
//	POST /api/search/save            — Story 7.9 save
//	GET  /api/search/saved           — Story 7.9 list
//	DEL  /api/search/saved/{id}      — Story 7.9 delete
//
// A search finds the videos whose title matches and the videos whose active
// transcript matches, one entry per video with its true match count (FTS: every
// matching segment, counted in SQL; semantic: the gRPC SemanticClient's top-K
// passages grouped per video; hybrid: the two video rankings fused with
// Reciprocal-Rank-Fusion, k=60). The ordered result is kept in search_runs under
// an opaque search_id, so paging never reorders it (results.go, runs.go). When
// SemanticClient is nil the API still serves the FTS path so single-binary
// deployments without Pipeline are usable.
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// DefaultSemanticBudget is the hard per-request wall-clock budget for the
// Embed+Chroma round-trip (Story 18.2 AC4 / HLB-333). On breach the
// semantic leg is abandoned, the request degrades to FTS-only, and the
// response carries degraded:true. Tunable via the Handler field so the
// budget stays a single source of truth (no magic numbers in callers).
const DefaultSemanticBudget = 200 * time.Millisecond

// SemanticClient is the gRPC-backed embed-then-Chroma path. Story 7.18
// owns the concrete implementation; this package only consumes the
// interface so tests can stub it.
type SemanticClient interface {
	// Search runs an embed + Chroma top-K and returns hits keyed by
	// transcript_segments.id with a similarity score.
	Search(ctx context.Context, q string, k int, filters Filters) ([]Hit, error)
}

// Filters is the AC-3 filter shape.
type Filters struct {
	Language    []string  `json:"language,omitempty"`
	LibraryID   []string  `json:"library_id,omitempty"`
	Speaker     []string  `json:"speaker,omitempty"`
	DurationSec *RangeF   `json:"duration_sec,omitempty"`
	Date        *RangeStr `json:"date,omitempty"`
}

// RangeF is a numeric “{gte, lte}“ range.
type RangeF struct {
	Gte *float64 `json:"gte,omitempty"`
	Lte *float64 `json:"lte,omitempty"`
}

// RangeStr is a date range (RFC3339 strings) with “{gte, lte}“.
type RangeStr struct {
	Gte string `json:"gte,omitempty"`
	Lte string `json:"lte,omitempty"`
}

// Request is the POST /api/search body.
type Request struct {
	Q       string  `json:"q"`
	Mode    string  `json:"mode,omitempty"` // hybrid (default), fts, semantic
	Limit   int     `json:"limit,omitempty"`
	Filters Filters `json:"filters,omitempty"`

	// Include opts into augmentation blocks (Story 26.8). Today the only
	// value is "youtube"; absent ⇒ the response is byte-identical to the
	// pre-26.8 shape (no YouTube key, no `youtube` field).
	Include []string `json:"include,omitempty"`

	// View is the Search page's view, "grid" or "list": it picks the page
	// size (rules.search.page_size_grid / page_size_list).
	View string `json:"view,omitempty"`

	// Save false runs the search without keeping it (no search_id, not in
	// Recent searches): a lookup such as the YouTube import dialog's.
	Save *bool `json:"save,omitempty"`
}

// save reports whether the search is kept under a search_id.
func (r Request) save() bool { return r.Save == nil || *r.Save }

// includes reports whether the request opted into the named block.
func (r Request) includes(name string) bool {
	for _, v := range r.Include {
		if v == name {
			return true
		}
	}
	return false
}

// Hit is a single segment-level result; the API maps unit→segment per
// Story 7.8 AC-6. It is also a search's deprecated flat `hits` list (kept
// one release for the GraphQL bridge and older clients; the Search page
// reads transcript_videos).
type Hit struct {
	SegmentID int64   `json:"segment_id"`
	VideoID   string  `json:"video_id"`
	StartSec  float64 `json:"start_sec"`
	EndSec    float64 `json:"end_sec"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
}

// ResponseT carries the per-source latency breakdown.
type ResponseT struct {
	FTS      int64 `json:"fts"`
	Semantic int64 `json:"semantic"`
	Fusion   int64 `json:"fusion"`
}

// Handler bundles deps.
type Handler struct {
	DB       *sql.DB
	Semantic SemanticClient // optional
	NowFunc  func() time.Time

	// EmbedCache memoises semantic results keyed by the normalised
	// query + filters + k (Story 18.2 AC2 / HLB-333). Identical
	// repeated queries skip the Embed+Chroma round-trip entirely.
	// Reuses the orphaned generic perf.Cache (HLB-346) — no second
	// cache implementation. Nil disables caching (behaviour unchanged).
	EmbedCache *perf.Cache[[]Hit]

	// SemanticBudget is the hard per-request deadline for the semantic
	// leg. Zero falls back to DefaultSemanticBudget. On breach the
	// request degrades to FTS-only with degraded:true.
	SemanticBudget time.Duration

	// Logger records degradation events (deadline breach / backend
	// error) so operators see a metric-able breadcrumb instead of the
	// previously-silent `semHits, _ = ...` swallow. Nil → slog default.
	Logger *slog.Logger

	// YouTube is the optional "From YouTube" search adapter (Story 26.8).
	// Nil ⇒ the feature is off: include=youtube yields an empty block
	// with reason "disabled" and never blocks or alters local results.
	YouTube YouTubeSearcher

	// Settings holds the runtime fusion weights (search.fts_weight,
	// search.semantic_weight; nil or unset: 1 each, plain RRF).
	Settings *runtimecfg.Reader
}

// semanticBudget returns the effective per-request semantic deadline.
func (h *Handler) semanticBudget() time.Duration {
	if h.SemanticBudget > 0 {
		return h.SemanticBudget
	}
	return DefaultSemanticBudget
}

// log returns the effective logger (never nil).
func (h *Handler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// embedKey builds the cache key for a semantic query. The filters are
// part of the key because they change the Chroma result set.
func embedKey(q string, k int, f Filters) string {
	b, _ := json.Marshal(struct {
		Q string  `json:"q"`
		K int     `json:"k"`
		F Filters `json:"f"`
	}{strings.ToLower(strings.TrimSpace(q)), k, f})
	return string(b)
}

// runSemantic executes the semantic leg under a hard deadline and an
// in-process result cache. It never returns an error: on any failure
// (cache miss + backend error, or deadline breach) it returns nil hits
// and degraded=true so the caller can fall back to FTS-only. This is
// the single place HLB-333's cache + deadline + degraded triad lives.
func (h *Handler) runSemantic(ctx context.Context, q string, k int, f Filters) (hits []Hit, degraded bool) {
	if h.Semantic == nil {
		return nil, false
	}
	key := embedKey(q, k, f)
	if h.EmbedCache != nil {
		if v, ok := h.EmbedCache.Get(key); ok {
			// Defensive copy: perf.Cache.Get returns the live backing
			// slice (no internal copy). The caller mutates Hit.Snippet
			// in place (highlightSnippet), so handing back the alias
			// would corrupt the cached entry on every hit (progressive
			// nested <mark>) and race when two same-key requests run
			// concurrently. Hit has only scalar/string fields, so a
			// shallow copy is sufficient.
			return append([]Hit(nil), v...), false
		}
	}
	cctx, cancel := context.WithTimeout(ctx, h.semanticBudget())
	defer cancel()
	res, err := h.Semantic.Search(cctx, q, k, f)
	if err != nil {
		// Deadline breach or backend error: log + continue FTS-only
		// (Story 18.2 AC4). Previously this was silently swallowed.
		h.log().WarnContext(ctx, "search: semantic leg degraded; serving FTS-only",
			"event", "search_semantic_degraded",
			"budget_ms", h.semanticBudget().Milliseconds(),
			"err", err)
		return nil, true
	}
	if h.EmbedCache != nil {
		// Store a copy so retaining `res` after Put (and mutating it
		// downstream) cannot alias and corrupt the live cache entry.
		cp := append([]Hit(nil), res...)
		h.EmbedCache.Put(key, cp)
	}
	return res, false
}

// Mount wires the search routes.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/search", h.Search)
	r.Get("/api/search/results/{sid}", h.Results)
	r.Get("/api/search/passages", h.Passages)
	r.Get("/api/search/recent", h.Recent)
	r.Delete("/api/search/recent", h.ClearRecent)
	r.Delete("/api/search/recent/{sid}", h.DeleteRecent)
	r.Get("/api/search/suggest", h.Suggest)
	r.Post("/api/search/save", h.SaveSearch)
	r.Get("/api/search/saved", h.ListSaved)
	r.Delete("/api/search/saved/{id}", h.DeleteSaved)
	// Story 26.8 — also expose YouTube search as a GET proxy and the
	// import endpoint that copies a YouTube match onto a local video.
	r.Get("/api/search/youtube", h.SearchYouTube)
	r.Get("/api/search/youtube/status", h.YouTubeStatus)
	r.Post("/api/videos/{id}/import-youtube", h.ImportYouTube)
}

// Suggest implements AC-4: prefix lookup on the search_history corpus,
// up to 10 results, sourced from the lowercased+unaccented column.
//
// Suggestions come from the caller's own history only: another user's
// searches never leak through the prefix lookup.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"suggestions": []string{}})
		return
	}
	prefix := normaliseSuggestQuery(q)
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT query FROM search_history
		WHERE query_norm LIKE $1 AND user_id = $2
		ORDER BY hits DESC, last_used_at DESC LIMIT 10
	`, prefix+"%", p.UserID)
	if err != nil {
		// No history table or empty — degrade gracefully.
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"suggestions": []string{}})
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			out = append(out, s)
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"suggestions": out})
}

// normaliseSuggestQuery applies a simple lowercase + diacritic-strip so
// the prefix LIKE matches the “query_norm“ column.
func normaliseSuggestQuery(s string) string {
	var b strings.Builder
	for _, r := range s {
		r = unicode.ToLower(r)
		// Drop combining marks (NFD-style).
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SaveSearch implements Story 7.9 AC-1.
type SaveRequest struct {
	Name  string          `json:"name"`
	Query json.RawMessage `json:"query"`
}

func (h *Handler) SaveSearch(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req SaveRequest
	if e := common.ReadJSON(r, &req, 32<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
		return
	}
	if len(req.Query) == 0 {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "query", Message: "required"}}))
		return
	}
	id := uuid.NewString()
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO saved_searches (id, user_id, name, query)
		VALUES ($1, $2, $3, $4)
	`, id, p.UserID, req.Name, string(req.Query))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.tv/problems/saved-search-name-exists",
				Title:  "name already exists",
				Status: http.StatusConflict,
				Detail: req.Name + " is already saved",
			})
			return
		}
		httperror.Write(w, r, httperror.Internal("save").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, map[string]any{"id": id, "name": req.Name})
}

// SavedSearch is the over-the-wire shape.
type SavedSearch struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Query     json.RawMessage `json:"query"`
	CreatedAt time.Time       `json:"created_at"`
}

func (h *Handler) ListSaved(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, name, query, created_at FROM saved_searches
		WHERE user_id = $1 ORDER BY created_at DESC
	`, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list saved").Wrap(err))
		return
	}
	defer rows.Close()
	items := []SavedSearch{}
	for rows.Next() {
		var s SavedSearch
		var q []byte
		if err := rows.Scan(&s.ID, &s.Name, &q, &s.CreatedAt); err != nil {
			continue
		}
		s.Query = q
		items = append(items, s)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) DeleteSaved(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	res, err := h.DB.ExecContext(r.Context(), `
		DELETE FROM saved_searches WHERE id = $1 AND user_id = $2
	`, id, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("delete saved").Wrap(err))
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httperror.Write(w, r, httperror.NotFound("saved search "+id))
		return
	}
	common.WriteNoContent(w)
}

// RRFuse fuses two ranked lists by Reciprocal Rank Fusion (k=60 by
// default). Order preserved is sum of 1/(k+rank) across sources.
// Story 7.8 AC-1.
func RRFuse(a, b []Hit, k int) []Hit { return RRFuseWeighted(a, b, k, 1, 1) }

// weights are the runtime fusion weights of the FTS and semantic lists
// (search.fts_weight / search.semantic_weight; unset: 1 each).
func (h *Handler) weights(ctx context.Context) (float64, float64) {
	wFTS, wSem := 1.0, 1.0
	if f, ok := h.Settings.Float(ctx, runtimecfg.SearchFTSWeight); ok {
		wFTS = f
	}
	if f, ok := h.Settings.Float(ctx, runtimecfg.SearchSemanticWeight); ok {
		wSem = f
	}
	return wFTS, wSem
}

// fuse is the hybrid ranking: RRF of the FTS and semantic lists with the
// runtime weights.
func (h *Handler) fuse(ctx context.Context, fts, sem []Hit) []Hit {
	wFTS, wSem := h.weights(ctx)
	return RRFuseWeighted(fts, sem, 60, wFTS, wSem)
}

// RRFuseWeighted is RRFuse with a weight per list: a hit scores
// wa/(k+rank) from a and wb/(k+rank) from b (weights 1, 1: plain RRF).
func RRFuseWeighted(a, b []Hit, k int, wa, wb float64) []Hit {
	if k <= 0 {
		k = 60
	}
	scores := map[int64]float64{}
	rec := map[int64]Hit{}
	for rank, h := range a {
		scores[h.SegmentID] += wa / float64(k+rank+1)
		rec[h.SegmentID] = h
	}
	for rank, h := range b {
		scores[h.SegmentID] += wb / float64(k+rank+1)
		if _, ok := rec[h.SegmentID]; !ok {
			rec[h.SegmentID] = h
		}
	}
	out := make([]Hit, 0, len(rec))
	for id, hit := range rec {
		hit.Score = scores[id]
		out = append(out, hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].SegmentID < out[j].SegmentID
		}
		return out[i].Score > out[j].Score
	})
	return out
}
