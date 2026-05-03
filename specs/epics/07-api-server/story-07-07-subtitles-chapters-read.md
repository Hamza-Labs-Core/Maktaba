# Story 7.7 — Subtitles & chapters read endpoints

`GET /api/videos/{id}/subtitles`, `GET /api/videos/{id}/chapters` from
§9.2. Read-only enumeration; the bytes themselves are served by Streaming
(Epic 8).

**AC-1 — Enumerate subtitles.**
- **Given** a video with one auto-generated VTT, one external SRT, and one
  embedded subtitle,
- **When** the endpoint is called,
- **Then** the response is an array of `{id, language, format, source,
  is_default, url}` where `url` is a signed Streaming URL (audience
  `streaming-static`, see Epic 10 Story 10.8) valid for
  `subtitle_url_ttl_sec` (default 3600 s).

**AC-2 — Chapters with provenance.**
- **Given** a video whose chapters were inferred from transcript topic
  shifts,
- **When** the endpoint is called,
- **Then** each item includes `{seq, start_sec, end_sec, title, source}`
  with `source ∈ {embedded, manual, inferred}`. Inferred chapters are
  produced by Epic 9 Story 9.18.

**Test cases:**
- Unit: signed URL TTL reflects config; a frozen-clock test asserts
  `expires_at = now + ttl`.
- Integration: a video with no subtitles returns `[]`, not 404.
- Integration: a video with no chapters returns `[]`, not 404.
- Integration: external SRT is reported with `format: "srt"` even though
  Streaming serves a converted VTT to the player.

**Edge cases:**
- Video has subtitles in three languages but the requesting client sent
  `Accept-Language: ar` — the response order puts `ar` first; no other
  filtering. Test case: header-based ordering works for `ar`, `en`, `*`.
- Subtitle file disappeared from disk between the Pipeline writing it and
  the API serving the URL — the URL still gets minted; Streaming returns
  404 to the player at fetch time. The API endpoint does not stat files
  per request (perf).
