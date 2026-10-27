package browse

// Epic 45 Phase 2, stories 45.11 and 45.12: what the grid's filters offer.
//
//	GET /api/videos/facets?library=<id>
//
// Over the visible videos of the library, or of every library the caller may
// see: their languages, tags and named speakers with counts, and the first
// characters of their titles (the A to Z jump bar). Another library's facets
// are 404, as an unknown library's.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// maxFacet caps the tags and speakers listed.
const maxFacet = 500

// LanguageFacet is a language and its videos.
type LanguageFacet struct {
	Code   string `json:"code"`
	Videos int64  `json:"videos"`
}

// TagFacet is a tag and its videos.
type TagFacet struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Videos int64  `json:"videos"`
}

// SpeakerFacet is a named speaker and the videos they speak in.
type SpeakerFacet struct {
	Name   string `json:"name"`
	Videos int64  `json:"videos"`
}

// LetterFacet is a jump bar entry: a first letter ("#" for the rest).
type LetterFacet struct {
	Letter string `json:"letter"`
	Videos int64  `json:"videos"`
}

// Facets implements GET /api/videos/facets.
func (h *Handler) Facets(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	s := scopeOf(p)
	lib := r.URL.Query().Get("library")
	if lib != "" {
		if _, err := uuid.Parse(lib); err != nil || !s.has(lib) {
			httperror.Write(w, r, httperror.NotFound("library "+lib))
			return
		}
	}
	doc := map[string]any{"languages": []LanguageFacet{}, "tags": []TagFacet{},
		"speakers": []SpeakerFacet{}, "letters": []LetterFacet{}}
	if s.none() {
		writeJSON(w, r, doc)
		return
	}
	ctx := r.Context()
	langs, err := h.languageFacets(ctx, s, lib)
	if err != nil {
		internal(w, r, "languages", err)
		return
	}
	tags, err := h.tagFacets(ctx, s, lib)
	if err != nil {
		internal(w, r, "tags", err)
		return
	}
	speakers, err := h.speakerFacets(ctx, s, lib)
	if err != nil {
		internal(w, r, "speakers", err)
		return
	}
	letters, err := h.letterFacets(ctx, s, lib)
	if err != nil {
		internal(w, r, "letters", err)
		return
	}
	doc["languages"], doc["tags"], doc["speakers"], doc["letters"] = langs, tags, speakers, letters
	writeJSON(w, r, doc)
}

func (h *Handler) languageFacets(ctx context.Context, s scope, lib string) ([]LanguageFacet, error) {
	var args []any
	cond := libFilter(s, lib, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT v.detected_language, COUNT(*) AS n FROM videos v
		WHERE `+and(visible, cond, "v.detected_language IS NOT NULL", "v.detected_language <> ''")+`
		GROUP BY v.detected_language ORDER BY n DESC, v.detected_language`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LanguageFacet{}
	for rows.Next() {
		var f LanguageFacet
		if err := rows.Scan(&f.Code, &f.Videos); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (h *Handler) tagFacets(ctx context.Context, s scope, lib string) ([]TagFacet, error) {
	var args []any
	cond := libFilter(s, lib, &args)
	args = append(args, maxFacet)
	rows, err := h.DB.QueryContext(ctx, `SELECT t.id, t.name, COUNT(*) FROM video_tags vt
		JOIN tags t ON t.id = vt.tag_id JOIN videos v ON v.id = vt.video_id
		WHERE `+and(visible, cond)+`
		GROUP BY t.id, t.name ORDER BY t.name, t.id LIMIT `+pidx(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagFacet{}
	for rows.Next() {
		var f TagFacet
		if err := rows.Scan(&f.ID, &f.Name, &f.Videos); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// speakerFacets lists the speakers an admin named: a speaker still called by
// its diarization label is not one.
func (h *Handler) speakerFacets(ctx context.Context, s scope, lib string) ([]SpeakerFacet, error) {
	var args []any
	cond := libFilter(s, lib, &args)
	args = append(args, maxFacet)
	rows, err := h.DB.QueryContext(ctx, `SELECT sp.name, COUNT(DISTINCT sp.video_id) FROM speakers sp
		JOIN videos v ON v.id = sp.video_id
		WHERE `+and(visible, cond, "sp.name <> ''", "sp.name <> COALESCE(sp.cluster_label, '')")+`
		GROUP BY sp.name ORDER BY sp.name LIMIT `+pidx(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpeakerFacet{}
	for rows.Next() {
		var f SpeakerFacet
		if err := rows.Scan(&f.Name, &f.Videos); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// letterFacets is the titles' first characters, a letter in either case
// counted once (under its capital), every other character under "#" (first),
// the letters in the title sort's order.
func (h *Handler) letterFacets(ctx context.Context, s scope, lib string) ([]LetterFacet, error) {
	var args []any
	cond := libFilter(s, lib, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT substr(COALESCE(v.title, ''), 1, 1) AS l, COUNT(*) FROM videos v
		WHERE `+and(visible, cond)+` GROUP BY l ORDER BY l`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	other := LetterFacet{Letter: "#"}
	var out []LetterFacet
	idx := map[string]int{}
	for rows.Next() {
		var l string
		var n int64
		if err := rows.Scan(&l, &n); err != nil {
			return nil, err
		}
		if !videos.IsLetterFirst(l) {
			other.Videos += n
			continue
		}
		key := strings.ToUpper(l)
		if i, ok := idx[key]; ok {
			out[i].Videos += n
			continue
		}
		idx[key] = len(out)
		out = append(out, LetterFacet{Letter: key, Videos: n})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Letter < out[j].Letter })
	if other.Videos > 0 {
		out = append([]LetterFacet{other}, out...)
	}
	if out == nil {
		out = []LetterFacet{}
	}
	return out, nil
}
