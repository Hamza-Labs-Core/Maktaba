# Story 8.2 — Capability matrix & client profile registry

Each session is opened with a `client_profile` (browser-chrome, browser-safari,
ios-native, android-native, tvos, androidtv, generic). The matrix maps
profile → supported `(container, video_codec, audio_codec, profile_level,
hdr_format)` tuples. Used by stories 8.3/8.4/8.5 to decide direct/remux/transcode.

**AC-1 — Matrix lookup.**
- **Given** a known profile,
- **When** asked `canDirectPlay(profile, mediaInfo)`,
- **Then** returns true iff every (container, video, audio) tuple of the
  source is in the profile's allow-list at a profile/level the client can
  decode.

**AC-2 — Per-session overrides.**
- **Given** a session opened with `force_transcode=true` or
  `max_bitrate_kbps=1500`,
- **When** the matrix is consulted,
- **Then** the override beats the profile default — direct/remux is
  skipped or the bitrate ceiling is enforced in the ladder.

**AC-3 — Unknown profile fallback.**
- **Given** a profile name not in the registry,
- **When** queried,
- **Then** the `generic` profile is used (HLS H.264 + AAC, max 720p) and
  a warning is logged with the supplied profile name and request UA.

**Test cases:**
- Unit table-driven: each profile × representative MKV/MP4/WebM source →
  expected mode (direct, remux, transcode).
- Unit: HEVC source on `browser-chrome` → transcode; same source on
  `browser-safari` (post-2020) → direct.
- Unit: AC-3 audio on `ios-native` → remux to AAC needed even if video
  is fine.

**Edge cases:**
- A profile that lies (claims H.265 but actually fails to decode at
  runtime) — out of scope; the user can flip the override per-session.
- Profile registry update without restart — the registry is reloaded on
  `LISTEN profiles_changed` (matches Epic 7's settings reload pattern).
