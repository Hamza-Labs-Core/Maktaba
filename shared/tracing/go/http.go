package tracing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// traceCtxKey carries the active trace/span ids on the request
// context. The full SDK will replace this with `trace.SpanContext`;
// for the skeleton stage we hand-roll it so handlers and the logger
// can read the ids today without pulling in OTel.
type traceCtxKey struct{}

// Trace is the per-request trace identifiers attached via Middleware.
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

// HTTP is a chi-compatible middleware that
//   - parses an inbound `traceparent` header (W3C trace-context),
//     reusing its trace-id when present,
//   - attaches a Trace to the request context,
//   - echoes the active `traceparent` on the response so curl users
//     can see the id their request belonged to,
//   - hashes the URL query string into the span attribute (EC3 of
//     Story 21.3) — never propagates the raw query.
//
// When the OTel SDK lands, this middleware swaps to `otelhttp.NewHandler`
// and the noop ids stop appearing on the wire.
func HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, spanID := parseTraceparent(r.Header.Get(TraceParentHeader))
		if traceID == "" {
			traceID = NewTraceID()
		}
		spanID = NewSpanID()
		t := Trace{TraceID: traceID, SpanID: spanID}
		ctx := withTrace(r.Context(), t)
		w.Header().Set(TraceParentHeader, formatTraceparent(traceID, spanID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// QueryHash returns the sha256-prefix of q so traces can correlate by
// query without exposing the raw text. EC3.
func QueryHash(q string) string {
	if q == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(q))
	return hex.EncodeToString(sum[:8])
}

// parseTraceparent picks out the trace-id and parent span-id from a
// W3C `traceparent` header. Returns empty strings on any malformation;
// the caller falls back to minting fresh ids.
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
