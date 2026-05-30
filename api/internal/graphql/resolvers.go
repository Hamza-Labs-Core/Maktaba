// Resolvers bridges the GraphQL root fields the 10-foot TV clients
// query to the same DB-backed logic the REST handlers use, so the
// GraphQL backbone returns real data instead of an unconditional 501.
//
// This is deliberately a thin, fixed dispatcher (no general GraphQL
// engine — that remains gated by a separate plan): it covers exactly
// `recommendations`, `continueWatching`, `search`, and `searchSuggest`,
// which are the operations `apps/tv/*` send.
package graphql

import (
	"database/sql"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/recommendations"
)

// Resolvers holds the dependencies the GraphQL resolvers need.
type Resolvers struct {
	DB  *sql.DB
	Rec *recommendations.Handler
}

// NewResolvers builds a Resolvers from a *sql.DB, reusing the
// recommendations handler so cache/dismissal/determinism behaviour is
// identical between REST and GraphQL.
func NewResolvers(db *sql.DB) *Resolvers {
	return &Resolvers{
		DB:  db,
		Rec: &recommendations.Handler{DB: db},
	}
}

// railCard is the GraphQL RailCard shape.
type railCard struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	DurationSec  float64  `json:"durationSec"`
	PositionSec  *float64 `json:"positionSec,omitempty"`
	RemainingSec *float64 `json:"remainingSec,omitempty"`
	PosterURL    string   `json:"posterUrl,omitempty"`
	Snippet      string   `json:"snippet,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

// resolve dispatches one root field. It returns the data payload, an
// HTTP status to use on error, and an error (nil on success).
func (rs *Resolvers) resolve(r *http.Request, field string, vars map[string]any) (any, int, error) {
	switch field {
	case "continueWatching":
		return rs.continueWatching(r, intVar(vars, "limit", 12)), http.StatusOK, nil
	case "recommendations":
		return rs.recommendations(r, strVar(vars, "surface", "tv-home"), intVar(vars, "limit", 12)), http.StatusOK, nil
	case "search":
		return rs.search(r, strVar(vars, "q", ""), intVar(vars, "limit", 24)), http.StatusOK, nil
	case "searchSuggest":
		return rs.searchSuggest(r, strVar(vars, "q", "")), http.StatusOK, nil
	default:
		return nil, http.StatusNotImplemented, errSchemaOnly{field}
	}
}

type errSchemaOnly struct{ field string }

func (e errSchemaOnly) Error() string {
	return "field " + e.field + " is not yet wired (schema-only)"
}

// continueWatching resolves the Continue Watching rail for the
// authenticated user. Reuses the recommendations handler's continue
// rail so the 5%..95% predicate and (updated_at DESC, video_id ASC)
// determinism are shared with REST.
func (rs *Resolvers) continueWatching(r *http.Request, limit int) []railCard {
	if rs.Rec == nil {
		return []railCard{}
	}
	rail := rs.Rec.ContinueRailForGraphQL(r, principalID(r), limit)
	return railsToCards([]recommendations.Rail{rail})
}

// recommendations resolves all rails for the surface (default
// tv-home). reason carries the rail's reason_kind so the client can
// localize it (Story 14.6).
func (rs *Resolvers) recommendations(r *http.Request, surface string, limit int) []railCard {
	if rs.Rec == nil {
		return []railCard{}
	}
	rails := rs.Rec.RailsForGraphQL(r, principalID(r), surface, limit)
	return railsToCards(rails)
}

// search resolves a typed text search. We do a minimal FTS over
// videos.title; the full hybrid/semantic search remains a REST-only
// surface for now, so this is the honest subset the TV client needs
// for its grid.
func (rs *Resolvers) search(r *http.Request, q string, limit int) []railCard {
	out := []railCard{}
	if q == "" || rs.DB == nil {
		return out
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := rs.DB.QueryContext(r.Context(), `
		SELECT id, COALESCE(title, filename), COALESCE(duration_sec, 0)
		FROM videos
		WHERE state = 'ready'
		  AND (COALESCE(title, '') ILIKE $1 OR filename ILIKE $1)
		ORDER BY updated_at DESC, id ASC
		LIMIT $2
	`, "%"+q+"%", limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c railCard
		if err := rows.Scan(&c.ID, &c.Title, &c.DurationSec); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// searchSuggest resolves the typeahead "did you mean" list (Story
// 14.4) from search_history, mirroring REST GET /api/search/suggest.
func (rs *Resolvers) searchSuggest(r *http.Request, q string) []string {
	out := []string{}
	if q == "" || rs.DB == nil {
		return out
	}
	rows, err := rs.DB.QueryContext(r.Context(), `
		SELECT query FROM search_history
		WHERE query_norm LIKE $1
		ORDER BY hits DESC, last_used_at DESC LIMIT 10
	`, q+"%")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

func railsToCards(rails []recommendations.Rail) []railCard {
	out := []railCard{}
	for _, rail := range rails {
		for _, it := range rail.Items {
			c := railCard{
				ID:        it.VideoID,
				Title:     it.VideoID, // title is hydrated client-side / future video resolver
				PosterURL: it.PosterURL,
				Reason:    rail.ReasonKind,
			}
			if it.DurationSec != nil {
				c.DurationSec = *it.DurationSec
			}
			c.PositionSec = it.PositionSec
			c.RemainingSec = it.RemainingSec
			out = append(out, c)
		}
	}
	return out
}

func principalID(r *http.Request) string {
	if p := principal.FromContext(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

func intVar(vars map[string]any, key string, def int) int {
	if vars == nil {
		return def
	}
	switch v := vars[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case nil:
		return def
	default:
		return def
	}
}

func strVar(vars map[string]any, key, def string) string {
	if vars == nil {
		return def
	}
	if s, ok := vars[key].(string); ok && s != "" {
		return s
	}
	return def
}
