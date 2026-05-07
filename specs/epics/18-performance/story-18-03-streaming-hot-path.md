# Story 18.3 — Streaming hot-path performance

Manifest issue, segment serve, and session open are on every video
playback. They must be cheap.

## Acceptance criteria

- AC1. `OpenSession` gRPC call (API → Streaming) returns in p95 ≤ 80 ms
  for a previously-probed video.
- AC2. HLS master manifest is generated from cached probe data with zero
  FFmpeg invocations; p95 ≤ 30 ms server-side.
- AC3. Range request hit on a cached HLS segment serves with `Content-
  Length` set, no chunked encoding, and p95 first-byte ≤ 100 ms.
- AC4. The transcode worker pool exposes a `transcode_queue_depth`
  metric; under steady-state direct-play workloads it is 0.

## Test cases

- TC1. Open 50 concurrent sessions on the same video; all complete within
  budget and the second-onwards `OpenSession` is faster than the first
  (probe cache hit).
- TC2. Issue 500 segment range requests against a fully-warm cache; all
  succeed within budget with no FFmpeg subprocess spawned.
- TC3. Force a cold transcode by `EvictHashCache` and request a segment;
  first segment p95 ≤ 6 s, subsequent segments fall to warm budget.

## Edge cases

- EC1. Client requests a byte range that crosses a segment boundary —
  must serve from two cached segments without re-transcoding.
- EC2. Concurrent identical cold-segment requests — only one FFmpeg
  invocation runs; the others wait on the in-flight result (single-flight).
- EC3. Cache LRU at exact `max_gib`: an in-progress transcode must not
  evict its own output mid-write.
