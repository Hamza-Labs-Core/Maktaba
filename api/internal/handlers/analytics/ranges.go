// Package analytics implements the admin analytics reads (Epic 29, widened by
// Epic 44 story 44.6), per-video statistics (29.5) and the CSV/JSON export
// (29.6). It reads every way of watching: plays (watch_sessions, slot 0086),
// Live TV viewings in an app or through the tuner (tv_watch_sessions, slot
// 0158) and on the remote TV (tv_remote_sessions, slot 0125), and the API's
// use per app (api_usage_daily, api_usage_people_daily). Every admin route is
// admin-gated in-handler.
package analytics

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Range is the canonical analytics window selector.
type Range struct {
	Label string    // normalised label, e.g. "7d", or "custom"
	Start time.Time // inclusive lower bound; zero for "all"
	End   time.Time // exclusive upper bound; zero for "until now"
}

// ParseRange maps a query string to a window relative to now. Recognised:
// today | 7d | 30d | 90d | 1y | all. Unknown/empty defaults to 7d. "all"
// yields a zero Start (no lower bound).
func ParseRange(s string, now time.Time) Range {
	switch s {
	case "today":
		y, m, d := now.Date()
		return Range{Label: "today", Start: time.Date(y, m, d, 0, 0, 0, 0, now.Location())}
	case "30d":
		return Range{Label: "30d", Start: now.AddDate(0, 0, -30)}
	case "90d":
		return Range{Label: "90d", Start: now.AddDate(0, 0, -90)}
	case "1y":
		return Range{Label: "1y", Start: now.AddDate(-1, 0, 0)}
	case "all":
		return Range{Label: "all", Start: time.Time{}}
	case "7d", "":
		return Range{Label: "7d", Start: now.AddDate(0, 0, -7)}
	default:
		return Range{Label: "7d", Start: now.AddDate(0, 0, -7)}
	}
}

// HasLowerBound reports whether the range constrains started_at (false
// for "all").
func (r Range) HasLowerBound() bool { return !r.Start.IsZero() }

// HasUpperBound reports whether the range ends before now (a custom "to").
func (r Range) HasUpperBound() bool { return !r.End.IsZero() }

// short reports whether the range spans two days or less (the activity
// trend is then by hour).
func (r Range) short(now time.Time) bool {
	if !r.HasLowerBound() {
		return false
	}
	end := now
	if r.HasUpperBound() {
		end = r.End
	}
	return end.Sub(r.Start) <= 48*time.Hour
}

// parseTime reads a custom range bound: a date (YYYY-MM-DD) or an RFC 3339
// instant. A date "to" means the end of that day.
func parseTime(s string, isEnd bool) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if isEnd {
			t = t.AddDate(0, 0, 1)
		}
		return t, true
	}
	return time.Time{}, false
}

// Surfaces are the ways of watching: a play of a video in an app, and a
// viewing of a channel in an app, through the tuner (Plex Live TV) or on the
// remote TV.
const (
	SurfaceVideo  = "video"
	SurfaceApp    = "app"
	SurfaceTuner  = "tuner"
	SurfaceRemote = "remote"
)

var allSurfaces = []string{SurfaceVideo, SurfaceApp, SurfaceTuner, SurfaceRemote}

// clientApp is what X-Maktaba-Client's app part may be (bounded, lowercase).
var clientApp = regexp.MustCompile(`^[a-z0-9._-]{1,32}$`)

// Filters narrow every analytics read: the range, one library (plays only:
// a channel is in no library, so viewings drop out), one person, one app
// (rows without an app read as "unknown") and one surface.
type Filters struct {
	Range     Range
	Library   string
	User      string
	ClientApp string
	Surface   string
}

// ParseFilters reads range|from|to, library, user, client_app and surface.
// Bad values are a 400, never silently ignored.
func ParseFilters(r *http.Request, now time.Time) (Filters, *httperror.Error) {
	q := r.URL.Query()
	f := Filters{Range: ParseRange(q.Get("range"), now)}
	from, to := q.Get("from"), q.Get("to")
	if from != "" || to != "" {
		f.Range = Range{Label: "custom"}
		if from != "" {
			t, ok := parseTime(from, false)
			if !ok {
				return f, httperror.InvalidQuery("from must be a date (YYYY-MM-DD) or an RFC 3339 time")
			}
			f.Range.Start = t
		}
		if to != "" {
			t, ok := parseTime(to, true)
			if !ok {
				return f, httperror.InvalidQuery("to must be a date (YYYY-MM-DD) or an RFC 3339 time")
			}
			f.Range.End = t
		}
		if f.Range.HasLowerBound() && f.Range.HasUpperBound() && !f.Range.End.After(f.Range.Start) {
			return f, httperror.InvalidQuery("to must be after from")
		}
	}
	if v := q.Get("library"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			return f, httperror.InvalidQuery("library must be a uuid")
		}
		f.Library = v
	}
	if v := q.Get("user"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			return f, httperror.InvalidQuery("user must be a uuid")
		}
		f.User = v
	}
	if v := strings.ToLower(q.Get("client_app")); v != "" {
		if !clientApp.MatchString(v) {
			return f, httperror.InvalidQuery("client_app is not an app name")
		}
		f.ClientApp = v
	}
	if v := q.Get("surface"); v != "" {
		ok := false
		for _, s := range allSurfaces {
			ok = ok || v == s
		}
		if !ok {
			return f, httperror.InvalidQuery("surface must be video, app, tuner or remote")
		}
		f.Surface = v
	}
	return f, nil
}

// key is the filters' identity for the summary cache.
func (f Filters) key() string {
	return strings.Join([]string{
		f.Range.Label, f.Range.Start.UTC().Format(time.RFC3339), f.Range.End.UTC().Format(time.RFC3339),
		f.Library, f.User, f.ClientApp, f.Surface,
	}, "|")
}

// wantPlays reports whether plays of videos are in.
func (f Filters) wantPlays() bool { return f.Surface == "" || f.Surface == SurfaceVideo }

// wantViewings reports whether channel viewings are in.
func (f Filters) wantViewings() bool { return f.Surface != SurfaceVideo && f.Library == "" }

// validBucket normalises an activity time-series bucket to
// hour|day|week|month; empty picks hour for a range of two days or less.
func validBucket(s string, rng Range, now time.Time) string {
	switch s {
	case "hour", "day", "week", "month":
		return s
	}
	if rng.short(now) {
		return "hour"
	}
	return "day"
}
