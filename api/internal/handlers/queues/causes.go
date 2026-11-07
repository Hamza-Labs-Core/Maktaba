package queues

import "strings"

// causeInfo is the human label and the one action offered for an
// issue group. The cause codes are produced by the Python engine
// (engine/causes.py, plan-06-11 §6.3); a cause not listed here is labelled
// by its kind (describeCause), with "retry" as action.
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
	"youtube_rate_limited": {"YouTube is limiting requests", ActionRetry},
	"youtube_unavailable":  {"YouTube video or channel unavailable", ActionRetry},
	"plex_unreachable":     {"Plex is unreachable", ActionOpenSettings},
	"plex_auth":            {"Plex rejected the token", ActionOpenSettings},
	"dizquetv_unreachable": {"dizqueTV is unreachable", ActionOpenSettings},
	"readback_mismatch":    {"Write did not read back correctly", ActionRetry},
	// Epic 54: a plugin job's end (story 54.06 §3; the web's words are queue.issues.cause.*)
	"plugin_trap":       {"The plugin crashed", ActionRetry},
	"plugin_timeout":    {"The plugin ran out of time", ActionRetry},
	"plugin_memory":     {"The plugin used more memory than it may", ActionRetry},
	"plugin_capability": {"The plugin asked for something it was not allowed", ActionRetry},
	"plugin_scope":      {"The plugin reached outside its job or its libraries", ActionRetry},
	"plugin_quota":      {"The plugin used up its quota", ActionRetry},
	"plugin_failed":     {"The plugin reported an error", ActionRetry},
	"plugin_held":       {"The plugin is stopped", ActionOpenSettings},
	"plugin_loop":       {"Stopped a loop between plugins", ActionOpenSettings},
	"no_handler":        {"No worker handles this stage", ActionShowWorkers},
	"graph_violation":   {"Stage requested work its graph does not allow", ActionRetry},
	"db_error":          {"Database error", ActionRetry},
	"shm_full":          {"Postgres ran out of shared memory", ActionRetry},
	"disk_full":         {"No space left on the disk", ActionRetry},
	"permission_denied": {"No permission to read the file", ActionRetry},
	"ffmpeg_rc":         {"ffmpeg failed on the file", ActionRetry},
	CauseAwaitingReview: {"Waiting for your review", ActionApply},
	CauseStaleGraph:     {"No worker has synced this stage recently", ActionShowWorkers},
}

// kindLabels names the failure kinds the stages raise that are not cause
// codes of their own: a generated cause "kind:..." takes its kind's label.
var kindLabels = map[string]string{
	"model_unavailable":     "The model did not answer",
	"chapter_frames_failed": "ffmpeg failed on the file",
	"thumbnail_unreadable":  "The thumbnail could not be read",
	"video_unhashed":        "The file has no content hash yet",
	"unknown":               "Unknown failure",
}

// describeCause returns the label/action for a cause code. A generated
// cause ("kind:normalised message", engine/causes.py) is a grouping key,
// never a label: it is labelled by its kind, as a known cause's label (a
// stage raised "nas_timeout" with a message the engine did not match), a
// known kind's, else the kind in words ("Model unavailable"). The
// normalised message, with its <n> and <path>, is never shown: the
// group's sample_message says what happened.
func describeCause(cause string) causeInfo {
	if ci, ok := causeLabels[cause]; ok {
		return ci
	}
	kind := cause
	if i := strings.IndexByte(cause, ':'); i > 0 {
		kind = cause[:i]
	}
	if ci, ok := causeLabels[kind]; ok {
		return ci
	}
	if label, ok := kindLabels[kind]; ok {
		return causeInfo{Label: label, Action: ActionRetry}
	}
	return causeInfo{Label: kindWords(kind), Action: ActionRetry}
}

// kindWords is a kind code in words: underscores as spaces, the first
// letter capitalised ("model_unavailable" -> "Model unavailable").
func kindWords(kind string) string {
	w := strings.TrimSpace(strings.ReplaceAll(kind, "_", " "))
	if w == "" {
		return kindLabels["unknown"]
	}
	return strings.ToUpper(w[:1]) + w[1:]
}
