package queues

import "strings"

// causeInfo is the human label and the one action offered for an
// issue group. The cause codes are produced by the Python engine
// (engine/causes.py, plan-06-11 §6.3); causes not listed here fall back
// to the raw code as label and "retry" as action.
type causeInfo struct {
	Label  string
	Action string
}

// Actions of a FailureGroup.
const (
	ActionRetry        = "retry"
	ActionApply        = "apply"
	ActionOpenSettings = "open_settings"
	ActionShowWorkers  = "show_workers"
)

// Pseudo-causes added by the API (plan-06-11 §6.3).
const (
	CauseAwaitingReview = "awaiting_review"
	CauseStaleGraph     = "stale_graph"
)

var causeLabels = map[string]causeInfo{
	"nas_timeout":          {"NAS read timed out", ActionRetry},
	"nas_invalid_argument": {"NAS refused the read (Invalid argument)", ActionRetry},
	"nas_partial_read":     {"NAS returned a partial read", ActionRetry},
	"file_missing":         {"File is missing on disk", ActionRetry},
	"ffmpeg_decode":        {"Could not decode the media file", ActionRetry},
	"youtube_bot_check":    {"YouTube asked for a bot check", ActionOpenSettings},
	"youtube_unavailable":  {"YouTube video or channel unavailable", ActionRetry},
	"plex_unreachable":     {"Plex is unreachable", ActionOpenSettings},
	"plex_auth":            {"Plex rejected the token", ActionOpenSettings},
	"dizquetv_unreachable": {"dizqueTV is unreachable", ActionOpenSettings},
	"readback_mismatch":    {"Write did not read back correctly", ActionRetry},
	"no_handler":           {"No worker handles this stage", ActionShowWorkers},
	"graph_violation":      {"Stage requested work its graph does not allow", ActionRetry},
	"db_error":             {"Database error", ActionRetry},
	CauseAwaitingReview:    {"Waiting for your review", ActionApply},
	CauseStaleGraph:        {"No worker has synced this stage recently", ActionShowWorkers},
}

// describeCause returns the label/action for a cause code.
func describeCause(cause string) causeInfo {
	if ci, ok := causeLabels[cause]; ok {
		return ci
	}
	// Generated causes look like "kind:normalised message".
	label := cause
	if i := strings.IndexByte(cause, ':'); i > 0 && i < len(cause)-1 {
		label = cause
	}
	return causeInfo{Label: label, Action: ActionRetry}
}
