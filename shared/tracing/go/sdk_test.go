package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// installRecorder swaps the global tracer provider for one backed by an
// in-memory exporter so a test can assert spans were actually emitted —
// the exact thing the old stub could never do.
func installRecorder(t *testing.T, sampler sdktrace.Sampler) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	if sampler == nil {
		sampler = sdktrace.AlwaysSample()
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sampler),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return sr
}

func TestHTTPEmitsRealServerSpan(t *testing.T) {
	sr := installRecorder(t, nil)

	h := HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, ok := FromContext(r.Context())
		if !ok || len(tr.TraceID) != 32 {
			t.Fatalf("trace not propagated to handler: %+v ok=%v", tr, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/videos?q=secret", nil)
	h.ServeHTTP(w, r)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 ended span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name() != "GET /videos" {
		t.Fatalf("span name = %q", s.Name())
	}
	var sawQueryHash, sawRawQuery bool
	for _, a := range s.Attributes() {
		if a.Key == "url.query_hash" {
			sawQueryHash = true
			if a.Value.AsString() == "q=secret" {
				t.Fatal("query hash leaked raw query")
			}
		}
		if a.Value.AsString() == "q=secret" {
			sawRawQuery = true
		}
	}
	if !sawQueryHash {
		t.Fatal("missing url.query_hash attribute (EC3)")
	}
	if sawRawQuery {
		t.Fatal("raw query string present in span attributes (EC3 violation)")
	}
	// Response carries a W3C traceparent for the emitted span.
	if got := w.Header().Get(TraceParentHeader); got == "" {
		t.Fatal("response missing traceparent")
	}
}

func TestHTTPMarksServerErrorSpan(t *testing.T) {
	sr := installRecorder(t, nil)
	h := HTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	var errored bool
	for _, a := range spans[0].Attributes() {
		if a.Key == "error" && a.Value.AsBool() {
			errored = true
		}
	}
	if !errored {
		t.Fatal("5xx span not marked error=true")
	}
}

func TestHTTPContinuesInboundTrace(t *testing.T) {
	installRecorder(t, nil)
	var seen string
	h := HTTP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		tr, _ := FromContext(r.Context())
		seen = tr.TraceID
	}))
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set(TraceParentHeader,
		"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("inbound trace not continued: got %q", seen)
	}
}

func TestCompositeSamplerForceAlwaysSamples(t *testing.T) {
	s := newCompositeSampler(0) // ratio 0 => unforced never sampled
	forced := s.ShouldSample(sdktrace.SamplingParameters{
		Name:       "forced",
		Attributes: []attribute.KeyValue{{Key: forceSampleAttr, Value: attribute.BoolValue(true)}},
	})
	if forced.Decision != sdktrace.RecordAndSample {
		t.Fatalf("forced span not sampled: %v", forced.Decision)
	}
	unforced := s.ShouldSample(sdktrace.SamplingParameters{Name: "unforced"})
	if unforced.Decision == sdktrace.RecordAndSample {
		t.Fatal("ratio-0 unforced span unexpectedly sampled")
	}
}

func TestInitNoopWhenEndpointEmpty(t *testing.T) {
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	sh, err := Init(context.Background(), Config{Service: "api"})
	if err != nil {
		t.Fatalf("Init err: %v", err)
	}
	if sh == nil {
		t.Fatal("nil shutdown")
	}
	if err := sh(context.Background()); err != nil {
		t.Fatalf("noop shutdown err: %v", err)
	}
	// Propagator must still be installed even with tracing off (header
	// plumbing, not telemetry).
	if otel.GetTextMapPropagator() == nil {
		t.Fatal("propagator not installed in noop mode")
	}
}
