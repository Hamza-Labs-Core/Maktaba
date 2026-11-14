# LG (webOS)

The LG launcher, an `.ipk` that hosts the home's `/tv/` UI (story 58.11). It runs on webOS 5
and later (2020 TVs on). A webOS 4.x TV is told it is too old and pointed to Cast and AirPlay
(58.09). LG is the first TV store Maktaba submits to: an Individual Seller account needs no
partner contract, and the listing covers every country the LG Content Store serves (Epic 58
README, Q4).

## What is submitted

| Item | Where it comes from |
|---|---|
| The package, `maktaba-webos-<version>.ipk` | the release workflow on a `v*` tag whose launcher changed (`tv-web-release.yml`), or `node apps/tv/web/webos/package.mjs --version X.Y.Z` after `pnpm build:launcher` |
| App ID | `com.hamzalabs.maktaba.tv` (`apps/tv/web/webos/appinfo.json`) |
| Version | the tag's `X.Y.Z`; every upload needs a version higher than the last |
| Signing | none here: LG signs the package when it is submitted, and no key is held |
| Icons | 80×80 and 130×130 in the package; the store images below are uploaded in Seller Lounge |
| The UX scenario document and the self-checklist | the sections below |
| The reviewer's sign-in | the review home (story 58.04); its submission notes name the secret, never the password |

`appinfo.json` declares `requiredACG: ["network.query", "systemconfig.query"]`. These are the
two luna groups the launcher calls: the connection manager's address for Find on this network,
and the TV's model, SDK version and UHD panel. webOS 26 can enforce ACG and webOS 27 requires
it. The package lint (`apps/tv/web/webos/lint-ipk.mjs`) fails a package whose list differs.

## Listing

### English

- **Name:** Maktaba
- **Category:** Entertainment (video)
- **Short description:** Watch your own Maktaba home's library on your LG TV.
- **Description:**

  Maktaba is a home media library you run yourself. This app puts your home's library on your
  LG TV: lessons, films, series and live channels, with your progress kept across your phone,
  computer and TV.

  - Sign in with a code. The TV shows a short code; approve it in Maktaba on your phone or
    computer. There is no password to type with the remote.
  - Or find your home on this network: the app looks for it on your Wi-Fi.
  - Plays on the TV's own player: 4K, HDR10 and Dolby audio where your TV supports them. Files
    your TV cannot play are converted by your home.
  - Subtitles drawn by Maktaba, Arabic included, right to left.
  - The whole app in English and Arabic.
  - Your home's rules apply: each person sees only the libraries and ratings their home allows.

  Maktaba needs a Maktaba home (maktaba.tv). The app sells nothing and has no purchases.

- **Keywords:** media library, home server, lessons, Arabic, subtitles, streaming

### العربية

- **الاسم:** مكتبة
- **الفئة:** الترفيه (فيديو)
- **وصف قصير:** شاهد مكتبة منزلك في مكتبة على تلفاز LG.
- **الوصف:**

  مكتبة هي مكتبة وسائط منزلية تديرها بنفسك. يضع هذا التطبيق مكتبة منزلك على تلفاز LG:
  الدروس والأفلام والمسلسلات والقنوات المباشرة، ويحفظ تقدّمك بين هاتفك وحاسوبك وتلفازك.

  - سجّل الدخول برمز: يعرض التلفاز رمزًا قصيرًا، فتوافق عليه في مكتبة على هاتفك أو
    حاسوبك، دون كتابة كلمة مرور بجهاز التحكم.
  - أو ابحث عن منزلك في هذه الشبكة: يبحث التطبيق عنه في شبكة Wi-Fi لديك.
  - يعرض الفيديو بمشغّل التلفاز نفسه: دقة 4K وHDR10 وصوت Dolby حيث يدعمها تلفازك، ويحوّل منزلك
    الملفات التي لا يستطيع تلفازك تشغيلها.
  - ترجمات ترسمها مكتبة بنفسها، والعربية منها من اليمين إلى اليسار.
  - التطبيق كله بالعربية والإنجليزية.
  - تسري قواعد منزلك: لا يرى كل شخص إلا المكتبات والتصنيفات التي يسمح بها منزله.

  يحتاج مكتبة إلى منزل مكتبة (maktaba.tv). لا يبيع التطبيق شيئًا ولا يتضمن أي مشتريات.

- **الكلمات المفتاحية:** مكتبة وسائط، خادم منزلي، دروس، العربية، ترجمة، بث

### Store images

Screenshots come from the launcher's Playwright run (`_tv-web.yml`, `web/test-results/launcher/shots`):
dark, 1920×1080, English and Arabic, on test data. The set is `webos-<lang>-start`, `-code`,
`-home`, `-watch` and `-subtitles`. Seller Lounge asks for the sizes it lists at upload time.
They are cut from those screenshots and from `tools/icons/source/maktaba-icon.png`
(`tools/icons/make-icons.py` writes the package icons).

## UX scenario document

LG asks for the app's flow as a document with the submission. This is its text; the
screenshots above illustrate each step.

1. **Launch.** A splash, then the start screen: Sign in with a code (focused on a new TV), Find on
   this network, and More ways (type the home's address). A TV that used a home before opens
   that home at once.
2. **Sign in with a code.** The screen shows the code large, a QR code, how long it lasts, and
   where to approve it. Once it is approved on a phone, the home's UI loads full screen.
3. **Find on this network.** The app lists the homes on the TV's network by name. Choosing one
   shows that home's own code to approve.
4. **Home.** Rows of the home's lessons, films, series and live channels, moved with the arrows.
   The Magic Remote's pointer focuses what it hovers.
5. **Playing.** OK on an item plays it full screen. The controls show on any key press and hide
   after 4 seconds. Left and Right seek, OK pauses, Subtitles and Audio open their panels.
   The remote's media keys work.
6. **Back.** Back closes a panel, then leaves the player, then goes up a screen. On Home it asks
   "Leave Maktaba?"; Leave returns to the webOS launcher.
7. **Language.** Settings → Language switches every screen to Arabic, mirrored right to left.
8. **No home, or a home too old.** The app says so and offers the way out: try a code, update the
   home, or update the app. It never shows a broken screen.

## Self-checklist (before each upload)

- [ ] The `.ipk` comes from the release workflow for the tag, and its package lint passed.
- [ ] The real-device checklist below has a passing result for this version.
- [ ] The review home is up and on the released version, and its submission notes (58.04) are
      filled in by the release session.
- [ ] The listing text above, in both languages, matches what the app does now.
- [ ] Nothing in the app sells anything or links to a purchase.
- [ ] Back from Home shows the confirm, and Leave exits to the webOS launcher.
- [ ] The app works with no network on first launch: it says so and offers a retry.

## Submitting (by a person)

1. The release candidate's checklist below has a passing result for its version
   (`tools/release/tv_checklist_guard.py --platform lg --version <X.Y.Z>`); without one the
   submission stops, with the reason the guard prints.
2. In LG Seller Lounge, add a new app or a new version of `com.hamzalabs.maktaba.tv`.
3. Upload the `.ipk`, the store images, the listing in English and Arabic, the UX scenario
   document, the self-checklist, and the review home's sign-in (from its secret).
4. Countries: every country the LG Content Store serves.
5. LG's QA runs in stages: pretest, function and content, with LG's App CTS suite. Every update is
   QA'd again. LG publishes no duration; reports run from 2 to 12 weeks, so the first submission
   is planned for the long end.

There is no public upload API, so this step is manual.

## Submission notes

The store's reviewers sign in to the project's review home, review.maktaba.tv (story 58.04).
The notes, what reviewers get and how a submission round goes are in
[review-home.md](review-home.md): its text goes in LG Seller Lounge's notes for the QA stage, beside the UX scenario document. The password is never written
here: the notes name the release secret `REVIEW_HOME_VIEWER_PASSWORD` (and, for the parental
checks, `REVIEW_HOME_CHILD_PASSWORD`), and the release session fills each in when it submits.
After the round, the release checklist rotates both ([review-home.md](review-home.md#a-submission-round)).

## Real-device checklist

Run before each launcher release candidate is submitted in LG's Seller Lounge (58.11 T05). The
rule and the result line are in [README.md](README.md).

**What CI covers instead:**
- Vitest on the webOS platform, with a fake `<video>` and fake luna: the device caps against
  58.02's vectors, the player's calls, subtitles and Back.
- The package lint on the `.ipk` that `ares-package` built.
- Playwright playing a video from `/tv/` through the launcher's `<video>`, with a webOS shim.

Playwright's Chromium is the version Playwright pins, not the TV's M120 engine. It decodes no
H.264, so the CI flow plays a VP9 file: it proves the messages, the transcode retry and the
playback path, not the TV's decoders. LG's webOS TV
Simulator is not run in CI: its download sits behind LG's license page, with no address a runner
can fetch, and headless runs are not documented. It also has no media pipeline (research.md
§4.3). **Only a device shows:** native HLS, HDR, Dolby Vision per model year, passthrough, the
Magic Remote and performance.

### Devices

- a webOS 5 TV (2020, the oldest supported)
- a webOS 6 TV (2021)
- a current webOS TV (webOS 25, or the newest on sale)
- for check 11 only: a webOS 4.5 TV (2019)

Put each TV in Developer Mode and install the candidate with `ares-install` (`ares-setup-device`,
then `ares-install --device <tv> maktaba-webos-<version>.ipk`). **First, Extend** the session in
the Developer Mode app: an expired session removes the test app in the middle of a run. A run
that hits an expiry restarts at Extend and is not recorded as a failure. Note the session's end
date with the result. Capture `ares-log` for the whole run. The launcher's own log is also under
Help → Send a report.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | Launch, sign in by the cloud code, and find a home on the network | both paths reach Home; `ares-log` shows `webos: sdk …` with the TV's real SDK version and UHD |
| 2 | Play a 4K HEVC HDR10 MKV (direct play) | the TV's `<video>` plays it in HDR at 4K (the TV's info overlay says HDR) |
| 3 | An HEVC remux (HLS with fMP4 segments) | plays, seeks, and survives a quality switch |
| 4 | `EXT-X-INDEPENDENT-SEGMENTS` behaviour | webOS ignores the tag: seeking lands on the right segment with no stall. If it refuses the stream, record that, and the home omits the tag for the `webos` profile |
| 5 | Dolby Vision profile 8, on each model year | DV where the year is in `DV_GOOD_YEARS` (`apps/tv/web/src/platform/webos-caps.ts`), HDR10 otherwise; a year that plays DV well is added there |
| 6 | EAC3 audio on a receiver or soundbar | passthrough |
| 7 | Arabic subtitles, then the subtitle size | drawn by Maktaba's layer, joined, right to left, above the controls; each size changes them |
| 8 | Two audio tracks | the Audio panel lists both and switches between them |
| 9 | Switch to Arabic | every screen mirrors; Left and Right swap in the focus order |
| 10 | The Magic Remote: pointer, wheel, arrows, OK, Back, the media and colour keys | the pointer focuses what it hovers; Back from Home shows the confirm, and Leave returns to the webOS launcher |
| 11 | Launch on the webOS 4.5 TV | the launcher says the TV is too old and names Cast and AirPlay |
| 12 | The performance budget (58.09 and 58.11) on the webOS 5 TV | launch to Home and Home to playback within the budget |

## Results

No result yet (the first release candidate records the first line).
