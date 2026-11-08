# Story 58.13 — Roku: the player, certification and release

> Epic 58 · Phase 4 · Depends on: 58.02, 58.04, 58.12 · Needed by: 58.14, 58.16

## Why

Roku's Video node plays a lot of formats natively (research.md §6.2):
- HLS (TS or CMAF), DASH, and MP4 or MKV files directly.
- HEVC to 4K, HDR10, HLG, and Dolby Vision as profile 5.
- AV1 only inside DASH. No TrueHD.

A channel also has to pass Roku's certification before the public can install it:
- performance limits;
- deep linking;
- Back behaviour;
- voice "direct to play".

## Description

**The player screen** is a SceneGraph `Video` node with Maktaba's own overlay (`Group`): title,
series and episode, position, chapter, the Up next card, audio and subtitle pickers. The overlay
mirrors in Arabic (58.15). The node's built-in trick-play bar is replaced by the overlay; seeking
uses the home's trickplay images as thumbnails.

**Opening a stream.**
- `POST /api/stream/sessions` with `client_profile: roku` and `device_caps` (58.02), built from:
  - `roDeviceInfo.CanDecodeVideo` for HEVC Main10 at 2160p, AV1 and VP9;
  - `GetDisplayProperties` for HDR10, HLG and Dolby Vision;
  - `CanDecodeAudio` and `GetAudioOutputChannel` for EAC3, AC3 and DTS passthrough.
- The content node's URL is the session's `manifest_url` (HLS, or DASH when the home picks DASH
  for AV1) or `direct_url`, signed, so the node's HTTP agent needs only the certificate bundle.
  No token is sent with media.
- `StreamFormat` is set from the session (`hls`, `dash`, `mp4`, `mkv`).

**What the home does for Roku** (58.02's `roku` profile):
- Dolby Vision only as profile 5. Profile 8 goes as its HDR10 base layer.
- AV1 goes as DASH (fMP4), the only way Roku plays AV1. Otherwise it is transcoded to HEVC.
- TrueHD is transcoded to EAC3.
- A stereo AAC track is always added beside a Dolby track, as Roku's media guidance asks.

**Subtitles.** The manifest's WebVTT renditions, or sidecar WebVTT for direct play
(`SubtitleTracks`).
- Roku draws them itself, and for Arabic that means wrong order (no bidi). So when an Arabic
  subtitle is chosen, the channel turns off Roku's renderer and draws the cues in its own `Label`
  layer, through 58.15's visual-text function.
- English subtitles use Roku's renderer, which respects Roku's system caption settings.

**Progress and queue.**
- Progress is posted every 10 seconds, and on pause, stop and Back.
- Play all and Shuffle walk the server queue (Epic 56).
- Up next shows 15 seconds before the end.

**Errors.** A Video node `error` state retries once with `force_transcode`. A second failure shows
the error with the home's reason and a Back button.

**Certification checklist** (Roku's criteria as of 2026-04, research.md §6.5). Each item is a
test or a device-checklist line:

| Criterion | How Maktaba meets it | Check |
|---|---|---|
| Home screen in ≤ 15 s | lazy rows, small posters | T05, device timing |
| Screens in ≤ 3 s; playback in ≤ 8 s | session opened as the Video screen draws; direct play first | device timing |
| Deep linking for each media type | 58.12's router; sample IDs from the review home in the dashboard | T03 |
| Voice: direct to play | the deep-link path handles `inputEvent` with a content ID | T03 |
| Back exits from Home, with one confirm at most | 58.12 | T05 |
| No ads | not applicable: the channel has no ads, so RAF and app-ads.txt do not apply | — |
| Instant Resume | required only above 5M US streaming hours a month; not built | — |
| Accessibility | captions; audio guide reads labels (`a11y` fields on every focusable node) | device checklist |

**Store and release.**
- The Roku Developer Dashboard account is free.
- The channel is a public channel with no ads and no purchases.
- A `v*` tag whose channel changed builds `maktaba-roku-<version>.zip` (`roku-release.yml`) and
  the package is signed on a Roku device with the project's signing key (`genkey`, kept in the
  release secrets; Roku packages on a device). A person uploads it to the dashboard with the
  notes from `docs/stores/roku.md` and the review home's credentials (58.04).
- Beta channels (up to 20 testers) carry each release candidate first.
- Roku updates installed channels on its own. The compatibility contract (58.01) covers the gap
  between a home's release and Roku's.

## Acceptance criteria

- **Given** a Roku Ultra and a 4K HEVC HDR10 MKV with EAC3, **then** it plays in direct play with
  HDR, and EAC3 passes through.
- **Given** an AV1 source, **then** the home serves DASH and the Roku plays it, or transcodes on a
  Roku without AV1.
- **Given** a DV profile 8.1 file on a Dolby Vision Roku, **then** it plays as HDR10.
- **Given** Arabic subtitles, **then** the channel's own layer draws them right to left, in the
  correct word order.
- **Given** Roku's certification run, **then** every line of the table passes.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | brs-engine unit | capability report from recorded `roDeviceInfo` answers of three models | build `device_caps` | 58.02 T07's vectors |
| T02 | brs-engine unit | Arabic and English cues | the subtitle layer's choice | Arabic → own layer with visual text; English → Roku's renderer |
| T03 | brs-engine unit | deep-link router | each media type, a blocked ID, voice `inputEvent` | plays / Home with the line |
| T04 | Go unit (streaming) | `roku` profile × source matrix | `Decide` | DV p8 → HDR10 remux; AV1 → DASH; TrueHD → EAC3 + AAC stereo |
| T05 | Roku Automated Channel Testing on a real Roku (checklist, or the self-hosted runner of Q6) | review home | launch → Home timing; play; Back out | within limits; debug console log in the report |
| T06 | Real-device checklist (`docs/stores/roku.md`) | Roku Express (HD), Roku Express 4K, Roku Ultra, a Roku TV | each release candidate | formats, HDR, passthrough, Arabic overlay and subtitles, voice search, certification table |

**Honest note:**
- brs-engine has no real Video node, so playback itself is tested only on devices (T05, T06).
- Roku has no emulator. Its WebDriver needs a device on the runner's network.

## Strings

| Key | English | Arabic |
|---|---|---|
| `roku.player.upNext` | Up next | التالي |
| `roku.player.retrying` | Trying a converted version… | جارٍ تجربة نسخة محوّلة… |
| `roku.player.failed` | This video can't play on this Roku. | لا يمكن تشغيل هذا الفيديو على جهاز Roku هذا. |
| `roku.player.audio` | Audio | الصوت |
| `roku.player.subtitles` | Subtitles | الترجمة |

## Edge cases

- **Roku's system captions setting on** with no subtitle chosen in Maktaba: the channel honours
  Roku's setting and picks the subtitle in the person's language if there is one.
- **A 1080p Roku Express on a 4K source**: `device_caps` says 1080p H.264 only, so the home
  transcodes.

## Build plan

| File | Change |
|---|---|
| `apps/tv/roku/components/Player/` | Video node, overlay, subtitle layer |
| `apps/tv/roku/source/caps.bs` | the capability report |
| `.github/workflows/roku-release.yml` (new) | zip build; signing on a device is a release-session step |
| `docs/stores/roku.md` | listing, certification table, checklist |
