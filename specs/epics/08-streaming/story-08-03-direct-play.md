# Story 8.3 — Direct play (range-served `206 Partial Content`)

The fast path: zero transcoding, zero remuxing. `GET /stream/direct/{video_id}`
serves the underlying file with full HTTP range support so any player
(browser, AVPlayer, ExoPlayer, VLC) can seek without server CPU.

**AC-1 — Conditional GET + range serving.**
- **Given** an authenticated request for a video that the matrix marks
  direct-playable for the requesting profile,
- **When** the request specifies `Range: bytes=N-M`,
- **Then** the response is `206 Partial Content` with `Content-Range:
  bytes N-M/total`, `Accept-Ranges: bytes`, `Content-Length: M-N+1`,
  correct `Content-Type` from probe metadata, and the bytes match the
  file slice.

**AC-2 — HEAD support.**
- **Given** a HEAD request,
- **When** processed,
- **Then** the response is `200 OK` with the same headers minus body,
  including correct `Content-Length`. Required by Safari before it
  attempts ranged GET.

**AC-3 — Multi-range refusal.**
- **Given** `Range: bytes=0-100,200-300` (multipart range),
- **When** received,
- **Then** the response is `416 Range Not Satisfiable` (we don't ship
  multipart/byteranges). Players degrade to single-range automatically.

**AC-4 — Falls through to remux/transcode if not direct-playable.**
- **Given** a video that the matrix marks not-direct-playable,
- **When** `GET /stream/direct/{video_id}` is called,
- **Then** the response is `409 Conflict` `type: direct-play-unsupported`
  with a `manifest_url` in `detail` pointing the client at the session
  manifest. Native clients should never reach this path; clients always
  call `POST /api/stream/sessions` first per Epic 7 Story 7.10 AC-5
  and follow the returned `mode`.

**Test cases:**
- Unit: range parsing — `bytes=-100` (suffix), `bytes=100-` (open-end),
  `bytes=100-200` (closed) all produce correct `Content-Range`.
- Integration: Safari range probe (HEAD then GET `bytes=0-1`) → both
  succeed.
- Integration: streaming a 4 GB MP4 in parallel from two clients →
  bandwidth scales linearly; CPU stays under 5% of one core.
- Integration: a request for a `.mkv` from `browser-chrome` returns 409
  with the manifest URL.
- Performance: p99 latency for first byte under 50 ms on local SSD.

**Edge cases:**
- File modified during read (mtime changed) — the response includes
  `Last-Modified` and `ETag` (BLAKE3 prefix); a stale request with `If-
  Range` against a changed ETag is served with `200 OK` full body so the
  player resyncs cleanly.
- File on a network filesystem disappears mid-stream — `io.Copy` errors;
  the connection is closed, the error is logged, no panic. Test case:
  unmount during stream → graceful close.
- Range past EOF — `416 Range Not Satisfiable` with `Content-Range:
  bytes */total`.
- A client sending `Range: bytes=0-9999999999` for a 1 GB file — the
  range is clamped to file size in the response, not rejected.
