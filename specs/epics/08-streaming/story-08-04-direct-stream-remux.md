# Story 8.4 — Direct stream (remux only)

Same codecs, wrong container. FFmpeg `-c copy` rewraps; near-zero CPU,
zero quality loss. Result is cached LRU at `cache/remux/{hash}/`.

**AC-1 — Cache-then-stream.**
- **Given** a video whose matrix verdict is "remux to MP4 fragmented",
- **When** the manifest is fetched for the first time,
- **Then** FFmpeg writes `cache/remux/{hash[:2]}/{hash}.mp4` via a
  temp file + atomic rename, and the manifest serves it via the direct
  play range-server (story 8.3) once written. Response while writing is
  HTTP 503 with `Retry-After: 2`.

**AC-2 — Cache hit serves immediately.**
- **Given** the remuxed file already exists,
- **When** requested,
- **Then** no FFmpeg is spawned and the file is range-served directly.

**AC-3 — Streaming write (preferred).**
- **Given** the remux is small enough to start serving partial bytes
  before completion,
- **When** the request is the very first one for this video,
- **Then** the response begins streaming as FFmpeg writes (chunked
  transfer-encoding, `Content-Length` omitted), and subsequent requests
  hit the cached file. Implementation may opt to skip this and always do
  AC-1's cache-then-stream behavior; the AC is "either is acceptable as
  long as the user-perceived TTFB is < 500 ms on local disk."

**Test cases:**
- Integration: MKV (H.264 + AAC) for `ios-native` → `cache/remux/.../*.mp4`
  exists after first request and serves with no FFmpeg on second.
- Integration: corrupt cache file detected by ffprobe → invalidated and
  regenerated.
- Integration: simultaneous first-request from two clients → only one
  FFmpeg subprocess (single-flight by `content_hash`).
- Integration: `LRU` eviction reclaims the file when the remux cache
  exceeds its share of the cap.

**Edge cases:**
- Source file changes (mtime) while remux exists — the cache key
  includes the file's `content_hash`, so any bit-level change yields a
  new cache entry. Stale entries are garbage collected by story 8.14.
- Remux fails partway (corrupt source) — temp file is removed; error is
  surfaced to the client as `502 Bad Gateway` `type: remux-failed`; the
  matrix verdict for this video is downgraded to transcode for the
  remainder of the session.
