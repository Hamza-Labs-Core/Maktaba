package queues

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Production-style overrides (the stand-alone control centre's "Pin a show's
// style"). season_style.override_style wins over the automatic rating. A
// change is saved and the season's rate_style job is queued in the same
// transaction: the stage's fingerprint includes the override, so it runs, and
// when the effective style or tier changes its output moves on to
// plan_channels (whose fingerprint covers every rate_style output) and the
// channel writers. Setting the same style again, or clearing a pin the rating
// agrees with, changes no output and so nothing downstream.

const (
	styleListDefault = 50
	styleListMax     = 200
	styleBodyLimit   = 4 << 10
	// stylePriority puts the re-rate ahead of the background backlog: a person is waiting.
	stylePriority = 100
)

var validStyles = map[string]bool{"static": true, "talk": true, "produced": true, "cinematic": true}

// SeasonStyle is one row of GET /api/seasons/styles.
type SeasonStyle struct {
	SeasonID      string   `json:"season_id"`
	SeriesID      string   `json:"series_id"`
	Series        string   `json:"series"`
	Name          string   `json:"name"`
	Season        int      `json:"season"`
	LibraryID     *string  `json:"library_id"`
	Library       *string  `json:"library"`
	Style         *string  `json:"style"`
	Tier          *int     `json:"tier"`
	Confidence    *float64 `json:"confidence"`
	OverrideStyle *string  `json:"override_style"`
}

// StyleRequest is the PUT body: a style, or null / "" to clear the pin.
type StyleRequest struct {
	Style *string `json:"style"`
}

// StyleResponse is the PUT answer.
type StyleResponse struct {
	Style   *string           `json:"style"`
	Seasons []StyleSeasonDone `json:"seasons"`
}

// StyleSeasonDone says what happened to one season's re-rate.
type StyleSeasonDone struct {
	SeasonID string `json:"season_id"`
	Outcome  string `json:"outcome"`
	JobID    *int64 `json:"job_id"`
}

const styleListSQL = `
SELECT ss.id::text, ss.series_id::text, COALESCE(s.name_override, s.name), ss.name, ss.season,
       ss.library_id::text, l.name, st.style, st.tier, st.confidence, st.override_style
  FROM series_seasons ss
  JOIN series s ON s.id = ss.series_id
  LEFT JOIN libraries l ON l.id = ss.library_id
  LEFT JOIN season_style st ON st.season_id = ss.id
 WHERE ss.retired_at IS NULL
   AND ($1::uuid IS NULL OR ss.library_id = $1::uuid)
   AND ($2::text = '' OR s.name ILIKE $2 ESCAPE '\' OR s.name_override ILIKE $2 ESCAPE '\' OR ss.name ILIKE $2 ESCAPE '\')
   AND (NOT $3::boolean OR st.override_style IS NOT NULL)
   AND ($5::uuid[] IS NULL OR ss.library_id = ANY($5::uuid[]))
 ORDER BY l.name NULLS LAST, s.name, ss.season
 LIMIT $4`

const styleUpsertSQL = `
INSERT INTO season_style (season_id, override_style, updated_at) VALUES ($1::uuid, $2, $3)
ON CONFLICT (season_id) DO UPDATE SET override_style = EXCLUDED.override_style, updated_at = EXCLUDED.updated_at`

const seasonsOfSeriesLiveSQL = `
SELECT id::text FROM series_seasons WHERE series_id = $1::uuid AND retired_at IS NULL ORDER BY season`

const seasonExistsSQL = `SELECT 1 FROM series_seasons WHERE id = $1::uuid AND retired_at IS NULL`

// SeasonStyles implements GET /api/seasons/styles?library_id=&q=&pinned=&limit=.
// A non-admin sees only the seasons of the libraries they were granted
// (seasons without a library are admin-only).
func (h *Handler) SeasonStyles(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	var lib any
	if v := r.URL.Query().Get("library_id"); v != "" {
		if !isUUID(v) {
			httperror.Write(w, r, httperror.InvalidQuery("library_id must be a uuid"))
			return
		}
		if !canReadLibrary(principal.FromContext(r.Context()), v) {
			httperror.Write(w, r, httperror.Forbidden("", "no access to this library"))
			return
		}
		lib = v
	}
	pinned, e := common.QueryBool(r, "pinned", false)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	limit, e := common.QueryInt(r, "limit", styleListDefault)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	limit = min(max(limit, 1), styleListMax)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	like := ""
	if q != "" {
		like = likePattern(q)
	}
	rows, err := h.DB.QueryContext(r.Context(), styleListSQL, lib, like, pinned, limit,
		libraryScope(principal.FromContext(r.Context())))
	if err != nil {
		writeErr(w, r, "list season styles", err)
		return
	}
	defer rows.Close()
	items := []SeasonStyle{}
	for rows.Next() {
		var s SeasonStyle
		var libID, libName, style, override sql.NullString
		var tier sql.NullInt64
		var conf sql.NullFloat64
		if err := rows.Scan(&s.SeasonID, &s.SeriesID, &s.Series, &s.Name, &s.Season, &libID, &libName,
			&style, &tier, &conf, &override); err != nil {
			writeErr(w, r, "list season styles", err)
			return
		}
		s.LibraryID, s.Library, s.Style, s.OverrideStyle = nullStrPtr(libID), nullStrPtr(libName), nullStrPtr(style), nullStrPtr(override)
		s.Tier, s.Confidence = nullInt(tier), nullFloat(conf)
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "list season styles", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// PutSeasonStyle implements PUT /api/seasons/{id}/style.
func (h *Handler) PutSeasonStyle(w http.ResponseWriter, r *http.Request) {
	h.putStyle(w, r, false)
}

// PutSeriesStyle implements PUT /api/series/{id}/style: every live season of
// the series, as the stand-alone tool pinned by series name.
func (h *Handler) PutSeriesStyle(w http.ResponseWriter, r *http.Request) {
	h.putStyle(w, r, true)
}

func (h *Handler) putStyle(w http.ResponseWriter, r *http.Request, wholeSeries bool) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		httperror.Write(w, r, httperror.BadRequest("id must be a uuid"))
		return
	}
	var body StyleRequest
	if e := common.ReadJSON(r, &body, styleBodyLimit); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var style any
	if body.Style != nil && *body.Style != "" {
		if !validStyles[*body.Style] {
			httperror.Write(w, r, httperror.BadRequest(`style must be "static", "talk", "produced", "cinematic" or null`))
			return
		}
		style = *body.Style
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "set style", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	seasons, found, err := styleTargets(ctx, tx, id, wholeSeries)
	if err != nil {
		writeErr(w, r, "set style", err)
		return
	}
	if !found {
		httperror.Write(w, r, httperror.NotFound("no such season"))
		return
	}
	now := h.now()
	prio := stylePriority
	resp := StyleResponse{Seasons: []StyleSeasonDone{}}
	if s, ok := style.(string); ok {
		resp.Style = &s
	}
	for _, sid := range seasons {
		if _, err := tx.ExecContext(ctx, styleUpsertSQL, sid, style, now); err != nil {
			writeErr(w, r, "set style", err)
			return
		}
		res, err := Enqueue(ctx, tx, EnqueueParams{
			SubjectKind: KindSeason, SubjectID: sid, Stage: "rate_style", Priority: &prio,
			Reason: ReasonFor(ctx, "style_override", map[string]any{"style": style}),
		})
		if err != nil {
			writeErr(w, r, "queue rate_style", err)
			return
		}
		resp.Seasons = append(resp.Seasons, StyleSeasonDone{SeasonID: sid, Outcome: res.Outcome, JobID: res.JobID})
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "set style", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// styleTargets are the seasons a style change applies to; found is false
// when the season (or a series without live seasons) does not exist.
func styleTargets(ctx context.Context, tx *sql.Tx, id string, wholeSeries bool) (ids []string, found bool, err error) {
	if !wholeSeries {
		var one int
		switch err := tx.QueryRowContext(ctx, seasonExistsSQL, id).Scan(&one); {
		case err == sql.ErrNoRows:
			return nil, false, nil
		case err != nil:
			return nil, false, err
		}
		return []string{id}, true, nil
	}
	rows, err := tx.QueryContext(ctx, seasonsOfSeriesLiveSQL, id)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, false, err
		}
		ids = append(ids, s)
	}
	return ids, len(ids) > 0, rows.Err()
}
