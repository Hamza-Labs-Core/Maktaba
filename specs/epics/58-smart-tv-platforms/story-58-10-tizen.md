# Story 58.10 — Samsung Tizen: AVPlay, the package and the Samsung store

> Epic 58 · Phase 3 · Depends on: 58.02, 58.04, 58.09 · Needed by: 58.14, 58.16

## Why

Samsung is the largest smart-TV brand. Its share of TV operating systems is about 17% worldwide
in 2025 per Omdia and 23% of US connected-TV platforms in use per Parks Associates, and it is
strong in the Gulf (research.md §7). The launcher (58.09) and the TV UI (58.08) are shared with
LG. This story is what only Samsung needs: its player, its package and its store.

## Description

**Supported TVs.** 2020 models and later: Tizen 5.5 and up, Chromium M69 and up. Older TVs are
told so (58.09).

**Player: AVPlay.** Samsung's guidance (research.md §3.2) rules the choice:
- HTML5 MSE is capped at FHD on Samsung TVs.
- Adaptive streaming, 4K and extra subtitle formats are AVPlay's.

The bridge's `play` therefore drives `webapis.avplay`:
- **Formats.** HLS (TS or fMP4) and direct play of MP4, MKV and TS files, which AVPlay opens by
  URL. The signed URL (`?sig=`) needs no header, which matters because AVPlay cannot set
  headers per segment.
- **Video plane.** `setDisplayRect` covers the screen under the transparent iframe;
  `setDisplayMethod` is letterbox.
- **Tracks.** Audio tracks come from `getTotalTrackInfo` and are switched with
  `setSelectTrack('AUDIO', i)`.
- **Subtitles.**
  - **Embedded** subtitles arrive as text through `onsubtitlechange`. The launcher draws them in
    its own subtitle layer: RTL for Arabic, sized for 10 feet, with the person's size choice.
  - **Sidecar WebVTT** is fetched and drawn the same way.
- **Seeking and 4K.** Seek uses `seekTo`. For 4K, `setStreamingProperty('SET_MODE_4K', 'TRUE')`
  is called when the source is 2160p.
- **No Dolby Vision on Samsung TVs.** The `device_caps` never lists `dv-*`, so the home sends
  HDR10 or HLG (58.02). HDR10+ is not a source format Maktaba keeps, so it is not reported.
- **Audio.** EAC3 and AC3 pass through to the TV's output when the TV reports them. DTS is
  unsupported on 2025 models, so it is transcoded (58.02).
- **Errors.** `onerror` is reported to the UI as `state.error`. The UI retries once with
  `force_transcode` (58.02).

**Keys.** `tizen.tvinputdevice.registerKeyBatch` registers the media keys (`MediaPlay`,
`MediaPause`, `MediaPlayPause`, `MediaFastForward`, `MediaRewind`, `MediaStop`) and
`ColorF0Red`…`ColorF3Blue` at start, and the bridge forwards them (58.09). Back is `XF86Back`
(10009). On the top screen, after the UI's confirm, the launcher calls
`tizen.application.getCurrentApplication().exit()`.

**Package.** `config.xml`:
- `tizen:application` with the app ID and package;
- privileges `internet`, `tv.inputdevice`, `application.launch`, `network.get`, `tv.display`;
- `<access origin="*" subdomains="true"/>` for the homes' and the relay's names (they are
  per-home names, so they cannot be listed);
- `tizen:allow-navigation` for the same;
- a CSP that allows `frame-src https:` and the LAN's `http:` fallback only.

The package is signed with the project's Samsung author certificate and the distributor
certificate from Samsung's certificate manager. Both live in the release secrets and are used
only by the release workflow.

**Store: Samsung TV Seller Office.**
- **Account.** A new account is a Public Seller, which can launch in the **US only**. Any other
  country, including the Gulf, needs Partner Seller status, which takes a contract with Samsung
  (research.md §3.4). The first release goes to the US as a Public Seller while partnership is
  applied for (owner question Q2).
- **Review.** Submit, review, verification test with the review home (58.04), then launch.
  Every update is reviewed again, with a phased rollout (3%, 10%, 100%).
- **Age rating.** The app shows the home's content, which the store cannot rate. Samsung's form is
  answered as a media player with user-supplied content. Samsung forbids 18+ apps in Korea,
  Saudi Arabia and the UAE (research.md §3.4); the app itself is not 18+, and the parental filter
  (58.14) is described in the submission.
- **Updates.** Launcher updates go through the store. The UI updates with the home (58.08), so
  store updates should be a few a year.

**Release.** A `v*` tag whose launcher changed builds `maktaba-tizen-<version>.wgt`
(`tv-web-release.yml`) with the Tizen CLI in a container. A person uploads it to Seller Office
with the notes from `docs/stores/samsung.md`; Seller Office has no public upload API.

## Acceptance criteria

- **Given** a 2022 Samsung TV and a 4K HEVC HDR10 MKV with EAC3, **then** it plays in direct play
  through AVPlay with HDR, and EAC3 passes through to a soundbar.
- **Given** a DV profile 8.1 file, **then** it plays as HDR10 (a remux, not a transcode).
- **Given** Arabic subtitles chosen, **then** they render right to left in the launcher's
  subtitle layer, at the person's size.
- **Given** the red key or Play/Pause on the remote, **then** the UI gets the action through the
  bridge.
- **Given** a 2019 Samsung TV, **then** the launcher says the TV is too old and names Cast.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Vitest | `platform/tizen.ts` with a fake `webapis.avplay` | play, tracks, subtitles, seek, error | the AVPlay call sequence; errors as `state.error` |
| T02 | Vitest | the subtitle layer | Arabic and English cues | `dir=rtl` for Arabic; sizes |
| T03 | Package lint | built `.wgt` | parse `config.xml` | privileges, CSP, access exactly as listed |
| T04 | Samsung TV emulator (Tizen Studio, KVM, Linux runner; best effort) | the `.wgt` with the launcher | launch, cloud code screen, find on network against a mock home | screenshots en and ar; the emulator's log in the CI report. AVPlay and UHD do not exist on the emulator, so playback is out of scope here |
| T05 | Real-device checklist (`docs/stores/samsung.md`) | a 2020 (Tizen 5.5), a 2022 and a 2025 Samsung TV, by `sdb` in Developer Mode | each launcher release candidate | 4K HDR10, DV p8 as HDR10, EAC3 passthrough, Arabic subtitles, keys, sleep/wake, performance budget |

**Honest note:**
- The Samsung emulator runs the launcher and the UI, but it has no AVPlay, no UHD and no secure
  WebSockets (research.md §3.3). Whether it runs headless on a CI runner is not documented, so
  T04 is best effort and not a required check.
- Player behaviour is proven by T01's fake and by T05 on real TVs only.

## Strings

The launcher's catalogs (58.09) and these:

| Key | English | Arabic |
|---|---|---|
| `tizen.error.playback` | The TV couldn't play this. Trying a converted version… | تعذّر على التلفاز تشغيل هذا. جارٍ تجربة نسخة محوّلة… |
| `tizen.subtitles.size` | Subtitle size | حجم الترجمة |

## Edge cases

- **The TV's own Samsung account and parental PIN**: Samsung's TV-wide app lock applies before
  the app opens. Maktaba neither reads nor sets it, and the home's filter (58.14) still applies.
- **A soundbar that cannot take EAC3**: the TV reports only what its output can do, so
  `device_caps` drops passthrough and the home transcodes audio.

## Build plan

| File | Change |
|---|---|
| `apps/tv/web/platform/tizen.ts`, `apps/tv/web/tizen/config.xml` | player, keys, package |
| `apps/tv/web/subtitles/` | the shared subtitle layer (webOS uses it for sidecar files) |
| `.github/workflows/tv-web-release.yml` (new) | `.wgt` build and signing |
| `.github/workflows/_tv-web.yml` (new) | T01–T03; T04 as a non-required job |
| `docs/stores/samsung.md` | listing, notes, checklist |

## As built

- **Files.** The player and caps are `apps/tv/web/src/platform/tizen/` (`avplay.ts`, `caps.ts`)
  beside `platform/tizen.ts`; the package is `apps/tv/web/tizen/` (`config.xml`, `icon.png`,
  `package_wgt.py`, `lint_wgt.py`, `sign_wgt.sh`); the subtitle layer is `src/subtitles/`, shared
  with 58.11.
- **4K.** Samsung's AVPlay reference deprecates `SET_MODE_4K` from Tizen 5.0. The adapter sets
  `ADAPTIVE_INFO` to `FIXED_MAX_RESOLUTION=3840X2160` in IDLE, before prepare, and falls back to
  `SET_MODE_4K` when a firmware refuses it. A 2160p source is known from `play`'s optional
  `height`; without it, an adaptive source on a UHD panel gets the 4K mode.
- **Privileges.** `webapis.productinfo` and `webapis.network.getIp()` need Samsung's
  `productinfo` and `network.public` privileges, so `config.xml` lists them with the five above.
- **Caps.** The audio output mode is `tizen.tvaudiocontrol.getOutputMode()`: Dolby Digital Plus
  passes AC3 and EAC3 through, Dolby Digital AC3 only, anything else none.
- **Subtitles.** The size is the optional `subtitle_scale` of `play` and `subtitle` (/tv/'s own
  subtitle size setting), so `tizen.subtitles.size` is not needed. An embedded track is chosen
  as `embedded:<n>`.
- **Audio tracks.** `state` lists the stream's audio tracks (`audio_tracks`, from
  `getTotalTrackInfo`).
- **Signing.** Tizen Studio's CLI is pinned by sha256. On a headless runner it keeps certificate
  passwords in an unlocked keyring. A tag without the project's certificates rehearses signing
  with a throwaway one, which is never attached to the release.
- **T04.** The Samsung TV emulator is not run in CI. It needs the TV extension and a KVM
  runner, has no documented headless mode, and would show less than the Playwright shim does
  (no AVPlay, no UHD). `docs/stores/samsung.md` says so.
