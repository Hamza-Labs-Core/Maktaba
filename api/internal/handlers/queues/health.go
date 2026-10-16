package queues

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// rawRetention is how long 10 s samples are kept; older history is
// served from the rollup rows.
const rawRetention = 48 * time.Hour

const sparkPoints = 30

const (
	latestHealthSQL = `
SELECT DISTINCT ON (metric) metric, value
  FROM health_samples
 WHERE at > $1 AND host <> '*rollup*'
 ORDER BY metric, at DESC`

	timeouts10mSQL = `
SELECT COALESCE(sum(value), 0)
  FROM health_samples
 WHERE metric = 'nas.timeouts' AND host <> '*rollup*' AND at > $1`

	sparkSQL = `
SELECT metric, date_trunc('minute', at) AS t, avg(value), sum(value)
  FROM health_samples
 WHERE at > $1 AND host <> '*rollup*'
   AND metric IN ('nas.open_ms_p95', 'nas.timeouts', 'cpu.pct')
 GROUP BY metric, t
 ORDER BY t`

	// Raw samples inside the retention window, rollups before it.
	healthSeriesSQL = `
SELECT metric, to_timestamp(floor(extract(epoch FROM at) / $3::float8) * $3::float8) AS t,
       CASE WHEN metric = 'nas.timeouts' THEN sum(value) ELSE avg(value) END
  FROM health_samples
 WHERE at >= $1
   AND ((host <> '*rollup*' AND at >= $2) OR (host = '*rollup*' AND at < $2))
 GROUP BY metric, t
 ORDER BY metric, t`
)

// backlogRing keeps the in-memory backlog sparkline: one sample per
// minute, because backlog size is not written to health_samples.
type backlogRing struct {
	vals []float64
	last time.Time
}

func (b *backlogRing) record(now time.Time, ready int) {
	if !b.last.IsZero() && now.Sub(b.last) < time.Minute {
		if n := len(b.vals); n > 0 {
			b.vals[n-1] = float64(ready)
		}
		return
	}
	b.last = now
	b.vals = append(b.vals, float64(ready))
	if len(b.vals) > sparkPoints {
		b.vals = b.vals[len(b.vals)-sparkPoints:]
	}
}

func (b *backlogRing) snapshot() []float64 { return append([]float64{}, b.vals...) }

func ptrF(v float64) *float64 { return &v }

// healthNow assembles the health strip from the latest samples, the
// pool usage and the queue backlog.
func (h *Handler) healthNow(ctx context.Context, now time.Time, pools []Pool, queues []QueueSummary) (HealthNow, error) {
	var hn HealthNow
	hn.Spark = map[string][]float64{
		"nas_open_ms": {}, "nas_timeouts": {}, "cpu_pct": {}, "backlog_ready": {},
	}

	rows, err := h.DB.QueryContext(ctx, latestHealthSQL, now.Add(-5*time.Minute))
	if err != nil {
		return hn, err
	}
	latest := map[string]float64{}
	for rows.Next() {
		var m string
		var v float64
		if err := rows.Scan(&m, &v); err != nil {
			rows.Close()
			return hn, err
		}
		latest[m] = v
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return hn, err
	}
	rows.Close()
	if v, ok := latest["nas.open_ms_p50"]; ok {
		hn.NAS.OpenMsP50 = ptrF(v)
	}
	if v, ok := latest["nas.open_ms_p95"]; ok {
		hn.NAS.OpenMsP95 = ptrF(v)
	}
	if v, ok := latest["cpu.pct"]; ok {
		hn.CPU.Pct = ptrF(v)
	}
	if v, ok := latest["net.plex_ms"]; ok {
		hn.Network.PlexMs = ptrF(v)
	}
	if v, ok := latest["net.tv_ms"]; ok {
		hn.Network.TvMs = ptrF(v)
	}
	if v, ok := latest["net.youtube_ms"]; ok {
		hn.Network.YoutubeMs = ptrF(v)
	}

	var timeouts float64
	if err := h.DB.QueryRowContext(ctx, timeouts10mSQL, now.Add(-rateWindow)).Scan(&timeouts); err != nil && err != sql.ErrNoRows {
		return hn, err
	}
	hn.NAS.Timeouts = int(timeouts)

	for _, p := range pools {
		if p.Pool == "nas_read" {
			hn.NAS.ReadsBusy, hn.NAS.ReadsLimit = p.Busy, p.EffectiveSize
		}
	}

	srows, err := h.DB.QueryContext(ctx, sparkSQL, now.Add(-sparkPoints*time.Minute))
	if err != nil {
		return hn, err
	}
	for srows.Next() {
		var m string
		var t time.Time
		var avg, sum float64
		if err := srows.Scan(&m, &t, &avg, &sum); err != nil {
			srows.Close()
			return hn, err
		}
		switch m {
		case "nas.open_ms_p95":
			hn.Spark["nas_open_ms"] = append(hn.Spark["nas_open_ms"], avg)
		case "nas.timeouts":
			hn.Spark["nas_timeouts"] = append(hn.Spark["nas_timeouts"], sum)
		case "cpu.pct":
			hn.Spark["cpu_pct"] = append(hn.Spark["cpu_pct"], avg)
		}
	}
	if err := srows.Err(); err != nil {
		srows.Close()
		return hn, err
	}
	srows.Close()
	for k, v := range hn.Spark {
		if len(v) > sparkPoints {
			hn.Spark[k] = v[len(v)-sparkPoints:]
		}
	}

	var oldest *float64
	for _, q := range queues {
		hn.Backlog.Ready += q.Counts.Ready
		if q.OldestWaitingSec != nil && (oldest == nil || *q.OldestWaitingSec > *oldest) {
			oldest = q.OldestWaitingSec
		}
	}
	hn.Backlog.OldestWaitingSec = oldest

	h.ringMu.Lock()
	h.ring.record(now, hn.Backlog.Ready)
	hn.Spark["backlog_ready"] = h.ring.snapshot()
	h.ringMu.Unlock()
	return hn, nil
}

// Health implements GET /api/queues/health?hours=6: one series per
// metric, bucketed so a series never exceeds ~360 points.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	hours, e := common.QueryInt(r, "hours", 6)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if hours < 1 || hours > 168 {
		httperror.Write(w, r, httperror.InvalidQuery("hours must be between 1 and 168"))
		return
	}
	now := h.now()
	bucket := hours * 3600 / 360
	if bucket < 60 {
		bucket = 60
	}
	rows, err := h.DB.QueryContext(r.Context(), healthSeriesSQL,
		now.Add(-time.Duration(hours)*time.Hour), now.Add(-rawRetention), bucket)
	if err != nil {
		writeErr(w, r, "health series", err)
		return
	}
	defer rows.Close()
	by := map[string]*HealthSeries{}
	order := []string{}
	for rows.Next() {
		var m string
		var t time.Time
		var v float64
		if err := rows.Scan(&m, &t, &v); err != nil {
			writeErr(w, r, "health series", err)
			return
		}
		s := by[m]
		if s == nil {
			s = &HealthSeries{Metric: m, Points: []HealthPoint{}}
			by[m] = s
			order = append(order, m)
		}
		s.Points = append(s.Points, HealthPoint{T: t.UTC(), V: v})
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "health series", err)
		return
	}
	sort.Strings(order)
	out := make([]HealthSeries, 0, len(order))
	for _, m := range order {
		out = append(out, *by[m])
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"series": out})
}
