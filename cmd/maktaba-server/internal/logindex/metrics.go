package logindex

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dashboards (Epic 53 story 53.12): the logs role scrapes this machine's /metrics every 15 s
// (the API's admin port, which merges the supervisor's), keeps an allow-list of series
// (dashboard-series.json) at one minute for 7 days and one hour for 30 days, and answers a
// board's series in one call. Counters are kept as a rate per minute, gauges as their last
// value (and min/max/avg by the hour). A series not on the list, or a label set past its
// cap, is not stored; how many were dropped is kept as maktaba_metric_dropped_total.

const metricsSchema = `
CREATE TABLE IF NOT EXISTS metric_1m (name TEXT NOT NULL, labels TEXT NOT NULL, minute INTEGER NOT NULL,
  value REAL NOT NULL, PRIMARY KEY (name, labels, minute));
CREATE TABLE IF NOT EXISTS metric_1h (name TEXT NOT NULL, labels TEXT NOT NULL, hour INTEGER NOT NULL,
  value REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, PRIMARY KEY (name, labels, hour));
CREATE INDEX IF NOT EXISTS metric_1m_minute ON metric_1m(minute);
CREATE INDEX IF NOT EXISTS metric_1h_hour ON metric_1h(hour);
`

//go:embed dashboard-series.json
var seriesJSON []byte

// SeriesDef is one allowed series.
type SeriesDef struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // counter or gauge
	Board    string `json:"board"`
	IDLabels bool   `json:"id_labels"`
}

type seriesList struct {
	LabelCap   int         `json:"label_cap"`
	IDLabelCap int         `json:"id_label_cap"`
	Series     []SeriesDef `json:"series"`
}

// AllowedSeries is the allow-list (dashboard-series.json).
func AllowedSeries() (map[string]SeriesDef, int, int) {
	var l seriesList
	if err := json.Unmarshal(seriesJSON, &l); err != nil {
		panic("logindex: dashboard-series.json: " + err.Error())
	}
	out := map[string]SeriesDef{}
	for _, s := range l.Series {
		out[s.Name] = s
	}
	return out, l.LabelCap, l.IDLabelCap
}

// DroppedSeries is the name the store's own drop count is kept under.
const DroppedSeries = "maktaba_metric_dropped_total"

// sample is one line of the text format.
type sample struct {
	name, labels string
	value        float64
}

// parseMetrics reads the Prometheus text format (and OpenMetrics' lines alike): comments are
// skipped, labels sorted into "k=v,k=v" so one label set is one key.
func parseMetrics(r io.Reader) []sample {
	var out []sample
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		var name, rest, labels string
		if i := strings.IndexByte(line, '{'); i >= 0 {
			j := strings.LastIndexByte(line, '}')
			if j < i {
				continue
			}
			name, labels, rest = line[:i], canonLabels(line[i+1:j]), strings.TrimSpace(line[j+1:])
		} else if i := strings.IndexAny(line, " \t"); i >= 0 {
			name, rest = line[:i], strings.TrimSpace(line[i+1:])
		} else {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(f[0], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out = append(out, sample{name: name, labels: labels, value: v})
	}
	return out
}

// canonLabels sorts a label list (`a="1",b="x"`) into `a=1,b=x`.
func canonLabels(s string) string {
	var parts []string
	for len(s) > 0 {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		k := strings.TrimSpace(strings.TrimPrefix(s[:eq], ","))
		s = s[eq+1:]
		if len(s) == 0 || s[0] != '"' {
			break
		}
		// the value runs to the next unescaped quote
		var v strings.Builder
		i := 1
		for ; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) {
				v.WriteByte(s[i+1])
				i++
				continue
			}
			if s[i] == '"' {
				break
			}
			v.WriteByte(s[i])
		}
		parts = append(parts, k+"="+v.String())
		if i+1 > len(s) {
			break
		}
		s = strings.TrimPrefix(s[i+1:], ",")
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// metricsState is what the scraper remembers between scrapes.
type metricsState struct {
	mu      sync.Mutex
	last    map[string]float64         // a counter's last value, by name+labels
	sets    map[string]map[string]bool // the label sets kept, by name
	dropped float64                    // series or label sets not stored
	allowed map[string]SeriesDef       // the allow-list
	caps    [2]int                     // label cap, id label cap
}

func newMetricsState() *metricsState {
	a, c, ic := AllowedSeries()
	return &metricsState{last: map[string]float64{}, sets: map[string]map[string]bool{}, allowed: a,
		caps: [2]int{c, ic}}
}

func minuteOf(t time.Time) int64 { return t.Unix() / 60 }

// Record stores one scrape taken at `at`: an allowed counter's increase since the last
// scrape is added to this minute's rate, a gauge's value replaces it.
func (ix *Index) Record(ctx context.Context, samples []sample, at time.Time) error {
	st := ix.metrics
	st.mu.Lock()
	defer st.mu.Unlock()
	minute := minuteOf(at)
	type row struct {
		name, labels string
		v            float64
		add          bool
	}
	var rows []row
	for _, s := range samples {
		def, ok := st.allowed[s.name]
		if !ok {
			continue // not on the list: not counted as dropped (most metrics are not wanted)
		}
		set := st.sets[s.name]
		if set == nil {
			set = map[string]bool{}
			st.sets[s.name] = set
		}
		if !set[s.labels] {
			limit := st.caps[0]
			if def.IDLabels {
				limit = st.caps[1]
			}
			if len(set) >= limit {
				st.dropped++
				continue
			}
			set[s.labels] = true
		}
		key := s.name + "{" + s.labels + "}"
		if def.Kind == "counter" {
			prev, seen := st.last[key]
			st.last[key] = s.value
			if !seen {
				continue // the first scrape gives no increase
			}
			d := s.value - prev
			if d < 0 {
				d = s.value // the process restarted: its counter began again
			}
			rows = append(rows, row{s.name, s.labels, d, true})
			continue
		}
		rows = append(rows, row{s.name, s.labels, s.value, false})
	}
	rows = append(rows, row{DroppedSeries, "", st.dropped, false})
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, r := range rows {
		q := `INSERT INTO metric_1m(name, labels, minute, value) VALUES(?, ?, ?, ?)
			ON CONFLICT(name, labels, minute) DO UPDATE SET value = excluded.value`
		if r.add {
			q = `INSERT INTO metric_1m(name, labels, minute, value) VALUES(?, ?, ?, ?)
				ON CONFLICT(name, labels, minute) DO UPDATE SET value = value + excluded.value`
		}
		if _, err := tx.Exec(q, r.name, r.labels, minute, r.v); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// RecordSizes stores the log store's own sizes (the Storage board).
func (ix *Index) RecordSizes(ctx context.Context, at time.Time) error {
	seg := dirBytes(filepath.Join(ix.root, "segments"))
	return ix.Record(ctx, []sample{{name: "maktaba_logs_segments_bytes", value: float64(seg)},
		{name: "maktaba_logs_index_bytes", value: float64(ix.Size())}}, at)
}

func dirBytes(dir string) int64 {
	var n int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// Rollup turns every closed hour's minutes into one row each (avg, min, max) and is
// idempotent: an hour already rolled up is rolled up again with the same answer.
func (ix *Index) Rollup(ctx context.Context) error {
	nowHour := ix.now().Unix() / 3600
	var start int64
	_ = ix.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(hour), 0) FROM metric_1h`).Scan(&start)
	if start == 0 {
		var m int64
		_ = ix.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(minute), 0) FROM metric_1m`).Scan(&m)
		start = m / 60
	}
	if start == 0 {
		return nil
	}
	_, err := ix.db.ExecContext(ctx, `INSERT INTO metric_1h(name, labels, hour, value, min, max)
		SELECT name, labels, minute / 60, AVG(value), MIN(value), MAX(value) FROM metric_1m
		WHERE minute / 60 >= ? AND minute / 60 < ? GROUP BY name, labels, minute / 60
		ON CONFLICT(name, labels, hour) DO UPDATE SET value = excluded.value, min = excluded.min, max = excluded.max`,
		start, nowHour)
	return err
}

// sweepMetrics keeps 7 days of minutes and 30 days of hours.
func (ix *Index) sweepMetrics(ctx context.Context) error {
	now := ix.now().Unix()
	if _, err := ix.db.ExecContext(ctx, `DELETE FROM metric_1m WHERE minute < ?`, (now-7*86400)/60); err != nil {
		return err
	}
	_, err := ix.db.ExecContext(ctx, `DELETE FROM metric_1h WHERE hour < ?`, (now-30*86400)/3600)
	return err
}

// Scrape reads each URL's metrics and records them, with the store's own sizes.
func (ix *Index) Scrape(ctx context.Context, urls []string, client *http.Client) {
	at := ix.now()
	var all []sample
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue // that part is down: its series have a gap
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			all = append(all, parseMetrics(bytes.NewReader(b))...)
		}
	}
	if err := ix.Record(ctx, all, at); err != nil {
		ix.Log("metrics_record_failed", "err", err)
	}
	if err := ix.RecordSizes(ctx, at); err != nil {
		ix.Log("metrics_record_failed", "err", err)
	}
}

// ---- boards ----

// Ranges are a board's time choices.
var Ranges = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour}

// Boards are the dashboards this store answers (Use reads Postgres, in the API).
var Boards = []string{"overview", "queue", "processing", "streaming", "storage"}

// MaxSeries and MaxPoints bound one board's answer.
const (
	MaxSeries = 50
	MaxPoints = 2000
)

// Series is one line of a board.
type Series struct {
	Name   string       `json:"name"`
	Labels string       `json:"labels,omitempty"`
	Kind   string       `json:"kind"`
	Points [][2]float64 `json:"points"` // [unix seconds, value]
}

// BoardDoc is a board's answer.
type BoardDoc struct {
	Board  string   `json:"board"`
	Range  string   `json:"range"`
	Step   int64    `json:"step"` // seconds between points
	Series []Series `json:"series"`
}

// ErrBoard is a board or range this store does not answer.
var ErrBoard = fmt.Errorf("logindex: no such board or range")

// Board answers a board's series over the range, downsampled to at most MaxPoints each.
func (ix *Index) Board(ctx context.Context, board, rng string) (BoardDoc, error) {
	d, ok := Ranges[rng]
	known := false
	for _, b := range Boards {
		known = known || b == board
	}
	if !ok || !known {
		return BoardDoc{}, ErrBoard
	}
	allowed, _, _ := AllowedSeries()
	var names []string
	kinds := map[string]string{}
	for _, s := range allowed {
		if s.Board == board {
			names = append(names, s.Name)
			kinds[s.Name] = s.Kind
		}
	}
	sort.Strings(names)
	now := ix.now()
	since := now.Add(-d).Unix()
	table, col, unit := "metric_1m", "minute", int64(60)
	if d > 7*24*time.Hour {
		table, col, unit = "metric_1h", "hour", 3600
	}
	span := d.Seconds() / float64(unit)
	bucket := int64(math.Ceil(span / MaxPoints))
	if bucket < 1 {
		bucket = 1
	}
	doc := BoardDoc{Board: board, Range: rng, Step: bucket * unit, Series: []Series{}}
	for _, name := range names {
		if len(doc.Series) >= MaxSeries {
			break
		}
		rows, err := ix.db.QueryContext(ctx, `SELECT labels, (`+col+` / ?) * ?, AVG(value) FROM `+table+`
			WHERE name = ? AND `+col+` >= ? GROUP BY labels, `+col+` / ? ORDER BY labels, 2`,
			bucket, bucket, name, since/unit, bucket)
		if err != nil {
			return doc, err
		}
		by := map[string]*Series{}
		var order []string
		for rows.Next() {
			var labels string
			var t int64
			var v float64
			if rows.Scan(&labels, &t, &v) != nil {
				continue
			}
			s := by[labels]
			if s == nil {
				s = &Series{Name: name, Labels: labels, Kind: kinds[name]}
				by[labels] = s
				order = append(order, labels)
			}
			s.Points = append(s.Points, [2]float64{float64(t * unit), v})
		}
		_ = rows.Close()
		for _, l := range order {
			if len(doc.Series) < MaxSeries {
				doc.Series = append(doc.Series, *by[l])
			}
		}
	}
	if board == "overview" && len(doc.Series) < MaxSeries {
		// errors per hour by component, from the error groups (story 53.10)
		rows, err := ix.db.QueryContext(ctx, `SELECT COALESCE(g.component, g.service), h.hour, SUM(h.count)
			FROM error_hours h JOIN error_groups g ON g.fp = h.fp WHERE h.hour >= ?
			GROUP BY 1, 2 ORDER BY 1, 2`, since/3600)
		if err == nil {
			by := map[string]*Series{}
			var order []string
			for rows.Next() {
				var comp string
				var h int64
				var n float64
				if rows.Scan(&comp, &h, &n) != nil {
					continue
				}
				s := by[comp]
				if s == nil {
					s = &Series{Name: "maktaba_errors_per_hour", Labels: "component=" + comp, Kind: "gauge"}
					by[comp] = s
					order = append(order, comp)
				}
				s.Points = append(s.Points, [2]float64{float64(h * 3600), n})
			}
			_ = rows.Close()
			for _, c := range order {
				if len(doc.Series) < MaxSeries {
					doc.Series = append(doc.Series, *by[c])
				}
			}
		}
	}
	return doc, nil
}

// ScrapeEvery is how often the metrics are read.
var ScrapeEvery = 15 * time.Second

func (ix *Index) scrapeLoop(ctx context.Context) {
	c := &http.Client{Timeout: 5 * time.Second}
	t := time.NewTicker(ScrapeEvery)
	defer t.Stop()
	for {
		ix.Scrape(ctx, ix.MetricsURLs, c)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
