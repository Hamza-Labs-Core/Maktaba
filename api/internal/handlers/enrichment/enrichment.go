// Package enrichment implements the Story 26.6 enrichment review surface:
//
//	GET  /api/videos/{id}/enrichment            ranked candidates + per-field diff
//	POST /api/videos/{id}/enrichment/accept     promote a candidate (provenance-aware)
//	POST /api/videos/{id}/enrichment/dismiss    hide candidate(s)
//	POST /api/videos/{id}/enrichment/search     manual re-search (no auto-apply)
//	POST /api/videos/{id}/enrichment/revert     restore a field's pre-accept value
//	POST /api/videos/{id}/enrichment/reenrich   enqueue a fresh enrich job (26.7)
//	POST /api/series/{id}/enrichment/accept-all  batch-accept across a series (26.6)
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
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
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
}

// FieldDiff is one promotable field's current-vs-proposed comparison.
type FieldDiff struct {
	Field       string `json:"field"`
	Current     string `json:"current"`
	Proposed    string `json:"proposed"`
	WouldChange bool   `json:"would_change"`
	Protected   bool   `json:"protected"` // user-owned ⇒ accept skips it
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
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"candidates": cands})
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
		out = append(out, FieldDiff{
			Field:       f,
			Current:     cur,
			Proposed:    proposed,
			WouldChange: cur != proposed,
			Protected:   prot[f],
		})
	}
	return out
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
