// Package tracing is the shared OpenTelemetry surface for Maktaba's Go
// services (Story 21.3). The current implementation is a stub: it owns
// the propagation header (W3C `traceparent`) and the on/off contract
// (`Init` is a no-op when the OTLP endpoint is empty), but the actual
// span processor / exporter is wired in a follow-up story so we don't
// pull the (large) OTel SDK into the dependency graph until tracing is
// validated end-to-end.
//
// The shape of the public API matches what the OTel-backed
// implementation will expose, so call sites in middleware and handlers
// don't need to change when the SDK lands.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// TraceParentHeader is the W3C trace-context propagation header.
// Story 21.3 AC-1 fixes this name across REST, GraphQL, gRPC.
const TraceParentHeader = "traceparent"

// Config drives Init.
type Config struct {
	// Service is the short service name (e.g. "api"). Stamped on every
	// emitted span as the otel `service.name` resource attribute when
	// the SDK lands.
	Service string
	// Env is the deployment environment (prod/dev/test). Stamped as
	// `deployment.environment` on every span.
	Env string
	// Version is the build-time version stamp.
	Version string
	// OTLPEndpoint, when non-empty, enables tracing. Empty means the
	// service runs with the noop tracer — no outbound connections,
	// no buffered spans, zero overhead. Story 21.3 AC-4: outbound is
	// off by default.
	OTLPEndpoint string
	// SampleRatio is the head-sampler ratio for non-error /
	// non-slow spans. Defaults to 0.01 (Story 21.3 AC-2).
	SampleRatio float64
}

// Shutdown is the cancellation hook returned by Init. Always safe to
// call; in noop mode it returns nil immediately.
type Shutdown func(context.Context) error

// Init wires the global tracer provider. The current stub returns a
// noop Shutdown when OTLPEndpoint is empty — exactly the behaviour
// Story 21.3 AC-4 requires.
//
// When the SDK lands, this function will:
//
//  1. Build a `resource.Resource` from Service / Env / Version.
//  2. Construct an OTLP/HTTP exporter pointed at OTLPEndpoint.
//  3. Wrap it with a BatchSpanProcessor (queue size 8192).
//  4. Build a composite head sampler that always-samples spans tagged
//     `error=true` or `slow=true` and otherwise applies SampleRatio.
//  5. Set the global TracerProvider and TextMapPropagator.
func Init(_ context.Context, cfg Config) (Shutdown, error) {
	if cfg.OTLPEndpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	// Stub: pretend we've installed an exporter. The follow-up story
	// replaces this branch with the real SDK setup.
	return func(context.Context) error { return nil }, nil
}

// NewTraceID returns a 16-byte hex-encoded trace id. Used by the
// stub-stage HTTP middleware to seed traceparent so downstream
// services see a valid header even before the SDK lands.
func NewTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewSpanID returns an 8-byte hex-encoded span id.
func NewSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
