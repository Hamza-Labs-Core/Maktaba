package channels

// The channel builder's API (Story 27.8): what the admin channel list and
// editor need beyond the CRUD.
//
//	POST /api/channels/{id}/follow-plan   hand a plan channel back to the lineup plan (admin)
//	GET  /api/channels/sources            ?kind=series|collection|lane|video &q= &limit=
//	                                      what a slot (or a filler pool) can play, with counts
//
// and two read-side additions on the list / get payload:
//
//	plan         {owned, edited, edited_at}: written by the lineup plan, and changed by
//	             hand since (channels.edited_at, slot 0118)
//	guide_until  the end of the channel's generated guide (how far ahead it is planned)
//
// "Edited" is decided by the plan writer (pipeline write_channels) from a content hash Python
// owns; Go never computes it. The API records its own edits in channels.edited_at, and
// follow-plan stores the sentinel FollowPlanHash as the link's hash, which the plan writer
// reads as "rewrite this channel from the plan, whatever it holds now".

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// FollowPlanHash is the external_links.content_hash that hands a channel back
// to the plan (pipeline write_channels.FOLLOW_PLAN; never a real hash).
const FollowPlanHash = "follow-plan"

// catalogSubjectID is the single subject the catalog stages (plan_channels,
// write_channels) run on (pipeline engine.graph.CATALOG_ID).
const catalogSubjectID = "00000000-0000-0000-0000-00000000ca7a"

// PlanStatus says whether the lineup plan owns a channel and whether a person
// changed it since.
type PlanStatus struct {
	Owned    bool       `json:"owned"`
	Edited   bool       `json:"edited"`
	EditedAt *time.Time `json:"edited_at,omitempty"`
}

// mountBuilder wires the builder routes (called from Mount).
func (h *Handler) mountBuilder(r chi.Router) {
	r.Get("/api/channels/sources", h.Sources)
	r.Post("/api/channels/{id}/follow-plan", h.FollowPlan)
}

// ─── plan status / guide coverage ────────────────────────────────────

// planStatuses returns the plan status of every plan-owned or edited channel
// (best-effort: a database without external_links or edited_at yields what it
// can).
func (h *Handler) planStatuses(ctx context.Context) map[string]PlanStatus {
	out := map[string]PlanStatus{}
	if rows, err := h.DB.QueryContext(ctx, `SELECT external_id FROM external_links
		WHERE target = 'maktaba_channel' AND subject_kind = 'catalog'`); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				out[id] = PlanStatus{Owned: true}
			}
		}
		_ = rows.Close()
	}
	if rows, err := h.DB.QueryContext(ctx,
		`SELECT id, edited_at FROM channels WHERE edited_at IS NOT NULL`); err == nil {
		for rows.Next() {
			var id string
			var at any
			if rows.Scan(&id, &at) != nil {
				continue
			}
			st := out[id]
			st.Edited = true
			if t, ok := asTime(at); ok {
				st.EditedAt = &t
			}
			out[id] = st
		}
		_ = rows.Close()
	}
	return out
}

// asTime reads a timestamp column as either driver hands it back.
func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
			if p, err := time.Parse(layout, t); err == nil {
				return p.UTC(), true
			}
		}
	case []byte:
		return asTime(string(t))
	}
	return time.Time{}, false
}

// guideUntil returns the end of each channel's generated guide (best-effort).
func (h *Handler) guideUntil(ctx context.Context) map[string]time.Time {
	out := map[string]time.Time{}
	rows, err := h.DB.QueryContext(ctx,
		`SELECT channel_id, max(end_at) FROM channel_programs_live GROUP BY channel_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var at any
		if rows.Scan(&id, &at) != nil {
			continue
		}
		if t, ok := asTime(at); ok {
			out[id] = t
		}
	}
	return out
}

// attachBuilderInfo sets plan / guide_until on c from the loaded maps.
func attachBuilderInfo(c *Channel, plan map[string]PlanStatus, until map[string]time.Time) {
	if st, ok := plan[c.ID]; ok {
		s := st
		c.Plan = &s
	}
	if t, ok := until[c.ID]; ok {
		u := t
		c.GuideUntil = &u
	}
}

// markEdited records that a person changed the channel's definition
// (best-effort: a database before slot 0118 has no column).
func (h *Handler) markEdited(ctx context.Context, ids ...string) {
	now := h.now()
	for _, id := range ids {
		_, _ = h.DB.ExecContext(ctx, `UPDATE channels SET edited_at = $1 WHERE id = $2`, now, id)
	}
}

// ─── follow-plan ─────────────────────────────────────────────────────

// FollowPlan hands a plan channel back to the lineup plan: the link's hash
// becomes FollowPlanHash (the plan writer then rewrites the channel from the
// plan and stores a real hash again), the "edited" mark is cleared, and a
// write_channels run is queued. 409 when no plan owns the channel.
func (h *Handler) FollowPlan(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.repo().get(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load channel").Wrap(err))
		return
	}
	res, err := h.DB.ExecContext(r.Context(), `UPDATE external_links SET content_hash = $1
		WHERE target = 'maktaba_channel' AND subject_kind = 'catalog' AND external_id = $2`,
		FollowPlanHash, id)
	if err != nil {
		httperror.Write(w, r, httperror.Conflict(
			"https://maktaba.dev/problems/channel-not-planned", "no lineup plan owns this channel"))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.Conflict(
			"https://maktaba.dev/problems/channel-not-planned", "no lineup plan owns this channel"))
		return
	}
	_, _ = h.DB.ExecContext(r.Context(), `UPDATE channels SET edited_at = NULL WHERE id = $1`, id)
	queued := h.enqueueWriteChannels(r.Context(), p)
	h.markStale(r.Context(), id)
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{
		"channel_id": id,
		"status":     "following-plan",
		"queued":     queued,
	})
}

// enqueueWriteChannels queues a forced write_channels run on the catalog
// through the same SQL function every enqueue goes through (best-effort: the
// next lineup run picks the channel up anyway).
func (h *Handler) enqueueWriteChannels(ctx context.Context, p *principal.Principal) bool {
	reason, _ := json.Marshal(map[string]any{"why": "follow-plan", "by": "user:" + p.UserID})
	var jobID sql.NullInt64
	var outcome string
	err := h.DB.QueryRowContext(ctx, `SELECT job_id, outcome FROM processing_jobs_enqueue(
		$1::text, $2::uuid, $3::text, $4::int, $5::boolean, $6::boolean,
		$7::jsonb, $8::bigint, $9::jsonb, $10::int)`,
		"catalog", catalogSubjectID, "write_channels", nil, true, false,
		string(reason), nil, nil, nil,
	).Scan(&jobID, &outcome)
	if err != nil {
		return false
	}
	switch outcome {
	case "inserted", "coalesced", "rerun_requested":
		return true
	}
	return false
}

// ─── sources ─────────────────────────────────────────────────────────

// Source kinds the builder can pick.
const (
	SourceSeries     = "series"
	SourceCollection = "collection"
	SourceLane       = "lane"
	SourceVideo      = "video"
)

// Source is one pickable thing: a series, a collection, a plan lane (a
// slot's queue) or a single video (a filler clip).
type Source struct {
	Kind       string   `json:"kind"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Count      int      `json:"count"`
	DurationMS int64    `json:"duration_ms"`
	Detail     []string `json:"detail,omitempty"` // a lane's shows
}

const (
	defaultSourceLimit = 25
	maxSourceLimit     = 100
)

// Sources answers GET /api/channels/sources: the series, collections, plan
// lanes or videos whose name matches q, with how many episodes/items each
// holds and how long they run. Admin-only (it is the builder's picker).
func (h *Handler) Sources(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := defaultSourceLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			httperror.Write(w, r, httperror.InvalidQuery("limit must be a positive integer"))
			return
		}
		limit = min(n, maxSourceLimit)
	}
	var items []Source
	var err error
	switch kind {
	case SourceSeries:
		items, err = h.seriesSources(r.Context(), q, limit)
	case SourceCollection:
		items, err = h.collectionSources(r.Context(), q, limit)
	case SourceLane:
		items, err = h.laneSources(r.Context(), q, limit)
	case SourceVideo:
		items, err = h.videoSources(r.Context(), q, limit)
	default:
		httperror.Write(w, r, httperror.InvalidQuery("kind must be one of series, collection, lane, video"))
		return
	}
	if err != nil {
		// a database without that table has nothing to offer, not an error
		items = []Source{}
	}
	if items == nil {
		items = []Source{}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"kind": kind, "items": items})
}

func likeArg(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func (h *Handler) seriesSources(ctx context.Context, q string, limit int) ([]Source, error) {
	return h.querySources(ctx, SourceSeries, `
		SELECT s.id, COALESCE(NULLIF(s.name_override, ''), s.name),
		       count(v.id), COALESCE(sum(v.duration_sec), 0)
		FROM series s
		LEFT JOIN series_episodes se ON se.series_id = s.id
		LEFT JOIN videos v ON v.id = se.video_id AND v.deleted_at IS NULL
		WHERE lower(COALESCE(NULLIF(s.name_override, ''), s.name)) LIKE $1 ESCAPE '\'
		GROUP BY s.id, s.name, s.name_override
		ORDER BY count(v.id) DESC, 2
		LIMIT $2`, q, limit)
}

func (h *Handler) collectionSources(ctx context.Context, q string, limit int) ([]Source, error) {
	return h.querySources(ctx, SourceCollection, `
		SELECT c.id, c.name, count(v.id), COALESCE(sum(v.duration_sec), 0)
		FROM collections c
		LEFT JOIN collection_items ci ON ci.collection_id = c.id
		LEFT JOIN videos v ON v.id = ci.video_id AND v.deleted_at IS NULL
		WHERE lower(c.name) LIKE $1 ESCAPE '\'
		GROUP BY c.id, c.name
		ORDER BY count(v.id) DESC, 2
		LIMIT $2`, q, limit)
}

func (h *Handler) videoSources(ctx context.Context, q string, limit int) ([]Source, error) {
	return h.querySources(ctx, SourceVideo, `
		SELECT v.id, COALESCE(NULLIF(v.title, ''), v.filename), 1, COALESCE(v.duration_sec, 0)
		FROM videos v
		WHERE v.deleted_at IS NULL
		  AND (lower(COALESCE(v.title, '')) LIKE $1 ESCAPE '\' OR lower(v.filename) LIKE $1 ESCAPE '\')
		ORDER BY 2
		LIMIT $2`, q, limit)
}

func (h *Handler) querySources(ctx context.Context, kind, query, q string, limit int) ([]Source, error) {
	rows, err := h.DB.QueryContext(ctx, query, likeArg(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var s Source
		var secs float64
		if err := rows.Scan(&s.ID, &s.Name, &s.Count, &secs); err != nil {
			continue
		}
		s.Kind = kind
		s.DurationMS = int64(secs * 1000)
		out = append(out, s)
	}
	return out, rows.Err()
}

// laneSources reads the latest lineup plan's lanes (the queues its slots play).
func (h *Handler) laneSources(ctx context.Context, q string, limit int) ([]Source, error) {
	var raw []byte
	if err := h.DB.QueryRowContext(ctx,
		`SELECT plan FROM channel_plans ORDER BY id DESC LIMIT 1`).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []Source{}, nil
		}
		return nil, err
	}
	var plan struct {
		Lanes map[string]struct {
			Name         string   `json:"name"`
			Shows        []string `json:"shows"`
			Hours        float64  `json:"hours"`
			EpisodeCount int      `json:"episode_count"`
		} `json:"lanes"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return []Source{}, nil
	}
	out := []Source{}
	for key, l := range plan.Lanes {
		hay := strings.ToLower(key + " " + l.Name + " " + strings.Join(l.Shows, " "))
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		name := l.Name
		if name == "" {
			name = key
		}
		out = append(out, Source{
			Kind: SourceLane, ID: key, Name: name, Count: l.EpisodeCount,
			DurationMS: int64(l.Hours * 3600 * 1000), Detail: l.Shows,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
