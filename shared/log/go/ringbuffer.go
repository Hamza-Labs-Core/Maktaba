package log

// In-memory ring-buffer log sink for the troubleshooting export feature.
//
// The buffer keeps the last N already-formatted JSON log lines in
// memory so an operator (or the diagnostics-collection UI) can pull a
// recent window of structured logs without scraping files. It is wired
// as an *additional* slog handler — a fan-out writes every record to
// both the service's normal stderr/stdout handler and a JSON handler
// that emits into this buffer (see build()).
//
// Because the buffer stores the exact JSON the JSONHandler produced,
// every line already carries the Maktaba base-field contract
// (ts/level/service/msg/version/env) and has already been through the
// redaction ReplaceAttr — there is no second redaction path to keep in
// sync.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultRingCapacity is the number of log lines retained when a
// service enables the ring sink without an explicit capacity.
const DefaultRingCapacity = 10_000

// globalRing is the process-wide ring installed by Init. nil until Init
// runs (NewLogger never installs one — test loggers stay isolated).
var globalRing *RingBuffer

// Ring returns the process-wide ring buffer installed by Init, or nil
// if Init has not run or ring capture was disabled. The export handler
// drains matching entries from it.
func Ring() *RingBuffer { return globalRing }

// RingBuffer is a fixed-capacity, thread-safe store of the most recent
// formatted log lines.
//
// It implements io.Writer so a slog JSON handler can tee into it:
// slog's handler emits exactly one Write per record (one JSON object +
// trailing newline). The Write below splits on newlines defensively so
// a future batched writer can't corrupt the store.
type RingBuffer struct {
	mu    sync.RWMutex
	lines [][]byte
	cap   int
	next  int // index of the next slot to write
	count int // total lines ever written (>= cap once wrapped)
}

// NewRingBuffer allocates a ring of the given capacity. A non-positive
// capacity falls back to DefaultRingCapacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = DefaultRingCapacity
	}
	return &RingBuffer{lines: make([][]byte, capacity), cap: capacity}
}

var newline = []byte{'\n'}

// Write implements io.Writer. It returns len(p) and a nil error so it
// never disrupts the surrounding slog handler, even on a malformed
// line — a logging sink that fails the log call would be worse than a
// dropped buffer entry.
func (rb *RingBuffer) Write(p []byte) (int, error) {
	for _, raw := range bytes.Split(bytes.TrimRight(p, "\n"), newline) {
		if len(raw) == 0 {
			continue
		}
		// slog reuses its internal buffer across calls, so the slice
		// must be copied before we retain it.
		line := make([]byte, len(raw))
		copy(line, raw)
		rb.appendLine(line)
	}
	return len(p), nil
}

func (rb *RingBuffer) appendLine(line []byte) {
	rb.mu.Lock()
	rb.lines[rb.next] = line
	rb.next = (rb.next + 1) % rb.cap
	rb.count++
	rb.mu.Unlock()
}

// Len reports how many lines are currently retained (saturates at cap).
func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if rb.count < rb.cap {
		return rb.count
	}
	return rb.cap
}

// Entry is a parsed view of one stored log line. Raw is the verbatim
// JSON line so the export bundle can stream it back losslessly; the
// scalar fields are decoded only for filtering and UI display.
type Entry struct {
	Time    time.Time       `json:"-"`
	Level   string          `json:"level"`
	Service string          `json:"service"`
	Raw     json.RawMessage `json:"raw"`
}

// Filter narrows the entries returned by Entries. Zero-value fields are
// "no constraint" (any time, any service, all levels, no search).
type Filter struct {
	Since    time.Time           // drop entries strictly before this
	MinLevel slog.Level          // drop entries below this level
	Services map[string]struct{} // empty == all services
	Search   string              // case-insensitive substring match on the raw line
	Limit    int                 // keep only the newest N matches (0 == all)
}

// lineHeader is the cheap subset we decode to filter without
// unmarshalling the whole record.
type lineHeader struct {
	Ts      string `json:"ts"`
	Level   string `json:"level"`
	Service string `json:"service"`
}

// Entries returns the matching lines oldest→newest. Lines that fail to
// parse as JSON are skipped (a corrupt entry must not abort a
// diagnostics pull).
func (rb *RingBuffer) Entries(f Filter) []Entry {
	rb.mu.RLock()
	snapshot := rb.orderedLocked()
	rb.mu.RUnlock()

	var searchLower []byte
	if f.Search != "" {
		searchLower = []byte(strings.ToLower(f.Search))
	}

	out := make([]Entry, 0, len(snapshot))
	for _, line := range snapshot {
		var h lineHeader
		if err := json.Unmarshal(line, &h); err != nil {
			continue
		}
		if ParseLevel(h.Level) < f.MinLevel {
			continue
		}
		var ts time.Time
		if h.Ts != "" {
			ts, _ = time.Parse(time.RFC3339Nano, h.Ts)
		}
		if !f.Since.IsZero() && ts.Before(f.Since) {
			continue
		}
		if len(f.Services) > 0 {
			if _, ok := f.Services[h.Service]; !ok {
				continue
			}
		}
		if searchLower != nil && !bytes.Contains(bytes.ToLower(line), searchLower) {
			continue
		}
		out = append(out, Entry{
			Time:    ts,
			Level:   h.Level,
			Service: h.Service,
			Raw:     append(json.RawMessage(nil), line...),
		})
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[len(out)-f.Limit:]
	}
	return out
}

// orderedLocked snapshots the stored lines oldest→newest. Caller holds
// at least the read lock.
func (rb *RingBuffer) orderedLocked() [][]byte {
	out := make([][]byte, 0, rb.cap)
	if rb.count < rb.cap {
		for i := 0; i < rb.next; i++ {
			if rb.lines[i] != nil {
				out = append(out, rb.lines[i])
			}
		}
		return out
	}
	for i := 0; i < rb.cap; i++ {
		idx := (rb.next + i) % rb.cap
		if rb.lines[idx] != nil {
			out = append(out, rb.lines[idx])
		}
	}
	return out
}

// WriteJSONL streams the filtered entries to w as newline-delimited
// JSON (one raw record per line). This is exactly the *-logs.jsonl
// shape the export bundle wants, so the export handler can copy a
// service's ring straight into the tarball.
func (rb *RingBuffer) WriteJSONL(w io.Writer, f Filter) error {
	for _, e := range rb.Entries(f) {
		if _, err := w.Write(e.Raw); err != nil {
			return err
		}
		if _, err := w.Write(newline); err != nil {
			return err
		}
	}
	return nil
}

// ParseLevel maps a Maktaba level string ("debug"/"info"/"warn"/
// "error", as lower-cased by the handler's ReplaceAttr) to its
// slog.Level. Unknown values sort as Info so a stray line is never
// silently dropped by a level floor.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// fanoutHandler forwards every record to each downstream handler. It is
// the seam that lets one logger write to both the console/stderr
// handler and the ring-buffer JSON handler. WithAttrs/WithGroup fan out
// so the `.With(service, version, env)` base fields land in every sink.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			// Clone so a handler that mutates the record (slog handlers
			// are permitted to) can't corrupt a sibling sink.
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: hs}
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return fanoutHandler{handlers: hs}
}

// RecentHandler serves a ring buffer over HTTP for the log viewer and
// the cross-service export proxy. Mount it on a service's internal /
// admin port (never the public one — it carries operational logs).
//
// Query params (all optional):
//
//	since    RFC3339 timestamp lower bound (default: no bound)
//	level    minimum level: debug|info|warn|error (default: debug)
//	services comma-separated service-name allowlist (default: all)
//	q        case-insensitive substring filter on the raw line
//	limit    keep only the newest N matches (default: all)
//	format   "jsonl" (default, application/x-ndjson) or "json" (array)
func RecentHandler(rb *RingBuffer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rb == nil {
			http.Error(w, "log ring buffer not enabled", http.StatusServiceUnavailable)
			return
		}
		f := FilterFromQuery(r)
		if r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Type", "application/json")
			entries := rb.Entries(f)
			raws := make([]json.RawMessage, len(entries))
			for i, e := range entries {
				raws[i] = e.Raw
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": raws, "count": len(raws)})
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = rb.WriteJSONL(w, f)
	})
}

// FilterFromQuery parses the standard log-filter query params shared by
// RecentHandler and the API export endpoint.
func FilterFromQuery(r *http.Request) Filter {
	q := r.URL.Query()
	// Absent level means "no floor" (debug and up), matching the
	// documented default — the diagnostics bundle wants debug lines.
	// ParseLevel("") would otherwise floor at info and silently drop
	// them.
	f := Filter{MinLevel: slog.LevelDebug}
	if lvl := strings.TrimSpace(q.Get("level")); lvl != "" {
		f.MinLevel = ParseLevel(lvl)
	}
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			f.Since = ts
		}
	}
	if v := strings.TrimSpace(q.Get("services")); v != "" {
		set := map[string]struct{}{}
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				set[s] = struct{}{}
			}
		}
		f.Services = set
	}
	f.Search = strings.TrimSpace(q.Get("q"))
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			f.Limit = n
		}
	}
	return f
}
