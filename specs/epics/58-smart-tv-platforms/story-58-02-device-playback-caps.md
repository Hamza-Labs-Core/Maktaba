# Story 58.02 — Playback that fits each TV: device-reported capabilities, new profiles and HLS in fMP4

> Epic 58 · Phase 1 · Depends on: — · Needed by: 58.05, 58.07, 58.08, 58.10, 58.11, 58.13, 58.17, 58.18

## Why

The streaming service decides direct play, remux or transcode from a named client profile
(`streaming/internal/capability/matrix.go`: `generic`, `desktop-player`, `browser-chrome`,
`browser-safari`, `ios-native`, `tvos`, `android-native`, `androidtv`). An unknown profile falls
back to `generic`: H.264 and AAC in HLS, at most 720p. Every new TV would get 720p transcodes.

One profile per platform is not enough either. What a TV decodes depends on its model and year,
not its platform (research.md §2):
- A Fire TV Stick 4K Max decodes AV1 and Dolby Vision; an older Fire TV Stick does neither.
- A Chromecast 3rd gen plays only H.264 at 1080p; a Google TV Streamer adds AV1.
- Samsung TVs have no Dolby Vision at all. Their MSE path stops at FHD.
- Roku plays Dolby Vision only as profile 5, plays AV1 only inside DASH, and has no TrueHD.

Two stream formats are also missing:
- **HLS in fMP4.** HLS is MPEG-TS only today (`ffmpeg.go` writes `seg-%d.ts`). Cast receivers do
  not take HEVC in TS, and webOS, Tizen's AVPlay and Roku all play HLS with fMP4 (CMAF)
  segments.
- **Fine-grained Dolby Vision.** Profile 8 files carry an HDR10 base layer that a TV without
  Dolby Vision can play. A profile 5 file has no such base layer.

## Description

**1. The device says what it can play.** `POST /api/stream/sessions` gains an optional
`device_caps`:

```json
{
  "containers": ["mp4", "mkv", "ts"],
  "video": [{"codec": "hevc", "max_height": 2160, "max_fps": 60, "bit_depth": 10},
            {"codec": "av1", "max_height": 2160}],
  "hdr": ["hdr10", "hlg", "dv-p5", "dv-p8"],
  "audio": ["aac", "ac3", "eac3"],
  "passthrough": ["eac3", "truehd"],
  "max_audio_channels": 8,
  "hls_segments": ["ts", "fmp4"],
  "dash": true,
  "max_bitrate_kbps": 80000
}
```

Each app fills this from the platform's own query, at start and when the output changes (a TV
switching HDMI input, a Fire TV moved to another screen):
- Fire TV and Android TV: `MediaCodecList` and `Display.getHdrCapabilities`.
- Tizen: `webapis.avplay` and `webapis.productinfo` (`isUdPanelSupported`, `is8KPanelSupported`).
- webOS: `MediaSource.isTypeSupported` with `webOS`'s `deviceInfo`.
- Roku: `roDeviceInfo.CanDecodeVideo`, `CanDecodeAudio`, `GetDisplayProperties` and
  `GetAudioOutputChannel`.
- The Cast receiver: `cast.framework.CastReceiverContext.canDisplayType`.
- Vega: its W3C media capability calls.

The streaming service starts from the named profile and applies the report:
- The report can **narrow** the profile freely.
- The report can **widen** it only within the platform's ceiling, the new profiles' `Max*`
  values. A buggy or modified app can therefore ask for a 4K HEVC direct play, but never for
  something outside the platform's limits.
- If the device then cannot play what it got, the app retries once with `force_transcode`
  (the field exists).

**2. New profiles.** The new profiles are `firetv`, `tizen`, `webos`, `roku`, `cast` and `vega`.
Each is a conservative baseline from research.md §2, so a session without `device_caps` still
plays on every model of that platform:

| Profile | Baseline (no report) | Ceiling (with a report) |
|---|---|---|
| `firetv` | H.264 1080p, AAC/AC3/EAC3, HLS TS | HEVC/AV1 2160p, HDR10/HLG/DV, EAC3/TrueHD passthrough, MKV direct |
| `tizen` | H.264 1080p, AAC/AC3/EAC3, HLS TS | HEVC/AV1 2160p (8K never asked), HDR10/HLG, **no DV**, MKV direct through AVPlay |
| `webos` | H.264 1080p, AAC/AC3/EAC3, HLS TS (native HLS v7) | HEVC/AV1 2160p, HDR10/HLG, DV-p8 through its HDR10 base only, HLS fMP4, MKV direct |
| `roku` | H.264 1080p, AAC, HLS TS | HEVC 2160p, HDR10/HLG, DV-p5, AV1 only by DASH, EAC3/AC3 passthrough, MKV direct |
| `cast` | H.264 1080p, AAC, HLS TS | HEVC/AV1 2160p in HLS fMP4 (never TS), HDR10/DV where `canDisplayType` says so; no MKV |
| `vega` | H.264 1080p, AAC, HLS TS | HEVC/AV1 2160p, EAC3/AC4, MKV direct |

**3. HLS with fMP4 segments.** The streaming service writes HLS with fMP4 segments
(`-hls_segment_type fmp4`, `init.mp4` + `seg-%d.m4s`, `#EXT-X-MAP`) when the session's codec is
HEVC or AV1 or the device lists only `fmp4`. Otherwise it stays with TS, so existing clients see
no change. The subtitle renditions of story 48.4 (WebVTT `EXT-X-MEDIA`) are kept in both
formats.

**4. Dolby Vision and HDR.**
- A DV profile 8 source goes to a TV without `dv-p8` as its HDR10 (8.1) or HLG (8.4) base
  layer. That is a remux with the RPU dropped, not a transcode.
- Profile 5 goes to a TV without `dv-p5` as a tone-mapped HDR10 transcode, or SDR when the TV
  has no HDR.
- Profile 7 is remuxed to its base layer.

**5. Audio.**
- TrueHD and DTS go to a TV without them as EAC3 5.1 (a transcode of the audio only, with the
  video direct or remuxed).
- A stereo AAC track is added beside any Dolby track for Roku, as Roku's certification asks.

## Acceptance criteria

- **Given** a Fire TV whose report lists HEVC 2160p and HDR10, **when** it opens a 4K HEVC HDR10
  MKV, **then** the session is `direct` and no ffmpeg runs.
- **Given** a Cast receiver listing `fmp4` only, **when** an HEVC source must be remuxed, **then**
  the manifest's segments are `.m4s` with an `EXT-X-MAP`.
- **Given** a Samsung TV (no `dv-*` in its report) and a DV profile 8.1 source, **then** the
  session is `remux` to HDR10, not `transcode`.
- **Given** a Roku report claiming AV1 inside HLS, **then** the session falls back within the
  `roku` ceiling: AV1 goes by DASH or is transcoded to HEVC.
- **Given** no `device_caps`, **then** each new profile plays its baseline on every model.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit (`capability`) | each new profile × the source matrix | `Decide` | the verdict table in `matrix_test.go` |
| T02 | Go unit | a report widening past the ceiling | `Decide` | clamped to the ceiling, `Reason` says so |
| T03 | Go unit (`ffmpeg`, fake runner) | HEVC remux for `cast` | args | `-hls_segment_type fmp4`, `.m4s` names |
| T04 | real-ffmpeg test (`realffmpeg_test.go`) | 5 s HEVC Main10 clip | HLS fMP4 | playable with `ffprobe`; `EXT-X-MAP` present; subtitle rendition listed |
| T05 | real-ffmpeg | DV 8.1 sample | remux for `tizen` | HDR10 output, no RPU NAL units |
| T06 | Go unit (API handler) | `device_caps` with unknown keys | open session | unknown keys ignored, known kept |
| T07 | Shared vectors `shared/playback/caps-vectors.json` | each app's capability reader with recorded platform answers | build the report | the expected JSON, in Kotlin, TypeScript, BrightScript and the receiver |

## Strings

None: no user-facing text. Player error text is story 58.15's.

## Edge cases

- **A report that changes mid-session** (the TV's HDMI switched to a non-HDR receiver): the app
  opens a new session at the same position. Sessions never change their mode in place.
- **Transcode capacity** (the home's encoder is busy): unchanged, the session queues
  (`accept_queue`). The TV shows the queue state it already reads.
- **The relay**: bitrate caps from the relay plan (Epic 50 story 50.19) still apply over the
  device's `max_bitrate_kbps`.

## Build plan

| File | Change |
|---|---|
| `streaming/internal/capability/matrix.go` | new profiles, `Ceiling`, merge with a report |
| `streaming/internal/ffmpeg/ffmpeg.go`, `transcode.go` | HLS fMP4 variant, DV base-layer remux, audio-only transcode |
| `api/internal/handlers/streaming/streaming.go` | `device_caps` on `OpenSessionRequest`, passed over gRPC |
| `streaming/proto/…` | `DeviceCaps` message |
| `shared/api/openapi.yaml` | the field |
| `shared/playback/caps-vectors.json` | vectors |
