# Story 8.8 — gRPC server (Open/Close/EvictHashCache/GetCapabilities)

The Streaming binary exposes the gRPC schema from §9.9. The API is the
sole gRPC client. All endpoints are non-streaming except future
HealthCheck-watch (out of scope here).

**AC-1 — OpenSession (request/response published in
`shared/proto/streaming.proto`).**
- **Given** a request with the explicit shape:
  ```
  message OpenSessionRequest {
    string video_id          = 1;
    string client_profile    = 2;
    optional int32 audio_track       = 3;
    optional int32 subtitle_track    = 4;
    optional int32 start_sec         = 5;
    optional int32 max_bitrate_kbps  = 6;
    optional string format           = 7;   // 'hls' | 'dash'
    bool force_software              = 8;
    bool force_transcode             = 9;
    bool burn_subs                   = 10;
    bool accept_queue                = 11;
  }
  message OpenSessionResponse {
    string session_id                   = 1;
    string mode                         = 2;   // 'direct' | 'remux' | 'transcode' | 'direct-degraded'
    repeated Rendition ladder           = 3;
    string manifest_path                = 4;   // relative; API signs the URL
    google.protobuf.Timestamp expires_at = 5;
    optional QueueState queue           = 6;   // populated when state=='queued'
  }
  ```
- **When** processed,
- **Then** the Streaming server:
  1. fetches the probe metadata (story 8.15) for `video_id`,
  2. consults the matrix (story 8.2) and chooses direct/remux/transcode,
  3. allocates a session id, inserts a `streaming_sessions` row in
     Streaming's own DB transaction (the API records the returned
     session id),
  4. for transcode/remux modes, spawns the FFmpeg subprocess and waits
     for the master playlist file to appear (capped at 5 s),
  5. returns `OpenSessionResponse` per the schema above.

**AC-2 — CloseSession.**
- **Given** a session id,
- **When** `CloseSession` is called,
- **Then** the FFmpeg subprocess is killed (`SIGTERM` then `SIGKILL`
  after 2 s grace), the per-session HLS dir is purged, the
  `streaming_sessions` row is updated `closed_at = now(), reason='api'`,
  and the response is empty.

**AC-3 — EvictHashCache.**
- **Given** a `content_hash`,
- **When** `EvictHashCache` is called,
- **Then** every cache entry keyed by that hash (remux, posters,
  sprites, thumbs, **and the in-memory probe cache**) is invalidated;
  in-flight sessions reading those files are unaffected (kernel keeps
  the inode alive). Used by Pipeline after reprocess.

**AC-4 — GetCapabilities (separate RPC).**
- **Given** any caller,
- **When** `GetCapabilities()` is called,
- **Then** the response is
  ```
  message Capabilities {
    repeated string codecs              = 1;
    string hwaccel                      = 2;
    int32 max_bitrate_kbps              = 3;
    repeated string supported_containers = 4;
    Slots transcode_slots               = 5;
    int64 cache_used_gib                 = 6;
    int64 cache_cap_gib                  = 7;
  }
  ```
  This is read from an in-Streaming cache (refreshed on
  `LISTEN profiles_changed` or boot) and does **not** issue any
  child process calls. p95 latency ≤ 50 ms.

**AC-5 — HealthCheck (liveness only).**
- **Given** a HealthCheck request,
- **When** processed,
- **Then** returns `{status, last_error?, last_error_at?}` without the
  capability fields. Capability data is exposed exclusively via
  `GetCapabilities`.

**Test cases:**
- Integration: Open → manifest path exists within 5 s for a transcode
  session against a fixture.
- Integration: Close kills FFmpeg within 2 s grace.
- Integration: EvictHashCache deletes only the specified hash's entries
  (cross-hash isolation), and the next OpenSession for that hash
  performs a fresh probe.
- Integration: Open with an unknown video id → `NOT_FOUND` gRPC error.
- Integration: GetCapabilities returns within 50 ms p95 from the cache.

**Edge cases:**
- Open while transcode slots are full — return `RESOURCE_EXHAUSTED`; API
  surfaces 503 to the client (Epic 7 Story 7.10) unless the request
  carries `accept_queue=true`, in which case the response carries
  `state='queued'` and a populated `queue` field (see Story 8.10).
- Close on an already-closed session — idempotent, returns OK.
- Close on a session whose FFmpeg has already crashed — also idempotent.
- EvictHashCache while a session is reading the file — the OS keeps the
  inode; the file is unlinked from the directory but the read continues.
  The next session for the same hash will see no cache and regenerate.
