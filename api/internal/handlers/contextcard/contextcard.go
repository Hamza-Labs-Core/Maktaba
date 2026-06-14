// Package contextcard implements the Story 26.9 web context card:
//
//	GET /api/videos/{id}/context
//
// It is a pure read aggregation over data already produced by earlier
// Epic 26 stories plus existing signals — it adds no tables and makes
// no provider calls at view time (everything was fetched during the
// out-of-band `enrich` job). The payload has three optional blocks:
//
//   - facts: the accepted enrichment's mapped facts (rating, cast,
//     genres, summary, …) with provider attribution. Omitted if the
//     video has no accepted enrichment (partial card is first-class).
//   - related_in_library: typed local cross-references — same_series
//     (26.3), shared_cast (canonical entity id, 26.2), shared_topic
//     (video_topics), same_collection (26.4) — each with a reason.
//   - more_like_this: a local shared-topic recommendation that honours
//     `recommendation_dismissals` (Story 14.7). No web call.
//
// All related/recommended videos are ACL-filtered to libraries the
// caller can read. A nil DB returns an empty (but well-formed) card.
package contextcard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	relatedCap = 12
	mltCap     = 12
)

// Handler bundles deps.
type Handler struct {
	DB *sql.DB
}

// Mount wires the route.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/videos/{id}/context", h.Get)
}

// Related is one cross-referenced video with its reason.
type Related struct {
	VideoID string   `json:"video_id"`
	Title   string   `json:"title"`
	Reason  string   `json:"reason"`
	Via     string   `json:"via,omitempty"`
	Score   *float64 `json:"score,omitempty"`
}

// Card is the response envelope. Blocks are omitted when empty so the
// client renders a partial card without null rows.
type Card struct {
	Facts            map[string]any `json:"facts,omitempty"`
	RelatedInLibrary []Related      `json:"related_in_library"`
	MoreLikeThis     []Related      `json:"more_like_this"`
}

// Get aggregates the card.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	card := Card{RelatedInLibrary: []Related{}, MoreLikeThis: []Related{}}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, card)
		return
	}
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}

	var libraryID string
	err := h.DB.QueryRowContext(r.Context(), `SELECT library_id FROM videos WHERE id = $1`, id).Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}
	if !p.AccessAllLibraries && !p.HasLibrary(libraryID) {
		httperror.Write(w, r, httperror.Forbidden("", ""))
		return
	}

	card.Facts = h.buildFacts(r, id)

	related := []Related{}
	related = append(related, h.seriesSiblings(r, id)...)
	related = append(related, h.sharedCast(r, id)...)
	related = append(related, h.sharedTopic(r, id)...)
	related = append(related, h.sameCollection(r, id)...)
	card.RelatedInLibrary = h.aclDedupeCap(r, p, id, related, relatedCap)

	card.MoreLikeThis = h.moreLikeThis(r, p, id)

	common.WriteJSON(w, r, http.StatusOK, card)
}

// buildFacts pulls the accepted enrichment candidate's facts. Returns
// nil when the video has no accepted enrichment (partial card).
func (h *Handler) buildFacts(r *http.Request, videoID string) map[string]any {
	var raw []byte
	var provider string
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT mapped, provider FROM media_metadata_enrichment
		WHERE video_id = $1 AND is_accepted = true
		ORDER BY confidence DESC LIMIT 1
	`, videoID).Scan(&raw, &provider)
	if err != nil {
		return nil
	}
	mapped := map[string]any{}
	if json.Unmarshal(raw, &mapped) != nil {
		return nil
	}
	facts := map[string]any{}
	// Only surface fact keys that are present; never emit null rows.
	for _, k := range []string{"rating", "runtime_min", "genres", "content_rating", "cast", "director", "summary", "summary_lang"} {
		if v, ok := mapped[k]; ok && v != nil {
			facts[k] = v
		}
	}
	if len(facts) == 0 {
		return nil
	}
	if url, ok := mapped["attribution_url"].(string); ok && url != "" {
		facts["attribution"] = []map[string]any{{"provider": provider, "url": url}}
	} else if provider != "" {
		facts["attribution"] = []map[string]any{{"provider": provider}}
	}
	return facts
}

func (h *Handler) seriesSiblings(r *http.Request, videoID string) []Related {
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT v.id, COALESCE(v.title, v.filename)
		FROM series_episodes se
		JOIN series_episodes sib ON sib.series_id = se.series_id AND sib.video_id <> se.video_id
		JOIN videos v ON v.id = sib.video_id
		WHERE se.video_id = $1
		ORDER BY sib.season NULLS LAST, sib.episode NULLS LAST
		LIMIT $2
	`, videoID, relatedCap)
	return scanRelated(rows, err, "same_series", "")
}

func (h *Handler) sharedCast(r *http.Request, videoID string) []Related {
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT v.id, COALESCE(v.title, v.filename), e.name
		FROM video_entities ve
		JOIN video_entities other ON other.entity_id = ve.entity_id AND other.video_id <> ve.video_id
		JOIN media_entities e ON e.id = ve.entity_id AND e.kind = 'PER'
		JOIN videos v ON v.id = other.video_id
		WHERE ve.video_id = $1
		LIMIT $2
	`, videoID, relatedCap)
	return scanRelatedVia(rows, err, "shared_cast")
}

func (h *Handler) sharedTopic(r *http.Request, videoID string) []Related {
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT v.id, COALESCE(v.title, v.filename)
		FROM video_topics vt
		JOIN video_topics other ON other.topic_id = vt.topic_id
		     AND other.library_id = vt.library_id AND other.video_id <> vt.video_id
		JOIN videos v ON v.id = other.video_id
		WHERE vt.video_id = $1
		GROUP BY v.id, v.title, v.filename
		LIMIT $2
	`, videoID, relatedCap)
	return scanRelated(rows, err, "shared_topic", "")
}

func (h *Handler) sameCollection(r *http.Request, videoID string) []Related {
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT v.id, COALESCE(v.title, v.filename)
		FROM collection_items ci
		JOIN collection_items other ON other.collection_id = ci.collection_id AND other.video_id <> ci.video_id
		JOIN videos v ON v.id = other.video_id
		WHERE ci.video_id = $1
		LIMIT $2
	`, videoID, relatedCap)
	return scanRelated(rows, err, "same_collection", "")
}

// moreLikeThis is a local shared-topic recommendation that excludes the
// video itself, anything the caller dismissed (Story 14.7), and is
// ranked by topic overlap.
func (h *Handler) moreLikeThis(r *http.Request, p *principal.Principal, videoID string) []Related {
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT v.id, COALESCE(v.title, v.filename), v.library_id, SUM(vt.score) AS s
		FROM video_topics vt
		JOIN video_topics other ON other.topic_id = vt.topic_id
		     AND other.library_id = vt.library_id AND other.video_id <> vt.video_id
		JOIN videos v ON v.id = other.video_id
		WHERE vt.video_id = $1
		  AND NOT EXISTS (
		    SELECT 1 FROM recommendation_dismissals d
		    WHERE d.user_id = $2 AND d.video_id = v.id
		  )
		GROUP BY v.id, v.title, v.filename, v.library_id
		ORDER BY s DESC
		LIMIT $3
	`, videoID, dismissalUser(p), mltCap)
	if err != nil {
		return []Related{}
	}
	defer rows.Close()
	out := []Related{}
	for rows.Next() {
		var rel Related
		var lib string
		var score float64
		if rows.Scan(&rel.VideoID, &rel.Title, &lib, &score) != nil {
			continue
		}
		if !p.AccessAllLibraries && !p.HasLibrary(lib) {
			continue
		}
		s := score
		rel.Score = &s
		rel.Reason = "more_like_this"
		out = append(out, rel)
	}
	return out
}

// aclDedupeCap removes the source video, de-duplicates by video id
// (first reason wins), ACL-filters, and caps the list.
func (h *Handler) aclDedupeCap(r *http.Request, p *principal.Principal, selfID string, in []Related, cap int) []Related {
	seen := map[string]bool{selfID: true}
	// Resolve library per related video for ACL in one pass.
	out := []Related{}
	for _, rel := range in {
		if seen[rel.VideoID] {
			continue
		}
		var lib string
		if h.DB.QueryRowContext(r.Context(), `SELECT library_id FROM videos WHERE id = $1`, rel.VideoID).Scan(&lib) != nil {
			continue
		}
		if !p.AccessAllLibraries && !p.HasLibrary(lib) {
			continue
		}
		seen[rel.VideoID] = true
		out = append(out, rel)
		if len(out) >= cap {
			break
		}
	}
	return out
}

func scanRelated(rows *sql.Rows, err error, reason, via string) []Related {
	if err != nil {
		return []Related{}
	}
	defer rows.Close()
	out := []Related{}
	for rows.Next() {
		var rel Related
		if rows.Scan(&rel.VideoID, &rel.Title) != nil {
			continue
		}
		rel.Reason = reason
		rel.Via = via
		out = append(out, rel)
	}
	return out
}

func scanRelatedVia(rows *sql.Rows, err error, reason string) []Related {
	if err != nil {
		return []Related{}
	}
	defer rows.Close()
	out := []Related{}
	for rows.Next() {
		var rel Related
		if rows.Scan(&rel.VideoID, &rel.Title, &rel.Via) != nil {
			continue
		}
		rel.Reason = reason
		out = append(out, rel)
	}
	return out
}

// dismissalUser returns the principal's user id for the dismissal
// anti-join, or an empty string sentinel that matches no row.
func dismissalUser(p *principal.Principal) string {
	if p == nil {
		return ""
	}
	return p.UserID
}
