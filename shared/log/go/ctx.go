package log

import (
	"context"
	"log/slog"
)

// fieldKey is unexported so external packages cannot stuff arbitrary
// values into the logging context — they must go through the typed
// helpers below.
type fieldKey string

const (
	keyRequestID fieldKey = "request_id"
	keySessionID fieldKey = "session_id"
	keyJobID     fieldKey = "job_id"
	keyVideoID   fieldKey = "video_id"
	keyUserID    fieldKey = "user_id"
)

// WithRequestID attaches a request id to ctx; subsequent log.From(ctx)
// calls emit it as `request_id`.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// WithSessionID attaches a session id to ctx.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keySessionID, id)
}

// WithJobID attaches a job id to ctx.
func WithJobID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyJobID, id)
}

// WithVideoID attaches a video id to ctx.
func WithVideoID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyVideoID, id)
}

// WithUserID attaches a user id to ctx.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyUserID, id)
}

// From returns Default enriched with whichever contextual ids ctx
// carries. If Init has not been called it returns slog.Default(), so
// library code can call From safely from tests that never initialised
// the global.
func From(ctx context.Context) *slog.Logger {
	l := Default
	if l == nil {
		l = slog.Default()
	}
	if ctx == nil {
		return l
	}
	if v, ok := ctx.Value(keyRequestID).(string); ok && v != "" {
		l = l.With("request_id", v)
	}
	if v, ok := ctx.Value(keySessionID).(string); ok && v != "" {
		l = l.With("session_id", v)
	}
	if v, ok := ctx.Value(keyJobID).(string); ok && v != "" {
		l = l.With("job_id", v)
	}
	if v, ok := ctx.Value(keyVideoID).(string); ok && v != "" {
		l = l.With("video_id", v)
	}
	if v, ok := ctx.Value(keyUserID).(string); ok && v != "" {
		l = l.With("user_id", v)
	}
	return l
}

// ctxFields are the context's ids in the order they are written.
var ctxFields = []fieldKey{keyRequestID, keySessionID, keyJobID, keyVideoID, keyUserID}

// ctxHandler adds the context's ids (request_id, session_id, ...) to a record
// logged with a context (logger.WarnContext(ctx, ...)), so a line written by a
// component's own logger carries the request's id as From(ctx)'s lines do. An
// id the logger already carries (From(ctx), With("request_id", ...)) is not
// written twice.
type ctxHandler struct {
	slog.Handler
	has map[fieldKey]bool
}

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		for _, k := range ctxFields {
			if h.has[k] {
				continue
			}
			if v, ok := ctx.Value(k).(string); ok && v != "" {
				r.AddAttrs(slog.String(string(k), v))
			}
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	has := make(map[fieldKey]bool, len(h.has)+1)
	for k, v := range h.has {
		has[k] = v
	}
	for _, a := range attrs {
		for _, k := range ctxFields {
			if a.Key == string(k) {
				has[k] = true
			}
		}
	}
	return ctxHandler{Handler: h.Handler.WithAttrs(attrs), has: has}
}

func (h ctxHandler) WithGroup(name string) slog.Handler {
	return ctxHandler{Handler: h.Handler.WithGroup(name), has: h.has}
}
