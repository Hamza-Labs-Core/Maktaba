package queues

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// Keyset cursors of the Epic 47 lists: (a time, a tie-break id), newest first.

func makeKeyset(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

// parseKeyset reads a cursor; "" is the first page (a nil time).
func parseKeyset(s string) (at any, id string, ok bool) {
	if s == "" {
		return nil, "", true
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, "", false
	}
	ts, rest, found := strings.Cut(string(b), "|")
	if !found || rest == "" {
		return nil, "", false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil, "", false
	}
	return t, rest, true
}

// parseLimit reads a page size: def when empty, else 1..most.
func parseLimit(s string, def, most int) (int, bool) {
	if s == "" {
		return def, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > most {
		return 0, false
	}
	return n, true
}
