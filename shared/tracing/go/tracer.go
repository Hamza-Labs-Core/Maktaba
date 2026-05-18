// Package tracing is the shared OpenTelemetry surface for Maktaba's Go
// services (Story 21.3 / HLB-273).
//
// Contract:
//
//   - Init wires a real OTel TracerProvider with an OTLP/HTTP span
//     exporter and a composite head sampler (always-sample error/slow,
//     SampleRatio otherwise) when an OTLP endpoint is configured.
//   - Init is a genuine no-op when the OTLP endpoint is empty: no
//     exporter, no outbound connections, the global provider stays the
//     OTel default no-op TracerProvider. Story 21.3 AC-4 (outbound is
//     off by default; never silent exfiltration).
//   - The W3C `traceparent` / `tracecontext` propagator is installed so
//     trace context crosses REST/GraphQL/gRPC boundaries.
//
// The public surface (Init / Config / Shutdown / TraceParentHeader /
// the HTTP middleware / FromContext / QueryHash) is unchanged from the
// stub it replaces, so existing call sites in middleware, handlers and
// the logger do not change.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TraceParentHeader is the W3C trace-context propagation header.
// Story 21.3 AC-1 fixes this name across REST, GraphQL, gRPC.
const TraceParentHeader = "traceparent"

// tracerName is the instrumentation scope name stamped on every span
// this package emits.
const tracerName = "github.com/Hamza-Labs-Core/Maktaba/shared/tracing/go"

// Config drives Init.
type Config struct {
	// Service is the short service name (e.g. "api"). Stamped on every
	// emitted span as the otel `service.name` resource attribute.
	Service string
	// Env is the deployment environment (prod/dev/test). Stamped as
	// `deployment.environment` on every span.
	Env string
	// Version is the build-time version stamp.
	Version string
	// OTLPEndpoint, when non-empty, enables tracing. Empty means the
	// service runs with the OTel default no-op tracer — no outbound
	// connections, no buffered spans, zero overhead. Story 21.3 AC-4:
	// outbound is off by default.
	//
	// The value is an OTLP/HTTP endpoint host:port (e.g.
	// "localhost:4318"); the exporter posts protobuf spans to
	// `/v1/traces` on it. Use OTLPInsecure to allow plain HTTP.
	OTLPEndpoint string
	// OTLPInsecure allows the exporter to talk plain HTTP to
	// OTLPEndpoint. Defaults to false (TLS). Self-hosters running a
	// local collector on loopback typically set this true.
	OTLPInsecure bool
	// SampleRatio is the head-sampler ratio for non-error /
	// non-slow spans. Defaults to 0.01 (Story 21.3 AC-2) when <= 0.
	SampleRatio float64
}

// Shutdown is the cancellation hook returned by Init. Always safe to
// call; in noop mode it returns nil immediately.
type Shutdown func(context.Context) error

// noopShutdown is the Shutdown returned when tracing is disabled.
func noopShutdown(context.Context) error { return nil }

// Init wires the global tracer provider and W3C propagator.
//
//   - Empty OTLPEndpoint  → genuine no-op. The global TracerProvider is
//     left as OTel's default (no spans, no exporter, no outbound
//     sockets). The W3C propagator is still installed so an inbound
//     traceparent is parsed/echoed even with tracing off — that is
//     header plumbing, not telemetry exfiltration.
//   - Non-empty OTLPEndpoint → real OTLP/HTTP exporter behind a
//     BatchSpanProcessor, wrapped by the composite head sampler.
//
// Returns a Shutdown that flushes the batch processor and closes the
// exporter; callers defer it on process shutdown.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	// Always install the W3C tracecontext + baggage propagator so trace
	// context crosses service boundaries regardless of whether THIS
	// service exports spans. Story 21.3 AC-1.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.OTLPEndpoint == "" {
		// No-op: leave the global no-op TracerProvider in place. Story
		// 21.3 AC-4 — zero outbound, default-off.
		return noopShutdown, nil
	}

	ratio := cfg.SampleRatio
	if ratio <= 0 {
		ratio = 0.01
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(nonEmpty(cfg.Service, "unknown")),
			semconv.ServiceVersion(nonEmpty(cfg.Version, "unknown")),
			semconv.DeploymentEnvironment(nonEmpty(cfg.Env, "dev")),
		),
	)
	if err != nil {
		return noopShutdown, fmt.Errorf("tracing: build resource: %w", err)
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.OTLPInsecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return noopShutdown, fmt.Errorf("tracing: build otlp exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		// BatchSpanProcessor with a bounded queue: spans are dropped (not
		// buffered unbounded) under backpressure. Story 21.3 EC1.
		sdktrace.WithBatcher(exp,
			sdktrace.WithMaxQueueSize(8192),
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithSampler(newCompositeSampler(ratio)),
	)
	otel.SetTracerProvider(tp)

	return func(sctx context.Context) error {
		// Flush queued spans then close the exporter. Story 21.3 EC —
		// shutdown drain.
		if err := tp.Shutdown(sctx); err != nil {
			return fmt.Errorf("tracing: provider shutdown: %w", err)
		}
		return nil
	}, nil
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// NewTraceID returns a 16-byte hex-encoded trace id. Retained for the
// HTTP middleware fallback path (used only when no SDK provider is
// active so downstream services still see a valid traceparent).
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
