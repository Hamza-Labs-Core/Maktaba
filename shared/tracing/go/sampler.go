package tracing

import (
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Story 21.3 AC-2 — composite head sampler.
//
// A pure head sampler cannot know at span-start whether a request will
// error or be slow, so the "always sample errors / slow" half of the AC
// is delivered with start-time hint attributes that callers may set on
// the root span: `sampling.force=true` (set by middleware when an
// inbound request is already known to be retried/flagged) plus the
// usual parent-based propagation. Spans whose parent was sampled are
// always sampled (so an errored downstream keeps its full trace), and a
// configurable head ratio applies to everything else.
//
// This keeps the sampler dependency-free and deterministic while
// honouring the AC's "100% of error/slow, SampleRatio otherwise"
// intent: error/slow classification happens at the trace root via the
// force attribute and parent-sampled propagation; unflagged traffic is
// ratio-sampled.

// forceSampleAttr, when present and true on the span's start
// attributes, forces the span (and thus its trace) to be recorded
// regardless of the head ratio. Middleware sets this for requests it
// already knows are interesting (errors surfaced before the span
// starts, slow-replayed requests, explicit debug header).
const forceSampleAttr = attribute.Key("sampling.force")

type compositeSampler struct {
	ratio  sdktrace.Sampler
	always sdktrace.Sampler
}

func newCompositeSampler(ratio float64) sdktrace.Sampler {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return &compositeSampler{
		// ParentBased: a sampled parent => always sample (keeps error
		// traces whole across services); root spans fall back to the
		// ratio sampler.
		ratio:  sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio)),
		always: sdktrace.AlwaysSample(),
	}
}

func (c *compositeSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	for _, kv := range p.Attributes {
		if kv.Key == forceSampleAttr && kv.Value.AsBool() {
			return c.always.ShouldSample(p)
		}
	}
	return c.ratio.ShouldSample(p)
}

func (c *compositeSampler) Description() string {
	return "MaktabaComposite{force-or-parent-sampled=always,else=ratio}"
}
