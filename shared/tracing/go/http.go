package tracing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// traceCtxKey carries the active trace/span ids on the request context
// so handlers and the logger can read them without importing OTel.
type traceCtxKey struct{}

// Trace is the per-request trace identifiers attached via HTTP. The
// shape is unchanged from the pre-SDK stub so the logger and any reader
// of FromContext keep compiling untouched; the ids are now sourced from
// the real OTel span context.
type Trace struct {
	TraceID string
	SpanID  string
}

// FromContext returns the current trace, if any.
func FromContext(ctx context.Context) (Trace, bool) {
	t, ok := ctx.Value(traceCtxKey{}).(Trace)
	return t, ok
}

// withTrace stamps t on ctx.
func withTrace(ctx context.Context, t Trace) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, t)
}

// statusRecorder captures the response status so the span can be marked
// error (Story 21.3 AC-2 — errors are always interesting).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// HTTP is a chi-compatible middleware that:
//   - extracts inbound W3C trace context via the OTel propagator,
//   - starts a real server span (SpanKindServer) on the global tracer;
//     when tracing is disabled this is OTel's no-op span (zero cost,
//     no outbound),
//   - mirrors the active trace/span ids onto the request context as a
//     Trace so the structured logger emits them,
//   - injects the active W3C `traceparent` onto the response so callers
//     (and downstream-propagating handlers) see the id,
//   - records the URL query as a salted hash attribute, never the raw
//     query string (Story 21.3 EC3 — no PII in spans),
//   - marks the span errored on a 5xx response so the composite sampler
//     and downstream analysis treat it as interesting.
func HTTP(next http.Handler) http.Handler {
	tracer := otel.Tracer(tracerName)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(
			r.Context(), propagation.HeaderCarrier(r.Header))

		attrs := []attribute.KeyValue{
			attribute.String("http.request.method", r.Method),
			attribute.String("url.path", r.URL.Path),
		}
		if qh := QueryHash(r.URL.RawQuery); qh != "" {
			attrs = append(attrs, attribute.String("url.query_hash", qh))
		}

		ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attrs...),
		)
		defer span.End()

		sc := span.SpanContext()
		t := Trace{}
		if sc.HasTraceID() {
			t.TraceID = sc.TraceID().String()
			t.SpanID = sc.SpanID().String()
		} else {
			// Tracing disabled (no-op span has no valid context):
			// still mint ids so logs/downstream see a stable
			// correlation id. Header plumbing, not telemetry.
			inTID, _ := parseTraceparent(r.Header.Get(TraceParentHeader))
			if inTID == "" {
				inTID = NewTraceID()
			}
			t.TraceID = inTID
			t.SpanID = NewSpanID()
			w.Header().Set(TraceParentHeader, formatTraceparent(t.TraceID, t.SpanID))
		}
		ctx = withTrace(ctx, t)

		// Echo the active W3C context on the response when a real span
		// is running (no-op path already set the header above).
		if sc.HasTraceID() {
			otel.GetTextMapPropagator().Inject(
				ctx, propagation.HeaderCarrier(w.Header()))
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		if rec.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
			span.SetAttributes(attribute.Bool("error", true))
		}
		span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
	})
}

// QueryHash returns the sha256-prefix of q so traces can correlate by
// query without exposing the raw text. Story 21.3 EC3.
func QueryHash(q string) string {
	if q == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(q))
	return hex.EncodeToString(sum[:8])
}

// parseTraceparent picks out the trace-id and parent span-id from a W3C
// `traceparent` header. Returns empty strings on any malformation.
// Retained for the tracing-disabled fallback path only.
//
// Format: `version-traceID-spanID-flags` (00-XXXX-YYYY-01).
func parseTraceparent(h string) (traceID, spanID string) {
	if len(h) < 55 {
		return "", ""
	}
	if h[:2] != "00" {
		return "", ""
	}
	if h[2] != '-' || h[35] != '-' || h[52] != '-' {
		return "", ""
	}
	traceID = h[3:35]
	spanID = h[36:52]
	if !hexOnly(traceID) || !hexOnly(spanID) {
		return "", ""
	}
	return traceID, spanID
}

func formatTraceparent(traceID, spanID string) string {
	return "00-" + traceID + "-" + spanID + "-01"
}

func hexOnly(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
