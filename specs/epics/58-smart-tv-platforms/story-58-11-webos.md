# Story 58.11 — LG webOS: the native player, the package and the LG Content Store

> Epic 58 · Phase 3 · Depends on: 58.02, 58.04, 58.09 · Needed by: 58.14, 58.16

## Why

LG is the third-largest TV operating system, at about 7–12% of TVs worldwide depending on the
source (research.md §7). LG is also the most open store for an individual developer: an
Individual Seller account exists, no partner contract is needed, and jellyfin-webos already
ships a hosted UI through LG's QA (research.md §4.3–4.4). It shares the launcher (58.09) and
the UI (58.08) with Samsung.

## Description

**Supported TVs.** webOS 5.0 and later (2020 models on, Chromium M68 and up). webOS 4.x (M53) is
told it is too old (58.09).

**Player.** The bridge's `play` drives a full-screen `<video>` element in the launcher, under the
iframe:
- **HLS.** Native HLS v7 on webOS 5.0+, with TS and fMP4 segments (research.md §4.2). The
  manifest URL is set directly on `<video>`. hls.js is not used, since the TV's own player
  handles HDR and passthrough better than MSE.
- **Direct play** of MP4 and MKV files by URL, with `?sig=` and no headers.
- **DASH.** Not used. The home opens HLS for `webos` (58.02).
- **Audio tracks** come from `audioTracks`. **Subtitles**: the manifest's WebVTT renditions
  (story 48.4) through `textTracks`, drawn by the launcher's shared subtitle layer (58.10), so
  Arabic renders right to left the same on both TVs.
- **What LG's player does not do** (research.md §4.2):
  - No playback speed other than 1.0, so the speed menu is hidden on webOS.
  - No I-frame playlists, so no trick-play thumbnails from them. The UI uses the home's trickplay
    images, which it already reads.
  - Audio and video segment durations must match, which the home's segmenter already does.
  - `EXT-X-INDEPENDENT-SEGMENTS` is listed as unsupported. T05 checks whether webOS ignores the
    tag or refuses the stream. If it refuses, the home omits the tag for the `webos` profile.
- **Dolby Vision** in web apps is unreliable (research.md §4.2). The `device_caps` reports
  `dv-p8` only when `MediaSource.isTypeSupported` says so *and* the real-device checklist has
  marked that model year as good (`DV_GOOD_YEARS` in `src/platform/webos-caps.ts`).
  Otherwise the home sends the HDR10 base layer.
- **Caps** come from luna `getSystemInfo` / `webOS.deviceInfo` plus `isTypeSupported`, checked
  against `shared/playback/caps-vectors.json`. Luna is reached through `webOS.service.request` or
  `PalmServiceBridge`, documented services only.

**Keys.**
- webOS sends Back as key code 461. The top-level Back, after the UI's confirm, calls
  `webOS.platformBack()`.
- Media keys (415 Play, 19 Pause, 413 Stop, 417 Fast-forward, 412 Rewind) and the color keys
  (403–406) arrive as normal key events inside the launcher and are forwarded.
- The Magic Remote's pointer reaches the iframe directly; `bigscreen` handles the pointer.

**Package.**
- `appinfo.json`: `id`, `version`, `type: web`, `main`, `title`, icons, `resolution: 1920x1080`,
  `disableBackHistoryAPI: true`, so the app handles Back itself.
- Built with the webOS CLI (`ares-package`) in a container.
- LG signs packages on submission, so no signing key is held.

**Store: LG Seller Lounge → LG Content Store.**
- **Account.** Individual Seller.
- **Submission.** The IPK, the UX scenario document (from `docs/stores/lg.md`), the self-checklist
  and the review home's sign-in (58.04).
- **QA.** Pretest, function and content stages, with LG's App CTS suite. Every update is QA'd
  again. LG publishes no duration; third parties report 2 to 12 weeks, so the release plan
  assumes the long end for the first submission.
- **Countries.** Every country the Content Store serves, including the Gulf states (owner
  question Q4).

**Release.** A `v*` tag whose launcher changed builds `maktaba-webos-<version>.ipk`
(`tv-web-release.yml`, with 58.10). A person uploads it to Seller Lounge; there is no public
upload API.

**Developer Mode for tests.** The Developer Mode app's session now lasts up to 1000 hours
(research.md §4.3). The device checklist notes the session's end date, and an expired session
removes the test app, so the checklist starts with Extend.

## Acceptance criteria

- **Given** a 2021 LG TV (webOS 6) and a 4K HEVC HDR10 MKV, **then** it plays in direct play with
  HDR.
- **Given** an HEVC remux, **then** the home serves HLS with fMP4 segments and webOS plays it.
- **Given** Arabic subtitles, **then** they render right to left in the shared subtitle layer.
- **Given** Back on the top screen, **then** the confirm shows, and Leave returns to the webOS
  launcher.
- **Given** a webOS 4.5 TV, **then** the launcher says the TV is too old and names Cast and AirPlay.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Vitest (`src/platform/webos.test.ts`) | `src/platform/webos.ts` with a fake `<video>` and `webOS` object | play, tracks, subtitles, Back | the call sequence; speed menu hidden |
| T02 | Package lint | built `.ipk` | parse `appinfo.json` | fields as listed |
| T03 | Not run in CI: the webOS TV Simulator downloads only behind LG's license page, with no fetchable URL, no documented headless run and no media pipeline (`docs/stores/lg.md`) | — | — | — |
| T04 | Playwright (Playwright's own Chromium), `web/e2e/launcher/webos.ui.spec.ts` on a webOS shim, beside the T04 flows of 58.09 | launcher + `/tv/` through the launcher's `<video>` | caps in the session, the transcode retry, playback, the Arabic RTL cue, Back and exit | as 58.09 T04. Playwright's Chromium decodes no H.264, so CI plays VP9 |
| T05 | Real-device checklist (`docs/stores/lg.md`) | a webOS 5 (2020), a webOS 23 and a webOS 25 TV, via `ares-install` in Developer Mode | each launcher release candidate | 4K HDR10, HLS fMP4, `INDEPENDENT-SEGMENTS` behaviour, DV p8 per model year, EAC3, Arabic subtitles, Magic Remote, performance budget |

**Honest note:** LG's simulator has no real media pipeline (research.md §4.3), and running it
headless in CI is not documented, so it is not run (T03). HLS, HDR, Dolby Vision and passthrough
are proven only on real TVs (T05).

## Strings

The launcher's catalogs (58.09); no webOS-only strings.

## Edge cases

- **LG's own parental lock on apps**: applies before the app opens. The home's filter (58.14)
  still applies.
- **A TV whose Developer Mode expired during a checklist run**: the app is removed. The checklist
  restarts at Extend, and the result is not recorded as a failure.

## Build plan

| File | Change |
|---|---|
| `apps/tv/web/src/platform/webos.ts` (with `webos-caps.ts`, `webos-luna.ts`, `webos-player.ts`), `apps/tv/web/webos/` (`appinfo.json`, `package.mjs`, `lint-ipk.mjs`) | player, caps, keys, package |
| `.github/workflows/tv-web-release.yml` | `.ipk` build |
| `.github/workflows/_tv-web.yml` | T01–T02, T04; the `.ipk` built and linted on every related run; no simulator job |
| `docs/stores/lg.md` | listing, UX scenario, self-checklist, device checklist |
