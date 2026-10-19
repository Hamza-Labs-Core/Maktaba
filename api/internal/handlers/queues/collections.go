package queues

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// LibraryCollections is the part of a library's story that is not a season
// (the stand-alone item page's "Topics, clips and promos"): the topic
// collections, the clip and stand-alone groups and the promo links of one
// library, each with the Plex collection written for it when there is one.

const promoListMax = 500

// TopicCollection is one topic (playlist) collection.
type TopicCollection struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Items      int     `json:"items"`
	Collection *string `json:"collection"`
}

// ClipGroup is one clip or stand-alone group.
type ClipGroup struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`  // clips | standalone
	Basis      string  `json:"basis"` // playlist | topic | rest | promo
	Title      string  `json:"title"`
	Items      int     `json:"items"`
	Collection *string `json:"collection"`
}

// PromoLink is a promo linked to the show it advertises.
type PromoLink struct {
	VideoID  string  `json:"video_id"`
	Title    string  `json:"title"`
	Show     string  `json:"show"`
	SeasonID *string `json:"season_id"`
	Season   *int    `json:"season"`
	Evidence string  `json:"evidence"`
}

// LibraryCollectionsResponse is the GET /api/items/library/{id}/collections body.
type LibraryCollectionsResponse struct {
	Topics     []TopicCollection `json:"topics"`
	ClipGroups []ClipGroup       `json:"clip_groups"`
	Promos     []PromoLink       `json:"promos"`
}

const topicsSQL = `
SELECT t.id::text, t.name, count(i.video_id), el.title
  FROM topic_collections t
  LEFT JOIN topic_items i ON i.topic_id = t.id
  LEFT JOIN external_links el ON el.target = 'plex_topic' AND el.subject_kind = 'topic'
                              AND el.subject_id = t.id::text
 WHERE t.library_id = $1::uuid
 GROUP BY t.id, t.name, el.title
 ORDER BY count(i.video_id) DESC, t.name`

const clipGroupsSQL = `
SELECT g.id::text, g.kind, g.basis, g.title, count(i.video_id), el.title
  FROM clip_groups g
  LEFT JOIN clip_group_items i ON i.group_id = g.id
  LEFT JOIN external_links el ON el.target = 'plex_clip_group' AND el.subject_kind = 'clip_group'
                              AND el.subject_id = g.id::text
 WHERE g.library_id = $1::uuid
 GROUP BY g.id, g.kind, g.basis, g.title, el.title
 ORDER BY g.kind, count(i.video_id) DESC, g.title`

const promosSQL = `
SELECT v.id::text, COALESCE(v.title, v.filename), COALESCE(s.name_override, s.name),
       p.season_id::text, p.season, p.evidence
  FROM promo_links p
  JOIN videos v ON v.id = p.video_id
  JOIN series s ON s.id = p.series_id
 WHERE p.library_id = $1::uuid
 ORDER BY COALESCE(s.name_override, s.name), p.season NULLS LAST, v.filename
 LIMIT $2`

// LibraryCollections implements GET /api/items/library/{id}/collections.
func (h *Handler) LibraryCollections(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		httperror.Write(w, r, httperror.BadRequest("id must be a uuid"))
		return
	}
	ctx := r.Context()
	out := LibraryCollectionsResponse{Topics: []TopicCollection{}, ClipGroups: []ClipGroup{}, Promos: []PromoLink{}}

	rows, err := h.DB.QueryContext(ctx, topicsSQL, id)
	if err != nil {
		writeErr(w, r, "topic collections", err)
		return
	}
	for rows.Next() {
		var t TopicCollection
		var coll sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &t.Items, &coll); err != nil {
			_ = rows.Close()
			writeErr(w, r, "topic collections", err)
			return
		}
		t.Collection = nullStrPtr(coll)
		out.Topics = append(out.Topics, t)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		writeErr(w, r, "topic collections", err)
		return
	}

	rows, err = h.DB.QueryContext(ctx, clipGroupsSQL, id)
	if err != nil {
		writeErr(w, r, "clip groups", err)
		return
	}
	for rows.Next() {
		var g ClipGroup
		var coll sql.NullString
		if err := rows.Scan(&g.ID, &g.Kind, &g.Basis, &g.Title, &g.Items, &coll); err != nil {
			_ = rows.Close()
			writeErr(w, r, "clip groups", err)
			return
		}
		g.Collection = nullStrPtr(coll)
		out.ClipGroups = append(out.ClipGroups, g)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		writeErr(w, r, "clip groups", err)
		return
	}

	rows, err = h.DB.QueryContext(ctx, promosSQL, id, promoListMax)
	if err != nil {
		writeErr(w, r, "promo links", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p PromoLink
		var seasonID sql.NullString
		var season sql.NullInt64
		if err := rows.Scan(&p.VideoID, &p.Title, &p.Show, &seasonID, &season, &p.Evidence); err != nil {
			writeErr(w, r, "promo links", err)
			return
		}
		p.SeasonID, p.Season = nullStrPtr(seasonID), nullInt(season)
		out.Promos = append(out.Promos, p)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "promo links", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}
