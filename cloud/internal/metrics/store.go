package metrics

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// Store is the Postgres layer for relay metrics. The relay role writes
// (FlushRaw, Rollup, purge); the api role reads (dashboard + export).
// Both share the same cloud database (README D8).
type Store struct {
	DB *sql.DB
}

// NewStore wires a Store to a pool.
func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

// FlushRaw upserts a batch of per-minute aggregate rows. Additive on
// conflict so two flushes in the same minute compose (README D4).
func (s *Store) FlushRaw(ctx context.Context, rows []RawRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	const q = `
        INSERT INTO relay_metrics_raw (bucket, metric, country, value, samples)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (bucket, metric, country) DO UPDATE SET
            value   = relay_metrics_raw.value   + EXCLUDED.value,
            samples = relay_metrics_raw.samples + EXCLUDED.samples`
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, q, r.Bucket, r.Metric, r.Country, r.Value, r.Samples); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Rollup aggregates completed-hour raw rows into the hourly table. It
// recomputes each hour fully and overwrites, so repeated runs are
// idempotent. Only hours strictly before the current hour are rolled
// (the in-progress hour keeps accumulating in raw).
func (s *Store) Rollup(ctx context.Context, now time.Time) error {
	curHour := now.UTC().Truncate(time.Hour)
	const q = `
        INSERT INTO relay_metrics_hourly (hour, metric, country, sum_value, max_value, samples)
        SELECT date_trunc('hour', bucket) AS hour, metric, country,
               SUM(value)   AS sum_value,
               MAX(value)   AS max_value,
               SUM(samples) AS samples
        FROM relay_metrics_raw
        WHERE bucket < $1
        GROUP BY date_trunc('hour', bucket), metric, country
        ON CONFLICT (hour, metric, country) DO UPDATE SET
            sum_value = EXCLUDED.sum_value,
            max_value = EXCLUDED.max_value,
            samples   = EXCLUDED.samples`
	_, err := s.DB.ExecContext(ctx, q, curHour)
	return err
}

// PurgeRaw deletes raw rows older than 24 h (Story 30.1).
func (s *Store) PurgeRaw(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.UTC().Add(-RawRetentionHours * time.Hour)
	res, err := s.DB.ExecContext(ctx, `DELETE FROM relay_metrics_raw WHERE bucket < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RawRetentionHours bounds raw-row lifetime (24 h). Mirrors
// privacy.RawRetentionHours, duplicated here to avoid an import cycle
// when the Runner purges.
const RawRetentionHours = 24

// ─── dashboard / export reads ──────────────────────────────────────────

// CounterTotals sums each counter metric over [start, now) across the
// hourly rollup plus the still-raw current window, keyed by metric.
func (s *Store) CounterTotals(ctx context.Context, start time.Time) (map[string]int64, error) {
	out := map[string]int64{}
	const q = `
        SELECT metric, COALESCE(SUM(v),0) FROM (
            SELECT metric, sum_value AS v FROM relay_metrics_hourly WHERE hour >= $1
            UNION ALL
            SELECT metric, value AS v FROM relay_metrics_raw
                WHERE bucket >= $1 AND bucket >= date_trunc('hour', now())
        ) t GROUP BY metric`
	rows, err := s.DB.QueryContext(ctx, q, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var v int64
		if err := rows.Scan(&m, &v); err != nil {
			return nil, err
		}
		out[m] = v
	}
	return out, rows.Err()
}

// SeriesPoint is one hour of one metric for the bandwidth graph.
type SeriesPoint struct {
	Hour   time.Time `json:"hour"`
	Metric string    `json:"metric"`
	Value  int64     `json:"value"`
}

// Series returns hourly sums for the given metrics over [start, now),
// ascending by hour.
func (s *Store) Series(ctx context.Context, metricNames []string, start time.Time) ([]SeriesPoint, error) {
	const q = `
        SELECT hour, metric, sum_value
        FROM relay_metrics_hourly
        WHERE hour >= $1 AND metric = ANY($2)
        ORDER BY hour ASC, metric ASC`
	rows, err := s.DB.QueryContext(ctx, q, start, pq.Array(metricNames))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesPoint{}
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.Hour, &p.Metric, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GeoPoint is one country's request total for the heatmap.
type GeoPoint struct {
	Country  string `json:"country"`
	Requests int64  `json:"requests"`
}

// Geo returns request totals by country over [start, now), descending.
// Empty country codes are reported as "unknown".
func (s *Store) Geo(ctx context.Context, start time.Time) ([]GeoPoint, error) {
	const q = `
        SELECT CASE WHEN country = '' THEN 'unknown' ELSE country END AS c,
               COALESCE(SUM(sum_value),0) AS reqs
        FROM relay_metrics_hourly
        WHERE hour >= $1 AND metric = $2
        GROUP BY c
        ORDER BY reqs DESC`
	rows, err := s.DB.QueryContext(ctx, q, start, MetricRequests)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GeoPoint{}
	for rows.Next() {
		var g GeoPoint
		if err := rows.Scan(&g.Country, &g.Requests); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ExportRows dumps the hourly rollup over [start, now) for CSV/JSON
// export (Story 30.4).
func (s *Store) ExportRows(ctx context.Context, start time.Time) ([]ExportRow, error) {
	const q = `
        SELECT hour, metric, country, sum_value, max_value, samples
        FROM relay_metrics_hourly
        WHERE hour >= $1
        ORDER BY hour ASC, metric ASC, country ASC`
	rows, err := s.DB.QueryContext(ctx, q, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportRow{}
	for rows.Next() {
		var (
			h time.Time
			r ExportRow
		)
		if err := rows.Scan(&h, &r.Metric, &r.Country, &r.SumValue, &r.MaxValue, &r.Samples); err != nil {
			return nil, err
		}
		r.Hour = h.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LiveSampleMaxAge is how old the newest gauge sample may be before the
// overview reports the relay as having nothing connected: the relay
// flushes every minute, so a sample older than this means no relay is
// running (or none can reach the database).
const LiveSampleMaxAge = 3 * time.Minute

// LatestGauges reads the newest connected_servers / active_tunnels sample
// the relay role flushed (each minute row holds value summed over its
// samples, so value/samples is that minute's reading). It is how the api
// role, a separate process from the relay's registry, shows live counts
// (story 30.3). Samples older than maxAge count as zero; ok is false then.
func (s *Store) LatestGauges(ctx context.Context, now time.Time, maxAge time.Duration) (servers, tunnels int, ok bool, err error) {
	const q = `
        SELECT DISTINCT ON (metric) metric, value, samples
        FROM relay_metrics_raw
        WHERE metric IN ($1, $2) AND country = '' AND samples > 0 AND bucket >= $3
        ORDER BY metric, bucket DESC`
	rows, err := s.DB.QueryContext(ctx, q, MetricConnectedServers, MetricActiveTunnels, now.UTC().Add(-maxAge))
	if err != nil {
		return 0, 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var v int64
		var n int
		if err := rows.Scan(&m, &v, &n); err != nil {
			return 0, 0, false, err
		}
		ok = true
		avg := gaugeReading(v, n)
		if m == MetricConnectedServers {
			servers = avg
		} else {
			tunnels = avg
		}
	}
	return servers, tunnels, ok, rows.Err()
}

// gaugeReading turns a summed gauge row back into one reading (rounded).
func gaugeReading(sum int64, samples int) int {
	if samples <= 0 {
		return 0
	}
	return int((sum + int64(samples)/2) / int64(samples))
}

// ServerCount is the total registered servers (not just connected).
func (s *Store) ServerCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM servers`).Scan(&n)
	return n, err
}

// PushStat carries push-delivery counts over a window.
type PushStat struct {
	Sent   int64 `json:"sent"`
	Failed int64 `json:"failed"`
}

// PushStats reads delivery outcomes from push_dispatch_log over [start,
// now) — the authoritative source (README D6). status 'ok' = sent,
// anything else = failed.
func (s *Store) PushStats(ctx context.Context, start time.Time) (PushStat, error) {
	var ps PushStat
	const q = `
        SELECT
            COALESCE(SUM(CASE WHEN status = 'ok'  THEN 1 ELSE 0 END), 0),
            COALESCE(SUM(CASE WHEN status <> 'ok' THEN 1 ELSE 0 END), 0)
        FROM push_dispatch_log
        WHERE sent_at >= $1`
	err := s.DB.QueryRowContext(ctx, q, start).Scan(&ps.Sent, &ps.Failed)
	return ps, err
}

// PlanCount is one subscription-tier bucket.
type PlanCount struct {
	Plan  string `json:"plan"`
	Users int64  `json:"users"`
}

// Subscriptions returns the subscriber breakdown by plan from users.
func (s *Store) Subscriptions(ctx context.Context) ([]PlanCount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT plan, count(*) FROM users GROUP BY plan ORDER BY count(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlanCount{}
	for rows.Next() {
		var p PlanCount
		if err := rows.Scan(&p.Plan, &p.Users); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
