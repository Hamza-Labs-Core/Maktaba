package tvremote

// Story 32.11 AC4: the Remote panel's "last 20 remote events", read from the
// API's in-memory log ring (the one the Logs page reads). Only the remote
// events (design §10) and only their safe fields: the logs never carry a
// stream key or a token (TestNoSecretsInLogs), and no address is copied.

import (
	"encoding/json"
	"strings"
	"time"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Event is one remote event line.
type Event struct {
	At        time.Time `json:"at"`
	Level     string    `json:"level"`
	Event     string    `json:"event"`
	Message   string    `json:"message"`
	ChannelID string    `json:"channel_id,omitempty"`
	PushID    string    `json:"push_id,omitempty"`
	HostID    string    `json:"host_id,omitempty"`
	Code      string    `json:"code,omitempty"`
}

// remoteEvent reports whether an event= value is one of the remote events.
func remoteEvent(ev string) bool {
	for _, p := range []string{"tv_push_", "tv_remote_", "tv_budget_", "tv_cf_", "tv_recording_deleted",
		"tv_storage_full", "tv_input_", "tv_session_timeout", "tv_signing_key_rotated"} {
		if strings.HasPrefix(ev, p) {
			return true
		}
	}
	return false
}

// ParseEvents keeps the remote events of raw JSON log lines, newest last, at
// most limit.
func ParseEvents(lines []json.RawMessage, limit int) []Event {
	out := []Event{}
	for _, raw := range lines {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		str := func(k string) string {
			v, _ := m[k].(string)
			return v
		}
		ev := str("event")
		if !remoteEvent(ev) {
			continue
		}
		e := Event{Level: str("level"), Event: ev, Message: str("msg"), ChannelID: str("channel_id"),
			PushID: str("push_id"), HostID: str("host_id"), Code: str("code")}
		for _, k := range []string{"ts", "time"} {
			if t, err := time.Parse(time.RFC3339Nano, str(k)); err == nil {
				e.At = t.UTC()
				break
			}
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// events reads the last remote events (Controller.EventSource, else the ring).
func (c *Controller) events(limit int) []Event {
	var lines []json.RawMessage
	if c.EventSource != nil {
		lines = c.EventSource()
	} else if r := mlog.Ring(); r != nil {
		for _, e := range r.Entries(mlog.Filter{Search: `"event":"tv_`}) {
			lines = append(lines, e.Raw)
		}
	}
	return ParseEvents(lines, limit)
}
