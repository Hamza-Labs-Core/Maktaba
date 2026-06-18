package analytics

// ─── Live (Story 29.3) ─────────────────────────────────────────────────

// LiveSession is one currently-watching entry.
type LiveSession struct {
	SessionID       string  `json:"session_id"`
	UserID          string  `json:"user_id"`
	Username        string  `json:"username"`
	VideoID         string  `json:"video_id"`
	Title           string  `json:"title"`
	StartedAt       string  `json:"started_at"`
	ElapsedSec      int     `json:"elapsed_sec"`
	PercentComplete float64 `json:"percent_complete"`
	DeviceType      string  `json:"device_type,omitempty"`
	Platform        string  `json:"platform,omitempty"`
}

// ─── Summary ───────────────────────────────────────────────────────────

// Summary is the headline dashboard payload for a range.
type Summary struct {
	Range          string      `json:"range"`
	TotalWatchSec  int64       `json:"total_watch_sec"`
	TotalSessions  int64       `json:"total_sessions"`
	UniqueViewers  int64       `json:"unique_viewers"`
	CompletionRate float64     `json:"completion_rate"`
	Devices        []CountStat `json:"devices"`
	Platforms      []CountStat `json:"platforms"`
	Libraries      []LabelStat `json:"libraries"`
	Genres         []CountStat `json:"genres"`
}

// CountStat is a label + a session count + watched seconds (breakdown bars).
type CountStat struct {
	Label    string `json:"label"`
	Sessions int64  `json:"sessions"`
	WatchSec int64  `json:"watch_sec"`
}

// LabelStat is a named bucket carrying an id (e.g. library).
type LabelStat struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Sessions int64  `json:"sessions"`
	WatchSec int64  `json:"watch_sec"`
}

// ─── Top videos ────────────────────────────────────────────────────────

// TopVideo is a most-watched entry.
type TopVideo struct {
	VideoID  string `json:"video_id"`
	Title    string `json:"title"`
	Sessions int64  `json:"sessions"`
	WatchSec int64  `json:"watch_sec"`
	Viewers  int64  `json:"unique_viewers"`
}

// ─── Activity ──────────────────────────────────────────────────────────

// ActivityResponse is the time series + the peak-hours heatmap.
type ActivityResponse struct {
	Bucket  string       `json:"bucket"`
	Series  []TimePoint  `json:"series"`
	Heatmap [7][24]int64 `json:"heatmap"` // [day_of_week][hour] → watch seconds
}

// TimePoint is one bucket of the watch-time-over-time series.
type TimePoint struct {
	Bucket   string `json:"bucket"`
	WatchSec int64  `json:"watch_sec"`
	Sessions int64  `json:"sessions"`
}

// ─── Users ─────────────────────────────────────────────────────────────

// ActiveUser is a most-active-by-watch-time entry.
type ActiveUser struct {
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	WatchSec   int64  `json:"watch_sec"`
	Sessions   int64  `json:"sessions"`
	LastSeenAt string `json:"last_seen_at"`
}

// ─── Per-video stats (Story 29.5) ──────────────────────────────────────

// VideoStats is the public per-video aggregate; Viewers is admin-only.
type VideoStats struct {
	TotalViews     int64    `json:"total_views"`
	UniqueViewers  int64    `json:"unique_viewers"`
	AvgCompletion  float64  `json:"avg_completion"`
	AvgWatchSec    float64  `json:"avg_watch_sec"`
	CompletionRate float64  `json:"completion_rate"`
	LastWatchedAt  *string  `json:"last_watched_at,omitempty"`
	Viewers        []Viewer `json:"viewers,omitempty"`
}

// Viewer is a per-user breakdown row (admin only).
type Viewer struct {
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	TimesWatched  int64   `json:"times_watched"`
	TotalWatchSec int64   `json:"total_watch_sec"`
	BestPercent   float64 `json:"best_percent"`
	LastWatchedAt string  `json:"last_watched_at"`
}
