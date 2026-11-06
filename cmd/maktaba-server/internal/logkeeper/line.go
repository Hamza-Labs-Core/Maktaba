// Package logkeeper is the log keeper (Epic 53 story 53.02): every supervised process's
// output, on disk, per service, capped and rotated, written by the supervisor and needing
// nothing else running (the API, Postgres and the log index can all be down).
//
// Each child writes its stdout and stderr to its own spool file (Spool); a follower reads
// it, turns each line into a shared-schema line (shared/log/schema/line.schema.json: a
// structured line is kept, anything else is wrapped as msg "raw_line" with its text in
// attrs.text), fills service, node, ts and the Epic 51 component, scrubs it once more with
// the shared redaction list, and appends it to the service's hourly segment:
//
//	<root>/segments/<service>/<yyyymmdd-hh>.ndjson      the open hour
//	<root>/segments/<service>/<yyyymmdd-hh>.ndjson.gz   closed hours
//	<root>/segments/<service>/<...>.idx                 first/last ts, lines, levels
//	<root>/segments/<service>/debug/...                 debug lines (kept 24 h)
//	<root>/segments/<service>/warn/...                  a copy of warn and above (kept last)
//
// A spool file, not a pipe: a child never blocks on its log, and one that outlives the
// supervisor (the TV processes across a handover, story 35.7) keeps writing where the
// next supervisor reads.
package logkeeper

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logfiles"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// MaxLine is the longest line kept; a longer one is cut and marked attrs.cut.
const MaxLine = 64 << 10

// Levels, lowest first.
var levelRank = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3, "fatal": 4}

// Rank is a level's order (an unknown level reads as info).
func Rank(level string) int {
	if r, ok := levelRank[level]; ok {
		return r
	}
	return 1
}

// normLevel maps the levels the roles write (slog's, structlog's, Python logging's) to
// the schema's five.
func normLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "trace":
		return "debug"
	case "warn", "warning":
		return "warn"
	case "error", "err", "exception":
		return "error"
	case "fatal", "critical", "panic":
		return "fatal"
	}
	return "info"
}

// Origin is who wrote a line: the service (the process's role, or an app), the
// supervised instance and its catalog component.
type Origin struct {
	Service   string
	Instance  string
	Component string
}

// Line is one kept line: the object as written to the segment, its time and level.
type Line struct {
	TS    time.Time
	Level string
	JSON  []byte
}

// Normalize turns one line of a child's output into a shared-schema line.
func Normalize(raw []byte, o Origin, node string, now time.Time) Line {
	raw = bytes.TrimRight(raw, "\r\n")
	cut := false
	if len(raw) > MaxLine {
		raw, cut = raw[:MaxLine], true
	}
	var obj map[string]any
	trimmed := bytes.TrimSpace(raw)
	// serve stamps its own lines with their time: "<RFC 3339> {json}" is a JSON line too
	if i := bytes.IndexByte(trimmed, '{'); i > 0 && i <= 40 {
		if t := parseTS(string(bytes.TrimSpace(trimmed[:i]))); !t.IsZero() {
			trimmed = trimmed[i:]
		}
	}
	if len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(trimmed, &obj) == nil && looksStructured(obj) {
		scrubObject(obj)
	} else {
		text := string(raw)
		ts, level := logfiles.Guess(text)
		obj = map[string]any{
			"msg":   "raw_line",
			"level": normLevel(level),
			"attrs": map[string]any{"text": mlog.Scrub(text)},
		}
		if !ts.IsZero() {
			obj["ts"] = ts.UTC().Format(time.RFC3339Nano)
		}
	}
	if cut {
		attrs, _ := obj["attrs"].(map[string]any)
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrs["cut"] = true
		obj["attrs"] = attrs
	}
	// structlog's event name, renamed as the pipeline's JSON renderer does in prod
	if _, ok := obj["msg"]; !ok {
		if ev, ok := obj["event"].(string); ok {
			obj["msg"] = ev
			delete(obj, "event")
		} else {
			obj["msg"] = "raw_line"
		}
	}
	level := normLevel(str(obj, "level"))
	obj["level"] = level
	ts := parseTS(str(obj, "ts"))
	if ts.IsZero() {
		ts = parseTS(str(obj, "time"))
		delete(obj, "time")
	}
	if ts.IsZero() {
		ts = now
	}
	obj["ts"] = ts.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if str(obj, "service") == "" && o.Service != "" {
		obj["service"] = o.Service
	}
	if str(obj, "node") == "" && node != "" {
		obj["node"] = node
	}
	if str(obj, "component") == "" && o.Component != "" {
		obj["component"] = o.Component
	}
	if o.Instance != "" && str(obj, "instance") == "" {
		obj["instance"] = o.Instance
	}
	b, err := json.Marshal(obj)
	if err != nil {
		b = []byte(`{"msg":"raw_line","level":"warn","attrs":{"text":"an unreadable line"}}`)
	}
	return Line{TS: ts.UTC(), Level: level, JSON: b}
}

// looksStructured: a JSON object that is a log record (it names a message or a level).
func looksStructured(m map[string]any) bool {
	for _, k := range []string{"msg", "event", "level"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// scrubObject applies the shared list once more (defence in depth): masked keys, scrubbed
// strings, and a user id written as its hash.
func scrubObject(m map[string]any) {
	if v, ok := m["user_id"]; ok {
		delete(m, "user_id")
		if h := mlog.HashUser(toString(v)); h != "" {
			m["user_h"] = h
		}
	}
	rules := mlog.SharedRedactRules()
	for k, v := range m {
		switch t := v.(type) {
		case string:
			if rules.KeySensitive(k, true) {
				m[k] = rules.KeyMask
			} else {
				m[k] = mlog.Scrub(t)
			}
		case map[string]any:
			scrubObject(t)
		}
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00"} {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
