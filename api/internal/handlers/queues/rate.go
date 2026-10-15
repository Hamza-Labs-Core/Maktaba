package queues

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// rateBucketSec is the fixed bucket width of the rate chart.
const rateBucketSec = 600

const rateSQL = `
SELECT floor(extract(epoch FROM finished_at) / 600)::bigint * 600 AS bucket,
       count(*) FILTER (WHERE state = 'done' AND outcome IS DISTINCT FROM 'skipped_unchanged'),
       count(*) FILTER (WHERE state = 'failed'),
       count(*) FILTER (WHERE outcome = 'skipped_unchanged')
  FROM processing_jobs
 WHERE stage = $1 AND finished_at >= $2 AND state IN ('done', 'failed')
 GROUP BY bucket
 ORDER BY bucket`

const rateTimeoutsSQL = `
SELECT floor(extract(epoch FROM at) / 600)::bigint * 600 AS bucket, COALESCE(sum(value), 0)
  FROM health_samples
 WHERE metric = 'nas.timeouts' AND host <> '*rollup*' AND at >= $1
 GROUP BY bucket`

// Rate implements GET /api/queues/{stage}/rate?hours=24 (1-168).
// Buckets with no activity are returned as zeros so the chart axis is
// continuous.
func (h *Handler) Rate(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	hours, e := common.QueryInt(r, "hours", 24)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if hours < 1 || hours > 168 {
		httperror.Write(w, r, httperror.InvalidQuery("hours must be between 1 and 168"))
		return
	}
	if _, ok := h.stageOr404(w, r, stage); !ok {
		return
	}
	now := h.now()
	since := now.Add(-time.Duration(hours) * time.Hour)
	startBucket := since.Unix() / rateBucketSec * rateBucketSec
	since = time.Unix(startBucket, 0).UTC()

	type cell struct{ done, failed, skipped, timeouts int }
	cells := map[int64]*cell{}
	get := func(b int64) *cell {
		c := cells[b]
		if c == nil {
			c = &cell{}
			cells[b] = c
		}
		return c
	}

	rows, err := h.DB.QueryContext(r.Context(), rateSQL, stage, since)
	if err != nil {
		writeErr(w, r, "rate", err)
		return
	}
	for rows.Next() {
		var b int64
		var done, failed, skipped int
		if err := rows.Scan(&b, &done, &failed, &skipped); err != nil {
			rows.Close()
			writeErr(w, r, "rate", err)
			return
		}
		c := get(b)
		c.done, c.failed, c.skipped = done, failed, skipped
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeErr(w, r, "rate", err)
		return
	}
	rows.Close()

	trows, err := h.DB.QueryContext(r.Context(), rateTimeoutsSQL, since)
	if err != nil {
		writeErr(w, r, "rate", err)
		return
	}
	for trows.Next() {
		var b int64
		var n float64
		if err := trows.Scan(&b, &n); err != nil {
			trows.Close()
			writeErr(w, r, "rate", err)
			return
		}
		get(b).timeouts = int(n)
	}
	if err := trows.Err(); err != nil {
		trows.Close()
		writeErr(w, r, "rate", err)
		return
	}
	trows.Close()

	points := []RatePoint{}
	for b := startBucket; b <= now.Unix(); b += rateBucketSec {
		p := RatePoint{T: time.Unix(b, 0).UTC()}
		if c := cells[b]; c != nil {
			p.Done, p.Failed, p.Skipped, p.NasTimeouts = c.done, c.failed, c.skipped, c.timeouts
		}
		points = append(points, p)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"bucket_sec": rateBucketSec, "points": points})
}
