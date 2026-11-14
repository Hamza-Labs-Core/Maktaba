# Samsung (Tizen)

The Samsung launcher, a `.wgt` hosting the home's `/tv/` UI (story 58.10). Its source is
[`apps/tv/web/`](../../apps/tv/web) (shared with LG); the Samsung-only parts are
`src/platform/tizen.ts` and `src/platform/tizen/` (AVPlay, the device caps) and
[`apps/tv/web/tizen/`](../../apps/tv/web/tizen) (the package). Story 58.04 adds the review home's
notes here.

Supported TVs: 2020 models and later (Tizen 5.5 and up, Chromium 69 and up). An older TV is told
it is too old and pointed to Google Cast and AirPlay (owner question Q10).

## Store listing

Seller Office takes the listing in each language the app is offered in. The app is free: nothing
is sold in it, and it has no purchase links and no store billing (Epic 58 decision 12).

**Name:** Maktaba · مكتبة

**Short description** (English):

> Watch your own Maktaba home's library on your Samsung TV.

**Short description** (Arabic):

> شاهد مكتبة جهازك الرئيسي في مكتبة على تلفاز سامسونج.

**Description** (English):

> Maktaba is your household's own video library: films, series, lessons and Live TV, kept on a
> Maktaba home you run. This app plays that library on your Samsung TV.
>
> - Find your home on this network, or sign in with a code you approve from your phone, with no
>   password typed on the remote.
> - Plays in 4K and HDR10 where your TV can, with Dolby Digital and Dolby Digital Plus passed
>   through to your soundbar or receiver.
> - Arabic and English, with right-to-left screens and subtitles drawn properly in Arabic.
> - Each person in the household sees only what their account allows, and the home's parental
>   filter applies on the TV.
>
> You need a Maktaba home to use this app. The app has no content of its own.

**Description** (Arabic):

> مكتبة هي مكتبة الفيديو الخاصة بأسرتك: أفلام ومسلسلات ودروس وقنوات بث مباشر، محفوظة على جهاز
> مكتبة رئيسي تديره بنفسك. وهذا التطبيق يشغّل تلك المكتبة على تلفاز سامسونج.
>
> - اعثر على جهازك الرئيسي في هذه الشبكة، أو سجّل الدخول برمز توافق عليه من هاتفك، دون كتابة
>   كلمة مرور بجهاز التحكم.
> - تشغيل بدقة 4K وHDR10 حيث يدعمها تلفازك، مع تمرير Dolby Digital وDolby Digital Plus إلى مكبّر
>   الصوت أو جهاز الاستقبال.
> - بالعربية والإنجليزية، بشاشات من اليمين إلى اليسار وترجمة تُرسم بالعربية كما ينبغي.
> - يرى كل فرد في الأسرة ما يسمح به حسابه فقط، ويسري مرشّح المحتوى العائلي للجهاز الرئيسي على
>   التلفاز.
>
> تحتاج إلى جهاز مكتبة رئيسي لاستخدام هذا التطبيق. لا يحتوي التطبيق على محتوى خاص به.

**Category:** Video. **Age rating:** answered as a media player whose content the user supplies;
the app itself is not 18+. Samsung does not allow 18+ apps in Korea, Saudi Arabia or the UAE
(research.md §3.4). The submission describes the home's parental filter (story 58.14).

**Screenshots:** 1920×1080, dark, English and Arabic, from the review home (story 58.04): Home,
a series, the player with Arabic subtitles, the cloud code screen.

**Icon:** `apps/tv/web/tizen/icon.png` (512×423), cut from the icon master by
`tools/icons/make-icons.py`.

## Seller account and countries

- **Public Seller first, in the US only.** A new Seller Office account is a Public Seller,
  which can launch only in the United States. The first release goes there.
- **Partner Seller for every other country, the Gulf included.** That status takes a contract
  with Samsung (research.md §3.4). Owner question Q2's default is to apply for it at the same
  time as the first US submission. Until it is granted, the listing covers the US only (Q4).
- Once Partner status is granted, the same package is offered in the further countries; no
  build changes.

## The package

Built on a `v*` tag whose launcher changed, by
[`tv-web-release.yml`](../../.github/workflows/tv-web-release.yml):

1. `pnpm build:launcher` (web/), checked for Chromium 68.
2. [`package_wgt.py`](../../apps/tv/web/tizen/package_wgt.py): the stage (config.xml with the
   tag's version, the icon, the build; index.html loads Samsung's `webapis.js`), and an unsigned
   `.wgt`.
3. [`sign_wgt.sh`](../../apps/tv/web/tizen/sign_wgt.sh): Tizen Studio's CLI (pinned by
   sha256) signs the stage with the project's Samsung **author** and **distributor**
   certificates.
4. [`lint_wgt.py`](../../apps/tv/web/tizen/lint_wgt.py) `--signed`: config.xml's privileges,
   CSP and access exactly as listed, the files, and both signatures.
5. `maktaba-tizen-<version>.wgt` and its `.sha256` are attached to the tag's release.

The certificates live only in the release secrets, never in the repository:
`TIZEN_AUTHOR_P12_BASE64`, `TIZEN_AUTHOR_PASSWORD`, `TIZEN_DISTRIBUTOR_P12_BASE64`,
`TIZEN_DISTRIBUTOR_PASSWORD`. Until they are set, the tag's run signs with a throwaway
certificate to prove the path, keeps that package as a workflow artifact
(`maktaba-tizen-<version>-rehearsal.wgt`) and attaches nothing.

**Making the certificates** (the owner, once): in Tizen Studio's Certificate Manager with the
Samsung TV extension, create a **Samsung** certificate profile signed in to the project's Samsung
account: an author certificate and a distributor certificate (privilege level Public). Back up the
`.p12` files and passwords outside the repository; a new author certificate cannot update an app
already in the store. To install on a test TV, add the TV's DUID to the distributor certificate.

**Every pull request** builds the unsigned `.wgt` and lints it (`_tv-web.yml`, its Samsung
block), and runs Playwright on the launcher with a `webapis`/AVPlay shim.

## Submission (Seller Office)

Seller Office has no upload API: a person submits.

1. The release candidate's checklist below has a passing result for its version
   (`tools/release/tv_checklist_guard.py --platform samsung --version <X.Y.Z>`).
2. Upload `maktaba-tizen-<version>.wgt` from the tag's release in Seller Office, with the listing
   above, the screenshots, and the review home's address and code (story 58.04) in the notes for
   the reviewer.
3. Samsung reviews it, runs its verification test against the review home, then launches it.
   Every update is reviewed again, with a phased rollout (3%, 10%, 100%).
4. The launcher changes rarely: the UI is the home's own `/tv/` and updates with the home, so
   store updates are a few a year.

## Real-device checklist

Run before each launcher release candidate is uploaded to Seller Office (58.10 T05). The rule
and the result line are in [README.md](README.md).

**What CI covers instead:** the AVPlay adapter against a fake `webapis.avplay` (Vitest: the call
sequence, tracks, subtitles, seek, errors), the device caps against the Samsung vectors of
`shared/playback/caps-vectors.json`, the launcher in Playwright with a `webapis`/AVPlay shim,
the package build and its lint, and signing (rehearsed on each tag). **Not in CI:** the Samsung
TV emulator. It needs Tizen Studio's TV extension and a KVM runner, has no documented headless
mode, and has no AVPlay, no UHD and no secure WebSockets anyway (research.md §3.3), so it would
show less than the shim does. **Only a device shows:** AVPlay playback, HDR, passthrough, the
keys on the real remote, the packaged page framing the home's `/tv/`, performance.

### Devices

- a 2020 Samsung TV (Tizen 5.5, the oldest supported)
- a 2022 Samsung TV
- a 2025 Samsung TV

Each in Developer Mode (Apps, then 1 2 3 4 5 on the remote, the computer's address as the Host
PC IP), the candidate installed with `sdb` (`sdb connect <tv-ip>`, then
`tizen install -n <file>.wgt -t <device>`). The package must be signed with a distributor
certificate that lists the TV's DUID. Capture `sdb dlog` for the whole run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | Launch, sign in by the cloud code, and find a home on the network | both paths reach Home |
| 2 | The packaged page frames the home's `/tv/`, over HTTPS and over the LAN's HTTP | `/tv/` loads in the frame (the home's `frame-ancestors` accepts the package's origin); the bridge's hello is answered |
| 3 | Play a 4K HEVC HDR10 MKV with EAC3 (2022 and 2025) | AVPlay plays it in direct play, in HDR at 4K |
| 4 | Play a Dolby Vision profile 8.1 file | plays as HDR10 (a remux, not a transcode) |
| 5 | EAC3 audio on a receiver or soundbar that takes it; then one that takes only Dolby Digital | passthrough on the first; on the second the TV's output mode drops EAC3 and the home converts it |
| 6 | Arabic subtitles, sidecar and embedded, at each subtitle size | drawn by Maktaba's layer, joined, right to left, at the chosen size |
| 7 | Switch audio track during playback | the chosen language plays; the Audio panel lists the tracks |
| 8 | Seek, pause, resume; a file AVPlay cannot play | each works; the unplayable file shows the notice and plays converted |
| 9 | Switch to Arabic | every screen mirrors, Left and Right swap in the focus order |
| 10 | The remote's keys: arrows, Enter, Back, play/pause, rewind, fast-forward, the colour keys | each registered key works; Back on the top screen asks first, then leaves the app |
| 11 | Leave with the app switcher mid-video, come back; sleep the TV mid-video, wake it | AVPlay is suspended and restored; the launcher resumes, still signed in |
| 12 | The device caps the TV sends (the launcher's log, `tizen caps:`) | the codecs, HDR and passthrough match the model; no Dolby Vision |
| 13 | The performance budget (58.09 and 58.10) on the 2020 TV | launch to Home and Home to playback within the budget |

## Results

No result yet (the first release candidate records the first line).
