# Story 7.6 — Transcript window endpoint

`GET /api/videos/{id}/segments?from={sec}&to={sec}` from §9.2. Used by the
player to render the transcript sidebar in real time.

**AC-1 — Return segments overlapping a time window.**
- **Given** a video with N transcript segments,
- **When** the request specifies `?from=120&to=300`,
- **Then** the response contains every segment where `start_sec < 300 AND
  end_sec > 120` (inclusive overlap), ordered by `seq` ascending, with
  `text` already bidi-isolated for safe mixed-script rendering.

**AC-2 — Default window.**
- **Given** no `from`/`to`,
- **When** the request is sent,
- **Then** the response returns the first 200 segments of the latest
  non-superseded transcript and includes a `next` cursor.

**AC-3 — Word-level optionally included.**
- **Given** `?words=true`,
- **When** the segments are returned,
- **Then** each segment includes its `words: [{seq, start_sec, end_sec,
  text, confidence}]` array if word-level timestamps were captured,
  otherwise `words: null`.

**Test cases:**
- Unit: segment-overlap predicate matches the four cases (fully inside,
  spanning start, spanning end, fully containing).
- Integration: 50-segment fixture, `?from=10&to=12.5` returns exactly the
  one segment whose `[start_sec, end_sec)` straddles 12.0.
- Integration: superseded transcript is **not** returned by default;
  `?include_superseded=true` returns it.
- Performance: 10,000-segment transcript paginated at 200/page completes
  in under 100 ms per page on a SQLite test DB.

**Edge cases:**
- Window crosses a paused-transcribe gap (segments only exist up to
  3500 s on a 5000 s video) — `?from=4000` returns an empty `items` and
  `partial: true` in the response root, instructing the UI to render
  "transcribing…" overlay.
- `from > to` — returns 400 `type: invalid-time-window`.
- `from < 0` is clamped to 0; `to > duration_sec` is clamped to
  `duration_sec` (no error). Test case: `?from=-5&to=99999` against a
  600 s video → returns segments in `[0, 600]`. `?from=NaN` is rejected
  with 400 `type: invalid-query-parameter` (the validator parses each
  numeric query param strictly before clamping).
- Right-to-left text rendering: `text` field MUST be wrapped in U+2068
  FIRST STRONG ISOLATE … U+2069 POP DIRECTIONAL ISOLATE so that an
  English query result interleaved into an Arabic transcript does not
  reorder the surrounding paragraph in the UI.
