# Story 7.18 — gRPC clients to Pipeline and Streaming

The API consumes the gRPC schemas from `shared/proto/` (§9.9). One
client wrapper per service, with timeouts, retries, and circuit breaking.

**AC-1 — Pipeline client interface.**
- **Given** the generated `pipeline.PipelineClient`,
- **When** wrapped,
- **Then** the API exposes
  `pipeline.Embed(ctx, text) (Vector, error)`,
  `pipeline.Transcribe(ctx, req) (<-chan TranscribeEvent, error)`,
  `pipeline.ExtractEmbeddedSubtitle(ctx, video_id, stream_index) (Path, error)`,
  `pipeline.ListBackends(ctx) ([]Backend, error)`,
  `pipeline.HealthCheck(ctx) (Status, error)`,
  with per-call deadlines from config.
  `ExtractEmbeddedSubtitle` is consumed by the Streaming Service via
  Pipeline (Pipeline Story 4.4); the API exposes the wrapper because
  some integration tests stub it from this side.

**AC-2 — Streaming client interface.**
- **Given** the generated `streaming.StreamingClient`,
- **When** wrapped,
- **Then** the API exposes `streaming.OpenSession`,
  `streaming.CloseSession`, `streaming.EvictHashCache`,
  `streaming.GetCapabilities`, `streaming.HealthCheck`. The
  `GetCapabilities` RPC is distinct from `HealthCheck` and returns
  `{codecs, hwaccel, max_bitrate_kbps, supported_containers,
  transcode_slots: {used, capacity}}` without performing a liveness
  check (cached at the Streaming binary; refreshed on
  `LISTEN profiles_changed`).

**AC-3 — Retry and circuit breaker.**
- **Given** a transient gRPC failure (`UNAVAILABLE`, `DEADLINE_EXCEEDED`),
- **When** the client retries,
- **Then** retries are bounded (default 3, jittered exponential backoff),
  and after `failure_rate > 50% in 30 s` the breaker opens for 10 s and
  fails fast with a `circuit-open` error.

**AC-4 — Context propagation.**
- **Given** an incoming HTTP request with `X-Request-Id`,
- **When** the gRPC call is made,
- **Then** the request ID is carried over via gRPC metadata
  (`maktaba-request-id`) and appears in the receiving service's logs.

**Test cases:**
- Integration: retry path proven by a fake gRPC server that fails twice
  then succeeds.
- Integration: circuit opens after 10 consecutive failures, closes after
  a successful probe call.
- Integration: deadline propagation — a 100 ms HTTP timeout caps the
  gRPC call to ≤100 ms.
- Integration: tracing — when OTel is enabled, the gRPC call inherits the
  HTTP span as parent.
- Integration: `streaming.GetCapabilities` returns within 50 ms p95 from
  the in-Streaming cache.

**Edge cases:**
- Pipeline returns an `INTERNAL` error — surfaced to the caller as a
  500 problem+json `type: pipeline-internal`, never a 200 with empty
  result.
- Streaming returns `RESOURCE_EXHAUSTED` (transcoder slots full) — the
  API translates to 503 problem+json with `Retry-After: 5`. Not retried
  inside the client (the user must back off).
- The protobuf schema adds a new optional field — old clients ignore it
  silently; tests assert this with a forward-compat fixture.
