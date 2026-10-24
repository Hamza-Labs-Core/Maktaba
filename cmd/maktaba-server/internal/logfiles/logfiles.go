// Package logfiles reads a machine's own log files over a time window, for the logs page
// and the diagnostics bundle: server.log (the supervisor's lines and every role's, as the
// app or the service writes them), app.log (the desktop app's updater) and launcher.log
// (the launcher's events, in the updates folder). That is where an update's story is
// (specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md); the api's own ring
// keeps only its newest lines.
//
// A line takes its time from itself (a JSON line's ts, time or timestamp; an RFC 3339 time
// at its start; slog's time=), else from the last line before it that had one: the
// supervisor's "[serve] …" lines and a program's continuation lines carry none.
package logfiles

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Names are the files a query may ask for, in their order.
var Names = []string{"server", "app", "launcher"}

// DefaultLimit and MaxLimit bound how many lines a query gets (the newest in its window).
const (
	DefaultLimit = 2000
	MaxLimit     = 10000
)

// tailBytes is how much of a file's end is read (server.log rotates at 50 MB).
const tailBytes = 16 << 20

// maxLine is the longest line kept (a longer one is cut, with an ellipsis).
const maxLine = 4 << 10

// Dirs are where a machine's files are.
type Dirs struct {
	Logs    string // server.log, server.err.log, app.log (each with its .1)
	Updates string // launcher.log
}

// Query is what a caller asks for.
type Query struct {
	Files []string  // of Names; empty: all
	Since time.Time // zero: from the start
	Until time.Time // zero: to the end
	Limit int       // 0: DefaultLimit
}

// Entry is one line.
type Entry struct {
	TS      string `json:"ts"` // RFC 3339; "" when neither it nor a line before it had a time
	Level   string `json:"level"`
	Service string `json:"service"`
	File    string `json:"file"`
	Msg     string `json:"msg"`
	Raw     string `json:"raw"`
	t       time.Time
}

// File says how a file was read.
type File struct {
	Name      string `json:"name"`
	Missing   bool   `json:"missing"`
	Truncated bool   `json:"truncated"`
}

// Result is a query's answer: entries oldest first, merged across the files.
type Result struct {
	Entries []Entry `json:"entries"`
	Files   []File  `json:"files"`
}

// ParseQuery reads files, since, until and limit (RFC 3339 times).
func ParseQuery(get func(string) string) (Query, error) {
	var q Query
	if v := strings.TrimSpace(get("files")); v != "" {
		for _, f := range strings.Split(v, ",") {
			f = strings.TrimSpace(f)
			if !known(f) {
				return q, fmt.Errorf("no such log file %q (one of %s)", f, strings.Join(Names, ", "))
			}
			q.Files = append(q.Files, f)
		}
	}
	for _, p := range []struct {
		name string
		t    *time.Time
	}{{"since", &q.Since}, {"until", &q.Until}} {
		if v := strings.TrimSpace(get(p.name)); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return q, fmt.Errorf("%s: not an RFC 3339 time", p.name)
			}
			*p.t = t
		}
	}
	if v := strings.TrimSpace(get("limit")); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
			return q, fmt.Errorf("limit: a positive number")
		}
		q.Limit = n
	}
	return q, nil
}

func known(f string) bool {
	for _, n := range Names {
		if f == n {
			return true
		}
	}
	return false
}

// paths are a file's pieces, oldest first.
func (d Dirs) paths(name string) []string {
	if (name == "launcher" && d.Updates == "") || (name != "launcher" && d.Logs == "") {
		return nil
	}
	switch name {
	case "server":
		return []string{filepath.Join(d.Logs, "server.log.1"), filepath.Join(d.Logs, "server.log"),
			filepath.Join(d.Logs, "server.err.log")}
	case "app":
		return []string{filepath.Join(d.Logs, "app.log.1"), filepath.Join(d.Logs, "app.log")}
	case "launcher":
		return []string{filepath.Join(d.Updates, "launcher.log")}
	}
	return nil
}

// Read answers a query from the files in d.
func Read(d Dirs, q Query) Result {
	files := q.Files
	if len(files) == 0 {
		files = Names
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	res := Result{Entries: []Entry{}, Files: []File{}}
	for _, name := range files {
		f := File{Name: name + ".log", Missing: true}
		var all []Entry
		var last time.Time // a rotated file's last time carries into the file after it
		for _, p := range d.paths(name) {
			es, cut, end, err := readFile(p, name, last)
			if err != nil {
				continue
			}
			last = end
			f.Missing = false
			f.Truncated = f.Truncated || cut
			all = append(all, es...)
		}
		for _, e := range all {
			if in(e, q) {
				res.Entries = append(res.Entries, e)
			}
		}
		res.Files = append(res.Files, f)
	}
	sort.SliceStable(res.Entries, func(i, j int) bool { return res.Entries[i].t.Before(res.Entries[j].t) })
	if len(res.Entries) > limit {
		res.Entries = res.Entries[len(res.Entries)-limit:]
		for i := range res.Files {
			res.Files[i].Truncated = true
		}
	}
	return res
}

// in: the entry is in the query's window (a line with no time only when it has no start).
func in(e Entry, q Query) bool {
	if e.t.IsZero() {
		return q.Since.IsZero()
	}
	return (q.Since.IsZero() || !e.t.Before(q.Since)) && (q.Until.IsZero() || !e.t.After(q.Until))
}

// Fit drops the oldest entries until the result encodes within limit bytes (an answer over
// the machine channel, whose frames are bounded), marking the files truncated.
func Fit(r Result, limit int) Result {
	sizes := make([]int, len(r.Entries))
	size := 0
	for i, e := range r.Entries {
		b, _ := json.Marshal(e) // as it goes out: escapes and all
		sizes[i] = len(b) + 1
		size += sizes[i]
	}
	cut := 0
	for size > limit && cut < len(r.Entries) {
		size -= sizes[cut]
		cut++
	}
	if cut > 0 {
		r.Entries = r.Entries[cut:]
		for i := range r.Files {
			r.Files[i].Truncated = true
		}
	}
	return r
}

// readFile reads a file's end (tailBytes) into entries, a line with no time taking
// last's (or the last before it in the file); cut: its start was not read; end: the
// last time in it.
func readFile(path, name string, last time.Time) (_ []Entry, cut bool, end time.Time, _ error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, last, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, last, err
	}
	if st.Size() > tailBytes {
		if _, err := f.Seek(st.Size()-tailBytes, io.SeekStart); err != nil {
			return nil, false, last, err
		}
		cut = true
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	file := strings.TrimSuffix(filepath.Base(path), ".1")
	var out []Entry
	first := cut
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if first {
			first = false // a partial line where the read began
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		e := parse(line, name)
		e.File = file
		if e.t.IsZero() {
			e.t = last
		} else {
			last = e.t
		}
		if !e.t.IsZero() {
			e.TS = e.t.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, e)
	}
	// a line longer than the scanner takes ends the read there: what came before stands
	return out, cut, last, nil
}

var (
	prefixRe = regexp.MustCompile(`^\[([A-Za-z0-9_.-]{1,32})\] ?`)
	slogTime = regexp.MustCompile(`(?:^|\s)time=("?)([0-9T:.+\-Z]+)("?)`)
	slogLvl  = regexp.MustCompile(`(?:^|\s)level=([A-Za-z]+)`)
	slogMsg  = regexp.MustCompile(`(?:^|\s)msg=("(?:[^"\\]|\\.)*"|\S+)`)
)

// timeLayouts are the times a line may start with or carry.
var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05,000", "2006-01-02 15:04:05.999999999", "2006/01/02 15:04:05.999999", "2006/01/02 15:04:05"}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parse reads one line: its time, level, service and message.
func parse(line, name string) Entry {
	e := Entry{Service: name, Raw: clip(line)}
	rest := line
	if m := prefixRe.FindStringSubmatch(rest); m != nil {
		e.Service = m[1]
		if e.Service == "serve" {
			e.Service = "server"
		}
		rest = rest[len(m[0]):]
	}
	if strings.HasPrefix(strings.TrimSpace(rest), "{") && json.Valid([]byte(strings.TrimSpace(rest))) {
		jsonLine(&e, strings.TrimSpace(rest))
		e.Msg = clip(e.Msg)
		return e
	}
	// an RFC 3339 time at the start (app.log, Go's log package)
	if sp := strings.IndexByte(rest, ' '); sp > 0 {
		if t := parseTime(rest[:sp]); !t.IsZero() {
			e.t, rest = t, rest[sp+1:]
		} else if sp2 := strings.IndexByte(rest[sp+1:], ' '); sp2 > 0 {
			if t := parseTime(rest[:sp+1+sp2]); !t.IsZero() {
				e.t, rest = t, rest[sp+1+sp2+1:]
			}
		}
	}
	if e.t.IsZero() {
		if m := slogTime.FindStringSubmatch(rest); m != nil {
			e.t = parseTime(m[2])
			if lv := slogLvl.FindStringSubmatch(rest); lv != nil {
				e.Level = strings.ToLower(lv[1])
			}
			if mm := slogMsg.FindStringSubmatch(rest); mm != nil {
				msg := mm[1]
				if strings.HasPrefix(msg, `"`) {
					if u, err := unquote(msg); err == nil {
						msg = u
					}
				}
				e.Msg = clip(msg)
				return e
			}
		}
	}
	e.Msg = clip(rest)
	return e
}

// jsonLine reads a structured line (the roles' mlog, the pipeline's, the launcher's events).
func jsonLine(e *Entry, s string) {
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		e.Msg = s
		return
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	e.t = parseTime(str("ts", "time", "timestamp", "@timestamp"))
	e.Level = strings.ToLower(str("level", "lvl", "severity", "levelname"))
	if svc := str("service", "svc", "logger"); svc != "" {
		e.Service = svc
	}
	e.Msg = str("msg", "message")
	if e.Msg == "" {
		if ev := str("event"); ev != "" {
			// a launcher event: its name, the version, its detail
			parts := []string{ev}
			if v := str("version"); v != "" {
				parts = append(parts, v)
			}
			if d, ok := m["detail"]; ok {
				if b, err := json.Marshal(d); err == nil && string(b) != "null" {
					parts = append(parts, string(b))
				}
			}
			e.Msg = strings.Join(parts, " ")
		}
	}
	if e.Msg == "" {
		e.Msg = s
	}
}

func unquote(s string) (string, error) {
	var out string
	err := json.Unmarshal([]byte(s), &out)
	return out, err
}

// clip cuts a line to maxLine bytes on a character boundary.
func clip(s string) string {
	if len(s) <= maxLine {
		return s
	}
	cut := maxLine
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// Raw writes the entries' raw lines, one per line (the bundle's files).
func Raw(es []Entry) []byte {
	var b bytes.Buffer
	for _, e := range es {
		b.WriteString(e.Raw)
		b.WriteByte('\n')
	}
	return b.Bytes()
}
