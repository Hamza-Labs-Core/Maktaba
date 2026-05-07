# Story 11.3 — Video player (HLS.js / Vidstack, subtitle overlay, chapter nav, speed control)

The player is Vidstack with HLS.js fallback for environments where native
HLS isn't available. It plays the manifest URL minted by `POST
/api/stream/sessions`, renders auto and sidecar subtitles, exposes chapter
markers on the scrubber, and supports playback rates 0.5×–2× in 0.25
increments.

**Anchors:** [`architecture.md` §6.2](../../architecture.md), §4.1
(streaming modes), §4.5 (subtitles), §4.6 (chapters). Depends on Epic 7
Stories 7.10 (sessions), 7.11 (watch progress); Epic 8 Stories 8.5 (HLS),
8.11 (subtitles), 8.12 (chapters).

## Session handshake (resolves [REVIEW §4.1](../../REVIEW.md))

The web player **always** calls `POST /api/stream/sessions` before play.
The response carries `{session_id, mode ∈ {direct, remux, transcode},
manifest_url, direct_url?, expires_at, ladder, current_rendition}`:

- If `mode = direct` and `direct_url` is present, the player loads
  `direct_url` (signed with audience `streaming-direct`).
- Otherwise the player loads `manifest_url` (signed with audience
  `streaming`).

The web client does **not** call `GET /stream/direct/{video_id}` directly;
direct-JWT minting is exposed only to native players (Story 12.3) via
[Epic 10 Story 10.8 AC-2](../../epics/02-api-streaming.md). This removes
the ambiguity flagged in REVIEW §4.1.

## AC

- Player loads within 2 s of the user clicking Play (cold start) on a
  100 Mbps LAN.
- HLS adaptive bitrate switches happen invisibly; overlay shows current
  rendition only when the user enables it via Settings → Show stats.
- Subtitle overlay supports Arabic correctly: RTL text rendering, Arabic
  numerals, no ligature breakage. Style controls (size, opacity, color,
  background) apply live.
- Chapter markers are rendered as ticks on the scrubber; hover shows the
  chapter title; click jumps to `chapter.start_sec`. Source: `chapters.json`
  ([architecture.md §4.6](../../architecture.md)) — chapters are inferred
  by the pipeline (REVIEW §2.7.a is resolved by adding a
  `chapter_infer` stage to Epic 1).
- Speed control: 0.5×, 0.75×, 1×, 1.25×, 1.5×, 1.75×, 2×. Audio pitch
  preserved (player default).
- Keyboard: `Space` toggle play, `←/→` ±10 s, `Shift+←/→` ±30 s, `J/L`
  ±10 s, `K` toggle play, `M` mute, `,` / `.` previous/next chapter, `0–9`
  jump to N×10%, `F` fullscreen, `C` toggle subtitles, `+/-` speed.
- Picture-in-picture: dedicated button + browser API; survives navigation
  away from `/watch/{id}`.
- Watch progress: posted every 10 s and on pause/seek to
  `POST /api/stream/sessions/{id}/progress`. Server uses last-writer-wins
  with **no monotonicity check** (resolves [REVIEW §1.5.a](../../REVIEW.md)
  in favor of [Epic 7 Story 7.11](../../epics/02-api-streaming.md)). The
  player records both forward seeks and backward scrubs the same way; the
  invented `seek=true` flag from Epic 24 Story 24.4 AC-2 is removed.
- Resume offer appears next time the user opens the same video on any
  device.

## TC

- Start a 1-hour video at 0; seek to 35:00. Stream catches up within 3 s.
- Play a 4K HEVC source on Safari (no native HEVC): server returns
  `mode=transcode` + HLS manifest; player consumes it without intervention.
- Play an H.264 + AAC MP4 in Chrome: server returns `mode=direct` +
  `direct_url`; player uses it.
- Toggle subtitles off and on while playing: no playback hiccup.
- Open the same video on a second tab: WS broadcasts position, the new tab
  shows "Resume at 35:14".
- Network drops for 8 s mid-segment: HLS.js back-off retries; UI shows a
  spinner overlay; recovery is automatic.
- User scrubs from 30:00 backward to 5:00 then forward to 40:00: each
  position is accepted by the server (no rejection on rewind).

## EC

- Manifest expires mid-watch (`expires_at` < now): client refetches
  `POST /api/stream/sessions` transparently; the gap is < 1 s.
- User changes audio track: the existing session is closed
  (`DELETE /api/stream/sessions/{id}`) and a new one opened; resume
  position is preserved.
- Browser blocks autoplay with sound: player starts muted with a
  "Click to unmute" affordance.
- HLS.js fails to bootstrap (e.g., quota for media source): re-call
  `POST /api/stream/sessions` with `force_transcode=false` to ask for
  direct mode if compatible; otherwise show a recoverable error with a
  "Retry" button.
- Source duration in metadata disagrees with the manifest: trust the
  manifest's `EXT-X-ENDLIST` for end-of-content detection.
