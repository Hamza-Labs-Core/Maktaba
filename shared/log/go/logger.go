// Package log is the shared structured logging surface for Maktaba's Go
// services (Story 21.1). One global logger per service: JSON in prod,
// human-readable text in dev, with a shared base-field contract across
// every line.
//
// Usage:
//
//	log.Init(log.Options{Service: "api", Env: "prod", Version: "v1.2.3"})
//	log.From(ctx).Info("video imported", "duration_s", 12.4)
//
// Required base fields on every line: ts (RFC 3339 UTC), level, service,
// msg, version, env. Contextual fields (request_id, session_id, job_id,
// video_id, user_id) are injected via context.
//
// Sensitive field names listed in DefaultRedactedFields are masked
// before emission.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Options carries the per-service configuration accepted by Init.
type Options struct {
	// Service is the short service name (e.g. "api", "streaming", "pipeline").
	// Required; emitted as the `service` field on every line.
	Service string
	// Env is the deployment environment (e.g. "prod", "dev", "test").
	// Selects the JSON vs. text handler when Format is empty.
	Env string
	// Version is the build-time version stamp (Story 22.2). Emitted on
	// every line so logs from different binaries can be told apart.
	Version string
	// Format forces "json" or "text" regardless of Env. Empty means auto:
	// "json" for env=="prod", "text" otherwise.
	Format string
	// Level is the initial level. Zero value (LevelInfo) is the default.
	Level slog.Level
	// Output is the writer logs are emitted to. nil means os.Stdout.
	Output io.Writer
	// RedactedFields overrides DefaultRedactedFields. nil means use defaults.
	RedactedFields []string
	// RingCapacity sizes the in-memory ring-buffer sink installed by
	// Init (the troubleshooting-export buffer). Zero means
	// DefaultRingCapacity; a negative value disables the ring entirely.
	// NewLogger never installs a ring regardless of this field.
	RingCapacity int
}

var (
	levelVar = new(slog.LevelVar)
	// Default is the process-wide logger. Set by Init; nil before Init.
	Default *slog.Logger

	initOnce sync.Once
)

// Init configures the global logger. Calling Init more than once is
// allowed but only the first call takes effect — subsequent calls are
// silently ignored. This matches the "one logger per process" contract
// in the story.
func Init(opts Options) *slog.Logger {
	initOnce.Do(func() {
		// Install the process-wide ring sink unless explicitly disabled
		// (RingCapacity < 0). The export endpoint drains from globalRing.
		if opts.RingCapacity >= 0 {
			capacity := opts.RingCapacity
			if capacity == 0 {
				capacity = DefaultRingCapacity
			}
			globalRing = NewRingBuffer(capacity)
		}
		Default = build(opts, globalRing)
		slog.SetDefault(Default)
		installSIGUSR1()
	})
	return Default
}

// build constructs a logger from opts without touching the global. Used
// by Init and exposed (via NewLogger) for tests that need an isolated
// logger. When ring is non-nil the returned logger tees every record
// into it (as JSON) in addition to the primary stderr/stdout handler.
func build(opts Options, ring *RingBuffer) *slog.Logger {
	if opts.Service == "" {
		opts.Service = "unknown"
	}
	if opts.Env == "" {
		opts.Env = "dev"
	}
	if opts.Version == "" {
		opts.Version = "unknown"
	}
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}
	levelVar.Set(opts.Level)

	redacted := opts.RedactedFields
	if redacted == nil {
		redacted = DefaultRedactedFields
	}
	redactSet := make(map[string]struct{}, len(redacted))
	for _, k := range redacted {
		redactSet[strings.ToLower(k)] = struct{}{}
	}

	hopts := &slog.HandlerOptions{
		Level:       levelVar,
		ReplaceAttr: makeReplaceAttr(redactSet),
	}

	format := opts.Format
	if format == "" {
		if opts.Env == "prod" {
			format = "json"
		} else {
			format = "text"
		}
	}

	var h slog.Handler
	switch format {
	case "json":
		h = slog.NewJSONHandler(out, hopts)
	default:
		h = slog.NewTextHandler(out, hopts)
	}

	// Tee into the ring buffer as JSON regardless of the primary
	// format, so the buffer is always structured for the export bundle.
	// Reusing hopts keeps the same level floor + redaction ReplaceAttr.
	if ring != nil {
		h = fanoutHandler{handlers: []slog.Handler{h, slog.NewJSONHandler(ring, hopts)}}
	}

	return slog.New(h).With(
		"service", opts.Service,
		"version", opts.Version,
		"env", opts.Env,
	)
}

// NewLogger builds an isolated logger for tests. It does not touch the
// global Default and does not install signal handlers.
func NewLogger(opts Options) *slog.Logger {
	return build(opts, nil)
}

// NewLoggerWithRing builds an isolated logger that tees every record
// into the supplied ring buffer (as JSON) in addition to its primary
// handler. It does not touch the global Default or install signal
// handlers, so tests and embedders can wire a dedicated buffer without
// the process-wide Init.
func NewLoggerWithRing(opts Options, ring *RingBuffer) *slog.Logger {
	return build(opts, ring)
}

// SetLevel changes the active log level at runtime. Used by the SIGUSR1
// handler and by the admin endpoint (Story 23 wires the HTTP route).
func SetLevel(l slog.Level) { levelVar.Set(l) }

// Level returns the active log level.
func Level() slog.Level { return levelVar.Level() }

// makeReplaceAttr returns an slog.HandlerOptions.ReplaceAttr function
// that:
//  1. renames the standard time/level/msg keys to the Maktaba contract
//     (`ts` / `level` / `msg`);
//  2. masks any field whose key is in the redact set;
//  3. truncates very large `msg` values to keep a single log line under
//     the 64 KiB pipe buffer (EC1).
func makeReplaceAttr(redact map[string]struct{}) func([]string, slog.Attr) slog.Attr {
	return func(_ []string, a slog.Attr) slog.Attr {
		switch a.Key {
		case slog.TimeKey:
			return slog.String("ts", a.Value.Time().UTC().Format(time.RFC3339Nano))
		case slog.LevelKey:
			return slog.String("level", strings.ToLower(a.Value.String()))
		case slog.MessageKey:
			if v := a.Value.String(); len(v) > maxMsgBytes {
				// Inline marker — slog.HandlerOptions.ReplaceAttr can
				// only return one attr, so we cannot emit a separate
				// `truncated: true` sibling field.
				return slog.String("msg", v[:maxMsgBytes-len(truncationSuffix)]+truncationSuffix)
			}
			return a
		}
		if _, ok := redact[strings.ToLower(a.Key)]; ok {
			return slog.String(a.Key, redactedValue)
		}
		return a
	}
}

// maxMsgBytes is the upper bound on a single `msg` field. The OS pipe
// buffer is 64 KiB on Linux; we leave 4 KiB for the surrounding JSON
// envelope.
const maxMsgBytes = 60_000

const redactedValue = "***REDACTED***"

// truncationSuffix is appended to oversized `msg` values so log
// readers can spot when a line was clipped.
const truncationSuffix = " ...[truncated]"
