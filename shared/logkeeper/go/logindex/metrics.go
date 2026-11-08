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
	Kind     string `json:"kind"` // counter, gauge or latency (a histogram: requests and p95)
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
	sets    map[string]map[string]bool // the label sets kept, by name (an id series': by name and machine)
	hosts   map[string]map[string]bool // the machines an id series keeps, by name
	dropped float64                    // series or label sets not stored
	allowed map[string]SeriesDef       // the allow-list
	caps    [2]int                     // label cap, id label cap
	hist    map[string]*histState      // the latency histograms, by name
}

func newMetricsState() *metricsState {
	a, c, ic := AllowedSeries()
	return &metricsState{last: map[string]float64{}, sets: map[string]map[string]bool{}, hosts: map[string]map[string]bool{}, allowed: a,
		caps: [2]int{c, ic}, hist: map[string]*histState{}}
}

func minuteOf(t time.Time) int64 { return t.Unix() / 60 }

// keep reports whether a sample's label set is stored: one already kept, or a new one
// within its series' cap (a dropped one is counted). A series of catalog ids keeps up to
// the id cap per machine, for up to the label cap of machines (the home's dashboards
// carry each joined machine's components, story 53.12); any other keeps up to the label
// cap.
func (st *metricsState) keep(s sample, def SeriesDef) bool {
	key, limit := s.name, st.caps[0]
	if def.IDLabels {
		machine := labelValue(s.labels, "machine")
		key, limit = s.name+"\x00"+machine, st.caps[1]
		hosts := st.hosts[s.name]
		if hosts == nil {
			hosts = map[string]bool{}
			st.hosts[s.name] = hosts
		}
		if !hosts[machine] {
			if len(hosts) >= st.caps[0] {
				st.dropped++
				return false
			}
			hosts[machine] = true
		}
	}
	set := st.sets[key]
	if set == nil {
		set = map[string]bool{}
		st.sets[key] = set
	}
	if !set[s.labels] {
		if len(set) >= limit {
			st.dropped++
			return false
		}
		set[s.labels] = true
	}
	return true
}

// labelValue is one label's value in a canonical label list (`a=1,b=x`), "" when absent.
func labelValue(labels, name string) string {
	for _, kv := range strings.Split(labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

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
	for name, def := range st.allowed {
		if def.Kind != LatencyKind {
			continue
		}
		if n, p95, hasP95, ok := st.latencyRows(name, samples, minute); ok {
			countName, p95Name := LatencySeries(name)
			rows = append(rows, row{countName, "", n, true})
			if hasP95 {
				rows = append(rows, row{p95Name, "", p95, false})
			}
		}
	}
	// each machine's components and restarts, and the totals across machines, summed as
	// they are written so the Overview reads a line per machine, not one per component
	tally := newMachineTally()
	for _, s := range samples {
		def, ok := st.allowed[s.name]
		if !ok || def.Kind == LatencyKind {
			continue // not on the list: not counted as dropped (most metrics are not wanted)
		}
		if !st.keep(s, def) {
			continue
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
			tally.add(s.name, s.labels, d)
			continue
		}
		rows = append(rows, row{s.name, s.labels, s.value, false})
		tally.add(s.name, s.labels, s.value)
	}
	for _, r := range tally.rows() {
		rows = append(rows, row{r.name, r.labels, r.v, r.add})
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
	// The first target that has a series keeps it: every Go process exports process_* and
	// go_* unlabelled, and two processes' counters read as one would look like a reset at
	// each scrape. A later target adds only what the earlier ones lacked (streaming's tv_*).
	seen := map[string]bool{}
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
		if resp.StatusCode != http.StatusOK {
			continue
		}
		mine := parseMetrics(bytes.NewReader(b))
		for _, s := range mine {
			if !seen[s.name+"{"+s.labels] {
				all = append(all, s)
			}
		}
		for _, s := range mine {
			seen[s.name+"{"+s.labels] = true
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
	// Machine is the one machine the Overview was asked for (machine=), "" for all.
	Machine string `json:"machine,omitempty"`
	// Machines (Overview only) counts the machines with lines and names the ones shown.
	Machines *MachineCount `json:"machines,omitempty"`
	// Omitted is how many lines MaxSeries left out.
	Omitted int `json:"omitted,omitempty"`
}

// ErrBoard is a board or range this store does not answer.
var ErrBoard = fmt.Errorf("logindex: no such board or range")

// window is where a board reads its points: the table, its time column and unit (seconds),
// the bucket (in units) the points are averaged over, and the first unit kept.
type window struct {
	table, col          string
	unit, bucket, since int64
}

// Board answers a board's series over the range for every machine.
func (ix *Index) Board(ctx context.Context, board, rng string) (BoardDoc, error) {
	return ix.BoardOf(ctx, board, rng, "")
}

// BoardOf answers a board's series over the range, downsampled to at most MaxPoints each and
// at most MaxSeries lines. On the Overview, machine names one machine whose lines it answers
// ("" all machines: totals across them and the TopMachines most in need of attention).
func (ix *Index) BoardOf(ctx context.Context, board, rng, machine string) (BoardDoc, error) {
	d, ok := Ranges[rng]
	known := false
	for _, b := range Boards {
		known = known || b == board
	}
	if !ok || !known || len(machine) > maxMachineID {
		return BoardDoc{}, ErrBoard
	}
	if board != "overview" {
		machine = ""
	}
	allowed, _, _ := AllowedSeries()
	var names []string
	kinds := map[string]string{}
	for _, s := range allowed {
		if s.Board != board || (board == "overview" && machineSeries(s.Name)) {
			continue // the Overview's per-machine series are answered by overviewMachines
		}
		if s.Kind == LatencyKind { // kept as its requests per minute and its p95
			count, p95 := LatencySeries(s.Name)
			names = append(names, count, p95)
			kinds[count], kinds[p95] = "counter", "gauge"
			continue
		}
		names = append(names, s.Name)
		kinds[s.Name] = s.Kind
	}
	sort.Strings(names)
	now := ix.now()
	w := window{table: "metric_1m", col: "minute", unit: 60}
	if d > 7*24*time.Hour {
		w.table, w.col, w.unit = "metric_1h", "hour", 3600
	}
	span := d.Seconds() / float64(w.unit)
	w.bucket = int64(math.Ceil(span / MaxPoints))
	if w.bucket < 1 {
		w.bucket = 1
	}
	since := now.Add(-d).Unix()
	w.since = since / w.unit
	doc := BoardDoc{Board: board, Range: rng, Step: w.bucket * w.unit, Series: []Series{}, Machine: machine}
	add := func(ss []Series) {
		for _, s := range ss {
			if len(doc.Series) < MaxSeries {
				doc.Series = append(doc.Series, s)
			} else {
				doc.Omitted++
			}
		}
	}
	if board == "overview" {
		lines, count, err := ix.overviewMachines(ctx, w, machine)
		if err != nil {
			return doc, err
		}
		doc.Machines = count
		add(lines)
		if machine != "" { // one machine: only its own lines (the rest are the home's)
			return doc, nil
		}
	}
	for _, name := range names {
		ss, err := ix.seriesOf(ctx, w, name, kinds[name])
		if err != nil {
			return doc, err
		}
		add(ss)
	}
	if board == "overview" {
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
				add([]Series{*by[c]})
			}
		}
	}
	return doc, nil
}

// seriesOf is one series name's lines over the window, a line per label set.
// With a label set given, only that one line (read through the primary key).
func (ix *Index) seriesOf(ctx context.Context, w window, name, kind string, labels ...string) ([]Series, error) {
	where, args := "", []any{w.bucket, w.bucket, name, w.since}
	if len(labels) == 1 {
		where, args = " AND labels = ?", append(args, labels[0])
	}
	rows, err := ix.db.QueryContext(ctx, `SELECT labels, (`+w.col+` / ?) * ?, AVG(value) FROM `+w.table+`
		WHERE name = ? AND `+w.col+` >= ?`+where+` GROUP BY labels, `+w.col+` / ? ORDER BY labels, 2`,
		append(args, w.bucket)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Series
	for rows.Next() {
		var labels string
		var t int64
		var v float64
		if rows.Scan(&labels, &t, &v) != nil {
			continue
		}
		if len(out) == 0 || out[len(out)-1].Labels != labels {
			out = append(out, Series{Name: name, Labels: labels, Kind: kind})
		}
		s := &out[len(out)-1]
		s.Points = append(s.Points, [2]float64{float64(t * w.unit), v})
	}
	return out, rows.Err()
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

// ImportHours stores rolled-up hours as they are (value, min and max alike): a series
// brought from elsewhere, or 30 days for the performance budget (logbench).
func (ix *Index) ImportHours(ctx context.Context, name, labels string, points [][2]float64) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, p := range points {
		if _, err := tx.Exec(`INSERT INTO metric_1h(name, labels, hour, value, min, max) VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(name, labels, hour) DO UPDATE SET value = excluded.value, min = excluded.min, max = excluded.max`,
			name, labels, int64(p[0])/3600, p[1], p[1], p[1]); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
