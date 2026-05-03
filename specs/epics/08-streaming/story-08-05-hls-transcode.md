# Story 8.5 — HLS adaptive transcode pipeline

The fallback for everything direct/remux can't handle. One FFmpeg
subprocess per session writes a ladder of H.264+AAC renditions; segments
are served out of `cache/hls/{session_id}/` with a rolling window
(`hls_list_size=6` by default). FFmpeg flags per §4.4.

**AC-1 — Manifest assembly.**
- **Given** an open session with default ladder `[1080p, 720p, 480p]`,
- **When** `GET /stream/{session_id}/manifest.m3u8` is fetched,
- **Then** the response is the master playlist exactly matching §4.3
  shape (variant streams, audio group, subtitle group), with
  `Cache-Control: no-store` (manifests are dynamic).

**AC-2 — Variant playlists update live.**
- **Given** the FFmpeg subprocess writing segments,
- **When** `GET /stream/{session_id}/{rendition}/index.m3u8` is fetched
  every ~2 s by the player,
- **Then** the playlist contains the latest 6 segments
  (`#EXT-X-MEDIA-SEQUENCE` advances), already-deleted segments are not
  listed, and `#EXT-X-ENDLIST` is present once FFmpeg exits cleanly.

**AC-3 — Segment serving.**
- **Given** an authenticated segment request,
- **When** the segment file exists on disk,
- **Then** it's served with `Content-Type: video/MP2T`, `Cache-Control:
  public, max-age=31536000, immutable` (segments are content-addressed by
  session id + sequence), and the JWT signature checked.
- **When** the segment file does not yet exist (player is asking too far
  ahead),
- **Then** the request waits up to `segment_wait_ms` (default 5000) for
  the file to appear, polling at 100 ms; if it never appears returns
  `404`.

**AC-4 — Bitrate ceiling.**
- **Given** a session opened with `max_bitrate_kbps=1500`,
- **When** the master playlist is built,
- **Then** the ladder excludes any rendition whose `BANDWIDTH > 1500000`.

**AC-5 — Seek triggers cold restart.**
- **Given** a player issues a seek beyond the rolling window,
- **When** the new range is requested,
- **Then** the session-pinned FFmpeg is killed and respawned with a new
  `-ss {start_sec}`; the master playlist's discontinuity tag is emitted;
  the player resumes within 2 s p95.

**AC-6 — Burned-in subtitles per session.**
- **Given** a session opened with `burn_subs=true` (Epic 7 Story 7.10),
- **When** FFmpeg is spawned for transcode,
- **Then** the transcoder applies `-vf "subtitles=<source>:force_style=..."`
  and the manifest does not advertise an external subtitle group. This
  costs an extra video filter pass and disables direct/remux for the
  duration of the session.

**Test cases:**
- Integration: a 30 s sample MKV transcoded → master + 3 variant
  playlists + at least 8 segments; each segment plays via ffprobe.
- Integration: HLS validator (e.g., `mediastreamvalidator` from Apple
  HLS tools) passes against the produced playlists (CI gate).
- Integration: rolling window — the 7th segment causes the 1st to be
  removed from the playlist and the file deleted from disk.
- Integration: bitrate cap of 1500 → 480p is the only rendition served.
- Integration: seek to t=600 in a 1200 s video — new FFmpeg starts at
  600, segment-zero is served within 2 s.
- Integration: a `burn_subs=true` session produces a manifest with no
  subtitle group and visible cues in the rendered video.

**Edge cases:**
- Player requests segment 0 after FFmpeg has rolled past it — return
  410 Gone (not 404); the player should reload the playlist.
- FFmpeg process crashes mid-stream — the session reaper (story 8.9)
  catches it; the player gets 502s; on player retry the session is
  marked failed, the API is notified, and the user sees a "playback
  error" toast.
- Player asks for a segment for a closed session — 404 immediately,
  don't wait `segment_wait_ms`.
- A network filesystem latency spike causes FFmpeg to fall behind the
  player's playback — the player buffers underrun; we surface this as a
  metric `hls_segment_starvation_total` and the player downshifts.
- Independent segment alignment for ABR switching: keyframe interval is
  forced to 2 s (`-g 48 -keyint_min 48` at 24 fps) regardless of source,
  so renditions can interleave at any segment boundary.
