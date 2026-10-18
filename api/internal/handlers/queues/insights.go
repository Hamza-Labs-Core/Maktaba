package queues

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// Two small read endpoints that carry the stand-alone control centre's
// status panels the queue snapshot does not:
//
//	GET /api/queues/insights   the counters (videos, seasons, collections ...)
//	                           and the "Production style" and "Topics" panels
//	GET /api/queues/pulse      the heartbeat: is anything moving, is a job stuck

const (
	insightGenres = 12
	// pulseStalledAfter: a running job whose worker has not beaten for this
	// long is stuck (workers beat every few seconds; the reaper reclaims a
	// claim after 90 s).
	pulseStalledAfter = 2 * time.Minute
	// pulseIdleAfter: queued work that nothing has touched for this long.
	pulseIdleAfter = 10 * time.Minute
	pulseLookback  = time.Hour
)

// InsightTotals are the counters at the top of the status page.
type InsightTotals struct {
	Libraries         int `json:"libraries"`
	Videos            int `json:"videos"`
	VideosWithLength  int `json:"videos_with_length"`
	Series            int `json:"series"`
	Seasons           int `json:"seasons"`
	PlexCollections   int `json:"plex_collections"`
	DizqueTVShows     int `json:"dizquetv_shows"`
	Channels          int `json:"channels"`
	StylePinned       int `json:"style_pinned"`
	YouTubeVideos     int `json:"youtube_videos"`
	VideosWithoutDate int `json:"videos_without_exact_date"`
}

// InsightGenre is "seasons per main topic".
type InsightGenre struct {
	Genre   string `json:"genre"`
	Seasons int    `json:"seasons"`
}

// Insights is the GET /api/queues/insights body.
type Insights struct {
	Totals InsightTotals  `json:"totals"`
	Styles map[string]int `json:"styles"`
	Genres []InsightGenre `json:"genres"`
}

const insightTotalsSQL = `
SELECT (SELECT count(*) FROM libraries),
       (SELECT count(*) FROM videos WHERE deleted_at IS NULL),
       (SELECT count(*) FROM videos WHERE deleted_at IS NULL AND duration_sec IS NOT NULL),
       (SELECT count(*) FROM series),
       (SELECT count(*) FROM series_seasons WHERE retired_at IS NULL),
       (SELECT count(*) FROM external_links WHERE target = 'plex_collection'),
       (SELECT count(*) FROM external_links WHERE target = 'dizquetv_show'),
       (SELECT count(*) FROM channels),
       (SELECT count(*) FROM season_style WHERE override_style IS NOT NULL),
       (SELECT count(*) FROM youtube_videos),
       (SELECT count(*) FROM youtube_videos WHERE date_quality IS DISTINCT FROM 'exact')`

const insightStylesSQL = `
SELECT COALESCE(st.override_style, st.style), count(*)
  FROM season_style st JOIN series_seasons ss ON ss.id = st.season_id AND ss.retired_at IS NULL
 WHERE COALESCE(st.override_style, st.style) IS NOT NULL
 GROUP BY 1`

const insightGenresSQL = `
SELECT substr(t.tag, 7), count(*) AS n
  FROM season_tags t JOIN series_seasons ss ON ss.id = t.season_id AND ss.retired_at IS NULL
 WHERE t.tag LIKE 'genre:%'
 GROUP BY 1 ORDER BY n DESC, 1 LIMIT $1`

// Insights implements GET /api/queues/insights.
func (h *Handler) Insights(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	ctx := r.Context()
	out := Insights{Styles: map[string]int{}, Genres: []InsightGenre{}}
	t := &out.Totals
	if err := h.DB.QueryRowContext(ctx, insightTotalsSQL).Scan(&t.Libraries, &t.Videos, &t.VideosWithLength,
		&t.Series, &t.Seasons, &t.PlexCollections, &t.DizqueTVShows, &t.Channels, &t.StylePinned,
		&t.YouTubeVideos, &t.VideosWithoutDate); err != nil {
		writeErr(w, r, "insights", err)
		return
	}
	rows, err := h.DB.QueryContext(ctx, insightStylesSQL)
	if err != nil {
		writeErr(w, r, "insights", err)
		return
	}
	for rows.Next() {
		var style string
		var n int
		if err := rows.Scan(&style, &n); err != nil {
			_ = rows.Close()
			writeErr(w, r, "insights", err)
			return
		}
		out.Styles[style] = n
	}
	_ = rows.Close()
	grows, err := h.DB.QueryContext(ctx, insightGenresSQL, insightGenres)
	if err != nil {
		writeErr(w, r, "insights", err)
		return
	}
	defer grows.Close()
	for grows.Next() {
		var g InsightGenre
		if err := grows.Scan(&g.Genre, &g.Seasons); err != nil {
			writeErr(w, r, "insights", err)
			return
		}
		out.Genres = append(out.Genres, g)
	}
	if err := grows.Err(); err != nil {
		writeErr(w, r, "insights", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Pulse states.
const (
	PulseIdle    = "idle"
	PulseWorking = "working"
	PulsePaused  = "paused"
	PulseStalled = "stalled"
)

// StuckJob is a running job whose worker stopped beating.
type StuckJob struct {
	JobID            int64   `json:"job_id"`
	Stage            string  `json:"stage"`
	Label            string  `json:"label"`
	SubjectKind      string  `json:"subject_kind"`
	SubjectID        string  `json:"subject_id"`
	HeartbeatAgeSec  float64 `json:"heartbeat_age_sec"`
	ClaimedBy        *string `json:"claimed_by"`
	StalledAfterSec  int     `json:"stalled_after_sec"`
	RunningForSecond float64 `json:"running_for_sec"`
}

// Pulse is the GET /api/queues/pulse body: the control centre's heartbeat line
// ("Last activity 12 s ago" / "No activity for 14 min: it may be stuck").
type Pulse struct {
	State          string     `json:"state"`
	Running        int        `json:"running"`
	Waiting        int        `json:"waiting"`
	PausedAll      bool       `json:"paused_all"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	AgeSec         *float64   `json:"age_sec"`
	Stuck          []StuckJob `json:"stuck"`
}

const pulseCountsSQL = `
SELECT count(*) FILTER (WHERE j.state IN ` + inFlightSQL + `),
       count(*) FILTER (WHERE j.state = 'pending' AND (j.not_before IS NULL OR j.not_before <= $1)
                          AND s.enabled AND NOT s.paused),
       max(j.last_heartbeat_at) FILTER (WHERE j.state IN ` + inFlightSQL + `)
  FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
 WHERE j.state IN ('claimed','running','resuming','pending')`

const pulseLastDoneSQL = `SELECT max(finished_at) FROM processing_jobs WHERE finished_at >= $1`

const pulseControlSQL = `SELECT COALESCE((SELECT paused FROM queue_control WHERE id = 1), false)`

// pulseStuckQuery lists the in-flight jobs whose last heartbeat ($1 = now) is older than
// $2 seconds, longest silent first.
func pulseStuckQuery() string {
	joins, label := labelSQL("j.subject_kind", "j.subject_id")
	return `SELECT j.id, j.stage, ` + label + `, j.subject_kind, j.subject_id::text,
       extract(epoch FROM ($1::timestamptz - COALESCE(j.last_heartbeat_at, j.claimed_at, j.started_at))),
       j.claimed_by, extract(epoch FROM ($1::timestamptz - COALESCE(j.started_at, j.claimed_at)))
  FROM processing_jobs j` + joins + `
 WHERE j.state IN ` + inFlightSQL + `
   AND COALESCE(j.last_heartbeat_at, j.claimed_at, j.started_at) < $1::timestamptz - $2::int * interval '1 second'
 ORDER BY 6 DESC
 LIMIT ` + "20" + `
`
}

// Pulse implements GET /api/queues/pulse.
func (h *Handler) Pulse(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	ctx := r.Context()
	now := h.now()
	out := Pulse{State: PulseIdle, Stuck: []StuckJob{}}
	var hb, done sql.NullTime
	if err := h.DB.QueryRowContext(ctx, pulseCountsSQL, now).Scan(&out.Running, &out.Waiting, &hb); err != nil {
		writeErr(w, r, "pulse", err)
		return
	}
	if err := h.DB.QueryRowContext(ctx, pulseLastDoneSQL, now.Add(-pulseLookback)).Scan(&done); err != nil {
		writeErr(w, r, "pulse", err)
		return
	}
	if err := h.DB.QueryRowContext(ctx, pulseControlSQL).Scan(&out.PausedAll); err != nil {
		writeErr(w, r, "pulse", err)
		return
	}
	var last time.Time
	for _, t := range []sql.NullTime{hb, done} {
		if t.Valid && t.Time.After(last) {
			last = t.Time
		}
	}
	if !last.IsZero() {
		l := last.UTC()
		age := now.Sub(last).Seconds()
		out.LastActivityAt, out.AgeSec = &l, &age
	}

	rows, err := h.DB.QueryContext(ctx, pulseStuckQuery(), now, int(pulseStalledAfter.Seconds()))
	if err != nil {
		writeErr(w, r, "pulse", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var j StuckJob
		var by sql.NullString
		if err := rows.Scan(&j.JobID, &j.Stage, &j.Label, &j.SubjectKind, &j.SubjectID, &j.HeartbeatAgeSec,
			&by, &j.RunningForSecond); err != nil {
			writeErr(w, r, "pulse", err)
			return
		}
		j.ClaimedBy, j.StalledAfterSec = nullStrPtr(by), int(pulseStalledAfter.Seconds())
		out.Stuck = append(out.Stuck, j)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "pulse", err)
		return
	}

	switch {
	case out.PausedAll:
		out.State = PulsePaused
	case len(out.Stuck) > 0:
		out.State = PulseStalled
	case out.Running > 0 || out.Waiting > 0:
		out.State = PulseWorking
		if out.Running == 0 && (out.AgeSec == nil || *out.AgeSec > pulseIdleAfter.Seconds()) {
			out.State = PulseStalled // work is queued and nothing has moved for a long while
		}
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}
