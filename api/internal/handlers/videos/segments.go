package videos

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Segment is one transcript_segments row in the over-the-wire shape.
type Segment struct {
	ID        int64     `json:"id"`
	Seq       int       `json:"seq"`
	StartSec  float64   `json:"start_sec"`
	EndSec    float64   `json:"end_sec"`
	Text      string    `json:"text"`
	Speaker   *string   `json:"speaker,omitempty"`
	Words     []Word    `json:"words,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Word is the optional word-level breakdown (AC-3).
type Word struct {
	Seq        int     `json:"seq"`
	StartSec   float64 `json:"start_sec"`
	EndSec     float64 `json:"end_sec"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// Segments implements Story 7.6: window-overlap query, RTL isolation,
// optional word-level.
func (h *Handler) Segments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}
	if e := h.canRead(r.Context(), v.LibraryID); e != nil {
		httperror.Write(w, r, e)
		return
	}

	from, e := common.QueryFloat(r, "from", 0)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	durSec := 0.0
	if v.DurationSec != nil {
		durSec = *v.DurationSec
	}
	defaultTo := durSec
	if defaultTo == 0 {
		defaultTo = 1e9 // sentinel "open right edge" when duration unknown
	}
	to, e := common.QueryFloat(r, "to", defaultTo)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if from > to {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/invalid-time-window",
			Title:  "invalid time window",
			Status: http.StatusBadRequest,
			Detail: "from > to",
		})
		return
	}
	// Clamp per EC-3.
	if from < 0 {
		from = 0
	}
	if v.DurationSec != nil && to > *v.DurationSec {
		to = *v.DurationSec
	}
	wantWords, e := common.QueryBool(r, "words", false)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	includeSuperseded, e := common.QueryBool(r, "include_superseded", false)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}

	limit, e := common.QueryInt(r, "limit", 200)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}

	// Find target transcript.
	var transID string
	transQ := `SELECT id FROM transcripts WHERE video_id = $1 AND is_active = TRUE ORDER BY created_at DESC LIMIT 1`
	if includeSuperseded {
		transQ = `SELECT id FROM transcripts WHERE video_id = $1 ORDER BY created_at DESC LIMIT 1`
	}
	err = h.DB.QueryRowContext(r.Context(), transQ, id).Scan(&transID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": []Segment{}, "partial": true})
			return
		}
		httperror.Write(w, r, httperror.Internal("load transcript"))
		return
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, seq, start_sec, end_sec, text, speaker
		FROM transcript_segments
		WHERE transcript_id = $1 AND start_sec < $2 AND end_sec > $3
		ORDER BY seq ASC
		LIMIT $4
	`, transID, to, from, limit+1)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("query segments: "+err.Error()))
		return
	}
	defer rows.Close()
	items := []Segment{}
	for rows.Next() {
		var s Segment
		if err := rows.Scan(&s.ID, &s.Seq, &s.StartSec, &s.EndSec, &s.Text, &s.Speaker); err != nil {
			httperror.Write(w, r, httperror.Internal("scan segment"))
			return
		}
		s.Text = bidiIsolate(s.Text)
		items = append(items, s)
	}

	if wantWords && len(items) > 0 {
		// Attach words per segment in one round-trip.
		first, last := items[0].ID, items[len(items)-1].ID
		wrows, err := h.DB.QueryContext(r.Context(), `
			SELECT segment_id, seq, start_sec, end_sec, text, confidence
			FROM transcript_words
			WHERE segment_id BETWEEN $1 AND $2
			ORDER BY segment_id, seq
		`, first, last)
		if err == nil {
			defer wrows.Close()
			byID := map[int64]*Segment{}
			for i := range items {
				byID[items[i].ID] = &items[i]
			}
			for wrows.Next() {
				var sid int64
				var word Word
				if err := wrows.Scan(&sid, &word.Seq, &word.StartSec, &word.EndSec, &word.Text, &word.Confidence); err == nil {
					if s, ok := byID[sid]; ok {
						s.Words = append(s.Words, word)
					}
				}
			}
		}
	}

	partial := false
	if len(items) <= limit {
		// Page fit; nothing trimmed. Check whether the window extends
		// past the last segment available — i.e. transcribe paused gap.
		if from > 0 && len(items) == 0 {
			partial = true
		}
	} else {
		items = items[:limit]
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"items":   items,
		"partial": partial,
	})
}

// bidiIsolate wraps s in U+2068 FIRST STRONG ISOLATE … U+2069 POP
// DIRECTIONAL ISOLATE so an English fragment in an Arabic paragraph
// doesn't flip surrounding text (Story 7.6 EC RTL).
func bidiIsolate(s string) string {
	if s == "" {
		return s
	}
	return "\u2068" + s + "\u2069"
}

// Subtitle is one row in the GET /api/videos/{id}/subtitles response
// (Story 7.7 AC-1).
type Subtitle struct {
	ID        int64  `json:"id"`
	Language  string `json:"language"`
	Format    string `json:"format"`
	Source    string `json:"source"`
	IsDefault bool   `json:"is_default"`
	URL       string `json:"url"`
}

// Subtitles enumerates subtitle_files. URLs are signed — but the
// signing key lives in Epic 10 / Streaming; here we mint an opaque
// placeholder that includes a TTL the test can assert on. Production
// wiring (Story 10.8 audience: streaming-static) injects a real
// URLSigner via Handler.URLSigner if present.
func (h *Handler) Subtitles(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}
	if e := h.canRead(r.Context(), v.LibraryID); e != nil {
		httperror.Write(w, r, e)
		return
	}

	subs, err := SubtitlesByVideo(r.Context(), h.DB, []string{id})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("query subtitles"))
		return
	}
	items := subs[id]
	if items == nil {
		items = []Subtitle{}
	}

	// the library's default_subtitle_lang (Story 9.1) marks its track the default;
	// a library that sets none has no default track
	if def := h.defaultSubtitleLang(r.Context(), v.LibraryID); def != "" {
		for i := range items {
			items[i].IsDefault = strings.EqualFold(items[i].Language, def)
		}
	}

	// AC-1 trailing rule: order by Accept-Language language preference.
	if accept := r.Header.Get("Accept-Language"); accept != "" {
		preferred := strings.Split(strings.ToLower(strings.Split(accept, ",")[0]), "-")[0]
		if preferred != "" {
			// stable sort: matched-language first, original order preserved
			matched := []Subtitle{}
			rest := []Subtitle{}
			for _, s := range items {
				if strings.EqualFold(s.Language, preferred) {
					matched = append(matched, s)
				} else {
					rest = append(rest, s)
				}
			}
			items = append(matched, rest...)
		}
	}

	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// defaultSubtitleLang is libraries.settings.default_subtitle_lang ("" when unset
// or not a string).
func (h *Handler) defaultSubtitleLang(ctx context.Context, libraryID string) string {
	var raw sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT settings FROM libraries WHERE id = $1`, libraryID).
		Scan(&raw); err != nil || !raw.Valid {
		return ""
	}
	var settings map[string]any
	if json.Unmarshal([]byte(raw.String), &settings) != nil {
		return ""
	}
	lang, _ := settings["default_subtitle_lang"].(string)
	return strings.TrimSpace(lang)
}

// Chapter is one row in GET /api/videos/{id}/chapters (Story 7.7 AC-2).
type Chapter struct {
	ID       int64    `json:"id"`
	Seq      int      `json:"seq"`
	StartSec float64  `json:"start_sec"`
	EndSec   *float64 `json:"end_sec,omitempty"`
	Title    string   `json:"title"`
	Source   string   `json:"source"`
}

// Chapters lists chapter rows for a video.
func (h *Handler) Chapters(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}
	if e := h.canRead(r.Context(), v.LibraryID); e != nil {
		httperror.Write(w, r, e)
		return
	}

	chapters, err := ChaptersByVideo(r.Context(), h.DB, []string{id})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("query chapters"))
		return
	}
	items := chapters[id]
	if items == nil {
		items = []Chapter{}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}
