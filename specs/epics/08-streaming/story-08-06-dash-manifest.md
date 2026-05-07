# Story 8.6 — DASH manifest (opt-in per session)

DASH is opt-in because §4.3 notes the FFmpeg single-encode → both-formats
trick isn't supported; running both formats for one session would double
CPU. Players that need DASH (some Android browsers) request it
explicitly via `format=dash` on session open.

**AC-1 — DASH-only session.**
- **Given** a session opened with `format=dash`,
- **When** `GET /stream/{session_id}/manifest.mpd` is fetched,
- **Then** an MPD with the same ladder as the HLS variant is returned,
  segments are `init.mp4` + `chunk-N.m4s`, and the player can play it.
- **When** the same session is asked for HLS, it returns 409
  `type: format-mismatch`.

**AC-2 — Validation.**
- **Given** any produced MPD,
- **When** validated against the DASH-IF MPD validator,
- **Then** it passes baseline conformance.

**AC-3 — Live-to-static profile transition.**
- **Given** a DASH session that has reached the end of the source video,
- **When** the FFmpeg subprocess exits cleanly,
- **Then** the MPD `type` flips from `dynamic` to `static`,
  `mediaPresentationDuration` is fixed, and the segment template
  remains valid for the next `dash_static_ttl_sec` (default 1800 s).
  Players that continue requesting segments after the transition see
  the static MPD on next refresh.

**Test cases:**
- Integration: shaka-player can play a fixture session.
- Integration: format=dash + format=hls cannot coexist on a session id.
- Integration: live→static MPD transition is asserted via two
  consecutive MPD fetches (one mid-stream, one post-EOF).

**Edge cases:**
- Subtitle handling is identical (VTT) but referenced differently in the
  MPD. Documented in story 8.11.
- DASH live profile vs static — sessions are live (`type="dynamic"`)
  during playback, switched to `static` on EOF as in AC-3.
