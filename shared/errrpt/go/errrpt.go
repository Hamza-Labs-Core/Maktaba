// Package errrpt is Maktaba's shared error-reporting surface (Story
// 21.5 / HLB-300).
//
// Today error handling is plain slog.Error with no id, no stack, no
// category and no alert path: a self-hoster who wants to be paged on
// failures gets nothing, and an error_id cannot cross a service
// boundary because none is ever minted. This package closes that gap
// with a thin, dependency-light contract:
//
//   - every reported error gets a UUIDv7 `error_id` (time-ordered, so
//     it sorts by occurrence and is safe as an audit_log.error_id),
//   - a `category` taxonomy field,
//   - a trimmed stack trace,
//   - structured emission through the existing slog logger (same
//     convention as shared/log — fielded, never string-concatenated),
//   - an optional, rate-limited webhook sink (Slack/Discord/generic
//     JSON) so opting into alerting is one env var, default-off.
//
// Default-off / no silent exfiltration (Story 21.5 AC-3 / 21.8 AC-1):
// with no webhook configured nothing leaves the host; Capture still
// mints the id and logs locally.
package errrpt

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Category is the coarse error taxonomy stamped on every report and
// surfaced in audit_log / cross-service metadata.
type Category string

const (
	CategoryInternal   Category = "internal"   // unclassified server fault
	CategoryDependency Category = "dependency" // downstream/db/grpc failure
	CategoryValidation Category = "validation" // bad input reaching deep code
	CategoryTimeout    Category = "timeout"
	CategorySecurity   Category = "security" // auth/keys/abuse
	CategoryPipeline   Category = "pipeline" // job/transcode failure
)

// errorIDKey carries an inbound error_id on the context so an error
// that crosses a service boundary keeps its id (Story 21.5 AC-4). The
// gRPC/HTTP propagation glue lives with the caller; this package owns
// the context key and the metadata header name so both ends agree.
type errorIDKey struct{}

// MetadataKey is the canonical wire key (gRPC metadata / HTTP header,
// lower-case) that carries error_id across service boundaries.
const MetadataKey = "x-error-id"

// WithErrorID stamps an inbound error_id on ctx so a later Capture in
// this process reuses it instead of minting a new one.
func WithErrorID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, errorIDKey{}, id)
}

// ErrorIDFromContext returns the propagated error_id, if any.
func ErrorIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(errorIDKey{}).(string)
	return v, ok && v != ""
}

// NewErrorID returns a fresh UUIDv7. Time-ordered so reports sort by
// occurrence and the value is a valid audit_log.error_id.
func NewErrorID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// uuid.NewV7 only errors if crypto/rand fails; fall back to v4
		// rather than panic in an error path.
		return uuid.NewString()
	}
	return id.String()
}

// Report is one captured error.
type Report struct {
	ErrorID    string
	Category   Category
	Message    string
	Stack      string
	OccurredAt time.Time
}

// Sink receives reports out of band (e.g. a webhook). Nil sink = no
// outbound. Send must not block the caller for long; the webhook sink
// rate-limits and drops rather than queueing unbounded.
type Sink interface {
	Send(ctx context.Context, r Report) error
}

// Reporter is the per-service entry point. Construct once in main and
// pass down, mirroring how shared/log's Default is wired.
type Reporter struct {
	log  *slog.Logger
	sink Sink
}

// New builds a Reporter. log is required (it is the always-on local
// path); sink is optional (nil = default-off, no outbound).
func New(log *slog.Logger, sink Sink) *Reporter {
	return &Reporter{log: log, sink: sink}
}

// Capture mints (or reuses a propagated) error_id, captures a trimmed
// stack, logs the error with the structured contract, dispatches to the
// sink if configured, and returns the error_id so the caller can put it
// on an HTTP response / audit row / gRPC trailer.
//
// Logging uses explicit fields (never msg concatenation) so it passes
// the 21.1 concat lint.
func (r *Reporter) Capture(ctx context.Context, cat Category, err error) string {
	if r == nil {
		return ""
	}
	id, ok := ErrorIDFromContext(ctx)
	if !ok {
		id = NewErrorID()
	}
	rep := Report{
		ErrorID:    id,
		Category:   cat,
		Message:    safeMsg(err),
		Stack:      captureStack(3),
		OccurredAt: time.Now().UTC(),
	}

	lg := r.log
	if lg == nil {
		lg = slog.Default()
	}
	lg.LogAttrs(ctx, slog.LevelError, "error reported",
		slog.String("event", "error_reported"),
		slog.String("error_id", rep.ErrorID),
		slog.String("category", string(rep.Category)),
		slog.String("error", rep.Message),
		slog.String("stack", rep.Stack),
	)

	if r.sink != nil {
		if serr := r.sink.Send(ctx, rep); serr != nil {
			// The sink failing must never mask the original error or
			// crash the caller. Story 21.5 EC2 (DSN-typo tolerance).
			lg.LogAttrs(ctx, slog.LevelWarn, "error sink dispatch failed",
				slog.String("event", "error_sink_failed"),
				slog.String("error_id", rep.ErrorID),
				slog.String("error", serr.Error()),
			)
		}
	}
	return rep.ErrorID
}

func safeMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// captureStack returns a compact stack trace skipping `skip` frames
// (the Capture plumbing). Trimmed to keep webhook payloads small and to
// avoid emitting the entire goroutine dump. Path masking of media-root
// paths is the caller's redaction concern (Story 21.8 EC2); this keeps
// only file:line:func which does not carry user content.
func captureStack(skip int) string {
	const maxFrames = 24
	pcs := make([]uintptr, maxFrames)
	n := runtime.Callers(skip, pcs)
	if n == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	for {
		f, more := frames.Next()
		b.WriteString(f.Function)
		b.WriteByte('\n')
		b.WriteByte('\t')
		b.WriteString(f.File)
		b.WriteByte(':')
		b.WriteString(itoa(f.Line))
		b.WriteByte('\n')
		if !more {
			break
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
