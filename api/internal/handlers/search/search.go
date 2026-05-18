// Package search implements Stories 7.8 and 7.9:
//
//	POST /api/search           — hybrid / FTS / semantic search
//	GET  /api/search/suggest   — typeahead (Story 7.8 AC-4)
//	POST /api/search/save      — Story 7.9 save
//	GET  /api/search/saved     — Story 7.9 list
//	DEL  /api/search/saved/{id}— Story 7.9 delete
//
// The hybrid implementation runs FTS against transcript_segments and
// optionally a semantic top-K via the gRPC SemanticClient, then fuses
// results with Reciprocal-Rank-Fusion (RRF, k=60). When SemanticClient
// is nil the API still serves the FTS path so single-binary deployments
// without Pipeline are usable.
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
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
}

// Hit is a single segment-level result; the API maps unit→segment per
// Story 7.8 AC-6.
type Hit struct {
	SegmentID int64   `json:"segment_id"`
	VideoID   string  `json:"video_id"`
	StartSec  float64 `json:"start_sec"`
	EndSec    float64 `json:"end_sec"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
}

// Response is the AC-1 envelope.
type Response struct {
	Hits    []Hit     `json:"hits"`
	Total   int       `json:"total"`
	TookMs  ResponseT `json:"took_ms"`
	Mode    string    `json:"mode"`
	Filters Filters   `json:"filters"`

	// Degraded is true when the semantic leg was abandoned (deadline
	// breach or backend error) and the request was served FTS-only
	// (Story 18.2 AC4 / HLB-333). Clients surface a "results may be
	// incomplete" banner. Omitted on the happy path.
	Degraded bool `json:"degraded,omitempty"`
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
			return v, false
		}
	}
	cctx, cancel := context.WithTimeout(ctx, h.semanticBudget())
	defer cancel()
	res, err := h.Semantic.Search(cctx, q, k, f)
	if err != nil {
		// Deadline breach or backend error: log + continue FTS-only
		// (Story 18.2 AC4). Previously this was silently swallowed.
		h.log().Warn("search: semantic leg degraded; serving FTS-only",
			"event", "search_semantic_degraded",
			"budget_ms", h.semanticBudget().Milliseconds(),
			"err", err)
		return nil, true
	}
	if h.EmbedCache != nil {
		h.EmbedCache.Put(key, res)
	}
	return res, false
}

// Mount wires the search routes.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/search", h.Search)
	r.Get("/api/search/suggest", h.Suggest)
	r.Post("/api/search/save", h.SaveSearch)
	r.Get("/api/search/saved", h.ListSaved)
	r.Delete("/api/search/saved/{id}", h.DeleteSaved)
}

// Search implements POST /api/search.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	var req Request
	if e := common.ReadJSON(r, &req, 32<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/invalid-query",
			Title:  "invalid query",
			Status: http.StatusBadRequest,
			Detail: "q must be non-empty",
		})
		return
	}
	if len(req.Q) > 1024 {
		req.Q = req.Q[:1024]
	}
	if req.Mode == "" {
		req.Mode = "hybrid"
	}
	if req.Limit <= 0 || req.Limit > 200 {
		req.Limit = 50
	}

	// Authz scope: non-admin restricted to their library set.
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	if !p.AccessAllLibraries {
		// Intersect requested library filter with principal's libraries.
		allowed := map[string]bool{}
		for _, l := range p.Libraries {
			allowed[l] = true
		}
		if len(req.Filters.LibraryID) == 0 {
			req.Filters.LibraryID = p.Libraries
		} else {
			intersected := []string{}
			for _, lid := range req.Filters.LibraryID {
				if allowed[lid] {
					intersected = append(intersected, lid)
				}
			}
			req.Filters.LibraryID = intersected
		}
		if len(req.Filters.LibraryID) == 0 {
			common.WriteJSON(w, r, http.StatusOK, Response{Hits: []Hit{}, Mode: req.Mode, Filters: req.Filters})
			return
		}
	}

	var (
		ftsHits  []Hit
		semHits  []Hit
		took     ResponseT
		errFTS   error
		degraded bool
	)
	t0 := time.Now()
	if req.Mode == "fts" || req.Mode == "hybrid" {
		ftsHits, errFTS = h.runFTS(r.Context(), req.Q, req.Limit, req.Filters)
		took.FTS = time.Since(t0).Milliseconds()
		if errFTS != nil {
			httperror.Write(w, r, httperror.Internal("fts: "+errFTS.Error()))
			return
		}
	}
	if (req.Mode == "semantic" || req.Mode == "hybrid") && h.Semantic != nil {
		t1 := time.Now()
		semHits, degraded = h.runSemantic(r.Context(), req.Q, req.Limit, req.Filters)
		took.Semantic = time.Since(t1).Milliseconds()
	}

	t2 := time.Now()
	var fused []Hit
	switch req.Mode {
	case "fts":
		fused = ftsHits
	case "semantic":
		fused = semHits
	default:
		fused = RRFuse(ftsHits, semHits, 60)
	}
	if len(fused) > req.Limit {
		fused = fused[:req.Limit]
	}
	for i := range fused {
		fused[i].Snippet = highlightSnippet(fused[i].Snippet, req.Q, 240)
	}
	took.Fusion = time.Since(t2).Milliseconds()

	common.WriteJSON(w, r, http.StatusOK, Response{
		Hits: fused, Total: len(fused), TookMs: took, Mode: req.Mode,
		Filters: req.Filters, Degraded: degraded,
	})
}

// runFTS issues a single FTS query against transcript_segments using
// “search_tsv @@ plainto_tsquery“ (Postgres) — SQLite stays as a LIKE
// fallback when run under the test driver. Filters are pushed into the
// SQL.
func (h *Handler) runFTS(ctx context.Context, q string, limit int, f Filters) ([]Hit, error) {
	args := []any{q}
	where := []string{}
	if len(f.LibraryID) > 0 {
		placeholders := make([]string, len(f.LibraryID))
		for i, lid := range f.LibraryID {
			placeholders[i] = "$" + strconv.Itoa(len(args)+1)
			args = append(args, lid)
		}
		where = append(where, "v.library_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(f.Language) > 0 {
		placeholders := make([]string, len(f.Language))
		for i, l := range f.Language {
			placeholders[i] = "$" + strconv.Itoa(len(args)+1)
			args = append(args, l)
		}
		where = append(where, "v.detected_language IN ("+strings.Join(placeholders, ",")+")")
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " AND " + strings.Join(where, " AND ")
	}
	args = append(args, limit)

	// Try Postgres first; fall back to LIKE on error (sqlite test path).
	pgQuery := `
		SELECT s.id, t.video_id, s.start_sec, s.end_sec, s.text,
		       ts_rank(s.search_tsv, plainto_tsquery('maktaba_search', $1)) AS score
		FROM transcript_segments s
		JOIN transcripts t ON t.id = s.transcript_id
		JOIN videos v ON v.id = t.video_id
		WHERE s.search_tsv @@ plainto_tsquery('maktaba_search', $1)
		` + whereSQL + `
		ORDER BY score DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := h.DB.QueryContext(ctx, pgQuery, args...)
	if err != nil {
		// Likely SQLite (no tsvector). Fall back.
		return h.runFTSFallback(ctx, q, limit, f)
	}
	defer rows.Close()
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.SegmentID, &h.VideoID, &h.StartSec, &h.EndSec, &h.Snippet, &h.Score); err != nil {
			continue
		}
		hits = append(hits, h)
	}
	return hits, nil
}

// runFTSFallback runs a LIKE-based query for SQLite test environments
// that don't have the Postgres-only tsvector column.
func (h *Handler) runFTSFallback(ctx context.Context, q string, limit int, f Filters) ([]Hit, error) {
	args := []any{"%" + q + "%"}
	where := []string{"s.text LIKE $1"}
	if len(f.LibraryID) > 0 {
		placeholders := make([]string, len(f.LibraryID))
		for i, lid := range f.LibraryID {
			placeholders[i] = "$" + strconv.Itoa(len(args)+1)
			args = append(args, lid)
		}
		where = append(where, "v.library_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(f.Language) > 0 {
		placeholders := make([]string, len(f.Language))
		for i, l := range f.Language {
			placeholders[i] = "$" + strconv.Itoa(len(args)+1)
			args = append(args, l)
		}
		where = append(where, "v.detected_language IN ("+strings.Join(placeholders, ",")+")")
	}
	args = append(args, limit)
	query := `
		SELECT s.id, t.video_id, s.start_sec, s.end_sec, s.text, 1.0
		FROM transcript_segments s
		JOIN transcripts t ON t.id = s.transcript_id
		JOIN videos v ON v.id = t.video_id
		WHERE ` + strings.Join(where, " AND ") + `
		LIMIT $` + strconv.Itoa(len(args))
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.SegmentID, &h.VideoID, &h.StartSec, &h.EndSec, &h.Snippet, &h.Score); err != nil {
			continue
		}
		hits = append(hits, h)
	}
	return hits, nil
}

// Suggest implements AC-4: prefix lookup on the search_history corpus,
// up to 10 results, sourced from the lowercased+unaccented column.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"suggestions": []string{}})
		return
	}
	prefix := normaliseSuggestQuery(q)
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT query FROM search_history
		WHERE query_norm LIKE $1
		ORDER BY hits DESC, last_used_at DESC LIMIT 10
	`, prefix+"%")
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
				Type:   "https://maktaba.dev/problems/saved-search-name-exists",
				Title:  "name already exists",
				Status: http.StatusConflict,
				Detail: req.Name + " is already saved",
			})
			return
		}
		httperror.Write(w, r, httperror.Internal("save: "+err.Error()))
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
		httperror.Write(w, r, httperror.Internal("list saved"))
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
		httperror.Write(w, r, httperror.Internal("delete saved"))
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
func RRFuse(a, b []Hit, k int) []Hit {
	if k <= 0 {
		k = 60
	}
	scores := map[int64]float64{}
	rec := map[int64]Hit{}
	for rank, h := range a {
		scores[h.SegmentID] += 1.0 / float64(k+rank+1)
		rec[h.SegmentID] = h
	}
	for rank, h := range b {
		scores[h.SegmentID] += 1.0 / float64(k+rank+1)
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

// highlightSnippet wraps each occurrence of q in s with “<mark>...</mark>“
// and trims to maxLen with an ellipsis around the first match. Search
// is case-insensitive.
func highlightSnippet(s, q string, maxLen int) string {
	if s == "" || q == "" {
		return s
	}
	lower := strings.ToLower(s)
	ql := strings.ToLower(q)
	idx := strings.Index(lower, ql)
	if idx == -1 {
		if len(s) > maxLen {
			return s[:maxLen] + "…"
		}
		return s
	}
	// Build snippet around the match.
	start := idx - 60
	if start < 0 {
		start = 0
	}
	end := idx + len(q) + maxLen - (idx - start)
	if end > len(s) {
		end = len(s)
	}
	excerpt := s[start:end]
	// Replace each (case-insensitive) occurrence with <mark>...</mark>.
	var out strings.Builder
	excerptLower := strings.ToLower(excerpt)
	i := 0
	for i < len(excerpt) {
		j := strings.Index(excerptLower[i:], ql)
		if j < 0 {
			out.WriteString(excerpt[i:])
			break
		}
		out.WriteString(excerpt[i : i+j])
		out.WriteString("<mark>")
		out.WriteString(excerpt[i+j : i+j+len(q)])
		out.WriteString("</mark>")
		i += j + len(q)
	}
	prefix := ""
	if start > 0 {
		prefix = "…"
	}
	suffix := ""
	if end < len(s) {
		suffix = "…"
	}
	return prefix + out.String() + suffix
}
