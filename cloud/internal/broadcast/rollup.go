package broadcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"
)

// Story 50.28 §2: the worker rolls the edge's events up every hour into broadcast_usage_hourly
// (kept 13 months), checks the rollup against metering once a day, and every minute refreshes the
// monitoring state behind the maktaba_broadcast_* series. Metering's truth is the edge's
// PublisherControl (50.20); turning this job off leaves metering untouched.

// Retention is how long hourly rows are kept.
const Retention = 13 * 31 * 24 * time.Hour

// settle is how long Analytics Engine is given to finish writing an hour or a minute.
const settle = 10 * time.Minute

// maxCatchUp bounds how many hours one run rolls up (an outage longer than this waits).
const maxCatchUp = 48

// Hourly is one publisher's channel in one hour.
type Hourly struct {
	Publisher      string
	Channel        string
	Hour           time.Time
	ViewerMinutes  float64
	Sessions       float64
	PeakConcurrent float64
	Countries      map[string]float64 // sessions started in the hour, by country
	Devices        map[string]float64 // sessions started in the hour, by device class
	Quality        map[string]float64 // viewer-minutes by rung
	RebufferRatio  float64            // rebuffer time / watch time
	StartupMsSum   float64            // sum of the plays' startup ms (average = / Sessions)
	Errors         float64
	ErrorsByCode   map[string]float64
	Blocked        map[string]float64 // by reason
	Segments       float64
	SegmentBytes   float64
	Rungs          int // distinct rungs ingested
}

type chKey struct{ pub, ch string }

// Aggregate builds an hour's rows from the hour query's rows and the peak query's rows. Pure:
// T02 runs it on a fake API's answer.
func Aggregate(hour time.Time, rows, peaks []Row) []Hourly {
	m := map[chKey]*Hourly{}
	rebuf := map[chKey]float64{}
	rungs := map[chKey]map[string]bool{}
	get := func(pub, ch string) *Hourly {
		k := chKey{pub, ch}
		h := m[k]
		if h == nil {
			h = &Hourly{Publisher: pub, Channel: ch, Hour: hour.UTC(), Countries: map[string]float64{}, Devices: map[string]float64{},
				Quality: map[string]float64{}, ErrorsByCode: map[string]float64{}, Blocked: map[string]float64{}}
			m[k] = h
		}
		return h
	}
	for _, r := range rows {
		pub, ch := r.Str("publisher"), r.Str("channel")
		if pub == "" || ch == "" {
			continue
		}
		h, n := get(pub, ch), r.Num("n")
		switch r.Str("event") {
		case "play":
			h.Sessions += n
			h.StartupMsSum += r.Num("startup_ms")
			h.Countries[orOther(r.Str("country"), "XX")] += n
			h.Devices[orOther(r.Str("device"), "web")] += n
		case "beat":
			mins := r.Num("minutes")
			h.ViewerMinutes += mins
			h.Quality[orOther(r.Str("rung"), "unknown")] += mins
			rebuf[chKey{pub, ch}] += r.Num("rebuffer_ms")
		case "error":
			h.Errors += n
			h.ErrorsByCode[orOther(r.Str("code"), "unknown")] += n
		case "blocked":
			h.Blocked[orOther(r.Str("code"), "unknown")] += n
		case "ingest":
			h.Segments += n
			h.SegmentBytes += r.Num("segment_bytes")
			k := chKey{pub, ch}
			if rungs[k] == nil {
				rungs[k] = map[string]bool{}
			}
			if rg := r.Str("rung"); rg != "" {
				rungs[k][rg] = true
			}
		}
	}
	for _, r := range peaks {
		pub, ch := r.Str("publisher"), r.Str("channel")
		if pub == "" || ch == "" {
			continue
		}
		h := get(pub, ch)
		h.PeakConcurrent = math.Max(h.PeakConcurrent, r.Num("n"))
	}
	out := make([]Hourly, 0, len(m))
	for k, h := range m {
		if h.ViewerMinutes > 0 {
			h.RebufferRatio = rebuf[k] / (h.ViewerMinutes * 60_000)
		}
		h.Rungs = len(rungs[k])
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Publisher != out[j].Publisher {
			return out[i].Publisher < out[j].Publisher
		}
		return out[i].Channel < out[j].Channel
	})
	return out
}

func orOther(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Jobs are the worker's analytics jobs.
type Jobs struct {
	DB     *sql.DB
	AE     Querier
	Logger *slog.Logger
	Now    func() time.Time
}

func (j *Jobs) now() time.Time {
	if j.Now != nil {
		return j.Now().UTC()
	}
	return time.Now().UTC()
}

// Run ticks every minute: the monitor's minute, then any hour to roll up, then the daily check.
func (j *Jobs) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		j.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs each job once; each one's failure is logged and retried at the next tick.
func (j *Jobs) Tick(ctx context.Context) {
	if err := j.PollMinute(ctx); err != nil && !errors.Is(err, ErrAnalyticsUnavailable) {
		j.Logger.Warn("broadcast monitor poll failed", "err", err)
	}
	if _, err := j.CatchUp(ctx); err != nil {
		j.Logger.Warn("broadcast rollup waits", "err", err)
	}
	if _, err := j.CrossCheck(ctx); err != nil {
		j.Logger.Warn("broadcast cross-check failed", "err", err)
	}
}

// RollHour rolls one hour up: its rows replace whatever the hour held (a re-run is the same).
func (j *Jobs) RollHour(ctx context.Context, hour time.Time) (int, error) {
	hour = hour.UTC().Truncate(time.Hour)
	rows, err := j.AE.Query(ctx, hourQuery(hour, hour.Add(time.Hour)))
	if err != nil {
		return 0, err
	}
	peaks, err := j.AE.Query(ctx, peakQuery(hour, hour.Add(time.Hour)))
	if err != nil {
		return 0, err
	}
	hs := Aggregate(hour, rows, peaks)
	tx, err := j.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM broadcast_usage_hourly WHERE hour = $1`, hour); err != nil {
		return 0, err
	}
	for _, h := range hs {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO broadcast_usage_hourly (publisher_id, channel_id, hour, viewer_minutes, sessions, peak_concurrent,
                countries, devices, quality, rebuffer_ratio, startup_ms_sum, errors, errors_by_code, blocked,
                segments, segment_bytes, rungs)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
			h.Publisher, h.Channel, h.Hour, h.ViewerMinutes, h.Sessions, h.PeakConcurrent,
			jsonMap(h.Countries), jsonMap(h.Devices), jsonMap(h.Quality), h.RebufferRatio, h.StartupMsSum,
			h.Errors, jsonMap(h.ErrorsByCode), jsonMap(h.Blocked), h.Segments, h.SegmentBytes, h.Rungs); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE broadcast_monitor SET rolled_through = GREATEST(COALESCE(rolled_through, $1), $1), updated_at = now() WHERE id = 1`, hour); err != nil {
		return 0, err
	}
	return len(hs), tx.Commit()
}

func jsonMap(m map[string]float64) []byte {
	if m == nil {
		m = map[string]float64{}
	}
	b, _ := json.Marshal(m)
	return b
}

// CatchUp rolls up every finished hour after the last one rolled (at most maxCatchUp), stopping
// at the first failure: the SQL API down means the hour is tried again at the next tick. Then it
// deletes rows past the retention.
func (j *Jobs) CatchUp(ctx context.Context) (int, error) {
	if j.AE == nil {
		return 0, nil
	}
	last := j.now().Add(-settle).Truncate(time.Hour).Add(-time.Hour) // the newest finished hour
	var through sql.NullTime
	if err := j.DB.QueryRowContext(ctx, `SELECT rolled_through FROM broadcast_monitor WHERE id = 1`).Scan(&through); err != nil {
		return 0, err
	}
	next := last
	if through.Valid {
		next = through.Time.UTC().Add(time.Hour)
	}
	if last.Sub(next) > maxCatchUp*time.Hour {
		next = last.Add(-(maxCatchUp - 1) * time.Hour)
	}
	done := 0
	for h := next; !h.After(last); h = h.Add(time.Hour) {
		if _, err := j.RollHour(ctx, h); err != nil {
			return done, fmt.Errorf("hour %s: %w", h.Format(time.RFC3339), err)
		}
		done++
	}
	if done > 0 {
		if _, err := j.DB.ExecContext(ctx, `DELETE FROM broadcast_usage_hourly WHERE hour < $1`, j.now().Add(-Retention)); err != nil {
			return done, err
		}
	}
	return done, nil
}

// MismatchLimit is the daily difference between the rollup and metering that raises a SEV3
// (README §14.8, the story's §2): 2 %. Below MismatchFloor minutes it is noise and stays quiet.
const (
	MismatchLimit = 0.02
	MismatchFloor = 10.0
)

// Mismatch is one publisher's day off by more than the limit.
type Mismatch struct {
	Publisher string
	Rollup    float64
	Metered   float64
	Ratio     float64
}

// Compare is the daily check: each publisher's rolled-up viewer-minutes against metering's.
func Compare(rollup, metered map[string]float64) []Mismatch {
	pubs := map[string]bool{}
	for p := range rollup {
		pubs[p] = true
	}
	for p := range metered {
		pubs[p] = true
	}
	var out []Mismatch
	for p := range pubs {
		r, m := rollup[p], metered[p]
		diff := math.Abs(r - m)
		ratio := diff / math.Max(m, 1)
		if diff >= MismatchFloor && ratio > MismatchLimit {
			out = append(out, Mismatch{Publisher: p, Rollup: r, Metered: m, Ratio: ratio})
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Publisher < out[k].Publisher })
	return out
}

// MismatchCode is the error code of the SEV3 line: maktaba_errors_total{code=…} pages through
// 50.10's broadcast_metering_mismatch rule.
const MismatchCode = "broadcast_metering_mismatch"

// CrossCheck compares yesterday (UTC) once a day, after 01:00 when the day's last hour is rolled
// up. Metering's windows are 50.20's broadcast_usage_windows; before that table exists there is
// nothing to compare and the day passes quietly.
func (j *Jobs) CrossCheck(ctx context.Context) ([]Mismatch, error) {
	now := j.now()
	day := now.Truncate(24 * time.Hour).Add(-24 * time.Hour)
	if now.Sub(day) < 25*time.Hour+settle {
		return nil, nil
	}
	var rolled sql.NullTime
	var checked sql.NullTime
	if err := j.DB.QueryRowContext(ctx, `SELECT rolled_through, checked_day FROM broadcast_monitor WHERE id = 1`).Scan(&rolled, &checked); err != nil {
		return nil, err
	}
	if checked.Valid && !checked.Time.Before(day) {
		return nil, nil
	}
	if !rolled.Valid || rolled.Time.Before(day.Add(23*time.Hour)) {
		return nil, nil // the day isn't rolled up yet
	}
	// claim the day: of two workers, one checks
	res, err := j.DB.ExecContext(ctx, `UPDATE broadcast_monitor SET checked_day = $1 WHERE id = 1 AND (checked_day IS NULL OR checked_day < $1)`, day)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	var exists sql.NullString
	if err := j.DB.QueryRowContext(ctx, `SELECT to_regclass('broadcast_usage_windows')::text`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists.Valid {
		return nil, nil
	}
	rollup, err := sums(ctx, j.DB, `SELECT publisher_id, SUM(viewer_minutes) FROM broadcast_usage_hourly
        WHERE hour >= $1 AND hour < $2 GROUP BY publisher_id`, day, day.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	metered, err := sums(ctx, j.DB, `SELECT publisher_id::text, SUM(minutes_full + minutes_lowest)::float8 FROM broadcast_usage_windows
        WHERE window_start >= $1 AND window_start < $2 GROUP BY publisher_id`, day, day.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	ms := Compare(rollup, metered)
	for _, m := range ms {
		j.Logger.Error("broadcast analytics and metering disagree", "code", MismatchCode,
			"home_h", HashID(m.Publisher), "day", day.Format("2006-01-02"),
			"rollup_minutes", m.Rollup, "metered_minutes", m.Metered, "ratio", m.Ratio)
	}
	return ms, nil
}

func sums(ctx context.Context, db *sql.DB, q string, args ...any) (map[string]float64, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]float64{}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// HashID is a publisher's id as log lines carry it (53.01: hashed, never in the clear): the
// edge's FNV-1a 64 (web/packages/broadcast-edge/src/log.ts), so both sides name it alike.
func HashID(id string) string {
	h := uint64(0xcbf29ce484222325)
	for _, c := range []byte(id) {
		h ^= uint64(c)
		h *= 0x100000001b3
	}
	return fmt.Sprintf("%016x", h)
}
