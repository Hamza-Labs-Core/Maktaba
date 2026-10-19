package guide

import (
	"context"
	"strings"
	"time"
)

// onAirWithin mirrors channel_runtime's stale window (handlers/tv).
const onAirWithin = 45 * time.Second

// onAir is the set of channels encoding now on any streaming host
// (channel_runtime with a fresh heartbeat). The live player prefetches the
// playlists of on-air neighbours only (Epic 34.6 / 32.9): asking for an
// idle channel's playlist would start its encoder. Any error: none.
func (h *Handler) onAir(ctx context.Context, now time.Time) map[string]bool {
	out := map[string]bool{}
	if h.DB == nil {
		return out
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT channel_id, state, COALESCE(heartbeat_at, last_segment_at) FROM channel_runtime`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		var beat any
		if rows.Scan(&id, &state, &beat) != nil {
			return out
		}
		t, ok := asTime(beat)
		if ok && state != "idle" && now.Sub(t) <= onAirWithin {
			out[strings.ToLower(id)] = true
		}
	}
	return out
}

func asTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case []byte:
		return parseTS(string(x))
	case string:
		return parseTS(x)
	}
	return time.Time{}, false
}

func parseTS(s string) (time.Time, bool) {
	for _, l := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(l, strings.TrimSpace(s)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
