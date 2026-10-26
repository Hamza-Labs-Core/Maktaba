package telemetry

import (
	"context"
	"log/slog"
	"sort"
	"sync"
)

// maxErrorKinds bounds the distinct errors held between two reports; past it,
// a new kind is counted as "other".
const maxErrorKinds = 200

// maxReportErrors is how many error kinds a report carries (the most frequent).
const maxReportErrors = 50

// maxErrorRunes caps an error's message in a report.
const maxErrorRunes = 300

// ErrorCounts counts the server's errors by kind between two reports: every
// record logged at Error level, by its message and its "event" attribute (the
// codebase's stable name for a log line), never its other attributes (where a
// path, an id or a name would be). The report scrubs the message again.
type ErrorCounts struct {
	mu     sync.Mutex
	counts map[string]int
}

// ErrorCount is one kind of error and how often it was logged.
type ErrorCount struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func (c *ErrorCounts) add(kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	if _, ok := c.counts[kind]; !ok && len(c.counts) >= maxErrorKinds {
		kind = "other"
	}
	c.counts[kind]++
}

// Take returns the counts so far, the most frequent first, and starts again.
func (c *ErrorCounts) Take() []ErrorCount {
	c.mu.Lock()
	counts := c.counts
	c.counts = nil
	c.mu.Unlock()
	out := make([]ErrorCount, 0, len(counts))
	for k, n := range counts {
		out = append(out, ErrorCount{Message: k, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Message < out[j].Message
	})
	return out
}

// Put adds counts back (a report that could not be built keeps them).
func (c *ErrorCounts) Put(list []ErrorCount) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	for _, e := range list {
		c.counts[e.Message] += e.Count
	}
}

// Handler wraps next: every record is passed on unchanged, and an Error-level
// one is counted.
func (c *ErrorCounts) Handler(next slog.Handler) slog.Handler {
	return &countingHandler{next: next, counts: c}
}

type countingHandler struct {
	next   slog.Handler
	counts *ErrorCounts
	// event is the "event" attribute a With gave the logger
	event string
}

func (h *countingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelError || h.next.Enabled(ctx, l)
}

func (h *countingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		event := h.event
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "event" {
				event = a.Value.String()
				return false
			}
			return true
		})
		kind := r.Message
		if event != "" {
			kind = event + ": " + kind
		}
		h.counts.add(kind)
	}
	if !h.next.Enabled(ctx, r.Level) {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h *countingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	event := h.event
	for _, a := range attrs {
		if a.Key == "event" {
			event = a.Value.String()
		}
	}
	return &countingHandler{next: h.next.WithAttrs(attrs), counts: h.counts, event: event}
}

func (h *countingHandler) WithGroup(name string) slog.Handler {
	return &countingHandler{next: h.next.WithGroup(name), counts: h.counts, event: h.event}
}
