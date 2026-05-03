# Story 8.12 — Chapter delivery

§4.6 chapter sources unified into one `GET /stream/{session_id}/chapters.json`
plus `#EXT-X-DATERANGE` markers in the master playlist for HLS-aware
players. Generation is owned by Epic 9 Story 9.18.

**AC-1 — JSON endpoint.**
- **Given** a session,
- **When** `chapters.json` is fetched,
- **Then** the response is `[{seq, start_sec, end_sec, title, source}]`
  sorted by `start_sec`. Sources merge in priority `embedded > manual >
  inferred`. The endpoint is reachable directly (without a session) at
  `GET /stream/posters/{video_id}/chapters.json` for use by Epic 7
  Story 7.7 — uses `aud=streaming-static`.

**AC-2 — DATERANGE in HLS.**
- **Given** the master playlist is built,
- **When** chapters exist,
- **Then** one `#EXT-X-DATERANGE:CLASS="chapter",ID="<seq>",START-DATE=...,
  DURATION=...,X-TITLE="..."` tag is emitted per chapter, anchored to the
  session's `started_at` so DATERANGE math works.

**Test cases:**
- Unit: priority merge — three chapters from each source with
  overlapping ranges → embedded wins on overlap.
- Integration: AVPlayer's `AVPlayerItemChapterMetadata` populates from
  the playlist DATERANGE.
- Integration: a session with no chapters returns `[]` and the playlist
  has no DATERANGE entries.

**Edge cases:**
- Two chapters with identical `start_sec` — secondary sort by `seq`.
- Chapter `end_sec` > `duration_sec` — clamped to `duration_sec`.
- An "inferred" chapter at a place where the player has no segment yet
  (live HLS window) — it's still in the JSON; the DATERANGE in the live
  playlist is added when the segment containing it is in the rolling
  window.
