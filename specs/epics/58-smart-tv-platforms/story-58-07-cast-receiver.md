# Story 58.07 — Maktaba's Cast receiver at cast.maktaba.tv

> Epic 58 · Phase 2 · Depends on: 58.01, 58.02, 58.04 · Needed by: 58.06, 58.14, 58.16

## Why

Google's Default Media Receiver plays a URL with Google's own screen. It has no Arabic, no
Maktaba queue, no subtitle choice by language, and no way to report progress to the home. A
Custom Web Receiver is a web page Maktaba writes with Google's CAF Web Receiver SDK. Google
loads it on the Cast device when a sender starts a session with its application ID. It must
be served over HTTPS at a public URL and registered in the Google Cast SDK Developer Console,
for a one-time $5 fee (research.md §5.1).

## Description

**Where it lives.** It is a static page at `https://cast.maktaba.tv/r/<api_level>/`, under
maktaba.tv (everything users touch).
- It is served by a Cloudflare Worker's static assets, beside the other maktaba.tv sites
  (`web/packages/tvsite`'s pattern).
- It carries no media. Every video byte goes from the home, or the relay, straight to the TV.
- The registered URL is `https://cast.maktaba.tv/r/`. That page reads the sender's `api_level`
  (58.06's custom data) and loads the newest receiver build that supports it. One registration
  serves every home version (58.01). A build is kept as long as its level is within 12 months,
  the same window as the home's.

**What it is.** A small TypeScript app, `web/packages/castreceiver`, built for the Cast devices'
Chromium:
- **CAF Web Receiver SDK.** `useShakaForHls: true`, with `shakaVersion` pinned to a tested
  release. Google is moving HLS to Shaka and has stopped fixing its older player (research.md
  §5.1). `hlsSegmentFormat` is set from the session (`TS` or `FMP4`, 58.02).
- **The home, from the custom data.** The `home` address is tried first. If it does not answer
  within 3 seconds, the receiver uses the relay address, which it reads from
  `GET /api/discovery/home`'s `urls` through the first address that answered for the sender.
- **Capabilities.** It builds `device_caps` (58.02) from `canDisplayType` and the platform's
  `IS_DOLBY_ATMOS_SUPPORTED`, then opens its own stream session with the cast grant (58.06),
  profile `cast`.
- **Progress and renewal.**
  - It reports progress every 10 seconds (`POST …/progress`, allowed by the grant).
  - It renews the signed URL before it ends.
  - It walks a play queue when given `queue_id`.
- **Its own screens, 10-foot and dark:**
  - an idle screen with the Maktaba mark;
  - the loading and buffering states;
  - the player overlay: title, series and episode, position, chapter, an Up next card;
  - the paused screen, with the poster and description;
  - errors, each with a next step.
- **Arabic and RTL.** The overlay mirrors in Arabic, chosen by the sender's `lang`. It uses the
  web's Arabic fonts (IBM Plex Sans Arabic, Amiri headings; SIL OFL, bundled). Numbers and times
  stay left-to-right inside Arabic text, with LRM marks.
- **Subtitles.** The receiver uses the WebVTT renditions of story 48.4 in the HLS manifest, and
  sidecar text tracks for direct play. Arabic subtitles render right-to-left.
- **Logs.** CastDebugLogger is on in debug builds only. Release builds log nothing to the device.

**Network constraints, said honestly:**
- A receiver page is HTTPS, so its media must be HTTPS too. Mixed content is blocked. The home's
  per-home certificate on its HTTPS name (story 50.15) is what makes LAN playback possible at all.
- Chrome's Local Network Access treats a public name that resolves to `192.168.x.x` as local.
  Whether Cast devices enforce it (they have no permission prompt) is not documented. The first
  task of this story is a spike on a Google TV Streamer and a Chromecast with Google TV that
  plays from the HTTPS LAN name.
- If LAN fetches are blocked, the receiver uses the relay for everything (cost: relay bandwidth),
  and the story records the finding in research.md.

**Registration and release.**
- The application ID is registered once in the Cast SDK Developer Console, under the project's
  Google account. The application ID is public; the account's credentials live in the release
  secrets.
- Test devices are registered by serial number for unpublished builds.
- A `v*` tag deploys a new `/r/<level>/` build when the receiver changed
  (`cast-receiver-release.yml`).
- The console entry is published once and needs republishing only if the registered URL
  changes, which it never does.

## Acceptance criteria

- **Given** a sender on Android, iOS or Chrome, **when** it casts, **then** the TV shows Maktaba's
  player screen with the title, in the sender's language.
- **Given** Arabic, **then** the overlay is mirrored and the times read correctly (LTR digits
  inside RTL text).
- **Given** an HEVC source on a Chromecast with Google TV, **then** the session is HLS fMP4 or
  direct, never HEVC in TS.
- **Given** the home's LAN name not answering from the TV, **then** playback starts through the
  relay within 6 seconds, and the overlay shows the Remote badge (the Android TV app's
  `live_remote_badge` meaning).
- **Given** a sender on `api_level` 3 and a receiver deployed up to level 5, **then** `/r/` loads
  the build that supports level 3.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Vitest | the receiver's logic with a fake `cast.framework` | load request with custom data | opens a session with the grant and the `device_caps`; picks TS/FMP4 |
| T02 | Playwright (Chromium, 1920×1080), dark, en and ar | the receiver page with a CAF stub and a mock home (`web/test-cases` pattern) | load, pause, error, Up next | screenshots match; mirrored in ar; the console log saved to the CI report |
| T03 | Vitest | the home address times out | load | falls back to the relay address |
| T04 | Unit | `/r/` loader with builds 3–5 | sender level 3, 6, 2 | 3, newest ≤ 6, the "Update the home" screen |
| T05 | Spike, then real-device checklist | Google TV Streamer, Chromecast with Google TV, a TV with Cast built in | LAN HTTPS name, relay, Arabic subtitles, DV and HDR10, EAC3 | recorded per device in `docs/stores/cast.md` |

**Honest note:** no emulator runs a Cast receiver (research.md §5.1). T01–T04 run the page in
desktop Chromium against a stub of Google's SDK. That proves the receiver's logic and its
screens, not that Google's SDK on a real device behaves the same. T05 is real devices only.

## Strings

The receiver's catalogs are JSON (`web/packages/castreceiver/i18n/{en,ar}.json`), checked by the
web's catalog check:

| Key | English | Arabic |
|---|---|---|
| `receiver.idle` | Ready to play from Maktaba | جاهز للتشغيل من Maktaba |
| `receiver.loading` | Getting %1$s ready… | جارٍ تجهيز %1$s… |
| `receiver.upNext` | Up next | التالي |
| `receiver.remote` | Through Maktaba Cloud | عبر Maktaba Cloud |
| `receiver.error.home` | The TV can't reach your home. Check that the home is on, or turn on remote access. | لا يستطيع التلفاز الوصول إلى منزلك. تأكد من أن المنزل يعمل، أو فعِّل الوصول عن بُعد. |
| `receiver.error.format` | This TV can't play this video. Try again: the home will convert it. | لا يستطيع هذا التلفاز تشغيل هذا الفيديو. أعد المحاولة: سيحوّله المنزل. |
| `receiver.error.grant` | This cast has ended. Cast again from your phone. | انتهى هذا البث. ابدأ البث مجددًا من هاتفك. |

## Edge cases

- **Two senders cast to one TV**: CAF's session model applies. The second sender takes over, and
  the first sees its mini controller end.
- **The grant expires mid-film** (more than 6 hours): the receiver shows `receiver.error.grant`
  at the end of the current item, never mid-play. The segment URLs already signed play out.
- **A Chromecast 3rd gen** (H.264 1080p only): its `canDisplayType` answers make every HEVC
  source a transcode, with no special case.

## Build plan

| File | Change |
|---|---|
| `web/packages/castreceiver/` (new) | the app, i18n, Worker config |
| `.github/workflows/cast-receiver-release.yml` (new) | build, T01–T04, deploy `/r/<level>/` |
| `docs/stores/cast.md` | console steps, test device registration, T05 checklist |
