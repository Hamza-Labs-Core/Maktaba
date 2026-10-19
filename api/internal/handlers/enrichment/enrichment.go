// Package enrichment implements the Story 26.6 enrichment review surface:
//
//	GET  /api/videos/{id}/enrichment            ranked candidates + per-field diff
//	POST /api/videos/{id}/enrichment/accept     promote a candidate (provenance-aware)
//	POST /api/videos/{id}/enrichment/dismiss    hide candidate(s)
//	POST /api/videos/{id}/enrichment/search     Find metadata: queue a search (no auto-apply)
//	POST /api/videos/{id}/enrichment/revert     restore a field's pre-accept value
//	POST /api/videos/{id}/enrichment/reenrich   enqueue a fresh enrich job (26.7)
//	POST /api/series/{id}/enrichment/accept-all  batch-accept across a series (26.6)
//
// The candidates come from the pipeline's metadata provider (the engine
// stage `enrich`, stub audit #4): `internal` (Maktaba's own intel:
// transcripts, chapters, the book a series explains, file and folder
// names, sibling episodes) and `web` / `llm` (a web search a language
// model read). No outside catalogue (TMDB, TVDB) is used. Each field
// carries its confidence and the sources it rests on (`mapped.fields`,
// `mapped.sources`), shown in the review panel.
//
// Enrichment results land in the `media_metadata_enrichment` staging
// table (slot 0077); nothing is promoted to `videos` until the user
// accepts. The "never overwrite a user edit" guarantee is enforced via
// `media_field_provenance` (slot 0078): an accept skips any field whose
// provenance origin is `user`, and every applied field is recorded
// `origin='enrichment'` with the previous value so it stays revertible.
//
// ACL: writes require an editor (admin in v1, mirroring the videos
// handler), so a read-only principal gets 403 on accept/dismiss/revert.
// Every handler degrades gracefully with a nil DB (empty candidates /
// 503 on mutation) so the dev/test path runs without a database.
package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
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

// mappableFields are the `videos` columns an enrichment candidate may
// promote. Everything else in `mapped` (rating, cast, genres, …) is a
// fact for the context card (26.9), not a writable video column.
var mappableFields = []string{"title", "description", "poster_path"}

// Handler bundles deps. A nil DB makes reads return empty and mutations
// return 503, keeping unit tests DB-free.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
	// Enqueue enqueues the engine's `enrich` stage for a video (the
	// router wires queues.Enqueue); nil leaves it to the home's sweep of
	// enrich_jobs (every 30 s).
	Enqueue func(ctx context.Context, videoID string, reason map[string]any) error
	Logger  *slog.Logger
}

// Candidate is one provider match for a video.
type Candidate struct {
	ID         string         `json:"id"`
	Provider   string         `json:"provider"`
	ExternalID string         `json:"external_id"`
	Confidence float64        `json:"confidence"`
	Accepted   bool           `json:"accepted"`
	Mapped     map[string]any `json:"mapped"`
	// Fields is the per-field diff vs. the current video (GET only).
	Fields []FieldDiff `json:"fields,omitempty"`
	// Title/Year are convenience hints for the "We found this might be
	// X (year)" headline.
	Title string `json:"title,omitempty"`
	Year  *int   `json:"year,omitempty"`
	// Sources is every source the candidate's fields cite: a web page
	// (url) or a kind of Maktaba's own intel (kind), GET only.
	Sources []Source `json:"sources,omitempty"`
	// Facts are the candidate's fields that are not video columns
	// (series, episode, presenter, the book, ...), with their sources.
	Facts []Fact `json:"facts,omitempty"`
}

// Source is where a candidate's field came from.
type Source struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// Fact is one non-promotable field of a candidate.
type Fact struct {
	Field      string   `json:"field"`
	Value      any      `json:"value"`
	Confidence *float64 `json:"confidence,omitempty"`
	Sources    []string `json:"sources,omitempty"`
}

// FieldDiff is one promotable field's current-vs-proposed comparison.
type FieldDiff struct {
	Field       string   `json:"field"`
	Current     string   `json:"current"`
	Proposed    string   `json:"proposed"`
	WouldChange bool     `json:"would_change"`
	Protected   bool     `json:"protected"` // user-owned ⇒ accept skips it
	Confidence  *float64 `json:"confidence,omitempty"`
	Sources     []string `json:"sources,omitempty"`
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/enrichment/pending", h.Pending)
	r.Get("/api/videos/{id}/enrichment", h.List)
	r.Post("/api/videos/{id}/enrichment/accept", h.Accept)
	r.Post("/api/videos/{id}/enrichment/dismiss", h.Dismiss)
	r.Post("/api/videos/{id}/enrichment/search", h.Search)
	r.Post("/api/videos/{id}/enrichment/revert", h.Revert)
	r.Post("/api/videos/{id}/enrichment/reenrich", h.Reenrich)
	r.Post("/api/series/{id}/enrichment/accept-all", h.AcceptAll)
}

// videoRow is the subset of `videos` enrichment cares about.
type videoRow struct {
	ID          string
	LibraryID   string
	Title       sql.NullString
	Description sql.NullString
	PosterPath  sql.NullString
	UpdatedAt   time.Time
}

func (v videoRow) field(name string) string {
	switch name {
	case "title":
		return v.Title.String
	case "description":
		return v.Description.String
	case "poster_path":
		return v.PosterPath.String
	}
	return ""
}

func (h *Handler) loadVideo(r *http.Request, id string) (videoRow, error) {
	var v videoRow
	if h.DB == nil {
		return v, sql.ErrConnDone
	}
	row := h.DB.QueryRowContext(r.Context(), `
		SELECT id, library_id, title, description, poster_path, updated_at
		FROM videos WHERE id = $1
	`, id)
	err := row.Scan(&v.ID, &v.LibraryID, &v.Title, &v.Description, &v.PosterPath, &v.UpdatedAt)
	return v, err
}

// canRead returns nil if the principal may read library_id.
func (h *Handler) canRead(r *http.Request, libraryID string) *httperror.Error {
	p := principal.FromContext(r.Context())
	if p == nil {
		return httperror.Forbidden("", "authentication required")
	}
	if p.AccessAllLibraries || p.HasLibrary(libraryID) {
		return nil
	}
	return httperror.Forbidden("", "")
}

// canEdit returns nil if the principal may edit a library (admin in v1,
// mirroring the videos PATCH path). A read-only user gets 403.
func (h *Handler) canEdit(r *http.Request) *httperror.Error {
	p := principal.FromContext(r.Context())
	if p == nil {
		return httperror.Forbidden("", "authentication required")
	}
	if p.IsAdmin {
		return nil
	}
	return httperror.Forbidden("", "editor required")
}

// List returns the ranked, non-dismissed candidates for a video with a
// per-field diff marking which fields would change and which are
// protected (user-owned).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"candidates": []Candidate{}})
		return
	}
	v, err := h.loadVideo(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}
	if e := h.canRead(r, v.LibraryID); e != nil {
		httperror.Write(w, r, e)
		return
	}
	cands, err := h.loadCandidates(r, id, "")
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load candidates"))
		return
	}
	prot := h.protectedFields(r, id)
	for i := range cands {
		cands[i].Fields = diffFields(v, cands[i].Mapped, prot)
		cands[i].Title, cands[i].Year = headline(cands[i].Mapped)
		cands[i].Sources, cands[i].Facts = provenanceOf(cands[i].Mapped)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"candidates": cands, "job": h.jobStatus(r, id)})
}

// PendingItem is one row in the library review queue.
type PendingItem struct {
	VideoID        string  `json:"video_id"`
	LibraryID      string  `json:"library_id"`
	VideoTitle     string  `json:"video_title"`
	CandidateTitle string  `json:"candidate_title"`
	Provider       string  `json:"provider"`
	Confidence     float64 `json:"confidence"`
}

// Pending lists videos that have at least one pending (non-dismissed,
// non-accepted) candidate, ACL-filtered to libraries the caller can
// read — the library review queue (Story 26.6). The top candidate per
// video is surfaced for the "We found this might be X" headline.
func (h *Handler) Pending(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	out := map[string]any{"items": []PendingItem{}}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, out)
		return
	}
	// One best candidate per video (highest confidence, not dismissed,
	// not yet accepted).
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT e.video_id, v.library_id, COALESCE(v.title, v.filename),
		       e.provider, e.confidence, e.mapped
		FROM media_metadata_enrichment e
		JOIN videos v ON v.id = e.video_id
		WHERE e.is_dismissed = false AND e.is_accepted = false
		ORDER BY e.video_id, e.confidence DESC
	`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load pending"))
		return
	}
	defer rows.Close()
	items := []PendingItem{}
	seen := map[string]bool{}
	for rows.Next() {
		var it PendingItem
		var raw []byte
		if rows.Scan(&it.VideoID, &it.LibraryID, &it.VideoTitle, &it.Provider, &it.Confidence, &raw) != nil {
			continue
		}
		if seen[it.VideoID] {
			continue // keep only the top candidate per video
		}
		seen[it.VideoID] = true
		if !p.AccessAllLibraries && !p.HasLibrary(it.LibraryID) {
			continue
		}
		title, _ := headline(decodeMapped(raw))
		it.CandidateTitle = title
		items = append(items, it)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// loadCandidates loads non-dismissed candidates for a video, ranked by
// confidence. When externalID is non-empty only that candidate is
// returned (used by accept).
func (h *Handler) loadCandidates(r *http.Request, videoID, externalID string) ([]Candidate, error) {
	q := `
		SELECT id, provider, external_id, confidence, is_accepted, mapped
		FROM media_metadata_enrichment
		WHERE video_id = $1 AND is_dismissed = false`
	args := []any{videoID}
	if externalID != "" {
		q += ` AND external_id = $2`
		args = append(args, externalID)
	}
	q += ` ORDER BY confidence DESC`
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		var raw []byte
		if err := rows.Scan(&c.ID, &c.Provider, &c.ExternalID, &c.Confidence, &c.Accepted, &raw); err != nil {
			continue
		}
		c.Mapped = decodeMapped(raw)
		out = append(out, c)
	}
	return out, rows.Err()
}

// protectedFields returns the set of fields whose provenance origin is
// 'user' (so an accept must skip them).
func (h *Handler) protectedFields(r *http.Request, videoID string) map[string]bool {
	out := map[string]bool{}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT field FROM media_field_provenance WHERE video_id = $1 AND origin = 'user'`, videoID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			out[f] = true
		}
	}
	return out
}

// diffFields builds the per-field current-vs-proposed diff.
func diffFields(v videoRow, mapped map[string]any, prot map[string]bool) []FieldDiff {
	out := []FieldDiff{}
	for _, f := range mappableFields {
		raw, ok := mapped[f]
		if !ok {
			continue
		}
		proposed := asString(raw)
		if proposed == "" {
			continue
		}
		cur := v.field(f)
		conf, srcs := fieldMeta(mapped, f)
		out = append(out, FieldDiff{
			Field:       f,
			Current:     cur,
			Proposed:    proposed,
			WouldChange: cur != proposed,
			Protected:   prot[f],
			Confidence:  conf,
			Sources:     srcs,
		})
	}
	return out
}

// fieldMeta reads a field's confidence and source ids from
// mapped.fields (the metadata provider's shape); nil when absent.
func fieldMeta(mapped map[string]any, field string) (*float64, []string) {
	fields, _ := mapped["fields"].(map[string]any)
	f, _ := fields[field].(map[string]any)
	if f == nil {
		return nil, nil
	}
	var conf *float64
	if c, ok := f["confidence"].(float64); ok {
		conf = &c
	}
	var srcs []string
	if raw, ok := f["sources"].([]any); ok {
		for _, s := range raw {
			if id, ok := s.(string); ok {
				srcs = append(srcs, id)
			}
		}
	}
	return conf, srcs
}

// provenanceOf lists a candidate's sources and its non-promotable fields.
func provenanceOf(mapped map[string]any) ([]Source, []Fact) {
	var sources []Source
	if raw, ok := mapped["sources"].([]any); ok {
		for _, x := range raw {
			m, _ := x.(map[string]any)
			if m == nil {
				continue
			}
			id := asString(m["id"])
			if id == "" {
				continue
			}
			src := Source{ID: id, Label: asString(m["label"]), Kind: asString(m["kind"])}
			if u := asString(m["url"]); safeURL(u) {
				src.URL = u
			}
			sources = append(sources, src)
		}
	}
	fields, _ := mapped["fields"].(map[string]any)
	names := make([]string, 0, len(fields))
	for name := range fields {
		if !contains(mappableFields, name) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if factOrder(names[i]) != factOrder(names[j]) {
			return factOrder(names[i]) < factOrder(names[j])
		}
		return names[i] < names[j]
	})
	var facts []Fact
	for _, name := range names {
		f, _ := fields[name].(map[string]any)
		if f == nil || f["value"] == nil {
			continue
		}
		conf, srcs := fieldMeta(mapped, name)
		facts = append(facts, Fact{Field: name, Value: f["value"], Confidence: conf, Sources: srcs})
	}
	return sources, facts
}

// factFields is the order facts are shown in.
var factFields = []string{"series", "season", "episode", "year", "presenter", "speakers",
	"language", "topic", "book_title", "book_author", "book_subject", "book_chapters"}

func factOrder(name string) int {
	for i, f := range factFields {
		if f == name {
			return i
		}
	}
	return len(factFields)
}

// safeURL: only http(s) links are handed to the panel (a source is a link).
func safeURL(u string) bool {
	return len(u) < 2048 && (strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://"))
}

// headline extracts the "X (year)" hint from a candidate's mapped fields.
func headline(mapped map[string]any) (string, *int) {
	title := asString(mapped["title"])
	if y, ok := mapped["year"]; ok {
		switch n := y.(type) {
		case float64:
			yi := int(n)
			return title, &yi
		case int:
			return title, &n
		}
	}
	return title, nil
}

func decodeMapped(raw []byte) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		b, _ := json.Marshal(s)
		return string(b)
	}
}
