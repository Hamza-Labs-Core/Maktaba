package analytics

// Window is the range a response covers, echoed back so the page can label it.
type Window struct {
	Range string `json:"range"`
	From  string `json:"from,omitempty"` // RFC 3339; empty for "all"
	To    string `json:"to,omitempty"`   // RFC 3339; empty for "until now"
}

// ─── Summary ───────────────────────────────────────────────────────────

// Summary is the Overview tab: watch time across every way of watching.
type Summary struct {
	Window
	WatchSec       int64       `json:"watch_sec"`       // videos + Live TV
	VideoSec       int64       `json:"video_sec"`       // plays of videos
	TVSec          int64       `json:"tv_sec"`          // viewings of channels
	LiveTVHours    float64     `json:"live_tv_hours"`   // TVSec in hours
	Plays          int64       `json:"plays"`           // plays of videos
	Viewings       int64       `json:"viewings"`        // viewings of channels
	People         int64       `json:"people"`          // distinct people who watched
	CompletionRate float64     `json:"completion_rate"` // completed plays / plays
	Surfaces       []CountStat `json:"surfaces"`        // video | app | tuner | remote, always all four
	Apps           []CountStat `json:"apps"`            // by client_app ("unknown" when not said)
	Platforms      []CountStat `json:"platforms"`
	Devices        []CountStat `json:"devices"`
}

// CountStat is a label with its plays and viewings (sessions), watched
// seconds and distinct people.
type CountStat struct {
	Label    string `json:"label"`
	Sessions int64  `json:"sessions"`
	WatchSec int64  `json:"watch_sec"`
	People   int64  `json:"people"`
}

// LabelStat is a named bucket carrying an id (e.g. library).
type LabelStat struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Sessions int64  `json:"sessions"`
	WatchSec int64  `json:"watch_sec"`
	People   int64  `json:"people"`
}

// ─── Now ───────────────────────────────────────────────────────────────

// NowPlay is a video being played now (a fresh heartbeat).
type NowPlay struct {
	SessionID       string  `json:"session_id"`
	UserID          string  `json:"user_id"`
	Username        string  `json:"username"`
	VideoID         string  `json:"video_id"`
	Title           string  `json:"title"`
	StartedAt       string  `json:"started_at"`
	ElapsedSec      int     `json:"elapsed_sec"`
	PercentComplete float64 `json:"percent_complete"`
	ClientApp       string  `json:"client_app"`
	ClientVersion   string  `json:"client_version"`
	Platform        string  `json:"platform"`
	DeviceType      string  `json:"device_type"`
}

// NowViewing is a channel being watched now (an open viewing seen lately).
type NowViewing struct {
	ID            string `json:"id"`
	Surface       string `json:"surface"`
	ChannelID     string `json:"channel_id"`
	ChannelName   string `json:"channel_name"`
	ChannelNumber int    `json:"channel_number"`
	UserID        string `json:"user_id"`  // empty for the tuner
	Username      string `json:"username"` // empty when not a person of this home
	StartedAt     string `json:"started_at"`
	ElapsedSec    int    `json:"elapsed_sec"`
	ClientApp     string `json:"client_app"`
	ClientVersion string `json:"client_version"`
	Platform      string `json:"platform"`
	DeviceType    string `json:"device_type"`
}

// NowResponse is the Now tab.
type NowResponse struct {
	Plays    []NowPlay        `json:"plays"`
	Viewings []NowViewing     `json:"viewings"`
	Counts   map[string]int64 `json:"counts"` // per surface
	At       string           `json:"at"`
}

// ─── Top videos / channels ─────────────────────────────────────────────

// TopVideo is a most-watched entry.
type TopVideo struct {
	VideoID        string  `json:"video_id"`
	Title          string  `json:"title"`
	Sessions       int64   `json:"sessions"`
	WatchSec       int64   `json:"watch_sec"`
	Viewers        int64   `json:"unique_viewers"`
	CompletionRate float64 `json:"completion_rate"`
}

// TopChannel is a most-watched channel, with its surfaces and the most
// people watching it at once in the range.
type TopChannel struct {
	ChannelID   string      `json:"channel_id"`
	Name        string      `json:"name"`
	Number      int         `json:"number"`
	Viewings    int64       `json:"viewings"`
	WatchSec    int64       `json:"watch_sec"`
	People      int64       `json:"people"`
	PeakViewers int         `json:"peak_viewers"`
	Surfaces    []CountStat `json:"surfaces"`
}

// TopChannelsResponse is the Live TV tab's channel list.
type TopChannelsResponse struct {
	Window
	Channels    []TopChannel `json:"channels"`
	Surfaces    []CountStat  `json:"surfaces"`     // app | tuner | remote
	PeakViewers int          `json:"peak_viewers"` // across every channel
	PeakAt      string       `json:"peak_at,omitempty"`
}

// ─── Activity ──────────────────────────────────────────────────────────

// ActivityResponse is the trend + the day-of-week x hour heatmap.
type ActivityResponse struct {
	Window
	Bucket  string       `json:"bucket"`
	Series  []TimePoint  `json:"series"`
	Heatmap [7][24]int64 `json:"heatmap"` // [day_of_week][hour] → watched seconds (videos + TV)
}

// TimePoint is one bucket of the trend.
type TimePoint struct {
	Bucket   string `json:"bucket"`
	WatchSec int64  `json:"watch_sec"` // video_sec + tv_sec
	VideoSec int64  `json:"video_sec"`
	TVSec    int64  `json:"tv_sec"`
	Plays    int64  `json:"plays"`
	Viewings int64  `json:"viewings"`
}

// ─── People ────────────────────────────────────────────────────────────

// Person is one person's watching and use of the apps in the range.
type Person struct {
	UserID        string   `json:"user_id"`
	Username      string   `json:"username"`
	WatchSec      int64    `json:"watch_sec"`
	VideoSec      int64    `json:"video_sec"`
	TVSec         int64    `json:"tv_sec"`
	Plays         int64    `json:"plays"`
	Viewings      int64    `json:"viewings"`
	Channels      int64    `json:"channels"` // distinct channels watched
	Apps          []string `json:"apps"`     // apps they watched in or used
	LastWatchedAt string   `json:"last_watched_at,omitempty"`
	LastActiveDay string   `json:"last_active_day,omitempty"` // last day an app of theirs called the API
}

// ─── Apps ──────────────────────────────────────────────────────────────

// AppUsage is one app's use of the API in the range.
type AppUsage struct {
	ClientApp string          `json:"client_app"`
	People    int64           `json:"people"`
	Requests  int64           `json:"requests"`
	Errors    int64           `json:"errors"`
	ErrorRate float64         `json:"error_rate"`
	Slow      int64           `json:"slow"`
	AvgMs     float64         `json:"avg_ms"`
	Versions  []VersionUse    `json:"versions"`
	Platforms []PlatformUsage `json:"platforms"`
}

// PlatformUsage is an app's requests on one platform.
type PlatformUsage struct {
	Platform  string  `json:"platform"`
	Requests  int64   `json:"requests"`
	Errors    int64   `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
	Slow      int64   `json:"slow"`
	AvgMs     float64 `json:"avg_ms"`
}

// VersionUse is a version of an app and the people on it.
type VersionUse struct {
	Version string `json:"version"`
	People  int64  `json:"people"`
}

// UsagePoint is one day of the API's use.
type UsagePoint struct {
	Day      string `json:"day"`
	Requests int64  `json:"requests"`
	Errors   int64  `json:"errors"`
	People   int64  `json:"people"`
}

// AppsResponse is the Apps tab.
type AppsResponse struct {
	Window
	Apps      []AppUsage   `json:"apps"`
	Trend     []UsagePoint `json:"trend"`
	Platforms []CountStat  `json:"platforms"` // watching, by platform
	Devices   []CountStat  `json:"devices"`   // watching, by device
}

// ─── Remote TV ─────────────────────────────────────────────────────────

// RemoteDay is one day of the remote TV's delivery and its cost.
type RemoteDay struct {
	Day              string  `json:"day"`
	MinutesDelivered float64 `json:"minutes_delivered"`
	SimulcastMinutes float64 `json:"simulcast_minutes"`
	CostUSD          float64 `json:"cost_usd"`
}

// RemoteTVResponse is the remote TV's part of the Live TV tab.
type RemoteTVResponse struct {
	Window
	MinutesDelivered float64     `json:"minutes_delivered"`
	SimulcastMinutes float64     `json:"simulcast_minutes"`
	CostUSD          float64     `json:"cost_usd"`
	Viewings         int64       `json:"viewings"`
	WatchSec         int64       `json:"watch_sec"`
	People           int64       `json:"people"`
	Days             []RemoteDay `json:"days"`
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
