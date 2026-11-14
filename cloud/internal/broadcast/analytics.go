package broadcast

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/lib/pq"
)

// Story 50.28 §3: what a publisher sees. Read from broadcast_usage_hourly only, folded: any
// country or device with fewer than K sessions in the shown period is "other" (README §14.10
// Q20). Totals are always shown, so a publisher with one viewer sees totals and "other".

// DefaultK is broadcast.analytics.k's default.
const DefaultK = 5

// OtherKey is the folded entry's name.
const OtherKey = "other"

// Count is one named count.
type Count struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

// Fold names each entry with at least k and puts the rest in one "other" entry (last, absent
// when empty). Named entries come largest first.
func Fold(m map[string]float64, k int) []Count {
	if k < 1 {
		k = DefaultK
	}
	out := []Count{}
	other := 0.0
	for key, v := range m {
		if v < float64(k) || key == OtherKey {
			other += v
			continue
		}
		out = append(out, Count{Key: key, Value: round(v)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Key < out[j].Key
	})
	if other > 0 {
		out = append(out, Count{Key: OtherKey, Value: round(other)})
	}
	return out
}

// Sorted lists a map largest first, unfolded (for counts that say nothing about viewers:
// errors by code, blocked attempts by reason, time per rung).
func Sorted(m map[string]float64) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		if v > 0 {
			out = append(out, Count{Key: k, Value: round(v)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func round(v float64) float64 { return math.Round(v*100) / 100 }

// Level is how much a plan's analytics show (README §14.4): Pro's guest sharing sees live viewers
// and hours; the Broadcaster tier sees everything.
type Level string

// The levels.
const (
	LevelHours Level = "hours"
	LevelFull  Level = "full"
)

// Report is a publisher's analytics for one period.
type Report struct {
	Level    Level     `json:"level"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	TimeZone string    `json:"time_zone"`
	K        int       `json:"k"`

	LiveViewers float64 `json:"live_viewers"`
	// Hours are viewer-hours today, in the last 7 days and this month, in the publisher's zone.
	Hours struct {
		Today     float64  `json:"today"`
		Week      float64  `json:"week"`
		Month     float64  `json:"month"`
		Allowance *float64 `json:"allowance"`
	} `json:"hours"`
	PeriodHours float64 `json:"period_hours"`

	// LevelFull only (absent for LevelHours)
	Peak          *float64 `json:"peak,omitempty"`
	Sessions      *float64 `json:"sessions,omitempty"`
	AvgWatchMin   *float64 `json:"avg_watch_minutes,omitempty"`
	AvgStartupMs  *float64 `json:"avg_startup_ms,omitempty"`
	RebufferRatio *float64 `json:"rebuffer_ratio,omitempty"`
	Countries     []Count  `json:"countries,omitempty"`
	Devices       []Count  `json:"devices,omitempty"`
	Quality       []Count  `json:"quality,omitempty"` // share of watch time per rung, 0..1
	Errors        []Count  `json:"errors,omitempty"`
	Blocked       []Count  `json:"blocked,omitempty"`
	Daily         []Day    `json:"daily,omitempty"`
}

// Day is one local day's totals (the page's chart, and the CSV's rows).
type Day struct {
	Date          string  `json:"date"`
	ViewerMinutes float64 `json:"viewer_minutes"`
	Sessions      float64 `json:"sessions"`
	Peak          float64 `json:"peak"`
	Errors        float64 `json:"errors"`
}

// Query is one report's ask.
type Query struct {
	Publishers []string // the publisher ids the home answers to (see the handler's PublisherIDs)
	From, To   time.Time
	Loc        *time.Location
	K          int
	Level      Level
	Allowance  *float64 // viewer-hours a month, when the plan has one
	Now        time.Time
}

func f(v float64) *float64 { r := round(v); return &r }

// Load builds the report from the hourly rows and the live table.
func Load(ctx context.Context, db *sql.DB, q Query) (Report, error) {
	if q.Loc == nil {
		q.Loc = time.UTC
	}
	if q.K < 1 {
		q.K = DefaultK
	}
	r := Report{Level: q.Level, From: q.From, To: q.To, TimeZone: q.Loc.String(), K: q.K}
	pubs := pq.Array(q.Publishers)
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(viewers), 0) FROM broadcast_live
        WHERE publisher_id = ANY($1) AND updated_at > $2`, pubs, q.Now.Add(-3*time.Minute)).Scan(&r.LiveViewers); err != nil {
		return Report{}, err
	}
	local := q.Now.In(q.Loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, q.Loc)
	month := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, q.Loc)
	for _, p := range []struct {
		from time.Time
		dst  *float64
	}{{today, &r.Hours.Today}, {today.AddDate(0, 0, -6), &r.Hours.Week}, {month, &r.Hours.Month}} {
		var mins float64
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(viewer_minutes), 0) FROM broadcast_usage_hourly
            WHERE publisher_id = ANY($1) AND hour >= $2`, pubs, p.from.UTC()).Scan(&mins); err != nil {
			return Report{}, err
		}
		*p.dst = round(mins / 60)
	}
	r.Hours.Allowance = q.Allowance

	rows, err := db.QueryContext(ctx, `
        SELECT hour, viewer_minutes, sessions, peak_concurrent, countries, devices, quality, rebuffer_ratio,
               startup_ms_sum, errors, errors_by_code, blocked
          FROM broadcast_usage_hourly
         WHERE publisher_id = ANY($1) AND hour >= $2 AND hour < $3
         ORDER BY hour`, pubs, q.From.UTC(), q.To.UTC())
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = rows.Close() }()
	var minutes, sessions, startup, rebufWeighted float64
	countries, devices, quality, errs, blocked := map[string]float64{}, map[string]float64{}, map[string]float64{}, map[string]float64{}, map[string]float64{}
	peakByHour := map[time.Time]float64{}
	days := map[string]*Day{}
	for rows.Next() {
		var hour time.Time
		var vm, ss, pk, rr, su, er float64
		var c, d, qu, ec, bl []byte
		if err := rows.Scan(&hour, &vm, &ss, &pk, &c, &d, &qu, &rr, &su, &er, &ec, &bl); err != nil {
			return Report{}, err
		}
		minutes += vm
		sessions += ss
		startup += su
		rebufWeighted += rr * vm
		peakByHour[hour] += pk // channels of one hour add up
		for _, m := range []struct {
			raw []byte
			dst map[string]float64
		}{{c, countries}, {d, devices}, {qu, quality}, {ec, errs}, {bl, blocked}} {
			addJSON(m.dst, m.raw)
		}
		key := hour.In(q.Loc).Format("2006-01-02")
		day := days[key]
		if day == nil {
			day = &Day{Date: key}
			days[key] = day
		}
		day.ViewerMinutes += vm
		day.Sessions += ss
		day.Errors += er
		day.Peak = math.Max(day.Peak, peakByHour[hour])
	}
	if err := rows.Err(); err != nil {
		return Report{}, err
	}
	r.PeriodHours = round(minutes / 60)
	if q.Level != LevelFull {
		return r, nil
	}
	peak := 0.0
	for _, v := range peakByHour {
		peak = math.Max(peak, v)
	}
	r.Peak, r.Sessions = f(peak), f(sessions)
	avg, st, rb := 0.0, 0.0, 0.0
	if sessions > 0 {
		avg, st = minutes/sessions, startup/sessions
	}
	if minutes > 0 {
		rb = rebufWeighted / minutes
	}
	r.AvgWatchMin, r.AvgStartupMs = f(avg), f(st)
	rbr := math.Round(rb*10000) / 10000
	r.RebufferRatio = &rbr
	r.Countries, r.Devices = Fold(countries, q.K), Fold(devices, q.K)
	total := 0.0
	for _, v := range quality {
		total += v
	}
	r.Quality = []Count{}
	for _, c := range Sorted(quality) {
		r.Quality = append(r.Quality, Count{Key: c.Key, Value: math.Round(c.Value/total*1000) / 1000})
	}
	r.Errors, r.Blocked = Sorted(errs), Sorted(blocked)
	r.Daily = make([]Day, 0, len(days))
	for _, d := range days {
		d.ViewerMinutes, d.Sessions, d.Errors, d.Peak = round(d.ViewerMinutes), round(d.Sessions), round(d.Errors), round(d.Peak)
		r.Daily = append(r.Daily, *d)
	}
	sort.Slice(r.Daily, func(i, j int) bool { return r.Daily[i].Date < r.Daily[j].Date })
	return r, nil
}

func addJSON(dst map[string]float64, raw []byte) {
	var m map[string]float64
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	for k, v := range m {
		dst[k] += v
	}
}

// WriteCSV writes the report as CSV: a summary block, the folded countries and devices, then one
// row per local day. Nothing in it is finer than the page shows.
func WriteCSV(w io.Writer, r Report) error {
	c := csv.NewWriter(w)
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	ptr := func(p *float64) string {
		if p == nil {
			return ""
		}
		return num(*p)
	}
	rows := [][]string{
		{"section", "key", "value"},
		{"period", "from", r.From.In(loc(r.TimeZone)).Format(time.RFC3339)},
		{"period", "to", r.To.In(loc(r.TimeZone)).Format(time.RFC3339)},
		{"period", "time_zone", r.TimeZone},
		{"summary", "viewer_hours", num(r.PeriodHours)},
		{"summary", "peak_concurrent", ptr(r.Peak)},
		{"summary", "sessions", ptr(r.Sessions)},
		{"summary", "average_watch_minutes", ptr(r.AvgWatchMin)},
		{"summary", "rebuffer_ratio", ptr(r.RebufferRatio)},
	}
	for _, sec := range []struct {
		name string
		cs   []Count
	}{{"country", r.Countries}, {"device", r.Devices}, {"quality_share", r.Quality}, {"error", r.Errors}, {"blocked", r.Blocked}} {
		for _, x := range sec.cs {
			rows = append(rows, []string{sec.name, x.Key, num(x.Value)})
		}
	}
	if err := c.WriteAll(rows); err != nil {
		return err
	}
	if len(r.Daily) > 0 {
		if err := c.Write([]string{}); err != nil {
			return err
		}
		if err := c.Write([]string{"date", "viewer_minutes", "sessions", "peak_concurrent", "errors"}); err != nil {
			return err
		}
		for _, d := range r.Daily {
			if err := c.Write([]string{d.Date, num(d.ViewerMinutes), num(d.Sessions), num(d.Peak), num(d.Errors)}); err != nil {
				return err
			}
		}
	}
	c.Flush()
	return c.Error()
}

func loc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return l
}

// Period resolves a named period in a zone to its instants: today, 7d (today and the 6 days
// before), month (this month), or day (the given date, YYYY-MM-DD).
func Period(name, date string, l *time.Location, now time.Time) (time.Time, time.Time, error) {
	local := now.In(l)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, l)
	switch name {
	case "", "today":
		return today, today.AddDate(0, 0, 1), nil
	case "7d":
		return today.AddDate(0, 0, -6), today.AddDate(0, 0, 1), nil
	case "month":
		m := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, l)
		return m, m.AddDate(0, 1, 0), nil
	case "day":
		d, err := time.ParseInLocation("2006-01-02", date, l)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("broadcast: day needs date=YYYY-MM-DD")
		}
		if d.Before(today.AddDate(0, -13, 0)) || d.After(today) {
			return time.Time{}, time.Time{}, fmt.Errorf("broadcast: the date is outside the 13 months kept")
		}
		return d, d.AddDate(0, 0, 1), nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("broadcast: period is today, 7d, month or day")
}
