# Epic 58 research: the TV platforms Maktaba does not reach yet

Read on 2026-11-08: each platform's developer documentation, the analysts' market reports cited
below, and the Maktaba repository on `main` at 0770a41db. Where a claim comes from a third party
(a forum, a vendor's guide, the press) or could not be confirmed from the platform's own pages,
it says so. Market figures differ between analysts because they count different things:
shipments, installed sets, viewing hours or ad traffic. Each figure names its source and what it
counts.

## 1. What a TV gets from a Maktaba home today

### 1.1 Reaching a home

- **LAN discovery.** Homes announce `_maktaba._tcp` over mDNS; the Android core and MaktabaKit
  browse it.
- **The discovery endpoint.** `GET /api/discovery/home` (no sign-in,
  `api/internal/handlers/discovery/home.go`) answers `{id, name, version, urls, sign_in}`. Older
  homes answer `GET /api/system/version` (MaktabaKit's `LegacyVersion`).
- **HTTPS on the LAN** (story 50.15). Each home has a publicly trusted certificate for IP-encoded
  names (`192-168-1-20.<home-id>.<direct-domain>`), so a LAN address has valid HTTPS. Routers that
  block DNS rebinding break those names; the native apps then use the bare IP with the
  certificate pinned by home ID. A web runtime cannot pin, so it needs another fallback (58.09).
- **The cloud's connection list.** `GET /v1/homes` lists each home's `connections`: `local`,
  `local_ip` (with `tls_name`), `direct`, `relay_hint`, `relay`. Apps try them in that order
  (story 50.13, `apps/android/client/…/session/Cloud.kt`).
- **Sign-in by code** (story 15.06): `POST /api/pairing/device`, then `POST /api/pairing/approve`
  from a signed-in phone (or the web's `/pair`), then `POST /api/pairing/device/token`. It is on
  the home only. There is no cloud device-code flow: `cloud/internal/handlers/auth` has register,
  login, refresh and OAuth, and nothing for a device without an account.
- **CORS** (`api/internal/httpsec/cors.go`) is an exact allow-list with credentials, empty by
  default. A packaged TV web app (a `file://` origin) gets no CORS answer today.

### 1.2 Playing

- **Sessions.** `POST /api/stream/sessions` takes `client_profile`, `audio_track`,
  `subtitle_track`, `start_sec`, `max_bitrate_kbps`, `format`, `force_transcode`,
  `force_software`, `burn_subs` and `accept_queue`, and answers `mode` (`direct`, `remux`,
  `transcode`), `manifest_url` or `direct_url`, the ladder and `expires_at`.
- **Profiles** (`streaming/internal/capability/matrix.go`): `generic` (H.264/AAC HLS, 720p, the
  fallback for unknown names), `desktop-player`, `browser-chrome`, `browser-safari`,
  `ios-native`, `tvos`, `android-native` and `androidtv`. The `androidtv` profile allows
  H.264/HEVC/VP9/AV1, AAC/AC3/EAC3/Opus, SDR/HDR10/DV, 2160p, 60 Mbps, MP4/MKV, HLS and DASH.
  There is no per-device report.
- **HLS segments are MPEG-TS** (`ffmpeg.go`, `seg-%d.ts`). DASH uses fMP4 (`init-*.m4s`,
  `chunk-*.m4s`) and is opt-in per session.
- **Signed URLs.** `?sig=` is accepted without a cookie or bearer token for `/stream/{session}/…`
  and `/stream/direct/{video}` (`cmd/maktaba-server/stream_gate.go`, story 48.12). Channel paths
  still need a session; Epic 57's story 57.10 signs them.
- **Subtitles** are WebVTT, as sidecars and as HLS `EXT-X-MEDIA` renditions (story 48.4).

### 1.3 What exists for TVs

- **Apple TV** (`apps/tv/tvos`, SwiftUI, AVPlayer) and **Android TV** (`apps/tv/android`, Compose
  for TV, Media3, minSdk 28, target 37, on the shared core). The Android TV app uses no Google
  Play Services. Only the phone Player uses the Cast framework
  (`apps/android/gradle/libs.versions.toml`, `player/…/media/Cast.kt`).
- **Cast today.** The Android Player casts to Google's **Default Media Receiver**, on the LAN only,
  with the signed URL and the title. The Apple Player and the web do not cast.
- **The web's big-screen view** (Epic 46, `web/src/bigscreen`, served at `/big`): a 10-foot UI
  with its own focus engine, key and gamepad input, English and Arabic. Built for `es2022`
  (`web/vite.config.ts`).
- **Epic 14 left Tizen and webOS out**, pointing to DLNA (story 15.4). Epic 48's research lists
  Fire TV and Chromecast among Plex's and Jellyfin's clients.
- **The client header.** `X-Maktaba-Client` names `app/version (platform; device)` and is a label,
  never a right (`api/internal/client`).
- **No compatibility contract.** The apps read the home's `version` string and nothing says which
  app builds a home serves.
- **Parental controls** (Epic 57) filter on the home, for every client. **No TV platform in this
  epic was found to give an app the viewer's age or the TV's rating limit**:
  - Fire TV, Roku, Samsung and LG each have a TV-wide PIN or app lock, set by the parent on the
    TV;
  - no app-readable signal like Apple's Declared Age Range was found in their developer docs.

## 2. Amazon

### 2.1 Fire TV on Fire OS (Android)

- **Fire OS and Android versions.** Fire OS 7 is API 28, Fire OS 8 is API 30, Fire OS 14 is
  API 34 and Fire OS 16 is API 36. The Fire TV Stick 4K Max 2nd gen, the Fire TV Stick 4K 2nd gen
  and the 4K Plus (2025) run Fire OS 8; the Fire TV Cube 3rd gen is listed at Fire OS 7.
  ([identify devices](https://developer.amazon.com/docs/device-specs/identify-fire-tv-devices.html),
  updated 2026-10-16; [Fire OS 16](https://developer.amazon.com/docs/fire-tv/fire-os-16.html))
- **targetSdk.** Amazon sets a minimum targetSdk per Fire OS version (7 → 28, 8 → 30, 14 → 34,
  16 → 36) and no maximum, so targetSdk 37 should be accepted (no explicit statement found).
  ([device filtering](https://developer.amazon.com/docs/app-submission/device-filtering-and-compatibility.html))
- **No Google Play Services.** Push goes through Amazon Device Messaging.
  ([FAQ](https://developer.amazon.com/docs/fire-tv/faq-general.html))
- **Manifest and launcher.** Touchscreen must be `required=false`; compatible devices are ticked in
  the console. Amazon's FAQ says Fire OS supports Leanback. An older community answer says Fire TV
  ignores `LEANBACK_LAUNCHER`, so declaring both launcher filters is the safe choice (unsettled
  by a current Amazon page).
- **Compose for TV.** Amazon's own developer-relations team published a Compose Fire TV sample
  ([dev.to](https://dev.to/amazonappdev/hello-world-for-fire-tv-with-kotlin-jetpack-compose-4b7i));
  no Amazon doc lists formal support.
- **Remote and review.** Remotes send key events only, never motion
  ([controller guidelines](https://developer.amazon.com/docs/fire-tv/controller-behavior-guidelines.html)).
  Amazon's test criteria include D-pad operation and Back leaving to the launcher
  ([test criteria](https://developer.amazon.com/ja/docs/app-testing/test-criteria.html)).
- **Player.** Amazon recommends its patched Media3 port for device fixes
  ([media players](https://developer.amazon.com/docs/fire-tv/media-players.html),
  [amzn/media3-external-port](https://github.com/amzn/media3-external-port)).
- **Capabilities, Fire TV Stick 4K Max 2nd gen:** HDR10, HDR10+, HLG, Dolby Vision; HEVC, VP9 and
  AV1 in 8 and 10 bit; Atmos (DD+ JOC, AC-4); TrueHD and DTS passthrough
  ([specs](https://developer.amazon.com/docs/fire-tv/device-specifications-fire-tv-stick.html)).
  Capabilities vary by model.
- **Store and account.** The Developer Console is free, with no annual fee (long-standing; not
  re-verified for 2026). Amazon publishes no review time; vendors report 3–10 working days
  (third party). **Live App Testing** is the beta channel, by email invitation
  ([LAT](https://developer.amazon.com/docs/app-testing/live-app-testing-getting-started.html)).
- **The phone Appstore closure** (2025-08-20) does not affect Fire TV
  ([Amazon blog](https://developer.amazon.com/apps-and-games/blogs/2025/02/upcoming-changes-to-amazon-appstore-for-android-devices-and-coins-program)).
- **No Fire OS emulator.** Amazon points to a stock Android TV AVD (API 30) as a fallback, and to
  real devices ([Android TV emulator](https://developer.amazon.com/docs/fire-app-builder/use-an-android-tv-emulator.html)).
  That AVD is Google's Android, not Fire OS.

### 2.2 Vega OS (Linux)

- **Launch.** Announced 2025-09-30 on the Fire TV Stick 4K Select, with Fire OS devices to
  continue ([announcement](https://developer.amazon.com/apps-and-games/blogs/2025/09/announcing-vega-os)).
- **Vega devices** (Vega OS 2.0): the Fire TV Stick 4K Select (2025), the Fire TV Stick HD (2026)
  and the Fire TV Stick 4K 3rd gen (2026-09-30; per AFTVnews, no Dolby Vision)
  ([AFTVnews](https://www.aftvnews.com/amazon-announces-new-3rd-gen-fire-tv-stick-4k-running-vega-os-with-an-all-new-remote/)).
  The press says future sticks will all run Vega; unconfirmed for TVs and the Cube.
- **No Android apps.** No APK support and no sideloading. Amazon's Cloud App Program streams
  Android apps to Vega for chosen partners only
  ([ADBT blog](https://developer.amazon.com/apps-and-games/blogs/2026/10/port-your-tv-app-to-react-native-with-adbt)).
- **Stack.** React Native for Vega (`@amazon-devices` packages) or Vega WebView.
- **Media.** A W3C media API on GStreamer: HLS, DASH and Smooth through MSE (patched Shaka, hls.js,
  dash.js); MP4 and MKV direct; H.264, HEVC, VP8, VP9, AV1; AAC, AC3, EAC3, AC4, Opus, FLAC; WebVTT
  ([media player](https://developer.amazon.com/docs/vega/0.23/media-player.html)).
- **Vega Virtual Device.** Runs on macOS or Ubuntu x86_64 with KVM
  ([install](https://developer.amazon.com/docs/vega/0.24/install-vega-sdk)). Amazon's CI guide
  installs the SDK headless but sets `SKIP_VVD_INSTALL=true` in its examples
  ([CI](https://developer.amazon.com/docs/vega/2.0/continuous-integration)).

## 3. Samsung Tizen

### 3.1 App and engine

- **App format.** A packaged web app (`.wgt`, `config.xml`), built with Tizen Studio and its TV
  extension; the VS Code extension complements it
  ([TV extension](https://developer.samsung.com/smarttv/develop/tools/tv-extension/download.html?device=htv)).
  Tizen Studio 6.1's notes say Tizen Studio will be deprecated in a next version, with no date
  ([notes](https://docs.tizen.org/application/tizen-studio/release-notes/6-1-release-notes)).
- **Chromium by model year**
  ([web engine](https://developer.samsung.com/smarttv/develop/specifications/web-engine-specifications.html)):

  | Year | Tizen | Chromium |
  |---|---|---|
  | 2026 | 10.0 | M130 |
  | 2025 | 9.0 | M120 |
  | 2024 | 8.0 | M108 |
  | 2023 | 7.0 | M94 |
  | 2022 | 6.5 | M85 |
  | 2021 | 6.0 | M76 |
  | 2020 | 5.5 | M69 |
  | 2019 | 5.0 | M63 |

- **Network access** needs the `internet` privilege and `<access origin>`; CSP and
  `allow-navigation` are set in `config.xml`
  ([FAQ](https://developer.samsung.com/smarttv/develop/faq/networking-and-connectivity.html)).
  Packaged apps reportedly send `Origin: file://` (third party).

### 3.2 Player

- **HTML5 or AVPlay.** HTML5 `<video>` handles common formats. **AVPlay** is Samsung's choice for
  adaptive streaming (HLS, DASH, Smooth), 4K and more subtitle formats. AVPlay hands subtitles to
  the app as text through a callback
  ([AVPlay](https://developer.samsung.com/smarttv/develop/guides/multimedia/media-playback/using-avplay.html)).
- **MSE is capped at FHD** on Samsung TVs, so hls.js cannot do 4K
  ([2025 video spec](https://developer.samsung.com/smarttv/develop/specifications/media-specifications/2025-tv-video-specifications.html)).
- **Formats** (2025): MKV, MP4 and TS; HEVC Main10; AV1 and HEVC to 4K (8K on top models); AC-3,
  E-AC-3 and AC-4; **no DTS on 2025 models**. TrueHD and Atmos are not listed.
- **No Dolby Vision** on Samsung TVs; HDR10+ instead (press, not developer docs).

### 3.3 Emulator and devices

- **Emulator.** QEMU-based, with HAXM or Linux KVM. Limits: no UHD, no secure WebSockets, some
  product APIs missing
  ([limitations](https://developer.samsung.com/smarttv/develop/getting-started/using-sdk/tv-emulator/emulator-limitations.html)).
  AVPlay and DRM are unavailable on it (Dolby's player docs:
  [OptiView](https://optiview.dolby.com/docs/theoplayer/getting-started/sdks/tizen/setting-up-emulator)).
  A headless CI mode is not documented.
- **TV Simulator and the Tizen MCP Server.** There is a lighter TV Simulator, and a Tizen MCP
  Server that can generate Playwright tests
  ([MCP](https://developer.samsung.com/smarttv/develop/tools/additional-tools/vscode-mcp-server.html)).
- **Real TVs** are reached in Developer Mode with `sdb connect` (standard workflow).

### 3.4 Store

- **Accounts.** Samsung TV Seller Office: a new account is a **Public Seller, US only**. Other
  countries need **Partner Seller** status, which takes an offline contract with Samsung
  ([membership](https://developer.samsung.com/tv-seller-office/guides/membership/becoming-seller-office-member.html)).
  No fees are listed. Whether individuals qualify is not stated.
- **Process.** Submit, review, verification test, launch. Every update is reviewed; phased rollout
  goes 3%, then 10%, then 100%. Third parties report 1–2 weeks a round
  ([process](https://developer.samsung.com/smarttv/develop/distribute/seller-office.html)).
- **18+ apps** cannot launch in Korea, Saudi Arabia or the UAE
  ([FAQ](https://developer.samsung.com/tv-seller-office/faq/application-registration.html)).
- **Hosted web apps.** A Samsung forum answer says a hosted web app (one that loads a remote URL)
  needs a partnership
  ([forum](https://forum.developer.samsung.com/t/is-it-possible-to-create-hosted-web-based-tizen-tv-app/31395)).
  This is not an official policy page.

### 3.5 Market

Tizen is about 17% of TV operating systems worldwide in 2025, falling to 16% by 2029 (Omdia,
shipments,
[Oct 2025](https://omdia.tech.informa.com/pr/2025/oct/castos-to-exceed-15-million-shipments-set-to-dominate-north-americas-tv-os-market)).
Another analyst, CTVMA, puts it at 12.9% of installed sets in 2024. See §7.

### 3.6 Arabic

Custom Arabic fonts are supported on 2014+ models
([FAQ](https://developer.samsung.com/smarttv/develop/faq/specifications-and-feature-support.html)).
There is no official RTL guide. Plex users reported reversed Arabic subtitles on Samsung in
2020–21 ([forum](https://forums.plex.tv/t/arabic-subtite-reversed-in-samsung-tv/638829/5)), which
is why Maktaba draws subtitles itself.

## 4. LG webOS

### 4.1 App and engine

- **App format.** A web app (`appinfo.json`, `.ipk`), built with the `ares-*` CLI or webOS
  Studio; Enact is optional.
- **Chromium by webOS version**
  ([engine](https://webostv.developer.lge.com/develop/specifications/web-api-and-web-engine)):

  | webOS | Year | Chromium |
  |---|---|---|
  | 26 | 2026 | 132 |
  | 25 | 2025 | 120 |
  | 24 | 2024 | 108 |
  | 23 | 2023 | 94 |
  | 22 | 2022 | 87 |
  | 6.x | 2021 | 79 |
  | 5.x | 2020 | 68 |
  | 4.x | 2018–19 | 53 |

- **No discovery API.** No Luna service for mDNS or SSDP is documented
  ([Luna](https://webostv.developer.lge.com/develop/references/luna-service-introduction)).

### 4.2 Player

- **Native HLS** is version 7 on webOS 5.0+, with fMP4 segments. HTTP/2 from 5.0.
- **Limits:** no playback speed other than 1.0; many HLS tags unsupported, including
  `I-FRAMES-ONLY` and `INDEPENDENT-SEGMENTS`; audio and video segment durations must match
  ([streaming](https://webostv.developer.lge.com/develop/specifications/streaming-protocol-drm)).
- **MSE and EME** are full W3C Recommendation from 5.0, so DASH means dash.js or Shaka.
- **Formats** (webOS 25): MKV, MP4 and TS; HEVC and AV1 to 8K; E-AC-3 and AC-4; DTS on some
  models; WebVTT. TrueHD, Atmos and HDR are not stated
  ([spec](https://webostv.developer.lge.com/develop/specifications/video-audio-250)).
- **Dolby Vision** in web apps is inconsistent; Jellyfin users report profile 8.1 falling back
  (third party,
  [forum](https://forum.jellyfin.org/t-how-can-i-avoid-dolby-vision-transcoding-in-webos)).

### 4.3 Simulator, Developer Mode and hosted apps

- **The webOS TV Simulator** replaces the VirtualBox emulator. It has the TV's Chromium, a remote
  and Luna mocks. It cannot test real video or audio, DRM, or some system APIs
  ([simulator](https://webostv.developer.lge.com/develop/tools/simulator-introduction)).
- **Developer Mode** sessions now last up to 1000 hours; an expired session removes the test apps
  ([Developer Mode](https://webostv.developer.lge.com/develop/getting-started/developer-mode-app);
  [forum](https://forum.webostv.developer.lge.com/t/whats-the-way-to-extend-dev-time-with-webos-cli/28033)).
- **Hosted apps** are a common webOS pattern. **jellyfin-webos** is in the LG Content Store and
  hands control to the web UI hosted on the server
  ([repo](https://github.com/jellyfin/jellyfin-webos)).

### 4.4 Store

- **Accounts.** LG Seller Lounge offers **Individual Seller** accounts
  ([ecosystem](https://webostv.developer.lge.com/distribute/app-ecosystem)).
- **Submission.** The IPK, a UX scenario and a self-checklist. QA runs pretest, function and
  content stages, with App CTS
  ([approval](https://webostv.developer.lge.com/distribute/app-approval-process)). Every update is
  re-approved. No fees are published.
- **Duration.** Not published. Third parties report about 2 to 12 weeks
  ([Easel TV](https://docs.easeltv.com/docs/lg-tv-submission)).

## 5. Google Cast

### 5.1 Receivers, console and testing

- **Senders.**
  - **Android:** Media3's `media3-cast` (`CastPlayer`, `RemoteCastPlayer`)
    ([Media3 Cast](https://developer.android.com/media/media3/cast)).
  - **iOS:** the Cast SDK 4.8.x, which needs `NSLocalNetworkUsageDescription` and `NSBonjourServices`
    with `_googlecast._tcp` and `_<APPID>._googlecast._tcp`
    ([iOS sender](https://developers.google.com/cast/docs/ios_sender)), and a Local Network
    screen before the first cast
    ([design checklist](https://developers.google.com/cast/docs/design_checklist/cast-dialog)).
  - **Chrome:** the sender works only from HTTPS pages (third party).
- **Receiver types.**
  - **Default Media Receiver:** no registration, no styling.
  - **Styled:** CSS only.
  - **Custom Web Receiver:** a full CAF app
    ([registration](https://developers.google.com/cast/docs/registration)).

  A custom receiver can set request headers in
  `PlaybackConfig.manifestRequestHandler`/`segmentRequestHandler`
  ([NetworkRequestInfo](https://developers.google.com/cast/docs/reference/web_receiver/cast.framework.NetworkRequestInfo)).
- **HLS player.** Google has stopped fixing its older HLS player (MPL) and points to Shaka
  (`useShakaForHls`). The default-switch date conflicts between Google's pages, so set it
  explicitly ([Shaka migration](https://developers.google.com/cast/docs/web_receiver/shaka_migration)).
- **fMP4 segments.** These need `hlsSegmentFormat = FMP4` in the load request
  ([protocols](https://developers.google.com/cast/docs/web_receiver/streaming_protocols)).
- **Console and hosting.** The Cast SDK Developer Console costs a one-time $5. A published receiver
  must be served over HTTPS at a public URL. Unpublished receivers run on devices registered by
  serial number.
- **Testing.** Debugging uses CastDebugLogger, the CaC Tool and Chrome's remote inspector
  ([CDL](https://developers.google.com/cast/docs/debugging/cast_debug_logger)). **There is no
  official emulator.**

### 5.2 Media

From the official table ([media](https://developers.google.com/cast/docs/media)):

| Device | Video | HDR | Notes |
|---|---|---|---|
| Chromecast 3rd gen | H.264 1080p60, VP8 | none | — |
| Chromecast Ultra | HEVC Main/Main10 4K60, VP9 p2 | DV, HDR10 | — |
| Chromecast with Google TV | HEVC Main10 4K60, VP9 p2 | not in the table; product pages claim DV, HDR10 and HDR10+ | — |
| Google TV Streamer | adds AV1 Main 4K60 | not in the table | Atmos listed with no passthrough |

- **Containers.** MKV is not listed, so MKV needs a remux. **HEVC is not supported in TS.**
- **Subtitles.** WebVTT, TTML and CEA-608/708. CORS is required on manifests, segments and
  subtitles.

### 5.3 Network

- **Mixed content.** An HTTPS receiver page cannot load HTTP media. Maktaba's per-home
  certificates avoid this.
- **Local Network Access.** Chrome's Local Network Access classifies requests by resolved IP: a
  public name resolving to 192.168.x.x counts as local, with a prompt from Chrome 142
  ([LNA](https://developer.chrome.com/blog/local-network-access)). **Whether Cast receivers
  enforce this is undocumented**: a spike is the first task of story 58.07.
- **Discovery** is mDNS on the same network. Guest and hotel networks may block it.

### 5.4 Market

- **Hardware.** Google ended Chromecast in 2024. The Google TV Streamer launched 2024-09-24
  ([Tech Advisor](https://www.techadvisor.com/article/2617026/google-chromecast-officially-discontinued-after-11-years.html)).
- **Google TV and Android TV.** Over 300 million monthly active devices (I/O, May 2026,
  [9to5Google](https://9to5google.com/2026/05/21/google-tv-hits-300-million-active-devices-as-growth-stalls/)),
  every one a Cast target.
- **Cast built in.** Vizio's TVs and many Sony, TCL and Hisense Google TV sets have Cast built in.

## 6. Roku

### 6.1 Stack

BrightScript and SceneGraph XML. Community tools:
- **BrighterScript**, a superset language;
- **Rooibos** for unit tests;
- **roku-deploy**;
- **brs-engine**, which runs BrightScript and SceneGraph in Node.

### 6.2 Media

From Roku's media specs, updated 2026-11-04 ([media](https://developer.roku.com/dev/docs/media)):
- **Streaming:** HLS (TS or CMAF) and DASH (fMP4 or CMAF).
- **Files:** MP4/MOV/M4V and MKV/WebM play directly with AVC or HEVC.
- **Video:** AVC to 1080p; HEVC to 4K; AV1 only in DASH; VP9 no longer listed.
- **HDR:** HDR10 (HEVC Main10 or AV1); HDR10+ with AV1 only; Dolby Vision as profile 5 (dvhe.05),
  dvav.09 or AV1; HLG with HEVC.
- **Audio:** AC3 and EAC3 decoding by device, with passthrough; DTS passthrough only; Atmos on some
  Ultra models; TrueHD not mentioned. Roku asks for an AAC stereo track beside any Dolby track.
- **Subtitles:** WebVTT (sidecar or segmented), SRT and TTML.

### 6.3 Network

- **HTTPS.** `roUrlTransfer` and `roHttpAgent` set headers and certificates; the stock bundle is
  `common:/certs/ca-bundle.crt` ([roUrlTransfer](https://developer.roku.com/dev/docs/rourltransfer)).
  Publicly trusted per-home certificates work with it.
- **Discovery.** No mDNS API was found. `roDatagramSocket` allows UDP multicast. ECP (port 8060)
  lets phones find and drive a Roku.
- **Deep linking** is mandatory for public channels with video
  ([deep linking](https://developer.roku.com/dev/docs/implementing-deep-linking)).

### 6.4 Arabic

**Roku states the platform does not support right-to-left languages**
([Roku partner support](https://partnersuccess.roku.com/hc/en-us/articles/360008136514)).
Developers report:
- reversing strings and splitting lines by hand;
- writing their own RTL lists;
- bundling fonts, since missing fonts show Arabic subtitles as boxes
  ([forum](https://forum.developer.roku.com/t/right-to-left-rtl-languages-support/7563)).

### 6.5 Certification and store

From Roku's criteria as of 2026-04
([certification](https://developer.roku.com/docs/developer-program/certification/certification.md)):
- **Performance:** home screen in ≤ 15 s, screens in ≤ 3 s, playback start in ≤ 8 s.
- **Deep linking** and voice direct-to-play.
- **Back** exits to the Roku home, with one confirmation allowed.
- **Instant Resume** only for US apps above 5 million streaming hours a month.
- **Ads.** RAF and app-ads.txt apply only to channels with ads.
- **Cost and timing.** Enrolment and publishing are free
  ([partner support](https://partnersuccess.roku.com/hc/en-us/articles/360005853933)). Review is
  about a week per round (Roku and vendors).
- **Beta channels.** 20 testers, 120 days (secondary sources).

### 6.6 Tooling

- **Official tools.** Roku's Automated Channel Testing is a WebDriver with Robot Framework and
  JavaScript libraries that drives a real device
  ([ACT](https://developer.roku.com/dev/docs/automated-channel-testing)).
- **No official emulator.** brs-engine is not Roku OS
  ([npm](https://npmjs.com/package/brs-engine)).

## 7. Market share

| Source, what it counts | Android/Google TV | Tizen | webOS | Roku | Fire TV | VIDAA | tvOS |
|---|---|---|---|---|---|---|---|
| TechInsights, global TV shipments Q4 2024 | > 24% | 16.9% | 11.8% | 9% | — | — | — |
| Omdia (via RBR), global 2025 | 32% (excl. China) | 17% | — | — | 4% | 6% | — |
| CTVMA, global installed 2024 | — | 12.9% | 7.4% | 6.4% | 6.4% | 7.8% | — |
| Parks Associates, US platforms in use, 2026-04 | — | 23% | — | 28% | — | — | — |
| Pixalate, North America ad traffic Q1 2026 | — | — | — | ~36% | (17% in Q2 2025) | — | — |

Sources:
- [TechInsights](https://techinsights.com/ja/node/59348)
- [RBR / Omdia](https://rbr.com/?p=299908)
- [CTVMA via digitaltvnews](https://digitaltvnews.net/?p=41935)
- [Parks via Advanced Television](https://www.advanced-television.com/2026/04/23/research-roku-samsung-dominate-ctv-platforms-in-us/)
- [Pixalate via The Desk](https://thedesk.net/2025/08/roku-samsung-ad-delivery-dominance-pixalate/)

Other figures:
- **Roku** reports over 90 million streaming households at the end of 2025, with international
  under 10% of revenue
  ([10-K](https://www.sec.gov/Archives/edgar/data/1428439/000162828026008114/roku-20251231.htm)).
- **Middle East and North Africa:** Tizen, webOS and Android TV together were about 55% of 55M+
  connected TV devices (TechInsights, Q2 2023, [link](https://techinsights.com/ko/node/53188)).
  Roku and Fire TV have little official presence there: Roku sells in the Americas and parts of
  Europe, and Amazon's Vega sticks ship to the US, Canada, Japan, Mexico, the UK, Germany, France,
  Italy, Spain, Switzerland and Australia.
- **Free country-level data** for Saudi Arabia or the UAE was not found.

## 8. VIDAA, Vizio, Xbox, and the no-app fallbacks

- **Hisense VIDAA.**
  - HTML5 web apps, through a partner portal that you apply to as a content partner
    ([VIDAA partners](https://www.vidaa.com/?p=4097)).
  - There is no self-service portal (vendor, [Muvi](https://help.muvi.com/?p=71740)), and whether
    individuals qualify is unconfirmed.
  - About 6–8% worldwide (Omdia, CTVMA) and about 14% in Europe (Omdia).
- **Vizio (Walmart; "VIZIO OS", called CastOS by Omdia).**
  - HTML5 apps, onboarded through Vizio's partner team and an App Integration Manager
    ([content partners](https://www.vizio.com/content-partners)). No path for small developers was
    found.
  - Chromecast is built in.
  - Omdia expects it to become #1 in North America by 2029 as Walmart's onn TVs move to it.
- **Xbox.**
  - Non-game apps still go through UWP and Partner Center, with extra Xbox review
    ([FAQ](https://learn.microsoft.com/windows/uwp/xbox-apps/frequently-asked-questions), 2026-01).
  - WebView2 is on Xbox for media apps
    ([announcement](https://blogs.windows.com/msedgedev/2023/11/01/webview2-for-xbox-announcement/)).
  - Apps get 1 GB of foreground memory
    ([resources](https://learn.microsoft.com/windows/uwp/xbox-apps/system-resource-allocation)).
  - The individual Partner Center fee was waived on 2025-09-10
    ([blog](https://blogs.windows.com/windowsdeveloper/2025/09/10/free-developer-registration-for-individual-developers-on-microsoft-store/)).
  - HEVC direct play in UWP clients is reported unreliable (Plex, Emby and Jellyfin forums,
    anecdotal).
  - There is no emulator.
- **AirPlay 2** is built into many Samsung, LG, Sony and Vizio TVs (2019+) and Roku OS 9.4+
  ([Apple](https://www.apple.com/airplay/)). The Apple Player's AirPlay already sends the signed HLS
  URL, which the TV fetches itself, so iPhone owners can watch on these TVs today with no TV app.
- **DLNA** on Samsung and LG plays MKV, MP4 and TS. It is LAN-only, with no HTTPS or signed URLs,
  weak subtitles and per-model behaviour. Epic 14 pointed at it; this epic keeps it as the last
  fallback and builds nothing for it.

## 9. One web codebase for Samsung and LG, and CI

- **Engines.** Both TVs run Chromium web apps on near-identical engines per year (M108 on both in
  2024, M120 on both in 2025).
- **Precedent.** jellyfin-tizen bundles jellyfin-web
  ([repo](https://github.com/jellyfin/jellyfin-tizen)); jellyfin-webos loads the server's UI.
- **The split** between the two is the player (AVPlay or `<video>`), the key codes and the
  packaging.
- **Focus libraries.** Norigin Spatial Navigation (TypeScript, React hooks) lists Tizen, webOS,
  VIDAA and Vizio ([repo](https://github.com/NoriginMedia/norigin-spatial-navigation)). Maktaba's
  `bigscreen/focus` already does this job and is kept.
- **CI.** No vendor documents a headless emulator for CI. The workable plan:
  - Playwright on Chromium for UI and key navigation, with mocked platform APIs;
  - the emulator and simulator as smoke tests where they run;
  - real TVs for playback.

## 10. Summary by platform

| | Fire TV (Fire OS) | Google Cast | Samsung Tizen | LG webOS | Roku | Vega OS |
|---|---|---|---|---|---|---|
| Stack | the Android TV app (Kotlin, Compose, Media3) | CAF web receiver + senders | packaged web app | packaged web app | BrightScript/SceneGraph | React Native for Vega |
| Player | Media3 | Shaka in CAF | AVPlay (MSE ≤ FHD) | native `<video>` HLS v7 | Video node | W3C media on GStreamer |
| HEVC / AV1 | by model / by model | fMP4 only / Streamer | yes / yes | yes / yes | yes / DASH only | yes / yes |
| Dolby Vision | by model | some models | **none** | unreliable in web apps | profile 5 | by model |
| Arabic/RTL | as Android TV | web, full | web, full; draw subtitles | web, full | **none: build-time and on-device shaping** | RN `I18nManager` |
| Emulator in CI | Android TV AVD (not Fire OS) | none | emulator, no AVPlay | simulator, no media | none (brs-engine) | VVD with KVM |
| Store review | ~3–10 days | none (console) | ~1–2 weeks per round | ~2–12 weeks | ~1 week per round | as Fire TV |
| Account cost | free | $5 once | free (US only without a partner contract) | free, individuals allowed | free | free |
| Reach | ~4–6% worldwide; US #2 stick | every Google TV and Cast TV | ~13–17% worldwide; US 23% | ~7–12% worldwide | US 28%; small elsewhere | new sticks from 2025 |
| Home reach | mDNS, cloud code | sender's address | cloud code, subnet probe | cloud code, subnet probe | cloud code, subnet probe | cloud code, subnet probe |
