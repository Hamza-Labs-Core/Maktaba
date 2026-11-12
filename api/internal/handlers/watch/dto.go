package watch

import "time"

// ─── Session lifecycle (Story 29.1) ────────────────────────────────────

// StartRequest opens a watch session. Only video_id is required; the
// device/platform/quality hints are best-effort metadata for the
// dashboard breakdowns.
type StartRequest struct {
	VideoID    string `json:"video_id"`
	DeviceType string `json:"device_type,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Quality    string `json:"quality,omitempty"`
}

// StartResponse carries the new session id, or signals that tracking is
// off for this user (privacy — Story 29.4), in which case SessionID is
// empty and Tracking is false.
type StartResponse struct {
	SessionID string `json:"session_id,omitempty"`
	Tracking  bool   `json:"tracking"`
}

// HeartbeatRequest advances an active session.
type HeartbeatRequest struct {
	SessionID   string  `json:"session_id"`
	PositionSec float64 `json:"position_sec"`
}

// StopRequest closes a session, optionally with a final position.
type StopRequest struct {
	SessionID   string   `json:"session_id"`
	PositionSec *float64 `json:"position_sec,omitempty"`
}

// SessionView is the public projection of a session row.
type SessionView struct {
	SessionID       string  `json:"session_id"`
	VideoID         string  `json:"video_id"`
	State           string  `json:"state"`
	DurationSec     int     `json:"duration_sec"`
	PercentComplete float64 `json:"percent_complete"`
}

// ─── History (Story 29.2) ──────────────────────────────────────────────

// HistoryItem is one watched video in the caller's history, with the
// resume position sourced from playback_state (D2).
//
// A video the caller watched and may no longer see (Epic 57 story 57.03: its
// parental limit hides it now) stays in the history as a row that is not
// available: Available is false and the row says only when and how much the
// caller watched. It carries no video id, title, length or position, so
// nothing in it names the video or leads to its poster. Key is every row's
// identity in a list: the video's id for an available row, and for one that is
// not, a value made from the time of the last watch (it names no video).
type HistoryItem struct {
	Key           string  `json:"key"`
	Available     bool    `json:"available"`
	VideoID       string  `json:"video_id"`
	Title         string  `json:"title"`
	DurationSec   float64 `json:"duration_sec"`
	TimesWatched  int     `json:"times_watched"`
	TotalWatchSec int     `json:"total_watch_sec"`
	BestPercent   float64 `json:"best_percent"`
	LastWatchedAt string  `json:"last_watched_at"`
	PositionSec   float64 `json:"position_sec"`
	Completed     bool    `json:"completed"`
}

// VideoHistory is the caller's full watch state for one video.
type VideoHistory struct {
	VideoID        string        `json:"video_id"`
	TimesWatched   int           `json:"times_watched"`
	TotalWatchSec  int           `json:"total_watch_sec"`
	BestPercent    float64       `json:"best_percent"`
	Completed      bool          `json:"completed"`
	PositionSec    float64       `json:"position_sec"`
	FirstWatchedAt string        `json:"first_watched_at"`
	LastWatchedAt  string        `json:"last_watched_at"`
	Sessions       []SessionStub `json:"sessions"`
}

// SessionStub is a compact session record for the per-video history.
type SessionStub struct {
	StartedAt       string  `json:"started_at"`
	EndedAt         *string `json:"ended_at,omitempty"`
	DurationSec     int     `json:"duration_sec"`
	PercentComplete float64 `json:"percent_complete"`
	State           string  `json:"state"`
}

// ─── Activity feed (Story 29.4) ────────────────────────────────────────

// ActivityItem is one entry in the merged per-user timeline. Kind is one
// of "watched" | "searched" | "rated"; Meta carries the kind-specific
// fields so the UI renders a single shape.
type ActivityItem struct {
	Kind string         `json:"kind"`
	At   time.Time      `json:"at"`
	Meta map[string]any `json:"meta"`
}

// PrivacySettings is the per-user tracking switch.
type PrivacySettings struct {
	TrackEnabled bool `json:"track_enabled"`
}
