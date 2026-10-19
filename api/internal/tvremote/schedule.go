package tvremote

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Simulcast schedules (story 32.13 AC2, plan D5): an attachment's rules
// resolve against the channel's timeline (channel_programs) and the
// channel's time zone into windows; each matching block is widened by the
// lead and tail margins and overlapping or touching windows merge into one.

// Rule kinds.
const (
	RuleWindow    = "window"    // weekly: days + start/end (HH:MM) in the channel's time zone
	RuleProgramme = "programme" // while a video airs
	RuleSeries    = "series"    // while any episode of a series airs
	RuleLive      = "live"      // during live slots (channel_programs.kind = live)
)

// Rule is one schedule rule of an attachment.
type Rule struct {
	Kind string `json:"kind"`
	// Days are 0 (Sunday) to 6 (Saturday); empty: every day.
	Days     []int  `json:"days,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	VideoID  string `json:"video_id,omitempty"`
	SeriesID string `json:"series_id,omitempty"`
}

// Window is one simulcast window [Start, End).
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Block is one block of a channel's timeline.
type Block struct {
	Start, End time.Time
	Kind       string
	VideoID    string
	SeriesID   string
}

var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// clock parses HH:MM into minutes after midnight.
func clock(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 || (hh == 24 && mm != 0) {
		return 0, false
	}
	return hh*60 + mm, true
}

// ParseRules decodes and checks an attachment's rules (day names are
// accepted and stored as numbers). A schedule needs at least one rule.
func ParseRules(raw json.RawMessage, mode string) ([]Rule, error) {
	var items []map[string]json.RawMessage
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("rules must be a list")
		}
	}
	out := make([]Rule, 0, len(items))
	for i, it := range items {
		var r Rule
		str := func(k string) string {
			var s string
			_ = json.Unmarshal(it[k], &s)
			return strings.TrimSpace(s)
		}
		r.Kind = str("kind")
		switch r.Kind {
		case RuleWindow:
			r.Start, r.End = str("start"), str("end")
			if _, ok := clock(r.Start); !ok {
				return nil, fmt.Errorf("rule %d: start must be HH:MM", i+1)
			}
			if _, ok := clock(r.End); !ok {
				return nil, fmt.Errorf("rule %d: end must be HH:MM", i+1)
			}
			if d, ok := it["days"]; ok && string(d) != "null" {
				var xs []json.RawMessage
				if json.Unmarshal(d, &xs) != nil {
					return nil, fmt.Errorf("rule %d: days must be a list", i+1)
				}
				seen := map[int]bool{}
				for _, x := range xs {
					var n int
					var name string
					switch {
					case json.Unmarshal(x, &n) == nil && n >= 0 && n <= 6:
					case json.Unmarshal(x, &name) == nil:
						key := strings.ToLower(strings.TrimSpace(name))
						if len(key) > 3 {
							key = key[:3]
						}
						v, ok := dayNames[key]
						if !ok {
							return nil, fmt.Errorf("rule %d: unknown day %q", i+1, name)
						}
						n = v
					default:
						return nil, fmt.Errorf("rule %d: days are 0 (Sunday) to 6", i+1)
					}
					if !seen[n] {
						seen[n] = true
						r.Days = append(r.Days, n)
					}
				}
				sort.Ints(r.Days)
			}
		case RuleProgramme:
			r.VideoID = str("video_id")
			if r.VideoID == "" {
				return nil, fmt.Errorf("rule %d: video_id is required", i+1)
			}
		case RuleSeries:
			r.SeriesID = str("series_id")
			if r.SeriesID == "" {
				return nil, fmt.Errorf("rule %d: series_id is required", i+1)
			}
		case RuleLive:
		default:
			return nil, fmt.Errorf("rule %d: kind must be window, programme, series or live", i+1)
		}
		out = append(out, r)
	}
	if mode == ModeSchedule && len(out) == 0 {
		return nil, fmt.Errorf("a schedule needs at least one rule")
	}
	if len(out) > 50 {
		return nil, fmt.Errorf("at most 50 rules")
	}
	return out, nil
}

// weekly expands a window rule over [from, to) in loc. A window whose end
// is not after its start runs past midnight (start == end: 24 hours); the
// days name the day it starts. Wall times are kept across DST changes.
func weekly(r Rule, loc *time.Location, from, to time.Time) []Window {
	sm, ok1 := clock(r.Start)
	em, ok2 := clock(r.End)
	if !ok1 || !ok2 {
		return nil
	}
	days := map[int]bool{}
	for _, d := range r.Days {
		days[d] = true
	}
	var out []Window
	lf := from.In(loc)
	// from the day before (a window from yesterday may still run)
	d := time.Date(lf.Year(), lf.Month(), lf.Day()-1, 0, 0, 0, 0, loc)
	for ; d.Before(to.Add(24 * time.Hour)); d = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc) {
		if len(days) > 0 && !days[int(d.Weekday())] {
			continue
		}
		s := time.Date(d.Year(), d.Month(), d.Day(), sm/60, sm%60, 0, 0, loc)
		ed := d.Day()
		if em <= sm {
			ed++
		}
		e := time.Date(d.Year(), d.Month(), ed, em/60, em%60, 0, 0, loc)
		if e.After(from) && s.Before(to) {
			out = append(out, Window{Start: s.UTC(), End: e.UTC()})
		}
	}
	return out
}

// matches reports whether a block matches a timeline rule.
func matches(r Rule, b Block) bool {
	switch r.Kind {
	case RuleProgramme:
		return b.VideoID != "" && strings.EqualFold(b.VideoID, r.VideoID)
	case RuleSeries:
		return b.SeriesID != "" && strings.EqualFold(b.SeriesID, r.SeriesID)
	case RuleLive:
		return b.Kind == "live"
	}
	return false
}

// ResolveWindows turns rules into merged windows over [from, to): weekly
// windows and matching timeline blocks, each widened by lead (before) and
// tail (after); overlapping or touching windows merge.
func ResolveWindows(rules []Rule, loc *time.Location, blocks []Block, from, to time.Time, lead, tail time.Duration) []Window {
	if loc == nil {
		loc = time.UTC
	}
	var ws []Window
	for _, r := range rules {
		if r.Kind == RuleWindow {
			for _, w := range weekly(r, loc, from.Add(-tail), to.Add(lead)) {
				ws = append(ws, Window{Start: w.Start.Add(-lead), End: w.End.Add(tail)})
			}
			continue
		}
		for _, b := range blocks {
			if matches(r, b) {
				ws = append(ws, Window{Start: b.Start.UTC().Add(-lead), End: b.End.UTC().Add(tail)})
			}
		}
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].Start.Before(ws[j].Start) })
	var out []Window
	for _, w := range ws {
		if !w.End.After(from) || !w.Start.Before(to) {
			continue
		}
		if n := len(out); n > 0 && !w.Start.After(out[n-1].End) {
			if w.End.After(out[n-1].End) {
				out[n-1].End = w.End
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

// windowAt is the window containing t (ok false: none).
func windowAt(ws []Window, t time.Time) (Window, bool) {
	for _, w := range ws {
		if !t.Before(w.Start) && t.Before(w.End) {
			return w, true
		}
	}
	return Window{}, false
}

// nextBoundary is the next window start or end after t (nil: none known).
func nextBoundary(ws []Window, t time.Time) *time.Time {
	for _, w := range ws {
		if w.Start.After(t) {
			x := w.Start
			return &x
		}
		if w.End.After(t) {
			x := w.End
			return &x
		}
	}
	return nil
}

// ScheduleHorizon is how far ahead windows are resolved.
const ScheduleHorizon = 8 * 24 * time.Hour

// channelZone is the channel's time zone (mode_config.timezone), else the
// server's (as the planner's).
func (s *store) channelZone(ctx context.Context, channel string) *time.Location {
	var raw ns
	if err := s.db.QueryRowContext(ctx, `SELECT mode_config FROM channels WHERE id = $1`, channel).Scan(&raw); err == nil && raw.S != "" {
		var m struct {
			Timezone string `json:"timezone"`
		}
		if json.Unmarshal([]byte(raw.S), &m) == nil && m.Timezone != "" {
			if loc, err := time.LoadLocation(m.Timezone); err == nil {
				return loc
			}
		}
	}
	return time.Local
}

// blocks is the channel's timeline over [from, to) that a timeline rule can
// match: programmes with their series and live slots.
func (s *store) blocks(ctx context.Context, channel string, from, to time.Time) ([]Block, error) {
	q := `SELECT p.start_at, p.end_at, p.kind, p.video_id,
		(SELECT e.series_id FROM series_episodes e WHERE e.video_id = p.video_id LIMIT 1)
		FROM channel_programs_live p WHERE p.channel_id = $1 AND p.kind IN ('program', 'live')`
	args := []any{channel}
	if s.postgres {
		q += ` AND p.end_at > $2 AND p.start_at < $3`
		args = append(args, s.t(from), s.t(to))
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Block
	for rows.Next() {
		var st, en nt
		var kind, video, series ns
		if err := rows.Scan(&st, &en, &kind, &video, &series); err != nil {
			return nil, err
		}
		if !st.Valid || !en.Valid || !en.T.After(from) || !st.T.Before(to) {
			continue
		}
		out = append(out, Block{Start: st.T, End: en.T, Kind: kind.S, VideoID: video.S, SeriesID: series.S})
	}
	return out, rows.Err()
}
