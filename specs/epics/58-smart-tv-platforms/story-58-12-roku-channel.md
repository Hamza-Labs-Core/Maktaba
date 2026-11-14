# Story 58.12 — Roku: a SceneGraph channel with the TV layout

> Epic 58 · Phase 4 · Depends on: 58.01, 58.03, 58.15 · Needed by: 58.13, 58.14

## Why

Roku is the largest streaming platform in the US: 28% of connected-TV platforms in use (Parks
Associates, 2026), and over 90 million streaming households (research.md §7). Outside the
Americas it is small, and it has almost no official presence in the Middle East. Roku runs
neither web apps nor Android apps. A channel is BrightScript and SceneGraph XML, so nothing of
the web-TV codebase runs there except shared test vectors.

Roku also does not support right-to-left languages (research.md §6.4). Arabic needs work no other
platform needs (58.15). That cost, and Roku's thin presence where Arabic is spoken, is why Roku
comes after Samsung and LG.

## Description

**Stack.**
- BrightScript and SceneGraph, written in **BrighterScript**: a superset that compiles to
  BrightScript, with classes, imports and type checks.
- Built and side-loaded with `roku-deploy`.
- Unit tests with **Rooibos** on a device, and with **brs-engine** in Node on CI (58.16).
- The code lives at `apps/tv/roku`.

**Screens.** Epic 56's TV layout, as the Apple TV and Android TV apps have it, built from
SceneGraph components:

| Screen | Components |
|---|---|
| Choose a home, Sign in with a code, Find on this network | `HomeScene` with a `Label` code, a `Poster` QR (rendered by the home or cloud as PNG: `GET /v1/tv/codes/{code}/qr.png`), a `LabelList` of homes |
| Home | `RowList` of Continue Watching, Recently added, For you |
| A library | a tab bar (custom `Group` of `Label`s, focus-managed) over the five tabs; Play and Shuffle buttons; `PosterGrid` for the Library tab; the letter rail as a narrow `LabelList` |
| A series | backdrop `Poster`, buttons, season pills, episodes `MarkupGrid` |
| A collection, a playlist | header, Play, Shuffle, items |
| Search | Roku's `MiniKeyboard` (and voice through the remote's microphone, which types into it) |
| Video | poster, title, description, Resume from / Start over |
| Player | 58.13 |
| Account | who, which home; switch home, sign out; language English / العربية |

**Remote.**
- Up, Down, Left, Right, OK and Back, plus Play/Pause, Rewind and Fast-forward. Roku's remote has
  no menu key, so `*` (Options) opens a card's actions.
- Back follows Epic 56's three steps.
- On the Home screen, Back leaves the channel. Roku's certification allows one confirmation, and
  the channel asks once.

**Finding the home.** Roku has no mDNS API (research.md §6.3):
1. **Saved homes** (`roRegistrySection`).
2. **Sign in with a code** through Maktaba Cloud (58.03). This is the default.
3. **Find on this network**: the same /24 probe as 58.09, using `roUrlTransfer` asynchronously,
   32 at a time, on the address from `roDeviceInfo.GetIPAddrs()`, then the home's HTTPS name and
   the home's own code.
4. **A typed address**, on `MiniKeyboard`.

**HTTPS.**
- Every `roUrlTransfer` and the Video node's HTTP agent call
  `SetCertificatesFile("common:/certs/ca-bundle.crt")`. The homes' per-home certificates are
  publicly trusted (story 50.15), so the stock bundle is enough.
- The plain-HTTP LAN fallback (rebinding-blocking routers) is the same as 58.09's, with the same
  line on the Account screen.

**Tokens.**
- The access and refresh tokens from the pairing redeem are kept in `roRegistrySection`, which is
  per channel, encrypted at rest by Roku, and removed when the channel is removed.
- Every request carries the current token (`Authorization: Bearer`). A 401 takes one refresh at a
  time and one retry, the same rule as the Android core.

**Deep linking** (required by Roku for channels with video):
- `contentId` is a video ID and `mediaType` is `movie`, `episode`, `series` or `shortFormVideo`.
- A deep link to a video the signed-in person can see opens and plays it. Anything else opens the
  Home screen with a line saying the video is not available. That covers parental controls too
  (58.14), with the same 404-as-missing rule.

**Client header.** `tv/<version> (roku; tv)`.

**Performance** (Roku certification, research.md §6.5):
- Home screen fully drawn in under 15 seconds from launch.
- Under 3 seconds between screens.
- Playback start under 8 seconds (58.13).

Posters are requested at `?size=small`, and `RowList` loads rows lazily.

## Acceptance criteria

- **Given** a Roku Express 4K, **when** the person signs in with a cloud code, **then** Home is
  drawn within 15 seconds of the next launch.
- **Given** a library, **then** the five tabs, Play and Shuffle, the series page and the letter
  rail behave as Epic 56 §6.5 says, mapped to Roku's keys.
- **Given** a deep link to a video the person can watch, **then** it plays. To one they cannot,
  Home opens with the not-available line.
- **Given** Arabic, **then** every screen is mirrored and every Arabic string reads correctly
  (58.15's visual text).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | brs-engine (Node) unit tests in CI | API client, token refresh, probe, compat check (58.01 vectors), deep-link router | run | pass; brs-engine's stdout kept in the CI report |
| T02 | BrighterScript compile + `bslint` | the channel | build | no errors; the zip under Roku's size limit |
| T03 | brs-engine SceneGraph render (headless, its canvas) | Home, Library tabs, Series with a mock home | key sequences | focus moves as Epic 56 §6.5; screenshots en and ar, dark, kept as references with honest tolerance (brs-engine is not Roku's renderer) |
| T04 | The logic tests on a real Roku (device checklist, and the optional self-hosted runner of Q6) | the same BrightScript test files, side-loaded as one package (`tools/build.mjs --device-tests`) | run on device | every `RESULT` line has no failure; the debug console kept |
| T05 | Roku Automated Channel Testing (WebDriver) on a real Roku | sign in by code (review home), browse, deep link, Back out | scripted | pass; the device's debug console (port 8085) captured into the report |

**Honest note:**
- Roku has no official emulator (research.md §6.6).
- brs-engine runs BrightScript and much of SceneGraph in Node, which is enough for logic and for
  focus and layout checks. It is not Roku OS: rendering, fonts, performance and the Video node
  differ.
- T04 and T05 need a real Roku on a network the runner reaches. Without the self-hosted device
  runner (Q6), they run by hand from the checklist before each submission.
- brs-node draws text with a canvas library that runs the bidi algorithm, which Roku never does.
  The screen tests preload a shim that turns it off, so Arabic is drawn in stored order as on a
  Roku.
- The probe of Find on this network runs in CI on Linux only: brs-engine's HTTP is synchronous,
  and only Linux refuses the other 127.0.0.x addresses at once.
- brs-engine fetches posters without the scene's bearer, and does not keep a RowList's unfocused
  rows at their own position. Both are real-device checklist rows.

## Strings

Roku's catalogs are `apps/tv/roku/locale/{en_US,ar_SA}/translations.ts`, in Roku's own format.
They are generated from one shared source, `apps/tv/roku/i18n/{en,ar}.json`, by the 58.15 build
step, which also shapes the Arabic. Keys are 58.01's `compat.*`, 58.09's `launcher.*` (the same
words) and:

| Key | English | Arabic |
|---|---|---|
| `roku.notAvailable` | That video isn't available. | هذا الفيديو غير متاح. |
| `roku.exitConfirm` | Leave Maktaba? | الخروج من Maktaba؟ |
| `roku.options` | Press * for more | اضغط * للمزيد |

## Edge cases

- **Roku's channel memory limit on low-end models**: the grid keeps at most 3 pages of posters
  loaded, and the brs-engine tests assert the node count stays bounded.
- **A Roku TV in a hotel** (no LAN home, captive portal): the cloud code works once the portal is
  passed. Before that, the channel says it has no internet.

## Build plan

| File | Change |
|---|---|
| `apps/tv/roku/` (new) | `manifest`, `source/`, `components/`, `i18n/`, `bsconfig.json`, `tools/build.mjs`, the tests with their mock home and mock cloud |
| `.github/workflows/_roku.yml` | T01–T03, the screenshots kept with the logs |
| `docs/stores/roku.md` | what the owner does (account, key, package, dashboard), the build, the real-device checklist |
| `web/packages/devportal/scripts/apps.mjs` | Roku on the TV page, with screenshots |
| `cloud/internal/handlers/tvcodes/` | `qr.png` |
