# Story 8.11 — Live subtitle rendering

Three sources, all served as VTT to the player (§4.5). Auto-generated
subs are rendered live from `transcript_segments` so they appear before
transcription is complete.

**AC-1 — Auto-generated VTT, live from DB.**
- **Given** a session whose video has `transcript_segments` rows,
- **When** `GET /stream/{session_id}/subs/auto.vtt` is fetched,
- **Then** the response is a valid WebVTT file generated at request time
  by streaming over `transcript_segments` (paginated to avoid memory
  spikes) and applying the §3.5 formatting rules. Cue text is
  HTML-escaped (`<`, `>`, `&`) to prevent injection through external
  SRT or LLM output containing markup. Cache-Control:
  `no-cache, must-revalidate` (the transcript can grow under the
  player's feet).

**AC-2 — Sidecar SRT/VTT served as VTT.**
- **Given** a sidecar `.srt` next to the video,
- **When** `GET /stream/{session_id}/subs/{lang}.vtt` is fetched,
- **Then** the SRT is converted to VTT on first request (with the same
  HTML-escaping as AC-1), cached at `cache/subs/{hash}/{lang}.vtt`,
  and served.

**AC-3 — Embedded subtitle extraction.**
- **Given** a video with embedded `S_TEXT/UTF8`,
- **When** the matching subtitle URL is fetched for the first time,
- **Then** the Streaming Service calls Pipeline's
  `ExtractEmbeddedSubtitle(video_id, stream_index)` gRPC RPC which
  validates that `stream_index` is a real subtitle stream of a supported
  codec, runs `ffmpeg -map 0:s:N -c:s webvtt` server-side, and returns
  the cache path. Subsequent requests hit the local cache. The Streaming
  Service does not invoke ffmpeg on its own for embedded extraction.

**AC-4 — Single-format HLS subtitle wrapper.**
- **Given** any subtitle source,
- **When** `GET /stream/{session_id}/subs/{lang}.m3u8` is fetched,
- **Then** the response is a single-segment HLS subtitle playlist that
  references the **monolithic** VTT file (`subs/{lang}.vtt`), with
  `EXT-X-TARGETDURATION` covering the full video duration. The
  segmented VTT path mentioned in arch §4.4 is dropped in favor of
  monolithic VTT because every supported player handles single-VTT HLS
  subtitles correctly.

**AC-5 — Bidi safety in cues.**
- **Given** mixed-script transcript text,
- **When** rendered to VTT cues,
- **Then** each cue's text is bidi-isolated as in Epic 7 Story 7.6 and
  lines wrap at the source language's natural break points (Arabic
  punctuation preferred for Arabic source; etc.).

**AC-6 — Burned-in subtitles are session 8.5's responsibility.**
- **Given** a session opened with `burn_subs=true`,
- **When** the player requests `/subs/*.vtt`,
- **Then** this story's endpoint returns 204 (no external subtitles
  served — the cues are baked into the video). The visual effect is
  produced by Epic 8 Story 8.5 AC-6.

**Test cases:**
- Integration: auto-VTT for an in-flight transcript that grows mid-fetch
  → the response contains the segments present at the moment of
  generation; a refetch after 10 s contains more.
- Integration: SRT→VTT round trip preserves timestamps to ms precision
  and HTML-escapes a `<script>` cue.
- Integration: embedded MKV subtitle extraction is single-flight under
  concurrent fetches and dispatched via Pipeline's
  `ExtractEmbeddedSubtitle` RPC (verified by counting Pipeline calls).
- Integration: validates against W3C WebVTT validator on a fixture set.

**Edge cases:**
- Transcript empty (transcribe job hasn't started) — return a valid
  empty WebVTT (`WEBVTT\n\n`) so the player initializes the track.
- Subtitle longer than the video (wrong sidecar) — clip cues to
  `duration_sec`; log a warning.
- Embedded subtitle in an obscure format (e.g. PGS image-based) —
  Pipeline's `ExtractEmbeddedSubtitle` returns `INVALID_ARGUMENT` with
  `code='unsupported-codec'`; the URL responds 415 `type:
  subtitle-format-unsupported` and the API filters such tracks out of
  `GET /api/videos/{id}/subtitles`.
