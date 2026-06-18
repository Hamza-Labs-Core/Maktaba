// Package analytics implements Epic 29's admin dashboard reads (Story
// 29.3), per-video statistics (29.5) and the CSV/JSON export (29.6). It
// is admin-gated in-handler and reads aggregates over watch_sessions
// (slot 0086) joined to users / videos / tags / libraries.
package analytics

import "time"

// Range is the canonical analytics window selector.
type Range struct {
	Label string    // normalised label, e.g. "7d"
	Start time.Time // inclusive lower bound; zero for "all"
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

// validBucket normalises an activity time-series bucket to day|week|month.
func validBucket(s string) string {
	switch s {
	case "week":
		return "week"
	case "month":
		return "month"
	default:
		return "day"
	}
}
