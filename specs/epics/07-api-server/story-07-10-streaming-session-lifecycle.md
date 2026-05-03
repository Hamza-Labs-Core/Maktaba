# Story 7.10 — Streaming session lifecycle

The API mints sessions and signs URLs; Streaming serves bytes (§9.4).

**AC-1 — Open session.**
- **Given** a video and a request `POST /api/stream/sessions` with
  `{video_id, client_profile, audio_track?, subtitle_track?, start_sec?,
  max_bitrate_kbps?, format?, force_software?, force_transcode?,
  burn_subs?, accept_queue?}`,
- **When** processed,
- **Then** the API:
  1. validates the user has access to the video and resolves the set of
     `library_ids` the user can read,
  2. gRPC-calls Streaming's `OpenSession` with the request (request shape
     is published in `shared/proto/streaming.proto`; see Epic 8 Story
     8.8 AC-1),
  3. mints a JWT signed URL `/stream/{session_id}/manifest.m3u8?sig=...`
     valid for `session_url_ttl_sec` (default 1800 s); the JWT carries
     `aud=streaming, sub=session_id, usr=user_id, lib=[library_id]`
     (single-element array — the session is anchored to one video and
     thus one library) per Epic 10 Story 10.8,
  4. inserts a `streaming_sessions` row,
  5. returns `{session_id, mode, manifest_url, direct_url?, expires_at,
     ladder, current_rendition}` where `mode ∈ {direct, remux, transcode}`
     and `direct_url` is populated only when `mode=direct`.

**AC-2 — Get session info.**
- **Given** an open session,
- **When** `GET /api/stream/sessions/{id}` is called,
- **Then** the response includes the bitrate ladder, current rendition,
  and `last_segment_fetched_at` for staleness diagnostics.

**AC-3 — Close session.**
- **Given** an open session,
- **When** `DELETE /api/stream/sessions/{id}` is called,
- **Then** the API gRPC-calls Streaming's `CloseSession`, the
  `streaming_sessions` row is updated `closed_at = now()`, and the
  response is `204`.

**AC-4 — Server capabilities.**
- **Given** any client,
- **When** `GET /api/stream/capabilities` is called,
- **Then** the response is `{codecs: [...], hwaccel: "videotoolbox"|...|
  "none", max_bitrate_kbps, supported_containers: [...]}`, fetched live
  from Streaming over gRPC `Streaming.GetCapabilities` (see Epic 8
  Story 8.8 AC-4 and Story 7.18 AC-2) and cached for 60 s in the API
  process.

**AC-5 — Single client flow.**
- **Given** any client (web, native, TV),
- **When** opening a stream,
- **Then** the client always calls `POST /api/stream/sessions` first;
  the response carries `mode` and the relevant URL (`manifest_url` for
  remux/transcode, `direct_url` for direct play). The client never
  calls Streaming's `/stream/direct/{video_id}` endpoint without a
  `direct_url` returned from this endpoint.

**Test cases:**
- Unit: signed URL contains `aud=streaming`, `sub=session_id`,
  `usr=user_id`, `lib=[library_id]`, `exp = iat + ttl`, and `iss=api`.
- Integration: open + close round-trip writes both rows and frees the
  Streaming transcoder slot.
- Integration: open session with `start_sec=600` propagates to FFmpeg
  `-ss 600`.
- Integration: open session for a video the user can't access returns
  403 `type: access-denied` and Streaming is never called.
- Integration: capabilities endpoint hits the gRPC backend at most once
  per 60 s under load (cache TTL respected).
- Integration: opening a direct-playable video returns
  `mode: "direct"` and a populated `direct_url`.

**Edge cases:**
- Streaming gRPC is down — `POST /api/stream/sessions` returns 503
  `type: streaming-unavailable` with `Retry-After: 5`. Test case: kill
  Streaming, hit the endpoint → 503.
- `start_sec` greater than `duration_sec` is clamped to `duration_sec - 5
  s` and a `Maktaba-Warning: start-sec-clamped` header is added.
- `client_profile` unknown — falls back to a generic profile that asks
  Streaming for HLS H.264 720p; logged at warn with the client UA.
- Two concurrent `POST /sessions` for the same `(user, video)` — both
  succeed (the user may legitimately watch the same video on two devices);
  rate-limited per-user via story 7.19.
- `manifest_url` expired before the player fetched it — Streaming returns
  401 with `type: signed-url-expired`; the client must call `POST
  /sessions` again. Document in the API reference.
- The user passes `Idempotency-Key` (Story 7.1 AC-4) when opening a
  session — the same response is replayed on retry, preventing duplicate
  session minting on a network failure between API and client.
