// Package transcription serves the live report of the whole-library
// transcription programme:
//
//	GET /api/transcription/summary
//
// One JSON document with the totals (videos, hours, segments), the
// latest-job state of every video in the transcribe / subtitle_gen /
// index stages, the recent throughput (10 min and 1 h windows), an ETA
// for the remaining audio, a per-library breakdown and the most recent
// open failures. Reads need an authenticated principal (the same rule
// as the queue control centre); there are no mutations.
//
// The page refreshes on the existing /ws/queues + jobs SSE streams, so
// the response is cached for CacheTTL: a burst of finished jobs costs
// one set of queries, not one per client per event.
package transcription

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Stages are the transcription programme's stages, in pipeline order.
var Stages = []string{"transcribe", "subtitle_gen", "index"}

// CacheTTL bounds how stale a served summary may be.
const CacheTTL = 3 * time.Second

// FailureLimit is how many recent failures the summary lists.
const FailureLimit = 20

// Handler serves the transcription report.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time

	mu    sync.Mutex
	at    time.Time
	cache *Summary
}

// Mount wires the route.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/transcription/summary", h.Summary)
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc().UTC()
	}
	return time.Now().UTC()
}

// ---------- contract ----------

// Summary is the response of GET /api/transcription/summary.
type Summary struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Totals      Totals        `json:"totals"`
	Stages      []StageCounts `json:"stages"`
	Rate        Rates         `json:"rate"`
	ETA         ETA           `json:"eta"`
	Libraries   []LibraryRow  `json:"libraries"`
	Failures    []FailureRow  `json:"failures"`
}

// Totals counts the whole collection. Hours come from
// videos.duration_sec; a video without a duration counts as 0 hours.
type Totals struct {
	Videos            int     `json:"videos"`
	VideosTranscribed int     `json:"videos_transcribed"`
	AudioHours        float64 `json:"audio_hours"`
	HoursTranscribed  float64 `json:"hours_transcribed"`
	HoursRemaining    float64 `json:"hours_remaining"`
	Segments          int64   `json:"segments"`
}

// StageCounts is the state of each video's latest job in one stage.
// Pending includes paused rows; running includes claimed and resuming.
// Videos with no job in the stage appear in none of the counters.
type StageCounts struct {
	Stage   string `json:"stage"`
	Pending int    `json:"pending"`
	Running int    `json:"running"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
}

// Rates holds the throughput over the two windows, and the live speed.
type Rates struct {
	Last10m Window `json:"last_10m"`
	Last1h  Window `json:"last_1h"`
	Live    Live   `json:"live"`
}

// Live is the speed the transcription workers report while they work: the
// audio their decoding moved past in the last 10 minutes, from each worker's
// stt.audio_sec:<slot> counter in health_samples. Finished jobs lag it,
// since a worker decodes ~30 files side by side and each finishes only at
// its end, so it is the basis for the ETA. LostHours is what a failure or a
// stopped worker threw away in the window (stt.audio_lost:<slot>; files
// resume from a checkpoint saved every minute, so a restart loses little):
// shown on its own, as a one-off it is not part of the speed.
type Live struct {
	WindowSec      int     `json:"window_sec"`
	Workers        int     `json:"workers"`
	AudioHours     float64 `json:"audio_hours"`
	RealtimeFactor float64 `json:"realtime_factor"`
	LostHours      float64 `json:"lost_hours"`
}

// Window is the transcribe throughput over one window of wall time.
// AudioHoursPerHour is audio transcribed per wall-clock hour, which is
// also the whole fleet's speed as a multiple of realtime
// (RealtimeFactor; 30 means 30 hours of audio per hour). AvgJobRTF is
// the mean of the jobs' own output_summary.rtf, null when none report
// one.
type Window struct {
	WindowSec         int      `json:"window_sec"`
	Jobs              int      `json:"jobs"`
	AudioHours        float64  `json:"audio_hours"`
	AudioHoursPerHour float64  `json:"audio_hours_per_hour"`
	RealtimeFactor    float64  `json:"realtime_factor"`
	AvgJobRTF         *float64 `json:"avg_job_rtf"`
}

// ETA projects the remaining hours at the recent rate: the live speed when
// workers report one, else the 10-minute window when it saw work, else the
// hour. Null when nothing ran.
type ETA struct {
	Seconds *float64   `json:"seconds"`
	At      *time.Time `json:"at"`
	Basis   *string    `json:"basis"`
}

// LibraryRow is one library's share of the programme.
type LibraryRow struct {
	LibraryID  string  `json:"library_id"`
	Name       string  `json:"name"`
	Videos     int     `json:"videos"`
	VideosDone int     `json:"videos_done"`
	Hours      float64 `json:"hours"`
	HoursDone  float64 `json:"hours_done"`
}

// FailureRow is a failed job that is still the latest job of its
// (video, stage): a later retry that succeeded hides it.
type FailureRow struct {
	JobID       int64      `json:"job_id"`
	VideoID     string     `json:"video_id"`
	Title       string     `json:"title"`
	LibraryName *string    `json:"library_name"`
	Stage       string     `json:"stage"`
	ErrorCause  *string    `json:"error_cause"`
	Error       *string    `json:"error"`
	FinishedAt  *time.Time `json:"finished_at"`
	Attempts    int        `json:"attempts"`

	// libraryID is the job's (or its video's) library, for the per-caller
	// filter; never sent.
	libraryID string
}

// ---------- SQL ----------

const (
	// One pass over videos (150k rows) with a hash join to the active
	// transcripts (partial index transcripts_video_active_idx).
	librariesSQL = `
SELECT v.library_id::text, COALESCE(l.name, ''),
       count(*),
       count(*) FILTER (WHERE t.video_id IS NOT NULL),
       COALESCE(sum(v.duration_sec), 0)::float8,
       COALESCE(sum(v.duration_sec) FILTER (WHERE t.video_id IS NOT NULL), 0)::float8
  FROM videos v
  LEFT JOIN libraries l ON l.id = v.library_id
  LEFT JOIN (SELECT DISTINCT video_id FROM transcripts WHERE is_active) t ON t.video_id = v.id
 GROUP BY v.library_id, l.name
 ORDER BY COALESCE(l.name, ''), v.library_id`

	// The state of each video's newest job per stage. One hash/group
	// aggregate per (stage, video) instead of DISTINCT ON: the newest id
	// is compared with the newest id in each state bucket, which avoids
	// a sort of every job row (~2x faster on 700k rows).
	stageCountsSQL = `
SELECT stage,
       count(*) FILTER (WHERE p = last),
       count(*) FILTER (WHERE r = last),
       count(*) FILTER (WHERE d = last),
       count(*) FILTER (WHERE f = last)
  FROM (SELECT stage, subject_id, max(id) AS last,
               max(id) FILTER (WHERE state IN ('pending','paused')) AS p,
               max(id) FILTER (WHERE state IN ('claimed','running','resuming')) AS r,
               max(id) FILTER (WHERE state = 'done') AS d,
               max(id) FILTER (WHERE state = 'failed') AS f
          FROM processing_jobs
         WHERE subject_kind = 'video' AND stage = ANY($1::text[])
         GROUP BY stage, subject_id) latest
 GROUP BY stage`

	// Segments of the newest successful transcribe per video, from the
	// stage's output_summary: counting transcript_segments itself means
	// scanning tens of millions of rows.
	segmentsSQL = `
SELECT COALESCE(sum(n), 0)::bigint
  FROM (SELECT DISTINCT ON (subject_id)
               CASE WHEN jsonb_typeof(output_summary->'segments') = 'number'
                    THEN (output_summary->>'segments')::float8 ELSE 0 END AS n
          FROM processing_jobs
         WHERE stage = 'transcribe' AND subject_kind = 'video' AND state = 'done'
         ORDER BY subject_id, id DESC) s`

	// Finished transcribe jobs in the last hour (processing_jobs_finished_idx).
	// Audio seconds come from output_summary.audio_sec, else the video's duration.
	rateSQL = `
WITH w AS (
    SELECT j.finished_at,
           COALESCE(CASE WHEN jsonb_typeof(j.output_summary->'audio_sec') = 'number'
                         THEN (j.output_summary->>'audio_sec')::float8 END,
                    v.duration_sec, 0)::float8 AS audio_sec,
           CASE WHEN jsonb_typeof(j.output_summary->'rtf') = 'number'
                THEN (j.output_summary->>'rtf')::float8 END AS rtf
      FROM processing_jobs j
      LEFT JOIN videos v ON v.id = j.subject_id
     WHERE j.stage = 'transcribe' AND j.subject_kind = 'video' AND j.state = 'done'
       AND j.finished_at >= $1
)
SELECT count(*) FILTER (WHERE finished_at >= $2),
       COALESCE(sum(audio_sec) FILTER (WHERE finished_at >= $2), 0)::float8,
       avg(rtf) FILTER (WHERE finished_at >= $2),
       count(*),
       COALESCE(sum(audio_sec), 0)::float8,
       avg(rtf)
  FROM w`

	// Audio decoded and lost in the window ($1..now): the sum of each counter's
	// rises (the samples read start at $2, so the window's first interval has its
	// previous sample; a restarted worker continues its series from 0, and a drop
	// counts as a rise from 0). The speed is each worker's audio over the time it
	// was reporting (intervals of a minute or less: a stopped stack's gap is not
	// time spent decoding), summed over the workers still reporting ($3): a
	// worker that started 2 minutes ago is not read as idle for the other 8.
	liveSQL = `
WITH s AS (
    SELECT host, metric, at, value,
           lag(value) OVER w AS prev, lag(at) OVER w AS prev_at
      FROM health_samples
     WHERE (metric LIKE 'stt.audio_sec:%' OR metric LIKE 'stt.audio_lost:%') AND at >= $2
    WINDOW w AS (PARTITION BY metric, host ORDER BY at)
), d AS (
    SELECT host, metric, at,
           CASE WHEN value >= prev THEN value - prev ELSE value END AS d,
           extract(epoch FROM at - prev_at) AS dt
      FROM s
     WHERE at >= $1 AND prev IS NOT NULL
), per AS (
    SELECT host, metric, sum(d) AS amount,
           COALESCE(sum(dt) FILTER (WHERE dt <= 60), 0) AS covered, max(at) AS last
      FROM d GROUP BY host, metric
)
SELECT COALESCE(sum(amount) FILTER (WHERE metric LIKE 'stt.audio_sec:%'), 0)::float8,
       COALESCE(sum(amount) FILTER (WHERE metric LIKE 'stt.audio_lost:%'), 0)::float8,
       count(*) FILTER (WHERE metric LIKE 'stt.audio_sec:%' AND amount > 0 AND last >= $3),
       COALESCE(sum(amount / covered) FILTER (WHERE metric LIKE 'stt.audio_sec:%'
                AND covered >= 60 AND last >= $3), 0)::float8
  FROM per`

	failuresSQL = `
SELECT j.id, j.subject_id::text, COALESCE(v.title, v.filename, j.subject_id::text), l.name,
       j.stage, j.error_cause, j.error, j.finished_at, j.attempts,
       COALESCE(j.library_id, v.library_id)::text
  FROM processing_jobs j
  LEFT JOIN videos v ON v.id = j.subject_id
  LEFT JOIN libraries l ON l.id = COALESCE(j.library_id, v.library_id)
 WHERE j.state = 'failed' AND j.subject_kind = 'video' AND j.stage = ANY($1::text[])
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
 ORDER BY j.finished_at DESC NULLS LAST, j.id DESC
 LIMIT $2`
)

// ---------- handler ----------

// Summary implements GET /api/transcription/summary. The per-library rows
// and the failures are kept to the libraries the caller can read (the
// cache is shared, so it is filtered per request); the totals stay global.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	s, err := h.cached(r.Context(), h.now(), CacheTTL)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("transcription summary: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, visibleTo(s, p))
}

// visibleTo is s with Libraries and Failures kept to what p can read. A
// failure with no library is shown to all-library callers only.
func visibleTo(s *Summary, p *principal.Principal) *Summary {
	if p.AccessAllLibraries {
		return s
	}
	out := *s
	out.Libraries = []LibraryRow{}
	for _, l := range s.Libraries {
		if videos.CanRead(p, l.LibraryID) {
			out.Libraries = append(out.Libraries, l)
		}
	}
	out.Failures = []FailureRow{}
	for _, f := range s.Failures {
		if f.libraryID != "" && videos.CanRead(p, f.libraryID) {
			out.Failures = append(out.Failures, f)
		}
	}
	return &out
}

// ETATTL is how old a summary the Processing card's time left may come from:
// the queue snapshot is rebuilt every 2 s, the summary's queries take ~0.1 s.
const ETATTL = time.Minute

// ETASeconds is the transcription's time left for the Processing page's
// Transcribe card (queues.Handler.StageETA): the report's, at the live speed
// the workers decode at, rather than the card's finished files per minute,
// which lag (a worker decodes ~30 files side by side and restarts drop them).
func (h *Handler) ETASeconds(ctx context.Context, now time.Time) (*float64, error) {
	s, err := h.cached(ctx, now.UTC(), ETATTL)
	if err != nil {
		return nil, err
	}
	return s.ETA.Seconds, nil
}

// cached returns the summary built less than ttl ago, or builds one.
func (h *Handler) cached(ctx context.Context, now time.Time, ttl time.Duration) (*Summary, error) {
	h.mu.Lock()
	if h.cache != nil && now.Sub(h.at) < ttl {
		s := h.cache
		h.mu.Unlock()
		return s, nil
	}
	h.mu.Unlock()

	s, err := h.Build(ctx, now)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.cache, h.at = s, now
	h.mu.Unlock()
	return s, nil
}

// Build runs the queries and assembles a Summary as of now.
func (h *Handler) Build(ctx context.Context, now time.Time) (*Summary, error) {
	s := &Summary{GeneratedAt: now, Libraries: []LibraryRow{}, Failures: []FailureRow{}}

	if err := h.loadLibraries(ctx, s); err != nil {
		return nil, err
	}
	if err := h.loadStageCounts(ctx, s); err != nil {
		return nil, err
	}
	if err := h.DB.QueryRowContext(ctx, segmentsSQL).Scan(&s.Totals.Segments); err != nil {
		return nil, err
	}
	if err := h.loadRates(ctx, s, now); err != nil {
		return nil, err
	}
	if err := h.loadFailures(ctx, s); err != nil {
		return nil, err
	}
	s.ETA = eta(s.Totals.HoursRemaining, s.Rate, now)
	return s, nil
}

func (h *Handler) loadLibraries(ctx context.Context, s *Summary) error {
	rows, err := h.DB.QueryContext(ctx, librariesSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	var secs, doneSecs float64
	for rows.Next() {
		var id sql.NullString
		var row LibraryRow
		var hs, ds float64
		if err := rows.Scan(&id, &row.Name, &row.Videos, &row.VideosDone, &hs, &ds); err != nil {
			return err
		}
		row.LibraryID = id.String
		row.Hours, row.HoursDone = hours(hs), hours(ds)
		secs += hs
		doneSecs += ds
		s.Totals.Videos += row.Videos
		s.Totals.VideosTranscribed += row.VideosDone
		s.Libraries = append(s.Libraries, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.Totals.AudioHours = hours(secs)
	s.Totals.HoursTranscribed = hours(doneSecs)
	s.Totals.HoursRemaining = hours(math.Max(0, secs-doneSecs))
	return nil
}

func (h *Handler) loadStageCounts(ctx context.Context, s *Summary) error {
	idx := map[string]*StageCounts{}
	s.Stages = make([]StageCounts, len(Stages))
	for i, st := range Stages {
		s.Stages[i] = StageCounts{Stage: st}
		idx[st] = &s.Stages[i]
	}
	rows, err := h.DB.QueryContext(ctx, stageCountsSQL, pq.Array(Stages))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var stage string
		var c StageCounts
		if err := rows.Scan(&stage, &c.Pending, &c.Running, &c.Done, &c.Failed); err != nil {
			return err
		}
		if dst := idx[stage]; dst != nil {
			c.Stage = stage
			*dst = c
		}
	}
	return rows.Err()
}

func (h *Handler) loadRates(ctx context.Context, s *Summary, now time.Time) error {
	var jobs10, jobs60 int
	var sec10, sec60 float64
	var rtf10, rtf60 sql.NullFloat64
	err := h.DB.QueryRowContext(ctx, rateSQL, now.Add(-time.Hour), now.Add(-10*time.Minute)).
		Scan(&jobs10, &sec10, &rtf10, &jobs60, &sec60, &rtf60)
	if err != nil {
		return err
	}
	s.Rate.Last10m = window(600, jobs10, sec10, rtf10)
	s.Rate.Last1h = window(3600, jobs60, sec60, rtf60)
	var decoded, lost, speed float64
	var workers int
	start := now.Add(-10 * time.Minute)
	err = h.DB.QueryRowContext(ctx, liveSQL, start, start.Add(-2*time.Minute), now.Add(-90*time.Second)).
		Scan(&decoded, &lost, &workers, &speed)
	if err != nil {
		return err
	}
	s.Rate.Live = live(600, workers, decoded, lost, speed)
	return nil
}

func (h *Handler) loadFailures(ctx context.Context, s *Summary) error {
	rows, err := h.DB.QueryContext(ctx, failuresSQL, pq.Array(Stages), FailureLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var f FailureRow
		var lib, cause, msg, libID sql.NullString
		var fin sql.NullTime
		if err := rows.Scan(&f.JobID, &f.VideoID, &f.Title, &lib, &f.Stage, &cause, &msg, &fin, &f.Attempts, &libID); err != nil {
			return err
		}
		f.libraryID = libID.String
		f.LibraryName = strPtr(lib)
		f.ErrorCause = strPtr(cause)
		if msg.Valid {
			e := errorText(msg.String)
			f.Error = &e
		}
		if fin.Valid {
			t := fin.Time.UTC()
			f.FinishedAt = &t
		}
		s.Failures = append(s.Failures, f)
	}
	return rows.Err()
}

// ---------- pure helpers ----------

func window(sec int, jobs int, audioSec float64, rtf sql.NullFloat64) Window {
	w := Window{WindowSec: sec, Jobs: jobs, AudioHours: hours(audioSec)}
	speed := audioSec / float64(sec)
	w.RealtimeFactor = round2(speed)
	w.AudioHoursPerHour = round2(speed)
	if rtf.Valid {
		v := round2(rtf.Float64)
		w.AvgJobRTF = &v
	}
	return w
}

// live: speed is the workers' own (audio over the time each was reporting).
func live(sec, workers int, decoded, lost, speed float64) Live {
	return Live{WindowSec: sec, Workers: workers, AudioHours: hours(decoded),
		RealtimeFactor: round2(speed), LostHours: hours(math.Max(0, lost))}
}

// eta divides the remaining audio by the most recent non-zero speed.
func eta(remainingHours float64, r Rates, now time.Time) ETA {
	var basis string
	var speed float64
	switch {
	case r.Live.RealtimeFactor > 0:
		basis, speed = "live", r.Live.RealtimeFactor
	case r.Last10m.Jobs > 0 && r.Last10m.RealtimeFactor > 0:
		basis, speed = "10m", r.Last10m.RealtimeFactor
	case r.Last1h.Jobs > 0 && r.Last1h.RealtimeFactor > 0:
		basis, speed = "1h", r.Last1h.RealtimeFactor
	default:
		if remainingHours <= 0 {
			zero := 0.0
			at := now
			return ETA{Seconds: &zero, At: &at}
		}
		return ETA{}
	}
	sec := math.Round(remainingHours * 3600 / speed)
	at := now.Add(time.Duration(sec) * time.Second)
	return ETA{Seconds: &sec, At: &at, Basis: &basis}
}

func hours(sec float64) float64 { return round2(sec / 3600) }

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func strPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// errorText extracts a readable message from processing_jobs.error,
// which is JSON {kind,message,...} on engine rows and plain text on
// older ones (same rule as the queue handlers).
func errorText(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		var e struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(raw), &e) == nil && (e.Message != "" || e.Kind != "") {
			if e.Message != "" {
				return truncate(e.Message, 300)
			}
			return e.Kind
		}
	}
	return truncate(raw, 300)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
