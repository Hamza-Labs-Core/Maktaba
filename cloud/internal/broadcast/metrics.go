package broadcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Story 50.28 §4: the maktaba_broadcast_* series (names in shared/metrics/cloud-series.md), which
// 50.10's Prometheus scrapes from the api role and alerts on. The worker's minute poll writes the
// state into broadcast_monitor; every api node reads it (Monitor), so any node answers alike.

// Snapshot is the monitoring state the minute poll keeps.
type Snapshot struct {
	Minute             time.Time `json:"minute"`
	ChannelsLive       float64   `json:"channels_live"`
	Viewers            float64   `json:"viewers"`
	ViewerMinutesTotal float64   `json:"viewer_minutes_total"`
	// LagBuckets are cumulative counts per LagBuckets bound; LagCount and LagSumSeconds the
	// histogram's count and sum. All only ever grow.
	LagBuckets    []float64 `json:"lag_buckets"`
	LagCount      float64   `json:"lag_count"`
	LagSumSeconds float64   `json:"lag_sum_seconds"`
	CostUSD       float64   `json:"cost_usd"`
}

// Cost model (README §14.4, estimates; checked against the Cloudflare bill monthly).
const (
	CostWorkersPaidUSD     = 5.0   // Workers Paid, once per account a month
	CostPerViewerHourUSD   = 0.001 // the primary path (R2 + Worker)
	CostChannelMonthUSD    = 7.4   // an always-on channel, one rung, a month
	CostExtraRungMonthUSD  = 3.2   // each further rung (3 rungs ≈ $13.8)
	hoursPerMonthForPrices = 730.0
)

// ChannelHourCost is one live channel-hour's share of its monthly fixed cost.
func ChannelHourCost(rungs int) float64 {
	if rungs < 1 {
		rungs = 1
	}
	return (CostChannelMonthUSD + CostExtraRungMonthUSD*float64(rungs-1)) / hoursPerMonthForPrices
}

// CostEstimate is the month's estimate so far: the fixed price, each live channel-hour's share,
// and the viewer-hours.
func CostEstimate(viewerMinutes float64, liveChannelHoursByRungs map[int]float64) float64 {
	c := CostWorkersPaidUSD + viewerMinutes/60*CostPerViewerHourUSD
	for r, h := range liveChannelHoursByRungs {
		c += ChannelHourCost(r) * h
	}
	return c
}

// PollMinute reads the minute before last from Analytics Engine (the last one may still be
// arriving) and folds it into the snapshot, once per minute however many workers run it.
func (j *Jobs) PollMinute(ctx context.Context) error {
	if j.AE == nil {
		return ErrAnalyticsUnavailable
	}
	minute := j.now().Truncate(time.Minute).Add(-2 * time.Minute)
	rows, err := j.AE.Query(ctx, minuteQuery(minute, minute.Add(time.Minute)))
	if err != nil {
		return err
	}
	type live struct{ pub, ch string }
	viewers := map[live]float64{}
	channels := map[live]bool{}
	lag := make([]float64, len(LagBuckets))
	var lagN, lagMs, beats float64
	for _, r := range rows {
		k := live{r.Str("publisher"), r.Str("channel")}
		switch r.Str("event") {
		case "beat":
			viewers[k] += r.Num("n")
			beats += r.Num("n")
		case "ingest":
			channels[k] = true
			lagN += r.Num("n")
			lagMs += r.Num("lag_ms")
			for i := range LagBuckets {
				lag[i] += r.Num(fmt.Sprintf("le_%d", i))
			}
		}
	}
	cost, err := j.monthCost(ctx)
	if err != nil {
		return err
	}

	tx, err := j.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	var polled sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT snapshot, polled_minute FROM broadcast_monitor WHERE id = 1 FOR UPDATE`).Scan(&raw, &polled); err != nil {
		return err
	}
	if polled.Valid && !polled.Time.Before(minute) {
		return nil // another worker took this minute
	}
	var s Snapshot
	_ = json.Unmarshal(raw, &s)
	if len(s.LagBuckets) != len(LagBuckets) {
		s.LagBuckets = make([]float64, len(LagBuckets))
	}
	s.Minute = minute
	s.ChannelsLive = float64(len(channels))
	s.Viewers = beats
	s.ViewerMinutesTotal += beats
	for i := range lag {
		s.LagBuckets[i] += lag[i]
	}
	s.LagCount += lagN
	s.LagSumSeconds += lagMs / 1000
	s.CostUSD = cost
	out, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE broadcast_monitor SET snapshot = $1, polled_minute = $2, updated_at = now() WHERE id = 1`, out, minute); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM broadcast_live`); err != nil {
		return err
	}
	for k, n := range viewers {
		if k.pub == "" || k.ch == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO broadcast_live (publisher_id, channel_id, viewers, updated_at) VALUES ($1, $2, $3, $4)`,
			k.pub, k.ch, n, minute.Add(time.Minute)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// monthCost is CostEstimate over this UTC month's hourly rows.
func (j *Jobs) monthCost(ctx context.Context) (float64, error) {
	now := j.now()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	rows, err := j.DB.QueryContext(ctx, `
        SELECT rungs, COUNT(*) FILTER (WHERE segments > 0)::float8, COALESCE(SUM(viewer_minutes), 0)
          FROM broadcast_usage_hourly WHERE hour >= $1 GROUP BY rungs`, month)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	hours := map[int]float64{}
	var minutes float64
	for rows.Next() {
		var r int
		var h, m float64
		if err := rows.Scan(&r, &h, &m); err != nil {
			return 0, err
		}
		hours[r] += h
		minutes += m
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return CostEstimate(minutes, hours), nil
}

// RecordRevocations notes that the edge took the revocation list at `at` (50.25 calls it after a
// PUT /v1/control/revocations answered 2xx); maktaba_broadcast_revocation_list_age_seconds is the
// time since. Until the first, the series is absent and its alert quiet.
func RecordRevocations(ctx context.Context, db *sql.DB, at time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE broadcast_monitor SET revocations_at = GREATEST(COALESCE(revocations_at, $1), $1) WHERE id = 1`, at.UTC())
	return err
}

// Monitor serves the series on the api role, reading broadcast_monitor at most every 15 s.
type Monitor struct {
	DB *sql.DB
	// BudgetUSD is broadcast.monthly_budget_usd (0: no budget series, the 80 % alert quiet).
	BudgetUSD float64
	Now       func() time.Time

	mu      sync.Mutex
	at      time.Time
	snap    Snapshot
	revoked sql.NullTime
	ok      bool
}

const monitorCache = 15 * time.Second

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Monitor) load() (Snapshot, sql.NullTime, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ok && m.now().Sub(m.at) < monitorCache {
		return m.snap, m.revoked, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var raw []byte
	var rev sql.NullTime
	err := m.DB.QueryRowContext(ctx, `SELECT snapshot, revocations_at FROM broadcast_monitor WHERE id = 1`).Scan(&raw, &rev)
	if err != nil {
		// before the migration, or the database away: no broadcast series this scrape
		if m.ok && !errors.Is(err, sql.ErrNoRows) {
			return m.snap, m.revoked, true
		}
		return Snapshot{}, sql.NullTime{}, false
	}
	var s Snapshot
	_ = json.Unmarshal(raw, &s)
	m.snap, m.revoked, m.at, m.ok = s, rev, m.now(), true
	return s, rev, true
}

// WriteMetrics writes the maktaba_broadcast_* series (a PrometheusHandler extra). A nil Monitor
// writes nothing.
func (m *Monitor) WriteMetrics(w io.Writer) error {
	if m == nil || m.DB == nil {
		return nil
	}
	s, rev, ok := m.load()
	if !ok {
		return nil
	}
	return WriteSeries(w, s, rev, m.BudgetUSD, m.now())
}

// WriteSeries writes a snapshot as the maktaba_broadcast_* series.
func WriteSeries(w io.Writer, s Snapshot, rev sql.NullTime, budgetUSD float64, now time.Time) error {
	var b strings.Builder
	head := func(name, kind, help string) { fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind) }
	head("maktaba_broadcast_channels_live", "gauge", "Broadcast channels that ingested a segment in the last polled minute.")
	fmt.Fprintf(&b, "maktaba_broadcast_channels_live %g\n", s.ChannelsLive)
	head("maktaba_broadcast_viewers", "gauge", "Broadcast viewers watching in the last polled minute (heartbeats).")
	fmt.Fprintf(&b, "maktaba_broadcast_viewers %g\n", s.Viewers)
	head("maktaba_broadcast_viewer_minutes_total", "counter", "Broadcast viewer-minutes counted from heartbeats.")
	fmt.Fprintf(&b, "maktaba_broadcast_viewer_minutes_total %g\n", s.ViewerMinutesTotal)
	head("maktaba_broadcast_ingest_lag_seconds", "histogram", "From a segment's upload starting to its being in the playlist.")
	for i, le := range LagBuckets {
		v := 0.0
		if i < len(s.LagBuckets) {
			v = s.LagBuckets[i]
		}
		fmt.Fprintf(&b, "maktaba_broadcast_ingest_lag_seconds_bucket{le=%q} %g\n", trim(le), v)
	}
	fmt.Fprintf(&b, "maktaba_broadcast_ingest_lag_seconds_bucket{le=\"+Inf\"} %g\n", s.LagCount)
	fmt.Fprintf(&b, "maktaba_broadcast_ingest_lag_seconds_sum %g\n", s.LagSumSeconds)
	fmt.Fprintf(&b, "maktaba_broadcast_ingest_lag_seconds_count %g\n", s.LagCount)
	if rev.Valid {
		head("maktaba_broadcast_revocation_list_age_seconds", "gauge", "Seconds since the edge last took the revocation list.")
		fmt.Fprintf(&b, "maktaba_broadcast_revocation_list_age_seconds %g\n", now.Sub(rev.Time).Seconds())
	}
	head("maktaba_broadcast_edge_cost_estimate_usd", "gauge", "This month's estimated broadcast edge cost so far (README §14.4's model).")
	fmt.Fprintf(&b, "maktaba_broadcast_edge_cost_estimate_usd %g\n", s.CostUSD)
	if budgetUSD > 0 {
		head("maktaba_broadcast_monthly_budget_usd", "gauge", "broadcast.monthly_budget_usd.")
		fmt.Fprintf(&b, "maktaba_broadcast_monthly_budget_usd %g\n", budgetUSD)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func trim(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.3f", f), "0"), ".")
}
