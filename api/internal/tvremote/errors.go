package tvremote

// What Maktaba does about a Cloudflare live input error (story 32.7 AC11,
// design §7.2).
const (
	// ActionNone: not an error Maktaba acts on.
	ActionNone = ""
	// ActionUnavailable: the whole remote feature is unavailable with that
	// reason (until a green Test connection).
	ActionUnavailable = "unavailable"
	// ActionFreshInput: the input goes to cleanup, the push is retried once on
	// a fresh input, then the channel is "remote failed" for FailCooldown.
	ActionFreshInput = "fresh_input"
)

// Action maps an ERR_* code (live_input.errored, or a status reason) to it.
func Action(code string) string {
	switch code {
	case "ERR_STORAGE_QUOTA_EXHAUSTED", "ERR_MISSING_SUBSCRIPTION":
		return ActionUnavailable
	case "ERR_GOP_OUT_OF_RANGE", "ERR_UNSUPPORTED_VIDEO_CODEC", "ERR_UNSUPPORTED_AUDIO_CODEC", "ERR_UNHEALTHY":
		return ActionFreshInput
	}
	return ActionNone
}

// ErrorText is the admin page's words for a code.
func ErrorText(code string) string {
	switch code {
	case "ERR_STORAGE_QUOTA_EXHAUSTED":
		return "the Stream storage is full (" + code + "): buy more storage or lower max_push_hours"
	case "ERR_MISSING_SUBSCRIPTION":
		return "the account has no Stream subscription (" + code + ")"
	case "ERR_GOP_OUT_OF_RANGE":
		return "the keyframe interval is outside 2 to 8 s (" + code + ")"
	case "ERR_UNSUPPORTED_VIDEO_CODEC":
		return "the video is not H.264 (" + code + ")"
	case "ERR_UNSUPPORTED_AUDIO_CODEC":
		return "the audio is not AAC (" + code + ")"
	case "ERR_UNHEALTHY":
		return "the input was unhealthy (" + code + ")"
	}
	return code
}
