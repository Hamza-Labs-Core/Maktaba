package browse

// Epic 56 story 56.05: a library's Categories tab.
//
//	GET /api/libraries/{id}/categories
//
// {speakers, topics, languages, years}: each a list of {value, label, count,
// poster_video_id}, the most videos first, at most 50 of each kind, over the
// library's visible videos. value is what the grid filters by (GET /api/videos's
// speaker, tag, language and year), label what the tile shows. A library the
// caller cannot read is 404, as every route of a library.

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// MaxCategories caps each kind of the Categories tab.
const MaxCategories = 50

// Category is one tile of the Categories tab.
type Category struct {
	Value         string `json:"value"`
	Label         string `json:"label"`
	Count         int64  `json:"count"`
	PosterVideoID string `json:"poster_video_id,omitempty"`
}

// Categories implements GET /api/libraries/{id}/categories.
func (h *Handler) Categories(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	id := chi.URLParam(r, "id")
	s := scopeOf(p)
	if _, err := uuid.Parse(id); err != nil || !s.has(id) {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return
	}
	var one int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM libraries WHERE id = $1`, id).Scan(&one); err != nil {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return
	}
	ctx := r.Context()
	doc := map[string][]Category{}
	for _, k := range []struct {
		name, q string
	}{
		// a speaker an admin named (one still called by its diarization label is not one)
		{"speakers", `SELECT sp.name, sp.name, COUNT(DISTINCT v.id), MIN(CAST(v.id AS TEXT)) FROM speakers sp JOIN videos v ON v.id = sp.video_id
			WHERE v.library_id = $1 AND ` + visible + ` AND sp.name <> '' AND sp.name <> COALESCE(sp.cluster_label, '')
			GROUP BY sp.name`},
		{"topics", `SELECT CAST(t.id AS TEXT), t.name, COUNT(*), MIN(CAST(v.id AS TEXT)) FROM video_tags vt JOIN tags t ON t.id = vt.tag_id
			JOIN videos v ON v.id = vt.video_id WHERE v.library_id = $1 AND ` + visible + ` GROUP BY t.id, t.name`},
		{"languages", `SELECT v.detected_language, v.detected_language, COUNT(*), MIN(CAST(v.id AS TEXT)) FROM videos v
			WHERE v.library_id = $1 AND ` + visible + ` AND v.detected_language IS NOT NULL AND v.detected_language <> ''
			GROUP BY v.detected_language`},
	} {
		cats, err := h.categories(ctx, k.q, id)
		if err != nil {
			internal(w, r, k.name, err)
			return
		}
		doc[k.name] = cats
	}
	years, err := h.years(ctx, id)
	if err != nil {
		internal(w, r, "years", err)
		return
	}
	ys := []Category{}
	for _, y := range years {
		v := strconv.Itoa(y.Year)
		ys = append(ys, Category{Value: v, Label: v, Count: y.Videos})
	}
	sort.SliceStable(ys, func(i, j int) bool { return ys[i].Count > ys[j].Count })
	if len(ys) > MaxCategories {
		ys = ys[:MaxCategories]
	}
	doc["years"] = ys
	writeJSON(w, r, doc)
}

// categories reads (value, label, count, a video) rows, the most videos first.
func (h *Handler) categories(ctx context.Context, q, lib string) ([]Category, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT * FROM (`+q+`) c ORDER BY 3 DESC, 2 LIMIT $2`, lib, MaxCategories)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Value, &c.Label, &c.Count, &c.PosterVideoID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
